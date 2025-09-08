package modular

import (
	"context"
	"fmt"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/util/ctxgroup"
)

// stepFunc is the signature for user-provided test steps.
type stepFunc func(context.Context, *logger.Logger, *Helper) error

// shouldStop is a channel that signals when a background step should stop.
type shouldStop chan struct{}

type StepProtocol interface {
	Description() string
	Run(context.Context, *logger.Logger, *Helper) error
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

// Run executes the step function.
func (s *singleStep) Run(ctx context.Context, l *logger.Logger, h *Helper) error {
	return s.fn(ctx, l, h)
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

func (cs *concurrentStep) Description() string {
	if len(cs.steps) == 1 {
		return cs.steps[0].Description()
	}
	return cs.label
}

func (cs *concurrentStep) Run(ctx context.Context, l *logger.Logger, h *Helper) error {
	if len(cs.steps) == 1 {
		// Single step, just run it directly
		return cs.steps[0].Run(ctx, l, h)
	}

	// Multiple steps, run them concurrently using ctxgroup
	group := ctxgroup.WithContext(ctx)

	for i, step := range cs.steps {
		group.GoCtx(func(ctx context.Context) error {
			stepLogger, err := l.ChildLogger(fmt.Sprintf("step_%d_%s", i, sanitizeStepName(step.Description())))
			if err != nil {
				return fmt.Errorf("failed to create logger for step %d: %w", i, err)
			}

			err = step.Run(ctx, stepLogger, h)
			if err != nil {
				return fmt.Errorf("step %d (%s) failed: %w", i, step.Description(), err)
			}
			return nil
		})
	}

	// Wait for all steps to complete
	return group.Wait()
}
