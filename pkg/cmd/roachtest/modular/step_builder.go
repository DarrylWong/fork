package modular

import "errors"

// Builder provides a simple way to construct DAGs. It allows chaining steps together
// to indicate dependencies.
type Builder interface {
	// Then adds a step that has a dependency on the previous step in the chain.
	Then(stepName string, fn stepFunc, opts ...StepOption) Builder
	// MaybeThen is like Then but only adds the step if the conditional is true.
	MaybeThen(condition bool, stepName string, fn stepFunc, opts ...StepOption) Builder
	// And adds a step that has the same dependencies as the previous step in the chain.
	And(stepName string, fn stepFunc, opts ...StepOption) Builder
	// MaybeAnd is like And but only adds the step if the conditional is true.
	MaybeAnd(condition bool, stepName string, fn stepFunc, opts ...StepOption) Builder
}

type StepBuilder struct {
	test      *Test
	stage     *Stage
	currLevel []*Step
	lastLevel []*Step
}

func newTestStep(stage *Stage, nodeID int, stepName string, fn stepFunc, opts ...StepOption) *Step {
	stepOpts := stepOpts{}
	for _, opt := range opts {
		opt(&stepOpts)
	}

	newStep := &Step{
		StepProtocol: newSingleStep(stepName, fn),
		nodeID:       nodeID,
		children:     make([]*Step, 0),
		opts:         stepOpts,
	}
	stage.stepMap[stepName] = newStep
	return newStep
}

// NewStage creates a new stage for organizing test steps.
func (t *Test) NewStage(name string, opts ...StageOption) *Stage {
	stage := &Stage{
		name:    name,
		roots:   make([]*Step, 0),
		stepMap: make(map[string]*Step),
	}

	for _, opt := range opts {
		opt(&stage.opts)
	}

	t.stages = append(t.stages, stage)
	return stage
}

func (t *Test) InStage(stage *Stage, stepName string, fn stepFunc, opts ...StepOption) Builder {
	ts := newTestStep(stage, t.nextNodeID(), stepName, fn, opts...)

	// Add step as a new root to the stage.
	stage.roots = append(stage.roots, ts)

	return &StepBuilder{
		test:      t,
		stage:     stage,
		currLevel: []*Step{ts},
	}
}

func (sb *StepBuilder) Then(stepName string, fn stepFunc, opts ...StepOption) Builder {
	nodeID := sb.test.nextNodeID()
	ts := newTestStep(sb.stage, nodeID, stepName, fn, opts...)

	// Our new step is dependent on all steps in the current level.
	for _, parent := range sb.currLevel {
		parent.children = append(parent.children, ts)
		ts.parents = append(ts.parents, parent)
	}

	sb.lastLevel, sb.currLevel = sb.currLevel, []*Step{ts}
	return sb
}
func (sb *StepBuilder) MaybeThen(condition bool, stepName string, fn stepFunc, opts ...StepOption) Builder {
	if condition {
		return sb.Then(stepName, fn, opts...)
	}
	return sb
}

func (sb *StepBuilder) And(stepName string, fn stepFunc, opts ...StepOption) Builder {
	nodeID := sb.test.nextNodeID()
	ts := newTestStep(sb.stage, nodeID, stepName, fn, opts...)

	// Our new step is dependent on all steps in the last level.
	for _, parent := range sb.lastLevel {
		parent.children = append(parent.children, ts)
		ts.parents = append(ts.parents, parent)
	}

	// Add the new step to the last level.
	sb.currLevel = append(sb.currLevel, ts)
	return sb
}

func (sb *StepBuilder) MaybeAnd(condition bool, stepName string, fn stepFunc, opts ...StepOption) Builder {
	if condition {
		return sb.And(stepName, fn, opts...)
	}
	return sb
}

type OperationBuilder struct{}

func NewOperation(stepName string, fn stepFunc, opts ...StepOption) Builder {
	// TODO: implement an operation builder.
	return nil
}

// Then adds another step that runs after the previous one in sequence.
func (ob *OperationBuilder) Then(stepName string, fn stepFunc, opts ...StepOption) Builder {
	// TODO: implement an operation builder.
	return nil
}

// MaybeThen is like Then, but only adds the step if the conditional is true.
func (ob *OperationBuilder) MaybeThen(condition bool, stepName string, fn stepFunc, opts ...StepOption) Builder {
	// TODO: implement an operation builder.
	return nil
}

// And adds a step that can run in parallel with the previous step.
func (ob *OperationBuilder) And(stepName string, fn stepFunc, opts ...StepOption) Builder {
	// TODO: implement an operation builder.
	return nil
}

// MaybeAnd is like And, but only adds the step if the conditional is true.
func (ob *OperationBuilder) MaybeAnd(condition bool, stepName string, fn stepFunc, opts ...StepOption) Builder {
	// TODO: implement an operation builder.
	return nil
}

// The following methods serve as an escape hatch for more complex DAG dependencies
// not supported by the Builder API. It allows explicit declaration of dependencies between steps,
// at the cost of being more verbose.

// NewStep creates a new step in the given stage with no dependencies.
func (t *Test) NewStep(stage *Stage, stepName string, fn stepFunc, opts ...StepOption) *Step {
	return newTestStep(stage, t.nextNodeID(), stepName, fn, opts...)
}

// AddDependency adds a directed dependency from this step to the child step.
func (s *Step) AddDependency(child *Step) {
	s.children = append(s.children, child)
	child.parents = append(child.parents, s)
}

// Finalize finalizes the stage's DAG by assigning root steps and ensuring we have a valid DAG.
func (s *Stage) Finalize() error {
	rootNodes := make([]*Step, 0)

	for _, step := range s.stepMap {
		if len(step.parents) == 0 {
			rootNodes = append(rootNodes, step)
		}
	}
	if len(rootNodes) == 0 {
		return errors.New("stage is an invalid DAG: no root nodes found")
	}

	s.roots = rootNodes
	return nil
}
