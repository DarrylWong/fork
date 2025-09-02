package modular

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
)

// Planner generates test plans from test definitions.
type Planner interface {
	Plan() (*TestPlan, error)
}

// SimplePlanner implements basic test planning.
type SimplePlanner struct {
	test   *Test
	rng    *rand.Rand
	config PlannerConfig
}

// PlannerConfig configures the test planner.
type PlannerConfig struct {
	IsLocal           bool
	ConcurrencyChance float64 // Probability that each eligible step runs concurrently (default 0.25)
}

// NewSimplePlanner creates a new simple planner.
func NewSimplePlanner(test *Test, config PlannerConfig) *SimplePlanner {
	return &SimplePlanner{
		test:   test,
		rng:    rand.New(rand.NewSource(test.seed)),
		config: config,
	}
}

// Plan generates a test plan from the test definition.
func (p *SimplePlanner) Plan() (*TestPlan, error) {
	stages := p.test.Stages()

	var stagePlans []sequentialRunStep
	for _, stage := range stages {
		stagePlans = append(stagePlans, p.test.generateStagePlan(stage))
	}

	return &TestPlan{
		name:          p.test.name,
		seed:          p.test.seed,
		clusterSpecs:  p.test.clusterSpecs,
		workloadSpecs: p.test.workloadSpecs,
		stagePlans:    stagePlans,
		isLocal:       p.config.IsLocal,
	}, nil
}

// TestPlan represents the execution plan for a modular test.
type TestPlan struct {
	name          string
	seed          int64
	clusterSpecs  []ClusterSpec
	workloadSpecs []WorkloadSpec
	stagePlans    []sequentialRunStep
	isLocal       bool
}

// String pretty prints the plan, with the test name, seed listed at the top,
// along with each step in order.
func (tp *TestPlan) String() string {
	var b strings.Builder

	// Header with test name, seed, and mode
	b.WriteString(fmt.Sprintf("Modular Test Plan: %s (seed: %d)\n", tp.name, tp.seed))
	if tp.isLocal {
		b.WriteString("Mode: Local\n")
	} else {
		b.WriteString("Mode: Distributed\n")
	}
	b.WriteString("Plan:\n")

	// Cluster specifications
	if len(tp.clusterSpecs) > 0 {
		b.WriteString("Cluster Specifications:\n")
		for _, spec := range tp.clusterSpecs {
			b.WriteString(fmt.Sprintf("  %s: %d nodes (%d-%d range)", spec.Name, spec.ActualNodes, spec.MinNodes, spec.MaxNodes))
			b.WriteString(fmt.Sprintf(" [mode: %s]", spec.DeploymentMode))
			if len(spec.DisabledDeploymentModes) > 0 {
				b.WriteString(fmt.Sprintf(" (disabled: %v)", spec.DisabledDeploymentModes))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Workload specifications
	if len(tp.workloadSpecs) > 0 {
		b.WriteString("Workload Specifications:\n")
		for _, spec := range tp.workloadSpecs {
			b.WriteString(fmt.Sprintf("  %s: %d nodes\n", spec.name, spec.numNodes))
		}
		b.WriteString("\n")
	}

	// List all steps in execution order
	for _, sequentialStep := range tp.stagePlans {
		for i, step := range sequentialStep.steps {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, step.Description()))
		}
	}

	return b.String()
}

// Steps returns all steps from all stage plans flattened into a single slice
func (tp *TestPlan) Steps() []testStep {
	var allSteps []testStep
	for _, stagePlan := range tp.stagePlans {
		allSteps = append(allSteps, stagePlan.steps...)
	}
	return allSteps
}

// Stages returns a slice all stages in order of execution.
func (t *Test) Stages() []*Stage {
	// Build unified stage list: setup + user stages + after-test
	allStages := make([]*Stage, 0)

	// Add setup stage if it exists
	if t.setupStage != nil {
		allStages = append(allStages, t.setupStage)
	}

	// Add user-defined stages
	allStages = append(allStages, t.stages...)

	// Add after-test stage if it exists
	if t.afterTestStage != nil {
		allStages = append(allStages, t.afterTestStage)
	}

	return allStages
}

func (t *Test) generateStagePlan(s *Stage) sequentialRunStep {
	// Start with a known valid ordering of steps: All the steps in the first chain
	// sequentially, followed by all the steps in the second chain and so on.
	steps := s.OrderedSteps()
	numSteps := len(steps)
	randomPairIndices := func() (int, int) {
		index := t.rng.Intn(numSteps - 1)
		return index, index + 1
	}

	var successfulSwaps int
	// Our randomization mixes in n^3*log(n) iterations.
	iterations := int(math.Ceil(10 * math.Pow(float64(numSteps), 3) * math.Log(float64(numSteps))))
	for successfulSwaps < iterations {
		first, second := randomPairIndices()
		// We can swap the two steps if they are in different chains,
		// or if they are in the same stepGroup.
		if steps[first].dependency.ValidSwap(steps[second].dependency) {
			steps[first], steps[second] = steps[second], steps[first]
			successfulSwaps++
		}
	}

	testSteps := make([]testStep, 0, numSteps)
	for _, step := range steps {
		testSteps = append(testSteps, step.step)
	}

	return sequentialRunStep{
		label: s.name,
		steps: testSteps,
	}
}

// stepGroup represents a group of test steps that must complete before the next
// stepGroup in a given chain can start.
type stepGroup []testStep

// Description returns a description for this stepGroup.
func (sg stepGroup) Description() string {
	if len(sg) == 1 {
		return sg[0].Description()
	}
	return fmt.Sprintf("parallel group with\n%d steps", len(sg))
}

// chain represents a sequence of step groups that must be executed in order
type chain []stepGroup

// MaxConcurrentSteps returns the maximum number of steps that can be run concurrently in this chain.
func (ch *chain) MaxConcurrentSteps() int {
	maxSteps := 0
	for _, gr := range *ch {
		if len(gr) > maxSteps {
			maxSteps = len(gr)
		}
	}
	return maxSteps
}

// Stage represents a group of test step chains that can be executed concurrently.
type Stage struct {
	name   string
	index  int
	chains []chain
}

// MaxConcurrentSteps returns the maximum number of steps that can be run concurrently in this stage.
func (s *Stage) MaxConcurrentSteps() int {
	maxSteps := 0
	for _, ch := range s.chains {
		maxGroupSize := 0
		for _, gr := range ch {
			if len(gr) > maxGroupSize {
				maxGroupSize = len(gr)
			}
		}
		maxSteps = maxSteps + maxGroupSize
	}

	return maxSteps
}

// Steps returns all the steps in this stage (flattened from all chains and stepGroups).
func (s *Stage) Steps() []testStep {
	var allSteps []testStep
	for _, chain := range s.chains {
		for _, stepGroup := range chain {
			allSteps = append(allSteps, stepGroup...)
		}
	}
	return allSteps
}

func (s *Stage) LongestChain() int {
	var longestChainLength int
	for _, c := range s.chains {
		if len(c) > longestChainLength {
			longestChainLength = len(c)
		}
	}
	return longestChainLength
}

// Chains returns the chains in this stage.
func (s *Stage) Chains() []chain {
	return s.chains
}

type stepDependency struct {
	chainID int
	depth   int
}

func (s stepDependency) ValidSwap(o stepDependency) bool {
	// We can swap the two steps if they are in different chains.
	if s.chainID != o.chainID {
		return true
	}
	// We can swap the two steps if they are part of the same
	// step group.
	return s.depth == o.depth
}

type orderedStep struct {
	step testStep
	// dependency represents an encoding of the steps dependencies and is
	// used to validate ordering of steps.
	dependency stepDependency
}

func (s *Stage) OrderedSteps() []orderedStep {
	var steps []orderedStep
	for chainID, ch := range s.chains {
		for depth, group := range ch {
			for _, step := range group {
				newStep := orderedStep{
					step: step,
					dependency: stepDependency{
						chainID: chainID,
						depth:   depth,
					},
				}
				steps = append(steps, newStep)
			}
		}
	}

	return steps
}
