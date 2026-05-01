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

	numTables          int
	fkDensity          float64
	workers            int
	minChainLen        int
	maxChainLen        int
	subdagRotateChains int
	updatePct          int
	deletePct          int
	tolerateSrcErrors  bool

	// schemaOnce guards lazy schema generation. Tables() and Hooks().PostLoad
	// both consume the result and may be called in either order; generating
	// once guarantees they agree on table names and FK statements.
	schemaOnce sync.Once
	tables     []workload.Table
	fkStmts    []tree.Statement
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
			`workers`:              {RuntimeOnly: true},
			`min-chain-len`:        {RuntimeOnly: true},
			`max-chain-len`:        {RuntimeOnly: true},
			`subdag-rotate-chains`: {RuntimeOnly: true},
			`update-pct`:           {RuntimeOnly: true},
			`delete-pct`:           {RuntimeOnly: true},
			`tolerate-src-errors`:  {RuntimeOnly: true},
		}
		g.flags.IntVar(&g.numTables, `num-tables`, 4,
			`Number of random tables to generate during init.`)
		g.flags.Float64Var(&g.fkDensity, `fk-density`, 0.4,
			`Per-ordered-pair probability of an FK during schema generation. `+
				`E[F] = num-tables*(num-tables-1)*fk-density.`)
		g.flags.IntVar(&g.workers, `workers`, 8,
			`Concurrent worker goroutines.`)
		g.flags.IntVar(&g.minChainLen, `min-chain-len`, 1,
			`Minimum txns per chain.`)
		g.flags.IntVar(&g.maxChainLen, `max-chain-len`, 5,
			`Maximum txns per chain.`)
		g.flags.IntVar(&g.subdagRotateChains, `subdag-rotate-chains`, 1000,
			`Re-pick the sub-DAG every N chains. 0 disables rotation.`)
		g.flags.IntVar(&g.updatePct, `update-pct`, 70,
			`Op-mix weight for UPDATE in the Exists state.`)
		g.flags.IntVar(&g.deletePct, `delete-pct`, 30,
			`Op-mix weight for DELETE in the Exists state.`)
		g.flags.BoolVar(&g.tolerateSrcErrors, `tolerate-src-errors`, true,
			`Log and skip source-side errors (FK violations, serialization conflicts) instead of failing.`)
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

func (g *fkConflict) ensureSchema() {
	g.schemaOnce.Do(func() {
		g.tables, g.fkStmts = generateSchema(RandomSeed.Seed(), g.numTables, g.fkDensity)
	})
}

// Ops implements the Opser interface. It connects to the database, discovers
// the schema, and hands the static run config to a fresh orchestrator that
// produces per-worker functions. All workers share one sub-DAG; rotation
// happens in-band when --subdag-rotate-chains is positive.
func (g *fkConflict) Ops(
	ctx context.Context, urls []string, reg *histogram.Registry,
) (workload.QueryLoad, error) {
	cfg := orchestratorConfig{
		URLs:               urls,
		ConnFlags:          g.connFlags,
		Workers:            g.workers,
		MinChainLen:        g.minChainLen,
		MaxChainLen:        g.maxChainLen,
		SubDAGRotateChains: g.subdagRotateChains,
		Mix:                OpMix{Update: g.updatePct, Delete: g.deletePct},
		TolerateSrcErrors:  g.tolerateSrcErrors,
		Seed:               RandomSeed.Seed(),
	}
	return newOrchestrator(ctx, cfg, reg)
}
