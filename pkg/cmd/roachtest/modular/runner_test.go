package modular

import (
	"testing"
)

func TestClusterStateTracker(t *testing.T) {
	tracker := NewClusterStateTracker()

	// Test table tracking
	tracker.TrackTableAdded("test_table")
	tracker.TrackTableAdded("mydb.another_table")
	
	// Test untracking
	tracker.UntrackTableAdded("test_table")

	// Test cluster setting tracking
	tracker.TrackClusterSetting("sql.trace.log_statement_execute", "false")
	tracker.TrackClusterSetting("sql.trace.log_statement_execute", "true") // Should preserve original
	
	// Test failure tracking
	failureInfo := FailureInfo{
		Type:        "network_partition",
		Description: "Simulated network partition between nodes 1 and 2",
		RecoveryInfo: map[string]interface{}{
			"affected_nodes": []int{1, 2},
		},
	}
	tracker.TrackFailureInjected("failure_1", failureInfo)
	
	// Test schema tracking
	tracker.TrackSchemaCreated("test_schema")
	
	// Test user tracking
	tracker.TrackUserCreated("test_user")
	
	// Test database tracking
	tracker.TrackDatabaseCreated("test_db")

	// Get tracked state and verify
	tables, settings, failures, schemas, users, databases := tracker.GetTrackedState()
	
	// Verify tables (test_table should be untracked)
	if len(tables) != 1 {
		t.Errorf("Expected 1 tracked table, got %d", len(tables))
	}
	if tables[0] != "mydb.another_table" {
		t.Errorf("Expected tracked table 'mydb.another_table', got '%s'", tables[0])
	}
	
	// Verify cluster settings (should preserve original value)
	if len(settings) != 1 {
		t.Errorf("Expected 1 tracked setting, got %d", len(settings))
	}
	if settings["sql.trace.log_statement_execute"] != "false" {
		t.Errorf("Expected original value 'false', got '%s'", settings["sql.trace.log_statement_execute"])
	}
	
	// Verify failures
	if len(failures) != 1 {
		t.Errorf("Expected 1 tracked failure, got %d", len(failures))
	}
	if failures["failure_1"].Type != "network_partition" {
		t.Errorf("Expected failure type 'network_partition', got '%s'", failures["failure_1"].Type)
	}
	
	// Verify schemas
	if len(schemas) != 1 {
		t.Errorf("Expected 1 tracked schema, got %d", len(schemas))
	}
	if schemas[0] != "test_schema" {
		t.Errorf("Expected tracked schema 'test_schema', got '%s'", schemas[0])
	}
	
	// Verify users
	if len(users) != 1 {
		t.Errorf("Expected 1 tracked user, got %d", len(users))
	}
	if users[0] != "test_user" {
		t.Errorf("Expected tracked user 'test_user', got '%s'", users[0])
	}
	
	// Verify databases
	if len(databases) != 1 {
		t.Errorf("Expected 1 tracked database, got %d", len(databases))
	}
	if databases[0] != "test_db" {
		t.Errorf("Expected tracked database 'test_db', got '%s'", databases[0])
	}
}

func TestExtractTableNameFromQuery(t *testing.T) {
	tests := []struct {
		query    string
		expected string
	}{
		{"CREATE TABLE test_table (id INT)", "test_table"},
		{"CREATE TABLE IF NOT EXISTS test_table (id INT)", "test_table"},
		{"create table mydb.test_table (id int)", "mydb.test_table"},
		{"CREATE TABLE IF NOT EXISTS myschema.test_table (id INT)", "myschema.test_table"},
		{"SELECT * FROM test_table", ""}, // Should not match
		{"CREATE INDEX ON test_table (id)", ""}, // Should not match
	}
	
	for _, test := range tests {
		result := extractTableNameFromQuery(test.query)
		if result != test.expected {
			t.Errorf("For query '%s', expected '%s', got '%s'", test.query, test.expected, result)
		}
	}
}