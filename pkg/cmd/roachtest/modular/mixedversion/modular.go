package mixedversion

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// StepBuilder is a wrapper over modular.StepBuilder to allow us to
// use a mixed version helper.
type StepBuilder struct {
	*modular.StepBuilder
}

func withMixedVersionHelper(fn stepFunc) func(ctx context.Context, l *logger.Logger, base *modular.Helper) error {
	return func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		mvHelper := &Helper{Helper: h}
		return fn(ctx, l, nil, mvHelper)
	}
}

// NewStage creates a new stage for organizing test steps.
func (p *testPlanner) NewStage(name string, opts ...modular.StageOption) *modular.Stage {
	return p.mod.NewStage(name, opts...)
}

// InStage adds a step to be executed in the specified stage.
func (p *testPlanner) InStage(stage *modular.Stage, stepName string, fn stepFunc, opts ...modular.StepOption) *StepBuilder {
	return &StepBuilder{
		StepBuilder: p.mod.InStage(stage, stepName, withMixedVersionHelper(fn), opts...),
	}
}

// Then adds another step that runs after this one in sequence.
func (sb *StepBuilder) Then(stepName string, fn stepFunc, opts ...modular.StepOption) *StepBuilder {
	return &StepBuilder{
		StepBuilder: sb.StepBuilder.Then(stepName, withMixedVersionHelper(fn), opts...),
	}
}

// And adds a step that can run in parallel with the previous step.
// All steps added via .And() will run in parallel within the same stepGroup.
func (sb *StepBuilder) And(stepName string, fn stepFunc, opts ...modular.StepOption) *StepBuilder {
	return &StepBuilder{
		StepBuilder: sb.StepBuilder.And(stepName, withMixedVersionHelper(fn), opts...),
	}
}
