package modular

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
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

// Test represents a modular test definition.
type Test struct {
	name              string
	seed              int64
	rng               *rand.Rand
	clusters          []cluster.Cluster
	clusterSpecs      []ClusterSpec
	workloadSpecs     []WorkloadSpec
	setupSteps        []testStep
	stages            []*Stage
	afterTestSteps    []testStep
	currentStageIndex int
	isLocal           bool
	testingKnobs      *TestingKnobs
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
		setupSteps:        make([]testStep, 0),
		stages:            make([]*Stage, 0),
		afterTestSteps:    make([]testStep, 0),
		currentStageIndex: 0,
	}
}

func (t *Test) WithCreateClusterFn(fn func() cluster.Cluster) {
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

	clusterInstance := t.createCluster(spec.Name, spec.ActualNodes, spec.DeploymentMode)

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
func (t *Test) Setup(fn stepFunc) {
	step := &singleStep{
		description: "setup step",
		fn:          fn,
		background:  nil,
	}
	t.setupSteps = append(t.setupSteps, step)
}

// NewStage creates a new stage for organizing test steps.
func (t *Test) NewStage(name string, opts ...StageOption) *Stage {
	stage := &Stage{
		name:        name,
		index:       t.currentStageIndex,
		steps:       make([]testStep, 0),
		repeatCount: 1,
		delay:       0,
	}

	for _, opt := range opts {
		opt(stage)
	}

	t.stages = append(t.stages, stage)
	t.currentStageIndex++
	return stage
}

// InStage adds a step to be executed in the specified stage.
func (t *Test) InStage(stage *Stage, fn stepFunc, opts ...StepOption) {
	step := &singleStep{
		description: fmt.Sprintf("stage %s step", stage.name),
		fn:          fn,
		background:  nil,
	}

	for _, opt := range opts {
		opt(step)
	}

	stage.steps = append(stage.steps, step)
}

// AfterTest adds a step that runs after all test stages are complete.
func (t *Test) AfterTest(fn stepFunc) {
	step := &singleStep{
		description: "after test step",
		fn:          fn,
		background:  nil,
	}
	t.afterTestSteps = append(t.afterTestSteps, step)
}

// TestPlan represents the execution plan for a modular test.
type TestPlan struct {
	name          string
	seed          int64
	clusterSpecs  []ClusterSpec
	workloadSpecs []WorkloadSpec
	setup         []testStep
	stages        []*Stage
	afterTest     []testStep
	isLocal       bool
}

// String pretty prints the plan, with the test name, seed listed at the top,
// along with each step in order.
func (tp *TestPlan) String() string {
	var b strings.Builder

	// Header with test name and seed
	b.WriteString(fmt.Sprintf("Modular Test Plan: %s (seed: %d)\n", tp.name, tp.seed))
	b.WriteString(strings.Repeat("=", 50) + "\n\n")

	// Cluster specifications
	if len(tp.clusterSpecs) > 0 {
		b.WriteString("Cluster Specifications:\n")
		for i, spec := range tp.clusterSpecs {
			b.WriteString(fmt.Sprintf("  %d. %s: %d nodes (%d-%d range)", i+1, spec.Name, spec.ActualNodes, spec.MinNodes, spec.MaxNodes))
			b.WriteString(fmt.Sprintf(" [mode: %s]", spec.DeploymentMode))
			if len(spec.DisabledDeploymentModes) > 0 {
				b.WriteString(fmt.Sprintf(" (disabled: %v)", spec.DisabledDeploymentModes))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Workload specifications
	if len(tp.workloadSpecs) > 0 {
		b.WriteString("Workload Specifications:\n")
		for i, spec := range tp.workloadSpecs {
			b.WriteString(fmt.Sprintf("  %d. %s: %d nodes\n", i+1, spec.name, spec.numNodes))
		}
		b.WriteString("\n")
	}

	// Setup steps
	if len(tp.setup) > 0 {
		b.WriteString("Setup Steps:\n")
		for i, step := range tp.setup {
			b.WriteString(fmt.Sprintf("  %d. %s", i+1, step.Description()))
			if step.Background() != nil {
				b.WriteString(" (background)")
			}
			if step.ConcurrencyDisabled() {
				b.WriteString(" (sequential)")
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Test stages
	if len(tp.stages) > 0 {
		b.WriteString("Test Stages:\n")
		for _, stage := range tp.stages {
			// Stage header
			b.WriteString(fmt.Sprintf("  Stage %d: %s", stage.index+1, stage.name))
			if stage.repeatCount > 1 {
				b.WriteString(fmt.Sprintf(" (repeat %d times", stage.repeatCount))
				if stage.delay > 0 {
					b.WriteString(fmt.Sprintf(", delay: %v", stage.delay))
				}
				b.WriteString(")")
			}
			b.WriteString("\n")

			// Stage steps
			for i, step := range stage.steps {
				b.WriteString(fmt.Sprintf("    %d. %s", i+1, step.Description()))
				if step.Background() != nil {
					b.WriteString(" (background)")
				}
				if step.ConcurrencyDisabled() {
					b.WriteString(" (sequential)")
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	// After-test steps
	if len(tp.afterTest) > 0 {
		b.WriteString("After-Test Steps:\n")
		for i, step := range tp.afterTest {
			b.WriteString(fmt.Sprintf("  %d. %s", i+1, step.Description()))
			if step.Background() != nil {
				b.WriteString(" (background)")
			}
			if step.ConcurrencyDisabled() {
				b.WriteString(" (sequential)")
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Footer
	if tp.isLocal {
		b.WriteString("Mode: Local\n")
	} else {
		b.WriteString("Mode: Distributed\n")
	}

	return b.String()
}

// Stage represents a group of test steps that can be executed concurrently.
type Stage struct {
	name        string
	index       int
	steps       []testStep
	repeatCount int
	delay       time.Duration
}

// Steps returns the steps in this stage.
func (s *Stage) Steps() []testStep {
	return s.steps
}

// GeneratePlan creates a TestPlan from the test definition.
func (t *Test) GeneratePlan() *TestPlan {
	return &TestPlan{
		name:          t.name,
		seed:          t.seed,
		clusterSpecs:  t.clusterSpecs,
		workloadSpecs: t.workloadSpecs,
		setup:         t.setupSteps,
		stages:        t.stages,
		afterTest:     t.afterTestSteps,
		isLocal:       t.isLocal,
	}
}

// TestPlan accessor methods for testing
func (tp *TestPlan) Name() string {
	return tp.name
}

func (tp *TestPlan) Seed() int64 {
	return tp.seed
}

func (tp *TestPlan) ClusterSpecs() []ClusterSpec {
	return tp.clusterSpecs
}

func (tp *TestPlan) WorkloadSpecs() []WorkloadSpec {
	return tp.workloadSpecs
}

func (tp *TestPlan) Setup() []testStep {
	return tp.setup
}

func (tp *TestPlan) Stages() []*Stage {
	return tp.stages
}

func (tp *TestPlan) AfterTest() []testStep {
	return tp.afterTest
}

func (tp *TestPlan) IsLocal() bool {
	return tp.isLocal
}
