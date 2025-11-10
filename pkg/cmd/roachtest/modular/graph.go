package modular

// The possible permutations of a modular test can be represented
// as a directed acyclic graph (DAG). A DAG is constructed from the
// following hierarchical components:
// 1. Stage: A group of one or more chains that a common start and end dependency.
//		A convenience grouping for organizing a test plan into distinct phases. Stages
//		in a DAG are executed sequentially.
// 2. Chain: A group of related steps that that have dependencies on each other.
//		A dependency represents the (potentially partial) ordering that steps must be
//		run in. Different operations have no shared dependencies other than trivial
//		root dependencies (i.e. stage start and end).
// 3. Step: The smallest unit of work in a modular test plan that won't be
//		further subdivided/preempted.
//
//                                Stage
//┌──────────────────────────────────────────────────────────────────────┐
//│                                                                      │
//│                                                                      │
//│                        ┌───────┐         ┌───────┐                   │
//│        ┌─────────────> │ StepA │         │ StepD │<─────────┐        │
//│        │               └───┬───┘         └───┬───┘          │        │
//│        │  dependency─────> │            ┌────┴────┐         │Chain 2 │
//│        │                   ▼            ▼         ▼         │        │
//│        │               ┌───────┐    ┌───────┐ ┌───────┐     │        │
//│ Chain 1│               │ StepB │    │ StepE │ │ StepF │<────┘        │
//│        │               └───┬───┘    └───────┘ └───────┘              │
//│        │                   │                                         │
//│        │                   ▼                                         │
//│        │               ┌───────┐                                     │
//│        └─────────────> │ StepC │                                     │
//│                        └───────┘                                     │
//│                                                                      │
//└──────────────────────────────────────────────────────────────────────┘

// stage represents one or more chains that all converge at the start and
// end of the stage.
type stage struct {
	name   string
	index  int
	chains []chain
	opts   stageOpts
}

// StageOption configures a stage.
type StageOption func(*stageOpts)

type stageOpts struct {
	failureInjectionDisabled bool
	// The maximum number of steps that can be run concurrently in this stage.
	maxStepConcurrency int
}

// chain represents a sequence of steps that must be executed in order.
type chain []stepGroup

// stepGroup is an internal implementation detail representing steps with
// in the same chain with the same dependencies, i.e. they can be run
// in any order/concurrently.
type stepGroup []step

// step represents the smallest unit of work in a modular plan.
type step struct {
	StepProtocol
	// runID represents the final execution order this step will be run in.
	runID int
	opts  stepOpts
}

// StepOption configures a step.
type StepOption func(*stepOpts)

type stepOpts struct {
}
