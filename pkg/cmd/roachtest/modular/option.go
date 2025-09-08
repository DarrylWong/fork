package modular

type TestOption func(options *TestOptions)

// StageOption configures a Stage.
type StageOption func(*Stage)

// WithStepConcurrency sets the maximum number of steps that can run concurrently in a stage.
func WithStepConcurrency(concurrency int) StageOption {
	return func(stage *Stage) {
		stage.maxStepConcurrency = concurrency
	}
}

// StepOption configures a SingleStep.
type StepOption func(*SingleStep)

// InBackground configures a step to run in the background.
func InBackground() StepOption {
	return func(step *SingleStep) {
		step.background = make(shouldStop)
	}
}

func DisableConcurrency() StepOption {
	return func(step *SingleStep) {
		step.concurrencyDisabled = true
	}
}
