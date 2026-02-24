// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package txnlock

import (
	"context"
	"encoding/binary"
	"hash/fnv"

	"github.com/cockroachdb/cockroach/pkg/crosscluster/logical/ldrdecoder"
	"github.com/cockroachdb/cockroach/pkg/crosscluster/logical/sqlwriter"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/descpb"
	"github.com/cockroachdb/cockroach/pkg/sql/rowenc"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/eval"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
)

// constraintPrefix produces a hash prefix that uniquely identifies a
// constraint on a table. It hashes the table ID and the column IDs that make
// up the constraint. This is used to ensure different constraints produce
// different lock hashes, while allowing foreign keys to share a prefix with
// the parent's PK or unique constraint they reference.
func constraintPrefix(tableID descpb.ID, colIDs []descpb.ColumnID) uint64 {
	h := fnv.New64a()
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(tableID))
	h.Write(buf[:])
	for _, colID := range colIDs {
		binary.BigEndian.PutUint32(buf[:], uint32(colID))
		h.Write(buf[:])
	}
	return h.Sum64()
}

type columnSet struct {
	columns []int32
	// prefix is an integer that is combined with the hash. It is used to ensure
	// different tables or unique constraints produce different hashes.
	prefix uint64
}

func (c *columnSet) hash(row tree.Datums) uint64 {
	// TODO(jeffswenson): can we get rid of the panic here?
	h := fnv.New64a()
	// Include the prefix in the hash to ensure different tables/constraints
	// produce different hashes even with the same column values.
	var prefixBytes [8]byte
	binary.BigEndian.PutUint64(prefixBytes[:], c.prefix)
	h.Write(prefixBytes[:])
	for _, idx := range c.columns {
		datum := row[idx]
		// Encode the datum for consistent hashing
		ed := rowenc.EncDatum{Datum: datum}
		encoded, err := ed.Fingerprint(
			context.Background(),
			datum.ResolvedType(),
			&tree.DatumAlloc{},
			nil, /* appendTo */
			nil, /* acc */
		)
		if err != nil {
			panic(err)
		}
		h.Write(encoded)
	}
	return h.Sum64()
}

func (c *columnSet) null(row tree.Datums) bool {
	if len(row) == 0 {
		return true
	}
	for _, idx := range c.columns {
		if row[idx] == tree.DNull {
			return true
		}
	}
	return false
}

func (c *columnSet) equal(ctx *eval.Context, rowA, rowB tree.Datums) bool {
	// if either is null return false
	if c.null(rowA) || c.null(rowB) {
		return false
	}
	// compare each column in the set
	for _, idx := range c.columns {
		cmp, err := rowA[idx].Compare(context.Background(), ctx, rowB[idx])
		if err != nil {
			panic(err)
		}
		if cmp != 0 {
			return false
		}
	}
	return true
}

type tableConstraints struct {
	PrimaryKey            columnSet
	UniqueConstraints     []columnSet
	ForeignKeyConstraints []columnSet
}

// containsPrefix returns true if prefix matches this table's primary key
// or any of its unique constraints.
func (t *tableConstraints) containsPrefix(prefix uint64) bool {
	if prefix == t.PrimaryKey.prefix {
		return true
	}
	for _, uc := range t.UniqueConstraints {
		if prefix == uc.prefix {
			return true
		}
	}
	// We don't check FK prefixes since they are constraints on a different table.
	return false
}

func newTableConstraints(table catalog.TableDescriptor) *tableConstraints {
	// Get the column schema which determines the order of datums in rows
	columnSchema := sqlwriter.GetColumnSchema(table)

	// Build a map from column ID to index in the datums array
	colIDToIndex := make(map[descpb.ColumnID]int32, len(columnSchema))
	for i, col := range columnSchema {
		colIDToIndex[col.Column.GetID()] = int32(i)
	}

	tc := &tableConstraints{}

	// Extract primary key columns
	primaryIndex := table.GetPrimaryIndex()
	pkColIDs := primaryIndex.CollectKeyColumnIDs().Ordered()
	tc.PrimaryKey = columnSet{
		columns: make([]int32, len(pkColIDs)),
		prefix:  constraintPrefix(table.GetID(), pkColIDs),
	}
	for i, colID := range pkColIDs {
		tc.PrimaryKey.columns[i] = colIDToIndex[colID]
	}

	// Extract unique constraints with indexes (excluding primary key)
	for _, uc := range table.EnforcedUniqueConstraintsWithIndex() {
		if uc.GetID() == primaryIndex.GetID() {
			continue
		}
		ucColIDs := uc.CollectKeyColumnIDs().Ordered()
		cols := make([]int32, len(ucColIDs))
		for i, colID := range ucColIDs {
			cols[i] = colIDToIndex[colID]
		}
		tc.UniqueConstraints = append(tc.UniqueConstraints, columnSet{
			columns: cols,
			prefix:  constraintPrefix(table.GetID(), ucColIDs),
		})
	}

	// Extract unique constraints without indexes
	for _, uc := range table.EnforcedUniqueConstraintsWithoutIndex() {
		ucColIDs := uc.CollectKeyColumnIDs().Ordered()
		cols := make([]int32, len(ucColIDs))
		for i, colID := range ucColIDs {
			cols[i] = colIDToIndex[colID]
		}
		tc.UniqueConstraints = append(tc.UniqueConstraints, columnSet{
			columns: cols,
			prefix:  constraintPrefix(table.GetID(), ucColIDs),
		})
	}

	// Outbound FKs: this table is the child. The FK's referenced column IDs
	// on the parent, sorted, produce the same prefix as the parent's PK or
	// unique constraint, so the child's FK lock collides with the parent's
	// PK/UC lock without needing inbound FK entries on the parent.
	for _, fk := range table.EnforcedOutboundForeignKeys() {
		refColIDs := fk.CollectReferencedColumnIDs().Ordered()
		// Map each referenced column ID to its corresponding origin column ID
		// so we can iterate origin columns in the parent's sorted column ID
		// order. This ensures the child FK hashes values in the same order as
		// the parent's PK/UC.
		refToOrigin := make(map[descpb.ColumnID]descpb.ColumnID, fk.NumOriginColumns())
		for i := 0; i < fk.NumOriginColumns(); i++ {
			refToOrigin[fk.GetReferencedColumnID(i)] = fk.GetOriginColumnID(i)
		}
		cols := make([]int32, len(refColIDs))
		for i, refColID := range refColIDs {
			cols[i] = colIDToIndex[refToOrigin[refColID]]
		}
		tc.ForeignKeyConstraints = append(tc.ForeignKeyConstraints, columnSet{
			columns: cols,
			prefix:  constraintPrefix(fk.GetReferencedTableID(), refColIDs),
		})
	}

	return tc
}

func (t *tableConstraints) deriveLocks(row ldrdecoder.DecodedRow, locks []Lock) []Lock {
	evalCtx := eval.Context{}
	locks = append(locks, Lock{
		Hash: t.PrimaryKey.hash(row.Row),
	})
	for _, uc := range t.UniqueConstraints {
		switch {
		case uc.null(row.Row) && uc.null(row.PrevRow):
			continue
		case uc.equal(&evalCtx, row.Row, row.PrevRow):
			continue
		default:
			if !uc.null(row.Row) {
				locks = append(locks, Lock{
					Hash: uc.hash(row.Row),
					Read: false,
				})
			}
			if !uc.null(row.PrevRow) {
				locks = append(locks, Lock{
					Hash: uc.hash(row.PrevRow),
					Read: false,
				})
			}
		}
	}
	for _, fk := range t.ForeignKeyConstraints {
		switch {
		case fk.null(row.Row) && fk.null(row.PrevRow):
			continue
		case fk.equal(&evalCtx, row.Row, row.PrevRow):
			continue
		default:
			if !fk.null(row.Row) {
				locks = append(locks, Lock{Hash: fk.hash(row.Row), Read: true})
			}
			if !fk.null(row.PrevRow) {
				locks = append(locks, Lock{Hash: fk.hash(row.PrevRow), Read: true})
			}
		}
	}
	return locks
}

// DependsOn returns true if b must be applied before a can be applied.
func (t *tableConstraints) DependsOn(a, b ldrdecoder.DecodedRow) bool {
	// TODO(jeffswenson): we should get a real eval context during
	// initialization.
	evalCtx := eval.Context{}
	if len(t.UniqueConstraints) == 0 {
		return false
	}
	for _, uc := range t.UniqueConstraints {
		if uc.equal(&evalCtx, a.Row, b.PrevRow) {
			return true
		}
	}
	return false
}

// fkDependsOn returns true if b must be applied before a, based on FK
// constraints between their tables. It checks whether the FK's prefix
// matches any of the parent's constraining column sets (PK or UCs),
// since a REFERENCES clause can target either.
func fkDependsOn(
	tcA, tcB *tableConstraints, a, b ldrdecoder.DecodedRow,
) bool {
	// Case 1: a is child, b is parent.
	// b (parent) must come before a (child) if a is creating a reference
	// (non-null FK in Row).
	for _, fk := range tcA.ForeignKeyConstraints {
		if tcB.containsPrefix(fk.prefix) && !fk.null(a.Row) {
			return true
		}
	}

	// Case 2: a is parent, b is child.
	// b (child) must come before a (parent) if b is releasing a reference
	// (non-null FK in PrevRow) and a (parent) is being deleted/updated
	// (non-empty PrevRow means parent row existed before).
	for _, fk := range tcB.ForeignKeyConstraints {
		if tcA.containsPrefix(fk.prefix) &&
			!fk.null(b.PrevRow) && len(a.PrevRow) > 0 {
			return true
		}
	}

	return false
}
