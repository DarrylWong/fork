package modular

import (
	"context"
	"io"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/testutils/echotest"
)

// noop is a no-op step function for tests
var noop = func(context.Context, *logger.Logger, *Helper) error {
	return nil
}

// noopPrePlan is a no-op PrePlan function for dynamic steps in tests
var noopPrePlan = func(context.Context, *logger.Logger, *Helper) (string, error) {
	return "plan-result", nil
}

// noopRun is a no-op Run function for dynamic steps in tests
var noopRun = func(ctx context.Context, l *logger.Logger, h *Helper, plan string) error {
	return nil
}

// testOpBuilder wraps OperationBuilder to provide the old API for backward compatibility in tests.
type testOpBuilder struct {
	*OperationBuilder
}

// testNewOperation is a test helper that creates an operation using the old API signature.
// This is a temporary bridge to avoid updating all test cases.
func testNewOperation(name string, fn stepFunc, opts ...StepOption) *testOpBuilder {
	return &testOpBuilder{NewOperation(NewStep(name, fn, opts...))}
}

// Then adds a step using the old API signature.
func (tob *testOpBuilder) Then(name string, fn stepFunc, opts ...StepOption) *testOpBuilder {
	tob.OperationBuilder = tob.OperationBuilder.Then(NewStep(name, fn, opts...))
	return tob
}

func TestChainMerging(t *testing.T) {
	testCases := []struct {
		name   string
		chains []chain
	}{
		{
			name: "two_conflicting_chains",
			chains: []chain{
				testNewOperation("chain 1: noop before", noop).
					Then("chain 1: modify storage.sstable.compression_algorithm", noop,
						AcquireLock(ClusterSettingAccess{
							Name: "storage.sstable.compression_algorithm",
						})).
					Then("chain 1: noop inside", noop).
					Then("chain 1: release cluster_setting", noop,
						ReleaseLock(ClusterSettingAccess{
							Name: "storage.sstable.compression_algorithm",
						})).
					Then("chain 1: noop after", noop).
					Chain,

				testNewOperation("chain 2: noop before", noop).
					Then("chain 2: acquire cluster_setting", noop,
						AcquireLock(ClusterSettingAccess{
							Name: "storage.sstable.compression_algorithm",
						})).
					Then("chain 2: noop inside", noop).
					Then("chain 2: release cluster_setting", noop,
						ReleaseLock(ClusterSettingAccess{
							Name: "storage.sstable.compression_algorithm",
						})).
					Then("chain 2: noop after", noop).
					Chain,
			},
		},
		{
			name: "transitive_conflict",
			chains: []chain{
				testNewOperation("chain 1: noop before", noop).
					Then("chain 1: acquire cluster_setting", noop,
						AcquireLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						})).
					Then("chain 1: noop inside", noop).
					Then("chain 1: release cluster_setting", noop,
						ReleaseLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						})).
					Then("chain 1: noop after", noop).
					Chain,
				testNewOperation("chain 2: noop before", noop).
					Then("chain 2: acquire schema", noop,
						AcquireLock(SchemaChangeAccess{
							Database: "public",
							Table:    "users",
						})).
					Then("chain 2: noop inside", noop).
					Then("chain 2: release schema", noop,
						ReleaseLock(SchemaChangeAccess{
							Database: "public",
							Table:    "users",
						})).
					Then("chain 2: noop after", noop).
					Chain,
				testNewOperation("chain 3: noop before", noop).
					Then("chain 3: acquire cluster_setting, schema", noop,
						AcquireLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						}),
						AcquireLock(SchemaChangeAccess{
							Database: "public",
							Table:    "users",
						})).
					Then("chain 3: noop inside", noop).
					Then("chain 3: release cluster_setting, schema", noop,
						ReleaseLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						}),
						ReleaseLock(SchemaChangeAccess{
							Database: "public",
							Table:    "users",
						})).
					Then("chain 3: noop after", noop).
					Chain,
			},
		},
		{
			name: "no_conflict",
			chains: []chain{
				testNewOperation("chain 1: noop before", noop).
					Then("chain 1: acquire cluster_setting", noop,
						AcquireLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						})).
					Then("chain 1: noop inside", noop).
					Then("chain 1: release cluster_setting", noop,
						ReleaseLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						})).
					Then("chain 1: noop after", noop).
					Chain,
				testNewOperation("chain 2: noop before", noop).
					Then("chain 2: acquire schema", noop,
						AcquireLock(SchemaChangeAccess{
							Database: "public",
							Table:    "users",
						})).
					Then("chain 2: noop inside", noop).
					Then("chain 2: release schema", noop,
						ReleaseLock(SchemaChangeAccess{
							Database: "public",
							Table:    "users",
						})).
					Then("chain 2: noop after", noop).
					Chain,
			},
		},
		{
			name: "partial_conflict",
			chains: []chain{
				testNewOperation("chain 1: noop before", noop).
					Then("chain 1: acquire cluster_setting", noop,
						AcquireLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						})).
					Then("chain 1: noop inside", noop).
					Then("chain 1: release cluster_setting", noop,
						ReleaseLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						})).
					Then("chain 1: noop after", noop).
					Chain,
				testNewOperation("chain 2: noop before", noop).
					Then("chain 2: acquire schema", noop,
						AcquireLock(SchemaChangeAccess{
							Database: "public",
							Table:    "users",
						})).
					Then("chain 2: noop inside", noop).
					Then("chain 2: release schema", noop,
						ReleaseLock(SchemaChangeAccess{
							Database: "public",
							Table:    "users",
						})).
					Then("chain 2: noop after", noop).
					Chain,
				testNewOperation("chain 3: noop before", noop).
					Then("chain 3: acquire cluster_setting", noop,
						AcquireLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						})).
					Then("chain 3: noop inside", noop).
					Then("chain 3: release cluster_setting", noop,
						ReleaseLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						})).
					Then("chain 3: noop after", noop).
					Chain,
				testNewOperation("chain 4: noop 1", noop).
					Then("chain 4: noop 2", noop).
					Then("chain 4: noop 3", noop).
					Chain,
			},
		},
		{
			name: "merge_interleaved",
			chains: []chain{
				testNewOperation("chain 1: noop before", noop).
					Then("chain 1: acquire cluster_setting", noop,
						AcquireLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						})).
					Then("chain 1: noop inside", noop).
					Then("chain 1: release cluster_setting", noop,
						ReleaseLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						})).
					Then("chain 1: noop after", noop).
					Chain,
				testNewOperation("chain 2: noop before", noop).
					Then("chain 2: acquire cluster_setting", noop,
						AcquireLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						})).
					Then("chain 2: noop inside", noop).
					Then("chain 2: release cluster_setting", noop,
						ReleaseLock(ClusterSettingAccess{
							Name: "kv.bulk_io_write.concurrent_export_requests",
						})).
					Then("chain 2: noop after", noop).
					Chain,
			},
		},
		{
			// Test that we detect conflicts when the locks are held
			// on different levels of the resource hierarchy, e.g.
			// a table lock and a database lock.
			name: "hierarchical_conflict",
			chains: []chain{
				testNewOperation("chain 1: backup table A", noop).
					Then("chain 1: restore table A", noop,
						AcquireAndReleaseLock(RestoreAccess{
							Database: "default_db",
							Table:    "table_a",
						})).
					Then("chain 1: noop after", noop).
					Chain,
				testNewOperation("chain 2: backup default_db", noop).
					Then("chain 2: restore default_db", noop,
						AcquireAndReleaseLock(RestoreAccess{
							Database: "default_db",
						})).
					Then("chain 2: noop after", noop).
					Chain,
			},
		},
		{
			// Test that we can lock an entire table.
			name: "lock_entire_table",
			chains: []chain{
				testNewOperation("chain 1: backup db1", noop).
					Then("chain 1: restore db1", noop,
						AcquireAndReleaseLock(RestoreAccess{
							Database: "db1",
						})).
					Chain,
				testNewOperation("chain 2: backup db2.table1", noop).
					Then("chain 2: restore db2.table1", noop,
						AcquireAndReleaseLock(RestoreAccess{
							Database: "db2",
							Table:    "table1",
						})).
					Chain,
				testNewOperation("chain 3: drop db1", noop,
					AcquireLock(DatabaseAccess{
						Database: "db1",
					})).
					Then("chain 3: restore db1", noop,
						ReleaseLock(DatabaseAccess{
							Database: "db1",
						})).
					Chain,
			},
		},
		{
			// Test that two accesses to the same resource don't conflict.
			name: "two_database_access",
			chains: []chain{
				testNewOperation("chain 1: run TPCC workload", noop,
					AcquireAndReleaseAccess(DatabaseAccess{Database: "TPCC"}),
				).Chain,
				testNewOperation("chain 2: backup TPCC database", noop,
					AcquireAndReleaseAccess(DatabaseAccess{Database: "TPCC"}),
				).Chain,
			},
		},
		{
			// Test that access conflicts with a lock on the same resource.
			name: "database_access_and_lock",
			chains: []chain{
				testNewOperation("chain 1: run TPCC workload", noop,
					AcquireAndReleaseAccess(DatabaseAccess{Database: "TPCC"}),
				).Chain,
				testNewOperation("chain 2: DROP TPCC database", noop,
					AcquireAndReleaseLock(DatabaseAccess{Database: "TPCC"}),
				).Chain,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			stage := Stage{
				name:   "test",
				chains: tc.chains,
			}

			nilLogger := func() *logger.Logger {
				cfg := logger.Config{
					Stdout: io.Discard,
					Stderr: io.Discard,
				}
				l, err := cfg.NewLogger("" /* path */)
				if err != nil {
					panic(err)
				}

				return l
			}()

			planner := &TestPlanner{
				logger: nilLogger,
				rng:    rand.New(rand.NewSource(1)),
			}

			var out strings.Builder
			out.WriteString("Before merge:\n")
			out.WriteString(GenerateDAG([]Stage{stage}))
			out.WriteString("\n")

			result, err := planner.maybeMergeChains(stage)
			if err != nil {
				t.Fatalf("failed to merge chains: %v", err)
			}

			out.WriteString("After merge:\n")
			out.WriteString(GenerateDAG([]Stage{result}))

			echotest.Require(t, out.String(), filepath.Join("testdata", "chainmerge", tc.name+".txt"))
		})
	}
}
