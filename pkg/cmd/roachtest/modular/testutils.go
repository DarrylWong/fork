package modular

import (
	"io"

	"context"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/task"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"math/rand"
	"time"
)

// nilLogger returns a logger that discards all output, useful for unit tests.
func nilLogger() *logger.Logger {
	cfg := logger.Config{
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
	l, _ := cfg.NewLogger("")
	return l
}

// newMockTaskManager creates a new task manager for unit tests.
func newMockTaskManager() task.Manager {
	return task.NewManager(context.Background(), nilLogger())
}

// testPlanBuilder helps construct test plans for testing
type testPlanBuilder struct {
	plan         *TestPlan
	currentStage *stagePlan
	nextRunID    int
	maxMsDelay   int
}

func newTestPlan(seed int64, maxMsDelay int) *testPlanBuilder {
	return &testPlanBuilder{
		plan: &TestPlan{
			seed:       seed,
			rng:        rand.New(rand.NewSource(seed)),
			stagePlans: []stagePlan{},
		},
		currentStage: nil,
		nextRunID:    1,
		maxMsDelay:   maxMsDelay,
	}
}

// Stage starts a new stage with the given name
func (b *testPlanBuilder) Stage(name string) *testPlanBuilder {
	b.plan.stagePlans = append(b.plan.stagePlans, stagePlan{
		stage: &stage{name: name},
		steps: []step{},
	})
	b.currentStage = &b.plan.stagePlans[len(b.plan.stagePlans)-1]
	return b
}

// AddStep adds one or more steps to the current stage.
// If a single description is provided, it's added as a sequential step.
// If multiple descriptions are provided, they're grouped as concurrent steps.
func (b *testPlanBuilder) AddStep(descriptions ...string) *testPlanBuilder {
	if len(descriptions) == 1 {
		// Single step - sequential
		s := step{
			StepProtocol: &SingleStep{
				description: descriptions[0],
				fn:          func(context.Context, *logger.Logger, *Helper) error { return nil },
			},
		}
		s.runID = b.nextRunID
		b.nextRunID++
		b.currentStage.steps = append(b.currentStage.steps, s)
		return b
	}

	// Multiple steps - concurrent
	var concurrentSteps []step
	var delays []time.Duration

	for _, desc := range descriptions {
		s := step{
			StepProtocol: &SingleStep{
				description: desc,
				fn:          func(context.Context, *logger.Logger, *Helper) error { return nil },
			},
		}
		s.runID = b.nextRunID
		b.nextRunID++
		concurrentSteps = append(concurrentSteps, s)
		newDelay := time.Duration(0)
		if b.plan.rng.Float64() < 0.5 {
			newDelay = time.Duration(b.plan.rng.Intn(b.maxMsDelay)) * time.Millisecond
		}
		delays = append(delays, newDelay)
	}

	cs := step{
		StepProtocol: &concurrentStep{
			steps:  concurrentSteps,
			delays: delays,
		},
	}
	b.currentStage.steps = append(b.currentStage.steps, cs)
	return b
}

func (b *testPlanBuilder) Plan() *TestPlan {
	return b.plan
}
