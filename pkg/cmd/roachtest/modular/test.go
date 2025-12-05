package modular

import (
	"context"
	"math/rand"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
)

// Test represents a modular test definition.
type Test struct {
	// setupStage is a special stage in the plan used for initializing the test.
	// Steps are run sequentially as declared, and no failure injection is attempted.
	setupStage *Stage
	stages     []*Stage
	// afterTestStage is like setupStage but run after the test is completed.
	afterTestStage *Stage
	options        TestOptions

	// Test execution context
	ctx       context.Context
	logger    *logger.Logger
	cluster   cluster.Cluster
	crdbNodes option.NodeListOption

	// hookIDCounter is a global counter that increments for every new testStep added
	hookIDCounter int
}

type TestOptions struct {
	defaultStepConcurrency int
	isLocal                bool
	debugMode              bool
	debugVerbosity         int
	debugModules           debugModules
	// cleanupOnFailure controls whether cluster state cleanup is performed on failure.
	// Default false (no cleanup) since cleanup is primarily for testing purposes.
	cleanupOnFailure bool
}

// NewTest creates a new modular test.
func NewTest(
	ctx context.Context,
	l *logger.Logger,
	c cluster.Cluster,
	crdbNodes option.NodeListOption,
	opts ...TestOption,
) *Test {
	options := TestOptions{
		defaultStepConcurrency: 3,
	}

	// Apply all provided options
	for _, opt := range opts {
		opt(&options)
	}

	return &Test{
		setupStage:     nil,
		stages:         make([]*Stage, 0),
		afterTestStage: nil,
		options:        options,
		ctx:            ctx,
		logger:         l,
		cluster:        c,
		crdbNodes:      crdbNodes,
		hookIDCounter:  0,
	}
}

// NewPlanner finalizes the DAG (start.e. randomly selects compatible failure injection
// operations to run for each stage) and returns a test planner.
func (t *Test) NewPlanner() TestPlanner {
	for stageIdx := range t.stages {
		t.AddFailureInjection(t.stages[stageIdx])
	}
	var combinedStages []Stage
	if t.setupStage != nil {
		AssignStepOrder(t.setupStage)
		combinedStages = append(combinedStages, *t.setupStage)
	}
	for stageIdx := range t.stages {
		AssignStepOrder(t.stages[stageIdx])
		combinedStages = append(combinedStages, *t.stages[stageIdx])
	}
	if t.afterTestStage != nil {
		AssignStepOrder(t.afterTestStage)
		combinedStages = append(combinedStages, *t.afterTestStage)
	}

	rng, seed := randutil.NewLockedPseudoRand()

	return TestPlanner{
		seed:             seed,
		rng:              rng,
		stages:           combinedStages,
		ctx:              t.ctx,
		logger:           t.logger,
		cluster:          t.cluster,
		crdbNodes:        t.crdbNodes,
		debugModules:     t.options.debugModules,
		cleanupOnFailure: t.options.cleanupOnFailure,
	}
}

func (t *Test) AddFailureInjection(s *Stage) {
	if s.failureInjectionDisabled {
		return
	}
	// TODO: Randomly select up to n failure injection operations
	// and add them as chains to the stage.
	return
}

// AssignStepOrder walks through the steps in a
// stage and assigns the finalized step orders.
func AssignStepOrder(s *Stage) {
	for chainID, ch := range s.chains {
		for depth, group := range ch {
			for stepIdx := range group {
				group[stepIdx].position = stepPosition{
					chainID: chainID,
					depth:   depth,
				}
			}
		}
	}
}

// nextHookID returns the next unique hookID for a new testStep.
func (t *Test) nextHookID() int {
	t.hookIDCounter++
	return t.hookIDCounter
}

// EnableCleanupOnFailure enables cluster state cleanup on test failure.
// By default, cleanup is disabled since it's primarily for testing purposes.
func (t *Test) EnableCleanupOnFailure() {
	t.options.cleanupOnFailure = true
}

// InsertRandomOperations randomly selects and inserts operations into a stage.
// This uses InsertOperation under the hood for each randomly selected operation.
//
// Parameters:
//   - stage: The stage to insert operations into
//   - candidateOps: Pool of operations to choose from
//   - numToInsert: Number of operations to insert
//   - rng: Random number generator for making random decisions
//
// Returns the list of operations that were successfully inserted.
func (t *Test) InsertRandomOperations(
	stage *Stage,
	candidateOps []Operation,
	numToInsert int,
	rng *rand.Rand,
) ([]Operation, error) {
	if numToInsert <= 0 {
		return nil, nil
	}

	if numToInsert > len(candidateOps) {
		numToInsert = len(candidateOps)
	}

	// Randomly select operations
	indices := rng.Perm(len(candidateOps))[:numToInsert]
	selected := make([]Operation, numToInsert)
	for i, idx := range indices {
		selected[i] = candidateOps[idx]
	}

	// Shuffle for random insertion order
	rng.Shuffle(len(selected), func(i, j int) {
		selected[i], selected[j] = selected[j], selected[i]
	})

	var inserted []Operation
	for _, op := range selected {
		t.AddOperation(stage, op)
		inserted = append(inserted, op)
	}

	return inserted, nil
}
