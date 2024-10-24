package fiplanner

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/cockroachdb/cockroach/pkg/util/randutil"
)

func GenerateStaticPlan(clusterSizes []int, spec FailurePlanSpec, numSteps int) error {
	registry := makeFailureRegistry(spec)
	RegisterFailures(&registry)

	rng := rand.New(rand.NewSource(spec.Seed))
	plan := make([]FailureStep, 0, numSteps)
	for stepID := 1; stepID <= numSteps; stepID++ {

		newStep, err := GenerateStep(registry, spec, rng, clusterSizes, stepID)
		if err != nil {
			return err
		}

		plan = append(plan, newStep)
	}

	fmt.Printf("generatedPlan: %+v\n", plan)
	return nil
}

type FailureStep struct {
	FailureType string
	StepID      int
	// Which cluster to target. Can be left empty if only one cluster.
	Cluster string
	Node    int
	// Amount of time to delay before reversing a failure.
	Delay time.Duration
	// FailureType specific arguments.
	Args map[string]string
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
		Delay:       time.Duration(delayInNanoseconds),
		Args:        failure.GenerateArgs(rng),
	}, nil
}
