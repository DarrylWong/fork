package modular

import (
	"context"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// Executor defines how steps should be executed. This allows execution to be
// mocked for unit tests, or in the future the ability to support local vs remote
// execution.
type Executor interface {
	Execute(ctx context.Context, l *logger.Logger, h *Helper, s *SingleStep) error
}

// LocalExecutor executes steps locally, i.e. on the test runner.
type LocalExecutor struct{}

func (e *LocalExecutor) Execute(ctx context.Context, l *logger.Logger, h *Helper, s *SingleStep) error {
	return s.fn(ctx, l, h)
}

// DryRunExecutor simulates step execution without actually running the step function.
// TODO: implement a dry run executor.
type DryRunExecutor struct{}

func (e *DryRunExecutor) Execute(ctx context.Context, l *logger.Logger, h *Helper, s *SingleStep) error {
	return nil
}
