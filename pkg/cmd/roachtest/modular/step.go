package modular

import (
	"context"
	"fmt"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// stepFunc is the signature for user-provided test steps.
type stepFunc func(context.Context, *logger.Logger, *Helper) error

// StepProtocol defines how the framework actually runs a test step.
type StepProtocol interface {
	// Description returns a human-readable description of the step.
	Description() string
	// Run executes the step function using the provided executor.
	Run(context.Context, Executor, *logger.Logger, *Helper) error
}

// SingleStep represents a single test step to be executed.
type SingleStep struct {
	description string
	fn          stepFunc
}

func newSingleStep(description string, fn stepFunc) *SingleStep {
	return &SingleStep{
		description: description,
		fn:          fn,
	}
}

func (s *SingleStep) Description() string {
	return s.description
}

func (s *SingleStep) Run(ctx context.Context, exec Executor, l *logger.Logger, h *Helper) error {
	return exec.Execute(ctx, l, h, s)
}

type ConcurrentStep struct {
	description string
	steps       []Step
}

func (cs *ConcurrentStep) Description() string {
	return fmt.Sprintf("running %d steps concurrently", len(cs.steps))
}

func (cs *ConcurrentStep) Run(ctx context.Context, exec Executor, l *logger.Logger, h *Helper) error {
	// TODO: implement the ability to run steps concurrently, with some randomized delay.
	return nil
}
