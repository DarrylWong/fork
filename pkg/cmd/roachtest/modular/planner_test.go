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
	for iteration := 0; iteration < 10000; iteration++ {
		// Generate a random test with multiple stages and chains
		mod := NewTest(fmt.Sprintf("property_test_%d", iteration), rng.Int63())

		stageName := fmt.Sprintf("stage")
		stage := mod.NewStage(stageName, DisableFailureInjection())

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

		// Validate dependency ordering for the flat step list
		validateStepOrdering(t, plan.Steps())
	}
}

// validateStepOrdering checks that the flat step list respects dependency constraints
func validateStepOrdering(t *testing.T, steps []testStep) {
	// Keep track of the highest depth seen for each chain
	maxDepthPerChain := make(map[string]int)

	// Walk through all steps in order
	for stepIndex, step := range steps {
		// Handle concurrent steps by examining each sub-step
		if concurrentStep, ok := step.(*concurrentStep); ok {
			// For concurrent steps, all sub-steps should be at the same depth level
			// and not violate ordering within their respective chains
			for _, subStep := range concurrentStep.steps {
				validateSingleStep(t, stepIndex, subStep, maxDepthPerChain)
			}
		} else {
			// Single step
			validateSingleStep(t, stepIndex, step, maxDepthPerChain)
		}
	}
}

// validateSingleStep validates ordering constraints for a single step
func validateSingleStep(t *testing.T, stepIndex int, step testStep, maxDepthPerChain map[string]int) {
	stepName := step.Description()

	// Parse step name to extract chain and depth
	parts := strings.Split(stepName, ":")
	if len(parts) != 3 {
		return // Skip steps that don't follow our naming convention
	}

	chainID := parts[0]
	depthStr := parts[1]

	// Convert depth to integer for proper comparison
	depth := 0
	if len(depthStr) > 0 {
		depth = int(depthStr[0] - '0') // Convert character to number
		if depth < 0 || depth > 9 {
			return // Invalid depth format
		}
	}

	// Check if we've seen a higher depth for this chain already
	if maxDepth, exists := maxDepthPerChain[chainID]; exists {
		if depth < maxDepth {
			t.Fatalf("Dependency violation at step %d: step %s (depth %d) appears after higher depth %d in chain %s",
				stepIndex+1, stepName, depth, maxDepth, chainID)
		}
	}

	// Update the maximum depth seen for this chain
	if maxDepth, exists := maxDepthPerChain[chainID]; !exists || depth > maxDepth {
		maxDepthPerChain[chainID] = depth
	}
}

// TestPlanRandomization generates 5 plans from the same test and logs them
func TestPlanRandomization(t *testing.T) {
	for i := 0; i < 5; i++ {
		mod := NewTest("randomization test", int64(12345+i))

		stage := mod.NewStage("test stage ", DisableFailureInjection())

		// Create a simple test with a few chains
		mod.InStage(stage, "step A", func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("step B1", func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).And("step B2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		})

		mod.InStage(stage, "step 1", func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		}).Then("step 2", func(ctx context.Context, l *logger.Logger, h *Helper) error {
			return nil
		})

		planner := NewSimplePlanner(mod, PlannerConfig{
			IsLocal:           true,
			ConcurrencyChance: 0.25,
		})

		plan, err := planner.Plan()
		if err != nil {
			t.Fatalf("Failed to generate plan: %v", err)
		}

		t.Logf("Plan %d:\n%s", i+1, plan.String())
	}
}
