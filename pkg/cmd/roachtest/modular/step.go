package modular

import (
	"context"
	"fmt"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// testStep is an opaque reference to one step of a modular
// test. It can be a singleStep (see below), or a "meta-step",
// meaning that it combines other steps in some way (for instance, a
// series of steps to be run sequentially or concurrently).
type testStep interface {
	singleStepProtocol
}

// singleStepProtocol is the set of functions that single step
// implementations need to provide.
type singleStepProtocol interface {
	// Description is a string representation of the step, intended
	// for human-consumption. Displayed when pretty-printing the test
	// plan.
	Description() string
	// Background returns a channel that controls the execution of a
	// background step: when that channel is closed, the context
	// associated with the step will be canceled. Returning `nil`
	// indicates that the step should not be run in the background.
	// When a step is *not* run in the background, the test will wait
	// for it to finish before moving on. When a background step
	// fails, the entire test fails.
	Background() shouldStop
	// Run implements the actual functionality of the step. This
	// signature should remain in sync with `stepFunc`.
	Run(context.Context, *logger.Logger, *Helper) error
	// ConcurrencyDisabled returns true if the step should not be run
	// concurrently with other steps.
	ConcurrencyDisabled() bool
}

// concurrentStep is a "meta-step" that groups multiple test steps
// that are meant to be executed concurrently.
type concurrentStep struct {
	label string
	steps []testStep
}

// newConcurrentStep creates a concurrent step from multiple steps.
func newConcurrentStep(label string, steps []testStep) *concurrentStep {
	return &concurrentStep{
		label: label,
		steps: steps,
	}
}

// Description returns a description of the concurrent step.
func (cs *concurrentStep) Description() string {
	if len(cs.steps) == 1 {
		return cs.steps[0].Description()
	}
	return fmt.Sprintf("run %d steps concurrently: %s", len(cs.steps), cs.label)
}

// Background returns nil since concurrent steps are managed differently.
func (cs *concurrentStep) Background() shouldStop {
	return nil
}

// ConcurrencyDisabled returns false since this step manages its own concurrency.
func (cs *concurrentStep) ConcurrencyDisabled() bool {
	return false
}

// Run executes all steps concurrently using goroutines.
func (cs *concurrentStep) Run(ctx context.Context, l *logger.Logger, h *Helper) error {
	if len(cs.steps) == 1 {
		// Single step, just run it directly
		return cs.steps[0].Run(ctx, l, h)
	}

	// Multiple steps, run them concurrently
	errCh := make(chan error, len(cs.steps))

	for i, step := range cs.steps {
		go func(stepIndex int, s testStep) {
			stepLogger, err := l.ChildLogger(fmt.Sprintf("step_%d_%s", stepIndex, sanitizeStepName(s.Description())))
			if err != nil {
				errCh <- fmt.Errorf("failed to create logger for step %d: %w", stepIndex, err)
				return
			}

			err = s.Run(ctx, stepLogger, h)
			if err != nil {
				errCh <- fmt.Errorf("step %d (%s) failed: %w", stepIndex, s.Description(), err)
			} else {
				errCh <- nil
			}
		}(i, step)
	}

	// Wait for all steps to complete
	var errors []error
	for i := 0; i < len(cs.steps); i++ {
		if err := <-errCh; err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("concurrent step failed with %d errors: %v", len(errors), errors)
	}

	return nil
}

// sanitizeStepName removes characters that might cause issues in logger names.
func sanitizeStepName(name string) string {
	// Replace colons and spaces with underscores for logger compatibility
	result := strings.ReplaceAll(name, ":", "_")
	result = strings.ReplaceAll(result, " ", "_")
	return result
}

// singleStep implements a basic test step.
type singleStep struct {
	description         string
	fn                  stepFunc
	background          shouldStop
	concurrencyDisabled bool
}

// Description returns a human-readable description of the step.
func (s *singleStep) Description() string {
	return s.description
}

// Background returns the channel that controls background execution.
func (s *singleStep) Background() shouldStop {
	return s.background
}

// Run executes the step function.
func (s *singleStep) Run(ctx context.Context, l *logger.Logger, h *Helper) error {
	return s.fn(ctx, l, h)
}

// ConcurrencyDisabled returns whether this step disables concurrency.
func (s *singleStep) ConcurrencyDisabled() bool {
	return s.concurrencyDisabled
}

// sequentialRunStep is a "meta-step" that indicates that a sequence
// of steps are to be executed sequentially. The default test runner
// already runs steps sequentially. This meta-step exists primarily as
// a way to group related steps so that a test plan is easier to
// understand for a human.
type sequentialRunStep struct {
	label string
	steps []testStep
}
