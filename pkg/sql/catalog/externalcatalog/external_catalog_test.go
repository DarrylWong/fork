// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package externalcatalog

import (
	"context"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/jobs"
	"github.com/cockroachdb/cockroach/pkg/security/username"
	"github.com/cockroachdb/cockroach/pkg/sql"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/descpb"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/descs"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/externalcatalog/externalpb"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/resolver"
	"github.com/cockroachdb/cockroach/pkg/sql/isql"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/catconstants"
	"github.com/cockroachdb/cockroach/pkg/sql/sqltestutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/skip"
	"github.com/cockroachdb/cockroach/pkg/testutils/sqlutils"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
	"github.com/cockroachdb/redact"
	"github.com/stretchr/testify/require"
)

// TestExtractIngestExternalCatalog extracts some tables from a cluster and
// ingests them into another cluster into a different database and schema.
func TestExtractIngestExternalCatalog(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)
	skip.UnderDuress(t)

	ctx := context.Background()
	srv, conn, _ := serverutils.StartServer(t, base.TestServerArgs{
		Knobs: base.TestingKnobs{
			JobsTestingKnobs: jobs.NewTestingKnobsWithShortIntervals(),
		},
	})
	defer srv.Stopper().Stop(ctx)
	s := srv.ApplicationLayer()

	execCfg := s.ExecutorConfig().(sql.ExecutorConfig)

	// Ensure gc job runs quickly drop DropExternalCatalog.
	sysDB := sqlutils.MakeSQLRunner(srv.SystemLayer().SQLConn(t))
	sysDB.Exec(t, "SET CLUSTER SETTING sql.virtual_cluster.feature_access.zone_configs.enabled = true;")
	sysDB.Exec(t, "SET CLUSTER SETTING sql.virtual_cluster.feature_access.zone_configs_unrestricted.enabled = true;")

	rng, _ := randutil.NewTestRand()
	fastGC := rng.Float64() < 0.5
	if fastGC {
		sysDB.Exec(t, "SET CLUSTER SETTING sql.gc_job.wait_for_gc.interval = '250ms'")
	}

	sqlDB := sqlutils.MakeSQLRunner(conn)
	sqlDB.Exec(t, "CREATE DATABASE db1")
	sqlDB.Exec(t, "CREATE SCHEMA db1.sc1")
	sqlDB.Exec(t, "CREATE TABLE db1.sc1.tab1 (a INT PRIMARY KEY)")
	sqlDB.Exec(t, "CREATE INDEX idx ON db1.sc1.tab1 (a)")
	sqlDB.Exec(t, "CREATE TABLE db1.sc1.tab2 (a INT PRIMARY KEY)")

	sqlUser, err := username.MakeSQLUsernameFromUserInput("root", username.PurposeValidation)
	require.NoError(t, err)

	extractCatalog := func(tableNames ...string) (catalog externalpb.ExternalCatalog, err error) {
		err = sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			opName := redact.SafeString("extractCatalog")
			planner, close := sql.NewInternalPlanner(
				opName,
				txn.KV(),
				sqlUser,
				&sql.MemoryMetrics{},
				&execCfg,
				sql.NewInternalSessionData(ctx, execCfg.Settings, opName),
			)
			defer close()

			catalog, err = ExtractExternalCatalog(ctx, planner.(resolver.SchemaResolver), txn, col, false, tableNames...)
			return err
		})
		return catalog, err
	}

	getDatabaseSchemaIDs := func(database string) (descpb.ID, descpb.ID) {
		var parentID descpb.ID
		var schemaID descpb.ID
		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			dbDesc, err := col.ByNameWithLeased(txn.KV()).Get().Database(ctx, database)
			require.NoError(t, err)
			parentID = dbDesc.GetID()
			schemaID, err = col.LookupSchemaID(ctx, txn.KV(), parentID, catconstants.PublicSchemaName)
			return err
		}))
		return parentID, schemaID
	}

	defaultdbID, defaultdbpublicID := getDatabaseSchemaIDs("defaultdb")

	t.Run("basic", func(t *testing.T) {
		tableNames := []string{"db1.sc1.tab1", "db1.sc1.tab2"}
		ingestedTableNames := []string{"tab1", "tab2_rename"}
		ingestableCatalog, err := extractCatalog(tableNames...)
		require.NoError(t, err)
		require.Equal(t, 2, len(ingestableCatalog.Tables))

		var written externalpb.ExternalCatalog
		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			written, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, ingestableCatalog, txn, col, defaultdbID, defaultdbpublicID, false, ingestedTableNames, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
			return err
		}))
		require.Equal(t, 2, len(written.Tables))
		sqlDB.CheckQueryResults(t, "SELECT schema_name,table_name FROM [SHOW TABLES]", [][]string{{"public", "tab1"}, {"public", "tab2_rename"}})

		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			return DropIngestedExternalCatalog(ctx, &execCfg, sqlUser, written, txn, s.JobRegistry().(*jobs.Registry), col, "test gc")
		}))
		var res int
		sqlDB.QueryRow(t, "SELECT count(*) FROM [SHOW TABLES]").Scan(&res)
		require.Zero(t, res)
		if fastGC {
			// With fast gc, the dropped tables may disappear before
			// crdb_internal.tables is read.
			sqlDB.CheckQueryResultsRetry(
				t,
				"SELECT status FROM [SHOW JOBS] WHERE description = 'test gc'",
				[][]string{{"succeeded"}},
			)
		} else {
			sqlDB.CheckQueryResults(t, "SELECT name FROM crdb_internal.tables WHERE state = 'DROP'", [][]string{{"tab1"}, {"tab2_rename"}})
		}
	})

	t.Run("fk missing constraints", func(t *testing.T) {
		sqlDB.Exec(t, "CREATE TABLE db1.sc1.tab3 (a INT PRIMARY KEY, b INT REFERENCES db1.sc1.tab2(a))")
		sadCatalog, err := extractCatalog("db1.sc1.tab3")
		require.NoError(t, err)
		require.ErrorContains(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			_, err := IngestExternalCatalog(ctx, &execCfg, sqlUser, sadCatalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"tab3"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
			return err
		}), "not in the replication set")

		anotherSadCatalog, err := extractCatalog("db1.sc1.tab2")
		require.NoError(t, err)
		require.ErrorContains(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			_, err := IngestExternalCatalog(ctx, &execCfg, sqlUser, anotherSadCatalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"tab2"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
			return err
		}), "not in the replication set")
	})

	t.Run("fk rewrite parent and child", func(t *testing.T) {
		sqlDB.Exec(t, "CREATE TABLE db1.sc1.rw_parent (a INT PRIMARY KEY)")
		sqlDB.Exec(t, "CREATE TABLE db1.sc1.rw_child (a INT PRIMARY KEY, b INT REFERENCES db1.sc1.rw_parent(a))")

		bothCatalog, err := extractCatalog("db1.sc1.rw_parent", "db1.sc1.rw_child")
		require.NoError(t, err)

		var written externalpb.ExternalCatalog
		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			written, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, bothCatalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"rw_parent_dst", "rw_child_dst"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
			return err
		}))

		parentDesc := written.Tables[0]
		childDesc := written.Tables[1]

		// Verify that table IDs were rewritten to new destination IDs.
		require.NotEqual(t, bothCatalog.Tables[0].ID, parentDesc.ID)
		require.NotEqual(t, bothCatalog.Tables[1].ID, childDesc.ID)

		// Verify that parent DB and schema IDs were rewritten.
		require.Equal(t, defaultdbID, parentDesc.ParentID)
		require.Equal(t, defaultdbpublicID, parentDesc.UnexposedParentSchemaID)
		require.Equal(t, defaultdbID, childDesc.ParentID)
		require.Equal(t, defaultdbpublicID, childDesc.UnexposedParentSchemaID)

		// Check that the FK IDs point to the replicated IDs instead of the old.
		require.Len(t, childDesc.OutboundFKs, 1)
		require.Equal(t, parentDesc.ID, childDesc.OutboundFKs[0].ReferencedTableID)
		require.Equal(t, childDesc.ID, childDesc.OutboundFKs[0].OriginTableID)

		require.Len(t, parentDesc.InboundFKs, 1)
		require.Equal(t, childDesc.ID, parentDesc.InboundFKs[0].OriginTableID)
		require.Equal(t, parentDesc.ID, parentDesc.InboundFKs[0].ReferencedTableID)

		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			return DropIngestedExternalCatalog(ctx, &execCfg, sqlUser, written, txn, s.JobRegistry().(*jobs.Registry), col, "test gc fk rewrite")
		}))
	})

	t.Run("fk self-referencing", func(t *testing.T) {
		sqlDB.Exec(t, "CREATE TABLE db1.sc1.self_ref (a INT PRIMARY KEY, b INT REFERENCES db1.sc1.self_ref(a))")

		selfCatalog, err := extractCatalog("db1.sc1.self_ref")
		require.NoError(t, err)

		var written externalpb.ExternalCatalog
		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			written, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, selfCatalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"self_ref_dst"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
			return err
		}))

		desc := written.Tables[0]
		require.NotEqual(t, selfCatalog.Tables[0].ID, desc.ID)

		// Both sides of the self-referencing FK should point to the new ID.
		require.Len(t, desc.OutboundFKs, 1)
		require.Equal(t, desc.ID, desc.OutboundFKs[0].OriginTableID)
		require.Equal(t, desc.ID, desc.OutboundFKs[0].ReferencedTableID)

		require.Len(t, desc.InboundFKs, 1)
		require.Equal(t, desc.ID, desc.InboundFKs[0].OriginTableID)
		require.Equal(t, desc.ID, desc.InboundFKs[0].ReferencedTableID)

		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			return DropIngestedExternalCatalog(ctx, &execCfg, sqlUser, written, txn, s.JobRegistry().(*jobs.Registry), col, "test gc self ref fk")
		}))
	})

	t.Run("skip fk", func(t *testing.T) {
		sqlDB.Exec(t, "CREATE TABLE db1.sc1.child (a INT PRIMARY KEY, b INT REFERENCES db1.sc1.tab2(a))")

		childCatalog, err := extractCatalog("db1.sc1.child")
		require.NoError(t, err)
		require.NotEmpty(t, childCatalog.Tables[0].OutboundFKs)

		var written externalpb.ExternalCatalog
		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			written, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, childCatalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"child"}, true /* skipForeignKeys */, false /* requireKvWriterCompatible */)
			return err
		}))
		require.Empty(t, written.Tables[0].InboundFKs)
		require.Empty(t, written.Tables[0].OutboundFKs)

		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			return DropIngestedExternalCatalog(ctx, &execCfg, sqlUser, written, txn, s.JobRegistry().(*jobs.Registry), col, "test gc skip fk outbound")
		}))

		parentCatalog, err := extractCatalog("db1.sc1.tab2")
		require.NoError(t, err)
		require.NotEmpty(t, parentCatalog.Tables[0].InboundFKs)

		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			written, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, parentCatalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"tab2_skip"}, true /* skipForeignKeys */, false /* requireKvWriterCompatible */)
			return err
		}))
		require.Empty(t, written.Tables[0].InboundFKs)
		require.Empty(t, written.Tables[0].OutboundFKs)

		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			return DropIngestedExternalCatalog(ctx, &execCfg, sqlUser, written, txn, s.JobRegistry().(*jobs.Registry), col, "test gc skip fk inbound")
		}))
	})

	t.Run("table with back-references", func(t *testing.T) {
		sqlDB.Exec(t, "CREATE TABLE db1.sc1.backref_base (a INT PRIMARY KEY)")
		sqlDB.Exec(t, "CREATE VIEW db1.sc1.backref_view AS SELECT a FROM db1.sc1.backref_base")

		catalog, err := extractCatalog("db1.sc1.backref_base")
		require.NoError(t, err)
		require.NotEmpty(t, catalog.Tables[0].DependedOnBy)

		var written externalpb.ExternalCatalog
		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			written, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, catalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"backref_dst"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
			return err
		}))
		require.Empty(t, written.Tables[0].DependedOnBy)

		require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			return DropIngestedExternalCatalog(ctx, &execCfg, sqlUser, written, txn, s.JobRegistry().(*jobs.Registry), col, "test gc backref")
		}))
	})
}

// TestIngestUnsupportedTables tests that we reject tables with unsupported features
// and throws an explicit error.
func TestIngestUnsupportedTables(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)
	skip.UnderStress(t)

	ctx := context.Background()
	srv, conn, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	s := srv.ApplicationLayer()

	execCfg := s.ExecutorConfig().(sql.ExecutorConfig)

	sqlDB := sqlutils.MakeSQLRunner(conn)
	sqlDB.Exec(t, "CREATE DATABASE db1")

	sqlUser, err := username.MakeSQLUsernameFromUserInput("root", username.PurposeValidation)
	require.NoError(t, err)

	extractCatalog := func(tableNames ...string) (catalog externalpb.ExternalCatalog, err error) {
		err = sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
			opName := redact.SafeString("extractCatalog")
			planner, close := sql.NewInternalPlanner(
				opName,
				txn.KV(),
				sqlUser,
				&sql.MemoryMetrics{},
				&execCfg,
				sql.NewInternalSessionData(ctx, execCfg.Settings, opName),
			)
			defer close()

			catalog, err = ExtractExternalCatalog(ctx, planner.(resolver.SchemaResolver), txn, col, false, tableNames...)
			return err
		})
		return catalog, err
	}

	var defaultdbID, defaultdbpublicID descpb.ID
	require.NoError(t, sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
		dbDesc, err := col.ByNameWithLeased(txn.KV()).Get().Database(ctx, "defaultdb")
		require.NoError(t, err)
		defaultdbID = dbDesc.GetID()
		defaultdbpublicID, err = col.LookupSchemaID(ctx, txn.KV(), defaultdbID, catconstants.PublicSchemaName)
		return err
	}))

	t.Run("user defined type", func(t *testing.T) {
		sqlDB.Exec(t, "CREATE TYPE db1.udt AS ENUM ('a', 'b', 'c')")
		sqlDB.Exec(t, "CREATE TABLE db1.udt_table (pk INT PRIMARY KEY, val1 db1.udt)")
		udtCatalog, err := extractCatalog("db1.udt_table")
		require.NoError(t, err)
		require.Equal(t, "udt", udtCatalog.Types[0].Name)
		require.Equal(t, "_udt", udtCatalog.Types[1].Name)

		require.ErrorContains(t,
			sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
				_, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, udtCatalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"udt_table"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
				return err
			}),
			"user-defined types are not supported in CREATE TABLE mode",
		)
	})

	t.Run("multiple column families", func(t *testing.T) {
		sqlDB.Exec(t, "CREATE TABLE db1.multi_cf (a INT PRIMARY KEY, b INT, FAMILY f1 (a), FAMILY f2 (b))")
		catalog, err := extractCatalog("db1.multi_cf")
		require.NoError(t, err)

		require.ErrorContains(t,
			sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
				_, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, catalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"multi_cf"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
				return err
			}),
			"more than one column family",
		)
	})

	t.Run("unique without index", func(t *testing.T) {
		sqlDB.Exec(t, "SET experimental_enable_unique_without_index_constraints = true")
		sqlDB.Exec(t, "CREATE TABLE db1.uwi (a INT PRIMARY KEY, b INT UNIQUE WITHOUT INDEX)")
		catalog, err := extractCatalog("db1.uwi")
		require.NoError(t, err)

		require.ErrorContains(t,
			sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
				_, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, catalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"uwi"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
				return err
			}),
			"UNIQUE WITHOUT INDEX",
		)
	})

	t.Run("composite types in primary key", func(t *testing.T) {
		sqlDB.Exec(t, "CREATE TABLE db1.composite_pk (a FLOAT PRIMARY KEY)")
		catalog, err := extractCatalog("db1.composite_pk")
		require.NoError(t, err)

		require.ErrorContains(t,
			sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
				_, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, catalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"composite_pk"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
				return err
			}),
			"composite encoding",
		)
	})

	t.Run("virtual computed column in primary key", func(t *testing.T) {
		sqlDB.Exec(t, "CREATE TABLE db1.virtual_pk (a INT, b INT AS (a + 1) VIRTUAL, PRIMARY KEY (a, b))")
		catalog, err := extractCatalog("db1.virtual_pk")
		require.NoError(t, err)

		require.ErrorContains(t,
			sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
				_, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, catalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"virtual_pk"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
				return err
			}),
			"virtual computed column",
		)
	})

	t.Run("sequence reference", func(t *testing.T) {
		sqlDB.Exec(t, "CREATE SEQUENCE db1.seq")
		sqlDB.Exec(t, "CREATE TABLE db1.seq_table (a INT PRIMARY KEY DEFAULT nextval('db1.seq'))")
		catalog, err := extractCatalog("db1.seq_table")
		require.NoError(t, err)

		require.ErrorContains(t,
			sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
				_, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, catalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"seq_table"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
				return err
			}),
			"references sequences",
		)
	})

	t.Run("partial index with kv writer", func(t *testing.T) {
		sqlDB.Exec(t, "CREATE TABLE db1.partial_idx (a INT PRIMARY KEY, b INT, INDEX (b) WHERE b > 0)")
		catalog, err := extractCatalog("db1.partial_idx")
		require.NoError(t, err)

		require.ErrorContains(t,
			sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
				_, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, catalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"partial_idx"}, false /* skipForeignKeys */, true /* requireKvWriterCompatible */)
				return err
			}),
			"partial index",
		)
	})

	t.Run("function reference", func(t *testing.T) {
		sqlDB.Exec(t, "USE db1")
		sqlDB.Exec(t, "CREATE FUNCTION my_func() RETURNS INT LANGUAGE SQL AS $$ SELECT 1 $$")
		sqlDB.Exec(t, "CREATE TABLE func_table (a INT PRIMARY KEY, b INT DEFAULT my_func())")
		sqlDB.Exec(t, "USE defaultdb")
		catalog, err := extractCatalog("db1.public.func_table")
		require.NoError(t, err)

		require.ErrorContains(t,
			sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
				_, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, catalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"func_table"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
				return err
			}),
			"references functions",
		)
	})

	t.Run("trigger", func(t *testing.T) {
		sqlDB.Exec(t, "USE db1")
		sqlDB.Exec(t, "CREATE TABLE trigger_table (a INT PRIMARY KEY)")
		sqlDB.Exec(t, "CREATE FUNCTION trigger_func() RETURNS TRIGGER LANGUAGE PLpgSQL AS $$ BEGIN RETURN NEW; END $$")
		sqlDB.Exec(t, "CREATE TRIGGER my_trigger BEFORE INSERT ON trigger_table FOR EACH ROW EXECUTE FUNCTION trigger_func()")
		sqlDB.Exec(t, "USE defaultdb")
		catalog, err := extractCatalog("db1.public.trigger_table")
		require.NoError(t, err)

		require.ErrorContains(t,
			sqltestutils.TestingDescsTxn(ctx, srv, func(ctx context.Context, txn isql.Txn, col *descs.Collection) error {
				_, err = IngestExternalCatalog(ctx, &execCfg, sqlUser, catalog, txn, col, defaultdbID, defaultdbpublicID, false, []string{"trigger_table"}, false /* skipForeignKeys */, false /* requireKvWriterCompatible */)
				return err
			}),
			"references triggers",
		)
	})
}
