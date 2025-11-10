package modular

import (
	"context"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/task"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// Executor defines how steps should be executed.
type Executor interface {
	Execute(ctx context.Context, l *logger.Logger, h *Helper, s *SingleStep) error
	// ErrorGroup returns an error group for coordinating concurrent execution.
	ErrorGroup() task.ErrorGroup
}

// LocalExecutor executes steps directly in the current process, i.e. the test runner.
type LocalExecutor struct {
	tasker task.Manager
}

// Execute runs the step function directly.
func (e *LocalExecutor) Execute(ctx context.Context, l *logger.Logger, h *Helper, s *SingleStep) error {
	return s.fn(ctx, l, h)
}

// ErrorGroup returns a new error group from the task manager.
func (e *LocalExecutor) ErrorGroup() task.ErrorGroup {
	return e.tasker.NewErrorGroup()
}

// MockExecutor records step executions for unit testing without actually running them.
type MockExecutor struct {
	// output is where step executions are recorded
	output *strings.Builder
	// tasker provides the task manager for concurrent execution
	tasker task.Manager
}

// NewMockExecutor creates a new mock executor that writes to the given string builder.
func NewMockExecutor(output *strings.Builder, tasker task.Manager) *MockExecutor {
	return &MockExecutor{
		output: output,
		tasker: tasker,
	}
}

// Execute records the step description to the output without actually running it.
func (e *MockExecutor) Execute(ctx context.Context, l *logger.Logger, h *Helper, s *SingleStep) error {
	e.output.WriteString(s.Description() + "\n")
	return nil
}

// ErrorGroup returns an error group from the task manager.
func (e *MockExecutor) ErrorGroup() task.ErrorGroup {
	return e.tasker.NewErrorGroup()
}
