package modular

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
)

// Planner generates test plans from test definitions.
type Planner interface {
	Plan() (*TestPlan, error)
}

// SimplePlanner implements basic test planning.
type SimplePlanner struct {
	test   *Test
	rng    *rand.Rand
	config PlannerConfig
}

// PlannerConfig configures the test planner.
type PlannerConfig struct {
	IsLocal bool
}

// NewSimplePlanner creates a new simple planner.
func NewSimplePlanner(test *Test, config PlannerConfig) *SimplePlanner {
	return &SimplePlanner{
		test:   test,
		rng:    rand.New(rand.NewSource(test.seed)),
		config: config,
	}
}

// Plan generates a test plan from the test definition.
func (p *SimplePlanner) Plan() (*TestPlan, error) {
	plan := p.test.GeneratePlan()
	plan.isLocal = p.config.IsLocal
	return plan, nil
}

// Execute runs a modular test with the given cluster.
func Execute(ctx context.Context, t test.Test, c cluster.Cluster, testDef *Test) error {
	planner := NewSimplePlanner(testDef, PlannerConfig{
		IsLocal: false, // TODO: detect from test environment
	})

	plan, err := planner.Plan()
	if err != nil {
		return fmt.Errorf("failed to generate test plan: %w", err)
	}

	runner := NewTestRunner(plan, t)
	return runner.Run(ctx)
}
