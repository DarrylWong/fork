package modular

import (
	"context"
	gosql "database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/task"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

var (
	// everything that is not an alphanum or a few special characters
	invalidChars = regexp.MustCompile(`[^a-zA-Z0-9 \-_.]`)
)

// FailureInfo contains information about an injected failure for recovery purposes.
type FailureInfo struct {
	Type         string                 `json:"type"`         // Type of failure (e.g., "network_partition", "node_crash")
	Description  string                 `json:"description"`  // Human-readable description
	RecoveryInfo map[string]interface{} `json:"recovery_info"` // Recovery-specific data
}

// ClusterStateTracker tracks cluster state changes made during test execution
// to allow restoration to the original state on failure.
type ClusterStateTracker struct {
	mu sync.RWMutex

	// tablesAdded tracks tables created during the test
	// Key: table name (e.g., "mydb.mytable"), Value: empty struct
	tablesAdded map[string]struct{}

	// clusterSettings tracks cluster settings that were modified
	// Key: setting name, Value: original value (before any test modifications)
	clusterSettings map[string]string

	// failuresInjected tracks failure injection operations
	// Key: failure identifier, Value: failure details for recovery
	failuresInjected map[string]FailureInfo

	// schemasCreated tracks schemas created during the test
	// Key: schema name, Value: empty struct
	schemasCreated map[string]struct{}

	// usersCreated tracks users created during the test
	// Key: username, Value: empty struct
	usersCreated map[string]struct{}

	// databasesCreated tracks databases created during the test
	// Key: database name, Value: empty struct
	databasesCreated map[string]struct{}
}

// NewClusterStateTracker creates a new cluster state tracker with initialized maps.
func NewClusterStateTracker() *ClusterStateTracker {
	return &ClusterStateTracker{
		tablesAdded:      make(map[string]struct{}),
		clusterSettings:  make(map[string]string),
		failuresInjected: make(map[string]FailureInfo),
		schemasCreated:   make(map[string]struct{}),
		usersCreated:     make(map[string]struct{}),
		databasesCreated: make(map[string]struct{}),
	}
}

// TrackTableAdded records that a table was created during the test.
func (c *ClusterStateTracker) TrackTableAdded(tableName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tablesAdded[tableName] = struct{}{}
}

// UntrackTableAdded removes a table from tracking (e.g., if the test drops it later).
func (c *ClusterStateTracker) UntrackTableAdded(tableName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tablesAdded, tableName)
}

// TrackClusterSetting records the original value of a cluster setting before modification.
// If the setting has already been tracked, it preserves the original value.
func (c *ClusterStateTracker) TrackClusterSetting(settingName, originalValue string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.clusterSettings[settingName]; !exists {
		c.clusterSettings[settingName] = originalValue
	}
}

// TrackFailureInjected records that a failure was injected.
func (c *ClusterStateTracker) TrackFailureInjected(failureID string, failureInfo FailureInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failuresInjected[failureID] = failureInfo
}

// UntrackFailureInjected removes a failure from tracking (e.g., if the test recovers it).
func (c *ClusterStateTracker) UntrackFailureInjected(failureID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.failuresInjected, failureID)
}

// TrackSchemaCreated records that a schema was created during the test.
func (c *ClusterStateTracker) TrackSchemaCreated(schemaName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.schemasCreated[schemaName] = struct{}{}
}

// UntrackSchemaCreated removes a schema from tracking.
func (c *ClusterStateTracker) UntrackSchemaCreated(schemaName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.schemasCreated, schemaName)
}

// TrackUserCreated records that a user was created during the test.
func (c *ClusterStateTracker) TrackUserCreated(username string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.usersCreated[username] = struct{}{}
}

// UntrackUserCreated removes a user from tracking.
func (c *ClusterStateTracker) UntrackUserCreated(username string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.usersCreated, username)
}

// TrackDatabaseCreated records that a database was created during the test.
func (c *ClusterStateTracker) TrackDatabaseCreated(dbName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.databasesCreated[dbName] = struct{}{}
}

// UntrackDatabaseCreated removes a database from tracking.
func (c *ClusterStateTracker) UntrackDatabaseCreated(dbName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.databasesCreated, dbName)
}

// GetTrackedState returns a copy of all tracked state for inspection or restoration.
func (c *ClusterStateTracker) GetTrackedState() (
	tables []string,
	settings map[string]string,
	failures map[string]FailureInfo,
	schemas []string,
	users []string,
	databases []string,
) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Copy tables
	for table := range c.tablesAdded {
		tables = append(tables, table)
	}

	// Copy settings
	settings = make(map[string]string)
	for setting, value := range c.clusterSettings {
		settings[setting] = value
	}

	// Copy failures
	failures = make(map[string]FailureInfo)
	for id, info := range c.failuresInjected {
		failures[id] = info
	}

	// Copy schemas
	for schema := range c.schemasCreated {
		schemas = append(schemas, schema)
	}

	// Copy users
	for user := range c.usersCreated {
		users = append(users, user)
	}

	// Copy databases
	for db := range c.databasesCreated {
		databases = append(databases, db)
	}

	return
}

// RestoreClusterState attempts to restore the cluster to its original state by
// reversing all tracked changes. This should be called on test failure.
func (c *ClusterStateTracker) RestoreClusterState(ctx context.Context, helper *Helper, l *logger.Logger) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	l.Printf("Starting cluster state restoration...")

	// Restore cluster settings to original values
	for setting, originalValue := range c.clusterSettings {
		if err := c.restoreClusterSetting(ctx, helper, l, setting, originalValue); err != nil {
			l.Printf("Failed to restore cluster setting %s: %v", setting, err)
			// Continue with other restorations
		}
	}

	// Drop tables that were created
	for table := range c.tablesAdded {
		if err := c.dropTable(ctx, helper, l, table); err != nil {
			l.Printf("Failed to drop table %s: %v", table, err)
			// Continue with other restorations
		}
	}

	// Drop schemas that were created
	for schema := range c.schemasCreated {
		if err := c.dropSchema(ctx, helper, l, schema); err != nil {
			l.Printf("Failed to drop schema %s: %v", schema, err)
			// Continue with other restorations
		}
	}

	// Drop users that were created
	for user := range c.usersCreated {
		if err := c.dropUser(ctx, helper, l, user); err != nil {
			l.Printf("Failed to drop user %s: %v", user, err)
			// Continue with other restorations
		}
	}

	// Drop databases that were created
	for db := range c.databasesCreated {
		if err := c.dropDatabase(ctx, helper, l, db); err != nil {
			l.Printf("Failed to drop database %s: %v", db, err)
			// Continue with other restorations
		}
	}

	// Recover from failures that were injected
	for failureID, failureInfo := range c.failuresInjected {
		if err := c.recoverFromFailure(ctx, helper, l, failureID, failureInfo); err != nil {
			l.Printf("Failed to recover from failure %s: %v", failureID, err)
			// Continue with other restorations
		}
	}

	l.Printf("Cluster state restoration completed")
	return nil
}

// restoreClusterSetting restores a cluster setting to its original value.
func (c *ClusterStateTracker) restoreClusterSetting(ctx context.Context, helper *Helper, l *logger.Logger, setting, originalValue string) error {
	l.Printf("Restoring cluster setting %s to original value: %s", setting, originalValue)
	return helper.Exec(fmt.Sprintf("SET CLUSTER SETTING %s = '%s'", setting, originalValue))
}

// dropTable drops a table that was created during the test.
func (c *ClusterStateTracker) dropTable(ctx context.Context, helper *Helper, l *logger.Logger, table string) error {
	l.Printf("Dropping table %s", table)
	return helper.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
}

// dropSchema drops a schema that was created during the test.
func (c *ClusterStateTracker) dropSchema(ctx context.Context, helper *Helper, l *logger.Logger, schema string) error {
	l.Printf("Dropping schema %s", schema)
	return helper.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema))
}

// dropUser drops a user that was created during the test.
func (c *ClusterStateTracker) dropUser(ctx context.Context, helper *Helper, l *logger.Logger, user string) error {
	l.Printf("Dropping user %s", user)
	return helper.Exec(fmt.Sprintf("DROP USER IF EXISTS %s", user))
}

// dropDatabase drops a database that was created during the test.
func (c *ClusterStateTracker) dropDatabase(ctx context.Context, helper *Helper, l *logger.Logger, db string) error {
	l.Printf("Dropping database %s", db)
	return helper.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s CASCADE", db))
}

// recoverFromFailure attempts to recover from an injected failure.
func (c *ClusterStateTracker) recoverFromFailure(ctx context.Context, helper *Helper, l *logger.Logger, failureID string, failureInfo FailureInfo) error {
	l.Printf("Recovering from failure %s (%s): %s", failureID, failureInfo.Type, failureInfo.Description)
	
	// Recovery logic would depend on the failure type and recovery info
	// This is a placeholder for failure-specific recovery logic
	switch failureInfo.Type {
	case "network_partition":
		// Restore network connectivity
		return c.recoverNetworkPartition(ctx, helper, l, failureInfo.RecoveryInfo)
	case "node_crash":
		// Restart crashed nodes
		return c.recoverNodeCrash(ctx, helper, l, failureInfo.RecoveryInfo)
	default:
		l.Printf("Unknown failure type %s, skipping recovery", failureInfo.Type)
		return nil
	}
}

// recoverNetworkPartition recovers from a network partition failure.
func (c *ClusterStateTracker) recoverNetworkPartition(ctx context.Context, helper *Helper, l *logger.Logger, recoveryInfo map[string]interface{}) error {
	// Implementation would depend on how network partitions are created
	// This is a placeholder for the actual recovery logic
	l.Printf("Recovering from network partition...")
	return nil
}

// recoverNodeCrash recovers from a node crash failure.
func (c *ClusterStateTracker) recoverNodeCrash(ctx context.Context, helper *Helper, l *logger.Logger, recoveryInfo map[string]interface{}) error {
	// Implementation would depend on how node crashes are simulated
	// This is a placeholder for the actual recovery logic
	l.Printf("Recovering from node crash...")
	return nil
}

// Runner executes a generated test plan from the modular framework.
type Runner struct {
	testPlan     *TestPlan
	helper       *Helper
	stateTracker *ClusterStateTracker
}

// NewRunner creates a new runner for executing a test plan.
func NewRunner(testPlan *TestPlan) *Runner {
	return &Runner{
		testPlan:     testPlan,
		helper:       &Helper{rng: testPlan.rng},
		stateTracker: NewClusterStateTracker(),
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
			return r.stageError(ctx, err, stageName, stageLogger)
		}

		duration := time.Since(start)
		prefix := fmt.Sprintf("FINISHED [%s]", duration)
		r.logStage(prefix, stageName, stageLogger)
	}

	l.Printf("All stages completed successfully")
	return nil
}

// executeStage executes all steps within a single stage.
func (r *Runner) executeStage(ctx context.Context, l *logger.Logger, stagePlan stagePlan) error {
	for _, step := range stagePlan.steps {
		stepLogger, err := r.loggerForStep(l, step.id, step.Description())
		if err != nil {
			return fmt.Errorf("failed to create step logger: %w", err)
		}

		r.logStep("STARTING", step.id, step.Description(), stepLogger)
		start := time.Now()

		err = step.Run(ctx, stepLogger, r.helper)
		if err != nil {
			return r.stepError(ctx, err, step.id, step.Description(), stepLogger)
		}

		duration := time.Since(start)
		prefix := fmt.Sprintf("FINISHED [%s]", duration)
		r.logStep(prefix, step.id, step.Description(), stepLogger)
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

	// Attempt to restore cluster state before failing
	l.Printf("Attempting to restore cluster state due to step failure...")
	if restoreErr := r.stateTracker.RestoreClusterState(ctx, r.helper, l); restoreErr != nil {
		l.Printf("Failed to restore cluster state: %v", restoreErr)
	}

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

	// Attempt to restore cluster state before failing
	l.Printf("Attempting to restore cluster state due to stage failure...")
	if restoreErr := r.stateTracker.RestoreClusterState(ctx, r.helper, l); restoreErr != nil {
		l.Printf("Failed to restore cluster state: %v", restoreErr)
	}

	// Rename the log file to indicate failure
	if renameErr := r.renameFailedLogger(l); renameErr != nil {
		l.Printf("could not rename failed stage logger: %v", renameErr)
	}

	return stageErr
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
