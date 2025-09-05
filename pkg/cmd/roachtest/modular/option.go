package modular

// StageOption configures a Stage.
type StageOption func(*Stage)

// WithStepConcurrency sets the maximum number of steps that can run concurrently in a stage.
func WithStepConcurrency(concurrency int) StageOption {
	return func(stage *Stage) {
		stage.maxStepConcurrency = concurrency
	}
}

// StepOption configures a singleStep.
type StepOption func(*singleStep)

// InBackground configures a step to run in the background.
func InBackground() StepOption {
	return func(step *singleStep) {
		step.background = make(shouldStop)
	}
}
