package tests

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular/operations"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
)

// This is an example of how the YCSB test could be converted to use the modular framework
func registerModularYCSBExample(r registry.Registry) {
	workloads := []operations.YCSBWorkloadType{
		operations.YCSBWorkloadA,
		operations.YCSBWorkloadB,
		operations.YCSBWorkloadC,
	}

	for _, wl := range workloads {
		r.Add(registry.TestSpec{
			Name:      fmt.Sprintf("modular-ycsb/%s/nodes=3", wl),
			Owner:     registry.OwnerTestEng,
			Benchmark: true,
			Cluster:   r.MakeClusterSpec(4, spec.CPU(32), spec.WorkloadNode(), spec.WorkloadNodeCPU(32)),
			Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
				runModularYCSB(ctx, t, c, wl)
			},
			CompatibleClouds: registry.AllClouds,
			Suites:           registry.Suites(registry.Nightly),
		})

		// Example: YCSB with random index creation during workload
		r.Add(registry.TestSpec{
			Name:      fmt.Sprintf("modular-ycsb-with-index/%s/nodes=3", wl),
			Owner:     registry.OwnerTestEng,
			Benchmark: true,
			Cluster:   r.MakeClusterSpec(4, spec.CPU(32), spec.WorkloadNode(), spec.WorkloadNodeCPU(32)),
			Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
				runModularYCSBWithIndex(ctx, t, c, wl)
			},
			CompatibleClouds: registry.AllClouds,
			Suites:           registry.Suites(registry.Nightly),
		})
	}
}

func runModularYCSB(ctx context.Context, t test.Test, c cluster.Cluster, workload operations.YCSBWorkloadType) {
	// Start cluster
	c.Start(ctx, t.L(), option.NewStartOpts(option.NoBackupSchedule), install.MakeClusterSettings(), c.CRDBNodes())

	// Setup database configuration
	setupOp := modular.NewOperation("setup database for ycsb", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		db := c.Conn(ctx, l, 1)
		defer db.Close()

		err := enableIsolationLevels(ctx, t, db)
		if err != nil {
			return fmt.Errorf("failed to enable isolation levels: %w", err)
		}

		err = roachtestutil.WaitFor3XReplication(ctx, l, db)
		if err != nil {
			return fmt.Errorf("failed to wait for replication: %w", err)
		}

		return nil
	})

	// Create YCSB workload operation
	ycsbOp := operations.YCSB(c, workload, operations.YCSBOptions{
		InsertCount:   1000000,
		Concurrency:   144, // Could be made dynamic based on cluster size
		Duration:      30 * time.Minute,
		RampTime:      2 * time.Minute,
		ReadCommitted: false,
	})

	// Run the operations in sequence
	plan := []modular.Operation{
		setupOp,
		ycsbOp,
	}

	t.Status("running modular YCSB plan")
	if err := modular.RunPlan(ctx, t.L(), c, plan); err != nil {
		t.Fatal(err)
	}
}

func runModularYCSBWithIndex(ctx context.Context, t test.Test, c cluster.Cluster, workload operations.YCSBWorkloadType) {
	// Start cluster
	c.Start(ctx, t.L(), option.NewStartOpts(option.NoBackupSchedule), install.MakeClusterSettings(), c.CRDBNodes())

	// Setup database
	setupOp := modular.NewOperation("setup database for ycsb", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		db := c.Conn(ctx, l, 1)
		defer db.Close()
		err := enableIsolationLevels(ctx, t, db)
		if err != nil {
			return fmt.Errorf("failed to enable isolation levels: %w", err)
		}
		err = roachtestutil.WaitFor3XReplication(ctx, l, db)
		if err != nil {
			return fmt.Errorf("failed to wait for replication: %w", err)
		}
		return nil
	})

	// Initialize YCSB data only
	ycsbInitOp := operations.YCSBInit(c, operations.YCSBOptions{
		InsertCount: 1000000,
	})

	// Add a random index during the workload
	randomIndexOp := operations.AddRandomIndex()

	// Run the YCSB workload
	ycsbRunOp := operations.YCSBRun(c, workload, operations.YCSBOptions{
		Concurrency: 144,
		Duration:    30 * time.Minute,
		RampTime:    2 * time.Minute,
	})

	// Create a more complex plan that demonstrates composability
	plan := []modular.Operation{
		setupOp,
		ycsbInitOp,
		randomIndexOp, // Add random index after data is loaded
		ycsbRunOp,     // Run workload with the new index
	}

	t.Status("running modular YCSB with index creation plan")
	if err := modular.RunPlan(ctx, t.L(), c, plan); err != nil {
		t.Fatal(err)
	}
}

// Example: Even more advanced composition - multiple workloads with index operations
func runAdvancedModularYCSBExample(ctx context.Context, t test.Test, c cluster.Cluster) {
	// This demonstrates the power of modular composition
	plan := []modular.Operation{
		// Setup
		setupDatabaseOp(),

		// Phase 1: Initialize with workload A data
		operations.YCSBInit(c, operations.YCSBOptions{InsertCount: 500000}),

		// Phase 2: Run workload A for a short time
		operations.YCSBRun(c, operations.YCSBWorkloadA, operations.YCSBOptions{
			Duration: 10 * time.Minute,
		}),

		// Phase 3: Add an index while workload is idle
		operations.AddRandomIndex(),

		// Phase 4: Switch to read-heavy workload B with the new index
		operations.YCSBRun(c, operations.YCSBWorkloadB, operations.YCSBOptions{
			Duration:      15 * time.Minute,
			ReadCommitted: true,
		}),

		// Phase 5: Add more data and switch to workload C
		operations.YCSBInit(c, operations.YCSBOptions{InsertCount: 500000}), // Add more data
		operations.YCSBRun(c, operations.YCSBWorkloadC, operations.YCSBOptions{
			Duration: 10 * time.Minute,
		}),
	}

	if err := modular.RunPlan(ctx, t.L(), c, plan); err != nil {
		t.Fatal(err)
	}
}