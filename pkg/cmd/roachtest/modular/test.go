package modular

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/task"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
)

// Test represents a modular test definition. The Test struct is how a test
// writer interacts (build and runs) with a modular test.
type Test struct {
	// setupStage is a special stage in the plan used for initializing the test.
	// Steps are run sequentially as declared, and no failure injection is attempted.
	setupStage *stage
	stages     []*stage
	// afterTestStage is like setupStage but run after the test is completed.
	afterTestStage *stage
	options        TestOptions

	// Test execution context
	ctx       context.Context
	logger    *logger.Logger
	cluster   cluster.Cluster
	crdbNodes option.NodeListOption
	tasker    task.Manager
}

type TestOptions struct {
	executor Executor
}

type TestOption func(options *TestOptions)

// WithExecutor allows specifying a custom executor for the test.
// If not provided, a LocalExecutor will be used by default.
func WithExecutor(executor Executor) TestOption {
	return func(opts *TestOptions) {
		opts.executor = executor
	}
}

func (t *Test) Run() error {
	planner := t.newPlanner()
	// Log the DAG.
	t.logger.Printf(planner.DAG())

	testPlan, err := planner.Plan()
	if err != nil {
		return err
	}

	// Log the test plan.
	t.logger.Printf(testPlan.String())

	r := t.newRunner()
	return r.Run(t.ctx, t.logger, *testPlan)
}

// NewPlanner finalizes the DAG (e.g. randomly selects compatible failure injection
// operations to run for each stage) and returns a test planner.
func (t *Test) newPlanner() Planner {
	var combinedStages []stage
	if t.setupStage != nil {
		combinedStages = append(combinedStages, *t.setupStage)
	}
	for stageIdx := range t.stages {
		combinedStages = append(combinedStages, *t.stages[stageIdx])
	}
	if t.afterTestStage != nil {
		combinedStages = append(combinedStages, *t.afterTestStage)
	}

	rng, seed := randutil.NewLockedPseudoRand()

	return Planner{
		seed:      seed,
		rng:       rng,
		stages:    combinedStages,
		ctx:       t.ctx,
		logger:    t.logger,
		cluster:   t.cluster,
		crdbNodes: t.crdbNodes,
	}
}

func (t *Test) newRunner() runner {
	executor := t.options.executor
	if executor == nil {
		// Default to LocalExecutor if none provided
		executor = &LocalExecutor{tasker: t.tasker}
	}
	return runner{
		executor: executor,
	}
}
