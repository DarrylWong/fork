// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"testing"

	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/stretchr/testify/require"
)

// TestGenerateSchemaStripsFKActions runs the generator with a handful of
// seeds and checks every emitted FK has Actions zeroed. randgen's mutator
// emits CASCADE / SET NULL / SET DEFAULT randomly; we strip those because
// the workload's transaction generator only handles NO ACTION.
func TestGenerateSchemaStripsFKActions(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 100, 12345} {
		tables, fkStmts := generateSchema(seed, 6)
		require.NotEmpty(t, tables, "seed %d produced no tables", seed)

		for _, stmt := range fkStmts {
			alter, ok := stmt.(*tree.AlterTable)
			require.True(t, ok, "seed %d: expected *tree.AlterTable, got %T", seed, stmt)
			for _, cmd := range alter.Cmds {
				add, ok := cmd.(*tree.AlterTableAddConstraint)
				if !ok {
					continue
				}
				fk, ok := add.ConstraintDef.(*tree.ForeignKeyConstraintTableDef)
				if !ok {
					continue
				}
				require.Equal(t, tree.ReferenceAction(0), fk.Actions.Delete,
					"seed %d FK %s: ON DELETE not stripped", seed, fk.Name)
				require.Equal(t, tree.ReferenceAction(0), fk.Actions.Update,
					"seed %d FK %s: ON UPDATE not stripped", seed, fk.Name)
			}
		}
	}
}

// TestGenerateSchemaDeterministic verifies that the same seed produces the
// same set of table names and FK statements. The orchestrator and the
// PostLoad hook both call generateSchema (via ensureSchema) — and a sync.Once
// guards the in-process cache, but the determinism contract is what makes
// repro on a fresh process possible.
func TestGenerateSchemaDeterministic(t *testing.T) {
	tablesA, fksA := generateSchema(99, 5)
	tablesB, fksB := generateSchema(99, 5)
	require.Equal(t, len(tablesA), len(tablesB))
	for i := range tablesA {
		require.Equal(t, tablesA[i].Name, tablesB[i].Name)
		require.Equal(t, tablesA[i].Schema, tablesB[i].Schema)
	}
	require.Equal(t, len(fksA), len(fksB))
	for i := range fksA {
		require.Equal(t, fksA[i].String(), fksB[i].String())
	}
}
