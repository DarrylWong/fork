package modular

import (
	"context"

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
