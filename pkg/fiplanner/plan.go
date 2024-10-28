package fiplanner

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/cockroachdb/cockroach/pkg/fiplanner/failures"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
)

var registerFailuresHook = failures.RegisterFailures
var generatePlanIDHook = generatePlanID

type FailurePlanSpec interface {
	Validate() error
	GeneratePlan() ([]byte, error)
}

type FailureStep struct {
	StepID      int               `yaml:"step_id"`
	FailureType string            `yaml:"failure_type"`
	Cluster     string            `yaml:"cluster,omitempty"` // Which cluster to target. Can be left empty if only one cluster.
	Node        int               `yaml:"node"`
	Delay       time.Duration     `yaml:"delay"`          // Amount of time to delay before reversing a failure.
	Args        map[string]string `yaml:"args,omitempty"` // FailureType specific arguments.
}

func generatePlanID(prefix string) string {
	secs := timeutil.Now().Unix()
	return fmt.Sprintf("%s-%d", prefix, secs)
}

type StepGenerator struct {
	registry failures.FailureRegistry
	plan     DynamicFailurePlan
	rng      *rand.Rand
}

// NewStepGenerator parses a given dynamic failure plan and returns
// a step generator. This step generator can be used to generate
// new failure steps based on the failure plan.
func NewStepGenerator(plan DynamicFailurePlan) *StepGenerator {
	registry := failures.MakeFailureRegistry(plan.DisabledFailures)
	registerFailuresHook(&registry)
	return &StepGenerator{registry: registry, plan: plan, rng: rand.New(rand.NewSource(plan.Seed))}
}

func (g *StepGenerator) randomDelay() time.Duration {
	delayInNanoseconds := randutil.RandInt63InRange(g.rng, g.plan.MinWait.Nanoseconds(), g.plan.MaxWait.Nanoseconds())
	return time.Duration(delayInNanoseconds).Truncate(time.Second)
}

// GenerateStep generates and returns a new failure step. Accept a stepID instead
// of keeping track of it in the generator so that way we can resume a plan.
// clusterNames and clusterSizes are also passed in to allow for a change in either
// mid-plan.
func (g *StepGenerator) GenerateStep(
	stepID int, clusterNames []string, clusterSizes []int,
) (FailureStep, error) {
	clusterToTarget := g.rng.Intn(len(clusterSizes))
	nodeToTarget := g.rng.Intn(clusterSizes[clusterToTarget]) + 1

	failure := g.registry.GetRandomFailure(g.rng)

	return FailureStep{
		FailureType: failure.Name,
		StepID:      stepID,
		Cluster:     clusterNames[clusterToTarget],
		Node:        nodeToTarget,
		Delay:       g.randomDelay(),
		Args:        failure.GenerateArgs(g.rng),
	}, nil
}
