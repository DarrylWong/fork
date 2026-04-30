// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"context"
	"math/rand"

	"github.com/cockroachdb/cockroach/pkg/sql/randgen"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/workload"
)

// generateSchema produces numTables random CREATE TABLE statements wired with
// random FK constraints, returning two parallel results: the workload.Table
// entries that init applies via CREATE TABLE, and the ALTER TABLE FK
// statements to apply afterward (collected for the PostLoad hook).
//
// FK actions are stripped to NO ACTION because the workload's transaction
// generator drives explicit child-first deletes and parent-first inserts —
// it does not branch on CASCADE / SET NULL / SET DEFAULT behavior. Letting
// randgen emit those actions would silently violate the workload's
// invariant.
func generateSchema(seed int64, numTables int) ([]workload.Table, []tree.Statement) {
	rng := rand.New(rand.NewSource(seed))
	stmts := randgen.RandCreateTables(
		context.Background(),
		rng,
		"t",
		numTables,
		[]randgen.TableOption{randgen.WithPrimaryIndexRequired()},
		randgen.ForeignKeyMutator,
	)

	var tables []workload.Table
	var fkStmts []tree.Statement
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *tree.CreateTable:
			fmtCtx := tree.NewFmtCtx(tree.FmtParsable)
			s.FormatBody(fmtCtx)
			tables = append(tables, workload.Table{
				Name:   string(s.Table.ObjectName),
				Schema: fmtCtx.CloseAndGetString(),
			})
		case *tree.AlterTable:
			stripFKActions(s)
			fkStmts = append(fkStmts, s)
		}
	}
	return tables, fkStmts
}

// stripFKActions clears ON DELETE / ON UPDATE actions on every FK constraint
// in the ALTER TABLE statement. randgen.ForeignKeyMutator picks any of
// CASCADE / SET NULL / SET DEFAULT / NO ACTION / RESTRICT, but the workload
// only handles NO ACTION (the default). Zeroing out Actions falls back to
// NO ACTION on both sides.
func stripFKActions(alter *tree.AlterTable) {
	for _, cmd := range alter.Cmds {
		add, ok := cmd.(*tree.AlterTableAddConstraint)
		if !ok {
			continue
		}
		fk, ok := add.ConstraintDef.(*tree.ForeignKeyConstraintTableDef)
		if !ok {
			continue
		}
		fk.Actions = tree.ReferenceActions{}
	}
}
