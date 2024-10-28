package fiplanner

import (
	"math/rand"
	"time"

	"github.com/cockroachdb/cockroach/pkg/fiplanner/failures"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
	"github.com/cockroachdb/errors"
	"gopkg.in/yaml.v2"
)

type DynamicFailurePlan struct {
	PlanID           string        `yaml:"plan_id"`
	TolerateErrors   bool          `yaml:"tolerate_errors,omitempty"`
	Seed             int64         `yaml:"seed"`
	DisabledFailures []string      `yaml:"disabled_failures,omitempty"`
	MinWait          time.Duration `yaml:"min_wait"`
	MaxWait          time.Duration `yaml:"max_wait"`
}

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

func (spec DynamicFailurePlanSpec) GeneratePlan() ([]byte, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	registry := failures.MakeFailureRegistry(spec.DisabledFailures)
	registerFailuresHook(&registry)

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

func (spec DynamicFailurePlanSpec) RandomDelay(rng *rand.Rand) time.Duration {
	delayInNanoseconds := randutil.RandInt63InRange(rng, spec.minWait.Nanoseconds(), spec.maxWait.Nanoseconds())
	return time.Duration(delayInNanoseconds).Truncate(time.Second)
}

func (spec DynamicFailurePlanSpec) Validate() error {
	if spec.User == "" {
		return errors.New("error validating failure plan spec: user must be specified")
	}

	if spec.maxWait < spec.minWait {
		return errors.New("error validating failure plan spec: maxWait must be greater than or equal to minWait")
	}

	return nil
}
