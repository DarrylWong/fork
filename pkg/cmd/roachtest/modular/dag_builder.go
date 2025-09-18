package modular

import (
	"fmt"
	"iter"
	"strings"
)

type dagGrid struct {
	runes         [][]rune
	width, height int
}

func (g *dagGrid) init(width, height int) {
	g.width = width
	g.height = height
	g.runes = make([][]rune, height)
	for i := range g.runes {
		g.runes[i] = make([]rune, width)
		for j := range g.runes[i] {
			g.runes[i][j] = ' '
		}
	}
}

const (
	verticalNodeSpacing    = 5
	horizontalNodeSpacing  = 5
	nodeWidth              = 21
	nodeHeight             = 5
	stageTransitionSpacing = 8
)

func GenerateDAG(stages []Stage) string {
	// First, calculate the dimensions needed for our grid.
	width, height := gridDimensions(stages)
	var grid dagGrid
	grid.init(width, height)
	gridXMidpoint := width / 2
	currentY := 0
	var connectionPoints [][2]int
	for i, stage := range stages {
		// Draw stage transitions.
		if i == 0 {
			// The first stage is the root node, which does not need a transition,
			// but still needs a stage label.
			grid.drawStageLabel(stage.name, gridXMidpoint, currentY)
			currentY += 1
		} else {
			grid.drawStageTransition(stage, connectionPoints, gridXMidpoint)
			currentY += stageTransitionSpacing
		}
		connectionPoints = grid.drawStage(stage, currentY, i == len(stages)-1 /* last stage */)
		for _, point := range connectionPoints {
			if point[1] > currentY {
				currentY = point[1]
			}
		}
	}

	return grid.render()
}

// gridWidth calculates the required g.
func gridDimensions(stages []Stage) (int, int) {
	var maxTotalWidth int
	var width, height int
	for i, stage := range stages {
		// We need to transition from the last stage's to this stage, unless it
		// is the first stage, then we special case adding just one row for the
		// stage label.
		if i == 0 {
			height += 1
		} else {
			// Add spacing for a potential group transition at the end of each stage.
			height += stageTransitionSpacing + 2
		}

		// Calculate the total width needed for this stage, accounting for parallel steps
		maxConcurrentSteps := stage.MaxConcurrentSteps()
		stageWidth := maxConcurrentSteps * (nodeWidth + horizontalNodeSpacing)
		if stageWidth > maxTotalWidth {
			maxTotalWidth = stageWidth
		}

		// The height of a stage is determined by the longest Chain in that stage.
		// Each Chain requires one node's height plus spacing, except the last one.
		height += stage.LongestChain()*(nodeHeight+verticalNodeSpacing) - verticalNodeSpacing
	}

	width = maxTotalWidth
	return width, height
}

// wrapText wraps text to fit within a given width
func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{}
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{}
	}

	var lines []string
	var currentLine string
	maxLines := 3 // Maximum lines for a box

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

// drawStageLabel draws a stage label at the given x, y position.
func (g *dagGrid) drawStageLabel(stageName string, x, y int) {
	label := fmt.Sprintf("[%s]", stageName)
	// Truncate the label if it's too long
	if len(label) > g.width {
		label = label[:g.width-3] + "..."
	}
	startX := x - len(label)/2
	for i, char := range label {
		g.runes[y][startX+i] = char
	}
}

// drawStage draws all steps in a stage starting at startY.
// It returns the x, y connection points for the next stage transition.
func (g *dagGrid) drawStage(stage Stage, startY int, lastStage bool) [][2]int {
	// Calculate the total width needed for all chains
	stageWidth := stage.MaxConcurrentSteps()*(nodeWidth+horizontalNodeSpacing) - horizontalNodeSpacing

	// Calculate starting X to center all chains on the grid width wise.
	xMidpoint := g.width / 2
	startX := xMidpoint - stageWidth/2

	connectionPoints := make([][2]int, len(stage.chains))

	for rowIdx, row := range DAGRows(&stage) {
		for _, group := range row.Groups {
			// Connect the previous group in the Chain to this group
			xConnection := connectionPoints[group.ChainID][0]
			if xConnection == 0 {
				xConnection = startX + row.Offset(group)*(nodeWidth+horizontalNodeSpacing) + (group.MaxChainConcurrentSteps*(nodeWidth+horizontalNodeSpacing)-horizontalNodeSpacing)/2
			}
			yConnection := connectionPoints[group.ChainID][1]
			groupStartingX := xConnection - (len(group.Steps)*(nodeWidth+horizontalNodeSpacing)-horizontalNodeSpacing)/2
			groupStartingY := startY + rowIdx*(nodeHeight+verticalNodeSpacing)

			if rowIdx > 0 {
				if len(group.Steps) == 1 && g.runes[yConnection-1][groupStartingX+(nodeWidth/2)] == '─' {
					// If we are connecting from one node to one node,
					// we can draw a simple vertical line down.
					g.runes[yConnection][groupStartingX+(nodeWidth/2)] = '│'
					g.runes[yConnection+1][groupStartingX+(nodeWidth/2)] = '│'
					g.runes[yConnection+2][groupStartingX+(nodeWidth/2)] = '│'
					g.runes[yConnection+3][groupStartingX+(nodeWidth/2)] = '│'
					g.runes[yConnection+4][groupStartingX+(nodeWidth/2)] = '▼'
				} else if len(group.Steps) == 1 {
					// If we are connecting from multiple nodes to one node,
					// we already have convergence lines drawn, so we draw less
					// vertical lines.
					g.runes[yConnection][groupStartingX+(nodeWidth/2)] = '│'
					g.runes[yConnection+1][groupStartingX+(nodeWidth/2)] = '│'
					g.runes[yConnection+2][groupStartingX+(nodeWidth/2)] = '▼'
				} else if len(group.Steps) > 1 && g.runes[yConnection-1][xConnection] == '─' {
					// If we are connecting from one node to multiple nodes,
					// we have no convergence lines drawn, so need to draw them here.
					g.runes[yConnection][xConnection] = '│'
					g.runes[yConnection+1][xConnection] = '│'
					nodeXMidpoints := make([]int, len(group.Steps))
					for stepIdx := range group.Steps {
						nodeXMidpoints[stepIdx] = groupStartingX + stepIdx*(nodeWidth+horizontalNodeSpacing) + (nodeWidth / 2)
						g.runes[yConnection+2][nodeXMidpoints[stepIdx]] = '┼'
					}
					groupWidth := len(group.Steps)*(nodeWidth+horizontalNodeSpacing) - horizontalNodeSpacing
					for x := groupStartingX + (nodeWidth / 2); x < groupStartingX+groupWidth-(nodeWidth/2); x++ {
						if g.runes[yConnection+2][x] == ' ' {
							g.runes[yConnection+2][x] = '─'
						}
					}
					for _, x := range nodeXMidpoints {
						g.runes[yConnection+3][x] = '│'
						g.runes[yConnection+4][x] = '▼'
					}
				} else {
					// If we are connecting from multiple nodes to multiple nodes,
					// we already have convergence lines drawn, so we draw less
					// vertical lines.
					g.runes[yConnection][xConnection] = '│'
					nodeXMidpoints := make([]int, len(group.Steps))
					for stepIdx := range group.Steps {
						nodeXMidpoints[stepIdx] = groupStartingX + stepIdx*(nodeWidth+horizontalNodeSpacing) + (nodeWidth / 2)
						g.runes[yConnection+1][nodeXMidpoints[stepIdx]] = '┼'
					}
					groupWidth := len(group.Steps)*(nodeWidth+horizontalNodeSpacing) - horizontalNodeSpacing
					for x := groupStartingX + (nodeWidth / 2); x < groupStartingX+groupWidth-(nodeWidth/2); x++ {
						if g.runes[yConnection+1][x] == ' ' {
							g.runes[yConnection+1][x] = '─'
						}
					}
					for _, x := range nodeXMidpoints {
						g.runes[yConnection+2][x] = '▼'
					}
				}
			}
			if len(group.Steps) == 1 {
				connectionPoints[group.ChainID] = g.drawSingleStep(group.Steps[0], groupStartingX, groupStartingY)
			} else {
				// Don't draw transition lines for the last stage if this is the last group in the Chain.
				drawTransition := !(lastStage && len(stage.chains[group.ChainID]) == rowIdx+1)
				connectionPoints[group.ChainID] = g.drawParallelSteps(group.Steps, groupStartingX, groupStartingY, drawTransition)
			}
		}
	}

	return connectionPoints
}

// drawSingleStep draws a single step and returns its connection point
func (g *dagGrid) drawSingleStep(step testStep, startX, startY int) [2]int {
	g.drawBox(startX, startY, nodeWidth, nodeHeight, step.Description())
	return [2]int{startX + nodeWidth/2, startY + nodeHeight}
}

// drawParallelSteps draws multiple parallel steps with convergence lines and returns the connection point
func (g *dagGrid) drawParallelSteps(group stepGroup, startX, startY int, shouldDrawTransition bool) [2]int {
	numSteps := len(group)
	// Draw a box for each step and record their center positions.
	stepCenters := make([]int, numSteps)
	for i, step := range group {
		nodeX := startX + i*(nodeWidth+horizontalNodeSpacing)
		g.drawBox(nodeX, startY, nodeWidth, nodeHeight, step.Description())
		stepCenters[i] = nodeX + nodeWidth/2
	}

	chainMidpointX := startX + (numSteps*(nodeWidth+horizontalNodeSpacing)-horizontalNodeSpacing)/2

	if !shouldDrawTransition {
		return [2]int{chainMidpointX, startY + nodeHeight}
	}
	return g.drawGroupTransition(stepCenters, chainMidpointX, startY+nodeHeight)
}

// drawGroupTransition draws the lines between parallel steps in a group.
func (g *dagGrid) drawGroupTransition(stepCenters []int, midpointX, startY int) [2]int {
	// Draw vertical lines down from each step
	for _, centerX := range stepCenters {
		g.runes[startY][centerX] = '│'
		g.runes[startY+1][centerX] = '┼'
	}

	// Draw horizontal line connecting all steps
	if len(stepCenters) >= 2 {
		leftmostX := stepCenters[0]
		rightmostX := stepCenters[len(stepCenters)-1]
		for x := leftmostX; x <= rightmostX; x++ {
			if g.runes[startY+1][x] == ' ' {
				g.runes[startY+1][x] = '─'
			}
		}
	}
	g.runes[startY+1][midpointX] = '┼'

	return [2]int{midpointX, startY + 2}
}

func (g *dagGrid) drawBox(x, y, width, height int, text string) {
	// Draw top border
	g.runes[y][x] = '┌'
	for i := 1; i < width-1; i++ {
		g.runes[y][x+i] = '─'
	}
	g.runes[y][x+width-1] = '┐'

	// Draw sides and content
	lines := wrapText(text, width-2)
	for i := 1; i < height-1; i++ {
		if y+i < g.height {
			g.runes[y+i][x] = '│'
			g.runes[y+i][x+width-1] = '│'

			// Add text content
			if i-1 < len(lines) {
				line := lines[i-1]
				for j, char := range line {
					g.runes[y+i][x+1+j] = char
				}
			}
		}
	}

	// Draw bottom border
	g.runes[y+height-1][x] = '└'
	for i := 1; i < width-1; i++ {
		g.runes[y+height-1][x+i] = '─'
	}
	g.runes[y+height-1][x+width-1] = '┘'
}

// drawStageTransition draws connections from the previous stage to the current stage.
// A transition consists of 2 vertical rows of spacing from the lowest connection point,
// followed by 3 rows of spacing to add the stage label, then another 2 rows of spacing
// before the next stage.
func (g *dagGrid) drawStageTransition(currentStage Stage, connectionPoints [][2]int, xMidpoint int) {
	// First, find the lowest connection point from the previous stage.
	lowestConnectionY := 0
	for _, point := range connectionPoints {
		if point[1] > lowestConnectionY {
			lowestConnectionY = point[1]
		}
	}

	// Then draw vertical lines down from each connection point to lowestConnectionY + 2.
	for _, point := range connectionPoints {
		x := point[0]
		for y := point[1]; y <= lowestConnectionY+1; y++ {
			g.runes[y][x] = '│'
		}
		if len(connectionPoints) == 1 {
			g.runes[lowestConnectionY+2][x] = '│'
		} else {
			g.runes[lowestConnectionY+2][x] = '┼'
		}
	}
	for x := connectionPoints[0][0]; x < connectionPoints[len(connectionPoints)-1][0]; x++ {
		if g.runes[lowestConnectionY+2][x] == ' ' {
			g.runes[lowestConnectionY+2][x] = '─'
		}
	}

	g.runes[lowestConnectionY+3][xMidpoint] = '│'
	g.drawStageLabel(currentStage.name, xMidpoint, lowestConnectionY+4)
	g.runes[lowestConnectionY+5][xMidpoint] = '│'

	totalChainsWidth := currentStage.MaxConcurrentSteps()*(nodeWidth+horizontalNodeSpacing) - horizontalNodeSpacing
	startingX := xMidpoint - totalChainsWidth/2

	var nodeXMidpoints []int
	DAGRows(&currentStage)(func(idx int, row DAGRowIter) bool {
		for _, group := range row.Groups {
			groupXMidpoint := startingX + row.Offset(group)*(nodeWidth+horizontalNodeSpacing) + (group.MaxChainConcurrentSteps*(nodeWidth+horizontalNodeSpacing)-horizontalNodeSpacing)/2
			groupStartingX := groupXMidpoint - (len(group.Steps)*(nodeWidth+horizontalNodeSpacing)-horizontalNodeSpacing)/2
			for stepIdx := range group.Steps {
				nodeXMidpoints = append(nodeXMidpoints, groupStartingX+stepIdx*(nodeWidth+horizontalNodeSpacing)+(nodeWidth/2))
			}
		}
		return false
	})

	if len(nodeXMidpoints) == 1 {
		g.runes[lowestConnectionY+6][xMidpoint] = '│'
		g.runes[lowestConnectionY+7][xMidpoint] = '▼'
		return
	}
	midpointSet := make(map[int]struct{})
	for _, x := range nodeXMidpoints {
		midpointSet[x] = struct{}{}
	}
	for x := nodeXMidpoints[0]; x <= nodeXMidpoints[len(nodeXMidpoints)-1]; x++ {
		if _, ok := midpointSet[x]; ok {
			g.runes[lowestConnectionY+6][x] = '┼'
		} else {
			g.runes[lowestConnectionY+6][x] = '─'
		}
	}
	for _, x := range nodeXMidpoints {
		g.runes[lowestConnectionY+7][x] = '▼'
	}
}

// render converts the rune grid to a string
func (g *dagGrid) render() string {
	var result strings.Builder

	for _, row := range g.runes {
		line := string(row)
		// Trim trailing spaces
		line = strings.TrimRight(line, " ")
		result.WriteString(line)
		result.WriteString("\n")
	}

	return result.String()
}

type DAGGroup struct {
	ChainID                 int
	MaxChainConcurrentSteps int
	Steps                   []testStep
}
type DAGRowIter struct {
	Groups []DAGGroup
}

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

func DAGRows(s *Stage) iter.Seq2[int, DAGRowIter] {
	return func(yield func(int, DAGRowIter) bool) {
		numRows := 0
		for _, ch := range s.chains {
			if n := len(ch); n > numRows {
				numRows = n
			}
		}

		for rowIdx := 0; rowIdx < numRows; rowIdx++ {
			var row DAGRowIter

			for chainID, ch := range s.chains {
				if rowIdx >= len(ch) {
					continue
				}
				grp := ch[rowIdx]

				steps := make([]testStep, len(grp))
				copy(steps, grp)

				row.Groups = append(row.Groups, DAGGroup{
					ChainID:                 chainID,
					MaxChainConcurrentSteps: ch.MaxConcurrentSteps(),
					Steps:                   steps,
				})
			}

			if !yield(rowIdx, row) {
				return
			}
		}
	}
}
