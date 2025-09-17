package modular

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/testutils/echotest"
)

func TestMarshalJSON(t *testing.T) {
	mod := newModTest()

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
	stages := planner.stages

	jsonBytes, err := MarshalJSON(stages)
	if err != nil {
		t.Fatalf("Failed to marshal JSON: %v", err)
	}

	echotest.Require(t, string(jsonBytes), filepath.Join("testdata", "marshal_json.txt"))
}
