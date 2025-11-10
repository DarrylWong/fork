package modular

import (
	"context"
	"time"

	"fmt"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/task"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// stepFunc is the signature for user-provided test steps.
type stepFunc func(context.Context, *logger.Logger, *Helper) error

// StepProtocol is the execution interface for test steps. The framework calls
// these methods to run your step.
type StepProtocol interface {
	Description() string
	Run(context.Context, Executor, *logger.Logger, *Helper) error
}

type SingleStep struct {
	description string
	fn          stepFunc
}

// Description returns a human-readable description of the step.
func (s *SingleStep) Description() string {
	return s.description
}

// Run executes the step function using the provided executor.
func (s *SingleStep) Run(ctx context.Context, exec Executor, l *logger.Logger, h *Helper) error {
	return exec.Execute(ctx, l, h, s)
}

// concurrentStep is a "meta-step" that groups multiple test steps
// that are meant to be executed concurrently.
type concurrentStep struct {
	steps  []step
	delays []time.Duration
}

func (cs *concurrentStep) Description() string {
	return fmt.Sprintf("running %d steps concurrently", len(cs.steps))
}

func (cs *concurrentStep) Run(ctx context.Context, exec Executor, l *logger.Logger, h *Helper) error {
	group := exec.ErrorGroup()
	for i, s := range cs.steps {
		delay := cs.delays[i]
		group.Go(func(ctx context.Context, l *logger.Logger) error {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
			return s.Run(ctx, exec, l, h)
		}, task.WithContext(ctx), task.Logger(l))
	}
	return group.WaitE()
}
