package modular

// StepBuilder allows method chaining for building step sequences.
type StepBuilder struct {
	test  *Test
	stage *Stage
}

func newTestStep(test *Test, stepName string, fn stepFunc, opts ...StepOption) testStep {
	return testStep{
		StepProtocol: NewSingleStep(stepName, fn, opts...),
		hookID:       test.nextHookID(),
	}
}

// Setup adds a setup step that runs before the test begins.
func (t *Test) Setup(stepName string, fn stepFunc, opts ...StepOption) {
	ts := newTestStep(t, stepName, fn, opts...)

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
	t.setupStage.chains[0] = append(t.setupStage.chains[0], stepGroup{ts})
}

// AfterTest adds a step that runs after all test stages are complete.
func (t *Test) AfterTest(stepName string, fn stepFunc, opts ...StepOption) {
	ts := newTestStep(t, stepName, fn, opts...)

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
	t.afterTestStage.chains[0] = append(t.afterTestStage.chains[0], stepGroup{ts})
}

// NewStage creates a new stage for organizing test steps.
func (t *Test) NewStage(name string, opts ...StageOption) *Stage {
	stage := &Stage{
		name:               name,
		chains:             make([]chain, 0),
		maxStepConcurrency: t.options.defaultStepConcurrency,
	}

	for _, opt := range opts {
		opt(stage)
	}

	t.stages = append(t.stages, stage)
	return stage
}

// InStage adds a step to be executed in the specified stage.
func (t *Test) InStage(stage *Stage, stepName string, fn stepFunc, opts ...StepOption) *StepBuilder {
	ts := newTestStep(t, stepName, fn, opts...)

	// Add step as a new chain with a single stepGroup to the stage
	stage.chains = append(stage.chains, chain{stepGroup{ts}})

	return &StepBuilder{
		test:  t,
		stage: stage,
	}
}

// Then adds another step that runs after this one in sequence.
func (sb *StepBuilder) Then(stepName string, fn stepFunc, opts ...StepOption) *StepBuilder {
	ts := newTestStep(sb.test, stepName, fn, opts...)

	// Add the step as a new stepGroup in the chain
	if len(sb.stage.chains) == 0 {
		// Create a new chain if none exists
		sb.stage.chains = append(sb.stage.chains, chain{stepGroup{ts}})
	} else {
		// Append to the last chain as a new stepGroup
		lastChainIndex := len(sb.stage.chains) - 1
		sb.stage.chains[lastChainIndex] = append(sb.stage.chains[lastChainIndex], stepGroup{ts})
	}

	return sb
}

// And adds a step that can run in parallel with the previous step.
// All steps added via .And() will run in parallel within the same stepGroup.
func (sb *StepBuilder) And(stepName string, fn stepFunc, opts ...StepOption) *StepBuilder {
	ts := newTestStep(sb.test, stepName, fn, opts...)

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
	sb.stage.chains[lastChainIndex][lastStepGroupIndex] = append(lastChain[lastStepGroupIndex], ts)

	return sb
}
