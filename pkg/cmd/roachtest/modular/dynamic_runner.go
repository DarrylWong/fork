package modular

import (
	"context"
	gosql "database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// DynamicPlanRunner executes test plans dynamically, making decisions at runtime
// based on cluster state and execution context. Unlike StaticPlanRunner which plans
// all stages upfront, DynamicPlanRunner plans and executes one stage at a time:
// plan stage 1 -> run stage 1 -> plan stage 2 -> run stage 2 -> ...
type DynamicPlanRunner struct {
	// Original test planner with stages to execute
	planner *TestPlanner

	// Execution state
	helper       *Helper
	stateTracker *ClusterStateTracker
	lockManager  *RuntimeLockManager

	// Current stage counter for step ID assignment
	currentStepID int
}

// RunDynamicTestPlan executes the test plan dynamically using the DynamicPlanRunner.
// It plans and executes stages one at a time, allowing for truly dynamic resource access
// where PrePlan is called before chain merging to make dynamic resources visible.
func RunDynamicTestPlan(ctx context.Context, t test.Test, planner *TestPlanner) error {
	runner := NewDynamicRunner(planner)
	return runner.Run(ctx, t)
}

// NewDynamicRunner creates a new dynamic runner for executing stages one at a time.
func NewDynamicRunner(planner *TestPlanner) *DynamicPlanRunner {
	clusterStateLogger := planner.debugModules.NewLogger(planner.logger, ClusterStateDebug)
	lockManager := NewRuntimeLockManager()

	return &DynamicPlanRunner{
		planner:       planner,
		helper:        &Helper{rng: planner.rng, lockManager: lockManager},
		stateTracker:  NewClusterStateTracker(clusterStateLogger),
		lockManager:   lockManager,
		currentStepID: 0,
	}
}

// Run executes the test plan dynamically, planning and executing one stage at a time.
func (r *DynamicPlanRunner) Run(ctx context.Context, t test.Test) error {
	l := t.L()

	// Initialize the helper with test context
	r.initializeHelper(ctx, t)

	// Log the test details
	l.Printf("Seed: %d", r.planner.seed)
	l.Printf("Number of stages: %d", len(r.planner.stages))
	l.Printf("Running in DYNAMIC mode: planning and executing one stage at a time")

	// Execute each stage dynamically
	for stageIdx := range r.planner.stages {
		stageName := r.planner.stages[stageIdx].name
		if stageName == "" {
			stageName = fmt.Sprintf("stage %d", stageIdx+1)
		}

		l.Printf("========== Planning stage %d: %s ==========", stageIdx+1, stageName)

		// Plan this stage based on current cluster state
		mergedStage, stagePlan, err := r.planStage(stageIdx)
		if err != nil {
			return fmt.Errorf("failed to plan stage %d (%s): %w", stageIdx+1, stageName, err)
		}

		l.Printf("Stage %d planned with %d steps", stageIdx+1, len(stagePlan.steps))

		// Print the DAG for this stage (dynamic names are now available since PrePlan was called)
		l.Printf("========== DAG for stage %d: %s ==========", stageIdx+1, stageName)
		stageDAG := GenerateDAG([]Stage{mergedStage})
		l.Printf("\n%s", stageDAG)

		// Print the plan for this stage
		l.Printf("========== Plan for stage %d: %s ==========", stageIdx+1, stageName)
		planStr := r.formatStagePlan(stagePlan)
		l.Printf("\n%s", planStr)

		// Execute the planned stage
		l.Printf("========== Executing stage %d: %s ==========", stageIdx+1, stageName)
		start := time.Now()

		stageLogger, err := r.loggerForStage(l, stageIdx, stageName)
		if err != nil {
			return fmt.Errorf("failed to create stage logger: %w", err)
		}

		err = r.executeStage(ctx, stageLogger, stagePlan)
		if err != nil {
			return fmt.Errorf("stage %d (%s) failed: %w", stageIdx+1, stageName, err)
		}

		duration := time.Since(start)
		l.Printf("========== Stage %d completed in %s ==========", stageIdx+1, duration)

		r.stateTracker.LogClusterStateDebug(fmt.Sprintf("after stage: %s", stageName))
	}

	l.Printf("All stages completed successfully")
	return nil
}

// planStage dynamically plans a single stage based on current cluster state.
// This is where runtime decisions can be made about which operations to run,
// in what order, and with what level of concurrency.
// Returns the merged stage (with PrePlan called) and the execution plan.
func (r *DynamicPlanRunner) planStage(stageIdx int) (Stage, stagePlan, error) {
	stage := r.planner.stages[stageIdx]

	// IMPORTANT: Call PrePlan BEFORE chain merging so that dynamic resources
	// are visible to the chain merger. This enables truly dynamic resource access.
	if err := r.callPrePlanOnStage(&stage); err != nil {
		return Stage{}, stagePlan{}, fmt.Errorf("PrePlan failed: %w", err)
	}

	// Merge chains if needed (now we can see dynamic resources!)
	mergedStage, err := r.planner.maybeMergeChains(stage)
	if err != nil {
		return Stage{}, stagePlan{}, err
	}

	// Generate the stage plan
	plan := r.planner.generateStagePlan(mergedStage)

	// Create concurrent steps
	r.planner.CreateConcurrentSteps(&plan)

	// Assign step IDs for this stage
	r.assignStageStepIDs(&plan)

	return mergedStage, plan, nil
}

// callPrePlanOnStage invokes PrePlan on all dynamic steps in a stage
// BEFORE chain merging. This allows dynamic resources to be visible to the chain merger.
func (r *DynamicPlanRunner) callPrePlanOnStage(stage *Stage) error {
	ctx := r.planner.ctx
	l := r.planner.logger

	// Iterate through all chains and step groups
	for _, ch := range stage.chains {
		for _, stepGroup := range ch {
			for i := range stepGroup {
				step := &stepGroup[i]

				// Check if this step implements a PrePlan method
				switch protocol := step.StepProtocol.(type) {
				case interface{ PrePlan(context.Context, *logger.Logger, *Helper) (interface{}, error) }:
					// This step has a PrePlan method - call it
					result, err := protocol.PrePlan(ctx, l, r.helper)
					if err != nil {
						return fmt.Errorf("PrePlan failed for %s: %w", step.Description(), err)
					}
					// Cache the result in the testStep for later execution
					step.planResult = result
					l.Printf("PrePlan completed for %s", step.Description())
				}
			}
		}
	}

	return nil
}

// callPrePlanCallbacks invokes PrePlan on all dynamic steps in the plan
// and caches the results for use during execution.
// NOTE: This is now only used as a fallback. PrePlan should be called via
// callPrePlanOnStage BEFORE chain merging.
func (r *DynamicPlanRunner) callPrePlanCallbacks(plan *stagePlan) error {
	ctx := r.planner.ctx
	l := r.planner.logger

	for i := range plan.steps {
		step := &plan.steps[i]

		// Check if this step implements a PrePlan method
		// We use type assertion to check for DynamicStep
		// Note: We can't use a simple interface because DynamicStep is generic
		// and we don't know the type parameter at compile time

		// Try to extract the underlying step protocol
		switch protocol := step.StepProtocol.(type) {
		case interface{ PrePlan(context.Context, *logger.Logger, *Helper) (interface{}, error) }:
			// This step has a PrePlan method - call it
			result, err := protocol.PrePlan(ctx, l, r.helper)
			if err != nil {
				return fmt.Errorf("step %d (%s) PrePlan failed: %w",
					step.stepID, step.Description(), err)
			}
			// Cache the result for execution
			step.planResult = result
			l.Printf("PrePlan completed for step %d (%s)", step.stepID, step.Description())
		}

		// For concurrent steps, recursively call PrePlan on nested steps
		if concStep, ok := step.StepProtocol.(*concurrentStep); ok {
			for j := range concStep.steps {
				nestedStep := &concStep.steps[j]
				if protocol, ok := nestedStep.StepProtocol.(interface{
					PrePlan(context.Context, *logger.Logger, *Helper) (interface{}, error)
				}); ok {
					result, err := protocol.PrePlan(ctx, l, r.helper)
					if err != nil {
						return fmt.Errorf("nested step %d (%s) PrePlan failed: %w",
							nestedStep.stepID, nestedStep.Description(), err)
					}
					nestedStep.planResult = result
					l.Printf("PrePlan completed for nested step %d (%s)",
						nestedStep.stepID, nestedStep.Description())
				}
			}
		}
	}

	return nil
}

// assignStageStepIDs assigns sequential step IDs to all steps in a stage.
func (r *DynamicPlanRunner) assignStageStepIDs(plan *stagePlan) {
	for i := range plan.steps {
		plan.steps[i].stepID = r.currentStepID
		r.currentStepID++
	}
}

// executeStage executes all steps within a single planned stage.
// This is similar to StaticPlanRunner.executeStage but operates on a single stage.
func (r *DynamicPlanRunner) executeStage(ctx context.Context, l *logger.Logger, stagePlan stagePlan) error {
	for _, step := range stagePlan.steps {
		stepLogger, err := r.loggerForStep(l, step.stepID, step.Description())
		if err != nil {
			return fmt.Errorf("failed to create step logger: %w", err)
		}

		r.logStep("STARTING", step.stepID, step.Description(), stepLogger)
		start := time.Now()

		// Acquire locks before executing step
		unlocks, err := r.acquireStepLocks(step, stepLogger)
		if err != nil {
			return err
		}

		// Execute the step - use RunWithPlan for dynamic steps with cached plan results
		err = r.executeStep(ctx, stepLogger, step)

		// Release locks after step execution
		r.releaseStepLocks(step, unlocks, stepLogger)

		if err != nil {
			return fmt.Errorf("step %d (%s) failed: %w", step.stepID, step.Description(), err)
		}

		duration := time.Since(start)
		r.logStep(fmt.Sprintf("FINISHED [%s]", duration), step.stepID, step.Description(), stepLogger)

		// Print tracked state after each step
		stateOutput := r.stateTracker.PrintTrackedState()
		stepLogger.Printf("State after step completion:\n%s", stateOutput)
	}

	return nil
}

// executeStep executes a single step, using RunWithPlan for dynamic steps
// with cached plan results.
func (r *DynamicPlanRunner) executeStep(ctx context.Context, l *logger.Logger, step testStep) error {
	// Check if this step has a cached plan result from PrePlan
	if step.planResult != nil {
		// This is a dynamic step with a plan result - check if we can call RunWithPlan
		switch protocol := step.StepProtocol.(type) {
		case interface{ RunWithPlan(context.Context, *logger.Logger, *Helper, interface{}) error }:
			return protocol.RunWithPlan(ctx, l, r.helper, step.planResult)
		}
	}

	// Either not a dynamic step or no plan result - use regular Run
	return step.Run(ctx, l, r.helper)
}

// logStep logs step start/finish messages (borrowed from StaticPlanRunner).
func (r *DynamicPlanRunner) logStep(prefix string, stepID int, stepDesc string, l *logger.Logger) {
	l.Printf("---------- %s (%d): %s ----------", prefix, stepID, stepDesc)
}

// loggerForStage creates a logger instance for a stage.
func (r *DynamicPlanRunner) loggerForStage(parent *logger.Logger, stageIdx int, stageName string) (*logger.Logger, error) {
	name := invalidChars.ReplaceAllString(strings.ToLower(stageName), "")
	name = fmt.Sprintf("stage_%d_%s", stageIdx, name)
	return parent.ChildLogger(name)
}

// loggerForStep creates a logger instance for a step.
func (r *DynamicPlanRunner) loggerForStep(parent *logger.Logger, stepID int, stepDesc string) (*logger.Logger, error) {
	name := invalidChars.ReplaceAllString(strings.ToLower(stepDesc), "")
	name = fmt.Sprintf("%d_%s", stepID, name)
	return parent.ChildLogger(name)
}

// acquireStepLocks acquires all locks declared by a step.
// Returns unlock functions that should be called when the step completes.
func (r *DynamicPlanRunner) acquireStepLocks(step testStep, l *logger.Logger) ([]func(), error) {
	var unlocks []func()

	// Check if the step implements ResourceAware interface
	resourceAware, ok := step.StepProtocol.(ResourceAware)
	if !ok {
		// concurrentStep or other types don't have direct resource declarations
		return unlocks, nil
	}

	// Acquire all declared accesses
	accesses := resourceAware.GetResourceAccesses()
	for _, access := range accesses {
		l.Printf("Acquiring lock: %s", access.String())
		unlock, ok := r.lockManager.Acquire(access)
		if !ok {
			// Failed to acquire lock - release any locks we already acquired
			for _, u := range unlocks {
				u()
			}
			return nil, fmt.Errorf("failed to acquire lock %s: conflicts with existing locks", access.String())
		}
		unlocks = append(unlocks, unlock)
	}

	return unlocks, nil
}

// releaseStepLocks releases locks acquired by a step and processes any release declarations.
func (r *DynamicPlanRunner) releaseStepLocks(step testStep, unlocks []func(), l *logger.Logger) {
	// Release locks that were acquired at step start
	for _, unlock := range unlocks {
		unlock()
	}

	// Process explicit release declarations
	resourceAware, ok := step.StepProtocol.(ResourceAware)
	if !ok {
		return
	}

	for _, release := range resourceAware.GetResourceReleases() {
		l.Printf("Processed release: %s", release.String())
		// The release should already be handled by the unlock functions above,
		// but this logs the explicit releases for debugging
	}
}

// initializeHelper sets up the helper with the necessary context and dependencies.
func (r *DynamicPlanRunner) initializeHelper(ctx context.Context, t test.Test) {
	// Initialize helper with context and cluster information
	r.helper.ctx = ctx
	r.helper.logger = t.L()
	r.helper.stateTracker = r.stateTracker

	// Use the test's task management interface
	r.helper.background = &testTaskManager{test: t}

	// Get cluster information from the planner
	var c cluster.Cluster
	var crdbNodes option.NodeListOption
	if r.planner.cluster != nil {
		c = r.planner.cluster
		crdbNodes = r.planner.crdbNodes
		r.helper.cluster = c
	}

	// Set up connection function if cluster is available
	var connFunc func(int) *gosql.DB
	if c != nil {
		connFunc = func(node int) *gosql.DB {
			// Use root certificate authentication for secure clusters
			if c.IsSecure() {
				return c.Conn(ctx, t.L(), node, option.AuthMode(install.AuthRootCert))
			}
			return c.Conn(ctx, t.L(), node)
		}
	}

	// Set up the default service with cluster functionality
	r.helper.defaultService = &Service{
		name:       "default",
		ctx:        ctx,
		connFunc:   connFunc,
		stepLogger: t.L(),
		monitor:    t.Monitor(),
		cluster:    c,
		nodes:      crdbNodes,
	}
}

// formatStagePlan formats a stage plan for logging, showing all steps with their descriptions.
func (r *DynamicPlanRunner) formatStagePlan(plan stagePlan) string {
	var out strings.Builder
	for i, step := range plan.steps {
		prefix := "├──"
		if i == len(plan.steps)-1 {
			prefix = "└──"
		}
		out.WriteString(fmt.Sprintf("%s Step %d: %s\n", prefix, step.stepID, step.Description()))
	}
	return out.String()
}
