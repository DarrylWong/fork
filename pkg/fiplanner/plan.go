package fiplanner

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/cockroachdb/cockroach/pkg/fiplanner/failures"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
)

var registerFailuresHook = failures.RegisterFailures
var generatePlanIDHook = generatePlanID

type FailurePlanSpec interface {
	Validate() error
	GeneratePlan() ([]byte, error)
	RandomDelay(*rand.Rand) time.Duration
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
	r failures.FailureRegistry,
	spec FailurePlanSpec,
	rng *rand.Rand,
	clusterNames []string,
	clusterSizes []int,
	stepID int,
) (FailureStep, error) {
	clusterToTarget := rng.Intn(len(clusterSizes))
	nodeToTarget := rng.Intn(clusterSizes[clusterToTarget]) + 1

	failure := r.GetRandomFailure(rng)

	return FailureStep{
		FailureType: failure.Name,
		StepID:      stepID,
		Cluster:     clusterNames[clusterToTarget],
		Node:        nodeToTarget,
		Delay:       spec.RandomDelay(rng),
		Args:        failure.GenerateArgs(rng),
	}, nil
}

func generatePlanID(prefix string) string {
	secs := timeutil.Now().Unix()
	return fmt.Sprintf("%s-%d", prefix, secs)
}
