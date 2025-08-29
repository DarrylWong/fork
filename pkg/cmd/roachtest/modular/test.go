package modular

import (
	"context"
	"fmt"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"math/rand"
)

// stepFunc is the signature for user-provided test steps.
type stepFunc func(context.Context, *logger.Logger, *Helper) error

// shouldStop is a channel that signals when a background step should stop.
type shouldStop chan struct{}

// TestingKnobs allows tests to inject custom behavior.
type TestingKnobs struct {
	// CreateClusterFn allows tests to override cluster creation.
	// If nil, the real cluster creation will be used (currently TODO).
	CreateClusterFn func() cluster.Cluster
}

// Helper provides utilities for modular test steps.
type Helper struct {
	clusters         map[string]Cluster
	workloadClusters map[string]*WorkloadCluster
	rng              *rand.Rand
}

// StepBuilder allows method chaining for building step sequences.
type StepBuilder struct {
	test  *Test
	stage *Stage
}

// Then adds another step that runs after this one in sequence.
func (sb *StepBuilder) Then(stepName string, fn stepFunc, opts ...StepOption) *StepBuilder {
	nextStep := &singleStep{
		description: stepName,
		fn:          fn,
		background:  nil,
	}

	// Apply step options
	for _, opt := range opts {
		opt(nextStep)
	}

	// Add the step as a new stepGroup in the chain
	if len(sb.stage.chains) == 0 {
		// Create a new chain if none exists
		sb.stage.chains = append(sb.stage.chains, chain{stepGroup{nextStep}})
	} else {
		// Append to the last chain as a new stepGroup
		lastChainIndex := len(sb.stage.chains) - 1
		sb.stage.chains[lastChainIndex] = append(sb.stage.chains[lastChainIndex], stepGroup{nextStep})
	}

	return sb
}

// And adds a step that can run in parallel with the previous step.
// All steps added via .And() will run in parallel within the same stepGroup.
func (sb *StepBuilder) And(stepName string, fn stepFunc, opts ...StepOption) *StepBuilder {
	newStep := &singleStep{
		description: stepName,
		fn:          fn,
		background:  nil,
	}

	// Apply step options
	for _, opt := range opts {
		opt(newStep)
	}

	if len(sb.stage.chains) == 0 {
		panic("no chain found to add an And() step to")
	}

	lastChainIndex := len(sb.stage.chains) - 1
	lastChain := sb.stage.chains[lastChainIndex]
	if len(lastChain) == 0 {
		panic("no step group found to add an And() step to")
	}
	// Add to the last stepGroup
	lastStepGroupIndex := len(lastChain) - 1
	sb.stage.chains[lastChainIndex][lastStepGroupIndex] = append(lastChain[lastStepGroupIndex], newStep)

	return sb
}

// Test represents a modular test definition.
type Test struct {
	name              string
	seed              int64
	rng               *rand.Rand
	clusters          []cluster.Cluster
	clusterSpecs      []ClusterSpec
	workloadSpecs     []WorkloadSpec
	setupStage        *Stage
	stages            []*Stage
	afterTestStage    *Stage
	currentStageIndex int
	isLocal           bool
	testingKnobs      *TestingKnobs
}

// ExecutionStrategy defines how steps should be executed within a stage.
type ExecutionStrategy int

const (
	ConcurrentExecution  ExecutionStrategy = iota // All steps run concurrently
	InterleavedExecution                          // Steps are randomly interleaved
)

// ExecutionStep represents a single step with a unique ID in the execution plan.
type ExecutionStep struct {
	ID          int
	Step        testStep
	ChainID     int   // Which step chain this belongs to (-1 for individual steps)
	ChainIndex  int   // Position within the step chain
	CanRunAfter []int // Step IDs that must complete before this step can run
}

// StageExecutionPlan contains the detailed execution plan for a stage.
type StageExecutionPlan struct {
	Stage            *Stage
	Strategy         ExecutionStrategy
	ExecutionSteps   []*ExecutionStep
	ConcurrentGroups [][]int // Groups of step IDs that can run concurrently
}

// NewTest creates a new modular test.
func NewTest(name string, seed int64) *Test {
	return &Test{
		name:              name,
		seed:              seed,
		rng:               rand.New(rand.NewSource(seed)),
		clusters:          make([]cluster.Cluster, 0),
		clusterSpecs:      make([]ClusterSpec, 0),
		workloadSpecs:     make([]WorkloadSpec, 0),
		setupStage:        nil, // Created lazily when first setup step is added
		stages:            make([]*Stage, 0),
		afterTestStage:    nil, // Created lazily when first after-test step is added
		currentStageIndex: 0,
	}
}

func (t *Test) WithCreateClusterFn(fn func() cluster.Cluster) {
	if t.testingKnobs == nil {
		t.testingKnobs = &TestingKnobs{}
	}
	t.testingKnobs.CreateClusterFn = fn
}

// createCluster creates a cluster using either the testing knobs or the real implementation .
func (t *Test) createCluster(name string, nodes int) cluster.Cluster {
	if t.testingKnobs != nil && t.testingKnobs.CreateClusterFn != nil {
		return t.testingKnobs.CreateClusterFn()
	}

	// TODO: Real cluster creation would go here.
	// This would call roachprod to create an actual cluster with the specified configuration.
	// For now, return nil as a placeholder.
	return nil
}

// selectDeploymentMode randomly selects an allowed deployment mode.
func (t *Test) selectDeploymentMode(disabledModes []DeploymentMode) DeploymentMode {
	allModes := []DeploymentMode{
		SystemOnlyDeployment,
		SharedProcessDeployment,
		SeparateProcessDeployment,
	}

	// Filter out disabled modes
	allowedModes := make([]DeploymentMode, 0)
	for _, mode := range allModes {
		disabled := false
		for _, disabledMode := range disabledModes {
			if mode == disabledMode {
				disabled = true
				break
			}
		}
		if !disabled {
			allowedModes = append(allowedModes, mode)
		}
	}

	if len(allowedModes) == 0 {
		// Default to system-only if all modes are disabled
		return SystemOnlyDeployment
	}

	return allowedModes[t.rng.Intn(len(allowedModes))]
}

// AddCluster adds a cluster specification to the test, performs randomization,
// and creates the actual cluster.
func (t *Test) AddCluster(opts ...ClusterOption) cluster.Cluster {
	spec := &ClusterSpec{
		Name:                    fmt.Sprintf("cluster-%d", len(t.clusterSpecs)),
		DisabledDeploymentModes: make([]DeploymentMode, 0),
		MinNodes:                3,
		MaxNodes:                3,
	}

	for _, opt := range opts {
		opt(spec)
	}

	// Perform randomization
	if spec.MinNodes == spec.MaxNodes {
		spec.ActualNodes = spec.MinNodes
	} else {
		spec.ActualNodes = spec.MinNodes + t.rng.Intn(spec.MaxNodes-spec.MinNodes+1)
	}

	spec.DeploymentMode = t.selectDeploymentMode(spec.DisabledDeploymentModes)

	clusterInstance := t.createCluster(spec.Name, spec.ActualNodes)

	// Store the spec and cluster
	t.clusterSpecs = append(t.clusterSpecs, *spec)
	t.clusters = append(t.clusters, clusterInstance)

	return clusterInstance
}

// AddWorkloadCluster adds a workload cluster specification to the test.
func (t *Test) AddWorkloadCluster(opts ...WorkloadOption) cluster.Cluster {
	spec := &WorkloadSpec{
		name:     fmt.Sprintf("workload-%d", len(t.workloadSpecs)),
		numNodes: 1,
	}

	for _, opt := range opts {
		opt(spec)
	}

	return t.createCluster(spec.name, spec.numNodes)
}

// Setup adds a setup step that runs before the test begins.
func (t *Test) Setup(stepName string, fn stepFunc, opts ...StepOption) {
	step := &singleStep{
		description: stepName,
		fn:          fn,
		background:  nil,
	}

	// Apply step options
	for _, opt := range opts {
		opt(step)
	}

	// Create setup stage if it doesn't exist
	if t.setupStage == nil {
		t.setupStage = &Stage{
			name:   "setup",
			chains: make([]chain, 1),
		}
		// Start with an empty chain
		t.setupStage.chains[0] = chain{}
	}

	// Add each setup step as a new stepGroup (sequential execution)
	t.setupStage.chains[0] = append(t.setupStage.chains[0], stepGroup{step})
}

// NewStage creates a new stage for organizing test steps.
func (t *Test) NewStage(name string, opts ...StageOption) *Stage {
	stage := &Stage{
		name:   name,
		index:  t.currentStageIndex,
		chains: make([]chain, 0),
	}

	for _, opt := range opts {
		opt(stage)
	}

	t.stages = append(t.stages, stage)
	t.currentStageIndex++
	return stage
}

// InStage adds a step to be executed in the specified stage.
func (t *Test) InStage(stage *Stage, stepName string, fn stepFunc, opts ...StepOption) *StepBuilder {
	step := &singleStep{
		description: stepName,
		fn:          fn,
		background:  nil,
	}

	for _, opt := range opts {
		opt(step)
	}

	// Add step as a new chain with a single stepGroup to the stage
	stage.chains = append(stage.chains, chain{stepGroup{step}})

	return &StepBuilder{
		test:  t,
		stage: stage,
	}
}

// AfterTest adds a step that runs after all test stages are complete.
func (t *Test) AfterTest(stepName string, fn stepFunc, opts ...StepOption) {
	step := &singleStep{
		description: stepName,
		fn:          fn,
		background:  nil,
	}

	// Apply step options
	for _, opt := range opts {
		opt(step)
	}

	// Create after-test stage if it doesn't exist
	if t.afterTestStage == nil {
		t.afterTestStage = &Stage{
			name:   "after-test",
			chains: make([]chain, 1),
		}
		// Start with an empty chain
		t.afterTestStage.chains[0] = chain{}
	}

	// Add each after-test step as a new stepGroup (sequential execution)
	t.afterTestStage.chains[0] = append(t.afterTestStage.chains[0], stepGroup{step})
}

// DAG generates a directed acyclic graph representation of all test steps and their dependencies.
func (t *Test) DAG() string {
	var stages []Stage

	if t.setupStage != nil {
		stages = append(stages, *t.setupStage)
	}

	for _, stage := range t.stages {
		stages = append(stages, *stage)
	}

	// Add after-test stage
	if t.afterTestStage != nil {
		stages = append(stages, *t.afterTestStage)
	}

	return GenerateDAG(stages)
}
