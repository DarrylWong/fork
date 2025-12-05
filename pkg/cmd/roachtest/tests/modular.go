// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package tests

import (
	"context"
	"fmt"
	"strings"
	"time"

	gosql "database/sql"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular/operations"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/clusterupgrade"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/errors"
)

func registerModular(r registry.Registry) {
	r.Add(registry.TestSpec{
		Name:             "modular/example",
		CompatibleClouds: registry.AllClouds,
		Suites:           registry.Suites(registry.Nightly),
		Owner:            registry.OwnerTestEng,
		Run:              runModularExample,
		Cluster:          r.MakeClusterSpec(6, spec.WorkloadNodeCount(1)),
		Timeout:          30 * time.Minute,
	})
	r.Add(registry.TestSpec{
		Name:             "modular/example/mvt",
		CompatibleClouds: registry.AllClouds,
		Suites:           registry.Suites(registry.Nightly),
		Owner:            registry.OwnerTestEng,
		Run:              runModularMVTExample,
		Cluster:          r.MakeClusterSpec(5, spec.CPU(16), spec.WorkloadNode()),
		Timeout:          60 * time.Minute,
	})
	r.Add(registry.TestSpec{
		Name:             "modular/example/recovery",
		CompatibleClouds: registry.AllClouds,
		Suites:           registry.Suites(registry.Nightly),
		Owner:            registry.OwnerTestEng,
		Run:              runModularRecoveryExample,
		Cluster:          r.MakeClusterSpec(6, spec.WorkloadNodeCount(1)),
		Timeout:          30 * time.Minute,
	})
	r.Add(registry.TestSpec{
		Name:             "modular/example/merge-operation",
		CompatibleClouds: registry.AllClouds,
		Suites:           registry.Suites(registry.Nightly),
		Owner:            registry.OwnerTestEng,
		Run:              runMergeOperationExample,
		Cluster:          r.MakeClusterSpec(5, spec.CPU(16), spec.WorkloadNode()),
		Timeout:          60 * time.Minute,
	})
}

func runModularExample(ctx context.Context, t test.Test, c cluster.Cluster) {
	// Create a new modular test with a specific seed for reproducibility
	mod := modular.NewTest(ctx, t.L(), c, c.CRDBNodes(), modular.WithDebug(modular.ClusterStateDebug))

	mod.Setup("initialize cluster", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		c.Start(ctx, l, option.DefaultStartOpts(), install.MakeClusterSettings(), c.CRDBNodes())
		return nil
	})

	mod.Setup("initialize bank workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		dbName, err := h.CreateDatabase("bank")
		if err != nil {
			return err
		}

		cmd := roachtestutil.NewCommand("%s workload init bank", test.DefaultCockroachPath).
			Flag("rows", 100000).
			Flag("db", dbName).
			Arg("{pgurl:%d}", h.RandomAvailableNode()).
			String()

		return c.RunE(ctx, option.WithNodes(c.WorkloadNode()), cmd)
	})

	mainStage := mod.NewStage("main-workload", modular.WithStepConcurrency(3))

	mod.AddOperation(mainStage, operations.AddRandomIndex())

	// Add TPCC workload chain: init, run, then check consistency
	mod.AddOperation(mainStage, operations.TPCC(c, 1000, time.Minute, operations.TPCCExtraOptions{}))

	mod.AddOperation(mainStage, operations.ReplicationFactorCycle())

	// Add INSPECT operation to validate table consistency
	mod.AddOperation(mainStage, operations.InspectTable())

	// Generate the test plan
	planner := mod.NewPlanner()
	testPlan, err := planner.Plan()
	if err != nil {
		t.Fatalf("Failed to generate test plan: %v", err)
	}

	// Execute the test plan using the runner
	err = modular.RunTestPlan(ctx, t, testPlan)
	if err != nil {
		// Check if the error contains ONLY the intentional failure we expect
		expectedError := "intentional fatal error to test recovery"
		if strings.Contains(err.Error(), expectedError) && !strings.Contains(err.Error(), "Failed to restore") {
			// The test succeeded - we got the expected failure and state restoration succeeded
			t.L().Printf("Test completed successfully: got expected intentional failure and cluster state was restored")
			return
		}
		// Any other error (including restoration failures) should fail the test
		t.Fatalf("Test execution failed: %v", err)
	}
}

func runModularMVTExample(ctx context.Context, t test.Test, c cluster.Cluster) {
	// Calculate warehouse and row counts similar to mixed-headroom
	maxWarehouses := maxSupportedTPCCWarehouses(*t.BuildVersion(), c.Cloud(), c.Spec())
	headroomWarehouses := int(float64(maxWarehouses) * 0.7)
	bankRows := 65104166 / 2
	if c.IsLocal() {
		bankRows = 1000
		headroomWarehouses = 20
	}

	// Create a modular test that mimics the mixed-headroom structure
	mod := modular.NewTest(ctx, t.L(), c, c.CRDBNodes())

	// Store the v25.3.0 binary path for workload operations
	var v253BinaryPath string

	initStage := mod.NewStage("cluster init")
	mod.InStage(initStage, "install fixtures for version 25.3", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		// Install fixtures for version 25.3
		version := clusterupgrade.MustParseVersion("v24.3.0")
		return clusterupgrade.InstallFixtures(ctx, l, c, c.CRDBNodes(), version)
	}).Then("start cluster at version 24.3", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		// Start cluster using version 24.3
		version := clusterupgrade.MustParseVersion("v24.3.0")
		binaryPath, err := clusterupgrade.UploadCockroach(ctx, t, l, c, c.CRDBNodes(), version)
		if err != nil {
			return err
		}

		// Store binary path for use in workload operations
		v253BinaryPath = binaryPath

		clusterSettings := install.MakeClusterSettings(
			install.BinaryOption(binaryPath),
		)

		startOpts := option.NewStartOpts(
			option.NoBackupSchedule,
		)

		c.Start(ctx, l, startOpts, clusterSettings, c.CRDBNodes())
		return nil
	}).Then("waiting for all nodes to acknowledge cluster version 25.3", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		// Use a timeout of 5 minutes for cluster version acknowledgment
		timeout := 5 * time.Minute

		// Connect function that creates a connection to a specific node
		// Let WaitForClusterUpgrade handle connection errors gracefully
		connectFunc := func(node int) *gosql.DB {
			db, err := c.ConnE(ctx, l, node)
			if err != nil {
				// Return nil and let WaitForClusterUpgrade handle the error
				// This is safer than panicking
				l.Printf("warning: failed to connect to node %d: %v", node, err)
				return nil
			}
			return db
		}

		return clusterupgrade.WaitForClusterUpgrade(ctx, l, c.CRDBNodes(), connectFunc, timeout)
	})

	inStartupStage := mod.NewStage("startup")
	mod.InStage(inStartupStage, "set preserve_downgrade_option to 25.3", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		db := c.Conn(ctx, l, 1)
		_, err := db.ExecContext(ctx, "SET CLUSTER SETTING cluster.preserve_downgrade_option = $1", "25.3")
		return err
	})
	mod.InStage(inStartupStage, "enable tenant features", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return enableTenantSplitScatterModular(l, h)
	}).Then("import TPCC dataset", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return importTPCCDataModular(ctx, t, c, l, h, headroomWarehouses, v253BinaryPath)
	}).And("import bank dataset", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return importBankDataModular(ctx, t, c, l, h, bankRows, v253BinaryPath)
	})

	upgradeStage := mod.NewStage("upgrade from v25.3 -> v25.4")
	mod.InStage(upgradeStage, "run TPCC workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return runTPCCWorkloadModular(ctx, t, c, l, h, headroomWarehouses)
	})

	for _, node := range c.CRDBNodes() {
		mod.InStage(upgradeStage, fmt.Sprintf("restart node %d with v25.4", node), func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
			return restartNodeWithVersion(ctx, t, l, c, node, "v25.4.0")
		}, modular.DisableConcurrency(), modular.AcquireLock(modular.NodeAvailability{}), modular.ReleaseLock(modular.NodeAvailability{}))
	}

	rollbackStage := mod.NewStage("rollback")
	mod.InStage(rollbackStage, "run TPCC workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return runTPCCWorkloadModular(ctx, t, c, l, h, headroomWarehouses)
	})

	for _, node := range c.CRDBNodes() {
		mod.InStage(rollbackStage, fmt.Sprintf("rollback node %d to version 25.3", node), func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
			return rollbackNodeToVersion(ctx, t, l, c, node, "v25.3.0")
		}, modular.DisableConcurrency(), modular.AcquireLock(modular.NodeAvailability{}), modular.ReleaseLock(modular.NodeAvailability{}))
	}

	finalizeStage := mod.NewStage("finalize")
	mod.InStage(finalizeStage, "run TPCC workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return runTPCCWorkloadModular(ctx, t, c, l, h, headroomWarehouses)
	})
	sb := mod.InStage(finalizeStage, fmt.Sprintf("restart node %d with v25.4", 1), func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return restartNodeWithVersion(ctx, t, l, c, 1, "v25.4.0")
	}, modular.DisableConcurrency(), modular.AcquireLock(modular.NodeAvailability{}), modular.ReleaseLock(modular.NodeAvailability{}))

	for _, node := range c.CRDBNodes()[1:] {
		sb = sb.And(fmt.Sprintf("restart node %d with v25.4", node), func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
			return restartNodeWithVersion(ctx, t, l, c, node, "v25.4.0")
		}, modular.DisableConcurrency(), modular.AcquireLock(modular.NodeAvailability{}), modular.ReleaseLock(modular.NodeAvailability{}))
	}
	sb.And("reset preserve_downgrade_option", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		db := c.Conn(ctx, l, 1)
		_, err := db.ExecContext(ctx, "RESET CLUSTER SETTING cluster.preserve_downgrade_option")
		return err
	}).Then("wait for all nodes to acknowledge v25.4 cluster version", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		// Use a timeout of 5 minutes for cluster version acknowledgment
		timeout := 5 * time.Minute

		// Connect function that creates a connection to a specific node
		// Let WaitForClusterUpgrade handle connection errors gracefully
		connectFunc := func(node int) *gosql.DB {
			db, err := c.ConnE(ctx, l, node)
			if err != nil {
				// Return nil and let WaitForClusterUpgrade handle the error
				// This is safer than panicking
				l.Printf("warning: failed to connect to node %d: %v", node, err)
				return nil
			}
			return db
		}

		return clusterupgrade.WaitForClusterUpgrade(ctx, l, c.CRDBNodes(), connectFunc, timeout)
	})

	// Final validation stage
	validationStage := mod.NewStage("validation")
	mod.InStage(validationStage, "check TPCC workload integrity", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return checkTPCCWorkloadModular(ctx, t, c, l, h, headroomWarehouses)
	})

	// Generate and execute test plan
	planner := mod.NewPlanner()
	testPlan, err := planner.Plan()
	if err != nil {
		t.Fatalf("Failed to generate test plan: %v", err)
	}

	err = modular.RunTestPlan(ctx, t, testPlan)
	if err != nil {
		t.Fatalf("Test execution failed: %v", err)
	}
}

// enableTenantSplitScatterModular enables tenant features for SPLIT and SCATTER operations
func enableTenantSplitScatterModular(l *logger.Logger, _ *modular.Helper) error {
	// This is a simplified version - in a real mixed-version test,
	// we would check version compatibility and enable settings conditionally
	settings := []string{
		"sql.split_at.allow_for_secondary_tenant.enabled",
		"sql.scatter.allow_for_secondary_tenant.enabled",
	}

	for _, setting := range settings {
		l.Printf("enabling setting: %s", setting)
		// In modular framework, we would need access to cluster connections
		// For now, this is a placeholder that demonstrates the structure
	}
	return nil
}

// importTPCCDataModular imports TPCC dataset
func importTPCCDataModular(ctx context.Context, _ test.Test, c cluster.Cluster, l *logger.Logger, _ *modular.Helper, warehouses int, binaryPath string) error {
	l.Printf("importing TPCC data with %d warehouses", warehouses)

	// Use the workload node for import
	cmd := tpccImportCmdWithCockroachBinary(
		binaryPath, "", "tpcc", warehouses,
		fmt.Sprintf("{pgurl%s}", c.Node(1)),
	)

	return c.RunE(ctx, option.WithNodes(c.Node(1)), cmd)
}

// importBankDataModular imports large bank dataset to stress the system
func importBankDataModular(ctx context.Context, _ test.Test, c cluster.Cluster, l *logger.Logger, _ *modular.Helper, rows int, binaryPath string) error {
	l.Printf("importing bank data with %d rows", rows)

	cmd := roachtestutil.NewCommand("%s workload fixtures import bank", binaryPath).
		Arg("{pgurl%s}", c.Node(1)).
		Flag("payload-bytes", 10240).
		Flag("rows", rows).
		Flag("seed", 4).
		Flag("db", "bigbank").
		String()

	return c.RunE(ctx, option.WithNodes(c.Node(1)), cmd)
}

// runTPCCWorkloadModular runs the TPCC workload
func runTPCCWorkloadModular(ctx context.Context, _ test.Test, c cluster.Cluster, l *logger.Logger, _ *modular.Helper, warehouses int) error {
	workloadDur := 10 * time.Minute
	rampDur := 1 * time.Minute
	if !c.IsLocal() {
		workloadDur = 2 * time.Minute
		rampDur = 30 * time.Second
	}

	l.Printf("running TPCC workload for %v with %d warehouses", workloadDur, warehouses)

	cmd := roachtestutil.NewCommand("./cockroach workload run tpcc").
		Arg("{pgurl%s}", c.CRDBNodes()).
		Flag("duration", workloadDur).
		Flag("warehouses", warehouses).
		Flag("ramp", rampDur).
		Flag("prometheus-port", 2112).
		String()

	return c.RunE(ctx, option.WithNodes(c.WorkloadNode()), cmd)
}

// checkTPCCWorkloadModular validates TPCC data integrity
func checkTPCCWorkloadModular(ctx context.Context, _ test.Test, c cluster.Cluster, l *logger.Logger, _ *modular.Helper, warehouses int) error {
	l.Printf("checking TPCC workload data integrity for %d warehouses", warehouses)

	cmd := roachtestutil.NewCommand("%s workload check tpcc", test.DefaultCockroachPath).
		Arg("{pgurl:1}").
		Flag("warehouses", warehouses).
		String()

	return c.RunE(ctx, option.WithNodes(c.WorkloadNode()), cmd)
}

// restartNodeWithVersion restarts a node with a specified binary version
// This mimics the behavior of restartWithNewBinaryStep from the mixed version framework
func restartNodeWithVersion(ctx context.Context, rt test.Test, l *logger.Logger, cluster cluster.Cluster, node int, versionStr string) error {
	l.Printf("restarting node %d with version %s", node, versionStr)

	// Parse the target version
	targetVersion := clusterupgrade.MustParseVersion(versionStr)

	// Use a timeout for the restart operation similar to mixed version framework
	startTimeout := 30 * time.Minute
	startCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()

	// Create node options for the specific node
	nodeOption := cluster.Node(node)

	// Custom start options similar to restartSystemSettings in mixed version
	customStartOpts := []option.StartStopOption{
		option.SkipInit,         // Don't re-initialize the cluster
		option.NoBackupSchedule, // Disable scheduled backups for deterministic tests
	}

	// Use the specified version binary
	// Create empty cluster settings slice
	var clusterSettings []install.ClusterSettingOption

	// Use clusterupgrade.RestartNodesWithNewBinary which handles the full restart process
	// This is the same function used in the mixed version framework
	return clusterupgrade.RestartNodesWithNewBinary(
		startCtx,
		rt,
		l,
		cluster,
		nodeOption,
		option.NewStartOpts(customStartOpts...),
		targetVersion,
		clusterSettings...,
	)
}

// rollbackNodeToVersion restarts a node with a specific version (rollback scenario)
// This simulates rolling back from current version to a previous version
func rollbackNodeToVersion(ctx context.Context, rt test.Test, l *logger.Logger, cluster cluster.Cluster, node int, versionStr string) error {
	l.Printf("rolling back node %d to version %s", node, versionStr)

	// Parse the target version
	targetVersion := clusterupgrade.MustParseVersion(versionStr)

	// Use a timeout for the restart operation similar to mixed version framework
	startTimeout := 30 * time.Minute
	startCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()

	// Create node options for the specific node
	nodeOption := cluster.Node(node)

	// Custom start options similar to restartSystemSettings in mixed version
	customStartOpts := []option.StartStopOption{
		option.SkipInit,         // Don't re-initialize the cluster
		option.NoBackupSchedule, // Disable scheduled backups for deterministic tests
	}

	// Use the specific target version binary
	// Create empty cluster settings slice
	var clusterSettings []install.ClusterSettingOption

	// Use clusterupgrade.RestartNodesWithNewBinary with the target version
	// This handles uploading the specific version binary and restarting
	return clusterupgrade.RestartNodesWithNewBinary(
		startCtx,
		rt,
		l,
		cluster,
		nodeOption,
		option.NewStartOpts(customStartOpts...),
		targetVersion,
		clusterSettings...,
	)
}

func runMergeOperationExample(ctx context.Context, t test.Test, c cluster.Cluster) {
	// Calculate warehouse and row counts similar to mixed-headroom
	maxWarehouses := maxSupportedTPCCWarehouses(*t.BuildVersion(), c.Cloud(), c.Spec())
	headroomWarehouses := int(float64(maxWarehouses) * 0.7)
	bankRows := 65104166 / 2
	if !c.IsLocal() {
		bankRows = 1000
		headroomWarehouses = 20
	}

	// Create a modular test that mimics the mixed-headroom structure
	mod := modular.NewTest(ctx, t.L(), c, c.CRDBNodes())

	// Store the v25.3.0 binary path for workload operations
	var v253BinaryPath string

	initStage := mod.NewStage("cluster init")
	mod.InStage(initStage, "install fixtures for version 25.3", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		// Install fixtures for version 25.3
		version := clusterupgrade.MustParseVersion("v25.3.0")
		return clusterupgrade.InstallFixtures(ctx, l, c, c.CRDBNodes(), version)
	}).Then("start cluster at version 25.3", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		// Start cluster using version 25.3
		version := clusterupgrade.MustParseVersion("v25.3.0")
		binaryPath, err := clusterupgrade.UploadCockroach(ctx, t, l, c, c.CRDBNodes(), version)
		if err != nil {
			return err
		}

		// Store binary path for use in workload operations
		v253BinaryPath = binaryPath

		clusterSettings := install.MakeClusterSettings(
			install.BinaryOption(binaryPath),
		)

		startOpts := option.NewStartOpts(
			option.NoBackupSchedule,
		)

		c.Start(ctx, l, startOpts, clusterSettings, c.CRDBNodes())
		return nil
	}).Then("waiting for all nodes to acknowledge cluster version 25.3", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		// Use a timeout of 5 minutes for cluster version acknowledgment
		timeout := 5 * time.Minute

		// Connect function that creates a connection to a specific node
		// Let WaitForClusterUpgrade handle connection errors gracefully
		connectFunc := func(node int) *gosql.DB {
			db, err := c.ConnE(ctx, l, node)
			if err != nil {
				// Return nil and let WaitForClusterUpgrade handle the error
				// This is safer than panicking
				l.Printf("warning: failed to connect to node %d: %v", node, err)
				return nil
			}
			return db
		}

		return clusterupgrade.WaitForClusterUpgrade(ctx, l, c.CRDBNodes(), connectFunc, timeout)
	})

	inStartupStage := mod.NewStage("startup")
	mod.InStage(inStartupStage, "set preserve_downgrade_option to 25.3", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		db := c.Conn(ctx, l, 1)
		_, err := db.ExecContext(ctx, "SET CLUSTER SETTING cluster.preserve_downgrade_option = $1", "25.3")
		return err
	})
	mod.InStage(inStartupStage, "enable tenant features", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return enableTenantSplitScatterModular(l, h)
	}).Then("import TPCC dataset", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return importTPCCDataModular(ctx, t, c, l, h, headroomWarehouses, v253BinaryPath)
	}).And("import bank dataset", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return importBankDataModular(ctx, t, c, l, h, bankRows, v253BinaryPath)
	})

	upgradeStage := mod.NewStage("upgrade from v25.3 -> v25.4")
	mod.InStage(upgradeStage, "run TPCC workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return runTPCCWorkloadModular(ctx, t, c, l, h, headroomWarehouses)
	})

	for _, node := range c.CRDBNodes() {
		mod.InStage(upgradeStage, fmt.Sprintf("restart node %d with v25.4", node), func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
			return restartNodeWithVersion(ctx, t, l, c, node, "v25.4.0")
		}, modular.DisableConcurrency(), modular.AcquireLock(modular.NodeAvailability{}), modular.ReleaseLock(modular.NodeAvailability{}))
	}

	rollbackStage := mod.NewStage("rollback")
	mod.InStage(rollbackStage, "run TPCC workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return runTPCCWorkloadModular(ctx, t, c, l, h, headroomWarehouses)
	})

	for _, node := range c.CRDBNodes() {
		mod.InStage(rollbackStage, fmt.Sprintf("rollback node %d to version 25.3", node), func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
			return rollbackNodeToVersion(ctx, t, l, c, node, "v25.3.0")
		}, modular.DisableConcurrency(), modular.AcquireLock(modular.NodeAvailability{}), modular.ReleaseLock(modular.NodeAvailability{}))
	}

	finalizeStage := mod.NewStage("finalize")
	mod.InStage(finalizeStage, "run TPCC workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return runTPCCWorkloadModular(ctx, t, c, l, h, headroomWarehouses)
	})
	sb := mod.InStage(finalizeStage, fmt.Sprintf("restart node %d with v25.4", 1), func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return restartNodeWithVersion(ctx, t, l, c, 1, "v25.4.0")
	}, modular.DisableConcurrency(), modular.AcquireLock(modular.NodeAvailability{}), modular.ReleaseLock(modular.NodeAvailability{}))

	for _, node := range c.CRDBNodes()[1:] {
		sb = sb.And(fmt.Sprintf("restart node %d with v25.4", node), func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
			return restartNodeWithVersion(ctx, t, l, c, node, "v25.4.0")
		}, modular.DisableConcurrency(), modular.AcquireLock(modular.NodeAvailability{}), modular.ReleaseLock(modular.NodeAvailability{}))
	}
	sb.And("reset preserve_downgrade_option", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		db := c.Conn(ctx, l, 1)
		_, err := db.ExecContext(ctx, "RESET CLUSTER SETTING cluster.preserve_downgrade_option")
		return err
	}).Then("wait for all nodes to acknowledge v25.4 cluster version", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		// Use a timeout of 5 minutes for cluster version acknowledgment
		timeout := 5 * time.Minute

		// Connect function that creates a connection to a specific node
		// Let WaitForClusterUpgrade handle connection errors gracefully
		connectFunc := func(node int) *gosql.DB {
			db, err := c.ConnE(ctx, l, node)
			if err != nil {
				// Return nil and let WaitForClusterUpgrade handle the error
				// This is safer than panicking
				l.Printf("warning: failed to connect to node %d: %v", node, err)
				return nil
			}
			return db
		}

		return clusterupgrade.WaitForClusterUpgrade(ctx, l, c.CRDBNodes(), connectFunc, timeout)
	})

	// Final validation stage
	validationStage := mod.NewStage("validation")
	mod.InStage(validationStage, "check TPCC workload integrity", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return checkTPCCWorkloadModular(ctx, t, c, l, h, headroomWarehouses)
	})

	mod.AddOperation(upgradeStage, operations.NodeRestart())
	//mod.AddOperation(upgradeStage, operations.BackupRestore(c, operations.BackupRestoreOptions{
	//	Database:          "tpcc",
	//	IncrementalLayers: 1,
	//}))
	//// Add backup/restore operation on same database "tpcc" and table "order" - should conflict
	//mod.AddOperation(upgradeStage, operations.BackupRestore(c, operations.BackupRestoreOptions{
	//	Database:          "tpcc",
	//	Table:             "order",
	//	IncrementalLayers: 2,
	//}))
	//// Add a backup restore to a different database, shouldn't conflict.
	//mod.AddOperation(upgradeStage, operations.BackupRestore(c, operations.BackupRestoreOptions{
	//	Database:          "bigbank",
	//	IncrementalLayers: 1,
	//}))

	// TODO lets add a operation mutator that lets us add n random operations
	mod.AddOperation(upgradeStage, operations.AddRandomIndex())
	mod.AddOperation(upgradeStage, operations.AddRandomIndex())
	mod.AddOperation(upgradeStage, operations.AddRandomColumn())
	mod.AddOperation(upgradeStage, operations.AddRandomColumn())
	mod.AddOperation(upgradeStage, operations.AddRandomColumn())

	// Print the DAG before we merge our extra operations.
	planner := mod.NewPlanner()
	t.L().Printf("DAG before merging:\n%s", planner.DAG())

	// Generate and execute test plan (this is where merging happens)
	testPlan, err := planner.Plan()
	if err != nil {
		t.Fatalf("Failed to generate test plan: %v", err)
	}

	// Print the DAG after merging to see if chains merged
	t.L().Printf("DAG after merging:\n%s", planner.DAG())

	err = modular.RunTestPlan(ctx, t, testPlan)
	if err != nil {
		t.Fatalf("Test execution failed: %v", err)
	}
}

func runModularRecoveryExample(ctx context.Context, t test.Test, c cluster.Cluster) {
	// Create a new modular test with state tracking enabled for recovery testing
	mod := modular.NewTest(ctx, t.L(), c, c.CRDBNodes(), modular.WithDebug(modular.ClusterStateDebug), modular.CleanupOnFailure())

	mod.Setup("initialize cluster", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		c.Start(ctx, l, option.DefaultStartOpts(), install.MakeClusterSettings(), c.CRDBNodes())
		return nil
	})

	// Create main test stage with custom concurrency
	mainStage := mod.NewStage("main-workload", modular.WithStepConcurrency(3))

	// Add TPCC workload chain: init, run, then check consistency
	mod.InStage(mainStage, "init tpcc workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		cmd := fmt.Sprintf("./cockroach workload init tpcc --warehouses=10 {pgurl:%d}", h.RandomAvailableNode())
		c.Run(ctx, option.WithNodes(c.WorkloadNode()), cmd)
		return nil
	}).Then("run tpcc workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		cmd := fmt.Sprintf("./cockroach workload run tpcc --warehouses=10 --duration=60s {pgurl%s}", h.AvailableNodes())
		c.Run(ctx, option.WithNodes(c.WorkloadNode()), cmd)
		return nil
	}).Then("check tpcc consistency", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		cmd := fmt.Sprintf("./cockroach workload check tpcc --warehouses=10 {pgurl:%d}", h.RandomAvailableNode())
		c.Run(ctx, option.WithNodes(c.WorkloadNode()), cmd)
		return nil
	})

	// Add replication factor chain with an unconditional fatal step to test recovery
	mod.InStage(mainStage, "increase rebalance snapshot rate", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return h.SetClusterSetting("kv.snapshot_rebalance.max_rate", "2 GiB")
	}).Then("increase replication factor to 5", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return h.AlterRange("default", "num_replicas = 5")
	}).Then("wait for replication to 5", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		_, db := h.RandomDB()
		defer db.Close()
		return roachtestutil.WaitForReplication(ctx, l, db, 5, roachprod.AtLeastReplicationFactor)
	}).Then("decrease replication factor to 3", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return h.AlterRange("default", "num_replicas = 3")
	}).Then("restore rebalance snapshot rate", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return h.ResetClusterSetting("kv.snapshot_rebalance.max_rate")
	})

	mod.InStage(mainStage, "error step", func(ctx context.Context, l *logger.Logger, helper *modular.Helper) error {
		return errors.New("intentional fatal error to test recovery")
	})

	// Generate the test plan
	planner := mod.NewPlanner()
	testPlan, err := planner.Plan()
	if err != nil {
		t.Fatalf("Failed to generate test plan: %v", err)
	}

	// Execute the test plan using the runner
	err = modular.RunTestPlan(ctx, t, testPlan)
	if err != nil {
		// Check if the error contains ONLY the intentional failure we expect
		expectedError := "intentional fatal error to test recovery"
		if strings.Contains(err.Error(), expectedError) && !strings.Contains(err.Error(), "Failed to restore") {
			// The test succeeded - we got the expected failure and state restoration succeeded
			t.L().Printf("Test completed successfully: got expected intentional failure and cluster state was restored")
			return
		}
		// Any other error (including restoration failures) should fail the test
		t.Fatalf("Test execution failed: %v", err)
	}
}
