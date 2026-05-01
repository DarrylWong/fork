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

// AssignPKs builds a PKAssignment for one transaction by walking sorted in
// topological order (parents before children). For each table it samples one
// fresh PK value per PK column from the column's full type domain, then
// patches any PK column that is also an FK column (to an in-sub parent) with
// the value already chosen for the parent. The propagation is what makes
// schemas like the diamond — where a shared composite-key component (e.g.
// org_id) is threaded through multiple tables — produce coherent rows: the
// shared column gets a single agreed value across all FKs that reference it.
//
// PKs come from the column's type domain (no shared pool), so cross-worker PK
// collisions are statistically rare. The workload's role is to feed the
// destination a stream of committed source transactions exercising FK
// constraints; source-side write-write contention is incidental, not a goal.
// See "Why no shared PK pool" in the design doc for the rationale.
func AssignPKs(rng *rand.Rand, sorted []*Table, sub *FKGraph) (PKAssignment, error) {
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
		pkCols := primaryKeyColumns(tbl)
		if len(pkCols) == 0 {
			return nil, errors.Newf("table %s has no primary key", tbl.Name)
		}
		colByName := make(map[string]Column, len(tbl.Columns))
		for _, c := range tbl.Columns {
			colByName[c.Name] = c
		}

		picked := make([]interface{}, len(pkCols))
		for i, pkCol := range pkCols {
			col, ok := colByName[pkCol]
			if !ok {
				return nil, errors.AssertionFailedf(
					"PK column %s.%s not found in column list", tbl.Name, pkCol,
				)
			}
			if col.Type == nil {
				return nil, errors.Newf(
					"PK column %s.%s has no discovered type", tbl.Name, pkCol,
				)
			}
			d := randgen.RandDatum(rng, col.Type, false /* nullOk */)
			v, err := randworkload.DatumToGoSQL(d)
			if err != nil {
				return nil, errors.Wrapf(err, "converting PK datum for %s.%s", tbl.Name, pkCol)
			}
			picked[i] = v
		}

		for i, pkCol := range pkCols {
			parent, parentCol, ok := lookupFKParent(tbl, pkCol, subEdges)
			if !ok {
				continue
			}
			parentPK, parentOk := pks[parent]
			if !parentOk {
				// Parent is not in the sub-DAG (trimmed by RandomSubDAG); fall
				// back to the freshly sampled value.
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
