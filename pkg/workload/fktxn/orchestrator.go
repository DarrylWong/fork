// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"context"
	gosql "database/sql"
	"fmt"
	"math/rand"
	mathrandv2 "math/rand/v2"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
	"github.com/cockroachdb/cockroach/pkg/workload"
	"github.com/cockroachdb/cockroach/pkg/workload/histogram"
	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5/stdlib"
)

// orchestratorConfig is the static run-time configuration assembled by Ops()
// and handed to the orchestrator. It owns the connection details, worker
// shape, and op mix.
type orchestratorConfig struct {
	URLs               []string
	ConnFlags          *workload.ConnFlags
	Workers            int
	MinChainLen        int
	MaxChainLen        int
	SubDAGRotateChains int
	Mix                OpMix
	TolerateSrcErrors  bool
	Seed               int64
}

// orchestrator owns the shared sub-DAG and PK pool that all workers operate
// on. It also owns the rotation state: workers consult the orchestrator
// before each chain to learn the current sub-DAG generation, and a swap
// happens out-of-band when the chain count crosses the rotation threshold.
//
// Lifecycle:
//   - newOrchestrator opens a pgx pool, discovers the schema, builds the
//     initial sub-DAG/pool, and returns one WorkerFn per worker.
//   - Each WorkerFn runs one chain per call (the workload framework loops).
//   - On chain rotation, the next worker to enter the rotation block swaps
//     the shared state under a write lock; concurrent workers wait via the
//     rwmutex so they pick up the new state on the very next chain.
//   - Close() shuts down the connection pool.
type orchestrator struct {
	cfg orchestratorConfig
	mcp *workload.MultiConnPool
	// db is a *sql.DB view of mcp used for schema discovery and for the
	// transaction handles workers acquire (each worker uses BeginTx on db
	// so per-worker transactions are independent).
	db     *gosql.DB
	dbName string
	hists  *histogram.Histograms

	// graphs is the full set of FK connected components discovered from the
	// live schema. Sub-DAG rotation re-picks one of these.
	graphs []*FKGraph

	// chainsCompleted counts every chain across all workers; the rotation
	// threshold compares against this counter.
	chainsCompleted atomic.Uint64

	// stateMu guards both state and orchRNG. State is read under a read lock
	// (workers do this once per chain via snapshotState); rotation upgrades
	// to the write lock to swap state and to advance orchRNG.
	stateMu sync.RWMutex
	state   *sharedState
	// orchRNG drives sub-DAG and pool re-rolls. It is only touched under
	// stateMu's write lock — never by workers directly.
	orchRNG *mathrandv2.Rand
}

// sharedState is the per-rotation snapshot every worker drives against. The
// orchestrator builds one in newOrchestrator and replaces it on rotation.
type sharedState struct {
	// generation distinguishes one rotation from the next. Workers don't use
	// it directly today, but recording it makes contention diagnostics
	// cheaper if we add per-rotation metrics later.
	generation uint64
	sorted     []*Table
	sub        *FKGraph
	dropped    []FKEdge
}

// newOrchestrator wires up the connection pool, discovers the schema, builds
// the initial sub-DAG and pool, and produces a QueryLoad with one worker
// function per --workers. Each worker function runs exactly one chain per
// invocation; the workload framework drives the loop.
func newOrchestrator(
	ctx context.Context, cfg orchestratorConfig, reg *histogram.Registry,
) (workload.QueryLoad, error) {
	poolCfg := workload.NewMultiConnPoolCfgFromFlags(cfg.ConnFlags)
	// Each worker opens transactions concurrently; cap connections at
	// workers + 1 to leave one connection for orchestrator-level work
	// (schema re-discovery, future health checks).
	poolCfg.MaxTotalConnections = cfg.Workers + 1
	mcp, err := workload.NewMultiConnPool(ctx, poolCfg, cfg.URLs...)
	if err != nil {
		return workload.QueryLoad{}, err
	}

	db := stdlib.OpenDBFromPool(mcp.Get())

	dbName, err := currentDatabase(ctx, db)
	if err != nil {
		mcp.Close()
		return workload.QueryLoad{}, errors.Wrap(err, "resolving current database")
	}

	schema, err := DiscoverSchema(db, dbName)
	if err != nil {
		mcp.Close()
		return workload.QueryLoad{}, errors.Wrap(err, "discovering schema")
	}
	graphs := BuildFKGraphs(schema)
	if len(graphs) == 0 {
		mcp.Close()
		return workload.QueryLoad{}, errors.Newf(
			"no FK graphs discovered in database %q; fktxn needs at least one FK constraint",
			dbName,
		)
	}

	o := &orchestrator{
		cfg:    cfg,
		mcp:    mcp,
		db:     db,
		dbName: dbName,
		hists:  reg.GetHandle(),
		graphs: graphs,
		// Seed the orchestrator's RNG separately from worker RNGs so re-
		// rolling the sub-DAG produces independent shapes from worker op
		// selection.
		orchRNG: mathrandv2.New(mathrandv2.NewPCG(uint64(cfg.Seed), 0)),
	}

	state, err := o.buildState(1)
	if err != nil {
		mcp.Close()
		return workload.QueryLoad{}, errors.Wrap(err, "building initial sub-DAG")
	}
	o.state = state
	logSubDAG(ctx, state)

	workerFns := make([]func(context.Context) error, cfg.Workers)
	for i := range workerFns {
		// Per-worker RNG: stable across the run (so the same seed reproduces
		// the same op sequence per worker), independent across workers.
		rng := rand.New(rand.NewSource(cfg.Seed + int64(i) + 1))
		workerFns[i] = o.makeWorkerFn(rng)
	}

	return workload.QueryLoad{
		WorkerFns: workerFns,
		Close: func(_ context.Context) error {
			mcp.Close()
			return db.Close()
		},
	}, nil
}

// makeWorkerFn returns a function the workload framework calls once per
// chain. Each call snapshots the shared state, runs one chain against it,
// records latency and outcome, and (if the rotation threshold is crossed)
// swaps the shared state for the next chain.
func (o *orchestrator) makeWorkerFn(rng *rand.Rand) func(context.Context) error {
	return func(ctx context.Context) error {
		state := o.snapshotState()
		w := NewWorker(WorkerConfig{
			DB:                o.db,
			Sorted:            state.sorted,
			Sub:               state.sub,
			Dropped:           state.dropped,
			Mix:               o.cfg.Mix,
			MinChainLen:       o.cfg.MinChainLen,
			MaxChainLen:       o.cfg.MaxChainLen,
			TolerateSrcErrors: o.cfg.TolerateSrcErrors,
		}, rng)

		start := timeutil.Now()
		res, err := w.Run(ctx)
		elapsed := timeutil.Since(start)
		o.hists.Get(`chain`).Record(elapsed)
		// Record one observation per committed txn so the workload's ops/sec
		// reflects committed transactions rather than chain attempts (which
		// includes chains that died on the first txn). Use the chain's mean
		// per-txn latency since we don't track per-event latency separately.
		if res.Committed > 0 {
			perTxn := elapsed / time.Duration(res.Committed)
			for i := 0; i < res.Committed; i++ {
				o.hists.Get(`committed_txn`).Record(perTxn)
			}
		}
		// Record one observation per pivot attempt / success. The latency is
		// the chain's elapsed time — pivot work isn't separately timed, but
		// using the chain elapsed keeps the histograms comparable to the
		// `chain` metric and the rate column (ops/sec) is what matters for
		// pivot-effectiveness analysis.
		for i := 0; i < res.PivotAttempted; i++ {
			o.hists.Get(`pivot_attempted`).Record(elapsed)
		}
		for i := 0; i < res.PivotSucceeded; i++ {
			o.hists.Get(`pivot_succeeded`).Record(elapsed)
		}
		if class := res.FailureClass(); class != "" {
			o.hists.Get(class).Record(elapsed)
		}
		if err != nil {
			return err
		}

		count := o.chainsCompleted.Add(1)
		o.maybeRotate(ctx, count)
		return nil
	}
}

// snapshotState returns a stable pointer to the current sharedState.
// Workers hold no reference to o.state directly so a swap during rotation
// doesn't tear in-flight chains.
func (o *orchestrator) snapshotState() *sharedState {
	o.stateMu.RLock()
	defer o.stateMu.RUnlock()
	return o.state
}

// maybeRotate replaces the sub-DAG and pool when the chain count crosses the
// rotation threshold. The first worker to observe a threshold crossing
// performs the swap; concurrent observers re-check under the write lock and
// no-op if the swap already happened.
func (o *orchestrator) maybeRotate(ctx context.Context, count uint64) {
	threshold := uint64(o.cfg.SubDAGRotateChains)
	if threshold == 0 {
		return
	}
	wantGen := count/threshold + 1
	o.stateMu.Lock()
	defer o.stateMu.Unlock()
	// Re-check under the write lock: another worker may have already rotated
	// past this generation while we waited for the lock.
	if o.state.generation >= wantGen {
		return
	}
	state, err := o.buildState(wantGen)
	if err != nil {
		log.Dev.Warningf(ctx, "fktxn: sub-DAG rotation failed: %v", err)
		return
	}
	o.state = state
	logSubDAG(ctx, state)
}

// logSubDAG emits one info line summarizing a sub-DAG selection. Used to
// diagnose post-rotation behavior (e.g. when a rotation collapses the sub-DAG
// down to a single table and the workload starts hitting unique violations
// that look like an FK ordering bug).
func logSubDAG(ctx context.Context, state *sharedState) {
	tables := make([]string, 0, len(state.sub.Tables))
	for name := range state.sub.Tables {
		tables = append(tables, name)
	}
	sort.Strings(tables)
	edges := make([]string, 0, len(state.sub.Edges))
	for _, e := range state.sub.Edges {
		edges = append(edges, fmt.Sprintf("%s->%s", e.ReferencingTable, e.ReferencedTable))
	}
	sort.Strings(edges)
	dropped := make([]string, 0, len(state.dropped))
	for _, e := range state.dropped {
		dropped = append(dropped, fmt.Sprintf("%s->%s", e.ReferencingTable, e.ReferencedTable))
	}
	sort.Strings(dropped)
	log.Dev.Infof(ctx,
		"fktxn: sub-DAG generation=%d tables=[%s] edges=[%s] dropped=[%s]",
		state.generation,
		strings.Join(tables, ","),
		strings.Join(edges, ","),
		strings.Join(dropped, ","),
	)
}

// buildState picks a random FK graph and derives a random sub-DAG. Caller
// must hold stateMu's write lock — buildState reads from o.orchRNG.
func (o *orchestrator) buildState(generation uint64) (*sharedState, error) {
	graph := o.graphs[o.orchRNG.IntN(len(o.graphs))]
	sorted, sub, dropped, err := RandomSubDAG(o.orchRNG, graph)
	if err != nil {
		return nil, errors.Wrap(err, "selecting sub-DAG")
	}
	return &sharedState{
		generation: generation,
		sorted:     sorted,
		sub:        sub,
		dropped:    dropped,
	}, nil
}

// currentDatabase returns the connection's current database name.
func currentDatabase(ctx context.Context, db *gosql.DB) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var name string
	if err := db.QueryRowContext(queryCtx, "SELECT current_database()").Scan(&name); err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("current_database() returned empty string")
	}
	return name, nil
}
