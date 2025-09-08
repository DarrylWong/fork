// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package tests

import (
	"context"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

func registerModular(r registry.Registry) {
	r.Add(registry.TestSpec{
		Name:             "modular/example",
		CompatibleClouds: registry.AllClouds,
		Suites:           registry.Suites(registry.Nightly),
		Owner:            registry.OwnerTestEng,
		Run:              runModularExample,
		Cluster:          r.MakeClusterSpec(4, spec.WorkloadNodeCount(1)),
		Timeout:          30 * time.Minute,
	})
}

func runModularExample(ctx context.Context, t test.Test, c cluster.Cluster) {
	// Create a new modular test with a specific seed for reproducibility
	mod := modular.NewTest(ctx, t.L(), c, c.CRDBNodes())

	mod.Setup("initialize cluster", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		c.Start(ctx, l, option.DefaultStartOpts(), install.MakeClusterSettings(), c.CRDBNodes())
		return nil
	})

	// Create main test stage with custom concurrency
	mainStage := mod.NewStage("main-workload", modular.WithStepConcurrency(3))

	// Add TPCC workload chain: init, run, then check consistency
	mod.InStage(mainStage, "init tpcc workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		l.Printf("Initializing TPCC workload...")
		cmd := "./cockroach workload init tpcc --warehouses=10 {pgurl:1}"
		c.Run(ctx, option.WithNodes(c.WorkloadNode()), cmd)
		return nil
	}).Then("run tpcc workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		l.Printf("Running TPCC workload...")
		cmd := "./cockroach workload run tpcc --warehouses=10 --duration=60s {pgurl:1-3}"
		c.Run(ctx, option.WithNodes(c.WorkloadNode()), cmd)
		return nil
	}).Then("check tpcc consistency", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		l.Printf("Running TPCC consistency checks...")
		cmd := "./cockroach workload check tpcc --warehouses=10 {pgurl:1}"
		c.Run(ctx, option.WithNodes(c.WorkloadNode()), cmd)
		return nil
	})

	// Add replication factor chain: speed up rebalancing, increase to 5, wait, decrease to 3, wait, then restore settings
	mod.InStage(mainStage, "increase rebalance snapshot rate", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		l.Printf("Increasing kv.snapshot_rebalance.max_rate to speed up replication...")
		db := c.Conn(ctx, t.L(), 1)
		defer db.Close()

		_, err := db.ExecContext(ctx, "SET CLUSTER SETTING kv.snapshot_rebalance.max_rate = '2 GiB'")
		return err
	}).Then("increase replication factor to 5", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		l.Printf("Increasing replication factor to 5...")
		db := c.Conn(ctx, t.L(), 1)
		defer db.Close()

		_, err := db.ExecContext(ctx, "ALTER RANGE default CONFIGURE ZONE USING num_replicas = 5")
		return err
	}).Then("wait for replication to 5", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		db := c.Conn(ctx, t.L(), 1)
		defer db.Close()

		return roachtestutil.WaitForReplication(ctx, l, db, 5, roachprod.AtLeastReplicationFactor)
	}).Then("decrease replication factor to 3", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		l.Printf("Decreasing replication factor to 3...")
		db := c.Conn(ctx, t.L(), 1)
		defer db.Close()

		_, err := db.ExecContext(ctx, "ALTER RANGE default CONFIGURE ZONE USING num_replicas = 3")
		return err
	}).Then("restore rebalance snapshot rate", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		l.Printf("Restoring kv.snapshot_rebalance.max_rate to default...")
		db := c.Conn(ctx, t.L(), 1)
		defer db.Close()

		_, err := db.ExecContext(ctx, "RESET CLUSTER SETTING kv.snapshot_rebalance.max_rate")
		return err
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
		t.Fatalf("Test execution failed: %v", err)
	}
}
