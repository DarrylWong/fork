package modular

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
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
	var stagePlans []stagePlan
	for _, stage := range p.stages {
		plan := p.generateStagePlan(stage)
		p.CreateConcurrentSteps(&plan)
		stagePlans = append(stagePlans, plan)
	}

	return &TestPlan{
		name:       p.name,
		seed:       p.seed,
		stagePlans: stagePlans,
	}, nil
}

// generateStagePlan generates a random legal permutation of steps in a Stage.
func (p *TestPlanner) generateStagePlan(s Stage) stagePlan {
	// Start with a known valid ordering of steps: All the steps in the first chain
	// sequentially, followed by all the steps in the second chain and so on.
	steps := s.Steps()
	numSteps := len(steps)
	if numSteps <= 1 {
		return stagePlan{
			stage: &s,
			steps: steps,
		}
	}

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
	// "Fuzz" the number of iterations to account for trivial cases with no
	// self loops, e.g. two chains of one step each.
	iterations := int(math.Ceil(math.Pow(float64(numSteps), 3)*math.Log(float64(numSteps)))) + p.rng.Intn(2)
	for proposedSwaps := 0; proposedSwaps < iterations; proposedSwaps++ {
		first, second := randomPairIndices()
		// We can swap the two steps if they are in different chains,
		// or if they are in the same stepGroup.
		if isValidSwap(steps[first], steps[second]) {
			steps[first], steps[second] = steps[second], steps[first]
		}
	}

	return stagePlan{
		stage: &s,
		steps: steps,
	}
}

// CreateConcurrentSteps takes in a stagePlan of singleSteps and
// randomly groups steps into concurrentSteps, respecting the
// stage's maxStepConcurrency and the validity of the concurrent group.
//
// It does so with a "locally" uniform distribution, i.e. uniformly distributed
// over all valid permutations for the given linearization of steps, but
// not necessarily uniform over all valid permutations of the DAG.
// The latter is more difficult to achieve as different linearizations
// may have different numbers of valid concurrent groupings, which would
// require computing all possible linearizations in order to weight groupings.
func (p *TestPlanner) CreateConcurrentSteps(stagePlan *stagePlan) {
	maxConc := stagePlan.stage.maxStepConcurrency
	if maxConc <= 1 || len(stagePlan.steps) <= 1 {
		return
	}

	// Use a local RNG so the caller’s RNG state isn’t perturbed by this routine.
	rng := rand.New(rand.NewSource(p.rng.Int63()))
	n := len(stagePlan.steps)

	for {
		spans, valid := randomSpans(rng, n, maxConc)
		if !valid {
			// Generated spans were invalid (i.e. one was too large), try again.
			continue
		}

		if p.isValidConcurrentGroups(stagePlan.steps, spans) {
			stagePlan.steps = p.buildConcurrentSteps(stagePlan.steps, spans)
			return
		}
	}
}

// stepSpan is a convenience type that represents a range [start, end] over stagePlan.steps.
type stepSpan struct{ start, end int }

func (s stepSpan) Size() int { return s.end - s.start + 1 }

// randomSpans randomly splits the plan into spans, eagerly failing if
// a span created violates maxConcurrency.
func randomSpans(rng *rand.Rand, numSteps int, maxConcurrency int) ([]stepSpan, bool) {
	// Given a slice of steps of length numSteps, we can split this up into numSteps-1 spans,
	// where a span will be grouped into a concurrent step. We randomly decide for each
	// of the n-1 boundaries whether to split or not.
	splitPoints := make([]bool, numSteps-1)
	for i := range splitPoints {
		splitPoints[i] = rng.Intn(2) == 1
	}

	//
	spans := make([]stepSpan, 0, numSteps)
	start := 0
	for i := 0; i < numSteps-1; i++ {
		if splitPoints[i] {
			newSpan := stepSpan{start: start, end: i}
			// If we've generated a span that's too large, bail out early.
			if newSpan.Size() > maxConcurrency {
				return nil, false
			}
			spans = append(spans, newSpan)
			start = i + 1
		}
	}
	newSpan := stepSpan{start: start, end: numSteps - 1}
	if newSpan.Size() > maxConcurrency {
		return nil, false
	}
	spans = append(spans, newSpan)
	return spans, true
}

func (p *TestPlanner) isValidConcurrentGroups(steps []testStep, spans []stepSpan) bool {
	// A concurrent grouping of steps [i, j] is valid iff:
	// - no step disables concurrency
	// - if two steps are in the same chain, they must be in the same step group (depth)
	for _, sp := range spans {
		chains := make(map[int]int)
		if sp.Size() == 1 {
			continue
		}
		for idx := sp.start; idx <= sp.end; idx++ {
			step := steps[idx]
			if ss, ok := step.StepProtocol.(*singleStep); ok {
				if ss.concurrencyDisabled {
					return false
				}
			}
			if d, ok := chains[step.order.chainID]; ok && d != step.order.depth {
				return false
			}
			chains[step.order.chainID] = step.order.depth
		}
	}
	return true
}

// buildConcurrentSteps creates the final step list with concurrent groups.
func (p *TestPlanner) buildConcurrentSteps(originalSteps []testStep, spans []stepSpan) []testStep {
	result := make([]testStep, 0, len(spans))

	for _, sp := range spans {
		if sp.start == sp.end {
			result = append(result, originalSteps[sp.start])
		} else {
			groupedSteps := make([]testStep, sp.Size())
			copy(groupedSteps, originalSteps[sp.start:sp.end+1])

			cs := newConcurrentStep(
				fmt.Sprintf("running %d steps concurrently", len(groupedSteps)),
				groupedSteps,
			)
			result = append(result, testStep{StepProtocol: cs})
		}
	}

	return result
}

// sequentialRunStep is a "meta-step" that indicates that a sequence
// of steps are to be executed sequentially. The default test runner
// already runs steps sequentially. This meta-step exists primarily as
// a way to group related steps so that a test plan is easier to
// understand for a human.
type stagePlan struct {
	stage *Stage
	steps []testStep
}

type TestPlan struct {
	name       string
	seed       int64
	stagePlans []stagePlan
}

func (p *TestPlan) Steps() []testStep {
	var allSteps []testStep
	for _, plan := range p.stagePlans {
		allSteps = append(allSteps, plan.steps...)
	}
	return allSteps
}

const (
	branchString       = "├──"
	nestedBranchString = "│   "
	lastBranchString   = "└──"
	lastBranchPadding  = "   "
)

func (p *TestPlan) String() string {
	var out strings.Builder

	// Print each stage with its steps indented underneath
	for i, stagePlan := range p.stagePlans {
		stagePrefix := treeBranchString(i, len(p.stagePlans))
		stageName := stagePlan.stage.name
		if stageName == "" {
			stageName = fmt.Sprintf("stage %d", i+1)
		}
		out.WriteString(fmt.Sprintf("%s %s\n", stagePrefix, stageName))

		// Print steps for this stage, indented under the stage
		for j, step := range stagePlan.steps {
			// Create proper nested prefix that maintains tree connections
			nestedPrefix := strings.ReplaceAll(stagePrefix, branchString, nestedBranchString)
			nestedPrefix = strings.ReplaceAll(nestedPrefix, lastBranchString, lastBranchPadding)
			stepPrefix := fmt.Sprintf("%s%s", nestedPrefix, treeBranchString(j, len(stagePlan.steps)))
			p.prettyPrintStep(&out, step, stepPrefix)
		}
	}

	var lines []string
	addLine := func(title string, val any) {
		titleWithColon := fmt.Sprintf("%s:", title)
		lines = append(lines, fmt.Sprintf("%-20s%v", titleWithColon, val))
	}

	addLine("Test Plan", p.name)
	addLine("Seed", p.seed)
	addLine("Stages", len(p.stagePlans))

	return fmt.Sprintf(
		"%s\nPlan:\n%s",
		strings.Join(lines, "\n"), out.String(),
	)
}

func (p *TestPlan) prettyPrintStep(out *strings.Builder, step testStep, prefix string) {
	writeNested := func(label string, steps []testStep) {
		out.WriteString(fmt.Sprintf("%s %s\n", prefix, label))
		for i, subStep := range steps {
			nestedPrefix := strings.ReplaceAll(prefix, branchString, nestedBranchString)
			nestedPrefix = strings.ReplaceAll(nestedPrefix, lastBranchString, lastBranchPadding)
			subPrefix := fmt.Sprintf("%s%s", nestedPrefix, treeBranchString(i, len(steps)))
			p.prettyPrintStep(out, subStep, subPrefix)
		}
	}

	writeSingle := func(description string) {
		out.WriteString(fmt.Sprintf("%s %s\n", prefix, description))
	}

	// Handle different step types
	if concurrentStep, ok := step.StepProtocol.(*concurrentStep); ok {
		writeNested(concurrentStep.Description(), concurrentStep.steps)
	} else {
		writeSingle(step.Description())
	}
}

func treeBranchString(idx, sliceLen int) string {
	if idx == sliceLen-1 {
		return lastBranchString
	}
	return branchString
}
