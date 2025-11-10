package modular

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBasicRunner verifies that given a simple test plan, we execute all steps
// and in order. Specifically, we want to ensure that concurrent steps are properly
// handled.
func TestBasicRunner(t *testing.T) {
	plan := newTestPlan(12345, 10).
		Stage("Stage 1").
		AddStep("1").
		AddStep("2A", "2B").
		Stage("Stage 2").
		AddStep("3A", "3B", "3C").
		AddStep("4").
		Stage("Stage 3").
		AddStep("5").
		AddStep("6A", "6B", "6C").
		AddStep("7").
		AddStep("8").
		Plan()

	var output strings.Builder
	mockExec := NewMockExecutor(&output, newMockTaskManager())
	runner := &runner{
		executor: mockExec,
	}
	require.NoError(t, runner.Run(context.Background(), nilLogger(), *plan))

	// Parse output line by line, tracking the highest seen step number
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	maxStep := 0
	numSteps := 0
	for _, line := range lines {
		numSteps++
		// Get the step number from the first character
		stepNum, err := strconv.Atoi(string(line[0]))
		require.NoError(t, err)
		// Step must be the same as max or one higher
		require.True(t, stepNum == maxStep || stepNum == maxStep+1)

		if stepNum > maxStep {
			maxStep = stepNum
		}
	}

	// Verify we saw all steps
	require.Equal(t, 13, numSteps)
}
