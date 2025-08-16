package modular

import (
	"fmt"
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
	verticalNodeSpacing    = 3
	horizontalNodeSpacing  = 5
	nodeWidth              = 21
	nodeHeight             = 5
	stageTransitionSpacing = 7
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
		connectionPoints = grid.drawStage(stage, currentY)
		for _, point := range connectionPoints {
			if point[1] > currentY {
				currentY = point[1]
			}
		}
		currentY += 1
	}

	return grid.render()
}

// gridWidth calculates the required g.
func gridDimensions(stages []Stage) (int, int) {
	var maxConcurrentChains int
	var width, height int
	for i, stage := range stages {
		// We need to transition from the last stage's to this stage, unless it
		// is the first stage, then we special case adding just one row for the
		// stage label.
		if i == 0 {
			height += 1
		} else {
			height += stageTransitionSpacing
		}
		if len(stage.chains) > maxConcurrentChains {
			maxConcurrentChains = len(stage.chains)
		}

		// The height of a stage is determined by the longest chain in that stage.
		var longestChainLength int
		for _, c := range stage.chains {
			if len(c) > longestChainLength {
				longestChainLength = len(c)
			}
		}
		// Each chain requires one node's height plus spacing, except the last one.
		height += longestChainLength*(nodeHeight*verticalNodeSpacing) - verticalNodeSpacing
	}
	// Grid width is based on the maximum number of concurrent step chains at any point of the plan.
	// Each chain requires one node's width plus spacing, except the last one.
	width = maxConcurrentChains*(nodeWidth+horizontalNodeSpacing) - horizontalNodeSpacing

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
func (g *dagGrid) drawStage(stage Stage, startY int) [][2]int {
	// Calculate the total width needed for all chains
	totalChainsWidth := len(stage.chains)*(nodeWidth+horizontalNodeSpacing) - horizontalNodeSpacing

	// Calculate starting X to center all chains on the grid width wise.
	xMidpoint := g.width / 2
	startX := xMidpoint - totalChainsWidth/2

	connectionPoints := make([][2]int, len(stage.chains))

	for chainIdx, stageChain := range stage.chains {
		for stepIdx, step := range stageChain {
			// Connect the previous step in the chain to this step.
			if stepIdx > 0 {
				xConnection := connectionPoints[chainIdx][0]
				yConnection := connectionPoints[chainIdx][1]

				// Draw vertical line down from previous step to current step
				for i := range verticalNodeSpacing - 1 {
					g.runes[yConnection+i][xConnection] = '│'
				}
				g.runes[yConnection+verticalNodeSpacing-1][xConnection] = '▼'
			}

			// Calculate the top left position of the node.
			nodeX := startX + chainIdx*(nodeWidth+horizontalNodeSpacing)
			nodeY := startY + stepIdx*(nodeHeight+verticalNodeSpacing)

			// Draw the box for the step
			g.drawBox(nodeX, nodeY, nodeWidth, nodeHeight, step.Description())
			connectionPoints[chainIdx] = [2]int{nodeX + nodeWidth/2, nodeY + nodeHeight}
		}
	}

	return connectionPoints
}

func (g *dagGrid) drawBox(x, y, width, height int, text string) {
	// Draw top border
	if y < g.height && x < g.width {
		g.runes[y][x] = '┌'
	}
	for i := 1; i < width-1; i++ {
		if y < g.height && x+i < g.width {
			g.runes[y][x+i] = '─'
		}
	}
	if y < g.height && x+width-1 < g.width {
		g.runes[y][x+width-1] = '┐'
	}

	// Draw sides and content
	lines := wrapText(text, width-2)
	for i := 1; i < height-1; i++ {
		if y+i < g.height {
			if x < g.width {
				g.runes[y+i][x] = '│'
			}
			if x+width-1 < g.width {
				g.runes[y+i][x+width-1] = '│'
			}

			// Add text content
			if i-1 < len(lines) {
				line := lines[i-1]
				for j, char := range line {
					if x+1+j < g.width {
						g.runes[y+i][x+1+j] = char
					}
				}
			}
		}
	}

	// Draw bottom border
	if y+height-1 < g.height && x < g.width {
		g.runes[y+height-1][x] = '└'
	}
	for i := 1; i < width-1; i++ {
		if y+height-1 < g.height && x+i < g.width {
			g.runes[y+height-1][x+i] = '─'
		}
	}
	if y+height-1 < g.height && x+width-1 < g.width {
		g.runes[y+height-1][x+width-1] = '┘'
	}
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

	numConcurrentChains := len(currentStage.chains)
	if numConcurrentChains == 1 {
		g.runes[lowestConnectionY+6][xMidpoint] = '│'
		g.runes[lowestConnectionY+7][xMidpoint] = '▼'
		return
	}

	totalChainsWidth := numConcurrentChains*(nodeWidth+horizontalNodeSpacing) - horizontalNodeSpacing
	startingX := xMidpoint - totalChainsWidth/2

	nodeXMidpoints := make([]int, numConcurrentChains)
	for chainIdx := range currentStage.chains {
		// The starting x offset plus
		if chainIdx == 0 {
			nodeXMidpoints[chainIdx] = startingX + (nodeWidth / 2)
		} else {
			nodeXMidpoints[chainIdx] = nodeXMidpoints[chainIdx-1] + nodeWidth + horizontalNodeSpacing
		}
	}

	for _, x := range nodeXMidpoints {
		g.runes[lowestConnectionY+6][x] = '┼'
	}
	for x := startingX + (nodeWidth / 2); x < startingX+totalChainsWidth-(nodeWidth/2); x++ {
		if g.runes[lowestConnectionY+6][x] == ' ' {
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
