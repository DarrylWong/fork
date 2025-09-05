package modular

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/testutils/echotest"
)

// TestBasicDAG tests the DAG generation using an echo test.
func TestBasicDAG(t *testing.T) {
	mod := NewTest("basic plan", 123456)

	baselineStage := mod.NewStage("baseline")
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
		mod.InStage(stage, "running TPCC workload for 1 hour" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("dropping TPCC tables" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		})

		mod.InStage(stage, "increasing replication factor to 5" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("waiting for replication" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("sleeping for 10 minutes" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("decreasing replication factor to 3" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		})

		mod.InStage(stage, "copy bank table" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		})

		// TODO: the framework itself should add this automatically when chaos is enabled.
		if stage == chaosStage {
			mod.InStage(stage, "inject network partition" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
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
	planner := mod.NewPlanner()
	echotest.Require(t, planner.DAG(), filepath.Join("testdata", "basic_dag.txt"))
	plan, err := planner.Plan()
	if err != nil {
		t.Fatalf("Failed to generate plan: %v", err)
	}
	t.Log(plan.String())
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

	planner := mod.NewPlanner()
	echotest.Require(t, planner.DAG(), filepath.Join("testdata", "setup_only_dag.txt"))
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

	upgradeStage := mod.NewStage("upgrade cluster from \"v24.2.2\" to \"master\"")
	mod.InStage(upgradeStage, "restart system server on node 1 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage(upgradeStage, "restart system server on node 2 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage(upgradeStage, "restart system server on node 3 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage(upgradeStage, "restart system server on node 4 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	mod.InStage(upgradeStage, "run backup", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage(upgradeStage, "test features", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	rollbackStage := mod.NewStage("downgrade nodes :1-4 from \"master\" to \"v24.2.2\"")
	mod.InStage(rollbackStage, "restart system server on node 1 with binary version v24.2.2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage(rollbackStage, "restart system server on node 2 with binary version v24.2.2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage(rollbackStage, "restart system server on node 3 with binary version v24.2.2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage(rollbackStage, "restart system server on node 4 with binary version v24.2.2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	mod.InStage(rollbackStage, "run backup", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage(rollbackStage, "test features", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	finalizeStage := mod.NewStage("upgrade cluster from \"v24.2.2\" to \"master\"")
	mod.InStage(finalizeStage, "restart system server on node 1 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).And("restart system server on node 2 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).And("restart system server on node 3 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).And("restart system server on node 4 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).Then("wait for all nodes (:1-4) to acknowledge cluster version <current> on system tenant", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	mod.InStage(finalizeStage, "run backup", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	mod.InStage(finalizeStage, "test features", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	planner := mod.NewPlanner()
	echotest.Require(t, planner.DAG(), filepath.Join("testdata", "mvt_dag.txt"))
}

func TestAndDAG(t *testing.T) {
	mod := NewTest("and synchronization test", 54321)

	stage := mod.NewStage("stage 1")
	stage2 := mod.NewStage("stage 2")

	// Setup some initial steps
	mod.Setup("cluster setup", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	mod.InStage(stage, "step A", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).And("step B", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).Then("step C", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).Then("step D", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).And("step E", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	// Add another independent step chain
	mod.InStage(stage, "step 1", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).Then("step 2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	// Add another independent step chain
	mod.InStage(stage2, "step A", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).Then("step B", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).Then("step C", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).Then("step D", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).Then("step E", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	mod.InStage(stage2, "step 1", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).And("step 2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).Then("step 3", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).Then("step 4", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).And("step 5", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).Then("step 6", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).And("step 7", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).And("step 8", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	planner := mod.NewPlanner()
	echotest.Require(t, planner.DAG(), filepath.Join("testdata", "and_dag.txt"))
}

func TestAfterTestOnlyDAG(t *testing.T) {
	mod := NewTest("basic plan", 12345)

	mod.AfterTest("TPCC consistency checks", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	planner := mod.NewPlanner()
	echotest.Require(t, planner.DAG(), filepath.Join("testdata", "after_test_only_dag.txt"))
}
