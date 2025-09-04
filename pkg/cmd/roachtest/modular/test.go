package modular

import "math/rand"

// Test represents a modular test definition.
type Test struct {
	name string
	seed int64
	rng  *rand.Rand
	// setupStage is a special stage in the plan used for initializing the test.
	// Steps are run sequentially as declared, and no failure injection is attempted.
	setupStage *Stage
	stages     []*Stage
	// afterTestStage is like setupStage but run after the test is completed.
	afterTestStage *Stage
	options        TestOptions
}

type TestOptions struct {
	isLocal bool
}

// NewTest creates a new modular test.
func NewTest(name string, seed int64) *Test {
	return &Test{
		name:           name,
		seed:           seed,
		rng:            rand.New(rand.NewSource(seed)),
		setupStage:     nil,
		stages:         make([]*Stage, 0),
		afterTestStage: nil,
	}
}

// NewPlanner finalizes the DAG (i.e. randomly selects compatible failure injection
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

	return TestPlanner{
		name:   t.name,
		seed:   t.seed,
		rng:    t.rng,
		stages: combinedStages,
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
				group[stepIdx].order = stepOrder{
					chainID: chainID,
					depth:   depth,
				}
			}
		}
	}
}
