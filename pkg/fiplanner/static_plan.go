package fiplanner

import (
	"time"

	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v2"
)

// StaticFailurePlan is a type of failure plan used when we have a fixed
// cluster state and know the exact number of steps we want to run. Example
// uses for this plan could be a DRT or adhoc cluster where we have a
// long-running cluster and may not want infinitely generate steps. It can
// also be used to handcraft a failure plan for debugging purposes, as the
// pre-generated can be manually edited.
type StaticFailurePlan struct {
	PlanID         string        `yaml:"plan_id"`
	ClusterNames   []string      `yaml:"cluster_names"`
	TolerateErrors bool          `yaml:"tolerate_errors,omitempty"`
	Steps          []FailureStep `yaml:"steps"`
}

// StaticFailurePlanSpec contains the information needed to generate a
// StaticFailurePlan.
//
// From the planner's perspective, the main difference between a static and
// dynamic plan is that the static plan generates ahead of time while the
// dynamic plan will asynchronously generate steps as needed. Because of this,
// the methods to generate steps are the same so a static plan spec is implicitly
// converted to a dynamic plan by the framework.
type StaticFailurePlanSpec struct {
	// User is used along with the current time to generate a unique plan ID.
	User string
	// Cluster(s) to target. Note the support for test that use multiple
	// clusters i.e. c2c.
	ClusterNames []string
	// ClusterSizes is the number of nodes in each cluster. The length and
	// order should correlate with the number of clusters specified in ClusterNames.
	ClusterSizes []int
	// If true, continue executing the plan even if some steps fail.
	TolerateErrors bool
	// Seed used to generate new failure injection steps. 0 indicates
	// that the planner should generate a seed. Flexibility is given
	// for both as i.e. roachtest may want control over how the seed
	// is generated but an ad hoc roachprod experiment won't.
	Seed             int64
	DisabledFailures []string
	// How many steps to generate.
	NumSteps int
	// How long to inject a failure. Time range of acceptable pause times.
	// The actual pause time is randomly chosen based off the seed when
	// each step is generated.
	MinWait time.Duration
	MaxWait time.Duration
}

// GeneratePlan generates a new static failure plan based on a static failure plan.
// It does so by:
//  1. Converting the static failure spec into a dynamic failure plan.
//  2. Creating a new step generator based on the dynamic plan.
//  3. Generating new steps until spec.NumSteps has been reached.
//  4. Parsing the static spec and generated steps into YAML.
func (spec StaticFailurePlanSpec) GeneratePlan() ([]byte, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	dynamicPlan := spec.GenerateDynamicPlan()
	gen := NewStepGenerator(dynamicPlan)

	steps := make([]FailureStep, 0, spec.NumSteps)

	for stepID := 1; stepID <= spec.NumSteps; stepID++ {

		newStep, err := gen.GenerateStep(stepID, spec.ClusterNames, spec.ClusterSizes)
		if err != nil {
			return nil, err
		}

		steps = append(steps, newStep)
	}

	plan := StaticFailurePlan{
		PlanID:         generatePlanIDHook(spec.User),
		ClusterNames:   spec.ClusterNames,
		TolerateErrors: spec.TolerateErrors,
		Steps:          steps,
	}

	return yaml.Marshal(plan)
}

// Validate checks that the static failure plan spec is valid.
func (spec StaticFailurePlanSpec) Validate() error {
	if len(spec.ClusterNames) == 0 {
		return errors.New("error validating failure plan spec: at least one cluster must be specified")
	}

	if len(spec.ClusterSizes) != len(spec.ClusterNames) {
		return errors.New("error validating failure plan spec: each cluster in ClusterNames must have a corresponding size in ClusterSizes")
	}

	if spec.NumSteps <= 0 {
		return errors.New("error validating failure plan spec: NumSteps must be greater than 0")
	}

	if spec.User == "" {
		return errors.New("error validating failure plan spec: user must be specified")
	}

	if spec.MaxWait < spec.MinWait {
		return errors.New("error validating failure plan spec: MaxWait must be greater than or equal to MinWait")
	}

	return nil
}

// GenerateDynamicPlan constructs a dynamic failure plan based on a static plan spec.
// The dynamic plan can be then used to create a step generator to generate failure steps.
// Note we still need the static plan spec as it contains information related to cluster
// state and number of steps to generate.
func (spec StaticFailurePlanSpec) GenerateDynamicPlan() DynamicFailurePlan {
	return DynamicFailurePlan{
		PlanID:           generatePlanIDHook(spec.User),
		TolerateErrors:   spec.TolerateErrors,
		Seed:             spec.Seed,
		DisabledFailures: spec.DisabledFailures,
		MinWait:          spec.MinWait,
		MaxWait:          spec.MaxWait,
	}
}
