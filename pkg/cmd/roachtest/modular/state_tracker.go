package modular

import (
	gosql "database/sql"
	"sync"

	"fmt"
	"github.com/cockroachdb/cockroach/pkg/roachprod/failureinjection/failures"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"strings"
	"time"
)

// ClusterStateTracker tracks cluster state changes made during test execution
// to allow restoration to the original state on failure.
type ClusterStateTracker struct {
	mu sync.RWMutex

	// tablesAdded tracks tables created during the test
	// Key: table name (e.g., "mydb.mytable"), Value: empty struct
	tablesAdded map[string]struct{}

	// clusterSettings tracks cluster settings that were modified using atomic operations
	// Key: setting name, Value: original value (before any test modifications)
	clusterSettings sync.Map

	// zoneConfigs tracks zone configurations that were modified using atomic operations
	// Key: range name (e.g., "default", "system"), Value: original zone config
	zoneConfigs sync.Map

	// failuresInjected tracks failure injection operations
	failuresInjected map[string]*failures.Failer

	// schemasCreated tracks schemas created during the test
	schemasCreated map[string]struct{}

	// usersCreated tracks users created during the test
	usersCreated map[string]struct{}

	// databasesCreated tracks databases created during the test
	databasesCreated map[string]struct{}

	// debugLogger logs debug information for tracking operations
	debugLogger *logger.Logger
}

// NewClusterStateTracker creates a new cluster state tracker with initialized maps.
func NewClusterStateTracker(debugLogger *logger.Logger) *ClusterStateTracker {
	return &ClusterStateTracker{
		tablesAdded: make(map[string]struct{}),
		// clusterSettings is a sync.Map, no initialization needed
		failuresInjected: make(map[string]*failures.Failer),
		schemasCreated:   make(map[string]struct{}),
		usersCreated:     make(map[string]struct{}),
		databasesCreated: make(map[string]struct{}),
		debugLogger:      debugLogger,
	}
}

func (c *ClusterStateTracker) NewTableName(namePrefix string) string {
	tableName := fmt.Sprintf("%s_%d", namePrefix, time.Now().Unix())
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tablesAdded[tableName] = struct{}{}
	return tableName
}

func (c *ClusterStateTracker) NewUsername(namePrefix string) string {
	username := fmt.Sprintf("%s_%d", namePrefix, time.Now().Unix())
	c.mu.Lock()
	defer c.mu.Unlock()
	c.usersCreated[username] = struct{}{}
	return username
}

func (c *ClusterStateTracker) NewDatabaseName(namePrefix string) string {
	dbName := fmt.Sprintf("%s_%d", namePrefix, time.Now().Unix())
	c.mu.Lock()
	defer c.mu.Unlock()
	c.databasesCreated[dbName] = struct{}{}
	return dbName
}

func (c *ClusterStateTracker) NewSchemaName(namePrefix string) string {
	schemaName := fmt.Sprintf("%s_%d", namePrefix, time.Now().Unix())
	c.mu.Lock()
	defer c.mu.Unlock()
	c.schemasCreated[schemaName] = struct{}{}
	return schemaName
}

// maybeTrackClusterSetting atomically checks if a cluster setting is tracked and tracks it if not.
// If the setting is not tracked, it uses the provided connFunc to get a database connection and query the current value.
// Returns an error if the current value cannot be queried.
func (c *ClusterStateTracker) maybeTrackClusterSetting(settingName string, connFunc func() *gosql.DB) error {
	// First, try to load the existing value
	if _, exists := c.clusterSettings.Load(settingName); exists {
		return nil // Already tracked
	}

	// Get database connection only when needed
	db := connFunc()
	defer db.Close()

	// Query the current value directly from the database outside of any locks
	query := "SHOW CLUSTER SETTING $1"
	row := db.QueryRow(query, settingName)
	var currentValue string
	if err := row.Scan(&currentValue); err != nil {
		// Log and return the error - we need the original value to track properly
		c.debugLogger.Printf("Failed to scan cluster setting '%s': %v", settingName, err)
		return fmt.Errorf("failed to query current value for cluster setting '%s': %w", settingName, err)
	} else {
		// Log the scan result for debugging
		c.debugLogger.Printf("Cluster Setting Scan Result - Setting: %s, Query: %s, Scanned Value: %s", settingName, query, currentValue)
	}

	// Attempt to store it atomically - LoadOrStore returns the actual stored value
	// and whether the value was loaded (true) or stored (false)
	_, _ = c.clusterSettings.LoadOrStore(settingName, currentValue)
	return nil
}

// TrackZoneConfig records the original value of a zone configuration before modification.
// If the zone config has already been tracked, it preserves the original value.
func (c *ClusterStateTracker) TrackZoneConfig(rangeName, originalConfig string) {
	// Use LoadOrStore for atomic operation - only stores if key doesn't exist
	c.zoneConfigs.LoadOrStore(rangeName, originalConfig)
}

// IsZoneConfigTracked returns true if the zone configuration is already being tracked.
func (c *ClusterStateTracker) IsZoneConfigTracked(rangeName string) bool {
	_, exists := c.zoneConfigs.Load(rangeName)
	return exists
}

// maybeTrackZoneConfig atomically checks if a zone config is tracked and tracks it if not.
// If the zone config is not tracked, it uses the provided connFunc to get a database connection and query the current value.
// Returns an error if the current config cannot be queried.
func (c *ClusterStateTracker) maybeTrackZoneConfig(rangeName string, connFunc func() *gosql.DB) error {
	// First, try to load the existing value
	if _, exists := c.zoneConfigs.Load(rangeName); exists {
		return nil // Already tracked
	}

	// Get database connection only when needed
	db := connFunc()
	defer db.Close()

	// Query the current zone configuration directly from the database
	query := "SHOW ZONE CONFIGURATION FOR RANGE $1"
	row := db.QueryRow(query, rangeName)
	var originalConfig string
	if err := row.Scan(&originalConfig); err != nil {
		// Log and return the error - we need the original config to track properly
		c.debugLogger.Printf("Failed to scan zone configuration for range '%s': %v", rangeName, err)
		return fmt.Errorf("failed to query current zone config for range '%s': %w", rangeName, err)
	} else {
		// Log the scan result for debugging
		c.debugLogger.Printf("Zone Config Scan Result - Range: %s, Query: %s, Scanned Config: %s", rangeName, query, originalConfig)
	}

	// Attempt to store it atomically - LoadOrStore returns the actual stored value
	// and whether the value was loaded (true) or stored (false)
	_, _ = c.zoneConfigs.LoadOrStore(rangeName, originalConfig)
	return nil
}

// LogClusterStateDebug logs the current cluster state for debugging purposes.
func (c *ClusterStateTracker) LogClusterStateDebug(context string) {
	c.debugLogger.Printf("Cluster State %s:", context)
	stateOutput := c.PrintTrackedState()
	c.debugLogger.Printf("%s", stateOutput)
}

// TrackFailureInjected records that a failure was injected.
func (c *ClusterStateTracker) TrackFailureInjected(failureID string, failer *failures.Failer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failuresInjected[failureID] = failer
}

// TrackSchemaCreated records that a schema was created during the test.
func (c *ClusterStateTracker) TrackSchemaCreated(schemaName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.schemasCreated[schemaName] = struct{}{}
}

// TrackUserCreated records that a user was created during the test.
func (c *ClusterStateTracker) TrackUserCreated(username string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.usersCreated[username] = struct{}{}
}

// TrackDatabaseCreated records that a database was created during the test.
func (c *ClusterStateTracker) TrackDatabaseCreated(dbName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.databasesCreated[dbName] = struct{}{}
}

// GetTrackedState returns a copy of all tracked state for inspection or restoration.
func (c *ClusterStateTracker) GetTrackedState() (
	tables []string,
	settings map[string]string,
	zoneConfigs map[string]string,
	failureMap map[string]*failures.Failer,
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

	// Copy settings from sync.Map
	settings = make(map[string]string)
	c.clusterSettings.Range(func(key, value interface{}) bool {
		settings[key.(string)] = value.(string)
		return true // continue iteration
	})

	// Copy zone configs from sync.Map
	zoneConfigs = make(map[string]string)
	c.zoneConfigs.Range(func(key, value interface{}) bool {
		zoneConfigs[key.(string)] = value.(string)
		return true // continue iteration
	})

	// Copy failures
	failureMap = make(map[string]*failures.Failer)
	for id, failer := range c.failuresInjected {
		failureMap[id] = failer
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

// PrintTrackedState returns a formatted string representation of all tracked cluster state.
// This is useful for debugging and understanding what changes have been made during test execution.
func (c *ClusterStateTracker) PrintTrackedState() string {
	tables, settings, zoneConfigs, failureMap, schemas, users, databases := c.GetTrackedState()

	var output []string
	output = append(output, "=== TRACKED CLUSTER STATE ===")

	// Count total items being tracked
	totalItems := len(tables) + len(settings) + len(zoneConfigs) + len(failureMap) + len(schemas) + len(users) + len(databases)
	if totalItems == 0 {
		output = append(output, "No cluster state changes tracked")
		output = append(output, "=============================")
		return strings.Join(output, "\n")
	}

	output = append(output, fmt.Sprintf("Total tracked items: %d", totalItems))
	output = append(output, "")

	// Tables created
	if len(tables) > 0 {
		output = append(output, fmt.Sprintf("Tables created (%d):", len(tables)))
		for _, table := range tables {
			output = append(output, fmt.Sprintf("  - %s", table))
		}
		output = append(output, "")
	}

	// Databases created
	if len(databases) > 0 {
		output = append(output, fmt.Sprintf("Databases created (%d):", len(databases)))
		for _, db := range databases {
			output = append(output, fmt.Sprintf("  - %s", db))
		}
		output = append(output, "")
	}

	// Schemas created
	if len(schemas) > 0 {
		output = append(output, fmt.Sprintf("Schemas created (%d):", len(schemas)))
		for _, schema := range schemas {
			output = append(output, fmt.Sprintf("  - %s", schema))
		}
		output = append(output, "")
	}

	// Users created
	if len(users) > 0 {
		output = append(output, fmt.Sprintf("Users created (%d):", len(users)))
		for _, user := range users {
			output = append(output, fmt.Sprintf("  - %s", user))
		}
		output = append(output, "")
	}

	// Cluster settings modified
	if len(settings) > 0 {
		output = append(output, fmt.Sprintf("Cluster settings modified (%d):", len(settings)))
		for setting, originalValue := range settings {
			output = append(output, fmt.Sprintf("  - %s (original: %s)", setting, originalValue))
		}
		output = append(output, "")
	}

	// Zone configurations modified
	if len(zoneConfigs) > 0 {
		output = append(output, fmt.Sprintf("Zone configurations modified (%d):", len(zoneConfigs)))
		for rangeName, originalConfig := range zoneConfigs {
			// Truncate long zone configs for readability
			displayConfig := originalConfig
			if len(displayConfig) > 50 {
				displayConfig = displayConfig[:50] + "..."
			}
			output = append(output, fmt.Sprintf("  - %s (original: %s)", rangeName, displayConfig))
		}
		output = append(output, "")
	}

	// Failures injected
	if len(failureMap) > 0 {
		output = append(output, fmt.Sprintf("Failures injected (%d):", len(failureMap)))
		for failureID, failer := range failureMap {
			description := failer.Description()
			// Truncate long descriptions for readability
			if len(description) > 60 {
				description = description[:60] + "..."
			}
			output = append(output, fmt.Sprintf("  - %s: %s", failureID, description))
		}
		output = append(output, "")
	}

	output = append(output, "=============================")
	return strings.Join(output, "\n")
}
