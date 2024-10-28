package fiplanner

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/cockroachdb/cockroach/pkg/util/randutil"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
	"gopkg.in/yaml.v2"
)

var registerFailuresHook = registerFailures
var generatePlanIDHook = generatePlanID

func GenerateStaticPlan(clusterSizes []int, spec FailurePlanSpec, numSteps int) ([]byte, error) {
	registry := makeFailureRegistry(spec)
	registerFailuresHook(&registry)

	rng := rand.New(rand.NewSource(spec.Seed))
	steps := make([]FailureStep, 0, numSteps)
	for stepID := 1; stepID <= numSteps; stepID++ {

		newStep, err := GenerateStep(registry, spec, rng, clusterSizes, stepID)
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

type DynamicFailurePlan struct {
	PlanID           string
	TolerateErrors   bool
	Seed             int64
	DisabledFailures []string
	MinWait          time.Duration
	MaxWait          time.Duration
}

type StaticFailurePlan struct {
	PlanID         string        `yaml:"plan_id"`
	ClusterNames   []string      `yaml:"cluster_names"`
	TolerateErrors bool          `yaml:"tolerate_errors,omitempty"`
	Steps          []FailureStep `yaml:"steps"`
}

type FailureStep struct {
	StepID      int               `yaml:"step_id"`
	FailureType string            `yaml:"failure_type"`
	Cluster     string            `yaml:"cluster,omitempty"` // Which cluster to target. Can be left empty if only one cluster.
	Node        int               `yaml:"node"`
	Delay       time.Duration     `yaml:"delay"`          // Amount of time to delay before reversing a failure.
	Args        map[string]string `yaml:"args,omitempty"` // FailureType specific arguments.
}

func GenerateStep(
	r failureRegistry, spec FailurePlanSpec, rng *rand.Rand, clusterSizes []int, stepID int,
) (FailureStep, error) {
	clusterToTarget := rng.Intn(len(clusterSizes))
	nodeToTarget := rng.Intn(clusterSizes[clusterToTarget]) + 1
	delayInNanoseconds := randutil.RandInt63InRange(rng, spec.minWait.Nanoseconds(), spec.maxWait.Nanoseconds())

	failure := r.GetRandomFailure(rng)

	return FailureStep{
		FailureType: failure.Name,
		StepID:      stepID,
		Cluster:     spec.ClusterNames[clusterToTarget],
		Node:        nodeToTarget,
		Delay:       time.Duration(delayInNanoseconds).Truncate(time.Second),
		Args:        failure.GenerateArgs(rng),
	}, nil
}

func generatePlanID(prefix string) string {
	secs := timeutil.Now().Unix()
	return fmt.Sprintf("%s-%d", prefix, secs)
}
