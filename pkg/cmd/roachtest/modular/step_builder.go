package modular

// Builder provides a simple way to construct DAGs. It allows chaining steps together
// to indicate dependencies.
type Builder interface {
	// Then adds a step that has a dependency on the previous step in the chain.
	Then(stepName string, fn stepFunc, opts ...StepOption) *Builder
	// MaybeThen is like Then but only adds the step if the conditional is true.
	MaybeThen(condition bool, stepName string, fn stepFunc, opts ...StepOption) *Builder
	// And adds a step that has the same dependencies as the previous step in the chain.
	And(stepName string, fn stepFunc, opts ...StepOption) *Builder
	// MaybeAnd is like And but only adds the step if the conditional is true.
	MaybeAnd(condition bool, stepName string, fn stepFunc, opts ...StepOption) *Builder
}

type StepBuilder struct{}

// NewStage creates a new stage for organizing test steps.
func (t *Test) NewStage(name string, opts ...StageOption) *Stage {
	// TODO: implement a DAG builder.
	return nil
}

func (t *Test) InStage(stepName string, fn stepFunc, opts ...StepOption) *Builder {
	// TODO: implement a DAG builder.
	return nil
}

func (sb *StepBuilder) Then(stepName string, fn stepFunc, opts ...StepOption) *Builder {
	// TODO: implement a DAG builder.
	return nil
}
func (sb *StepBuilder) MaybeThen(condition bool, stepName string, fn stepFunc, opts ...StepOption) *Builder {
	// TODO: implement a DAG builder.
	return nil
}

func (sb *StepBuilder) And(stepName string, fn stepFunc, opts ...StepOption) *Builder {
	// TODO: implement a DAG builder.
	return nil
}

func (sb *StepBuilder) MaybeAnd(condition bool, stepName string, fn stepFunc, opts ...StepOption) *Builder {
	// TODO: implement a DAG builder.
	return nil
}
