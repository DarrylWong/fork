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

// ResourceAware is an optional interface that steps can implement
// to declare their resource access/lock requirements.
type ResourceAware interface {
	GetResourceAccesses() []ResourceAccess
	GetResourceReleases() []ResourceAccess
}

// sanitizeStepName removes characters that might cause issues in logger names.
func sanitizeStepName(name string) string {
	// Replace colons and spaces with underscores for logger compatibility
	result := strings.ReplaceAll(name, ":", "_")
	result = strings.ReplaceAll(result, " ", "_")
	return result
}

func NewSingleStep(description string, fn stepFunc, opts ...StepOption) *SingleStep {
	ss := &SingleStep{
		description: description,
		fn:          fn,
	}

	// Apply step options
	for _, opt := range opts {
		opt(ss)
	}
	return ss
}

// NewStep is an alias for NewSingleStep for consistency with NewDynamicStep.
func NewStep(description string, fn stepFunc, opts ...StepOption) *SingleStep {
	return NewSingleStep(description, fn, opts...)
}

// SingleStep implements a basic test step.
type SingleStep struct {
	description         string
	fn                  stepFunc
	background          shouldStop
	concurrencyDisabled bool
	resources           struct {
		accesses []ResourceAccess
		releases []ResourceAccess
	}
}

// Description returns a human-readable description of the step.
func (s *SingleStep) Description() string {
	return s.description
}

// Run executes the step function.
func (s *SingleStep) Run(ctx context.Context, l *logger.Logger, h *Helper) error {
	return s.fn(ctx, l, h)
}

// GetResourceAccesses returns the resource accesses declared by this step.
func (s *SingleStep) GetResourceAccesses() []ResourceAccess {
	return s.resources.accesses
}

// GetResourceReleases returns the resource releases declared by this step.
func (s *SingleStep) GetResourceReleases() []ResourceAccess {
	return s.resources.releases
}

// DynamicStep is a test step that performs planning before execution.
// The PrePlan function is called during stage planning (before execution)
// and its result is passed to the Run function during execution.
// This is useful for steps that need to make runtime decisions like
// "pick a random table" that should be determined during planning.
type DynamicStep[T any] struct {
	description         string
	prePlanFn           func(context.Context, *logger.Logger, *Helper) (T, error)
	runFn               func(context.Context, *logger.Logger, *Helper, T) error
	background          shouldStop
	concurrencyDisabled bool
	resources           struct {
		accesses []ResourceAccess
		releases []ResourceAccess
	}
	// resourceCallback extracts resource declarations from the PrePlan result.
	// This enables truly dynamic resource access based on runtime decisions.
	resourceCallback func(T) (accesses []ResourceAccess, releases []ResourceAccess)
	// nameCallback generates a dynamic name based on the PrePlan result.
	// This allows step names to reflect runtime-determined information.
	nameCallback func(T) string
	planResult   T // Cached result from PrePlan, set during planning phase
}

// DynamicStepOption configures a DynamicStep.
type DynamicStepOption[T any] func(*DynamicStep[T])

// NewDynamicStep creates a new dynamic step with PrePlan and Run callbacks.
func NewDynamicStep[T any](
	description string,
	prePlanFn func(context.Context, *logger.Logger, *Helper) (T, error),
	runFn func(context.Context, *logger.Logger, *Helper, T) error,
	opts ...DynamicStepOption[T],
) *DynamicStep[T] {
	ds := &DynamicStep[T]{
		description: description,
		prePlanFn:   prePlanFn,
		runFn:       runFn,
	}

	// Apply step options
	for _, opt := range opts {
		opt(ds)
	}
	return ds
}

// WithDynamicInBackground configures a dynamic step to run in the background.
func WithDynamicInBackground[T any]() DynamicStepOption[T] {
	return func(step *DynamicStep[T]) {
		step.background = make(shouldStop)
	}
}

// WithDynamicDisableConcurrency disables concurrent execution for a dynamic step.
func WithDynamicDisableConcurrency[T any]() DynamicStepOption[T] {
	return func(step *DynamicStep[T]) {
		step.concurrencyDisabled = true
	}
}

// WithDynamicResourceAccess adds resource access declarations to a dynamic step.
func WithDynamicResourceAccess[T any](accesses ...ResourceAccess) DynamicStepOption[T] {
	return func(step *DynamicStep[T]) {
		step.resources.accesses = append(step.resources.accesses, accesses...)
	}
}

// WithDynamicResourceRelease adds resource release declarations to a dynamic step.
func WithDynamicResourceRelease[T any](releases ...ResourceAccess) DynamicStepOption[T] {
	return func(step *DynamicStep[T]) {
		step.resources.releases = append(step.resources.releases, releases...)
	}
}

// WithDynamicResourceCallback sets a callback that extracts resource declarations
// from the PrePlan result. This enables truly dynamic resource access where the
// resources are determined at planning time based on runtime decisions.
//
// Example usage:
//
//	NewDynamicStep(
//	  "add index to random table",
//	  prePlanFn, // returns a plan that includes which table was selected
//	  runFn,
//	  WithDynamicResourceCallback(func(plan *IndexPlan) ([]ResourceAccess, []ResourceAccess) {
//	    // Extract the table from the plan and declare a lock on it
//	    access := SchemaChangeAccess{Database: plan.DBName, Table: plan.TableName}.Resource(true)
//	    return []ResourceAccess{access}, []ResourceAccess{access}
//	  }),
//	)
func WithDynamicResourceCallback[T any](
	callback func(T) (accesses []ResourceAccess, releases []ResourceAccess),
) DynamicStepOption[T] {
	return func(step *DynamicStep[T]) {
		step.resourceCallback = callback
	}
}

// WithDynamicName sets a callback that generates a dynamic name based on the PrePlan result.
// This allows step names to reflect runtime-determined information like which table was selected.
//
// Example usage:
//
//	NewDynamicStep(
//	  "add index to random table",
//	  prePlanFn,
//	  runFn,
//	  WithDynamicName(func(plan *IndexPlan) string {
//	    return fmt.Sprintf("add index to %s.%s", plan.DBName, plan.TableName)
//	  }),
//	)
func WithDynamicName[T any](callback func(T) string) DynamicStepOption[T] {
	return func(step *DynamicStep[T]) {
		step.nameCallback = callback
	}
}

// Description returns a human-readable description of the step.
// If a dynamic name callback is set and PrePlan has been called, returns the dynamic name.
func (ds *DynamicStep[T]) Description() string {
	// If we have a PrePlan result and a name callback, use the dynamic name
	if ds.nameCallback != nil {
		var zero T
		if any(ds.planResult) != any(zero) {
			return ds.nameCallback(ds.planResult)
		}
	}
	return ds.description
}

// Run executes the step's regular Run function (for compatibility with StepProtocol).
// This is called when the step is used in a static runner context.
// For dynamic runners, use RunWithPlan instead.
func (ds *DynamicStep[T]) Run(ctx context.Context, l *logger.Logger, h *Helper) error {
	// If planResult is not set, we need to call PrePlan first
	var zero T
	if any(ds.planResult) == any(zero) {
		result, err := ds.prePlanFn(ctx, l, h)
		if err != nil {
			return fmt.Errorf("PrePlan failed: %w", err)
		}
		ds.planResult = result
	}
	return ds.runFn(ctx, l, h, ds.planResult)
}

// PrePlan executes the planning function and caches the result.
// This is called during the planning phase in the dynamic runner.
// If PrePlan has already been called, it returns the cached result (idempotent).
func (ds *DynamicStep[T]) PrePlan(ctx context.Context, l *logger.Logger, h *Helper) (interface{}, error) {
	// If already called, return cached result (makes this idempotent)
	var zero T
	if any(ds.planResult) != any(zero) {
		return ds.planResult, nil
	}

	// Execute the planning function
	result, err := ds.prePlanFn(ctx, l, h)
	if err != nil {
		return nil, err
	}
	ds.planResult = result
	return result, nil
}

// RunWithPlan executes the step with a previously planned result.
// This is called during execution in the dynamic runner.
func (ds *DynamicStep[T]) RunWithPlan(ctx context.Context, l *logger.Logger, h *Helper, planResult interface{}) error {
	// Type assert the plan result back to T
	typedResult, ok := planResult.(T)
	if !ok {
		var zero T
		typedResult = zero
	}
	return ds.runFn(ctx, l, h, typedResult)
}

// GetResourceAccesses returns the resource accesses declared by this step.
// This includes both statically-declared resources (via WithDynamicResourceAccess)
// and dynamically-declared resources (via WithDynamicResourceCallback, after PrePlan).
func (ds *DynamicStep[T]) GetResourceAccesses() []ResourceAccess {
	accesses := ds.resources.accesses

	// If we have a PrePlan result and a resource callback, include dynamic resources
	if ds.resourceCallback != nil {
		var zero T
		if any(ds.planResult) != any(zero) {
			dynamicAccesses, _ := ds.resourceCallback(ds.planResult)
			accesses = append(accesses, dynamicAccesses...)
		}
	}

	return accesses
}

// GetResourceReleases returns the resource releases declared by this step.
// This includes both statically-declared resources (via WithDynamicResourceRelease)
// and dynamically-declared resources (via WithDynamicResourceCallback, after PrePlan).
func (ds *DynamicStep[T]) GetResourceReleases() []ResourceAccess {
	releases := ds.resources.releases

	// If we have a PrePlan result and a resource callback, include dynamic resources
	if ds.resourceCallback != nil {
		var zero T
		if any(ds.planResult) != any(zero) {
			_, dynamicReleases := ds.resourceCallback(ds.planResult)
			releases = append(releases, dynamicReleases...)
		}
	}

	return releases
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
