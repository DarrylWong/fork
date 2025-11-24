package modular

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/testutils/echotest"
	"github.com/stretchr/testify/require"
)

// testRenderDAG is a helper that finalizes stages and renders the DAG.
func testRenderDAG(t *testing.T, stages []*Stage) string {
	t.Helper()
	for _, stage := range stages {
		require.NoError(t, stage.Finalize())
	}
	dag, err := renderDAG(stages)
	require.NoError(t, err)
	return dag
}

// TestBasicDAG tests the visual DAG generation for a basic dag.
func TestBasicDAG(t *testing.T) {
	test := &Test{}

	baselineStage := test.NewStage("baseline")
	chaosStage := test.NewStage("chaos")

	for _, stage := range []*Stage{baselineStage, chaosStage} {
		// Run TPCC workload
		test.InStage(stage, "running TPCC workload for 1 hour", func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("dropping TPCC tables", func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		})

		test.InStage(stage, "increasing replication factor to 5", func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("waiting for replication", func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("sleeping for 10 minutes", func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("decreasing replication factor to 3", func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		})

		test.InStage(stage, "copy bank table", func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		})

		if stage == chaosStage {
			test.InStage(stage, "inject network partition", func(ctx context.Context, l *logger.Logger, h *Helper) error {
				return nil
			}).Then("recover from network partition", func(ctx context.Context, l *logger.Logger, h *Helper) error {
				return nil
			})
		}
	}

	dag := testRenderDAG(t, test.stages)
	echotest.Require(t, dag, filepath.Join("testdata", "basic_dag.txt"))
}

// TestMVTDAG tests the visual DAG generation for a mvt dag.
func TestMVTDAG(t *testing.T) {
	test := &Test{}

	upgradeStage := test.NewStage("upgrade cluster from \"v24.2.2\" to \"master\"")
	test.InStage(upgradeStage, "restart system server on node 1 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	test.InStage(upgradeStage, "restart system server on node 2 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	test.InStage(upgradeStage, "restart system server on node 3 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	test.InStage(upgradeStage, "restart system server on node 4 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	test.InStage(upgradeStage, "run backup", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	test.InStage(upgradeStage, "test features", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	rollbackStage := test.NewStage("downgrade nodes :1-4 from \"master\" to \"v24.2.2\"")
	test.InStage(rollbackStage, "restart system server on node 1 with binary version v24.2.2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	test.InStage(rollbackStage, "restart system server on node 2 with binary version v24.2.2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	test.InStage(rollbackStage, "restart system server on node 3 with binary version v24.2.2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	test.InStage(rollbackStage, "restart system server on node 4 with binary version v24.2.2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	test.InStage(rollbackStage, "run backup", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	test.InStage(rollbackStage, "test features", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	finalizeStage := test.NewStage("upgrade cluster from \"v24.2.2\" to \"master\"")
	test.InStage(finalizeStage, "restart system server on node 1 with binary version master", func(ctx context.Context, l *logger.Logger, h *Helper) error {
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

	test.InStage(finalizeStage, "run backup", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})
	test.InStage(finalizeStage, "test features", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	dag := testRenderDAG(t, test.stages)
	echotest.Require(t, dag, filepath.Join("testdata", "mvt_dag.txt"))
}

// TestAndDAG tests the visual DAG generation for a and dag.
func TestAndDAG(t *testing.T) {
	test := &Test{}

	stage := test.NewStage("stage 1")
	stage2 := test.NewStage("stage 2")

	test.InStage(stage, "step A", func(ctx context.Context, l *logger.Logger, h *Helper) error {
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

	// Another independent chain
	test.InStage(stage, "step 1", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}).Then("step 2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	// Stage 2
	test.InStage(stage2, "step A", func(ctx context.Context, l *logger.Logger, h *Helper) error {
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

	test.InStage(stage2, "step 1", func(ctx context.Context, l *logger.Logger, h *Helper) error {
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

	dag := testRenderDAG(t, test.stages)
	echotest.Require(t, dag, filepath.Join("testdata", "and_dag.txt"))
}

// TestDAGNodeTransitions tests various node(s) to node(s) transitions in the DAG.
func TestDAGNodeTransitions(t *testing.T) {
	type transitionTest struct {
		from int
		to   int
	}

	tests := []transitionTest{
		{from: 1, to: 3},
		{from: 1, to: 4},
		{from: 3, to: 1},
		{from: 4, to: 1},
		{from: 2, to: 3},
		{from: 3, to: 2},
		{from: 3, to: 3},
		{from: 3, to: 4},
		{from: 4, to: 3},
		{from: 4, to: 4},
	}

	for _, subtest := range tests {
		t.Run(fmt.Sprintf("%d-%d", subtest.from, subtest.to), func(t *testing.T) {
			test := &Test{}
			stage := test.NewStage(fmt.Sprintf("%d to %d", subtest.from, subtest.to))
			b := test.InStage(stage, "step 1", noopStep())
			for i := 1; i < subtest.from; i++ {
				b = b.And(fmt.Sprintf("step %d", i+1), noopStep())
			}

			b = b.Then(fmt.Sprintf("step %d", subtest.from+1), noopStep())
			for j := 1; j < subtest.to; j++ {
				b = b.And(fmt.Sprintf("step %d", subtest.from+1+j), noopStep())
			}

			dag := testRenderDAG(t, test.stages)
			echotest.Require(t, dag, filepath.Join("testdata", fmt.Sprintf("%d_to_%d_dag.txt", subtest.from, subtest.to)))
		})
	}
}

// TestDrawNodes tests each individual draw function in isolation.
func TestDrawNodes(t *testing.T) {
	var output string
	// drawStepNode
	builder := NewDAGBuilder(nodeWidth, nodeHeight)
	drawStepNode("test step", builder.PutFunc(0, 0))
	output += fmt.Sprintf("drawStepNode:\n%s\n", builder.String())

	// drawStepNode with long text
	builder = NewDAGBuilder(nodeWidth, nodeHeight)
	drawStepNode("test step with long description that wraps", builder.PutFunc(0, 0))
	output += fmt.Sprintf("drawStepNode/text-wrap:\n%s\n", builder.String())

	// drawUpperDependencyNode
	builder = NewDAGBuilder(nodeWidth, nodeHeight)
	drawUpperDependencyNode(builder.PutFunc(0, 0))
	output += fmt.Sprintf("drawUpperDependencyNode:\n%s\n", builder.String())

	// drawLowerDependencyNode
	builder = NewDAGBuilder(nodeWidth, nodeHeight)
	drawLowerDependencyNode(builder.PutFunc(0, 0))
	output += fmt.Sprintf("drawLowerDependencyNode:\n%s\n", builder.String())

	// drawStageLabel
	builder = NewDAGBuilder(nodeWidth, nodeHeight)
	drawStageLabel("test stage", builder.PutFunc(0, 0))
	output += fmt.Sprintf("drawStageLabel:\n%s\n", builder.String())

	// drawMergeLeftNode
	builder = NewDAGBuilder(nodeWidth, nodeHeight)
	drawMergeLeftNode(builder.PutFunc(0, 0))
	output += fmt.Sprintf("drawMergeLeftNode:\n%s\n", builder.String())

	// drawMergeCenterNode
	builder = NewDAGBuilder(nodeWidth, nodeHeight)
	drawMergeCenterNode(builder.PutFunc(0, 0))
	output += fmt.Sprintf("drawMergeCenterNode:\n%s\n", builder.String())

	// drawMergeRightNode
	builder = NewDAGBuilder(nodeWidth, nodeHeight)
	drawMergeRightNode(builder.PutFunc(0, 0))
	output += fmt.Sprintf("drawMergeRightNode:\n%s\n", builder.String())

	// drawSplitLeftNode
	builder = NewDAGBuilder(nodeWidth, nodeHeight)
	drawSplitLeftNode(builder.PutFunc(0, 0))
	output += fmt.Sprintf("drawSplitLeftNode:\n%s\n", builder.String())

	// drawSplitCenterNode
	builder = NewDAGBuilder(nodeWidth, nodeHeight)
	drawSplitCenterNode(builder.PutFunc(0, 0))
	output += fmt.Sprintf("drawSplitCenterNode:\n%s\n", builder.String())

	// drawSplitRightNode
	builder = NewDAGBuilder(nodeWidth, nodeHeight)
	drawSplitRightNode(builder.PutFunc(0, 0))
	output += fmt.Sprintf("drawSplitRightNode:\n%s\n", builder.String())

	echotest.Require(t, output, filepath.Join("testdata", "draw_nodes.txt"))
}
