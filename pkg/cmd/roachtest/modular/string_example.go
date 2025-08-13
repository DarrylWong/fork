package modular

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// ExampleComplexTestPlanString demonstrates the String() output for a complex test plan.
func ExampleComplexTestPlanString() {
	// Create a complex test definition
	testDef := NewTest("complex-tpcc-test", 42)

	// Add multiple clusters with different configurations
	testDef.AddCluster(
		MinNodes(3),
		MaxNodes(6),
		DisabledDeploymentModes(SeparateProcessDeployment),
	)

	testDef.AddWorkloadCluster(WorkloadNodeCount(2))

	// Setup phase
	testDef.Setup(func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	// Multiple stages with different configurations
	importStage := testDef.NewStage("data-import")
	testDef.InStage(importStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}, WithDescription("import TPCC data"))

	testDef.InStage(importStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}, WithDescription("import Bank data"))

	workloadStage := testDef.NewStage("workload-execution", Repeat(3, 3))
	testDef.InStage(workloadStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}, InBackground())

	chaosStage := testDef.NewStage("chaos-testing",
		Repeat(5, 10),
		DelayInterval(30*time.Second, 2*time.Minute))

	testDef.InStage(chaosStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}, DisableConcurrency(), WithDescription("kill random nodes"))

	testDef.InStage(chaosStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	}, WithDescription("network partition"))

	// Cleanup
	testDef.AfterTest(func(ctx context.Context, l *logger.Logger, h *Helper) error {
		return nil
	})

	// Generate and print the plan
	plan := testDef.GeneratePlan()
	fmt.Print(plan.String())

	// Output:
	// Modular Test Plan: complex-tpcc-test (seed: 42)
	// ==================================================
	//
	// Cluster Specifications:
	//   1. cluster-0: 3-6 nodes (disabled: [separate-process])
	//
	// Workload Specifications:
	//   1. workload-0: 2 nodes
	//
	// Setup Steps:
	//   1. setup step
	//
	// Test Stages:
	//   Stage 1: data-import
	//     1. import TPCC data
	//     2. import Bank data
	//
	//   Stage 2: workload-execution (repeat 3 times)
	//     1. run TPCC workload (background)
	//
	//   Stage 3: chaos-testing (repeat 7 times, delay: 1m0s)
	//     1. kill random nodes (sequential)
	//     2. network partition
	//
	// After-Test Steps:
	//   1. after test step
	//
	// Mode: Distributed
}
