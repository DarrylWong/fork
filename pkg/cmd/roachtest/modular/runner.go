package modular

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

var (
	// everything that is not an alphanum or a few special characters
	invalidChars = regexp.MustCompile(`[^a-zA-Z0-9 \-_.]`)
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
		helper:   &Helper{rng: testPlan.rng},
	}
}

// RunTestPlan executes the test plan using the provided roachtest.Test interface.
// It logs the DAG and test plan, then executes all steps in position.
func RunTestPlan(ctx context.Context, t test.Test, testPlan *TestPlan) error {
	runner := NewRunner(testPlan)
	return runner.Run(ctx, t)
}

// Run executes the test plan, logging the DAG and test plan before execution.
func (r *Runner) Run(ctx context.Context, t test.Test) error {
	l := t.L()

	// Log the test plan details
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

		stageLogger, err := r.loggerForStage(l, stageIdx, stageName)
		if err != nil {
			return fmt.Errorf("failed to create stage logger: %w", err)
		}

		r.logStage("STARTING", stageName, stageLogger)
		start := time.Now()

		err = r.executeStage(ctx, stageLogger, stagePlan)
		if err != nil {
			return r.stageError(ctx, err, stageName, stageLogger)
		}

		duration := time.Since(start)
		prefix := fmt.Sprintf("FINISHED [%s]", duration)
		r.logStage(prefix, stageName, stageLogger)
	}

	l.Printf("All stages completed successfully")
	return nil
}

// executeStage executes all steps within a single stage.
func (r *Runner) executeStage(ctx context.Context, l *logger.Logger, stagePlan stagePlan) error {
	for _, step := range stagePlan.steps {
		stepLogger, err := r.loggerForStep(l, step.id, step.Description())
		if err != nil {
			return fmt.Errorf("failed to create step logger: %w", err)
		}

		r.logStep("STARTING", step.id, step.Description(), stepLogger)
		start := time.Now()

		err = step.Run(ctx, stepLogger, r.helper)
		if err != nil {
			return r.stepError(ctx, err, step.id, step.Description(), stepLogger)
		}

		duration := time.Since(start)
		prefix := fmt.Sprintf("FINISHED [%s]", duration)
		r.logStep(prefix, step.id, step.Description(), stepLogger)
	}

	return nil
}

// logStage logs stage start/finish messages with consistent formatting.
func (r *Runner) logStage(prefix, stageName string, l *logger.Logger) {
	dashes := strings.Repeat("=", 10)
	l.Printf("%[1]s %s: %s %[1]s", dashes, prefix, stageName)
}

// logStep logs step start/finish messages with consistent formatting.
func (r *Runner) logStep(prefix string, stepID int, stepDesc string, l *logger.Logger) {
	dashes := strings.Repeat("-", 10)
	l.Printf("%[1]s %s (%d): %s %[1]s", dashes, prefix, stepID, stepDesc)
}

// loggerForStage creates a logger instance for a stage.
func (r *Runner) loggerForStage(parent *logger.Logger, stageIdx int, stageName string) (*logger.Logger, error) {
	name := invalidChars.ReplaceAllString(strings.ToLower(stageName), "")
	name = fmt.Sprintf("stage_%d_%s", stageIdx, name)
	return parent.ChildLogger(name)
}

// loggerForStep creates a logger instance for a step, similar to mixed-version runner.
func (r *Runner) loggerForStep(parent *logger.Logger, stepID int, stepDesc string) (*logger.Logger, error) {
	name := invalidChars.ReplaceAllString(strings.ToLower(stepDesc), "")
	name = fmt.Sprintf("%d_%s", stepID, name)
	return parent.ChildLogger(name)
}

// stepError generates a detailed error for step failures.
func (r *Runner) stepError(_ context.Context, err error, stepID int, stepDesc string, l *logger.Logger) error {
	stepErr := fmt.Errorf("modular test failure while running step %d (%s): %w", stepID, stepDesc, err)

	// Log the error for convenience
	l.Printf("Step failed: %+v", stepErr)

	// Rename the log file to indicate failure
	if renameErr := r.renameFailedLogger(l); renameErr != nil {
		l.Printf("could not rename failed step logger: %v", renameErr)
	}

	return stepErr
}

// stageError generates a detailed error for stage failures.
func (r *Runner) stageError(_ context.Context, err error, stageName string, l *logger.Logger) error {
	stageErr := fmt.Errorf("modular test failure while running stage %s: %w", stageName, err)

	// Log the error for convenience
	l.Printf("Stage failed: %+v", stageErr)

	// Rename the log file to indicate failure
	if renameErr := r.renameFailedLogger(l); renameErr != nil {
		l.Printf("could not rename failed stage logger: %v", renameErr)
	}

	return stageErr
}

// renameFailedLogger renames the log file to include "FAILED" prefix.
func (r *Runner) renameFailedLogger(l *logger.Logger) error {
	if l.File == nil {
		return nil
	}

	currentFileName := l.File.Name()
	newLogName := filepath.Join(
		filepath.Dir(currentFileName),
		"FAILED_"+filepath.Base(currentFileName),
	)
	return os.Rename(currentFileName, newLogName)
}
