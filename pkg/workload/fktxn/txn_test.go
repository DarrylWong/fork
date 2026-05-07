// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"context"
	"fmt"
	"math/rand"
	randv2 "math/rand/v2"
	"strings"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/sqlutils"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
	"github.com/stretchr/testify/require"
)

// txnSchemaCases is the canonical set of DDLs covered by the per-operation
// transaction tests. Adding a case here exercises it in upsert, delete (and,
// once added, update) tests uniformly.
var txnSchemaCases = []struct {
	name string
	ddl  string
}{
	{name: "simple_pair", ddl: simplePairDDL},
	{name: "chain", ddl: chainDDL},
	{name: "diamond", ddl: diamondCompositeDDL},
	{name: "transitive", ddl: transitiveOverlapDDL},
	{name: "self_ref", ddl: selfRefDDL},
}

// shardSetup mirrors what an orchestrator hands to a shard at startup: the
// sub-DAG selected for this shard. Tests build one of these per scenario,
// then call runOnce to execute individual transactions on top of it.
type shardSetup struct {
	srv     serverutils.TestServerInterface
	dbName  string
	sorted  []*Table
	sub     *FKGraph
	dropped []FKEdge
}

// newShardSetup discovers the schema and picks a sub-DAG. rng drives the
// sub-DAG selection (via a seed derived from rng).
func newShardSetup(
	t *testing.T, srv serverutils.TestServerInterface, dbName string, rng *rand.Rand,
) *shardSetup {
	t.Helper()
	testDB := srv.ApplicationLayer().SQLConn(t, serverutils.DBName(dbName))
	s, err := DiscoverSchema(testDB, dbName)
	require.NoError(t, err)
	graphs := BuildFKGraphs(s)
	require.NotEmpty(t, graphs)

	// RandomSubDAG takes a v2 RNG; derive its seed from the v1 stream so the
	// failure seed reported by randutil reproduces both halves.
	rngV2 := randv2.New(randv2.NewPCG(rng.Uint64(), rng.Uint64()))
	sorted, sub, dropped, err := RandomSubDAG(rngV2, graphs[0])
	require.NoError(t, err)

	return &shardSetup{
		srv:     srv,
		dbName:  dbName,
		sorted:  sorted,
		sub:     sub,
		dropped: dropped,
	}
}

// runUpsertOnce samples a fresh PK assignment and executes one UPSERT
// transaction. Returns the rows emitted and the PKs used (so the caller can
// chain a delete on the same chain).
func (s *shardSetup) runUpsertOnce(t *testing.T, rng *rand.Rand) (emittedSet, PKAssignment) {
	t.Helper()
	ctx := context.Background()
	testDB := s.srv.ApplicationLayer().SQLConn(t, serverutils.DBName(s.dbName))

	pks, err := AssignPKs(rng, s.sorted, s.sub, nil)
	require.NoError(t, err)

	tx, err := testDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	emitted, err := ExecuteUpsert(ctx, tx, rng, s.sorted, s.sub, s.dropped, pks)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("upsert failed: %v", err)
	}
	require.NoError(t, tx.Commit())
	return emitted, pks
}

func TestExecuteUpsert(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	for _, tc := range txnSchemaCases {
		t.Run(tc.name, func(t *testing.T) {
			rng, seed := randutil.NewTestRand()
			t.Logf("seed=%d", seed)

			dbName := "up_" + tc.name
			discoverSchemaFromDDL(t, srv, sqlDB, dbName, tc.ddl)
			setup := newShardSetup(t, srv, dbName, rng)

			for i := 0; i < 10; i++ {
				emitted, _ := setup.runUpsertOnce(t, rng)
				require.NotEmpty(t, emitted, "iteration=%d", i)
			}
		})
	}
}

func TestExecuteDelete(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	for _, tc := range txnSchemaCases {
		t.Run(tc.name, func(t *testing.T) {
			rng, seed := randutil.NewTestRand()
			t.Logf("seed=%d", seed)

			dbName := "del_" + tc.name
			discoverSchemaFromDDL(t, srv, sqlDB, dbName, tc.ddl)
			setup := newShardSetup(t, srv, dbName, rng)

			// Insert a row chain, then delete the same chain.
			_, pks := setup.runUpsertOnce(t, rng)

			testDB := srv.ApplicationLayer().SQLConn(t, serverutils.DBName(dbName))
			tx, err := testDB.BeginTx(ctx, nil)
			require.NoError(t, err)
			deleted, err := ExecuteDelete(ctx, tx, setup.sorted, pks)
			require.NoError(t, err)
			require.True(t, deleted, "delete should succeed when rows exist")
			require.NoError(t, tx.Commit())

			// All chain rows should be gone now.
			testSQL := sqlutils.MakeSQLRunner(testDB)
			for _, tbl := range setup.sorted {
				pkCols := primaryKeyColumns(tbl)
				args := pks[tbl.Name]
				whereParts := make([]string, len(pkCols))
				for i, c := range pkCols {
					whereParts[i] = fmt.Sprintf("%s = $%d", c, i+1)
				}
				query := fmt.Sprintf(
					"SELECT count(*) FROM %s WHERE %s",
					tbl.Name, strings.Join(whereParts, " AND "),
				)
				var count int
				testSQL.QueryRow(t, query, args...).Scan(&count)
				require.Equal(t, 0, count, "table %s row %v should be deleted", tbl.Name, args)
			}
		})
	}
}

func TestExecuteUpdate(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	for _, tc := range txnSchemaCases {
		t.Run(tc.name, func(t *testing.T) {
			rng, seed := randutil.NewTestRand()
			t.Logf("seed=%d", seed)

			dbName := "upd_" + tc.name
			discoverSchemaFromDDL(t, srv, sqlDB, dbName, tc.ddl)
			setup := newShardSetup(t, srv, dbName, rng)

			// Insert a chain so the UPDATE has a target row to hit.
			_, pks := setup.runUpsertOnce(t, rng)

			testDB := srv.ApplicationLayer().SQLConn(t, serverutils.DBName(dbName))
			tx, err := testDB.BeginTx(ctx, nil)
			require.NoError(t, err)
			updated, err := ExecuteUpdate(ctx, tx, rng, setup.sorted, setup.sub, pks)
			require.NoError(t, err)
			require.NoError(t, tx.Commit())

			// Whether updated is true depends on whether the sub-DAG had any
			// in-sub outbound FKs. self_ref's only edges are self-referential,
			// so updated may be false. For schemas with cross-table FKs we
			// expect at least the chain row was matched.
			hasInSubFK := false
			for _, e := range setup.sub.Edges {
				if e.ReferencingTable != e.ReferencedTable {
					hasInSubFK = true
					break
				}
			}
			if hasInSubFK {
				require.True(t, updated, "expected UPDATE to match the row we just upserted")
			}
		})
	}
}

// TestExecuteUpdate_NoTargetRow verifies that ExecuteUpdate returns
// (false, nil) when the target row does not exist (UPDATE matched zero
// rows). This mirrors the race where another worker deleted the row
// between PK assignment and statement execution.
func TestExecuteUpdate_NoTargetRow(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	rng, seed := randutil.NewTestRand()
	t.Logf("seed=%d", seed)

	const dbName = "upd_missing"
	discoverSchemaFromDDL(t, srv, sqlDB, dbName, simplePairDDL)
	setup := newShardSetup(t, srv, dbName, rng)

	// Don't insert anything. Try to update; should return false.
	pks, err := AssignPKs(rng, setup.sorted, setup.sub, nil)
	require.NoError(t, err)

	testDB := srv.ApplicationLayer().SQLConn(t, serverutils.DBName(dbName))
	tx, err := testDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	updated, err := ExecuteUpdate(ctx, tx, rng, setup.sorted, setup.sub, pks)
	require.NoError(t, err)
	require.False(t, updated, "expected UPDATE to report false when row is missing")
	require.NoError(t, tx.Commit())
}

// TestExecuteDelete_RowNotPresent verifies that ExecuteDelete returns
// (false, nil) when a target row does not exist (e.g. another worker
// already deleted it). The transaction is left in a state the caller can
// commit cleanly.
func TestExecuteDelete_RowNotPresent(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	rng, seed := randutil.NewTestRand()
	t.Logf("seed=%d", seed)

	const dbName = "del_missing"
	discoverSchemaFromDDL(t, srv, sqlDB, dbName, simplePairDDL)
	setup := newShardSetup(t, srv, dbName, rng)

	// Don't insert anything. Try to delete; should return false.
	pks, err := AssignPKs(rng, setup.sorted, setup.sub, nil)
	require.NoError(t, err)

	testDB := srv.ApplicationLayer().SQLConn(t, serverutils.DBName(dbName))
	tx, err := testDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	deleted, err := ExecuteDelete(ctx, tx, setup.sorted, pks)
	require.NoError(t, err)
	require.False(t, deleted, "delete should report false when row is missing")
	require.NoError(t, tx.Commit())
}
