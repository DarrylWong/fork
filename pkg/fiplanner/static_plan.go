package fiplanner

import (
	"time"

	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v2"
)

type StaticFailurePlan struct {
	PlanID         string        `yaml:"plan_id"`
	ClusterNames   []string      `yaml:"cluster_names"`
	TolerateErrors bool          `yaml:"tolerate_errors,omitempty"`
	Steps          []FailureStep `yaml:"steps"`
}

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
	minWait time.Duration
	maxWait time.Duration
}

func (spec StaticFailurePlanSpec) GeneratePlan() ([]byte, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	dynamicPlan := spec.GenerateDynamicPlan()
	gen, err := NewStepGenerator(dynamicPlan)
	if err != nil {
		return nil, err
	}
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

	if spec.maxWait < spec.minWait {
		return errors.New("error validating failure plan spec: maxWait must be greater than or equal to minWait")
	}

	return nil
}

func (spec StaticFailurePlanSpec) GenerateDynamicPlan() DynamicFailurePlan {
	return DynamicFailurePlan{
		PlanID:           generatePlanIDHook(spec.User),
		TolerateErrors:   spec.TolerateErrors,
		Seed:             spec.Seed,
		DisabledFailures: spec.DisabledFailures,
		MinWait:          spec.minWait,
		MaxWait:          spec.maxWait,
	}
}
