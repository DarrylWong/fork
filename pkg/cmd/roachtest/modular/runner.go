package modular

import (
	"context"
	"fmt"
	"math/rand"
	"sync"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// TestRunner executes a modular test plan.
type TestRunner struct {
	plan       *TestPlan
	test       test.Test
	helper     *Helper
	logger     *logger.Logger
	rng        *rand.Rand
	background map[string]context.CancelFunc
	mu         sync.Mutex
}

// NewTestRunner creates a new test runner for the given plan.
func NewTestRunner(plan *TestPlan, t test.Test) *TestRunner {
	return &TestRunner{
		plan:       plan,
		test:       t,
		logger:     t.L(),
		rng:        rand.New(rand.NewSource(plan.seed)),
		background: make(map[string]context.CancelFunc),
		helper: &Helper{
			clusters:         make(map[string]Cluster),
			workloadClusters: make(map[string]*WorkloadCluster),
			rng:              rand.New(rand.NewSource(plan.seed)),
		},
	}
}

// Run executes the test plan.
func (r *TestRunner) Run(ctx context.Context) error {
	if r.logger != nil {
		r.logger.Printf("Starting modular test: %s", r.plan.name)
	}

	// Run all steps in order - they are already organized with concurrent steps as meta-steps
	for i, step := range r.plan.Steps() {
		if r.logger != nil {
			r.logger.Printf("Running step %d: %s", i+1, step.Description())
		}

		if err := step.Run(ctx, r.logger, r.helper); err != nil {
			return fmt.Errorf("step %d (%s) failed: %w", i+1, step.Description(), err)
		}
	}

	if r.logger != nil {
		r.logger.Printf("Completed modular test: %s", r.plan.name)
	}
	return nil
}

// runStage executes all steps in a stage.
func (r *TestRunner) runStage(ctx context.Context, stage *Stage) error {
	if r.logger != nil {
		r.logger.Printf("Running stage %s", stage.name)
	}
	return r.runSteps(ctx, stage.name, stage.Steps())
}

// runSteps executes a list of test steps, respecting concurrency settings.
func (r *TestRunner) runSteps(ctx context.Context, stageName string, steps []testStep) error {
	var wg sync.WaitGroup
	errorCh := make(chan error, len(steps))

	for i, step := range steps {
		stepID := fmt.Sprintf("%s-%d", stageName, i)
		if r.logger != nil {
			r.logger.Printf("Starting step: %s - %s", stepID, step.Description())
		}

		if step.ConcurrencyDisabled() {
			// Run sequentially
			if err := r.runSingleStep(ctx, stepID, step); err != nil {
				return err
			}
		} else {
			// Run concurrently
			wg.Add(1)
			go func(stepID string, step testStep) {
				defer wg.Done()
				if err := r.runSingleStep(ctx, stepID, step); err != nil {
					select {
					case errorCh <- err:
					default:
					}
				}
			}(stepID, step)
		}
	}

	// Wait for all concurrent steps to finish
	wg.Wait()
	close(errorCh)

	// Check for errors
	for err := range errorCh {
		if err != nil {
			return err
		}
	}

	return nil
}

// runSingleStep executes a single test step.
func (r *TestRunner) runSingleStep(ctx context.Context, stepID string, step testStep) error {
	if step.Background() != nil {
		// Background step
		stepCtx, cancel := context.WithCancel(ctx)
		r.mu.Lock()
		r.background[stepID] = cancel
		r.mu.Unlock()

		go func() {
			defer func() {
				r.mu.Lock()
				delete(r.background, stepID)
				r.mu.Unlock()
			}()

			if err := step.Run(stepCtx, r.logger, r.helper); err != nil {
				if r.logger != nil {
					r.logger.Printf("Background step %s failed: %v", stepID, err)
				}
			}
		}()

		return nil
	} else {
		// Foreground step
		return step.Run(ctx, r.logger, r.helper)
	}
}

// stopAllBackground stops all running background tasks.
func (r *TestRunner) stopAllBackground() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for stepID, cancel := range r.background {
		if r.logger != nil {
			r.logger.Printf("Stopping background step: %s", stepID)
		}
		cancel()
	}
	r.background = make(map[string]context.CancelFunc)
}
