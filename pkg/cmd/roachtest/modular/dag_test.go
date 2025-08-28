package modular

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/testutils/echotest"
	"github.com/golang/mock/gomock"
	"path/filepath"
)

// TestPlanString tests the String() method of TestPlan.
func TestPlanner(t *testing.T) {
	mod := NewTest("basic plan", 12345)
	ctrl := gomock.NewController(t)
	mod.WithCreateClusterFn(func() cluster.Cluster {
		mockCluster := cluster.NewMockCluster(ctrl)
		mockCluster.EXPECT().Name().Return("my-cluster").AnyTimes()
		return mockCluster
	})

	// Add cluster with options
	_ = mod.AddCluster(
		MinNodes(3),
		MaxNodes(5),
		DisabledDeploymentModes(SeparateProcessDeployment),
	)

	// Add workload cluster
	_ = mod.AddWorkloadCluster(WorkloadNodeCount(1))

	baselineStage := mod.NewStage("baseline", DisableFailureInjection())
	chaosStage := mod.NewStage("chaos")

	// Install prometheus and grafana, then after that is done, start a background loop to scrape metrics.
	mod.Setup("installing prom" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		l.Printf("Installing Prometheus and Grafana")
		return nil
	})
	mod.Setup("starting histogram scrape loop" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		l.Printf("starting histogram scrape loop")
		time.Sleep(5 * time.Second)
		l.Printf("measured perf as 400 qps")
		return nil
	}, InBackground())
	mod.Setup("importing tpcc workload" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		l.Printf("importing TPCC workload")
		return nil
	})

	for _, stage := range []*Stage{baselineStage, chaosStage} {
		// Run TPCC workload
		mod.InStage("running TPCC workload for 1 hour" /* step name */, stage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("running TPCC workload for 1 hour")
			return nil
		}).Then("dropping TPCC tables" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("dropping TPCC tables")
			return nil
		})

		mod.InStage("increasing replication factor to 5" /* step name */, stage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("increasing replication factor to 5")
			return nil
		}).Then("waiting for replication" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("waiting for replication")
			return nil
		}).Then("sleeping for 10 minutes" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("sleeping for 10 minutes")
			return nil
		}).Then("decreasing replication factor to 3" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("decreasing replication factor to 3")
			return nil
		})

		mod.InStage("copy bank table" /* step name */, stage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("copy bank table")
			return nil
		})
	}

	// Add after-test step, which is run after prometheus scraping is turned off.
	mod.AfterTest("TPCC consistency checks", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		l.Printf("running TPCC consistency checks")
		return nil
	})

	// Use the planner to generate the plan with randomized execution strategies
	planner := NewSimplePlanner(mod, PlannerConfig{
		IsLocal:           true,
		ConcurrencyChance: 0.25,
	})

	plan, err := planner.Plan()
	if err != nil {
		t.Fatalf("Failed to generate plan: %v", err)
	}

	t.Log(plan)
}

// TestBasicDAG tests the DAG generation using an echo test.
func TestBasicDAG(t *testing.T) {
	mod := NewTest("basic plan", 123456)

	baselineStage := mod.NewStage("baseline", DisableFailureInjection())
	chaosStage := mod.NewStage("chaos")

	// Install prometheus and grafana, then after that is done, start a background loop to scrape metrics.
	mod.Setup("installing prom" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.Setup("starting histogram scrape loop" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}, InBackground())
	mod.Setup("importing tpcc workload" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	for _, stage := range []*Stage{baselineStage, chaosStage} {
		// Run TPCC workload
		mod.InStage("running TPCC workload for 1 hour" /* step name */, stage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("dropping TPCC tables" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		})

		mod.InStage("increasing replication factor to 5" /* step name */, stage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("waiting for replication" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("sleeping for 10 minutes" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("decreasing replication factor to 3" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		})

		mod.InStage("copy bank table" /* step name */, stage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		})

		// TODO: the framework itself should add this automatically when chaos is enabled.
		if stage == chaosStage {
			mod.InStage("inject network partition" /* step name */, stage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
				return nil
			}).Then("recover from network partition" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
				return nil
			})
		}

	}

	// Add after-test step, which is run after prometheus scraping is turned off.
	mod.AfterTest("TPCC consistency checks", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	// Use the planner to generate the plan with randomized execution strategies
	planner := NewSimplePlanner(mod, PlannerConfig{
		IsLocal:           true,
		ConcurrencyChance: 0.25,
	})

	plan, err := planner.Plan()
	if err != nil {
		t.Fatalf("Failed to generate plan: %v", err)
	}

	t.Log(plan)

	echotest.Require(t, mod.DAG(), filepath.Join("testdata", "basic_dag"))
}

func TestSetupOnlyDAG(t *testing.T) {
	mod := NewTest("basic plan", 12345)

	// Install prometheus and grafana, then after that is done, start a background loop to scrape metrics.
	mod.Setup("installing prom" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.Setup("starting histogram scrape loop" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}, InBackground())
	mod.Setup("importing tpcc workload" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	echotest.Require(t, mod.DAG(), filepath.Join("testdata", "setup_only_dag"))
}

func TestMVTDAG(t *testing.T) {
	mod := NewTest("mixed version plan", 12345)

	// Cluster init steps are done sequentially.
	mod.Setup("install fixtures for version \"v24.2.2\"" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.Setup("start cluster at version \"v24.2.2\"" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}, InBackground())
	mod.Setup("wait for all nodes (:1-4) to acknowledge cluster version '24.2' on system tenant" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	upgradeStage := mod.NewStage("upgrade cluster from \"v24.2.2\" to \"master\"", DisableFailureInjection())
	mod.InStage("restart system server on node 1 with binary version master", upgradeStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage("restart system server on node 2 with binary version master", upgradeStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage("restart system server on node 3 with binary version master", upgradeStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage("restart system server on node 4 with binary version master", upgradeStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage("run backup", upgradeStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage("test features", upgradeStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	echotest.Require(t, mod.DAG(), filepath.Join("testdata", "setup_only_dag"))
}

func TestAfterTestOnlyDAG(t *testing.T) {
	mod := NewTest("basic plan", 12345)

	mod.AfterTest("TPCC consistency checks", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	echotest.Require(t, mod.DAG(), filepath.Join("testdata", "after_test_only_dag"))
}
