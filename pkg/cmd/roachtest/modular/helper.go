package modular

import (
	"context"
	gosql "database/sql"
	"fmt"
	"math/rand"
	"path"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/task"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/errors"
)

const (
	logPrefix = "modular-test"
)

// Helper provides utilities for modular test steps.
type Helper struct {
	defaultService *Service
	rng            *rand.Rand
	// taskCount keeps track of the number of tasks started with `helper.Go()`.
	// The counter is used to generate unique log file names.
	taskCount    int64
	cluster      cluster.Cluster
	logger       *logger.Logger
	background   task.Manager
	ctx          context.Context
	stateTracker *ClusterStateTracker
}

func (h *Helper) AvailableNodes() option.NodeListOption {
	return h.defaultService.AvailableNodes()
}

func (h *Helper) RandomAvailableNode() int {
	nodes := h.AvailableNodes()
	return nodes.SeededRandNode(h.rng)[0]
}

// Connect returns a connection pool to the given node using the default service.
func (h *Helper) Connect(node int) *gosql.DB {
	return h.defaultService.Connect(node)
}

// RandomDB returns a connection pool to a random node using the default service.
func (h *Helper) RandomDB() (int, *gosql.DB) {
	return h.defaultService.RandomDB(h.rng)
}

// Query performs `db.QueryContext` on a randomly picked database node. The
// query and the node picked are logged in the logs of the step that calls this
// function.
func (h *Helper) Query(query string, args ...interface{}) (*gosql.Rows, error) {
	return h.defaultService.Query(h.rng, query, args...)
}

// QueryRow performs `db.QueryRowContext` on a randomly picked
// database node. The query and the node picked are logged in the logs
// of the step that calls this function.
func (h *Helper) QueryRow(query string, args ...interface{}) *gosql.Row {
	return h.defaultService.QueryRow(h.rng, query, args...)
}

// Exec performs `db.ExecContext` on a randomly picked database node.
// The query and the node picked are logged in the logs of the step
// that calls this function.
func (h *Helper) Exec(query string, args ...interface{}) error {
	return h.defaultService.Exec(h.rng, query, args...)
}

// ExecWithGateway is like Exec, but allows the caller to specify the
// set of nodes that should be used as gateway.
func (h *Helper) ExecWithGateway(
	nodes option.NodeListOption, query string, args ...interface{},
) error {
	return h.defaultService.ExecWithGateway(h.rng, nodes, query, args...)
}

// StateTracker returns the cluster state tracker for recording state changes.
func (h *Helper) StateTracker() *ClusterStateTracker {
	return h.stateTracker
}

// CreateTableWithTracking creates a table and automatically tracks it for cleanup.
func (h *Helper) CreateTableWithTracking(query string, args ...interface{}) error {
	if err := h.Exec(query, args...); err != nil {
		return err
	}
	
	// Extract table name from CREATE TABLE query
	tableName := extractTableNameFromQuery(query)
	if tableName != "" {
		h.stateTracker.TrackTableAdded(tableName)
	}
	
	return nil
}

// DropTableWithUntracking drops a table and removes it from tracking.
func (h *Helper) DropTableWithUntracking(tableName string) error {
	if err := h.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName)); err != nil {
		return err
	}
	
	h.stateTracker.UntrackTableAdded(tableName)
	return nil
}

// SetClusterSettingWithTracking sets a cluster setting and tracks the original value.
func (h *Helper) SetClusterSettingWithTracking(settingName, newValue string) error {
	// Get the current value first
	row := h.QueryRow("SHOW CLUSTER SETTING $1", settingName)
	var currentValue string
	if err := row.Scan(&currentValue); err != nil {
		// If we can't get the current value, use empty string as fallback
		currentValue = ""
	}
	
	// Track the original value
	h.stateTracker.TrackClusterSetting(settingName, currentValue)
	
	// Set the new value
	return h.Exec(fmt.Sprintf("SET CLUSTER SETTING %s = '%s'", settingName, newValue))
}

// InjectFailureWithTracking injects a failure and tracks it for recovery.
func (h *Helper) InjectFailureWithTracking(failureID string, failureType, description string, recoveryInfo map[string]interface{}) error {
	failureInfo := FailureInfo{
		Type:         failureType,
		Description:  description,
		RecoveryInfo: recoveryInfo,
	}
	
	h.stateTracker.TrackFailureInjected(failureID, failureInfo)
	
	// The actual failure injection would happen here
	// This is a placeholder for the specific failure injection logic
	h.logger.Printf("Injecting failure %s (%s): %s", failureID, failureType, description)
	
	return nil
}

// RecoverFailureWithUntracking recovers from a failure and removes it from tracking.
func (h *Helper) RecoverFailureWithUntracking(failureID string) error {
	// The actual failure recovery would happen here
	// This is a placeholder for the specific failure recovery logic
	h.logger.Printf("Recovering from failure %s", failureID)
	
	h.stateTracker.UntrackFailureInjected(failureID)
	return nil
}

// CreateSchemaWithTracking creates a schema and automatically tracks it for cleanup.
func (h *Helper) CreateSchemaWithTracking(schemaName string) error {
	if err := h.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName)); err != nil {
		return err
	}
	
	h.stateTracker.TrackSchemaCreated(schemaName)
	return nil
}

// DropSchemaWithUntracking drops a schema and removes it from tracking.
func (h *Helper) DropSchemaWithUntracking(schemaName string) error {
	if err := h.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName)); err != nil {
		return err
	}
	
	h.stateTracker.UntrackSchemaCreated(schemaName)
	return nil
}

// CreateUserWithTracking creates a user and automatically tracks it for cleanup.
func (h *Helper) CreateUserWithTracking(username, password string) error {
	if err := h.Exec(fmt.Sprintf("CREATE USER %s WITH PASSWORD '%s'", username, password)); err != nil {
		return err
	}
	
	h.stateTracker.TrackUserCreated(username)
	return nil
}

// DropUserWithUntracking drops a user and removes it from tracking.
func (h *Helper) DropUserWithUntracking(username string) error {
	if err := h.Exec(fmt.Sprintf("DROP USER IF EXISTS %s", username)); err != nil {
		return err
	}
	
	h.stateTracker.UntrackUserCreated(username)
	return nil
}

// CreateDatabaseWithTracking creates a database and automatically tracks it for cleanup.
func (h *Helper) CreateDatabaseWithTracking(dbName string) error {
	if err := h.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", dbName)); err != nil {
		return err
	}
	
	h.stateTracker.TrackDatabaseCreated(dbName)
	return nil
}

// DropDatabaseWithUntracking drops a database and removes it from tracking.
func (h *Helper) DropDatabaseWithUntracking(dbName string) error {
	if err := h.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s CASCADE", dbName)); err != nil {
		return err
	}
	
	h.stateTracker.UntrackDatabaseCreated(dbName)
	return nil
}

// defaultTaskOptions returns the default options that are passed to all tasks
// started by the helper.
func (h *Helper) defaultTaskOptions() []task.Option {
	loggerFuncOpt := task.LoggerFunc(func(name string) (*logger.Logger, error) {
		bgLogger, err := h.loggerFor(name)
		if err != nil {
			return nil, fmt.Errorf("failed to create logger for task function %q: %w", name, err)
		}
		return bgLogger, nil
	})
	panicOpt := task.PanicHandler(func(_ context.Context, name string, l *logger.Logger, r interface{}) error {
		l.Printf("panic in task function %s: %v", name, r)
		return fmt.Errorf("panic in task function %s: %v", name, r)
	})
	errHandlerOpt := task.ErrorHandler(func(ctx context.Context, name string, l *logger.Logger, err error) error {
		if err != nil {
			if task.IsContextCanceled(ctx) {
				return err
			}
			l.Printf("error in task function %s: %v", name, err)
			return errors.Wrapf(err, "error in task function %s", name)
		}
		return nil
	})
	return []task.Option{loggerFuncOpt, panicOpt, errHandlerOpt}
}

// GoWithCancel implements the Tasker interface.
func (h *Helper) GoWithCancel(fn task.Func, opts ...task.Option) context.CancelFunc {
	return h.background.GoWithCancel(
		fn, task.OptionList(h.defaultTaskOptions()...), task.OptionList(opts...),
	)
}

// Go implements the Tasker interface.
func (h *Helper) Go(fn task.Func, opts ...task.Option) {
	h.GoWithCancel(fn, opts...)
}

// NewGroup implements the Group interface.
func (h *Helper) NewGroup(opts ...task.Option) task.Group {
	return h.background.NewGroup(task.OptionList(h.defaultTaskOptions()...), task.OptionList(opts...))
}

// NewErrorGroup implements the Group interface.
func (h *Helper) NewErrorGroup(opts ...task.Option) task.ErrorGroup {
	return h.background.NewErrorGroup(task.OptionList(h.defaultTaskOptions()...), task.OptionList(opts...))
}

// GoCommand has the same semantics of `GoWithCancel()`; the command passed will
// run and the test will fail if the command is not successful. The task name is
// derived from the command passed.
func (h *Helper) GoCommand(cmd string, nodes option.NodeListOption) context.CancelFunc {
	desc := fmt.Sprintf("run command: %q", cmd)
	return h.GoWithCancel(func(ctx context.Context, l *logger.Logger) error {
		l.Printf("running command `%s` on nodes %v in a task", cmd, nodes)
		return h.cluster.RunE(ctx, option.WithNodes(nodes), cmd)
	}, task.Name(desc))
}

// loggerFor creates a logger instance to be used by task functions (created by
// calling `Go` on the helper instance). It is similar to the logger instances
// created for mixed-version steps, but with the `task_` prefix.
func (h *Helper) loggerFor(name string) (*logger.Logger, error) {
	atomic.AddInt64(&h.taskCount, 1)

	fileName := invalidChars.ReplaceAllString(strings.ToLower(name), "")
	fileName = fmt.Sprintf("task_%s_%d", fileName, h.taskCount)
	fileName = path.Join(logPrefix, fileName)

	return h.logger.ChildLogger(fileName)
}

// extractTableNameFromQuery attempts to extract the table name from a CREATE TABLE query.
// This is a simple regex-based extraction and may not handle all edge cases.
func extractTableNameFromQuery(query string) string {
	// Match CREATE TABLE statements with optional schema qualification
	// This regex looks for "CREATE TABLE [IF NOT EXISTS] [schema.]table_name"
	re := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)?)`)
	matches := re.FindStringSubmatch(query)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// Service implements helper functions on behalf of a specific
// service. Internal fields are provided by the testRunner struct,
// allowing us to connect to a specific node and check live the test
// runner's view of cluster versions, etc.
type Service struct {
	name       string
	ctx        context.Context
	connFunc   func(int) *gosql.DB
	stepLogger *logger.Logger
	monitor    test.Monitor
	cluster    cluster.Cluster
	nodes      option.NodeListOption
}

func (s *Service) AvailableNodes() option.NodeListOption {
	return s.monitor.AvailableNodes(s.name).Intersect(s.nodes)
}

func (s *Service) RandomAvailableNode(rng *rand.Rand) int {
	nodes := s.AvailableNodes()
	return nodes.SeededRandNode(rng)[0]
}

// Connect returns a connection pool to the given node. Note that
// these connection pools are managed by the framework and therefore
// *must not* be closed. They are closed automatically when the test
// finishes.
func (s *Service) Connect(node int) *gosql.DB {
	return s.connFunc(node)
}

// RandomDB returns a connection pool to a random node in the
// cluster. Do *not* call `Close` on the pool returned (see comment on
// `Connect` function).
func (s *Service) RandomDB(rng *rand.Rand) (int, *gosql.DB) {
	node := s.RandomAvailableNode(rng)
	return node, s.Connect(node)
}

// prepareQuery returns a connection to one of the available nodes in `nodes`
// provided and logs the query and gateway node in the step's log file. Called
// before the query is actually performed.
func (s *Service) prepareQuery(
	rng *rand.Rand, nodes option.NodeListOption, query string, args ...any,
) (*gosql.DB, error) {
	availableNodes := s.AvailableNodes().Intersect(nodes)
	if len(availableNodes) == 0 {
		return nil, errors.Newf(
			"no available nodes in the intersection of %s and %s",
			s.AvailableNodes(), nodes,
		)
	}
	node := availableNodes.SeededRandNode(rng)[0]
	db := s.Connect(node)

	logSQL(
		s.stepLogger, node, s.name, query, args...,
	)

	return db, nil
}

func (s *Service) Query(rng *rand.Rand, query string, args ...interface{}) (*gosql.Rows, error) {
	db, err := s.prepareQuery(rng, s.nodes, query, args...)
	handleInternalError(err)
	return db.QueryContext(s.ctx, query, args...)
}

func (s *Service) QueryRow(rng *rand.Rand, query string, args ...interface{}) *gosql.Row {
	db, err := s.prepareQuery(rng, s.nodes, query, args...)
	handleInternalError(err)
	return db.QueryRowContext(s.ctx, query, args...)
}

func (s *Service) Exec(rng *rand.Rand, query string, args ...interface{}) error {
	return s.ExecWithGateway(rng, s.nodes, query, args...)
}

func (s *Service) ExecWithGateway(
	rng *rand.Rand, nodes option.NodeListOption, query string, args ...interface{},
) error {
	db, err := s.prepareQuery(rng, nodes, query, args...)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(s.ctx, query, args...)
	return err
}

// logSQL standardizes the logging when a SQL statement or query is
// run using one of the Helper methods. It includes the node used as
// gateway for ease of debugging.
func logSQL(
	l *logger.Logger,
	node int,
	serviceName string,
	stmt string,
	args ...interface{},
) {
	var lines []string
	addLine := func(format string, args ...interface{}) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}

	addLine("running SQL")
	addLine("Node:      %d", node)
	addLine("Service:   %s", serviceName)
	addLine("Statement: %s", stmt)
	addLine("Arguments: %v", args)

	l.Printf("%s", strings.Join(lines, "\n"))
}

// handleInternalError can be used when the caller does not expect any
// errors from a function call. If the error value provided is not
// nil, we'll panic with an internal error message.
func handleInternalError(err error) {
	if err == nil {
		return
	}

	panic(fmt.Errorf("modular internal error: %w", err))
}
