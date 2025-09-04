package modular

import (
	"math"
	"math/rand"
)

type TestPlanner struct {
	name   string
	seed   int64
	rng    *rand.Rand
	stages []Stage
}

// DAG generates a directed acyclic graph representation of all test steps and their dependencies.
func (p *TestPlanner) DAG() string {
	return GenerateDAG(p.stages)
}

func (p *TestPlanner) Plan() (*TestPlan, error) {
	var stagePlans []sequentialRunStep
	for _, stage := range p.stages {
		stagePlans = append(stagePlans, p.generateStagePlan(stage))
	}

	return &TestPlan{
		name:       p.name,
		seed:       p.seed,
		stagePlans: stagePlans,
	}, nil
}

// generateStagePlan generates a random legal permutation of
func (p *TestPlanner) generateStagePlan(s Stage) sequentialRunStep {
	// Start with a known valid ordering of steps: All the steps in the first chain
	// sequentially, followed by all the steps in the second chain and so on.
	steps := s.Steps()
	numSteps := len(steps)
	randomPairIndices := func() (int, int) {
		index := p.rng.Intn(numSteps - 1)
		return index, index + 1
	}
	isValidSwap := func(i, j testStep) bool {
		// We can swap the two steps if they are in different chains.
		if i.order.chainID != j.order.chainID {
			return true
		}
		// We can swap the two steps if they are part of the same
		// step group.
		return i.order.depth == j.order.depth
	}

	// Our randomization mixes in n^3*log(n) iterations.
	iterations := int(math.Ceil(math.Pow(float64(numSteps), 3) * math.Log(float64(numSteps))))
	for proposedSwaps := 0; proposedSwaps < iterations; proposedSwaps++ {
		first, second := randomPairIndices()
		// We can swap the two steps if they are in different chains,
		// or if they are in the same stepGroup.
		if isValidSwap(steps[first], steps[second]) {
			steps[first], steps[second] = steps[second], steps[first]
		}
	}

	return sequentialRunStep{
		label: s.name,
		steps: steps,
	}
}

// sequentialRunStep is a "meta-step" that indicates that a sequence
// of steps are to be executed sequentially. The default test runner
// already runs steps sequentially. This meta-step exists primarily as
// a way to group related steps so that a test plan is easier to
// understand for a human.
type sequentialRunStep struct {
	label string
	steps []testStep
}

type TestPlan struct {
	name       string
	seed       int64
	stagePlans []sequentialRunStep
}

func (p *TestPlan) Steps() []testStep {
	var allSteps []testStep
	for _, stagePlan := range p.stagePlans {
		allSteps = append(allSteps, stagePlan.steps...)
	}
	return allSteps
}
