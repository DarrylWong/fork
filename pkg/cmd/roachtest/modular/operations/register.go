// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package operations

import (
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
)

// init registers all modular operations to the global registry.
// This allows the modular scheduler to discover and use these operations.
func init() {
	// Register all operations by calling their factory functions
	// and registering the created operations with unique names

	// Schema operations
	modular.RegisterOperation(AddRandomIndexDynamic())
	modular.RegisterOperation(AddRandomColumnDynamic())

	// Workload operations
	modular.RegisterOperation(InspectTableDynamic())

	// Node operations
	modular.RegisterOperation(NodeRestart())

	// Backup/Restore operations
	modular.RegisterOperation(BackupRestoreDynamic())
	modular.RegisterOperation(BackupRestoreDatabaseDynamic())

	// Cluster setting operations
	modular.RegisterOperation(ChangeClusterSettingDynamic(ClusterSettingOptions{}))

	// Replication operations
	// Disabled for now, since its buggy and takes too long when it works.
	// Its not properly finding user created tables.
	// modular.RegisterOperation(ReplicationFactorCycle())
}
