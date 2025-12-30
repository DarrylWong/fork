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
	modular.RegisterOperation(AddRandomIndex())
	modular.RegisterOperation(AddRandomIndexDynamic())
	modular.RegisterOperation(AddRandomColumnDynamic())

	// Workload operations
	modular.RegisterOperation(InspectTable())
	modular.RegisterOperation(InspectTableDynamic())

	// Node operations
	modular.RegisterOperation(NodeRestart())

	// Replication operations
	modular.RegisterOperation(ReplicationFactorCycle())

	// Note: TPCC, BackupRestore, and other operations that require parameters
	// cannot be pre-registered since they need cluster or configuration info.
	// The scheduler will need to handle these specially or we need to add
	// parameterless versions.
}
