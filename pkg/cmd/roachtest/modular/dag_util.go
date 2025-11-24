package modular

import (
	"iter"
	"strings"
)

// containsNonAdjacentEdges returns true if the stage contains any edges
// that connect non-adjacent levels.
// N.B. requires levels to have been computed first.
func (s *Stage) containsNonAdjacentEdges() bool {
	for _, parent := range s.stepMap {
		for _, child := range parent.children {
			if child.level != parent.level+1 {
				return true
			}
		}
	}
	return false
}

// Chain represents a sequence of step groups that have dependencies on each other.
// Each group contains steps at the same level within the chain.
type Chain [][]*Step

// DAGGroup represents a group of steps from a single chain at a specific row index.
type DAGGroup struct {
	ChainID                 int
	MaxChainConcurrentSteps int // Maximum concurrent steps across all groups in this chain
	Steps                   []*Step
}

// DAGRowIter represents a single row in the DAG, containing groups from multiple chains.
type DAGRowIter struct {
	Groups []DAGGroup
}

// wrapText wraps text to fit within a given width
func wrapText(text string, width int, height int) []string {
	if width <= 0 {
		return []string{}
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{}
	}

	var lines []string
	var currentLine string
	maxLines := height - 2

	for _, word := range words {
		if len(currentLine) == 0 {
			currentLine = word
		} else if len(currentLine)+1+len(word) <= width {
			currentLine += " " + word
		} else {
			lines = append(lines, centerText(currentLine, width))
			currentLine = word

			if len(lines) >= maxLines {
				break
			}
		}
	}

	if len(currentLine) > 0 && len(lines) < maxLines {
		lines = append(lines, centerText(currentLine, width))
	}

	return lines
}

// centerText centers text within a given width
func centerText(text string, width int) string {
	if len(text) >= width {
		return text[:width]
	}

	padding := width - len(text)
	leftPad := padding / 2
	rightPad := padding - leftPad

	return strings.Repeat(" ", leftPad) + text + strings.Repeat(" ", rightPad)
}

// ============================================================================
// Chain and DAG Functions
// ============================================================================

// maxConcurrentSteps returns the maximum number of steps that can be run concurrently at once.
// In other words, this is the max width our DAG will be for this slice of chains.
func maxConcurrentSteps(chains []Chain) int {
	maxSteps := 0
	for _, ch := range chains {
		maxGroupSize := 0
		for _, gr := range ch {
			if len(gr) > maxGroupSize {
				maxGroupSize = len(gr)
			}
		}
		maxSteps = maxSteps + maxGroupSize
	}

	return maxSteps
}

// Offset returns the horizontal offset for a given group within a row.
func (r *DAGRowIter) Offset(gr DAGGroup) int {
	offset := 0
	for _, g := range r.Groups {
		if g.ChainID == gr.ChainID {
			break
		}
		offset += g.MaxChainConcurrentSteps
	}
	return offset
}

// StageToChains converts a Stage DAG into chains by identifying connected components.
// A chain is a sequence of ordered steps (by level) with connected dependencies.
func StageToChains(s *Stage) []Chain {
	visited := make(map[*Step]bool)
	var chains []Chain

	// Find all connected components
	for _, root := range s.roots {
		// Skip if already visited
		if visited[root] {
			continue
		}

		chain := make(Chain, s.depth+1)
		queue := []*Step{root}
		visited[root] = true

		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			chain[current.level] = append(chain[current.level], current)

			// Add all parent steps to queue if not visited
			for _, parent := range current.parents {
				if !visited[parent] {
					visited[parent] = true
					queue = append(queue, parent)
				}
			}

			// Add all child steps to queue if not visited
			for _, child := range current.children {
				if !visited[child] {
					visited[child] = true
					queue = append(queue, child)
				}
			}
		}

		chains = append(chains, chain)
	}

	return chains
}

// DAGRows iterates over rows of the DAG, yielding groups from all chains at each row index.
// In other words, it yields steps from left to right as how they would appear in a DAG visualization.
func DAGRows(chains []Chain) iter.Seq2[int, DAGRowIter] {
	return func(yield func(int, DAGRowIter) bool) {
		numRows := 0
		for _, ch := range chains {
			if n := len(ch); n > numRows {
				numRows = n
			}
		}

		// Calculate the max concurrent steps for each chain
		maxChainSteps := make([]int, len(chains))
		for chainID, ch := range chains {
			maxGroupSize := 0
			for _, gr := range ch {
				if len(gr) > maxGroupSize {
					maxGroupSize = len(gr)
				}
			}
			maxChainSteps[chainID] = maxGroupSize
		}

		for rowIdx := 0; rowIdx < numRows; rowIdx++ {
			var row DAGRowIter

			for chainID, ch := range chains {
				if rowIdx >= len(ch) {
					continue
				}
				grp := ch[rowIdx]

				// Skip empty groups, this indicates the end of a chain.
				if len(grp) == 0 {
					continue
				}

				steps := make([]*Step, len(grp))
				copy(steps, grp)

				row.Groups = append(row.Groups, DAGGroup{
					ChainID:                 chainID,
					MaxChainConcurrentSteps: maxChainSteps[chainID],
					Steps:                   steps,
				})
			}

			if !yield(rowIdx, row) {
				return
			}
		}
	}
}
