package modular

import (
	"github.com/cockroachdb/errors"
	// "strings"
	"strings"
)

func renderDAG(stages []*Stage) (string, error) {
	sb := &strings.Builder{}
	for _, s := range stages {
		layers, err := s.layers()
		if err != nil {
			return "", err
		}
		for _, l := range layers {
			drawLayer(sb, l)
		}
	}
	return sb.String(), nil
}

// sortedSteps returns all steps in the stage in a topological order.
func (s *Stage) sortedSteps() ([]*Step, error) {
	indegrees := make(map[*Step]int)
	for _, step := range s.stepMap {
		indegrees[step] = step.indegree
	}

	// Initialize queue with all steps that have indegree 0, i.e. have no child dependencies.
	queue := make([]*Step, 0, len(s.roots))
	queue = append(queue, s.roots...)

	var steps []*Step
	for len(queue) > 0 {
		curr := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		steps = append(steps, curr)

		for child := range curr.children {
			// A node's level is defined as one more than the max level of its parents.
			if curr.level+1 > child.level {
				child.level = curr.level + 1
			}

			indegrees[child]--
			// If indegree becomes 0, then we have added all parent dependencies and
			// can add it to the queue.
			if indegrees[child] == 0 {
				queue = append(queue, child)
			}
		}
	}

	if len(steps) != len(s.stepMap) {
		return nil, errors.Errorf("unreachable step(s) detected in stage %q", s.name)
	}

	return steps, nil
}

// Layers returns slices of steps grouped by their topological level.
// A step's level is defined as one more than the max level of its parents.
// A root step (step's with no parent dependencies) have level 0, i.e. first slice.
func (s *Stage) layers() ([][]*Step, error) {
	//sortedSteps, err := s.sortedSteps()
	//if err != nil {
	//	return nil, err
	//}

	var layers [][]*Step

	return layers, nil
}

func drawLayer(sb *strings.Builder, layer []*Step) {

}
