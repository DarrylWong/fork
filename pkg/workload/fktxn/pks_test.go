// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"context"
	"math/rand"
	mathrandv2 "math/rand/v2"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/sqlutils"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/stretchr/testify/require"
)

// TestPKValuePool exercises the per-PK-column value pool: every PK value
// returned by AssignPKs should come from the pool when one is supplied.
func TestPKValuePool(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	// Composite PK so the test exercises pool sampling on multiple columns
	// per table.
	const ddl = `
		CREATE TABLE parent (
			a INT NOT NULL,
			b INT NOT NULL,
			PRIMARY KEY (a, b));
		CREATE TABLE child (
			a INT NOT NULL,
			b INT NOT NULL,
			c INT NOT NULL,
			PRIMARY KEY (a, b, c),
			FOREIGN KEY (a, b) REFERENCES parent(a, b))`

	schema := discoverSchemaFromDDL(t, srv, sqlDB, "pkpool", ddl)
	graphs := BuildFKGraphs(schema)
	require.Len(t, graphs, 1, "expected a single FK graph")
	graph := graphs[0]

	rng := rand.New(rand.NewSource(1))
	sorted, sub, dropped, err := RandomSubDAG(mathrandv2.New(mathrandv2.NewPCG(2, 0)), graph)
	require.NoError(t, err)
	_ = dropped

	const poolSize = 3
	pool, err := BuildPKValuePool(rng, sorted, poolSize)
	require.NoError(t, err)

	// Verify every pool bucket holds the expected number of values.
	for _, tbl := range sorted {
		for _, pkCol := range primaryKeyColumns(tbl) {
			values, ok := pool.lookup(tbl.Name, pkCol)
			require.True(t, ok, "missing pool entry for %s.%s", tbl.Name, pkCol)
			require.Len(t, values, poolSize, "wrong pool size for %s.%s", tbl.Name, pkCol)
		}
	}

	// Build a per-column allowed-set from the pool so we can check membership
	// of each AssignPKs return value.
	allowed := make(map[string]map[string]map[interface{}]bool, len(sorted))
	for _, tbl := range sorted {
		allowed[tbl.Name] = make(map[string]map[interface{}]bool)
		for _, pkCol := range primaryKeyColumns(tbl) {
			values, _ := pool.lookup(tbl.Name, pkCol)
			set := make(map[interface{}]bool, len(values))
			for _, v := range values {
				set[v] = true
			}
			allowed[tbl.Name][pkCol] = set
		}
	}

	// Sample many assignments and assert every column value comes from
	// the pool. 100 iterations against poolSize=3 gives ample chance to
	// catch a fall-through to RandDatum.
	for i := 0; i < 100; i++ {
		assignment, err := AssignPKs(rng, sorted, sub, pool)
		require.NoError(t, err)
		for _, tbl := range sorted {
			pkCols := primaryKeyColumns(tbl)
			values, ok := assignment[tbl.Name]
			require.True(t, ok, "no assignment for table %s", tbl.Name)
			require.Len(t, values, len(pkCols))
			for j, pkCol := range pkCols {
				require.Truef(t, allowed[tbl.Name][pkCol][values[j]],
					"iter %d: %s.%s value %v not in pool", i, tbl.Name, pkCol, values[j])
			}
		}
	}
}

// TestAssignPKsNilPoolFallsBack verifies that when no pool is supplied,
// AssignPKs continues to sample from the type domain (the prior behavior).
// The test asserts only that AssignPKs succeeds and returns one value per
// PK column; type-domain coverage is not testable in a single call.
func TestAssignPKsNilPoolFallsBack(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	schema := discoverSchemaFromDDL(t, srv, sqlDB, "pkpool_nil", simplePairDDL)
	graphs := BuildFKGraphs(schema)
	require.Len(t, graphs, 1)
	sorted, sub, _, err := RandomSubDAG(mathrandv2.New(mathrandv2.NewPCG(3, 0)), graphs[0])
	require.NoError(t, err)

	rng := rand.New(rand.NewSource(1))
	assignment, err := AssignPKs(rng, sorted, sub, nil)
	require.NoError(t, err)
	for _, tbl := range sorted {
		pkCols := primaryKeyColumns(tbl)
		require.Len(t, assignment[tbl.Name], len(pkCols))
	}
}
