package modular

import (
	"context"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/stretchr/testify/require"
)

// Helper function to create a dummy step function for testing.
func noopStep() stepFunc {
	return func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}
}

func mustGetStep(t *testing.T, stage *Stage, name string) *Step {
	step, err := stage.find(name)
	if err != nil {
		t.Fatal(err)
	}
	return step
}

// Test that we can build a simple linear chain:
// A ──▶ B ──▶ C
func TestBasicBuilder(t *testing.T) {
	test := &Test{}
	stage := test.NewStage("test-stage")

	test.InStage(stage, "A", noopStep()).
		Then("B", noopStep()).
		Then("C", noopStep())

	require.Len(t, stage.roots, 1)

	stepA := mustGetStep(t, stage, "A")
	require.Equal(t, "A", stepA.Description())
	require.Len(t, stepA.parents, 0, "A should not be dependent on any step")
	require.Len(t, stepA.children, 1, "A should have 1 step depending on it (B)")

	stepB := mustGetStep(t, stage, "B")
	require.Equal(t, "B", stepB.Description())
	require.Len(t, stepB.parents, 1, "B should depend on A")
	require.Len(t, stepB.children, 1, "B should have 1 step depending on it (C)")

	stepC := mustGetStep(t, stage, "C")
	require.Equal(t, "C", stepC.Description())
	require.Len(t, stepC.parents, 1, "C should depend on B")
	require.Len(t, stepC.children, 0, "C should have no steps depending on it")
}

// Test that we can build a chain with forking and converging dependencies:
/*
               ┌───▶ C ───┐
               │          │
A ───▶ B ──────┤          ├───▶ E
               │          │
               └───▶ D ───┘
*/
func TestAndBuilder(t *testing.T) {
	test := &Test{}
	stage := test.NewStage("test-stage")

	test.InStage(stage, "A", noopStep()).
		Then("B", noopStep()).
		Then("C", noopStep()).
		And("D", noopStep()).
		Then("E", noopStep())

	require.Len(t, stage.roots, 1)

	stepA := mustGetStep(t, stage, "A")
	require.Equal(t, "A", stepA.Description())
	require.Len(t, stepA.parents, 0, "A should not be dependent on any step")
	require.Len(t, stepA.children, 1, "A should have 1 step depending on it (B)")

	stepB := mustGetStep(t, stage, "B")
	require.Equal(t, "B", stepB.Description())
	require.Len(t, stepB.parents, 1, "B should depend on A")
	require.Len(t, stepB.children, 2, "B should have 2 steps depending on it (C and D)")

	stepC := mustGetStep(t, stage, "C")
	require.Equal(t, "C", stepC.Description())
	require.Len(t, stepC.parents, 1, "C should depend on B")
	require.Len(t, stepC.children, 1, "C should have 1 step depending on it (E)")

	stepD := mustGetStep(t, stage, "D")
	require.Equal(t, "D", stepD.Description())
	require.Len(t, stepD.parents, 1, "D should depend on B")
	require.Len(t, stepD.children, 1, "D should have 1 step depending on it (E)")

	stepE := mustGetStep(t, stage, "E")
	require.Equal(t, "E", stepE.Description())
	require.Len(t, stepE.parents, 2, "E should depend on C and D")
	require.Len(t, stepE.children, 0, "E should have no steps depending on it")
}

// Test that multiple chains in a stage still works.
/*
     ┌──▶ B
A ───┤
     └──▶ C

1 ───┐
     │──▶ 3
2 ───┘
*/
func TestMultipleChainsBuilder(t *testing.T) {
	test := &Test{}
	stage := test.NewStage("test-stage")

	test.InStage(stage, "A", noopStep()).
		Then("B", noopStep()).
		And("C", noopStep())

	test.InStage(stage, "1", noopStep()).
		And("2", noopStep()).
		Then("3", noopStep())

	require.Equal(t, 2, len(stage.roots))

	stepA := mustGetStep(t, stage, "A")
	require.Len(t, stepA.parents, 0)
	require.Len(t, stepA.children, 2)

	stepB := mustGetStep(t, stage, "B")
	require.Len(t, stepB.parents, 1)
	require.Len(t, stepB.children, 0)

	stepC := mustGetStep(t, stage, "C")
	require.Len(t, stepC.parents, 1)
	require.Len(t, stepC.children, 0)

	step1 := mustGetStep(t, stage, "1")
	require.Len(t, step1.parents, 0)
	require.Len(t, step1.children, 1)

	step2 := mustGetStep(t, stage, "2")
	require.Len(t, step2.parents, 0)
	require.Len(t, step2.children, 1)

	step3 := mustGetStep(t, stage, "3")
	require.Len(t, step3.parents, 2)
	require.Len(t, step3.children, 0)
}

// Test that our conditional methods work as expected.
// MaybeThen(true):  A ──▶ B ──▶ C
// MaybeThen(false): A ──▶ C (B is skipped)
func TestConditionalBuilder(t *testing.T) {
	// Test MaybeThen with condition=true
	test := &Test{}
	stage := test.NewStage("test-stage")

	test.InStage(stage, "A", noopStep()).
		MaybeThen(true, "B", noopStep()).
		Then("C", noopStep())

	require.Len(t, stage.stepMap, 3)

	stage = test.NewStage("test-stage 2")

	test.InStage(stage, "A", noopStep()).
		MaybeThen(false, "B", noopStep()).
		Then("C", noopStep())

	require.Len(t, stage.stepMap, 2)
	_, err := stage.find("B")
	require.Error(t, err)
	_ = mustGetStep(t, stage, "C")
}

// Test that demonstrates a complex dependency that can't be easily expressed
// with the step builder API.
/*
A ──▶ B ───┐
           │
           ├──▶ D
           │
C ─────────┘
*/
func TestEscapeHatch(t *testing.T) {
	test := &Test{}
	stage := test.NewStage("test-stage")

	// Create steps manually
	stepA := newTestStep(stage, test.nextNodeID(), "A", noopStep())
	stepB := newTestStep(stage, test.nextNodeID(), "B", noopStep())
	stepC := newTestStep(stage, test.nextNodeID(), "C", noopStep())
	stepD := newTestStep(stage, test.nextNodeID(), "D", noopStep())

	stepA.AddDependency(stepB)
	stepB.AddDependency(stepD)
	stepC.AddDependency(stepD)

	require.NoError(t, stage.Finalize())

	stepA = mustGetStep(t, stage, "A")
	require.Len(t, stepA.parents, 0)
	require.Len(t, stepA.children, 1)

	stepB = mustGetStep(t, stage, "B")
	require.Len(t, stepB.parents, 1)
	require.Len(t, stepB.children, 1)

	stepC = mustGetStep(t, stage, "C")
	require.Len(t, stepC.parents, 0)
	require.Len(t, stepC.children, 1)

	stepD = mustGetStep(t, stage, "D")
	require.Len(t, stepD.parents, 2)
	require.Len(t, stepD.children, 0)
}

// Test that finalize will return an error if there is a cycle in the dependencies.
/*
A ──▶ B
▲     │
│     ▼
└──── C
*/
func TestEscapeHatchCycle(t *testing.T) {
	test := &Test{}
	stage := test.NewStage("test-stage")

	stepA := newTestStep(stage, test.nextNodeID(), "A", noopStep())
	stepB := newTestStep(stage, test.nextNodeID(), "B", noopStep())
	stepC := newTestStep(stage, test.nextNodeID(), "C", noopStep())

	stepA.AddDependency(stepB)
	stepB.AddDependency(stepC)
	stepC.AddDependency(stepA)

	require.Error(t, stage.Finalize())
}

// Test that we can use the escape hatch with the builder API to create complex dependencies.
/*
A ──▶ B ───┐
           │
           ├──▶ D
           │
C ─────────┘
*/
func TestEscapeHatchWithBuilder(t *testing.T) {
	test := &Test{}
	stage := test.NewStage("test-stage")

	test.InStage(stage, "A", noopStep()).
		Then("B", noopStep()).
		Then("D", noopStep())

	test.InStage(stage, "C", noopStep())

	// Use escape hatch to add dependency from C to D
	stepC := mustGetStep(t, stage, "C")
	stepD := mustGetStep(t, stage, "D")
	stepC.AddDependency(stepD)

	require.NoError(t, stage.Finalize())

	stepA := mustGetStep(t, stage, "A")
	require.Len(t, stepA.parents, 0)
	require.Len(t, stepA.children, 1)

	stepB := mustGetStep(t, stage, "B")
	require.Len(t, stepB.parents, 1)
	require.Len(t, stepB.children, 1)

	stepC = mustGetStep(t, stage, "C")
	require.Len(t, stepC.parents, 0)
	require.Len(t, stepC.children, 1)

	stepD = mustGetStep(t, stage, "D")
	require.Len(t, stepD.parents, 2)
	require.Len(t, stepD.children, 0)
}
