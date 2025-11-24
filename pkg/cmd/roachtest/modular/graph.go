package modular

import (
	"fmt"
	"slices"

	"github.com/cockroachdb/errors"
)

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
	// The depth of the stage's DAG, i.e. the length of the longest path from
	// a root step to a leaf step. Lazily computed after stage.Finalize() is called.
	depth int
	opts  stageOpts
}

// find returns the Step with the given step name, or an error if not found.
func (s *Stage) find(stepName string) (*Step, error) {
	step, ok := s.stepMap[stepName]
	if !ok {
		return nil, fmt.Errorf("step %q not found in stage %q", stepName, s.name)
	}
	return step, nil
}

// Finalize finalizes the stage's DAG by assigning root steps and levels. It also ensures we
// have a valid DAG.
func (s *Stage) Finalize() error {
	rootNodes := make([]*Step, 0)

	// Assign any root nodes (steps with indegree 0).
	for _, step := range s.stepMap {
		if len(step.parents) == 0 {
			rootNodes = append(rootNodes, step)
		}
	}
	if len(rootNodes) == 0 {
		return errors.New("stage is an invalid DAG: no root nodes found")
	}

	// Sort root nodes by nodeID so we have a deterministic way to traverse our graph.
	slices.SortFunc(rootNodes, func(a, b *Step) int {
		return a.nodeID - b.nodeID
	})

	s.roots = rootNodes

	// Assign levels to each step in the DAG.
	if err := s.assignLevels(); err != nil {
		return err
	}

	return nil
}

func (s *Stage) assignLevels() error {
	indegrees := make(map[*Step]int)
	for _, step := range s.stepMap {
		indegrees[step] = len(step.parents)
	}

	// Initialize queue with all steps that have indegree 0, i.e. have no child dependencies.
	queue := make([]*Step, 0, len(s.roots))
	queue = append(queue, s.roots...)

	maxLevel := 0
	numSteps := 0
	for len(queue) > 0 {
		curr := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		numSteps++

		for _, child := range curr.children {
			// A node's level is defined as one more than the max level of its parents.
			if curr.level+1 > child.level {
				child.level = curr.level + 1
				maxLevel = max(maxLevel, child.level)
			}

			indegrees[child]--
			// If indegree becomes 0, then we have added all parent dependencies and
			// can add it to the queue.
			if indegrees[child] == 0 {
				queue = append(queue, child)
			}
		}
	}
	if numSteps != len(s.stepMap) {
		return errors.Errorf("unreachable step(s) detected in stage %q", s.name)
	}

	s.depth = maxLevel
	return nil
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
