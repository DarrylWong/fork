package fiplanner

import (
	"math/rand"
	"os"
	"time"

	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v2"
)

// DynamicFailurePlan is a type of failure plan that contains the information
// needed to generate new steps but does not do so yet. Actual generation of
// failure steps is done as needed by the consumer of the plan. The main use
// of this mode is for roachtests where we cannot predetermine the exact number
// of steps required to cover an entire test due to fluctuating run times or
// failure injection impact. Instead, roachtests can have steps generated infinitely
// until the test is complete.
type DynamicFailurePlan struct {
	PlanID           string        `yaml:"plan_id"`
	TolerateErrors   bool          `yaml:"tolerate_errors,omitempty"`
	Seed             int64         `yaml:"seed"`
	DisabledFailures []string      `yaml:"disabled_failures,omitempty"`
	MinWait          time.Duration `yaml:"min_wait"`
	MaxWait          time.Duration `yaml:"max_wait"`
}

// DynamicFailurePlanSpec contains the information needed to generate a
// DynamicFailurePlan.
type DynamicFailurePlanSpec struct {
	// User is used along with the current time to generate a unique plan ID.
	User string
	// If true, continue executing the plan even if some steps fail.
	TolerateErrors bool
	// Seed used to generate new failure injection steps. 0 indicates
	// that the planner should generate a seed. Flexibility is given
	// for both as i.e. roachtest may want control over how the seed
	// is generated but an ad hoc roachprod experiment won't.
	Seed             int64
	DisabledFailures []string
	// How long to inject a failure. Time range of acceptable pause times.
	// The actual pause time is randomly chosen based off the seed when
	// each step is generated.
	minWait time.Duration
	maxWait time.Duration
}

// GeneratePlan generates a new dynamic failure plan based on a dynamic failure plan spec.
func (spec DynamicFailurePlanSpec) GeneratePlan() ([]byte, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	rng := rand.New(rand.NewSource(spec.Seed))

	// Generate a new seed if one is not provided.
	planSeed := spec.Seed
	for planSeed == 0 {
		planSeed = rng.Int63()
	}

	plan := DynamicFailurePlan{
		PlanID:           generatePlanIDHook(spec.User),
		TolerateErrors:   spec.TolerateErrors,
		Seed:             spec.Seed,
		DisabledFailures: spec.DisabledFailures,
		MinWait:          spec.minWait,
		MaxWait:          spec.maxWait,
	}

	return yaml.Marshal(plan)
}

// Validate checks that the dynamic failure plan spec is valid.
func (spec DynamicFailurePlanSpec) Validate() error {
	if spec.User == "" {
		return errors.New("error validating failure plan spec: user must be specified")
	}

	if spec.maxWait < spec.minWait {
		return errors.New("error validating failure plan spec: maxWait must be greater than or equal to minWait")
	}

	return nil
}

// ParseDynamicPlanFromFile reads a dynamic failure plan from a YAML file and
// unmarshals it into a DynamicFailurePlan.
func ParseDynamicPlanFromFile(planFile string) (DynamicFailurePlan, error) {
	planBytes, err := os.ReadFile(planFile)
	if err != nil {
		return DynamicFailurePlan{}, err
	}
	var plan DynamicFailurePlan
	if err = yaml.Unmarshal(planBytes, &plan); err != nil {
		return DynamicFailurePlan{}, err
	}
	return plan, nil
}
