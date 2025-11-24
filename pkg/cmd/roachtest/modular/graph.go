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
//		root dependencies (i.e. Stage start and end).
// 3. Step: The smallest unit of work in a modular test plan that won't be
//		further subdivided/preempted.
//
//                               Stage 1
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
//└─────────────────────────────────┬────────────────────────────────────┘
//                                  │
//                                  ▼
//                               Stage 2
//┌──────────────────────────────────────────────────────────────────────┐
//│                                                                      │
//                                 ...

// Stage represents one or more chains that all converge at the start and
// end of the Stage.
type Stage struct {
	name  string
	index int
	roots []*Step
	// stepMap maps step names to their corresponding Steps. Used for unit tests and
	// as an escape hatch for more complex DAG dependencies not supported by the Builder API.
	// Assumes step names are unique within a stage.
	stepMap map[string]*Step
	opts    stageOpts
}

// StageOption configures a Stage.
type StageOption func(*stageOpts)
type stageOpts struct{}

// Step represents the smallest unit of work in a modular plan.
type Step struct {
	StepProtocol
	// nodeID is the unique ID assigned to the step in the context of the DAG.
	nodeID int
	// children is the slice of steps that depend on this step, i.e. this step must be run before all children
	children []*Step
	// parents is the slice of steps that this step depends on, i.e. all parents must be run before this step
	parents []*Step
	// level is the topological level of the step in the DAG. Lazily computed after DAG is finalized.
	level int
	opts  stepOpts
}

// StepOption configures a Step.
type StepOption func(*stepOpts)
type stepOpts struct{}
