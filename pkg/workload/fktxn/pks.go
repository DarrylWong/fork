// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"math/rand"

	"github.com/cockroachdb/cockroach/pkg/sql/randgen"
	randworkload "github.com/cockroachdb/cockroach/pkg/workload/rand"
	"github.com/cockroachdb/errors"
)

// PKPool is a per-table set of PK candidate values that workers in a shard
// sample from. The orchestrator builds one pool per shard at startup using a
// shard-seeded RNG; the bounded pool size is what produces collisions across
// transactions and drives FK constraint contention. The pool is type-agnostic:
// candidates are generated via randgen.RandDatum so it works for INT, STRING,
// UUID, composite PKs — whatever shape the schema has.
//
// Each map entry is the table's PK candidates; each candidate is one row's
// worth of PK column values, in primary-key column order.
type PKPool map[string][][]interface{}

// BuildPKPool generates poolSize PK candidates per table in tables. Caller
// supplies the RNG so the pool is reproducible from the shard seed.
//
// Tables without a discoverable PK or PK columns lacking a discovered type
// produce an error — the workload cannot generate type-appropriate values
// without the type, and the orchestrator should not have selected such a
// schema for this shard.
func BuildPKPool(rng *rand.Rand, tables []*Table, poolSize int) (PKPool, error) {
	if poolSize <= 0 {
		return nil, errors.AssertionFailedf("pool size must be positive, got %d", poolSize)
	}
	pool := make(PKPool, len(tables))
	for _, tbl := range tables {
		pkCols := primaryKeyColumns(tbl)
		if len(pkCols) == 0 {
			return nil, errors.Newf("table %s has no primary key", tbl.Name)
		}
		colByName := make(map[string]Column, len(tbl.Columns))
		for _, c := range tbl.Columns {
			colByName[c.Name] = c
		}

		candidates := make([][]interface{}, poolSize)
		for i := range candidates {
			vals := make([]interface{}, len(pkCols))
			for j, name := range pkCols {
				col, ok := colByName[name]
				if !ok {
					return nil, errors.AssertionFailedf(
						"PK column %s.%s not found in column list", tbl.Name, name,
					)
				}
				if col.Type == nil {
					return nil, errors.Newf(
						"PK column %s.%s has no discovered type", tbl.Name, name,
					)
				}
				d := randgen.RandDatum(rng, col.Type, false /* nullOk */)
				v, err := randworkload.DatumToGoSQL(d)
				if err != nil {
					return nil, errors.Wrapf(err, "converting PK datum for %s.%s", tbl.Name, name)
				}
				vals[j] = v
			}
			candidates[i] = vals
		}
		pool[tbl.Name] = candidates
	}
	return pool, nil
}

// AssignPKs builds a PKAssignment for one transaction by walking sorted in
// topological order (parents before children). For each table it samples one
// candidate from pool, then patches any PK column that is also an FK column
// (to an in-sub parent) with the value already chosen for the parent. This
// coordination is what makes schemas like the diamond — where a shared
// composite-key component (e.g. org_id) is threaded through multiple tables
// — produce coherent rows: the shared column gets a single agreed value
// across all FKs that reference it.
//
// Workers in the same shard call AssignPKs against the same pool and sub-DAG;
// independent rng streams sample different candidates, but collisions on the
// bounded pool size produce row-level contention.
func AssignPKs(
	rng *rand.Rand, sorted []*Table, sub *FKGraph, pool PKPool,
) (PKAssignment, error) {
	subEdges := make(map[string]bool, len(sub.Edges))
	for _, e := range sub.Edges {
		subEdges[e.Name] = true
	}
	tableByName := make(map[string]*Table, len(sorted))
	for _, t := range sorted {
		tableByName[t.Name] = t
	}

	pks := make(PKAssignment, len(sorted))
	for _, tbl := range sorted {
		candidates, ok := pool[tbl.Name]
		if !ok {
			return nil, errors.AssertionFailedf("table %s missing from PK pool", tbl.Name)
		}
		// Copy so we can patch without mutating the pool.
		picked := append([]interface{}(nil), candidates[rng.Intn(len(candidates))]...)

		pkCols := primaryKeyColumns(tbl)
		for i, pkCol := range pkCols {
			parent, parentCol, ok := lookupFKParent(tbl, pkCol, subEdges)
			if !ok {
				continue
			}
			parentPK, parentOk := pks[parent]
			if !parentOk {
				// Parent is not in the sub-DAG (trimmed by RandomSubDAG); fall
				// back to the sampled value.
				continue
			}
			parentTbl, ok := tableByName[parent]
			if !ok {
				continue
			}
			parentPKCols := primaryKeyColumns(parentTbl)
			for j, c := range parentPKCols {
				if c == parentCol {
					picked[i] = parentPK[j]
					break
				}
			}
		}
		pks[tbl.Name] = picked
	}
	return pks, nil
}

// lookupFKParent returns the in-sub FK parent (if any) for childCol on tbl.
// Self-ref edges are skipped — they're handled via NULL + backfill, not via
// PK propagation.
func lookupFKParent(
	tbl *Table, childCol string, subEdges map[string]bool,
) (parent, parentCol string, ok bool) {
	for _, e := range tbl.OutboundFKs {
		if e.ReferencingTable == e.ReferencedTable {
			continue
		}
		if !subEdges[e.Name] {
			continue
		}
		for i, c := range e.ReferencingColumns {
			if c == childCol {
				return e.ReferencedTable, e.ReferencedColumns[i], true
			}
		}
	}
	return "", "", false
}
