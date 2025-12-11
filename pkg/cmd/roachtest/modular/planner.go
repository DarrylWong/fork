package modular

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// Runner is an interface for executing test plans from the modular framework.
type Runner interface {
	Run(ctx context.Context, t test.Test) error
}

type TestPlanner struct {
	seed   int64
	rng    *rand.Rand
	stages []Stage

	// Test execution context
	ctx       context.Context
	logger    *logger.Logger
	cluster   cluster.Cluster
	crdbNodes option.NodeListOption

	debugModules debugModules

	// cleanupOnFailure controls whether cluster state cleanup is performed on failure.
	// Default false (no cleanup) since cleanup is primarily for testing purposes.
	cleanupOnFailure bool
}

// DAG generates a directed acyclic graph representation of all test steps and their dependencies.
func (p *TestPlanner) DAG() string {
	return GenerateDAG(p.stages)
}

func (p *TestPlanner) Plan() (*TestPlan, error) {
	// First, analyze all stages and merge chains with conflicting resource
	// accesses.
	// Note: PrePlan is NOT called here because it requires a running database.
	// Instead, runners call PrePlan during execution when database is available.
	mergedStages := make([]Stage, len(p.stages))
	for i, stage := range p.stages {
		mergedStage, err := p.maybeMergeChains(stage)
		if err != nil {
			return nil, err
		}
		mergedStages[i] = mergedStage
	}

	// Now generate plans from resolved stages
	var stagePlans []stagePlan
	for _, stage := range mergedStages {
		plan := p.generateStagePlan(stage)
		p.CreateConcurrentSteps(&plan)
		stagePlans = append(stagePlans, plan)
	}
	p.assignStepIDs(stagePlans)

	return &TestPlan{
		seed:             p.seed,
		rng:              p.rng,
		stagePlans:       stagePlans,
		ctx:              p.ctx,
		logger:           p.logger,
		cluster:          p.cluster,
		crdbNodes:        p.crdbNodes,
		debugModules:     p.debugModules,
		cleanupOnFailure: p.cleanupOnFailure,
	}, nil
}

// generateStagePlan generates a random legal permutation of steps in a Stage.
func (p *TestPlanner) generateStagePlan(s Stage) stagePlan {
	// Start with a known valid ordering of steps: All the steps in the first Chain
	// sequentially, followed by all the steps in the second Chain and so on.
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
		if i.position.chainID != j.position.chainID {
			return true
		}
		// We can swap the two steps if they are part of the same
		// step group.
		return i.position.depth == j.position.depth
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

// hasConcurrentConflict checks if two steps conflict when run concurrently.
// Steps conflict if they access the same resource with at least one having exclusive access (lock).
func hasConcurrentConflict(step1, step2 *SingleStep) bool {
	// Collect all resource accesses from both steps
	allResources1 := make([]ResourceAccess, 0, len(step1.resources.accesses)+len(step1.resources.releases))
	allResources1 = append(allResources1, step1.resources.accesses...)
	allResources1 = append(allResources1, step1.resources.releases...)

	allResources2 := make([]ResourceAccess, 0, len(step2.resources.accesses)+len(step2.resources.releases))
	allResources2 = append(allResources2, step2.resources.accesses...)
	allResources2 = append(allResources2, step2.resources.releases...)

	// Check if any resources conflict
	for _, res1 := range allResources1 {
		for _, res2 := range allResources2 {
			if res1.ConflictsWith(&res2) {
				return true
			}
		}
	}

	return false
}

// findConflictingChains uses union-find to group chains that have conflicting resources.
// If two chains have conflicting resources (i.e. a resource is locked by one chain
// and accessed or locked by another), they cannot be run in parallel and must be merged.
//
// Returns a slice of groups, where each group is a slice of chain indices.
// Groups are sorted by their minimum chain index for deterministic ordering.
//
// Example with 4 chains:
//
//	Chain 0: locks cluster_setting:A
//	Chain 1: locks schema_change:B
//	Chain 2: locks cluster_setting:A (conflicts with 0!)
//	Chain 3: no locks
//
// Union-find process:
//
//	Initial: parent = [0, 1, 2, 3] (each chain is its own group)
//	Compare 0 vs 1: no conflict, parent = [0, 1, 2, 3]
//	Compare 0 vs 2: CONFLICT! union(0,2), parent = [0, 1, 0, 3] (2 now points to 0)
//	Compare 0 vs 3: no conflict, parent = [0, 1, 0, 3]
//	Compare 1 vs 2: find(2)=0, so comparing 1 vs group{0,2}, no conflict
//	Compare 1 vs 3: no conflict
//	Compare 2 vs 3: already in group with 0, no new conflict
//
// Final groups:
//
//	[[0, 2], [1], [3]] - sorted by first element
//
// Transitive conflicts also work:
//
//	Chain 0: locks A
//	Chain 1: locks B
//	Chain 2: locks A and B (conflicts with both!)
//	Result: union(0,2), union(1,2) → all three chains merge into one group
//	because union(1,2) finds that 2 is already with 0, so it unions 1 with 0's group.
func findConflictingChains(chainResources [][]ResourceAccess) [][]int {
	n := len(chainResources)

	// Initialize: each chain starts in its own group
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i // parent[i] = i means "i is its own root"
	}

	// find(x) returns the root of x's group (with path compression)
	// Path compression: flattens the tree so future lookups are faster
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x]) // Recursively find root and compress path
		}
		return parent[x]
	}

	// union(x, y) merges the groups containing x and y
	union := func(x, y int) {
		px, py := find(x), find(y)
		if px != py {
			parent[px] = py // Make py the parent of px's entire group
		}
	}

	// Find all conflicting pairs (chains that conflict on any resource)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			// Check if chains i and j have conflicting resources
			conflict := false
			for _, res1 := range chainResources[i] {
				for _, res2 := range chainResources[j] {
					if res1.ConflictsWith(&res2) {
						conflict = true
						break
					}
				}
				if conflict {
					break
				}
			}
			if conflict {
				union(i, j) // Merge i and j into the same group
			}
		}
	}

	// Group chains by their root parent
	groupMap := make(map[int][]int)
	for i := 0; i < n; i++ {
		root := find(i)
		groupMap[root] = append(groupMap[root], i)
	}

	// Convert to slice and sort by minimum chain index for determinism
	var groups [][]int
	for _, group := range groupMap {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i][0] < groups[j][0]
	})

	return groups
}

// maybeMergeChains analyzes chains for lock conflicts and merges
// conflicting chains.
func (p *TestPlanner) maybeMergeChains(stage Stage) (Stage, error) {
	if len(stage.chains) <= 1 {
		return stage, nil
	}

	// Collect resource accesses for each chain.
	chainResources := make([][]ResourceAccess, len(stage.chains))
	for i, ch := range stage.chains {
		var resources []ResourceAccess
		for _, stepGroup := range ch {
			for _, step := range stepGroup {
				// Try to get resources from any ResourceAware step (SingleStep or DynamicStep)
				if resourceAware, ok := step.StepProtocol.(ResourceAware); ok {
					resources = append(resources, resourceAware.GetResourceAccesses()...)
					resources = append(resources, resourceAware.GetResourceReleases()...)
				}
			}
		}
		chainResources[i] = resources
	}

	// Find which chains conflict and group them.
	groups := findConflictingChains(chainResources)

	p.logger.Printf("Stage '%s': Found %d conflict groups from %d chains", stage.name, len(groups), len(stage.chains))
	for i, group := range groups {
		p.logger.Printf("  Group %d: %d chains to merge", i, len(group))
	}

	// Merge each group using smart interleaving
	newChains := make([]chain, 0, len(groups))
	for _, group := range groups {
		if len(group) == 1 {
			// No conflict, keep as-is
			newChains = append(newChains, stage.chains[group[0]])
		} else {
			// Collect all chains to merge
			chainsToMerge := make([]chain, len(group))
			for i, idx := range group {
				chainsToMerge[i] = stage.chains[idx]
			}

			merged, err := p.mergeChains(chainsToMerge)
			if err != nil {
				return Stage{}, err
			}
			newChains = append(newChains, merged)
		}
	}

	return Stage{
		name:                     stage.name,
		chains:                   newChains,
		maxStepConcurrency:       stage.maxStepConcurrency,
		failureInjectionDisabled: stage.failureInjectionDisabled,
	}, nil
}

// mergeChains merges multiple chains by doing pairwise merges.
// Each merge takes one chain as a base and inserts stepGroups from the second
// chain at random positions, using rejection sampling to ensure valid lock sequences.
func (p *TestPlanner) mergeChains(chains []chain) (chain, error) {
	result := chains[0]
	for i := 1; i < len(chains); i++ {
		var err error
		result, err = p.mergeTwoChains(result, chains[i])
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// mergeTwoChains merges two chains while respecting locks and step ordering.
func (p *TestPlanner) mergeTwoChains(c1, c2 chain) (chain, error) {
	maxAttempts := 1000

	p.logger.Printf("Merging two chains: c1 has %d stepGroups, c2 has %d stepGroups", len(c1), len(c2))

	// TODO (darryl): we could record plans and early reject if taken already
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt%100 == 0 && attempt > 0 {
			p.logger.Printf("  Merge attempt %d/%d", attempt, maxAttempts)
		}
		// We want to interleave our two chains with a uniform distribution.
		// A naive approach is to randomly pick with 50% probability the next
		// stepGroup from either chain. However, this skews the distribution
		// if the chains are of different lengths, e.g. consider if chain 1
		// is length 10 and chain 2 is length 1, chain 2 will be skewed toward
		// the start of the merged chain.
		//
		// Randomly shuffling both chains together would give us a uniform
		// distribution, but the rejection sampling would be inefficient
		// as we violate the ordering dependencies within each chain.
		//
		// Instead, we do something in between. We create a slice with an
		// entry for each stepGroup in both chains, marking which chain the
		// entry belongs to. This slice is then shuffled and drawn from, i.e.
		// if we pull a 0, we take the next stepGroup from chain 1, if we pull a 1,
		// we take the next stepGroup from chain 2.
		order := make([]int, 0, len(c1)+len(c2))
		for range c1 {
			order = append(order, 0)
		}
		for range c2 {
			order = append(order, 1)
		}

		// Shuffle the order to get a random interleaving
		p.rng.Shuffle(len(order), func(i, j int) {
			order[i], order[j] = order[j], order[i]
		})

		// Pull stepGroups from each chain according to the order
		result := make(chain, 0, len(order))
		c1Ptr, c2Ptr := 0, 0

		for _, chainIdx := range order {
			if chainIdx == 0 {
				result = append(result, c1[c1Ptr])
				c1Ptr++
			} else {
				result = append(result, c2[c2Ptr])
				c2Ptr++
			}
		}

		// Validate the lock sequence
		if p.hasValidLockSequenceForChain(result) {
			return result, nil
		}
	}

	return nil, fmt.Errorf("failed to find valid interleaving after %d attempts when merging chains with %d and %d stepGroups",
		maxAttempts, len(c1), len(c2))
}

// hasValidLockSequenceForChain validates a chain by flattening it to steps
// and checking the lock sequence.
func (p *TestPlanner) hasValidLockSequenceForChain(ch chain) bool {
	var steps []testStep
	for _, stepGroup := range ch {
		steps = append(steps, stepGroup...)
	}
	return p.hasValidLockSequence(steps)
}

// hasValidLockSequence validates that a sequence of steps doesn't have
// double-locks or double-unlocks, and that accesses don't conflict with held locks.
func (p *TestPlanner) hasValidLockSequence(steps []testStep) bool {
	// Track currently held resources (both exclusive locks and non-exclusive accesses)
	heldResources := make(map[string]ResourceAccess)

	for _, step := range steps {
		singleStep, ok := step.StepProtocol.(*SingleStep)
		if !ok {
			continue
		}

		// Process accesses: check if they're exclusive locks or non-exclusive accesses
		for _, access := range singleStep.resources.accesses {
			if access.Lock {
				// This is an exclusive lock acquisition
				// Check if we're trying to acquire a lock that conflicts with any held resource
				for _, heldResource := range heldResources {
					if access.ConflictsWith(&heldResource) {
						// Double-lock or overlapping lock detected
						return false
					}
				}
				// Add to held resources
				heldResources[access.String()] = access
			} else {
				// This is a non-exclusive access
				// Check it doesn't conflict with held resources (specifically exclusive locks)
				for _, heldResource := range heldResources {
					if access.ConflictsWith(&heldResource) {
						// Trying to access a resource that's exclusively locked
						return false
					}
				}
				// Add to held resources (non-exclusive accesses also need to be tracked)
				heldResources[access.String()] = access
			}
		}

		// Process releases
		for _, release := range singleStep.resources.releases {
			// Verify we're releasing a resource we actually hold
			key := release.String()
			if heldResource, ok := heldResources[key]; !ok {
				// Trying to release a resource we don't hold
				return false
			} else if release.Lock != heldResource.Lock {
				// The Lock type we're releasing doesn't match what we acquired
				return false
			}
			delete(heldResources, key)
		}
	}

	return true
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
// require computing all possible linearizations in position to weight groupings.
func (p *TestPlanner) CreateConcurrentSteps(stagePlan *stagePlan) {
	maxConc := stagePlan.stage.maxStepConcurrency
	if maxConc <= 1 || len(stagePlan.steps) <= 1 {
		return
	}

	for {
		spans, valid := randomSpans(p.rng, len(stagePlan.steps), maxConc)
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
	// - if two steps are in the same Chain, they must be in the same step group (depth)
	// - steps don't lock the same resources (no double locking)
	for _, sp := range spans {
		chains := make(map[int]int)
		if sp.Size() == 1 {
			continue
		}

		// Collect resource accesses from all steps in this concurrent group
		groupSteps := make([]*SingleStep, 0, sp.Size())

		for idx := sp.start; idx <= sp.end; idx++ {
			step := steps[idx]
			if ss, ok := step.StepProtocol.(*SingleStep); ok {
				if ss.concurrencyDisabled {
					return false
				}
				groupSteps = append(groupSteps, ss)
			}
			if d, ok := chains[step.position.chainID]; ok && d != step.position.depth {
				return false
			}
			chains[step.position.chainID] = step.position.depth
		}

		// Check for lock conflicts within the concurrent group
		for i := 0; i < len(groupSteps); i++ {
			for j := i + 1; j < len(groupSteps); j++ {
				if hasConcurrentConflict(groupSteps[i], groupSteps[j]) {
					return false
				}
			}
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

			// Create a meaningful label from the step descriptions
			stepNames := make([]string, len(groupedSteps))
			for i, step := range groupedSteps {
				stepNames[i] = step.Description()
			}
			label := strings.Join(stepNames, " + ")

			cs := newConcurrentStep(label, groupedSteps)
			result = append(result, testStep{StepProtocol: cs})
		}
	}

	return result
}

func (p *TestPlanner) assignStepIDs(stagePlans []stagePlan) {
	stepID := 1
	for _, sp := range stagePlans {
		for stepIdx := range sp.steps {
			step := &sp.steps[stepIdx]
			if _, ok := step.StepProtocol.(*concurrentStep); ok {
				for i := range step.StepProtocol.(*concurrentStep).steps {
					step.StepProtocol.(*concurrentStep).steps[i].stepID = stepID
					stepID++
				}
			} else {
				sp.steps[stepIdx].stepID = stepID
				stepID++
			}
		}
	}
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
	seed       int64
	rng        *rand.Rand
	stagePlans []stagePlan

	// Test execution context
	ctx       context.Context
	logger    *logger.Logger
	cluster   cluster.Cluster
	crdbNodes option.NodeListOption

	debugModules debugModules

	// cleanupOnFailure controls whether cluster state cleanup is performed on failure.
	// Default false (no cleanup) since cleanup is primarily for testing purposes.
	cleanupOnFailure bool
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

	addLine("Seed", p.seed)

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

	writeSingle := func(description string, id int) {
		out.WriteString(fmt.Sprintf("%s %s (%d)\n", prefix, description, id))
	}

	// Handle different step types
	if concurrentStep, ok := step.StepProtocol.(*concurrentStep); ok {
		writeNested(concurrentStep.Description(), concurrentStep.steps)
	} else {
		writeSingle(step.Description(), step.stepID)
	}
}

func treeBranchString(idx, sliceLen int) string {
	if idx == sliceLen-1 {
		return lastBranchString
	}
	return branchString
}

func (p *TestPlan) newRNGFromRNG() *rand.Rand {
	return rand.New(rand.NewSource(p.rng.Int63()))
}
