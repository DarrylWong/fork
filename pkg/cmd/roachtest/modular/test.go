package modular

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// Test is the main struct test writers will interact with. It is used to
// construct a DAG of the test, which is then converted to a test plan
// and executed.
type Test struct {
	// setupStage is a special Stage in the plan used for initializing the test.
	// Steps are run sequentially as declared i.e. no randomization, and no failure
	// injection is attempted.
	setupStage *Stage
	// stages of the test, represented as a DAG.
	stages []*Stage
	// afterTestStage is like setupStage but run after the test is completed.
	afterTestStage *Stage
	numSteps       int
	options        TestOptions
}

type TestOptions struct {
	executor Executor
}
type TestOption func(options *TestOptions)

func WithExecutor(executor Executor) TestOption {
	return func(options *TestOptions) {
		options.executor = executor
	}
}

func (t *Test) Plan() (string, *TestPlan, error) {
	// TODO: implement the test planner.
	planner := NewPlanner(t.stages, nil /* planFn */)
	if err := planner.FinalizeStages(); err != nil {
		return "", nil, err
	}

	DAG, err := planner.DAG()
	if err != nil {
		return "", nil, err
	}
	plan, err := planner.Plan()
	return DAG, plan, err
}

func (t *Test) Run(ctx context.Context, l *logger.Logger) error {
	DAG, plan, err := t.Plan()
	if err != nil {
		return err
	}
	l.Printf(DAG)
	l.Printf(plan.String())

	r := NewRunner(t.options.executor)
	return r.Run(ctx, l, plan)
}

func (t *Test) nextNodeID() int {
	t.numSteps++
	return t.numSteps
}
