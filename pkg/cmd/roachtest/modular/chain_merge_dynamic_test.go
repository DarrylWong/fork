package modular

import (
	"io"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/testutils/echotest"
)

func TestChainMergingDynamic(t *testing.T) {
	testCases := []struct {
		name   string
		chains []chain
	}{
		{
			// Test that dynamic steps with resource access declarations work with chain merging.
			// This verifies that WithDynamicResourceAccess is properly handled during merge.
			name: "dynamic_steps_with_locks",
			chains: []chain{
				// Chain 1a: Regular steps
				NewOperation(NewStep("chain 1: noop before", noop)).
					Then(NewStep("chain 1: noop middle", noop)).Chain,

				// Chain 1b: Dynamic step with cluster setting lock
				NewOperation(NewStep("chain 1 dynamic: plan index creation", noop)).
					Then(
						NewDynamicOperation[string]("chain 1 dynamic: create index with lock").
							PrePlan(noopPrePlan).
							WithRun(noopRun).
							WithDynamicResourceAccess(
								ClusterSettingAccess{
									Name: "kv.bulk_io_write.concurrent_export_requests",
								}.Resource(true),
							),
					).Chain,

				// Chain 2: Conflicts with chain 1's dynamic step (same cluster setting)
				NewOperation(
					NewDynamicOperation[string]("chain 2 dynamic: modify cluster setting").
						PrePlan(noopPrePlan).
						WithRun(noopRun).
						WithDynamicResourceCallback(func(s string) ([]ResourceAccess, []ResourceAccess) {
							access := ClusterSettingAccess{
								Name: "kv.bulk_io_write.concurrent_export_requests",
							}.Resource(true)
							return []ResourceAccess{access}, []ResourceAccess{access}
						}),
				).Then(NewStep("chain 2: noop after", noop)).Chain,

				// Chain 3: No conflict (different cluster setting)
				NewOperation(
					NewDynamicOperation[string]("chain 3 dynamic: modify different setting").
						PrePlan(noopPrePlan).
						WithRun(noopRun).
						WithDynamicResourceCallback(func(s string) ([]ResourceAccess, []ResourceAccess) {
							access := ClusterSettingAccess{
								Name: "storage.sstable.compression_algorithm",
							}.Resource(true)
							return []ResourceAccess{access}, []ResourceAccess{access}
						}),
				).Chain,
			},
		},
		{
			// Test mixing regular and dynamic steps with schema change locks
			name: "mixed_steps_schema_locks",
			chains: []chain{
				// Chain 1: Mix of regular and dynamic steps on same table
				NewOperation(NewStep("chain 1: validate table", noop)).
					Then(NewStep("chain 1: acquire schema lock", noop,
						AcquireLock(SchemaChangeAccess{
							Database: "db1",
							Table:    "users",
						}))).Chain,
				NewOperation(
					NewStep("chain 1: regular step before dynamic", noop),
				).Then(
					NewDynamicOperation[string]("chain 1 dynamic: add index").
						PrePlan(noopPrePlan).
						WithRun(noopRun).
						WithDynamicResourceAccess(
							SchemaChangeAccess{
								Database: "db1",
								Table:    "users",
							}.Resource(true),
						),
				).Then(NewStep("chain 1: release schema lock", noop,
					ReleaseLock(SchemaChangeAccess{
						Database: "db1",
						Table:    "users",
					}))).Chain,

				// Chain 2: Pure dynamic operation on different table (no conflict)
				NewOperation(
					NewDynamicOperation[string]("chain 2 dynamic: modify table2").
						PrePlan(noopPrePlan).
						WithRun(noopRun).
						WithDynamicResourceCallback(func(s string) ([]ResourceAccess, []ResourceAccess) {
							access := SchemaChangeAccess{
								Database: "db1",
								Table:    "orders",
							}.Resource(true)
							return []ResourceAccess{access}, []ResourceAccess{access}
						}),
				).Chain,

				// Chain 3: Conflicts with chain 1 (same table)
				NewOperation(NewStep("chain 3: noop", noop)).
					Then(NewStep("chain 3: schema change on users", noop,
						AcquireAndReleaseLock(SchemaChangeAccess{
							Database: "db1",
							Table:    "users",
						}))).Chain,
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
