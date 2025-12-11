package modular

import (
	"context"
	"io"
	"math/rand"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// TestDynamicNameWithChainMerging verifies that dynamic names work correctly
// through the entire planning flow including chain merging.
func TestDynamicNameWithChainMerging(t *testing.T) {
	type TestPlan struct {
		DB    string
		Table string
	}

	// Create a logger
	cfg := logger.Config{
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
	l, err := cfg.NewLogger("")
	if err != nil {
		t.Fatal(err)
	}

	// Create a planner
	planner := &TestPlanner{
		logger: l,
		rng:    rand.New(rand.NewSource(1)),
		ctx:    context.Background(),
	}

	// Create a dynamic step with a name callback
	dynamicStep := NewDynamicStep(
		"add index (dynamic)",
		func(ctx context.Context, l *logger.Logger, h *Helper) (*TestPlan, error) {
			return &TestPlan{DB: "testdb", Table: "users"}, nil
		},
		func(ctx context.Context, l *logger.Logger, h *Helper, plan *TestPlan) error {
			return nil
		},
		WithDynamicName(func(plan *TestPlan) string {
			return "add index to " + plan.DB + "." + plan.Table
		}),
		WithDynamicResourceCallback(func(plan *TestPlan) ([]ResourceAccess, []ResourceAccess) {
			access := SchemaChangeAccess{
				Database: plan.DB,
				Table:    plan.Table,
			}.Resource(true)
			return []ResourceAccess{access}, []ResourceAccess{access}
		}),
	)

	// Create a stage with the dynamic step
	stage := Stage{
		name: "test-stage",
		chains: []chain{
			{
				{testStep{StepProtocol: dynamicStep}},
			},
		},
	}

	t.Logf("Before PrePlan: %s", dynamicStep.Description())
	if dynamicStep.Description() != "add index (dynamic)" {
		t.Fatalf("Expected static name before PrePlan, got: %s", dynamicStep.Description())
	}

	// Create a DynamicPlanRunner and call PrePlan on the stage
	runner := NewDynamicRunner(planner)
	if err := runner.callPrePlanOnStage(&stage); err != nil {
		t.Fatalf("PrePlan failed: %v", err)
	}

	t.Logf("After PrePlan: %s", dynamicStep.Description())
	expectedName := "add index to testdb.users"
	if dynamicStep.Description() != expectedName {
		t.Fatalf("Expected dynamic name after PrePlan, got %q, want %q", 
			dynamicStep.Description(), expectedName)
	}

	// Now do chain merging
	mergedStage, err := planner.maybeMergeChains(stage)
	if err != nil {
		t.Fatalf("Chain merging failed: %v", err)
	}

	// The merged stage should still have the dynamic name
	// Extract the step from the merged stage
	if len(mergedStage.chains) == 0 || len(mergedStage.chains[0]) == 0 || len(mergedStage.chains[0][0]) == 0 {
		t.Fatal("Merged stage has no steps")
	}

	mergedStep := mergedStage.chains[0][0][0]
	t.Logf("After chain merging: %s", mergedStep.Description())
	
	if mergedStep.Description() != expectedName {
		t.Fatalf("Dynamic name lost after chain merging! Got %q, want %q",
			mergedStep.Description(), expectedName)
	}

	// Generate the plan and verify the name is still there
	plan := planner.generateStagePlan(mergedStage)
	if len(plan.steps) == 0 {
		t.Fatal("Plan has no steps")
	}

	t.Logf("In final plan: %s", plan.steps[0].Description())
	if plan.steps[0].Description() != expectedName {
		t.Fatalf("Dynamic name lost in final plan! Got %q, want %q",
			plan.steps[0].Description(), expectedName)
	}

	// Most importantly, verify that GenerateDAG shows the dynamic name
	// This is what the user actually sees in test output!
	dag := GenerateDAG([]Stage{mergedStage})
	t.Logf("DAG output:\n%s", dag)

	// Check if the DAG contains the key parts of the dynamic name
	// Note: DAG wraps text across multiple lines, so we check for key components
	if !(containsSubstring(dag, "testdb") && containsSubstring(dag, "users")) {
		t.Fatalf("Dynamic name components not found in DAG output! Expected to find table name in:\n%s", dag)
	}

	t.Logf("SUCCESS: Dynamic name preserved through entire flow including DAG generation!")
}

// containsSubstring checks if a string contains a substring
func containsSubstring(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
