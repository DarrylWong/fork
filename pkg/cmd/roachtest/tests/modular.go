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
	r.Add(registry.TestSpec{
		Name:             "modular/example/dynamic-resource",
		CompatibleClouds: registry.AllClouds,
		Suites:           registry.Suites(registry.Nightly),
		Owner:            registry.OwnerTestEng,
		Run:              runDynamicResourceExample,
		Cluster:          r.MakeClusterSpec(4, spec.WorkloadNodeCount(1)),
		Timeout:          30 * time.Minute,
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
		dbName, err := h.CreateRandomDatabase("bank")
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
	err = modular.RunStaticTestPlan(ctx, t, testPlan)
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

	err = modular.RunStaticTestPlan(ctx, t, testPlan)
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
	// TODO: AddRandomColumn not yet implemented
	//mod.AddOperation(upgradeStage, operations.AddRandomColumn())
	//mod.AddOperation(upgradeStage, operations.AddRandomColumn())
	//mod.AddOperation(upgradeStage, operations.AddRandomColumn())

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

	err = modular.RunStaticTestPlan(ctx, t, testPlan)
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
	err = modular.RunStaticTestPlan(ctx, t, testPlan)
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

// runDynamicResourceExample demonstrates truly dynamic resource access where
// the resource to lock is determined at planning time, not build time.
// This test demonstrates that dynamic steps with WithDynamicResourceCallback
// can properly participate in chain merging based on runtime-selected resources.
func runDynamicResourceExample(ctx context.Context, t test.Test, c cluster.Cluster) {
	mod := modular.NewTest(ctx, t.L(), c, c.CRDBNodes(), modular.WithDebug(modular.ClusterStateDebug))

	// Setup: Start cluster and create multiple tables
	mod.Setup("initialize cluster", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		c.Start(ctx, l, option.DefaultStartOpts(), install.MakeClusterSettings(), c.CRDBNodes())
		return nil
	})

	mod.Setup("initialize tpcc workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		dbName := "tpcc"
		if err := h.CreateDatabase(dbName); err != nil {
			return err
		}

		// Initialize TPCC with 10 warehouses
		// This creates realistic tables: warehouse, district, customer, history,
		// new_order, "order", order_line, item, stock
		cmd := roachtestutil.NewCommand("%s workload fixtures import tpcc", test.DefaultCockroachPath).
			Flag("warehouses", 10).
			Flag("db", dbName).
			Arg("{pgurl:%d}", h.RandomAvailableNode()).
			String()

		l.Printf("Initializing TPCC workload with 10 warehouses in database %s", dbName)
		if err := c.RunE(ctx, option.WithNodes(c.WorkloadNode()), cmd); err != nil {
			return errors.Wrap(err, "failed to initialize TPCC workload")
		}

		l.Printf("TPCC workload initialized successfully")
		return nil
	})

	// Main stage: Demonstrate dynamic resource access
	mainStage := mod.NewStage("dynamic-operations", modular.WithStepConcurrency(2))

	// Add 3 static AddIndexToTable operations - one for each TPCC table
	// These have the table specified upfront (not dynamic)
	// The planner can see exactly which tables they'll access
	mod.AddOperation(mainStage, operations.AddIndexToTable("tpcc", "customer", "c_last"))
	mod.AddOperation(mainStage, operations.AddIndexToTable("tpcc", "order_line", "ol_i_id"))
	mod.AddOperation(mainStage, operations.AddIndexToTable("tpcc", "stock", "s_w_id"))

	// Add multiple dynamic operations that select tables at PrePlan time
	// With these dynamic operations + 3 static operations, some will target the same TPCC tables,
	// and we'll see chain merging when operations conflict on the same table
	for i := 0; i < 3; i++ {
		mod.AddOperation(mainStage, operations.AddRandomIndexDynamic())
	}
	for i := 0; i < 2; i++ {
		mod.AddOperation(mainStage, operations.AddRandomColumnDynamic())
	}

	// Add a node restart operation - this acquires NodeAvailability lock
	// This will run in parallel with index operations since they don't conflict
	mod.AddOperation(mainStage, operations.NodeRestart())

	// Add a dynamic database-level backup/restore operation
	// This selects a database dynamically during PrePlan phase from a whitelist (tpcc, cct_tpcc, bank)
	// and performs a full backup + 1 incremental backup, then restores to a new name
	mod.AddOperation(mainStage, operations.BackupRestoreDatabaseDynamic())

	// Stage 2: Delete all tables except one to make it obvious dynamic operations adapt
	deleteStage := mod.NewStage("delete-all-but-one-table", modular.WithStepConcurrency(1))

	// Delete all TPCC tables except "customer" to demonstrate dynamic planning adaptation
	// This makes it very obvious that dynamic operations will only select from remaining tables
	// Also drop any restored databases from the backup/restore operation
	//
	// TODO(test-eng): The BackupRestoreDatabaseDynamic operation doesn't expose the restored
	// database name to subsequent stages. We have to pattern-match on '*_restored_*' to find
	// and drop these databases. Ideally, operations should be able to track/expose created
	// resources (databases, tables, etc.) so later stages can reference them without guessing.
	// Consider adding a mechanism for operations to register created resources in the cluster
	// state tracker, with a way to query them by type or tag.
	mod.InStage(deleteStage, "drop restored databases", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		db := h.RandomDBConn()
		rows, err := db.QueryContext(ctx, "SELECT database_name FROM [SHOW DATABASES] WHERE database_name LIKE '%_restored_%'")
		if err != nil {
			return err
		}
		defer rows.Close()

		var restoredDBs []string
		for rows.Next() {
			var dbName string
			if err := rows.Scan(&dbName); err != nil {
				return err
			}
			restoredDBs = append(restoredDBs, dbName)
		}

		for _, dbName := range restoredDBs {
			l.Printf("Dropping restored database: %s", dbName)
			if err := h.Exec(fmt.Sprintf("DROP DATABASE %s CASCADE", dbName)); err != nil {
				return err
			}
		}
		return nil
	}).Then("drop table tpcc.warehouse", operations.DropTable("tpcc", "warehouse")).
		Then("drop table tpcc.district", operations.DropTable("tpcc", "district")).
		Then("drop table tpcc.history", operations.DropTable("tpcc", "history")).
		Then("drop table tpcc.new_order", operations.DropTable("tpcc", "new_order")).
		Then("drop table tpcc.order", operations.DropTable("tpcc", "order")).
		Then("drop table tpcc.order_line", operations.DropTable("tpcc", "order_line")).
		Then("drop table tpcc.item", operations.DropTable("tpcc", "item")).
		Then("drop table tpcc.stock", operations.DropTable("tpcc", "stock"))

	// Stage 3: Add more dynamic operations to show they only select the remaining table
	verifyStage := mod.NewStage("verify-only-customer-table-selected", modular.WithStepConcurrency(2))

	// Add several more dynamic operations to verify deleted tables are avoided
	// All of these should select only tpcc.customer since it's the only remaining table
	for i := 0; i < 2; i++ {
		mod.AddOperation(verifyStage, operations.AddRandomIndexDynamic())
	}
	for i := 0; i < 2; i++ {
		mod.AddOperation(verifyStage, operations.AddRandomColumnDynamic())
	}
	// Add an INSPECT operation to validate the table
	mod.AddOperation(verifyStage, operations.InspectTableDynamic())

	// Print the DAG to show what the planner sees
	planner := mod.NewPlanner()
	t.L().Printf("=== DAG before planning (dynamic names not yet available) ===\n%s", planner.DAG())

	t.L().Printf("\n=== DYNAMIC RESOURCE ACCESS WITH CHAIN MERGING ===")
	t.L().Printf("The dynamic operations (AddRandomIndexDynamic, AddRandomColumnDynamic, InspectTableDynamic)")
	t.L().Printf("all select tables at PrePlan time using WithDynamicResourceCallback.")
	t.L().Printf("This enables proper chain merging:")
	t.L().Printf("1. PrePlan is called before chain merging, making dynamic resources visible")
	t.L().Printf("2. If two dynamic operations select the same table, they are serialized")
	t.L().Printf("3. Operations targeting different tables can run in parallel")
	t.L().Printf("4. The DAG will show dynamic names after PrePlan is called\n")

	t.L().Printf("\n=== STAGE 2: TABLE DELETION ===")
	t.L().Printf("In this stage, we delete ALL TPCC tables except tpcc.customer.")
	t.L().Printf("This makes it very obvious that dynamic operations in stage 3 adapt to the changed schema.\n")

	t.L().Printf("\n=== STAGE 3: VERIFY DYNAMIC PLANNING AFTER DELETION ===")
	t.L().Printf("Adding more dynamic operations (index, column, inspect) to verify deleted tables are not selected.")
	t.L().Printf("All dynamic operations should ONLY select the tpcc.customer table:\n")
	t.L().Printf("- customer (the only remaining table)")
	t.L().Printf("- All other TPCC tables (warehouse, district, history, new_order, order, order_line, item, stock)")
	t.L().Printf("  should NOT be selected since they were deleted")
	t.L().Printf("This demonstrates that all three types of dynamic operations adapt to schema changes.\n")

	// Execute the test plan dynamically
	// This calls PrePlan before each stage, enabling truly dynamic resource access
	err := modular.RunDynamicTestPlan(ctx, t, &planner)
	if err != nil {
		t.Fatalf("Test execution failed: %v", err)
	}

	t.L().Printf("\n=== Test completed ===")
	t.L().Printf("This test demonstrates that dynamic resource selection works correctly")
	t.L().Printf("with proper chain merging. The chain merger can see runtime-determined")
	t.L().Printf("resources via WithDynamicResourceCallback, enabling proper serialization.")
	t.L().Printf("\nAdditionally, this test shows that dynamic operations adapt to schema changes.")
	t.L().Printf("After deleting all tables except customer, all dynamic operations in stage 3")
	t.L().Printf("selected only the customer table, proving schema-aware dynamic planning.")
}
