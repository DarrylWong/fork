package modular

import (
	"context"
	"io"
	"math/rand"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// TestDynamicNamesWithRealOperations demonstrates dynamic names working with actual operations.
// This creates a mock scenario where AddRandomIndexDynamic would select a table and the name
// should reflect the selected table.
func TestDynamicNamesWithRealOperations(t *testing.T) {
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

	// Create stages with real-looking AddRandomIndexDynamic operations
	// We simulate what the operation would do by creating DynamicSteps
	type IndexPlan struct {
		DBName    string
		TableName string
	}

	// Simulate AddRandomIndexDynamic selecting "inventory.products"
	indexOp1 := NewDynamicStep(
		"add random index (dynamic)",
		func(ctx context.Context, l *logger.Logger, h *Helper) (*IndexPlan, error) {
			// Simulating table selection
			return &IndexPlan{DBName: "inventory", TableName: "products"}, nil
		},
		func(ctx context.Context, l *logger.Logger, h *Helper, plan *IndexPlan) error {
			l.Printf("Creating index on %s.%s", plan.DBName, plan.TableName)
			return nil
		},
		WithDynamicName(func(plan *IndexPlan) string {
			return "add random index to " + plan.DBName + "." + plan.TableName
		}),
		WithDynamicResourceCallback(func(plan *IndexPlan) ([]ResourceAccess, []ResourceAccess) {
			access := SchemaChangeAccess{
				Database: plan.DBName,
				Table:    plan.TableName,
			}.Resource(true)
			return []ResourceAccess{access}, []ResourceAccess{access}
		}),
	)

	// Simulate another AddRandomIndexDynamic selecting "sales.orders"
	indexOp2 := NewDynamicStep(
		"add random index (dynamic)",
		func(ctx context.Context, l *logger.Logger, h *Helper) (*IndexPlan, error) {
			return &IndexPlan{DBName: "sales", TableName: "orders"}, nil
		},
		func(ctx context.Context, l *logger.Logger, h *Helper, plan *IndexPlan) error {
			l.Printf("Creating index on %s.%s", plan.DBName, plan.TableName)
			return nil
		},
		WithDynamicName(func(plan *IndexPlan) string {
			return "add random index to " + plan.DBName + "." + plan.TableName
		}),
		WithDynamicResourceCallback(func(plan *IndexPlan) ([]ResourceAccess, []ResourceAccess) {
			access := SchemaChangeAccess{
				Database: plan.DBName,
				Table:    plan.TableName,
			}.Resource(true)
			return []ResourceAccess{access}, []ResourceAccess{access}
		}),
	)

	// Create a stage with both operations
	stage := Stage{
		name: "add-indexes",
		chains: []chain{
			{{testStep{StepProtocol: indexOp1}}},
			{{testStep{StepProtocol: indexOp2}}},
		},
	}

	t.Log("========== BEFORE PrePlan ==========")
	t.Logf("Step 1: %s", indexOp1.Description())
	t.Logf("Step 2: %s", indexOp2.Description())

	// Both should show generic names before PrePlan
	if indexOp1.Description() != "add random index (dynamic)" {
		t.Fatalf("Expected generic name before PrePlan for step 1, got: %s", indexOp1.Description())
	}
	if indexOp2.Description() != "add random index (dynamic)" {
		t.Fatalf("Expected generic name before PrePlan for step 2, got: %s", indexOp2.Description())
	}

	// Call PrePlan (this is what DynamicPlanRunner does)
	runner := NewDynamicRunner(planner)
	if err := runner.callPrePlanOnStage(&stage); err != nil {
		t.Fatalf("PrePlan failed: %v", err)
	}

	t.Log("\n========== AFTER PrePlan ==========")
	t.Logf("Step 1: %s", indexOp1.Description())
	t.Logf("Step 2: %s", indexOp2.Description())

	// Now both should show specific table names
	expectedName1 := "add random index to inventory.products"
	expectedName2 := "add random index to sales.orders"

	if indexOp1.Description() != expectedName1 {
		t.Fatalf("Dynamic name not working for step 1! Got %q, want %q",
			indexOp1.Description(), expectedName1)
	}
	if indexOp2.Description() != expectedName2 {
		t.Fatalf("Dynamic name not working for step 2! Got %q, want %q",
			indexOp2.Description(), expectedName2)
	}

	// Do chain merging
	mergedStage, err := planner.maybeMergeChains(stage)
	if err != nil {
		t.Fatalf("Chain merging failed: %v", err)
	}

	t.Log("\n========== AFTER Chain Merging ==========")
	// Extract steps from merged stage and verify names are preserved
	var mergedSteps []testStep
	for _, ch := range mergedStage.chains {
		for _, stepGroup := range ch {
			mergedSteps = append(mergedSteps, stepGroup...)
		}
	}

	for i, step := range mergedSteps {
		t.Logf("Merged step %d: %s", i, step.Description())
	}

	// Verify both steps still have dynamic names after merging
	foundStep1 := false
	foundStep2 := false
	for _, step := range mergedSteps {
		if step.Description() == expectedName1 {
			foundStep1 = true
		}
		if step.Description() == expectedName2 {
			foundStep2 = true
		}
	}

	if !foundStep1 {
		t.Fatalf("Dynamic name for step 1 lost after chain merging! Expected %q", expectedName1)
	}
	if !foundStep2 {
		t.Fatalf("Dynamic name for step 2 lost after chain merging! Expected %q", expectedName2)
	}

	// Generate DAG and verify names appear
	t.Log("\n========== DAG Output ==========")
	dag := GenerateDAG([]Stage{mergedStage})
	t.Logf("\n%s", dag)

	// Check that DAG contains the specific table names
	if !containsSubstring(dag, "inventory") || !containsSubstring(dag, "products") {
		t.Fatalf("DAG doesn't contain inventory.products table name!")
	}
	if !containsSubstring(dag, "sales") || !containsSubstring(dag, "orders") {
		t.Fatalf("DAG doesn't contain sales.orders table name!")
	}

	t.Log("\n========== SUCCESS ==========")
	t.Log("✓ Dynamic names show generic description before PrePlan")
	t.Log("✓ Dynamic names show specific tables after PrePlan")
	t.Log("✓ Dynamic names preserved through chain merging")
	t.Log("✓ Dynamic names visible in DAG output")
}
