package modular

// StageOption configures a Stage.
type StageOption func(*Stage)

// StepOption configures a singleStep.
type StepOption func(*singleStep)

// InBackground configures a step to run in the background.
func InBackground() StepOption {
	return func(step *singleStep) {
		step.background = make(shouldStop)
	}
}
