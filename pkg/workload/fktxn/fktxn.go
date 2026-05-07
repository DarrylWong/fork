// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"context"
	gosql "database/sql"
	"sync"

	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/workload"
	"github.com/cockroachdb/cockroach/pkg/workload/histogram"
	"github.com/cockroachdb/errors"
	"github.com/spf13/pflag"
)

// RandomSeed seeds the workload's RNGs (orchestrator sub-DAG selection and
// per-worker op selection). Reproducible runs use the same seed plus the
// same target schema.
var RandomSeed = workload.NewInt64RandomSeed()

// fkConflict is the workload generator. The schema is discovered from the
// live database in Ops, not produced here — the caller owns initial DDL and
// data load. This decoupling lets the same workload run against random
// schemas, hand-crafted schemas, or production debug-zip extracts.
type fkConflict struct {
	flags     workload.Flags
	connFlags *workload.ConnFlags

	numTables         int
	fkDensity         float64
	requireMinFKs     int
	workers           int
	minChainLen       int
	maxChainLen       int
	opsPerRotation    int
	updatePct         int
	deletePct         int
	tolerateSrcErrors bool
	pkPoolSize        int

	// schemaOnce guards lazy schema generation. Tables() and Hooks().PostLoad
	// both consume the result and may be called in either order; generating
	// once guarantees they agree on table names and FK statements.
	schemaOnce sync.Once
	tables     []workload.Table
	fkStmts    []tree.Statement
	// schemaErr captures the failure (if any) from the retry loop in
	// ensureSchema. Tables() can't return an error, so we surface it through
	// PostLoad and Ops instead.
	schemaErr error
}

func init() {
	workload.Register(fkConflictMeta)
}

var fkConflictMeta = workload.Meta{
	Name: `fktxn`,
	Description: `FK-txn generates concurrent transactions across foreign-key-` +
		`related tables to stress LDR replication of FK constraints.`,
	Version:    `1.0.0`,
	RandomSeed: RandomSeed,
	New: func() workload.Generator {
		g := &fkConflict{}
		g.flags.FlagSet = pflag.NewFlagSet(`fktxn`, pflag.ContinueOnError)
		// num-tables affects schema generation; the rest are runtime-only.
		g.flags.Meta = map[string]workload.FlagMeta{
			`workers`:             {RuntimeOnly: true},
			`min-chain-len`:       {RuntimeOnly: true},
			`max-chain-len`:       {RuntimeOnly: true},
			`ops-per-rotation`:    {RuntimeOnly: true},
			`update-pct`:          {RuntimeOnly: true},
			`delete-pct`:          {RuntimeOnly: true},
			`tolerate-src-errors`: {RuntimeOnly: true},
			`pk-pool-size`:        {RuntimeOnly: true},
		}
		g.flags.IntVar(&g.numTables, `num-tables`, 4,
			`Number of random tables to generate during init.`)
		g.flags.Float64Var(&g.fkDensity, `fk-density`, 0.4,
			`Per-ordered-pair probability of an FK during schema generation. `+
				`E[F] = num-tables*(num-tables-1)*fk-density.`)
		g.flags.IntVar(&g.requireMinFKs, `require-min-fks`, 5,
			`Minimum FK constraints the generated schema must contain. `+
				`Init retries with seed+1, seed+2, ... up to a cap until satisfied.`)
		g.flags.IntVar(&g.workers, `workers`, 8,
			`Concurrent worker goroutines.`)
		g.flags.IntVar(&g.minChainLen, `min-chain-len`, 1,
			`Minimum txns per chain.`)
		g.flags.IntVar(&g.maxChainLen, `max-chain-len`, 5,
			`Maximum txns per chain.`)
		g.flags.IntVar(&g.opsPerRotation, `ops-per-rotation`, 10000,
			`Re-pick the sub-DAG every N committed chains. 0 disables rotation.`)
		g.flags.IntVar(&g.updatePct, `update-pct`, 70,
			`Op-mix weight for UPDATE in the Exists state.`)
		g.flags.IntVar(&g.deletePct, `delete-pct`, 30,
			`Op-mix weight for DELETE in the Exists state.`)
		g.flags.BoolVar(&g.tolerateSrcErrors, `tolerate-src-errors`, true,
			`Log and skip source-side errors (FK violations, serialization conflicts) instead of failing.`)
		g.flags.IntVar(&g.pkPoolSize, `pk-pool-size`, 0,
			`If >0, sample PK column values from a shared pool of N pre-generated `+
				`values per column instead of from each column's full type domain. `+
				`Smaller N raises cross-worker PK overlap and the source-side `+
				`serialization rate that comes with it. 0 disables pooling.`)
		RandomSeed.AddFlag(&g.flags)
		g.connFlags = workload.NewConnFlags(&g.flags)
		return g
	},
}

// Meta implements the Generator interface.
func (*fkConflict) Meta() workload.Meta { return fkConflictMeta }

// Flags implements the Flagser interface.
func (g *fkConflict) Flags() workload.Flags { return g.flags }

// ConnFlags implements the ConnFlagser interface.
func (g *fkConflict) ConnFlags() *workload.ConnFlags { return g.connFlags }

// Hooks implements the Hookser interface.
func (g *fkConflict) Hooks() workload.Hooks {
	return workload.Hooks{
		Validate: g.validate,
		PostLoad: g.postLoad,
	}
}

// postLoad runs the ALTER TABLE FK statements collected during schema
// generation. FKs run after table creation and any initial data load so
// that the data load isn't subject to FK ordering constraints.
func (g *fkConflict) postLoad(ctx context.Context, db *gosql.DB) error {
	g.ensureSchema()
	if g.schemaErr != nil {
		return g.schemaErr
	}
	for _, stmt := range g.fkStmts {
		if _, err := db.ExecContext(ctx, stmt.String()); err != nil {
			return errors.Wrapf(err, "applying FK constraint: %s", stmt.String())
		}
	}
	return nil
}

func (g *fkConflict) validate() error {
	if g.numTables <= 0 {
		return errors.Newf("num-tables must be positive, got %d", g.numTables)
	}
	if g.fkDensity < 0 || g.fkDensity > 1 {
		return errors.Newf("fk-density must be in [0, 1], got %f", g.fkDensity)
	}
	if g.requireMinFKs < 0 {
		return errors.Newf("require-min-fks must be non-negative, got %d", g.requireMinFKs)
	}
	if g.workers <= 0 {
		return errors.Newf("workers must be positive, got %d", g.workers)
	}
	if g.minChainLen <= 0 {
		return errors.Newf("min-chain-len must be positive, got %d", g.minChainLen)
	}
	if g.maxChainLen < g.minChainLen {
		return errors.Newf(
			"max-chain-len (%d) must be >= min-chain-len (%d)",
			g.maxChainLen, g.minChainLen,
		)
	}
	if g.updatePct < 0 || g.deletePct < 0 {
		return errors.Newf(
			"op-mix weights must be non-negative, got update=%d delete=%d",
			g.updatePct, g.deletePct,
		)
	}
	if g.updatePct+g.deletePct == 0 {
		return errors.New("at least one of update-pct or delete-pct must be positive")
	}
	if g.pkPoolSize < 0 {
		return errors.Newf("pk-pool-size must be non-negative, got %d", g.pkPoolSize)
	}
	return nil
}

// Tables implements the Generator interface. Returns a randomly generated
// set of CREATE TABLE definitions. FK constraints are applied separately by
// PostLoad so initial data loads (if any) aren't constrained by FK ordering.
//
// To run against a hand-crafted schema instead, skip ./workload init: create
// the tables yourself and call ./workload run, which discovers whatever
// schema is in the live database.
func (g *fkConflict) Tables() []workload.Table {
	g.ensureSchema()
	return g.tables
}

// schemaRetryCap bounds the seed-bump loop in ensureSchema. Generation is
// fast and deterministic, so a high cap is cheap; pick something well above
// any realistic requirement-satisfaction probability.
const schemaRetryCap = 100

func (g *fkConflict) ensureSchema() {
	g.schemaOnce.Do(func() {
		baseSeed := RandomSeed.Seed()
		for attempt := 0; attempt < schemaRetryCap; attempt++ {
			seed := baseSeed + int64(attempt)
			tables, fkStmts, ok := generateSchema(seed, g.numTables, g.fkDensity)
			if !ok || len(fkStmts) < g.requireMinFKs {
				continue
			}
			g.tables, g.fkStmts = tables, fkStmts
			if attempt > 0 {
				log.Dev.Infof(context.Background(),
					"fktxn: schema accepted at seed=%d (attempt %d) with %d FKs",
					seed, attempt+1, len(fkStmts))
			}
			return
		}
		// No seed in the search range produced an acceptable schema. Record
		// the failure for postLoad/Ops to surface; do not fall back to a
		// partial schema. Callers (LDR roachtest) handle init failure by
		// retrying with a different base seed.
		g.schemaErr = errors.Newf(
			"no schema satisfied require-min-fks=%d after %d attempts starting at seed=%d",
			g.requireMinFKs, schemaRetryCap, baseSeed)
	})
}

// Ops implements the Opser interface. It connects to the database, discovers
// the schema, and hands the static run config to a fresh orchestrator that
// produces per-worker functions. All workers share one sub-DAG; rotation
// happens in-band when --ops-per-rotation is positive.
func (g *fkConflict) Ops(
	ctx context.Context, urls []string, reg *histogram.Registry,
) (workload.QueryLoad, error) {
	cfg := orchestratorConfig{
		URLs:              urls,
		Workers:           g.workers,
		MinChainLen:       g.minChainLen,
		MaxChainLen:       g.maxChainLen,
		OpsPerRotation:    g.opsPerRotation,
		Mix:               OpMix{Update: g.updatePct, Delete: g.deletePct},
		TolerateSrcErrors: g.tolerateSrcErrors,
		Seed:              RandomSeed.Seed(),
		PKPoolSize:        g.pkPoolSize,
	}
	return newOrchestrator(ctx, cfg, reg)
}
