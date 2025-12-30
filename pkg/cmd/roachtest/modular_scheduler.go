// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package main

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/errors"
)

// SchedulerConfig contains configuration for the modular scheduler.
type SchedulerConfig struct {
	// BaseDAGNames are the names of base DAGs to select from. If empty, all are available.
	BaseDAGNames []string

	// IncludeOperations are regex patterns for operations to include.
	// If nil or empty, all operations are included by default.
	IncludeOperations []string

	// ExcludeOperations are regex patterns for operations to exclude.
	ExcludeOperations []string

	// OperationsPerStage specifies the min and max number of operations to add per stage.
	// [0] is min, [1] is max.
	OperationsPerStage [2]int

	// Seed for the RNG. If 0, uses time-based seed.
	Seed int64

	// TestName is the name for the generated test.
	TestName string
}

// Scheduler generates and executes random modular test plans.
type Scheduler struct {
	config  SchedulerConfig
	logger  *logger.Logger
	cluster cluster.Cluster
	rng     *rand.Rand
	opPool  *modular.OperationPool
}

// NewScheduler creates a new scheduler with the given configuration.
func NewScheduler(
	config SchedulerConfig, l *logger.Logger, c cluster.Cluster,
) (*Scheduler, error) {
	seed := config.Seed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}

	// Create operation pool
	opPool, err := modular.NewOperationPool(
		config.IncludeOperations,
		config.ExcludeOperations,
		seed,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create operation pool")
	}

	return &Scheduler{
		config:  config,
		logger:  l,
		cluster: c,
		rng:     rand.New(rand.NewSource(seed)),
		opPool:  opPool,
	}, nil
}

// GenerateTestPlan generates a new test plan by:
// 1. Selecting a random base DAG
// 2. Building the base test structure
// 3. Augmenting stages with random operations from the pool
func (s *Scheduler) GenerateTestPlan(ctx context.Context) (*modular.Test, error) {
	s.logger.Printf("Generating test plan (seed: %d)", s.config.Seed)

	// Select base DAG
	baseDAG, err := s.selectBaseDAG()
	if err != nil {
		return nil, err
	}
	s.logger.Printf("Selected base DAG: %s - %s", baseDAG.Name(), baseDAG.Description())

	// Create modular test
	testName := s.config.TestName
	if testName == "" {
		testName = fmt.Sprintf("modular-scheduler-%s-%d", baseDAG.Name(), s.config.Seed)
	}

	modTest := modular.NewTest(
		ctx,
		s.logger,
		s.cluster,
		s.cluster.CRDBNodes(),
		modular.WithDebug(modular.ClusterStateDebug),
	)

	// Build base DAG structure
	if err := baseDAG.Build(modTest); err != nil {
		return nil, errors.Wrapf(err, "failed to build base DAG: %s", baseDAG.Name())
	}

	// Augment stages with random operations
	if err := s.augmentStages(modTest); err != nil {
		return nil, errors.Wrap(err, "failed to augment stages with random operations")
	}

	s.logger.Printf("Test plan generated successfully")
	return modTest, nil
}

// ExecuteTestPlan executes the given test plan using the modular framework.
func (s *Scheduler) ExecuteTestPlan(
	ctx context.Context, t test.Test, modTest *modular.Test,
) error {
	s.logger.Printf("Executing test plan")

	// Create planner
	planner := modTest.NewPlanner()

	// Generate and log DAG visualization
	dag := planner.DAG()
	s.logger.Printf("Generated DAG:\n%s", dag)

	// Execute using dynamic runner (plans one stage at a time)
	if err := modular.RunDynamicTestPlan(ctx, t, &planner); err != nil {
		return errors.Wrap(err, "test plan execution failed")
	}

	s.logger.Printf("Test plan executed successfully")
	return nil
}

// RunIteration executes one full test iteration:
// 1. Generate test plan
// 2. Execute test plan
// 3. Cleanup (TODO)
func (s *Scheduler) RunIteration(ctx context.Context, t test.Test) error {
	s.logger.Printf("Starting scheduler iteration")
	startTime := time.Now()

	// Generate test plan
	modTest, err := s.GenerateTestPlan(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to generate test plan")
	}

	// Execute test plan
	if err := s.ExecuteTestPlan(ctx, t, modTest); err != nil {
		return errors.Wrap(err, "failed to execute test plan")
	}

	// TODO: Cleanup cluster state
	s.logger.Printf("TODO: Cleanup cluster state between iterations")

	elapsed := time.Since(startTime)
	s.logger.Printf("Scheduler iteration completed successfully in %s", elapsed)
	return nil
}

// selectBaseDAG selects a random base DAG from the configured set.
func (s *Scheduler) selectBaseDAG() (modular.BaseDAG, error) {
	var availableDAGs []modular.BaseDAG

	if len(s.config.BaseDAGNames) == 0 {
		// Use all registered base DAGs
		availableDAGs = modular.GetAllBaseDAGs()
	} else {
		// Use only specified base DAGs
		for _, name := range s.config.BaseDAGNames {
			dag, err := modular.GetBaseDAG(name)
			if err != nil {
				return nil, errors.Wrapf(err, "base DAG not found: %s", name)
			}
			availableDAGs = append(availableDAGs, dag)
		}
	}

	if len(availableDAGs) == 0 {
		return nil, errors.New("no base DAGs available")
	}

	// Randomly select one
	idx := s.rng.Intn(len(availableDAGs))
	return availableDAGs[idx], nil
}

// augmentStages adds random operations to each stage in the test.
func (s *Scheduler) augmentStages(modTest *modular.Test) error {
	// Get number of operations to add
	min := s.config.OperationsPerStage[0]
	max := s.config.OperationsPerStage[1]

	if min < 0 || max < min {
		return errors.Newf("invalid operations per stage range: [%d, %d]", min, max)
	}

	if max == 0 {
		s.logger.Printf("No random operations to add (max=0)")
		return nil
	}

	numOps := min
	if max > min {
		numOps = min + s.rng.Intn(max-min+1)
	}

	s.logger.Printf("Adding %d random operations per stage", numOps)

	// Get available operations
	ops, err := s.opPool.RandomSelect(numOps)
	if err != nil {
		return errors.Wrap(err, "failed to select random operations")
	}

	s.logger.Printf("Selected operations:")
	for i, op := range ops {
		s.logger.Printf("  %d. %s", i+1, op.Name())
	}

	// Add operations to all non-setup stages
	stages := modTest.GetStages()
	if len(stages) == 0 {
		s.logger.Printf("Warning: No stages found in test, cannot add random operations")
		return nil
	}

	// Distribute operations evenly across stages
	opsPerStage := len(ops) / len(stages)
	remainder := len(ops) % len(stages)

	opIndex := 0
	for stageIdx, stage := range stages {
		// Distribute remainder operations to first few stages
		opsForThisStage := opsPerStage
		if stageIdx < remainder {
			opsForThisStage++
		}

		if opsForThisStage > 0 {
			stageOps := ops[opIndex : opIndex+opsForThisStage]
			for _, op := range stageOps {
				modTest.AddOperation(stage, op)
				s.logger.Printf("  Added %s to stage '%s'", op.Name(), stage.Name())
			}
			opIndex += opsForThisStage
		}
	}

	return nil
}

// GetSummary returns a summary of the scheduler configuration.
func (s *Scheduler) GetSummary() string {
	summary := fmt.Sprintf("Modular Scheduler Configuration:\n")
	summary += fmt.Sprintf("  Seed: %d\n", s.config.Seed)
	summary += fmt.Sprintf("  Base DAGs: %v\n", s.config.BaseDAGNames)
	summary += fmt.Sprintf("  Operations per stage: [%d, %d]\n",
		s.config.OperationsPerStage[0], s.config.OperationsPerStage[1])
	if len(s.config.ExcludeOperations) > 0 {
		summary += fmt.Sprintf("  Exclude patterns: %v\n", s.config.ExcludeOperations)
	}

	count, _ := s.opPool.Count()
	summary += fmt.Sprintf("  Available operations: %d\n", count)

	return summary
}

// CleanupManager handles cluster state cleanup between test plan iterations.
type CleanupManager struct {
	cluster cluster.Cluster
	logger  *logger.Logger
}

// NewCleanupManager creates a new cleanup manager.
func NewCleanupManager(c cluster.Cluster, l *logger.Logger) *CleanupManager {
	return &CleanupManager{
		cluster: c,
		logger:  l,
	}
}

// Cleanup performs cluster state reset to prepare for the next test iteration.
//
// TODO: Implement proper cleanup logic:
func (c *CleanupManager) Cleanup(ctx context.Context) error {
	c.logger.Printf("TODO: Implement cluster cleanup")

	// Placeholder implementation
	return nil
}
