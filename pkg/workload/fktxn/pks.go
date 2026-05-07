// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"context"
	"fmt"
	"math/rand"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/sql/randgen"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	randworkload "github.com/cockroachdb/cockroach/pkg/workload/rand"
	"github.com/cockroachdb/errors"
)

// PKValuePool holds a fixed set of pre-generated values per PK column, keyed
// by table name and then column name. Workers that share a pool will sample
// from the same N values per column, producing cross-worker PK overlaps and
// the source-side serialization conflicts that come with them. A nil pool
// preserves the original behavior of sampling from each column's full type
// domain on every call (see AssignPKs).
type PKValuePool map[string]map[string][]interface{}

// BuildPKValuePool pre-generates poolSize values per PK column across all
// tables in sorted, using rng for value generation. Callers should pass an
// orchestrator-owned RNG so workers don't perturb pool contents. Returns an
// error if any PK column lacks a discovered type.
//
// poolSize must be > 0; callers that want pool-free behavior should pass a
// nil pool to AssignPKs instead of building a zero-sized one.
func BuildPKValuePool(rng *rand.Rand, sorted []*Table, poolSize int) (PKValuePool, error) {
	if poolSize <= 0 {
		return nil, errors.AssertionFailedf("BuildPKValuePool requires poolSize > 0, got %d", poolSize)
	}
	pool := make(PKValuePool, len(sorted))
	for _, tbl := range sorted {
		pkCols := primaryKeyColumns(tbl)
		if len(pkCols) == 0 {
			return nil, errors.Newf("table %s has no primary key", tbl.Name)
		}
		colByName := make(map[string]Column, len(tbl.Columns))
		for _, c := range tbl.Columns {
			colByName[c.Name] = c
		}
		tblPool := make(map[string][]interface{}, len(pkCols))
		for _, pkCol := range pkCols {
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
			values := make([]interface{}, poolSize)
			for i := 0; i < poolSize; i++ {
				d := randgen.RandDatum(rng, col.Type, false /* nullOk */)
				v, err := randworkload.DatumToGoSQL(d)
				if err != nil {
					return nil, errors.Wrapf(err, "converting PK datum for %s.%s", tbl.Name, pkCol)
				}
				values[i] = v
			}
			tblPool[pkCol] = values
		}
		pool[tbl.Name] = tblPool
	}
	return pool, nil
}

// AssignPKs builds a PKAssignment for one transaction by walking sorted in
// topological order (parents before children). For each table it picks one PK
// value per PK column, then patches any PK column that is also an FK column
// (to an in-sub parent) with the value already chosen for the parent. The
// propagation is what makes schemas like the diamond — where a shared
// composite-key component (e.g. org_id) is threaded through multiple tables —
// produce coherent rows: the shared column gets a single agreed value across
// all FKs that reference it.
//
// When pool is nil, each PK column is sampled fresh from its full type domain
// via randgen.RandDatum, so cross-worker collisions are statistically rare.
// When pool is non-nil, each column is sampled (with replacement) from its
// pre-built bucket of poolSize values, which deliberately raises the
// cross-worker collision rate and the source-side serialization that comes
// with it. See the design doc's "Knobs we should expose" for the rationale.
func AssignPKs(
	rng *rand.Rand, sorted []*Table, sub *FKGraph, pool PKValuePool,
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
			if values, ok := pool.lookup(tbl.Name, pkCol); ok {
				picked[i] = values[rng.Intn(len(values))]
				continue
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

// lookup returns the pool of pre-generated values for table.col, or
// (nil, false) when the pool is nil or has no entry for that column. Used by
// AssignPKs to decide whether to sample from the pool or fall back to fresh
// type-domain generation.
func (p PKValuePool) lookup(table, col string) ([]interface{}, bool) {
	if p == nil {
		return nil, false
	}
	tblPool, ok := p[table]
	if !ok {
		return nil, false
	}
	values, ok := tblPool[col]
	if !ok || len(values) == 0 {
		return nil, false
	}
	return values, true
}

// LookupExistingPK searches for a row in t whose values match the attempted
// upsert on any non-PK unique constraint, and returns the existing row's PK
// values in PK column order. The worker uses this on a unique-violation
// upsert: it rewrites pks[t.Name] with the returned PK and re-runs the
// upsert, which now hits the PK-update path instead of insert+UC violation.
//
// attempted is the row we tried to insert (column name → value); the lookup
// uses each non-PK UC's columns to find a matching existing row. The first
// UC that yields a match wins. Returns (nil, nil) if no UC matched any
// existing row — the conflict was on the PK itself or on a UC whose values
// changed mid-flight.
func LookupExistingPK(
	ctx context.Context, q dbTx, t *Table, attempted emittedRow,
) ([]interface{}, error) {
	pkCols := primaryKeyColumns(t)
	if len(pkCols) == 0 {
		return nil, errors.AssertionFailedf("table %s has no primary key", t.Name)
	}
	pkSet := make(map[string]bool, len(pkCols))
	for _, c := range pkCols {
		pkSet[c] = true
	}

	pkSelect := make([]string, len(pkCols))
	for i, c := range pkCols {
		pkSelect[i] = tree.NameString(c)
	}

	for _, uc := range t.UniqueConstraints {
		if uc.IsPrimary {
			continue
		}
		// Skip UCs that overlap with the PK; UPSERT already handles PK
		// conflicts on its own.
		if ucOverlapsPK(uc, pkSet) {
			continue
		}
		args := make([]interface{}, 0, len(uc.Columns))
		whereParts := make([]string, 0, len(uc.Columns))
		skip := false
		for _, c := range uc.Columns {
			v, ok := attempted[c]
			if !ok || v == nil {
				// Missing or NULL value: NULL never matches in a UNIQUE check
				// so no row can match this UC; skip it.
				skip = true
				break
			}
			args = append(args, v)
			whereParts = append(whereParts, fmt.Sprintf("%s = $%d", tree.NameString(c), len(args)))
		}
		if skip {
			continue
		}
		query := fmt.Sprintf(
			"SELECT %s FROM %s WHERE %s LIMIT 1",
			strings.Join(pkSelect, ", "),
			tree.NameString(t.Name),
			strings.Join(whereParts, " AND "),
		)
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, errors.Wrapf(err, "querying %s for existing UC %s row", t.Name, uc.Name)
		}
		pk, err := scanPKRow(rows, len(pkCols))
		if err != nil {
			return nil, errors.Wrapf(err, "scanning %s lookup for UC %s", t.Name, uc.Name)
		}
		if pk != nil {
			return pk, nil
		}
	}
	return nil, nil
}

// ucOverlapsPK reports whether any column of uc is part of the table's PK.
func ucOverlapsPK(uc UniqueConstraint, pkSet map[string]bool) bool {
	for _, c := range uc.Columns {
		if pkSet[c] {
			return true
		}
	}
	return false
}

// scanPKRow scans a single row of nCols values into a []interface{}. Returns
// (nil, nil) if the result set is empty.
func scanPKRow(
	rows interface {
		Next() bool
		Scan(...interface{}) error
		Close() error
	}, nCols int,
) ([]interface{}, error) {
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, nil
	}
	out := make([]interface{}, nCols)
	dest := make([]interface{}, nCols)
	for i := range dest {
		dest[i] = &out[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	return out, nil
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
