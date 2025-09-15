package modular

import "github.com/cockroachdb/cockroach/pkg/roachprod/logger"

type TestOption func(options *TestOptions)
type debugModule string

const (
	ClusterStateDebug debugModule = "cluster-state"
	RunnerDebug       debugModule = "runner"
	PlannerDebug      debugModule = "planner"
)

type debugModules map[debugModule]bool

func (d debugModules) NewLogger(parent *logger.Logger, module debugModule) *logger.Logger {
	var newLogger *logger.Logger
	if d != nil && d[module] {
		// Create a non-quiet logger that logs to modular_debug.txt
		newLogger, _ = parent.ChildLogger(string(module))
	} else {
		// Create a quiet logger that still logs to file but not to stdout/stderr
		newLogger, _ = parent.ChildLogger(string(module), logger.QuietStdout, logger.QuietStderr)
	}
	return newLogger
}

// WithDebug enables debug logging for specific modules.
func WithDebug(modules ...debugModule) TestOption {
	return func(options *TestOptions) {
		if options.debugModules == nil {
			options.debugModules = make(map[debugModule]bool)
		}
		for _, module := range modules {
			options.debugModules[module] = true
		}
	}
}

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
