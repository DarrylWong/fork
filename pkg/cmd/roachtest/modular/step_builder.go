package modular

import (
	"context"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// StepBuilder allows method chaining for building step sequences.
type StepBuilder struct {
	test  *Test
	stage *Stage
}

func newTestStep(hookID int, stepName string, fn stepFunc, opts ...StepOption) testStep {
	return testStep{
		StepProtocol: NewSingleStep(stepName, fn, opts...),
		hookID:       hookID,
	}
}

// newDynamicTestStep creates a test step for a dynamic step with PrePlan callback.
func newDynamicTestStep[T any](
	hookID int,
	stepName string,
	prePlanFn func(context.Context, *logger.Logger, *Helper) (T, error),
	runFn func(context.Context, *logger.Logger, *Helper, T) error,
	opts ...DynamicStepOption[T],
) testStep {
	return testStep{
		StepProtocol: NewDynamicStep(stepName, prePlanFn, runFn, opts...),
		hookID:       hookID,
	}
}

// Setup adds a setup step that runs before the test begins.
func (t *Test) Setup(stepName string, fn stepFunc, opts ...StepOption) {
	ts := newTestStep(t.nextHookID(), stepName, fn, opts...)

	// Create setup stage if it doesn't exist
	if t.setupStage == nil {
		t.setupStage = &Stage{
			name:   "setup",
			chains: make([]chain, 1),
		}
		// Start with an empty Chain
		t.setupStage.chains[0] = chain{}
	}

	// Add each setup step as a new stepGroup (sequential execution)
	t.setupStage.chains[0] = append(t.setupStage.chains[0], stepGroup{ts})
}

// AfterTest adds a step that runs after all test stages are complete.
func (t *Test) AfterTest(stepName string, fn stepFunc, opts ...StepOption) {
	ts := newTestStep(t.nextHookID(), stepName, fn, opts...)

	// Create after-test stage if it doesn't exist
	if t.afterTestStage == nil {
		t.afterTestStage = &Stage{
			name:   "after-test",
			chains: make([]chain, 1),
		}
		// Start with an empty Chain
		t.afterTestStage.chains[0] = chain{}
	}

	// Add each after-test step as a new stepGroup (sequential execution)
	t.afterTestStage.chains[0] = append(t.afterTestStage.chains[0], stepGroup{ts})
}

// NewStage creates a new stage for organizing test steps.
func (t *Test) NewStage(name string, opts ...StageOption) *Stage {
	stage := &Stage{
		name:               name,
		chains:             make([]chain, 0),
		maxStepConcurrency: t.options.defaultStepConcurrency,
	}

	for _, opt := range opts {
		opt(stage)
	}

	t.stages = append(t.stages, stage)
	return stage
}

// InStage adds a step to be executed in the specified stage.
func (t *Test) InStage(stage *Stage, stepName string, fn stepFunc, opts ...StepOption) *StepBuilder {
	ts := newTestStep(t.nextHookID(), stepName, fn, opts...)

	// Add step as a new Chain with a single stepGroup to the stage
	stage.chains = append(stage.chains, chain{stepGroup{ts}})

	return &StepBuilder{
		test:  t,
		stage: stage,
	}
}

// InStageDynamic adds a dynamic step with PrePlan callback to be executed in the specified stage.
// The PrePlan function is called during stage planning and its result is passed to the Run function.
func InStageDynamic[T any](
	t *Test,
	stage *Stage,
	stepName string,
	prePlanFn func(context.Context, *logger.Logger, *Helper) (T, error),
	runFn func(context.Context, *logger.Logger, *Helper, T) error,
	opts ...DynamicStepOption[T],
) *StepBuilder {
	ts := newDynamicTestStep(t.nextHookID(), stepName, prePlanFn, runFn, opts...)

	// Add step as a new Chain with a single stepGroup to the stage
	stage.chains = append(stage.chains, chain{stepGroup{ts}})

	return &StepBuilder{
		test:  t,
		stage: stage,
	}
}

// Then adds another step that runs after this one in sequence.
func (sb *StepBuilder) Then(stepName string, fn stepFunc, opts ...StepOption) *StepBuilder {
	ts := newTestStep(sb.test.nextHookID(), stepName, fn, opts...)

	// Add the step as a new stepGroup in the Chain
	if len(sb.stage.chains) == 0 {
		// Create a new Chain if none exists
		sb.stage.chains = append(sb.stage.chains, chain{stepGroup{ts}})
	} else {
		// Append to the last Chain as a new stepGroup
		lastChainIndex := len(sb.stage.chains) - 1
		sb.stage.chains[lastChainIndex] = append(sb.stage.chains[lastChainIndex], stepGroup{ts})
	}

	return sb
}

// And adds a step that can run in parallel with the previous step.
// All steps added via .And() will run in parallel within the same stepGroup.
func (sb *StepBuilder) And(stepName string, fn stepFunc, opts ...StepOption) *StepBuilder {
	ts := newTestStep(sb.test.nextHookID(), stepName, fn, opts...)

	if len(sb.stage.chains) == 0 {
		panic("no Chain found to add an And() step to")
	}

	lastChainIndex := len(sb.stage.chains) - 1
	lastChain := sb.stage.chains[lastChainIndex]
	if len(lastChain) == 0 {
		panic("no step group found to add an And() step to")
	}
	// Add to the last stepGroup
	lastStepGroupIndex := len(lastChain) - 1
	sb.stage.chains[lastChainIndex][lastStepGroupIndex] = append(lastChain[lastStepGroupIndex], ts)

	return sb
}

// OperationBuilder allows method chaining for building operations.
type OperationBuilder struct {
	Chain Chain
}

// NewOperation creates a new operation builder with a single step.
// The step can be either a simple step (created with NewStep) or a dynamic step
// (created with NewDynamicStep), allowing for a unified API.
//
// Examples:
//
//	// Simple operation
//	NewOperation(NewStep("run workload", func(ctx, l, h) error { ... }))
//
//	// Dynamic operation
//	NewOperation(NewDynamicStep("restart node", prePlanFn, runFn, opts...))
func NewOperation(step StepProtocol) *OperationBuilder {
	ts := testStep{
		StepProtocol: step,
		hookID:       0,
	}
	ob := &OperationBuilder{
		Chain: Chain{stepGroup{ts}},
	}
	return ob
}

// Then adds another step that runs after the previous one in sequence.
// The step can be either a simple step (NewStep) or dynamic step (NewDynamicStep).
func (ob *OperationBuilder) Then(step StepProtocol) *OperationBuilder {
	ts := testStep{
		StepProtocol: step,
		hookID:       0,
	}

	// Add step as a new stepGroup (sequential execution)
	ob.Chain = append(ob.Chain, stepGroup{ts})
	return ob
}

// MaybeThen is like Then, but only adds the step if the conditional is true.
func (ob *OperationBuilder) MaybeThen(condition bool, step StepProtocol) *OperationBuilder {
	if !condition {
		return ob
	}
	ts := testStep{
		StepProtocol: step,
		hookID:       0,
	}

	// Add step as a new stepGroup (sequential execution)
	ob.Chain = append(ob.Chain, stepGroup{ts})
	return ob
}

// And adds a step that can run in parallel with the previous step.
// The step can be either a simple step (NewStep) or dynamic step (NewDynamicStep).
func (ob *OperationBuilder) And(step StepProtocol) *OperationBuilder {
	ts := testStep{
		StepProtocol: step,
		hookID:       0,
	}

	if len(ob.Chain) == 0 {
		panic("no step group found to add an And() step to")
	}

	// Add to the last stepGroup
	lastStepGroupIndex := len(ob.Chain) - 1
	ob.Chain[lastStepGroupIndex] = append(ob.Chain[lastStepGroupIndex], ts)

	return ob
}

// MaybeAnd is like And, but only adds the step if the conditional is true.
func (ob *OperationBuilder) MaybeAnd(condition bool, step StepProtocol) *OperationBuilder {
	if !condition {
		return ob
	}
	ts := testStep{
		StepProtocol: step,
		hookID:       0,
	}

	if len(ob.Chain) == 0 {
		panic("no step group found to add an And() step to")
	}

	// Add to the last stepGroup
	lastStepGroupIndex := len(ob.Chain) - 1
	ob.Chain[lastStepGroupIndex] = append(ob.Chain[lastStepGroupIndex], ts)

	return ob
}

// DynamicOperationBuilder allows fluent construction of dynamic operations.
// Use PrePlan() to set the planning function and WithRun() to set the execution function.
// Implements StepProtocol so it can be used directly without calling Build().
//
// Example:
//
//	modular.NewDynamicOperation[IndexPlan]("add index").
//	    PrePlan(func(ctx, l, h) (*IndexPlan, error) { ... }).
//	    WithRun(func(ctx, l, h, plan) error { ... }).
//	    WithDynamicName(func(plan) string { ... })
type DynamicOperationBuilder[T any] struct {
	name      string
	prePlanFn func(context.Context, *logger.Logger, *Helper) (T, error)
	runFn     func(context.Context, *logger.Logger, *Helper, T) error
	opts      []DynamicStepOption[T]
	step      *DynamicStep[T] // Lazily built when StepProtocol methods are called
}

// NewDynamicOperation creates a new builder for a dynamic operation.
// Use the fluent methods PrePlan() and Run() to configure the operation.
func NewDynamicOperation[T any](stepName string) *DynamicOperationBuilder[T] {
	return &DynamicOperationBuilder[T]{
		name: stepName,
		opts: make([]DynamicStepOption[T], 0),
	}
}

// PrePlan sets the planning function that runs during the planning phase.
// This function should make runtime decisions and return a plan object.
func (b *DynamicOperationBuilder[T]) PrePlan(fn func(context.Context, *logger.Logger, *Helper) (T, error)) *DynamicOperationBuilder[T] {
	b.prePlanFn = fn
	b.step = nil // Invalidate cached step
	return b
}

// WithRun sets the execution function that runs during the execution phase.
// This function receives the plan created by PrePlan and executes the operation.
func (b *DynamicOperationBuilder[T]) WithRun(fn func(context.Context, *logger.Logger, *Helper, T) error) *DynamicOperationBuilder[T] {
	b.runFn = fn
	b.step = nil // Invalidate cached step
	return b
}

// WithDynamicName adds a callback to generate the operation name from the plan.
func (b *DynamicOperationBuilder[T]) WithDynamicName(fn func(T) string) *DynamicOperationBuilder[T] {
	b.opts = append(b.opts, WithDynamicName(fn))
	b.step = nil // Invalidate cached step
	return b
}

// WithDynamicResourceCallback adds a callback to declare resource dependencies from the plan.
func (b *DynamicOperationBuilder[T]) WithDynamicResourceCallback(fn func(T) ([]ResourceAccess, []ResourceAccess)) *DynamicOperationBuilder[T] {
	b.opts = append(b.opts, WithDynamicResourceCallback(fn))
	b.step = nil // Invalidate cached step
	return b
}

// WithDynamicResourceAccess adds static resource access declarations to the dynamic step.
func (b *DynamicOperationBuilder[T]) WithDynamicResourceAccess(accesses ...ResourceAccess) *DynamicOperationBuilder[T] {
	b.opts = append(b.opts, WithDynamicResourceAccess[T](accesses...))
	b.step = nil // Invalidate cached step
	return b
}

// getOrBuildStep lazily builds the DynamicStep when needed.
func (b *DynamicOperationBuilder[T]) getOrBuildStep() *DynamicStep[T] {
	if b.step == nil {
		if b.prePlanFn == nil {
			panic("PrePlan function must be set before using DynamicOperationBuilder")
		}
		if b.runFn == nil {
			panic("Run function must be set before using DynamicOperationBuilder")
		}
		b.step = NewDynamicStep(b.name, b.prePlanFn, b.runFn, b.opts...)
	}
	return b.step
}

// Description implements StepProtocol.
func (b *DynamicOperationBuilder[T]) Description() string {
	return b.getOrBuildStep().Description()
}

// Run implements StepProtocol.
func (b *DynamicOperationBuilder[T]) Run(ctx context.Context, l *logger.Logger, h *Helper) error {
	return b.getOrBuildStep().Run(ctx, l, h)
}

// GetResourceAccesses implements ResourceAware.
func (b *DynamicOperationBuilder[T]) GetResourceAccesses() []ResourceAccess {
	return b.getOrBuildStep().GetResourceAccesses()
}

// GetResourceReleases implements ResourceAware.
func (b *DynamicOperationBuilder[T]) GetResourceReleases() []ResourceAccess {
	return b.getOrBuildStep().GetResourceReleases()
}

func (t *Test) AddOperation(stage *Stage, op Operation, opts ...StepOption) {
	var newChain chain
	for _, group := range op.Chain() {
		steps := make([]testStep, len(group))
		for i, step := range group {
			// Preserve the original step with its hookID updated
			// We need to keep the original StepProtocol (DynamicStep, SingleStep, etc.)
			// to avoid losing type information
			step.hookID = t.nextHookID()

			// If user provided additional options, we need to apply them
			// For now, just preserve the step as-is since options are handled during operation construction
			steps[i] = step
		}
		newChain = append(newChain, steps)
	}

	stage.chains = append(stage.chains, newChain)
}
