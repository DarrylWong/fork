package modular

import (
	"fmt"
	"strings"
)

const (
	// nodeWidth is the character width of a given node.
	nodeWidth = 25
	// nodeHeight is the character height of a given node.
	nodeHeight = 5

	// horizontalPadding is the padding on the left and right sides of a box.
	horizontalPadding = 2
)

// The following functions draw individual components of the DAG
// using a provided Put function. The Put function abstracts away
// the details of where specific characters are drawn in the overall
// grid, instead only needing to know where characters go relative to
// the standardized node size.

// drawStepNode draws a step in the DAG, represented as a box
// with the step description centered within it.
func drawStepNode(description string, Put func(x, y int, r rune)) {
	// Draw the top border of the box.
	Put(horizontalPadding, 0, '┌')
	for x := horizontalPadding + 1; x < nodeWidth-horizontalPadding-1; x++ {
		Put(x, 0, '─')
	}
	Put(nodeWidth-horizontalPadding-1, 0, '┐')

	// Draw sides of the box  and the step description.
	lines := wrapText(description, nodeWidth-4*horizontalPadding, nodeHeight)
	for y := 1; y < nodeHeight-1; y++ {
		Put(horizontalPadding, y, '│')
		Put(nodeWidth-horizontalPadding-1, y, '│')

		// Add text content
		if y-1 < len(lines) {
			line := lines[y-1]
			for x, char := range line {
				Put(2*horizontalPadding+x, y, char)
			}
		}
	}

	// Draw bottom border of the box.
	Put(horizontalPadding, nodeHeight-1, '└')
	for x := horizontalPadding + 1; x < nodeWidth-horizontalPadding-1; x++ {
		Put(x, nodeHeight-1, '─')
	}
	Put(nodeWidth-horizontalPadding-1, nodeHeight-1, '┘')
}

// drawStageLabel draws a stage label centered within the node.
func drawStageLabel(name string, Put func(x, y int, r rune)) {
	label := fmt.Sprintf("[%s]", name)
	startX := (nodeWidth - len(label)) / 2

	for i, char := range label {
		Put(startX+i, nodeHeight-1, char)
	}
}

// drawUpperDependencyNode draws the upper half of a dependency line between steps.
func drawUpperDependencyNode(Put func(x, y int, r rune)) {
	xMid := nodeWidth / 2
	for y := 0; y < nodeHeight/2; y++ {
		Put(xMid, y, '│')
	}
}

// drawLowerDependencyNode draws the lower half of a dependency line between steps.
func drawLowerDependencyNode(Put func(x, y int, r rune)) {
	xMid := nodeWidth / 2
	for y := nodeHeight / 2; y < nodeHeight; y++ {
		if y == nodeHeight-1 {
			Put(xMid, y, '▼')
		} else {
			Put(xMid, y, '│')
		}
	}
}

// drawMergeLeftNode draws the left edge of a merge point in the DAG where we join many nodes to a center point.
func drawMergeLeftNode(Put func(x, y int, r rune)) {
	xMid := nodeWidth / 2
	var y int
	for y = 0; y < nodeHeight-4; y++ {
		Put(xMid, y, '│')
	}

	// Draw the merge point with intersection
	Put(xMid, y, '┼')

	// Fill in horizontal line to the right
	for x := xMid + 1; x < nodeWidth; x++ {
		Put(x, y, '─')
	}
}

// drawMergeCenterNode draws the center portion of a merge point in the DAG where we join many nodes to a center point.
func drawMergeCenterNode(Put func(x, y int, r rune)) {
	xMid := nodeWidth / 2
	var y int
	for y = 0; y < nodeHeight-4; y++ {
		Put(xMid, y, '│')
	}

	// Draw the merge point with intersection
	Put(xMid, y, '┼')

	// Fill in horizontal line to both sides
	for x := 0; x < xMid; x++ {
		Put(x, y, '─')
	}
	for x := xMid + 1; x < nodeWidth; x++ {
		Put(x, y, '─')
	}
}

// drawMergeRightNode draws the right edge of a merge point in the DAG where we join many nodes to a center point.
func drawMergeRightNode(Put func(x, y int, r rune)) {
	xMid := nodeWidth / 2
	var y int
	for y = 0; y < nodeHeight-4; y++ {
		Put(xMid, y, '│')
	}

	// Draw the merge point with intersection
	Put(xMid, y, '┼')

	// Fill in horizontal line to the left
	for x := 0; x < xMid; x++ {
		Put(x, y, '─')
	}
}

// drawSplitLeftNode draws the left edge of a split point in the DAG where we branch one node to many.
func drawSplitLeftNode(Put func(x, y int, r rune)) {
	xMid := nodeWidth / 2

	// Draw the split point with intersection
	Put(xMid, 3, '┼')

	// Fill in horizontal line to the right
	for x := xMid + 1; x < nodeWidth; x++ {
		Put(x, 3, '─')
	}

	// Draw vertical line down to the step
	for y := 4; y < nodeHeight; y++ {
		Put(xMid, y, '│')
	}

	// Draw arrow at the end
	Put(xMid, nodeHeight-1, '▼')
}

// drawSplitCenterNode draws the center portion of a split point in the DAG where we branch one node to many.
func drawSplitCenterNode(Put func(x, y int, r rune)) {
	xMid := nodeWidth / 2

	// Draw the split point with intersection
	Put(xMid, 3, '┼')

	// Fill in horizontal line to both sides
	for x := 0; x < xMid; x++ {
		Put(x, 3, '─')
	}
	for x := xMid + 1; x < nodeWidth; x++ {
		Put(x, 3, '─')
	}

	// Draw vertical line down to the step
	for y := 4; y < nodeHeight; y++ {
		Put(xMid, y, '│')
	}

	// Draw arrow at the end
	Put(xMid, nodeHeight-1, '▼')
}

// drawSplitRightNode draws the right edge of a split point in the DAG where we branch one node to many.
func drawSplitRightNode(Put func(x, y int, r rune)) {
	xMid := nodeWidth / 2

	// Draw the split point with intersection
	Put(xMid, 3, '┼')

	// Fill in horizontal line to the left
	for x := 0; x < xMid; x++ {
		Put(x, 3, '─')
	}

	// Draw vertical line down to the step
	for y := 4; y < nodeHeight; y++ {
		Put(xMid, y, '│')
	}

	// Draw arrow at the end
	Put(xMid, nodeHeight-1, '▼')
}

// DAGBuilder constructs a text-based DAG visualization.
type DAGBuilder struct {
	grid [][]rune // [y][x] indexed

	// Keeps track of the current y offset of the grid, the DAGBuilder
	// draws from top to bottom.
	yOffset int
}

// NewDAGBuilder creates a new DAGBuilder with the given width and height.
func NewDAGBuilder(width, height int) DAGBuilder {
	builder := DAGBuilder{
		grid: make([][]rune, height),
	}
	for i := range builder.grid {
		builder.grid[i] = make([]rune, width)
		for j := range builder.grid[i] {
			builder.grid[i][j] = ' '
		}
	}
	return builder
}

// PutFunc returns a function that puts runes at the given offset in the grid.
func (b *DAGBuilder) PutFunc(xOffset, yOffset int) func(x, y int, r rune) {
	// Our grid is indexed as [y][x], but we usually think in [x][y] coordinates,
	// so swap them here.
	return func(x, y int, r rune) {
		// Special case the intersection of vertical and horizontal lines to use '┼'
		// instead of overwriting.
		switch b.grid[yOffset+y][xOffset+x] {
		case '│':
			if r == '─' {
				r = '┼'
			}
		case '─':
			if r == '│' {
				r = '┼'
			}
		}

		b.grid[yOffset+y][xOffset+x] = r
	}
}

// GridWidth returns the width of the grid.
func (b *DAGBuilder) GridWidth() int {
	return len(b.grid[0])
}

// String converts the DAGBuilder grid to a string representation.
func (b *DAGBuilder) String() string {
	var result strings.Builder
	for _, row := range b.grid {
		line := string(row)
		// Trim trailing spaces
		line = strings.TrimRight(line, " ")
		result.WriteString(line)
		result.WriteString("\n")
	}

	return result.String()
}

func (b *DAGBuilder) drawStageLabel(stageName string) {
	startX := b.GridWidth()/2 - nodeWidth/2
	drawStageLabel(stageName, b.PutFunc(startX, b.yOffset))
	b.yOffset += nodeHeight
}

func (b *DAGBuilder) drawStage(chains []Chain) {
	// Convenience struct for tracking coordinates.
	type coordinate struct {
		x, y int
	}

	// Calculate the total width needed for all chains
	stageWidth := maxConcurrentSteps(chains) * nodeWidth

	// Calculate starting X to center all chains on the grid width wise.
	stageXOffset := b.GridWidth()/2 - stageWidth/2

	// Keep track of the current leaf nodes for each group and their coordinates.
	// This is used to connect groups across rows.
	leafNodes := make(map[int][]coordinate, len(chains))
	for rowIdx, row := range DAGRows(chains) {
		newLeafNodes := make(map[int][]coordinate, len(chains))
		for _, group := range row.Groups {
			// First, we need to connect the previous group's nodes to this group's nodes.
			connectionPoints := leafNodes[group.ChainID]

			var groupXOffset int
			if len(connectionPoints) > 0 {
				// Center this group under the connection points.
				centerX := (connectionPoints[0].x + connectionPoints[len(connectionPoints)-1].x + nodeWidth) / 2
				groupXOffset = centerX - (len(group.Steps)*nodeWidth)/2
			} else {
				// If there are no connection points (i.e. the first row), we instead center
				// the group within the chain's allocated space (i.e. the chain's max width).
				groupXOffset = stageXOffset + row.Offset(group)*nodeWidth + (group.MaxChainConcurrentSteps*nodeWidth-len(group.Steps)*nodeWidth)/2
			}
			groupXCenter := groupXOffset + len(group.Steps)*nodeWidth/2

			// If it's the first row in the stage, we don't have any connections to draw.
			if rowIdx > 0 {
				// If we have multiple leaf nodes in the previous group, we need to draw merge connectors.
				if len(connectionPoints) > 1 {
					for i, leafNode := range connectionPoints {
						if i == 0 {
							drawMergeLeftNode(b.PutFunc(leafNode.x, b.yOffset))
						} else if i == len(connectionPoints)-1 {
							drawMergeRightNode(b.PutFunc(leafNode.x, b.yOffset))
						} else {
							drawMergeCenterNode(b.PutFunc(leafNode.x, b.yOffset))
						}
					}
				} else {
					// Otherwise, we draw a straight line.
					drawUpperDependencyNode(b.PutFunc(connectionPoints[0].x, b.yOffset))
				}

				// If we have multiple nodes in the current group, we need to draw split connectors.
				if len(group.Steps) > 1 {
					for i := 0; i < len(group.Steps); i++ {
						xOffset := groupXOffset + i*nodeWidth
						if i == 0 {
							drawSplitLeftNode(b.PutFunc(xOffset, b.yOffset))
						} else if i == len(group.Steps)-1 {
							drawSplitRightNode(b.PutFunc(xOffset, b.yOffset))
						} else {
							drawSplitCenterNode(b.PutFunc(xOffset, b.yOffset))
						}
					}
				} else {
					// Otherwise, we draw a straight line.
					drawLowerDependencyNode(b.PutFunc(groupXOffset, b.yOffset))
				}

				// Then, we need to connect the upper and lower connectors with a vertical line.
				b.PutFunc(groupXCenter, b.yOffset+nodeHeight/2)(0, 0, '│')
			}

			// Now draw the steps in this group.
			for i := 0; i < len(group.Steps); i++ {
				xOffset := groupXOffset + i*nodeWidth
				stepYOffset := b.yOffset
				if rowIdx > 0 {
					stepYOffset += nodeHeight
				}
				drawStepNode(group.Steps[i].Description(), b.PutFunc(xOffset, stepYOffset))
				// Record the leaf node position for the next row's connections.
				newLeafNodes[group.ChainID] = append(newLeafNodes[group.ChainID], coordinate{
					x: xOffset,
					y: stepYOffset + nodeHeight,
				})
			}
		}
		if rowIdx > 0 {
			b.yOffset += 2 * nodeHeight
		} else {
			b.yOffset += nodeHeight
		}
		leafNodes = newLeafNodes
	}
}

// DAGHeight calculates the required height for rendering the given stages.
func DAGHeight(stages []*Stage) int {
	height := 0
	for _, s := range stages {
		height += nodeHeight                 // Stage label
		height += (s.depth + 1) * nodeHeight // Steps
		height += (s.depth) * nodeHeight     // Dependencies between steps
	}
	return height
}

// DAGWidth calculates the required width for rendering based on grid width.
func DAGWidth(gridWidth int) int {
	return gridWidth * nodeWidth
}

// renderDAG renders a slice of stages into a text-based DAG visualization.
func renderDAG(stages []*Stage) (string, error) {
	// It's easier to render our DAG if we can process it from left to
	// right, top to bottom. Converting our stages into "chains" lets
	// us establish this ordering.
	var allChains [][]Chain
	var gridWidth int
	for _, s := range stages {
		chains := StageToChains(s)
		gridWidth = max(gridWidth, maxConcurrentSteps(chains))
		allChains = append(allChains, chains)
	}

	builder := NewDAGBuilder(DAGWidth(gridWidth), DAGHeight(stages))
	for i, chains := range allChains {
		// Draw the name of the stage.
		builder.drawStageLabel(stages[i].name)

		// Draw the stage itself.
		builder.drawStage(chains)
	}

	return builder.String(), nil
}
