package modular

import (
	"context"
	gosql "database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/task"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/failureinjection/failures"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

var (
	// everything that is not an alphanum or a few special characters
	invalidChars = regexp.MustCompile(`[^a-zA-Z0-9 \-_.]`)
)

// Runner executes a generated test plan from the modular framework.
type Runner struct {
	testPlan     *TestPlan
	helper       *Helper
	stateTracker *ClusterStateTracker
}

// NewRunner creates a new runner for executing a test plan.
func NewRunner(testPlan *TestPlan) *Runner {
	clusterStateLogger := testPlan.debugModules.NewLogger(testPlan.logger, ClusterStateDebug)

	return &Runner{
		testPlan:     testPlan,
		helper:       &Helper{rng: testPlan.rng},
		stateTracker: NewClusterStateTracker(clusterStateLogger),
	}
}

// RunTestPlan executes the test plan using the provided roachtest.Test interface.
// It logs the DAG and test plan, then executes all steps in position.
func RunTestPlan(ctx context.Context, t test.Test, testPlan *TestPlan) error {
	runner := NewRunner(testPlan)
	return runner.Run(ctx, t)
}

// Run executes the test plan, logging the DAG and test plan before execution.
func (r *Runner) Run(ctx context.Context, t test.Test) error {
	l := t.L()

	// Initialize the helper with test context
	r.initializeHelper(ctx, t)

	// Log the test plan details
	l.Printf("Seed: %d", r.testPlan.seed)
	l.Printf("Number of stages: %d", len(r.testPlan.stagePlans))

	// Log the full test plan structure
	l.Printf("Test Plan Structure:\n%s", r.testPlan.String())

	// Generate and log the DAG
	planner := &TestPlanner{
		seed:   r.testPlan.seed,
		stages: r.extractStages(),
	}
	dag := planner.DAG()
	l.Printf("DAG Visualization:\n%s", dag)

	// Execute all steps in the plan
	return r.executeSteps(ctx, l)
}

// initializeHelper sets up the helper with the necessary context and dependencies.
func (r *Runner) initializeHelper(ctx context.Context, t test.Test) {
	// Initialize helper with context and cluster information
	r.helper.ctx = ctx
	r.helper.logger = t.L()
	r.helper.stateTracker = r.stateTracker

	// Use the test's task management interface
	r.helper.background = &testTaskManager{test: t}

	// Get cluster information from the test plan
	var c cluster.Cluster
	var crdbNodes option.NodeListOption
	if r.testPlan.cluster != nil {
		c = r.testPlan.cluster
		crdbNodes = r.testPlan.crdbNodes
		r.helper.cluster = c
	}

	// Set up connection function if cluster is available
	var connFunc func(int) *gosql.DB
	if c != nil {
		connFunc = func(node int) *gosql.DB {
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

// testTaskManager adapts the test.Test interface to the task.Manager interface
type testTaskManager struct {
	test test.Test
}

func (tm *testTaskManager) GoWithCancel(fn task.Func, opts ...task.Option) context.CancelFunc {
	return tm.test.GoWithCancel(fn, opts...)
}

func (tm *testTaskManager) Go(fn task.Func, opts ...task.Option) {
	tm.test.Go(fn, opts...)
}

func (tm *testTaskManager) NewGroup(opts ...task.Option) task.Group {
	return tm.test.NewGroup(opts...)
}

func (tm *testTaskManager) NewErrorGroup(opts ...task.Option) task.ErrorGroup {
	return tm.test.NewErrorGroup(opts...)
}

func (tm *testTaskManager) Terminate(_ *logger.Logger) {
	// The test framework handles termination
}

func (tm *testTaskManager) Cancel() {
	// The test framework handles cancellation
}

func (tm *testTaskManager) CompletedEvents() <-chan task.Event {
	// Return a closed channel since the test framework handles events
	ch := make(chan task.Event)
	close(ch)
	return ch
}

// extractStages extracts Stage objects from the test plan for DAG generation.
func (r *Runner) extractStages() []Stage {
	var stages []Stage
	for _, stagePlan := range r.testPlan.stagePlans {
		if stagePlan.stage != nil {
			stages = append(stages, *stagePlan.stage)
		}
	}
	return stages
}

// executeSteps executes all steps in the test plan sequentially by stage.
func (r *Runner) executeSteps(ctx context.Context, l *logger.Logger) error {
	for stageIdx, stagePlan := range r.testPlan.stagePlans {
		stageName := stagePlan.stage.name
		if stageName == "" {
			stageName = fmt.Sprintf("stage %d", stageIdx+1)
		}

		stageLogger, err := r.loggerForStage(l, stageIdx, stageName)
		if err != nil {
			return fmt.Errorf("failed to create stage logger: %w", err)
		}

		r.logStage("STARTING", stageName, stageLogger)
		start := time.Now()

		err = r.executeStage(ctx, stageLogger, stagePlan)
		if err != nil {
			// Attempt to restore cluster state before returning the error (if enabled)
			if r.testPlan.cleanupOnFailure {
				l.Printf("Attempting to restore cluster state due to execution failure...")
				if restoreErr := r.restoreClusterState(ctx, l); restoreErr != nil {
					l.Printf("Failed to restore cluster state: %v", restoreErr)
				}
			}
			return r.stageError(ctx, err, stageName, stageLogger)
		}

		duration := time.Since(start)
		prefix := fmt.Sprintf("FINISHED [%s]", duration)
		r.logStage(prefix, stageName, stageLogger)

		r.stateTracker.LogClusterStateDebug(fmt.Sprintf("after stage: %s", stageName))
	}

	l.Printf("All stages completed successfully")
	return nil
}

// executeStage executes all steps within a single stage.
func (r *Runner) executeStage(ctx context.Context, l *logger.Logger, stagePlan stagePlan) error {
	for _, step := range stagePlan.steps {
		stepLogger, err := r.loggerForStep(l, step.stepID, step.Description())
		if err != nil {
			return fmt.Errorf("failed to create step logger: %w", err)
		}

		r.logStep("STARTING", step.stepID, step.Description(), stepLogger)
		start := time.Now()

		err = step.Run(ctx, stepLogger, r.helper)
		if err != nil {
			return r.stepError(ctx, err, step.stepID, step.Description(), stepLogger)
		}

		duration := time.Since(start)
		prefix := fmt.Sprintf("FINISHED [%s]", duration)
		r.logStep(prefix, step.stepID, step.Description(), stepLogger)

		// Print tracked state after each step for debugging and visibility
		stateOutput := r.stateTracker.PrintTrackedState()
		stepLogger.Printf("State after step completion:\n%s", stateOutput)
	}

	return nil
}

// logStage logs stage start/finish messages with consistent formatting.
func (r *Runner) logStage(prefix, stageName string, l *logger.Logger) {
	dashes := strings.Repeat("=", 10)
	l.Printf("%[1]s %s: %s %[1]s", dashes, prefix, stageName)
}

// logStep logs step start/finish messages with consistent formatting.
func (r *Runner) logStep(prefix string, stepID int, stepDesc string, l *logger.Logger) {
	dashes := strings.Repeat("-", 10)
	l.Printf("%[1]s %s (%d): %s %[1]s", dashes, prefix, stepID, stepDesc)
}

// loggerForStage creates a logger instance for a stage.
func (r *Runner) loggerForStage(parent *logger.Logger, stageIdx int, stageName string) (*logger.Logger, error) {
	name := invalidChars.ReplaceAllString(strings.ToLower(stageName), "")
	name = fmt.Sprintf("stage_%d_%s", stageIdx, name)
	return parent.ChildLogger(name)
}

// loggerForStep creates a logger instance for a step, similar to mixed-version runner.
func (r *Runner) loggerForStep(parent *logger.Logger, stepID int, stepDesc string) (*logger.Logger, error) {
	name := invalidChars.ReplaceAllString(strings.ToLower(stepDesc), "")
	name = fmt.Sprintf("%d_%s", stepID, name)
	return parent.ChildLogger(name)
}

// stepError generates a detailed error for step failures.
func (r *Runner) stepError(ctx context.Context, err error, stepID int, stepDesc string, l *logger.Logger) error {
	stepErr := fmt.Errorf("modular test failure while running step %d (%s): %w", stepID, stepDesc, err)

	// Log the error for convenience
	l.Printf("Step failed: %+v", stepErr)

	// Rename the log file to indicate failure
	if renameErr := r.renameFailedLogger(l); renameErr != nil {
		l.Printf("could not rename failed step logger: %v", renameErr)
	}

	return stepErr
}

// stageError generates a detailed error for stage failures.
func (r *Runner) stageError(ctx context.Context, err error, stageName string, l *logger.Logger) error {
	stageErr := fmt.Errorf("modular test failure while running stage %s: %w", stageName, err)

	// Log the error for convenience
	l.Printf("Stage failed: %+v", stageErr)

	// Rename the log file to indicate failure
	if renameErr := r.renameFailedLogger(l); renameErr != nil {
		l.Printf("could not rename failed stage logger: %v", renameErr)
	}

	return stageErr
}

// restoreClusterState attempts to restore the cluster to its original state by
// reversing all tracked changes. This should be called on test failure.
func (r *Runner) restoreClusterState(ctx context.Context, l *logger.Logger) error {
	l.Printf("Starting cluster state restoration...")

	// Get all tracked state from the state tracker
	tables, settings, zoneConfigs, failureMap, schemas, users, databases := r.stateTracker.GetTrackedState()

	// Restore cluster settings to original values
	for setting, originalValue := range settings {
		if err := r.restoreClusterSetting(ctx, l, setting, originalValue); err != nil {
			l.Printf("Failed to restore cluster setting %s: %v", setting, err)
			// Continue with other restorations
		}
	}

	// Restore zone configurations to original values
	for rangeName, originalConfig := range zoneConfigs {
		if err := r.restoreZoneConfig(ctx, l, rangeName, originalConfig); err != nil {
			l.Printf("Failed to restore zone config for %s: %v", rangeName, err)
			// Continue with other restorations
		}
	}

	// Drop tables that were created
	for _, table := range tables {
		if err := r.dropTable(ctx, l, table); err != nil {
			l.Printf("Failed to drop table %s: %v", table, err)
			// Continue with other restorations
		}
	}

	// Drop schemas that were created
	for _, schema := range schemas {
		if err := r.dropSchema(ctx, l, schema); err != nil {
			l.Printf("Failed to drop schema %s: %v", schema, err)
			// Continue with other restorations
		}
	}

	// Drop users that were created
	for _, user := range users {
		if err := r.dropUser(ctx, l, user); err != nil {
			l.Printf("Failed to drop user %s: %v", user, err)
			// Continue with other restorations
		}
	}

	// Drop databases that were created
	for _, db := range databases {
		if err := r.dropDatabase(ctx, l, db); err != nil {
			l.Printf("Failed to drop database %s: %v", db, err)
			// Continue with other restorations
		}
	}

	// Recover from failures that were injected
	for failureID, failer := range failureMap {
		if err := r.recoverFromFailure(ctx, l, failureID, failer); err != nil {
			l.Printf("Failed to recover from failure %s: %v", failureID, err)
			// Continue with other restorations
		}
	}

	l.Printf("Cluster state restoration completed")
	return nil
}

// restoreClusterSetting restores a cluster setting to its original value.
func (r *Runner) restoreClusterSetting(ctx context.Context, l *logger.Logger, setting, originalValue string) error {
	l.Printf("Restoring cluster setting %s to original value: %s", setting, originalValue)
	// Use parameterized query for the value but format the setting name
	query := fmt.Sprintf("SET CLUSTER SETTING %s = $1", setting)
	return r.helper.Exec(query, originalValue)
}

// restoreZoneConfig restores a zone configuration to its original value.
func (r *Runner) restoreZoneConfig(ctx context.Context, l *logger.Logger, rangeName, originalConfig string) error {
	l.Printf("Restoring zone config for %s to original value", rangeName)

	return r.helper.Exec(originalConfig)
}

// dropTable drops a table that was created during the test.
func (r *Runner) dropTable(ctx context.Context, l *logger.Logger, table string) error {
	l.Printf("Dropping table %s", table)
	return r.helper.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
}

// dropSchema drops a schema that was created during the test.
func (r *Runner) dropSchema(ctx context.Context, l *logger.Logger, schema string) error {
	l.Printf("Dropping schema %s", schema)
	return r.helper.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema))
}

// dropUser drops a user that was created during the test.
func (r *Runner) dropUser(ctx context.Context, l *logger.Logger, user string) error {
	l.Printf("Dropping user %s", user)
	return r.helper.Exec(fmt.Sprintf("DROP USER IF EXISTS %s", user))
}

// dropDatabase drops a database that was created during the test.
func (r *Runner) dropDatabase(ctx context.Context, l *logger.Logger, db string) error {
	l.Printf("Dropping database %s", db)
	return r.helper.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s CASCADE", db))
}

// recoverFromFailure attempts to recover from an injected failure.
func (r *Runner) recoverFromFailure(ctx context.Context, l *logger.Logger, failureID string, failer *failures.Failer) error {
	l.Printf("Recovering from failure %s: %s", failureID, failer.Description())
	return failer.Recover(ctx, l)
}

// renameFailedLogger renames the log file to include "FAILED" prefix.
func (r *Runner) renameFailedLogger(l *logger.Logger) error {
	if l.File == nil {
		return nil
	}

	currentFileName := l.File.Name()
	newLogName := filepath.Join(
		filepath.Dir(currentFileName),
		"FAILED_"+filepath.Base(currentFileName),
	)
	return os.Rename(currentFileName, newLogName)
}
