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
	"sort"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/sql/randgen"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	randworkload "github.com/cockroachdb/cockroach/pkg/workload/rand"
	"github.com/cockroachdb/errors"
)

// dbTx is the subset of *sql.Tx that the Execute* helpers need. Production
// code passes a real *sql.Tx; tests pass a recording wrapper to capture SQL
// for inspection. Keeping the surface narrow lets test wrappers remain small.
type dbTx interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (gosql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*gosql.Rows, error)
}

// PKAssignment maps each table in a sub-DAG to the PK column values to use
// for that table, in primary-key column order. The orchestrator builds this
// per shard; workers within the same shard receive the same PKAssignment so
// they contend on the same rows. Workers know nothing about shards or PK
// ranges — they just write the PKs they're handed.
type PKAssignment map[string][]interface{}

// emittedRow holds the column values written for one table within a single
// transaction. Keys are column names; values are the Go-SQL representations
// passed to the driver. The full row is stored (not just the PK) because FK
// constraints may reference non-PK unique constraint columns.
type emittedRow map[string]interface{}

// emittedSet maps table name → row written for that table in the current txn.
type emittedSet map[string]emittedRow

// droppedEdgeSet indexes edges dropped by RandomSubDAG by referencing table.
// FK columns belonging to a dropped edge are written as NULL in the initial
// UPSERT and patched with an UPDATE after the topological walk completes.
type droppedEdgeSet map[string][]FKEdge

func newDroppedEdgeSet(edges []FKEdge) droppedEdgeSet {
	s := make(droppedEdgeSet, len(edges))
	for _, e := range edges {
		s[e.ReferencingTable] = append(s[e.ReferencingTable], e)
	}
	return s
}

// hasColumn reports whether col is a column on this edge's referencing side.
func (s droppedEdgeSet) hasColumn(table, col string) bool {
	for _, e := range s[table] {
		for _, c := range e.ReferencingColumns {
			if c == col {
				return true
			}
		}
	}
	return false
}

// ExecuteUpsert generates and executes an UPSERT walk over sorted in
// topological order, writing one row per table. PK values come from pks
// (built by the orchestrator); FK column values are taken from rows already
// emitted in this transaction (parents are guaranteed to have been visited
// first by the topological order). Dropped cycle-breaking edges are written
// as NULL initially, then patched with UPDATE statements once both sides
// exist.
//
// Returns the set of rows emitted, which the caller can use to chain a
// subsequent UPDATE/DELETE on the same transaction. On failure the partial
// emitted set is returned alongside the error so callers can inspect what
// was attempted (e.g. to pivot on a unique-violation collision).
func ExecuteUpsert(
	ctx context.Context,
	tx dbTx,
	rng *rand.Rand,
	sorted []*Table,
	sub *FKGraph,
	droppedEdges []FKEdge,
	pks PKAssignment,
) (emittedSet, error) {
	return ExecuteUpsertWithPinned(ctx, tx, rng, sorted, sub, droppedEdges, pks, nil)
}

// ExecuteUpsertWithPinned is ExecuteUpsert with an additional override map.
// For any table in pinned, the pre-built row replaces what buildRow would
// have produced — used by the worker's unique-violation pivot path to keep
// the failing row's UC values stable across the retry while a fresh PK is
// substituted.
func ExecuteUpsertWithPinned(
	ctx context.Context,
	tx dbTx,
	rng *rand.Rand,
	sorted []*Table,
	sub *FKGraph,
	droppedEdges []FKEdge,
	pks PKAssignment,
	pinned map[string]emittedRow,
) (emittedSet, error) {
	emitted := make(emittedSet, len(sorted))
	dropped := newDroppedEdgeSet(droppedEdges)

	for _, t := range sorted {
		pkVals, ok := pks[t.Name]
		if !ok {
			return emitted, errors.AssertionFailedf("no PK assigned for table %s", t.Name)
		}
		var row emittedRow
		if pinnedRow, ok := pinned[t.Name]; ok {
			// Use the caller's pre-built row, but ensure the PK columns reflect
			// the (possibly pivoted) PK assignment.
			row = make(emittedRow, len(pinnedRow))
			for k, v := range pinnedRow {
				row[k] = v
			}
			pkCols := primaryKeyColumns(t)
			for i, c := range pkCols {
				row[c] = pkVals[i]
			}
		} else {
			built, err := buildRow(rng, t, sub, dropped, emitted, pkVals)
			if err != nil {
				return emitted, errors.Wrapf(err, "building row for %s", t.Name)
			}
			row = built
		}
		emitted[t.Name] = row
		if err := execUpsertRow(ctx, tx, t, row); err != nil {
			return emitted, &UpsertError{Table: t.Name, Row: row, Err: err}
		}
		// If any in-sub child FK references a computed column on t, read it
		// back so downstream buildRow calls can resolve the FK value.
		if err := readBackComputedFKTargets(ctx, tx, t, sub, pkVals, row); err != nil {
			return emitted, errors.Wrapf(err, "reading back computed FK targets for %s", t.Name)
		}
	}

	if err := patchDroppedEdges(ctx, tx, sorted, droppedEdges, emitted); err != nil {
		return emitted, errors.Wrap(err, "patching dropped edges")
	}
	return emitted, nil
}

// readBackComputedFKTargets fills computed columns on t into row when the
// columns are referenced by any in-sub FK. Without this, downstream child
// UPSERTs can't resolve their FK values — the parent's computed column was
// populated by the DB on the just-executed UPSERT, but the workload never
// chose a value for it client-side.
//
// Issues one SELECT per parent table that has at least one FK-referenced
// computed column. The SELECT runs against the same tx as the UPSERT, so it
// observes the just-written row.
func readBackComputedFKTargets(
	ctx context.Context, tx dbTx, t *Table, sub *FKGraph, pkVals []interface{}, row emittedRow,
) error {
	computed := make(map[string]bool)
	for _, c := range t.Columns {
		if c.Computed {
			computed[c.Name] = true
		}
	}
	if len(computed) == 0 {
		return nil
	}

	// Collect computed columns on t that are referenced by some in-sub FK.
	needed := make(map[string]bool)
	for _, e := range sub.Edges {
		if e.ReferencedTable != t.Name {
			continue
		}
		for _, refCol := range e.ReferencedColumns {
			if computed[refCol] {
				needed[refCol] = true
			}
		}
	}
	if len(needed) == 0 {
		return nil
	}

	pkCols := primaryKeyColumns(t)
	if len(pkVals) != len(pkCols) {
		return errors.AssertionFailedf(
			"table %s expects %d PK values, got %d", t.Name, len(pkCols), len(pkVals),
		)
	}
	colNames := make([]string, 0, len(needed))
	for c := range needed {
		colNames = append(colNames, c)
	}
	sort.Strings(colNames)
	selectCols := make([]string, len(colNames))
	for i, c := range colNames {
		selectCols[i] = tree.NameString(c)
	}
	whereClauses := make([]string, len(pkCols))
	args := make([]interface{}, len(pkCols))
	for i, c := range pkCols {
		whereClauses[i] = fmt.Sprintf("%s = $%d", tree.NameString(c), i+1)
		args[i] = pkVals[i]
	}
	stmt := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s",
		strings.Join(selectCols, ", "),
		tree.NameString(t.Name),
		strings.Join(whereClauses, " AND "),
	)
	rows, err := tx.QueryContext(ctx, stmt, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return errors.AssertionFailedf(
			"no row found in %s after UPSERT (PK=%v); read-back of computed columns failed",
			t.Name, pkVals,
		)
	}
	dest := make([]interface{}, len(colNames))
	for i := range dest {
		var v interface{}
		dest[i] = &v
	}
	if err := rows.Scan(dest...); err != nil {
		return err
	}
	for i, c := range colNames {
		row[c] = *(dest[i].(*interface{}))
	}
	return rows.Err()
}

// UpsertError tags an ExecuteUpsert failure with the table that hit the
// error and the row we attempted to insert. The worker uses Table and Row
// to look up the existing row's PK (via UC values) and pivot the chain on
// a unique violation.
type UpsertError struct {
	Table string
	Row   emittedRow
	Err   error
}

func (e *UpsertError) Error() string {
	return fmt.Sprintf("upserting into %s: %s", e.Table, e.Err)
}

func (e *UpsertError) Unwrap() error { return e.Err }

// buildRow generates one row's worth of column values for t. PK columns are
// filled from pkVals in PK column order (caller-supplied). FK columns on
// edges present in sub are copied from the parent's emitted row. FK columns
// on dropped or self-referential edges are set to nil (NULL); they get
// backfilled by patchDroppedEdges or by the caller. All other columns get
// random datums via randgen.RandDatum.
func buildRow(
	rng *rand.Rand,
	t *Table,
	sub *FKGraph,
	dropped droppedEdgeSet,
	emitted emittedSet,
	pkVals []interface{},
) (emittedRow, error) {
	pkCols := primaryKeyColumns(t)
	if len(pkVals) != len(pkCols) {
		return nil, errors.AssertionFailedf(
			"table %s expects %d PK values, got %d", t.Name, len(pkCols), len(pkVals),
		)
	}
	pkValByCol := make(map[string]interface{}, len(pkCols))
	for i, c := range pkCols {
		pkValByCol[c] = pkVals[i]
	}

	// Resolve FK-determined values for each child column. A single child column
	// can be referenced by multiple FK edges (e.g. a shared composite key
	// thread like org_id appearing in both projects→depts and projects→teams).
	// In that case we must verify all parents agree on the value; if they
	// don't, the caller's PK assignment is uncoordinated and the row would
	// otherwise pass FK validation only by luck.
	fkValueByCol, err := resolveFKColumnValues(t, sub, emitted)
	if err != nil {
		return nil, err
	}

	row := make(emittedRow, len(t.Columns))
	for _, col := range t.Columns {
		// Computed columns are populated by the database; the workload
		// neither writes them nor pre-computes their values. Downstream FKs
		// that reference them are filled in by readBackComputedColumns after
		// the parent UPSERT.
		if col.Computed {
			continue
		}
		fkVal, fkOk := fkValueByCol[col.Name]
		switch {
		case fkOk:
			row[col.Name] = fkVal

		case dropped.hasColumn(t.Name, col.Name) || isSelfRefColumn(t, col.Name):
			row[col.Name] = nil

		default:
			if val, ok := pkValByCol[col.Name]; ok {
				row[col.Name] = val
				continue
			}
			val, err := randomColumnValue(rng, col)
			if err != nil {
				return nil, err
			}
			row[col.Name] = val
		}
	}
	return row, nil
}

// resolveFKColumnValues collects, for each child column on t that participates
// in any in-sub FK edge, the value the column must take from its parent. Self-
// referential edges are skipped (handled via NULL + backfill). When multiple
// FK edges reference the same child column (e.g. a composite-key thread shared
// across two FKs), all parents must agree on the value. Disagreement means the
// caller's PK assignment is uncoordinated — the orchestrator picked PKs that
// land on different parent rows for the shared column, and no in-row choice
// can satisfy all FKs.
func resolveFKColumnValues(
	t *Table, sub *FKGraph, emitted emittedSet,
) (map[string]interface{}, error) {
	subEdges := make(map[string]bool, len(sub.Edges))
	for _, e := range sub.Edges {
		subEdges[e.Name] = true
	}

	// Track every (parent table, parent col) candidate per child column so we
	// can produce a precise error if they disagree.
	type candidate struct {
		parentTable string
		parentCol   string
		val         interface{}
	}
	candidates := make(map[string][]candidate)

	for _, e := range t.OutboundFKs {
		if e.ReferencingTable == e.ReferencedTable {
			continue
		}
		if !subEdges[e.Name] {
			continue
		}
		if len(e.ReferencingColumns) != len(e.ReferencedColumns) {
			return nil, errors.AssertionFailedf(
				"FK %s has %d referencing cols but %d referenced cols",
				e.Name, len(e.ReferencingColumns), len(e.ReferencedColumns),
			)
		}
		parentRow, ok := emitted[e.ReferencedTable]
		if !ok {
			return nil, errors.AssertionFailedf(
				"FK %s parent %s has not been emitted", e.Name, e.ReferencedTable,
			)
		}
		for i, refCol := range e.ReferencingColumns {
			parentCol := e.ReferencedColumns[i]
			val, ok := parentRow[parentCol]
			if !ok {
				return nil, errors.AssertionFailedf(
					"parent column %s.%s missing from emitted row",
					e.ReferencedTable, parentCol,
				)
			}
			candidates[refCol] = append(candidates[refCol], candidate{
				parentTable: e.ReferencedTable,
				parentCol:   parentCol,
				val:         val,
			})
		}
	}

	resolved := make(map[string]interface{}, len(candidates))
	for childCol, cands := range candidates {
		first := cands[0]
		for _, c := range cands[1:] {
			if c.val != first.val {
				return nil, errors.Newf(
					"FK column %s.%s has conflicting parent values: %s.%s=%v vs %s.%s=%v "+
						"(orchestrator must pick PKs that agree on shared FK columns)",
					t.Name, childCol,
					first.parentTable, first.parentCol, first.val,
					c.parentTable, c.parentCol, c.val,
				)
			}
		}
		resolved[childCol] = first.val
	}
	return resolved, nil
}

// randomColumnValue produces a Go-SQL value for col via randgen.RandDatum. If
// the column type was not discovered (e.g. user-defined type), the column is
// treated as opaque and NULL is returned — the workload tolerates this rather
// than failing on unknown types.
func randomColumnValue(rng *rand.Rand, col Column) (interface{}, error) {
	if col.Type == nil {
		return nil, nil
	}
	d := randgen.RandDatum(rng, col.Type, false /* nullOk */)
	return randworkload.DatumToGoSQL(d)
}

// isSelfRefColumn reports whether col is part of a self-referential FK on t.
// Self-ref FK columns are written as NULL initially because the row they
// would point to does not yet exist.
func isSelfRefColumn(t *Table, col string) bool {
	for _, e := range t.OutboundFKs {
		if e.ReferencingTable != e.ReferencedTable {
			continue
		}
		for _, c := range e.ReferencingColumns {
			if c == col {
				return true
			}
		}
	}
	return false
}

// primaryKeyColumns returns the column names that form t's primary key, or
// nil if t has no PK (which is unexpected for tables we operate on).
func primaryKeyColumns(t *Table) []string {
	for _, uc := range t.UniqueConstraints {
		if uc.IsPrimary {
			return uc.Columns
		}
	}
	return nil
}

// execUpsertRow executes an UPSERT INTO t with the columns and values in row.
// Column order is taken from t.Columns so the SQL is deterministic.
func execUpsertRow(ctx context.Context, tx dbTx, t *Table, row emittedRow) error {
	cols := make([]string, 0, len(t.Columns))
	args := make([]interface{}, 0, len(t.Columns))
	for _, col := range t.Columns {
		v, ok := row[col.Name]
		if !ok {
			// A column not in the row means we couldn't generate a value for
			// it (e.g. unknown type). Skip it and let the table default fill in.
			continue
		}
		cols = append(cols, tree.NameString(col.Name))
		args = append(args, v)
	}
	if len(cols) == 0 {
		return nil
	}
	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	stmt := fmt.Sprintf(
		"UPSERT INTO %s (%s) VALUES (%s)",
		tree.NameString(t.Name),
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
	)
	_, err := tx.ExecContext(ctx, stmt, args...)
	return err
}

// ExecuteDelete deletes one row per table in the sub-DAG, identified by the
// PKs in pks. The walk locks rows in topological order (parents first) with
// SELECT ... FOR UPDATE, then deletes in reverse order (leaves first) so
// child rows are gone before their parents.
//
// Returns (false, nil) and rolls nothing back if any target row is missing
// — typically because another worker in the same shard already deleted it.
// The caller is expected to commit the transaction either way; the partial
// SELECT FOR UPDATE locks acquired so far are released on commit.
func ExecuteDelete(
	ctx context.Context, tx dbTx, sorted []*Table, pks PKAssignment,
) (deleted bool, err error) {
	for _, t := range sorted {
		pkVals, ok := pks[t.Name]
		if !ok {
			return false, errors.AssertionFailedf("no PK assigned for table %s", t.Name)
		}
		found, err := lockRow(ctx, tx, t, pkVals)
		if err != nil {
			return false, errors.Wrapf(err, "locking row in %s", t.Name)
		}
		if !found {
			return false, nil
		}
	}

	for i := len(sorted) - 1; i >= 0; i-- {
		t := sorted[i]
		pkVals := pks[t.Name]
		if err := deleteRow(ctx, tx, t, pkVals); err != nil {
			return false, errors.Wrapf(err, "deleting row from %s", t.Name)
		}
	}
	return true, nil
}

// lockRow runs SELECT ... FOR UPDATE on t's PK and returns whether a row
// matched. The selected columns are unused — only the lock matters.
func lockRow(ctx context.Context, tx dbTx, t *Table, pkVals []interface{}) (bool, error) {
	pkCols := primaryKeyColumns(t)
	if len(pkVals) != len(pkCols) {
		return false, errors.AssertionFailedf(
			"table %s expects %d PK values, got %d", t.Name, len(pkCols), len(pkVals),
		)
	}
	whereClauses := make([]string, len(pkCols))
	for i, c := range pkCols {
		whereClauses[i] = fmt.Sprintf("%s = $%d", tree.NameString(c), i+1)
	}
	stmt := fmt.Sprintf(
		"SELECT 1 FROM %s WHERE %s FOR UPDATE",
		tree.NameString(t.Name),
		strings.Join(whereClauses, " AND "),
	)
	rows, err := tx.QueryContext(ctx, stmt, pkVals...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

// deleteRow runs DELETE FROM t WHERE pk = ... using pkVals. Missing rows are
// not an error — we may have raced with another worker between the lock walk
// and the delete walk. (The lock walk's FOR UPDATE makes this race rare but
// not impossible if the txn was retried.)
func deleteRow(ctx context.Context, tx dbTx, t *Table, pkVals []interface{}) error {
	pkCols := primaryKeyColumns(t)
	whereClauses := make([]string, len(pkCols))
	for i, c := range pkCols {
		whereClauses[i] = fmt.Sprintf("%s = $%d", tree.NameString(c), i+1)
	}
	stmt := fmt.Sprintf(
		"DELETE FROM %s WHERE %s",
		tree.NameString(t.Name),
		strings.Join(whereClauses, " AND "),
	)
	_, err := tx.ExecContext(ctx, stmt, pkVals...)
	return err
}

// ExecuteUpdate picks one table in sorted that has at least one in-sub
// outbound FK and re-points its FK columns at the parent values in pks. The
// row is identified by its own PK from pks. This is the "FK re-point"
// pattern: the target child row has its FK column(s) overwritten with the
// PK of a (possibly different) parent row. Workers in the same shard share
// the pks → they re-point to the same parent values → contention on the FK
// constraint check.
//
// Returns (false, nil) if no table in the sub-DAG has any in-sub outbound
// FK to re-point (e.g. a single-table or self-ref-only sub-DAG), or if the
// target row does not exist (UPDATE matched zero rows). The caller treats
// either as a no-op transaction.
//
// Self-ref edges and dropped cycle-breaking edges are skipped — those
// columns are kept NULL by the workload's UPSERT path and re-pointing them
// here would just NULL them again.
func ExecuteUpdate(
	ctx context.Context, tx dbTx, rng *rand.Rand, sorted []*Table, sub *FKGraph, pks PKAssignment,
) (updated bool, err error) {
	subEdges := make(map[string]bool, len(sub.Edges))
	for _, e := range sub.Edges {
		subEdges[e.Name] = true
	}

	var candidates []*Table
	for _, t := range sorted {
		for _, e := range t.OutboundFKs {
			if e.ReferencingTable == e.ReferencedTable {
				continue
			}
			if subEdges[e.Name] {
				candidates = append(candidates, t)
				break
			}
		}
	}
	if len(candidates) == 0 {
		return false, nil
	}

	target := candidates[rng.Intn(len(candidates))]
	fkValues, err := resolveFKColumnValuesFromPKs(target, sub, pks)
	if err != nil {
		return false, errors.Wrapf(err, "resolving FK columns for %s", target.Name)
	}
	if len(fkValues) == 0 {
		// Target has in-sub outbound FKs but they all share columns with the
		// PK and were already trivially equal. Treat as no-op.
		return false, nil
	}

	pkVals, ok := pks[target.Name]
	if !ok {
		return false, errors.AssertionFailedf("no PK assigned for table %s", target.Name)
	}
	pkCols := primaryKeyColumns(target)
	if len(pkVals) != len(pkCols) {
		return false, errors.AssertionFailedf(
			"table %s expects %d PK values, got %d", target.Name, len(pkCols), len(pkVals),
		)
	}

	// Deterministic column order so the SQL is reproducible.
	colNames := make([]string, 0, len(fkValues))
	for c := range fkValues {
		colNames = append(colNames, c)
	}
	sort.Strings(colNames)

	setClauses := make([]string, 0, len(colNames))
	args := make([]interface{}, 0, len(colNames)+len(pkCols))
	argIdx := 1
	for _, c := range colNames {
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", tree.NameString(c), argIdx))
		args = append(args, fkValues[c])
		argIdx++
	}
	whereClauses := make([]string, len(pkCols))
	for i, c := range pkCols {
		whereClauses[i] = fmt.Sprintf("%s = $%d", tree.NameString(c), argIdx)
		args = append(args, pkVals[i])
		argIdx++
	}

	stmt := fmt.Sprintf(
		"UPDATE %s SET %s WHERE %s",
		tree.NameString(target.Name),
		strings.Join(setClauses, ", "),
		strings.Join(whereClauses, " AND "),
	)
	res, err := tx.ExecContext(ctx, stmt, args...)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// resolveFKColumnValuesFromPKs is the PK-pool analog of resolveFKColumnValues
// (which works against in-txn emitted rows). For each child column on t that
// participates in any in-sub FK edge, it returns the parent value as it
// appears in the parent's PK assignment. Multiple FKs sharing a child column
// must agree on the value; disagreement indicates an uncoordinated PK
// assignment (AssignPKs is responsible for preventing this).
//
// Skips self-ref edges (handled via NULL + backfill) and FK columns whose
// parent isn't in the sub-DAG (those edges aren't candidates for re-point).
func resolveFKColumnValuesFromPKs(
	t *Table, sub *FKGraph, pks PKAssignment,
) (map[string]interface{}, error) {
	subEdges := make(map[string]bool, len(sub.Edges))
	for _, e := range sub.Edges {
		subEdges[e.Name] = true
	}
	tableByName := make(map[string]*Table, len(sub.Tables))
	for name, tbl := range sub.Tables {
		tableByName[name] = tbl
	}

	type candidate struct {
		parentTable string
		parentCol   string
		val         interface{}
	}
	candidates := make(map[string][]candidate)

	for _, e := range t.OutboundFKs {
		if e.ReferencingTable == e.ReferencedTable {
			continue
		}
		if !subEdges[e.Name] {
			continue
		}
		parentPKVals, ok := pks[e.ReferencedTable]
		if !ok {
			continue
		}
		parentTbl, ok := tableByName[e.ReferencedTable]
		if !ok {
			continue
		}
		parentPKCols := primaryKeyColumns(parentTbl)
		parentPKByName := make(map[string]interface{}, len(parentPKCols))
		for i, c := range parentPKCols {
			parentPKByName[c] = parentPKVals[i]
		}

		if len(e.ReferencingColumns) != len(e.ReferencedColumns) {
			return nil, errors.AssertionFailedf(
				"FK %s has %d referencing cols but %d referenced cols",
				e.Name, len(e.ReferencingColumns), len(e.ReferencedColumns),
			)
		}
		for i, refCol := range e.ReferencingColumns {
			parentCol := e.ReferencedColumns[i]
			val, ok := parentPKByName[parentCol]
			if !ok {
				// FK references a non-PK unique constraint column on the
				// parent. We don't have its value in pks (only PK columns are
				// assigned), so we can't re-point through this FK. Skip.
				continue
			}
			candidates[refCol] = append(candidates[refCol], candidate{
				parentTable: e.ReferencedTable,
				parentCol:   parentCol,
				val:         val,
			})
		}
	}

	resolved := make(map[string]interface{}, len(candidates))
	for childCol, cands := range candidates {
		first := cands[0]
		for _, c := range cands[1:] {
			if c.val != first.val {
				return nil, errors.Newf(
					"FK column %s.%s has conflicting parent PK values: %s.%s=%v vs %s.%s=%v "+
						"(orchestrator must pick PKs that agree on shared FK columns)",
					t.Name, childCol,
					first.parentTable, first.parentCol, first.val,
					c.parentTable, c.parentCol, c.val,
				)
			}
		}
		resolved[childCol] = first.val
	}
	return resolved, nil
}

// patchDroppedEdges issues UPDATE statements to fill in FK columns that were
// written as NULL during the topological walk because their parent had not
// yet been visited (cycle-breaking edges from RandomSubDAG). Both sides must
// be present in emitted; otherwise the edge is left as NULL.
func patchDroppedEdges(
	ctx context.Context, tx dbTx, sorted []*Table, droppedEdges []FKEdge, emitted emittedSet,
) error {
	if len(droppedEdges) == 0 {
		return nil
	}
	pkByTable := make(map[string][]string, len(sorted))
	for _, t := range sorted {
		pkByTable[t.Name] = primaryKeyColumns(t)
	}

	for _, e := range droppedEdges {
		childRow, ok := emitted[e.ReferencingTable]
		if !ok {
			continue
		}
		parentRow, ok := emitted[e.ReferencedTable]
		if !ok {
			continue
		}
		if err := execPatch(ctx, tx, e, childRow, parentRow, pkByTable[e.ReferencingTable]); err != nil {
			return errors.Wrapf(err, "patching FK %s", e.Name)
		}
	}
	return nil
}

// execPatch runs an UPDATE that sets the FK columns on the child row to the
// parent's referenced columns, identifying the child by its PK.
func execPatch(
	ctx context.Context, tx dbTx, e FKEdge, childRow, parentRow emittedRow, childPKCols []string,
) error {
	if len(childPKCols) == 0 {
		return errors.AssertionFailedf("table %s has no primary key", e.ReferencingTable)
	}

	setClauses := make([]string, 0, len(e.ReferencingColumns))
	args := make([]interface{}, 0, len(e.ReferencingColumns)+len(childPKCols))
	argIdx := 1
	for i, refCol := range e.ReferencingColumns {
		parentVal, ok := parentRow[e.ReferencedColumns[i]]
		if !ok {
			return errors.AssertionFailedf(
				"parent column %s missing from emitted row", e.ReferencedColumns[i],
			)
		}
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", tree.NameString(refCol), argIdx))
		args = append(args, parentVal)
		argIdx++
	}

	whereClauses := make([]string, 0, len(childPKCols))
	for _, pkCol := range childPKCols {
		pkVal, ok := childRow[pkCol]
		if !ok {
			return errors.AssertionFailedf(
				"PK column %s missing from emitted row for %s", pkCol, e.ReferencingTable,
			)
		}
		whereClauses = append(whereClauses, fmt.Sprintf("%s = $%d", tree.NameString(pkCol), argIdx))
		args = append(args, pkVal)
		argIdx++
	}

	stmt := fmt.Sprintf(
		"UPDATE %s SET %s WHERE %s",
		tree.NameString(e.ReferencingTable),
		strings.Join(setClauses, ", "),
		strings.Join(whereClauses, " AND "),
	)
	_, err := tx.ExecContext(ctx, stmt, args...)
	return err
}
