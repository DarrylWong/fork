// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/sqlutils"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/stretchr/testify/require"
)

// discoverSchemaFromDDL creates a database, executes DDL, and returns
// the discovered Schema.
func discoverSchemaFromDDL(
	t *testing.T,
	srv serverutils.TestServerInterface,
	sqlDB *sqlutils.SQLRunner,
	dbName string,
	ddl string,
) *Schema {
	t.Helper()
	sqlDB.Exec(t, fmt.Sprintf("CREATE DATABASE %s", tree.NameString(dbName)))
	testDB := srv.ApplicationLayer().SQLConn(t, serverutils.DBName(dbName))
	testSQL := sqlutils.MakeSQLRunner(testDB)
	for _, stmt := range strings.Split(ddl, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		testSQL.Exec(t, stmt)
	}
	s, err := DiscoverSchema(testDB, dbName)
	require.NoError(t, err)
	return s
}

func graphTableNames(g *FKGraph) []string {
	names := make([]string, 0, len(g.Tables))
	for name := range g.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestBuildFKGraphs(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	tests := []struct {
		name           string
		ddl            string
		expectedGraphs [][]string // sorted table names per graph
	}{
		{
			// Each FK uses a different column, so no column overlap.
			// Each FK edge is its own graph.
			name: "simple_chain_no_overlap",
			ddl: `
				CREATE TABLE a (id INT PRIMARY KEY);
				CREATE TABLE b (id INT PRIMARY KEY, a_id INT NOT NULL REFERENCES a(id));
				CREATE TABLE c (id INT PRIMARY KEY, b_id INT NOT NULL REFERENCES b(id))`,
			expectedGraphs: [][]string{{"a", "b"}, {"b", "c"}},
		},
		{
			// Both FKs reference the same UC (a_pkey), so they get unioned
			// through inboundToUC → one graph.
			name: "fan_out",
			ddl: `
				CREATE TABLE a (id INT PRIMARY KEY);
				CREATE TABLE b (id INT PRIMARY KEY, a_id INT NOT NULL REFERENCES a(id));
				CREATE TABLE c (id INT PRIMARY KEY, a_id INT NOT NULL REFERENCES a(id))`,
			expectedGraphs: [][]string{{"a", "b", "c"}},
		},
		{
			// b and c both reference a_pkey → unioned into one graph.
			// d references b_pkey and c_pkey separately, each its own graph.
			name: "diamond_partial",
			ddl: `
				CREATE TABLE a (id INT PRIMARY KEY);
				CREATE TABLE b (id INT PRIMARY KEY, a_id INT NOT NULL REFERENCES a(id));
				CREATE TABLE c (id INT PRIMARY KEY, a_id INT NOT NULL REFERENCES a(id));
				CREATE TABLE d (id INT PRIMARY KEY, b_id INT NOT NULL REFERENCES b(id), c_id INT NOT NULL REFERENCES c(id))`,
			expectedGraphs: [][]string{{"a", "b", "c"}, {"b", "d"}, {"c", "d"}},
		},
		{
			name: "two_disconnected_fks",
			ddl: `
				CREATE TABLE a (id INT PRIMARY KEY);
				CREATE TABLE b (id INT PRIMARY KEY, a_id INT NOT NULL REFERENCES a(id));
				CREATE TABLE c (id INT PRIMARY KEY);
				CREATE TABLE d (id INT PRIMARY KEY, c_id INT NOT NULL REFERENCES c(id))`,
			expectedGraphs: [][]string{{"a", "b"}, {"c", "d"}},
		},
		{
			name: "transitive_overlap",
			ddl: `
				CREATE TABLE countries (country_code STRING PRIMARY KEY);
				CREATE TABLE regions (
					country_code STRING NOT NULL REFERENCES countries(country_code),
					region_name STRING NOT NULL,
					PRIMARY KEY (country_code, region_name));
				CREATE TABLE stores (
					id INT PRIMARY KEY,
					country_code STRING NOT NULL,
					region_name STRING NOT NULL,
					FOREIGN KEY (country_code, region_name) REFERENCES regions(country_code, region_name))`,
			expectedGraphs: [][]string{{"countries", "regions", "stores"}},
		},
		{
			name: "non_transitive",
			ddl: `
				CREATE TABLE countries (country_code STRING PRIMARY KEY);
				CREATE TABLE regions (
					country_code STRING NOT NULL REFERENCES countries(country_code),
					region_name STRING NOT NULL,
					PRIMARY KEY (country_code, region_name));
				CREATE TABLE stores (
					store_id INT PRIMARY KEY,
					country_code STRING NOT NULL,
					region_name STRING NOT NULL,
					FOREIGN KEY (country_code, region_name) REFERENCES regions(country_code, region_name));
				CREATE TABLE inventory (id INT PRIMARY KEY, store_id INT NOT NULL REFERENCES stores(store_id))`,
			expectedGraphs: [][]string{
				{"countries", "regions", "stores"},
				{"inventory", "stores"},
			},
		},
		{
			// Diamond via composite keys: org -> dept and org -> team share
			// org_id through org's PK. dept and team both carry org_id as
			// part of their PK, so projects referencing both via composite
			// FKs that include org_id creates overlap and merges everything.
			name: "diamond_composite",
			ddl: `
				CREATE TABLE orgs (org_id INT PRIMARY KEY);
				CREATE TABLE depts (
					org_id INT NOT NULL REFERENCES orgs(org_id),
					dept_id INT NOT NULL,
					PRIMARY KEY (org_id, dept_id));
				CREATE TABLE teams (
					org_id INT NOT NULL REFERENCES orgs(org_id),
					team_id INT NOT NULL,
					PRIMARY KEY (org_id, team_id));
				CREATE TABLE projects (
					id INT PRIMARY KEY,
					org_id INT NOT NULL,
					dept_id INT NOT NULL,
					team_id INT NOT NULL,
					FOREIGN KEY (org_id, dept_id) REFERENCES depts(org_id, dept_id),
					FOREIGN KEY (org_id, team_id) REFERENCES teams(org_id, team_id))`,
			// org_id overlaps across all FKs → one graph.
			expectedGraphs: [][]string{{"depts", "orgs", "projects", "teams"}},
		},
		{
			// Deep transitive chain via composite keys. Each level adds a
			// column to the PK and carries forward all parent columns.
			name: "deep_transitive_chain",
			ddl: `
				CREATE TABLE l1 (a INT PRIMARY KEY);
				CREATE TABLE l2 (
					a INT NOT NULL REFERENCES l1(a),
					b INT NOT NULL,
					PRIMARY KEY (a, b));
				CREATE TABLE l3 (
					a INT NOT NULL,
					b INT NOT NULL,
					c INT NOT NULL,
					PRIMARY KEY (a, b, c),
					FOREIGN KEY (a, b) REFERENCES l2(a, b));
				CREATE TABLE l4 (
					a INT NOT NULL,
					b INT NOT NULL,
					c INT NOT NULL,
					d INT NOT NULL,
					PRIMARY KEY (a, b, c, d),
					FOREIGN KEY (a, b, c) REFERENCES l3(a, b, c))`,
			// Column "a" threads through every level → one graph.
			expectedGraphs: [][]string{{"l1", "l2", "l3", "l4"}},
		},
		{
			// Fan-in: multiple children reference the same parent via
			// composite FK that shares a column with the parent's outbound FK.
			name: "fan_in_composite",
			ddl: `
				CREATE TABLE tenants (tenant_id INT PRIMARY KEY);
				CREATE TABLE users (
					tenant_id INT NOT NULL REFERENCES tenants(tenant_id),
					user_id INT NOT NULL,
					PRIMARY KEY (tenant_id, user_id));
				CREATE TABLE posts (
					id INT PRIMARY KEY,
					tenant_id INT NOT NULL,
					user_id INT NOT NULL,
					FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, user_id));
				CREATE TABLE comments (
					id INT PRIMARY KEY,
					tenant_id INT NOT NULL,
					user_id INT NOT NULL,
					FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, user_id))`,
			// tenant_id overlaps between users→tenants FK and the UC on
			// users that posts and comments reference → one graph.
			expectedGraphs: [][]string{{"comments", "posts", "tenants", "users"}},
		},
		{
			// Mixed: one subgraph with overlap, another without. The two
			// subgraphs should be separate FKGraphs.
			name: "mixed_overlap_and_isolated",
			ddl: `
				CREATE TABLE tenants (tenant_id INT PRIMARY KEY);
				CREATE TABLE users (
					tenant_id INT NOT NULL REFERENCES tenants(tenant_id),
					user_id INT NOT NULL,
					PRIMARY KEY (tenant_id, user_id));
				CREATE TABLE posts (
					id INT PRIMARY KEY,
					tenant_id INT NOT NULL,
					user_id INT NOT NULL,
					FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, user_id));
				CREATE TABLE configs (id INT PRIMARY KEY);
				CREATE TABLE settings (id INT PRIMARY KEY, config_id INT NOT NULL REFERENCES configs(id))`,
			expectedGraphs: [][]string{
				{"configs", "settings"},
				{"posts", "tenants", "users"},
			},
		},
		{
			name: "self_referencing",
			ddl:  `CREATE TABLE employees (id INT PRIMARY KEY, manager_id INT REFERENCES employees(id))`,
			expectedGraphs: [][]string{{"employees"}},
		},
		{
			name:           "no_fks",
			ddl:            `CREATE TABLE standalone1 (id INT PRIMARY KEY); CREATE TABLE standalone2 (id INT PRIMARY KEY)`,
			expectedGraphs: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := discoverSchemaFromDDL(t, srv, sqlDB, "bg_"+tc.name, tc.ddl)
			graphs := BuildFKGraphs(s)

			if tc.expectedGraphs == nil {
				require.Empty(t, graphs)
				return
			}

			require.Len(t, graphs, len(tc.expectedGraphs))
			for i, g := range graphs {
				require.Equal(t, tc.expectedGraphs[i], graphTableNames(g))
			}
		})
	}
}

func TestTopologicalSort(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	tests := []struct {
		name          string
		ddl           string
		expectedOrder []string // for the first graph
		expectedErr   string
	}{
		{
			// Transitive overlap creates one graph with all three tables.
			name: "transitive_chain",
			ddl: `
				CREATE TABLE countries (country_code STRING PRIMARY KEY);
				CREATE TABLE regions (
					country_code STRING NOT NULL REFERENCES countries(country_code),
					region_name STRING NOT NULL,
					PRIMARY KEY (country_code, region_name));
				CREATE TABLE stores (
					id INT PRIMARY KEY,
					country_code STRING NOT NULL,
					region_name STRING NOT NULL,
					FOREIGN KEY (country_code, region_name) REFERENCES regions(country_code, region_name))`,
			expectedOrder: []string{"countries", "regions", "stores"},
		},
		{
			// Simple two-table FK: parent before child.
			name: "simple_pair",
			ddl: `
				CREATE TABLE a (id INT PRIMARY KEY);
				CREATE TABLE b (id INT PRIMARY KEY, a_id INT NOT NULL REFERENCES a(id))`,
			expectedOrder: []string{"a", "b"},
		},
		{
			name: "diamond_composite",
			ddl: `
				CREATE TABLE orgs (org_id INT PRIMARY KEY);
				CREATE TABLE depts (
					org_id INT NOT NULL REFERENCES orgs(org_id),
					dept_id INT NOT NULL,
					PRIMARY KEY (org_id, dept_id));
				CREATE TABLE teams (
					org_id INT NOT NULL REFERENCES orgs(org_id),
					team_id INT NOT NULL,
					PRIMARY KEY (org_id, team_id));
				CREATE TABLE projects (
					id INT PRIMARY KEY,
					org_id INT NOT NULL,
					dept_id INT NOT NULL,
					team_id INT NOT NULL,
					FOREIGN KEY (org_id, dept_id) REFERENCES depts(org_id, dept_id),
					FOREIGN KEY (org_id, team_id) REFERENCES teams(org_id, team_id))`,
			expectedOrder: []string{"orgs", "depts", "teams", "projects"},
		},
		{
			name: "deep_chain",
			ddl: `
				CREATE TABLE l1 (a INT PRIMARY KEY);
				CREATE TABLE l2 (
					a INT NOT NULL REFERENCES l1(a),
					b INT NOT NULL,
					PRIMARY KEY (a, b));
				CREATE TABLE l3 (
					a INT NOT NULL,
					b INT NOT NULL,
					c INT NOT NULL,
					PRIMARY KEY (a, b, c),
					FOREIGN KEY (a, b) REFERENCES l2(a, b));
				CREATE TABLE l4 (
					a INT NOT NULL,
					b INT NOT NULL,
					c INT NOT NULL,
					d INT NOT NULL,
					PRIMARY KEY (a, b, c, d),
					FOREIGN KEY (a, b, c) REFERENCES l3(a, b, c))`,
			expectedOrder: []string{"l1", "l2", "l3", "l4"},
		},
		{
			// Self-refs don't affect inter-table ordering — the table can
			// still be placed. The txn generator handles self-refs separately
			// (NULL for first rows, then back-fill).
			name: "self_ref",
			ddl:  `CREATE TABLE employees (id INT PRIMARY KEY, manager_id INT REFERENCES employees(id))`,
			expectedOrder: []string{"employees"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := discoverSchemaFromDDL(t, srv, sqlDB, "ts_"+tc.name, tc.ddl)
			graphs := BuildFKGraphs(s)
			require.NotEmpty(t, graphs)

			sorted, err := graphs[0].TopologicalSort()
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
				return
			}
			require.NoError(t, err)
			names := make([]string, len(sorted))
			for i, tbl := range sorted {
				names[i] = tbl.Name
			}
			require.Equal(t, tc.expectedOrder, names)
		})
	}
}

func TestHasCycle(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	tests := []struct {
		name     string
		ddl      string
		expected bool
	}{
		{
			name: "simple_pair_no_cycle",
			ddl: `
				CREATE TABLE a (id INT PRIMARY KEY);
				CREATE TABLE b (id INT PRIMARY KEY, a_id INT NOT NULL REFERENCES a(id))`,
			expected: false,
		},
		{
			name:     "self_ref",
			ddl:      `CREATE TABLE employees (id INT PRIMARY KEY, manager_id INT REFERENCES employees(id))`,
			expected: true,
		},
		{
			// Transitive overlap creates one acyclic graph.
			name: "transitive_no_cycle",
			ddl: `
				CREATE TABLE countries (country_code STRING PRIMARY KEY);
				CREATE TABLE regions (
					country_code STRING NOT NULL REFERENCES countries(country_code),
					region_name STRING NOT NULL,
					PRIMARY KEY (country_code, region_name));
				CREATE TABLE stores (
					id INT PRIMARY KEY,
					country_code STRING NOT NULL,
					region_name STRING NOT NULL,
					FOREIGN KEY (country_code, region_name) REFERENCES regions(country_code, region_name))`,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := discoverSchemaFromDDL(t, srv, sqlDB, "hc_"+tc.name, tc.ddl)
			graphs := BuildFKGraphs(s)
			require.NotEmpty(t, graphs)
			require.Equal(t, tc.expected, graphs[0].HasCycle())
		})
	}
}

func TestFindCycles(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	tests := []struct {
		name           string
		ddl            string
		expectedCycles [][]string
	}{
		{
			name: "simple_pair_no_cycles",
			ddl: `
				CREATE TABLE a (id INT PRIMARY KEY);
				CREATE TABLE b (id INT PRIMARY KEY, a_id INT NOT NULL REFERENCES a(id))`,
			expectedCycles: nil,
		},
		{
			name:           "self_ref",
			ddl:            `CREATE TABLE employees (id INT PRIMARY KEY, manager_id INT REFERENCES employees(id))`,
			expectedCycles: [][]string{{"employees"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := discoverSchemaFromDDL(t, srv, sqlDB, "fc_"+tc.name, tc.ddl)
			graphs := BuildFKGraphs(s)
			require.NotEmpty(t, graphs)

			cycles := graphs[0].FindCycles()
			if tc.expectedCycles == nil {
				require.Empty(t, cycles)
				return
			}
			require.Equal(t, tc.expectedCycles, cycles)
		})
	}

	// Mutual cycle A→B→A: FK columns don't overlap with PK columns, so
	// the two FK edges end up in separate graphs. Neither individual
	// graph has a cycle — the cycle only exists across graphs.
	t.Run("mutual_across_graphs", func(t *testing.T) {
		s := discoverSchemaFromDDL(t, srv, sqlDB, "fc_mutual", `
			CREATE TABLE a (id INT PRIMARY KEY, b_id INT);
			CREATE TABLE b (id INT PRIMARY KEY, a_id INT REFERENCES a(id));
			ALTER TABLE a ADD CONSTRAINT a_b_fkey FOREIGN KEY (b_id) REFERENCES b(id)`)
		graphs := BuildFKGraphs(s)
		require.Len(t, graphs, 2)
		for _, g := range graphs {
			require.False(t, g.HasCycle())
			require.Empty(t, g.FindCycles())
		}
	})
}
