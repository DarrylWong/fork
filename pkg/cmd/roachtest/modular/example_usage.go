package modular

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// ExampleTPCCTest demonstrates how to use the modular framework to create a TPCC-like test.
func ExampleTPCCTest(ctx context.Context, t test.Test, c cluster.Cluster) error {
	// Create a new modular test definition
	testDef := NewTest("example-tpcc", 12345)

	// Define cluster requirements - AddCluster now performs randomization and returns cluster.Cluster
	mainCluster := testDef.AddCluster(
		MinNodes(3),
		MaxNodes(5),
		DisabledDeploymentModes(SeparateProcessDeployment),
	)

	// Define workload cluster
	workloadCluster := testDef.AddWorkloadCluster(
		WorkloadNodeCount(1),
	)

	// Use the actual clusters returned from AddCluster
	_ = mainCluster
	_ = workloadCluster

	// Setup phase: install prometheus
	testDef.Setup(func(ctx context.Context, l *logger.Logger, h *Helper) error {
		if l != nil {
			l.Printf("Installing Prometheus on cluster")
		}
		// operation.InstallPrometheus(ctx, mainCluster)
		return nil
	})

	// Stage 1: Import data concurrently
	importStage := testDef.NewStage("data-import")

	// TPCC import
	testDef.InStage(importStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		if l != nil {
			l.Printf("Importing TPCC fixtures")
		}
		// operation.ImportTPCC(ctx, mainCluster, workloadCluster)
		return nil
	}, WithDescription("TPCC import"))

	// Bank import (runs concurrently with TPCC)
	testDef.InStage(importStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		if l != nil {
			l.Printf("Importing Bank fixtures")
		}
		// operation.ImportBank(ctx, mainCluster, workloadCluster)
		return nil
	}, WithDescription("Bank import"))

	// Stage 2: Run workload in background
	workloadStage := testDef.NewStage("workload-run")
	testDef.InStage(workloadStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		if l != nil {
			l.Printf("Running TPCC workload")
		}
		// operation.RunTPCC(ctx, mainCluster, workloadCluster, duration=1h)
		// Simulate long-running workload
		select {
		case <-time.After(1 * time.Hour):
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}, InBackground(), WithDescription("TPCC workload"))

	// Stage 3: Repeated scatter operations
	scatterStage := testDef.NewStage("scatter-ranges",
		Repeat(5, 10),
		DelayInterval(30*time.Second, 2*time.Minute))

	testDef.InStage(scatterStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		if l != nil {
			l.Printf("Scattering ranges")
		}
		// operation.ScatterRanges(ctx, mainCluster)
		return nil
	}, WithDescription("scatter ranges"))

	// After test: consistency checks
	testDef.AfterTest(func(ctx context.Context, l *logger.Logger, h *Helper) error {
		if l != nil {
			l.Printf("Running TPCC consistency checks")
		}
		// operation.RunTPCCConsistencyChecks(ctx, mainCluster, workloadCluster)
		return nil
	})

	// Execute the test
	return Execute(ctx, t, c, testDef)
}

// ExampleSimpleTest demonstrates a basic test structure.
func ExampleSimpleTest(ctx context.Context, t test.Test, c cluster.Cluster) error {
	testDef := NewTest("simple-test", 54321)

	// Add a basic cluster - randomization happens automatically
	clusterInstance := testDef.AddCluster(NodeCount(3))
	_ = clusterInstance // Will be nil in mock implementation

	// Setup
	testDef.Setup(func(ctx context.Context, l *logger.Logger, h *Helper) error {
		if l != nil {
			l.Printf("Setting up test environment")
		}
		return nil
	})

	// Single stage with simple operations
	stage := testDef.NewStage("main-operations")

	testDef.InStage(stage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		if l != nil {
			l.Printf("Performing operation 1")
		}
		return nil
	})

	testDef.InStage(stage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		if l != nil {
			l.Printf("Performing operation 2")
		}
		return nil
	})

	// Cleanup
	testDef.AfterTest(func(ctx context.Context, l *logger.Logger, h *Helper) error {
		if l != nil {
			l.Printf("Cleaning up test")
		}
		return nil
	})

	return Execute(ctx, t, c, testDef)
}

// This file shows examples of how to use the modular test framework.
// AddCluster now performs randomization and returns cluster.Cluster instances.
// Operations like InstallPrometheus, ImportTPCC, etc. would be implemented
// separately as concrete operations that test writers can compose.
var _ = fmt.Printf // suppress unused import warning
