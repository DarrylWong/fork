package modular

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
)

// TestDependencyOrdering is a property based test that constructs stages
// with randomized chains. Each step is named as {A-Z}:{A-Z}:{A-Z} where the
// first letter represents the chain, all steps in a chain should have the same first
// letter. The second letter represents the depth of the step in the chain, all steps
// with a higher letter are dependent on the steps with smaller letters. Finally,
// the third letter represents steps that are part of the same step group, these are
// steps that can run concurrently with each other.
//
// From this, we can generate random plans, then assert that our plan never attempts to run
// steps out of order. e.g. It should never run AB before/concurrently AA, but BA before AB is
// allowed.
func TestDependencyOrdering(t *testing.T) {
	rng, _ := randutil.NewPseudoRand()
	// Run property-based test with multiple iterations
	for iteration := 0; iteration < 2; iteration++ {
		// Generate a random test with multiple stages and chains
		mod := NewTest(fmt.Sprintf("property_test_%d", iteration), rng.Int63())

		// Generate 1-3 stages
		numStages := 1 + rng.Intn(3)
		stages := make([]*Stage, numStages)

		for i := 0; i < numStages; i++ {
			stageName := fmt.Sprintf("stage_%d", i)
			stage := mod.NewStage(stageName, DisableFailureInjection())
			stages[i] = stage

			// Generate 1-5 chains per stage
			numChains := 1 + rng.Intn(5)

			for chainIdx := 0; chainIdx < numChains; chainIdx++ {
				chainLetter := string(rune('A' + chainIdx))

				// Generate 2-4 step groups per chain
				numStepGroups := 2 + rng.Intn(3)

				var builder *StepBuilder

				for stepGroupIdx := 0; stepGroupIdx < numStepGroups; stepGroupIdx++ {
					// Generate 1-3 steps per step group.
					numStepsInGroup := 1 + rng.Intn(3)

					for stepIdx := 0; stepIdx < numStepsInGroup; stepIdx++ {
						stepName := fmt.Sprintf("%s:%d:%d", chainLetter, stepGroupIdx, stepIdx)

						noopFunc := func(ctx context.Context, l *logger.Logger, h *Helper) error {
							return nil
						}

						if builder == nil {
							// First step in the chain
							builder = mod.InStage(stage, stepName, noopFunc)
						} else if stepIdx == 0 {
							// First step in a new step group (sequential)
							builder = builder.Then(stepName, noopFunc)
						} else {
							// Additional step in the same step group (concurrent)
							builder = builder.And(stepName, noopFunc)
						}
					}
				}
			}
		}

		// Generate a plan using the planner
		planner := NewSimplePlanner(mod, PlannerConfig{
			IsLocal:           true,
			ConcurrencyChance: 0.5, // 50% chance for more randomization
		})

		plan, err := planner.Plan()
		if err != nil {
			t.Fatalf("Failed to generate plan: %v", err)
		}
		t.Log(plan.String())

		// Validate dependency ordering for each stage
		for stageIdx, stageExecPlan := range plan.stageExecutionPlans {
			if stageExecPlan == nil {
				continue // Skip setup/after-test stages
			}

			validateStageOrdering(t, stages[stageIdx], stageExecPlan)
		}
	}
}

// validateStageOrdering checks that the execution plan respects dependency constraints
func validateStageOrdering(t *testing.T, stage *Stage, plan *StageExecutionPlan) {
	// First, collect all steps from the stage to ensure every step appears exactly once
	expectedSteps := make(map[string]bool)
	for _, step := range stage.Steps() {
		expectedSteps[step.Description()] = false // false = not yet seen in plan
	}

	// Keep track of the highest depth seen for each chain
	maxDepthPerChain := make(map[string]string)

	// Walk through all execution steps in order
	for _, execStep := range plan.ExecutionSteps {
		stepName := execStep.Step.Description()

		// Mark this step as seen
		if seen, exists := expectedSteps[stepName]; exists {
			if seen {
				t.Errorf("Step %s appears multiple times in execution plan", stepName)
			}
			expectedSteps[stepName] = true
		} else {
			t.Errorf("Step %s is unexpected", stepName)
		}

		// Parse step name to extract chain and depth
		parts := strings.Split(stepName, ":")
		if len(parts) != 3 {
			t.Errorf("Invalid step name")
		}

		chainID := parts[0]
		depth := parts[1]

		// Check if we've seen a higher depth for this chain already
		if maxDepth, exists := maxDepthPerChain[chainID]; exists {
			if depth < maxDepth {
				t.Errorf("Dependency violation: step %s (depth %s) appears after higher depth %s in chain %s",
					stepName, depth, maxDepth, chainID)
			}
		}

		// Update the maximum depth seen for this chain
		maxDepthPerChain[chainID] = depth
	}

	// Ensure all expected steps were seen exactly once
	for stepName, seen := range expectedSteps {
		if !seen {
			t.Errorf("Step %s from stage is missing from execution plan", stepName)
		}
	}
}
