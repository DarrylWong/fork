package modular

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
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
	ConcurrencyChance float64 // Probability of choosing concurrent execution (default 0.25)
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
	plan := p.test.GeneratePlan()
	plan.isLocal = p.config.IsLocal

	// Generate randomized execution plans for each stage
	plan.stageExecutionPlans = p.generateStageExecutionPlans(plan.stages)

	return plan, nil
}

// generateStageExecutionPlans creates randomized execution plans for stages.
func (p *SimplePlanner) generateStageExecutionPlans(stages []*Stage) []*StageExecutionPlan {
	plans := make([]*StageExecutionPlan, len(stages))
	concurrencyChance := p.config.ConcurrencyChance
	if concurrencyChance == 0 {
		concurrencyChance = 0.25 // Default 25% chance
	}

	globalStepID := 1

	for i, stage := range stages {
		// Skip execution plans for setup and after-test stages
		if stage.name == "setup" || stage.name == "after-test" {
			plans[i] = nil // No execution plan for special stages
			// Still advance globalStepID for these stages
			globalStepID += len(stage.Steps())
			continue
		}

		plan := &StageExecutionPlan{
			Stage: stage,
		}

		// Extract all individual steps from step chains and single steps
		plan.ExecutionSteps, globalStepID = p.extractExecutionSteps(stage, globalStepID)

		// Skip stages with no steps or stages with sequential-only steps
		if len(plan.ExecutionSteps) == 0 || p.hasSequentialOnlyExecutionSteps(plan.ExecutionSteps) {
			plan.Strategy = InterleavedExecution
			plan.ConcurrentGroups = p.generateSequentialGroups(plan.ExecutionSteps)
		} else {
			// Randomly choose between concurrent and interleaved execution
			if p.rng.Float64() < concurrencyChance {
				plan.Strategy = ConcurrentExecution
				plan.ConcurrentGroups = p.generateConcurrentGroups(plan.ExecutionSteps)
			} else {
				plan.Strategy = InterleavedExecution
				plan.ConcurrentGroups = p.generateInterleavedGroups(plan.ExecutionSteps, concurrencyChance)
			}
		}

		// Validate that dependencies are satisfied
		if err := p.validateDependencies(plan); err != nil {
			return nil // For now, return empty plans on validation error
		}

		plans[i] = plan
	}

	return plans
}

// extractExecutionSteps extracts all individual steps from stage chains with stepGroups.
func (p *SimplePlanner) extractExecutionSteps(stage *Stage, startID int) ([]*ExecutionStep, int) {
	var executionSteps []*ExecutionStep
	currentID := startID

	// Work with the new stepGroup-based chain structure
	for chainIndex, stageChain := range stage.Chains() {
		var prevGroupStepIDs []int // IDs of all steps in the previous stepGroup

		for stepGroupIndex, stepGroup := range stageChain {
			var currentGroupStepIDs []int // IDs of all steps in the current stepGroup

			if len(stepGroup) == 1 {
				// Single step in this stepGroup
				execStep := &ExecutionStep{
					ID:         currentID,
					Step:       stepGroup[0],
					ChainID:    chainIndex,
					ChainIndex: stepGroupIndex,
				}

				// Depend on all steps from the previous stepGroup
				execStep.CanRunAfter = prevGroupStepIDs

				executionSteps = append(executionSteps, execStep)
				currentGroupStepIDs = append(currentGroupStepIDs, currentID)
				currentID++
			} else {
				// Multiple steps in this stepGroup - they can run in parallel
				for _, step := range stepGroup {
					execStep := &ExecutionStep{
						ID:         currentID,
						Step:       step,
						ChainID:    chainIndex,
						ChainIndex: stepGroupIndex,
					}

					// All steps in this group depend on all steps from the previous stepGroup
					execStep.CanRunAfter = prevGroupStepIDs

					executionSteps = append(executionSteps, execStep)
					currentGroupStepIDs = append(currentGroupStepIDs, currentID)
					currentID++
				}
			}

			// Update prevGroupStepIDs for the next iteration
			prevGroupStepIDs = currentGroupStepIDs
		}
	}

	return executionSteps, currentID
}

// hasSequentialOnlyExecutionSteps checks if any execution step requires sequential execution.
func (p *SimplePlanner) hasSequentialOnlyExecutionSteps(steps []*ExecutionStep) bool {
	for _, step := range steps {
		if step.Step.ConcurrencyDisabled() {
			return true
		}
	}
	return false
}

// generateSequentialGroups creates groups where each step runs one after another.
func (p *SimplePlanner) generateSequentialGroups(steps []*ExecutionStep) [][]int {
	if len(steps) == 0 {
		return nil
	}

	groups := make([][]int, len(steps))
	for i, step := range steps {
		groups[i] = []int{step.ID}
	}
	return groups
}

// generateConcurrentGroups creates one group with all steps that can run concurrently.
func (p *SimplePlanner) generateConcurrentGroups(steps []*ExecutionStep) [][]int {
	if len(steps) == 0 {
		return nil
	}

	var concurrentSteps []int
	var sequentialGroups [][]int

	// Separate steps that must run sequentially from those that can run concurrently
	processed := make(map[int]bool)

	for _, step := range steps {
		if processed[step.ID] {
			continue
		}

		if len(step.CanRunAfter) > 0 || step.Step.ConcurrencyDisabled() {
			// This step has dependencies or requires sequential execution
			sequentialGroups = append(sequentialGroups, []int{step.ID})
		} else {
			// This step can run concurrently
			concurrentSteps = append(concurrentSteps, step.ID)
		}
		processed[step.ID] = true
	}

	var result [][]int
	if len(concurrentSteps) > 0 {
		result = append(result, concurrentSteps)
	}
	result = append(result, sequentialGroups...)

	return result
}

// generateInterleavedGroups creates a random interleaving with some concurrent groups.
func (p *SimplePlanner) generateInterleavedGroups(steps []*ExecutionStep, concurrencyChance float64) [][]int {
	if len(steps) == 0 {
		return nil
	}

	// Use dependency-aware scheduling to create an interleaved execution plan
	return p.createDependencyAwareSchedule(steps, concurrencyChance)
}

// createDependencyAwareSchedule creates an interleaved schedule that respects dependencies.
func (p *SimplePlanner) createDependencyAwareSchedule(steps []*ExecutionStep, concurrencyChance float64) [][]int {
	// Create a dependency graph
	stepMap := make(map[int]*ExecutionStep)
	for _, step := range steps {
		stepMap[step.ID] = step
	}

	// Track which steps are ready to run (no pending dependencies)
	readySteps := make([]*ExecutionStep, 0)
	blockedSteps := make([]*ExecutionStep, 0)
	completedSteps := make(map[int]bool)

	// Initialize ready steps (those with no dependencies)
	for _, step := range steps {
		if len(step.CanRunAfter) == 0 {
			readySteps = append(readySteps, step)
		} else {
			blockedSteps = append(blockedSteps, step)
		}
	}

	var schedule [][]int

	// Continue until all steps are scheduled
	for len(completedSteps) < len(steps) {
		if len(readySteps) == 0 {
			// This shouldn't happen if dependencies are valid
			break
		}

		// Decide whether to run steps concurrently or sequentially
		if len(readySteps) > 1 && p.rng.Float64() < concurrencyChance {
			// Create a concurrent group with 2-min(3, len(readySteps)) ready steps
			maxGroupSize := min(3, len(readySteps))
			groupSize := 2
			if maxGroupSize > 2 {
				groupSize = 2 + p.rng.Intn(maxGroupSize-1)
			}

			concurrentGroup := make([]int, 0, groupSize)

			// Randomly select steps for the concurrent group
			indices := p.rng.Perm(len(readySteps))
			var selectedSteps []*ExecutionStep
			for i := 0; i < groupSize && i < len(indices); i++ {
				step := readySteps[indices[i]]
				concurrentGroup = append(concurrentGroup, step.ID)
				completedSteps[step.ID] = true
				selectedSteps = append(selectedSteps, step)
			}

			// Remove selected steps from ready steps
			var newReadySteps []*ExecutionStep
			selectedSet := make(map[int]bool)
			for _, step := range selectedSteps {
				selectedSet[step.ID] = true
			}

			for _, step := range readySteps {
				if !selectedSet[step.ID] {
					newReadySteps = append(newReadySteps, step)
				}
			}
			readySteps = newReadySteps

			schedule = append(schedule, concurrentGroup)
		} else {
			// Run one step sequentially
			// Randomly select a ready step
			idx := p.rng.Intn(len(readySteps))
			step := readySteps[idx]

			schedule = append(schedule, []int{step.ID})
			completedSteps[step.ID] = true

			// Remove from ready steps
			readySteps = append(readySteps[:idx], readySteps[idx+1:]...)
		}

		// Check if any blocked steps are now ready
		newReadySteps := make([]*ExecutionStep, 0)
		remainingBlockedSteps := make([]*ExecutionStep, 0)

		for _, blockedStep := range blockedSteps {
			allDepsCompleted := true
			for _, depID := range blockedStep.CanRunAfter {
				if !completedSteps[depID] {
					allDepsCompleted = false
					break
				}
			}

			if allDepsCompleted {
				newReadySteps = append(newReadySteps, blockedStep)
			} else {
				remainingBlockedSteps = append(remainingBlockedSteps, blockedStep)
			}
		}

		readySteps = append(readySteps, newReadySteps...)
		blockedSteps = remainingBlockedSteps
	}

	return schedule
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// createConcurrentGroupsFromSequential takes a sequential plan and creates some concurrent groups
func (p *SimplePlanner) createConcurrentGroupsFromSequential(groups [][]int, steps []*ExecutionStep, concurrencyChance float64) [][]int {
	// Create step lookup map
	stepMap := make(map[int]*ExecutionStep)
	for _, step := range steps {
		stepMap[step.ID] = step
	}

	var result [][]int
	i := 0

	for i < len(groups) {
		currentGroup := groups[i]

		if len(currentGroup) == 1 {
			stepID := currentGroup[0]
			step := stepMap[stepID]

			// If this is an independent step, check if we should make it concurrent with the next ones
			if step.ChainID == -1 && len(step.CanRunAfter) == 0 && p.rng.Float64() < concurrencyChance {
				// Try to group with next independent steps
				concurrentGroup := []int{stepID}
				j := i + 1

				for j < len(groups) && len(groups[j]) == 1 {
					nextStepID := groups[j][0]
					nextStep := stepMap[nextStepID]

					// Only add if it's also independent and we haven't exceeded a reasonable group size
					if nextStep.ChainID == -1 && len(nextStep.CanRunAfter) == 0 && len(concurrentGroup) < 3 {
						concurrentGroup = append(concurrentGroup, nextStepID)
						j++
					} else {
						break
					}
				}

				if len(concurrentGroup) > 1 {
					result = append(result, concurrentGroup)
					i = j // Skip the steps we've grouped
					continue
				}
			}
		}

		// Add the group as-is
		result = append(result, currentGroup)
		i++
	}

	return result
}

// validateDependencies ensures that all dependency constraints are satisfied in the execution plan.
func (p *SimplePlanner) validateDependencies(plan *StageExecutionPlan) error {
	// Create a map of step ID to its position in the execution order
	stepPositions := make(map[int]int)
	position := 0

	for _, group := range plan.ConcurrentGroups {
		for _, stepID := range group {
			stepPositions[stepID] = position
		}
		position++
	}

	// Check each step's dependencies
	for _, execStep := range plan.ExecutionSteps {
		for _, depID := range execStep.CanRunAfter {
			if depPos, exists := stepPositions[depID]; exists {
				if stepPos, exists := stepPositions[execStep.ID]; exists {
					if stepPos <= depPos {
						return fmt.Errorf("dependency violation: step %d must run after step %d, but they are scheduled at positions %d and %d respectively",
							execStep.ID, depID, stepPos, depPos)
					}
				}
			}
		}
	}

	return nil
}

// Execute runs a modular test with the given cluster.
func Execute(ctx context.Context, t test.Test, c cluster.Cluster, testDef *Test) error {
	planner := NewSimplePlanner(testDef, PlannerConfig{
		IsLocal:           false, // TODO: detect from test environment
		ConcurrencyChance: 0.25,  // 25% chance of concurrent execution
	})

	plan, err := planner.Plan()
	if err != nil {
		return fmt.Errorf("failed to generate test plan: %w", err)
	}

	runner := NewTestRunner(plan, t)
	return runner.Run(ctx)
}

// TestPlan represents the execution plan for a modular test.
type TestPlan struct {
	name                string
	seed                int64
	clusterSpecs        []ClusterSpec
	workloadSpecs       []WorkloadSpec
	stages              []*Stage // All stages including setup and after-test
	isLocal             bool
	stageExecutionPlans []*StageExecutionPlan // Randomized execution plans for stages
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

	// All stages (setup, user stages, after-test) with unified formatting
	tp.formatStagesWithTree(&b)

	return b.String()
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

// GeneratePlan creates a TestPlan from the test definition.
func (t *Test) GeneratePlan() *TestPlan {
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

	return &TestPlan{
		name:          t.name,
		seed:          t.seed,
		clusterSpecs:  t.clusterSpecs,
		workloadSpecs: t.workloadSpecs,
		stages:        allStages,
		isLocal:       t.isLocal,
	}
}

// TestPlan accessor methods for testing
func (tp *TestPlan) Name() string {
	return tp.name
}

func (tp *TestPlan) Seed() int64 {
	return tp.seed
}

func (tp *TestPlan) ClusterSpecs() []ClusterSpec {
	return tp.clusterSpecs
}

func (tp *TestPlan) WorkloadSpecs() []WorkloadSpec {
	return tp.workloadSpecs
}

func (tp *TestPlan) Setup() []testStep {
	// Find setup stage and return its steps
	for _, stage := range tp.stages {
		if stage.name == "setup" {
			return stage.Steps()
		}
	}
	return nil
}

func (tp *TestPlan) Stages() []*Stage {
	return tp.stages
}

func (tp *TestPlan) AfterTest() []testStep {
	// Find after-test stage and return its steps
	for _, stage := range tp.stages {
		if stage.name == "after-test" {
			return stage.Steps()
		}
	}
	return nil
}

func (tp *TestPlan) IsLocal() bool {
	return tp.isLocal
}
