package modular

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// Runner executes a generated test plan from the modular framework.
type Runner struct {
	testPlan *TestPlan
	helper   *Helper
}

// NewRunner creates a new runner for executing a test plan.
func NewRunner(testPlan *TestPlan) *Runner {
	return &Runner{
		testPlan: testPlan,
		helper:   &Helper{rng: testPlan.rng()},
	}
}

// RunTestPlan executes the test plan using the provided roachtest.Test interface.
// It logs the DAG and test plan, then executes all steps in order.
func RunTestPlan(ctx context.Context, t test.Test, testPlan *TestPlan) error {
	runner := NewRunner(testPlan)
	return runner.Run(ctx, t)
}

// Run executes the test plan, logging the DAG and test plan before execution.
func (r *Runner) Run(ctx context.Context, t test.Test) error {
	l := t.L()

	// Log the test plan details
	l.Printf("Starting modular test execution")
	l.Printf("Test Plan: %s", r.testPlan.name)
	l.Printf("Seed: %d", r.testPlan.seed)
	l.Printf("Number of stages: %d", len(r.testPlan.stagePlans))

	// Log the full test plan structure
	l.Printf("Test Plan Structure:\n%s", r.testPlan.String())

	// Generate and log the DAG
	planner := &TestPlanner{
		seed:   r.testPlan.seed,
		stages: r.extractStages(),
	}
	dag := planner.DAG()
	l.Printf("DAG Visualization:\n%s", dag)

	// Execute all steps in the plan
	return r.executeSteps(ctx, l)
}

// extractStages extracts Stage objects from the test plan for DAG generation.
func (r *Runner) extractStages() []Stage {
	var stages []Stage
	for _, stagePlan := range r.testPlan.stagePlans {
		if stagePlan.stage != nil {
			stages = append(stages, *stagePlan.stage)
		}
	}
	return stages
}

// executeSteps executes all steps in the test plan sequentially by stage.
func (r *Runner) executeSteps(ctx context.Context, l *logger.Logger) error {
	for stageIdx, stagePlan := range r.testPlan.stagePlans {
		stageName := stagePlan.stage.name
		if stageName == "" {
			stageName = fmt.Sprintf("stage %d", stageIdx+1)
		}

		stageLogger, err := l.ChildLogger(fmt.Sprintf("stage_%d_%s", stageIdx, sanitizeStepName(stageName)))
		if err != nil {
			return fmt.Errorf("failed to create stage logger: %w", err)
		}

		stageLogger.Printf("Starting stage: %s", stageName)
		start := time.Now()

		err = r.executeStage(ctx, stageLogger, stagePlan)
		if err != nil {
			return fmt.Errorf("stage %s failed: %w", stageName, err)
		}

		duration := time.Since(start)
		stageLogger.Printf("Stage %s completed successfully in %v", stageName, duration)
	}

	l.Printf("All stages completed successfully")
	return nil
}

// executeStage executes all steps within a single stage.
func (r *Runner) executeStage(ctx context.Context, l *logger.Logger, stagePlan stagePlan) error {
	for stepIdx, step := range stagePlan.steps {
		stepLogger, err := l.ChildLogger(fmt.Sprintf("step_%d_%s", stepIdx, sanitizeStepName(step.Description())))
		if err != nil {
			return fmt.Errorf("failed to create step logger: %w", err)
		}

		stepLogger.Printf("Starting step: %s", step.Description())
		start := time.Now()

		err = step.Run(ctx, stepLogger, r.helper)
		if err != nil {
			return fmt.Errorf("step %s failed: %w", step.Description(), err)
		}

		duration := time.Since(start)
		stepLogger.Printf("Step %s completed successfully in %v", step.Description(), duration)
	}

	return nil
}

// rng returns the random number generator from the test plan.
func (p *TestPlan) rng() *rand.Rand {
	// Since TestPlan doesn't directly contain an RNG, we'll create one from the seed
	return rand.New(rand.NewSource(p.seed))
}
