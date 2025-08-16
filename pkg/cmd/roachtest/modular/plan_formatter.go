package modular

import (
	"fmt"
	"strings"
)

// Tree formatting constants (similar to mixed version framework)
const (
	branchString       = "├──"
	nestedBranchString = "│   "
	lastBranchString   = "└──"
	lastBranchPadding  = "   "
)

// treeBranchString returns the appropriate tree branch character based on position
func treeBranchString(idx, sliceLen int) string {
	if idx == sliceLen-1 {
		return lastBranchString
	}
	return branchString
}

// buildTreePrefix builds the proper indentation prefix for nested tree items
func buildTreePrefix(prefix string) string {
	result := strings.ReplaceAll(prefix, branchString, nestedBranchString)
	result = strings.ReplaceAll(result, lastBranchString, lastBranchPadding)
	return result
}

// formatStep formats a step for display, handling both individual steps and step chains.
func formatStep(step testStep, indent string) string {
	var b strings.Builder
	
	if chain, ok := step.(*stepChain); ok {
		// This is a step chain - show as a sequence
		if len(chain.steps) == 0 {
			b.WriteString(indent + "empty step chain")
		} else {
			// Show the first step as the main entry
			b.WriteString(indent + chain.steps[0].Description())
			if chain.steps[0].Background() != nil {
				b.WriteString(" (background)")
			}
			if chain.steps[0].ConcurrencyDisabled() {
				b.WriteString(" (sequential)")
			}
			
			// Show subsequent steps as sub-steps
			for i := 1; i < len(chain.steps); i++ {
				b.WriteString("\n" + indent + "  ↳ " + chain.steps[i].Description())
				if chain.steps[i].Background() != nil {
					b.WriteString(" (background)")
				}
				if chain.steps[i].ConcurrencyDisabled() {
					b.WriteString(" (sequential)")
				}
			}
		}
	} else {
		// This is a single step
		b.WriteString(indent + step.Description())
		if step.Background() != nil {
			b.WriteString(" (background)")
		}
		if step.ConcurrencyDisabled() {
			b.WriteString(" (sequential)")
		}
	}
	
	return b.String()
}

// formatDetailedExecutionPlan formats the detailed execution plan for a stage using tree structure.
func (tp *TestPlan) formatDetailedExecutionPlan(b *strings.Builder, plan *StageExecutionPlan) {
	if len(plan.ConcurrentGroups) == 0 {
		return
	}
	
	// Create a map of step ID to execution step for quick lookup
	stepMap := make(map[int]*ExecutionStep)
	for _, execStep := range plan.ExecutionSteps {
		stepMap[execStep.ID] = execStep
	}
	
	// Format each group with tree structure
	for groupIdx, group := range plan.ConcurrentGroups {
		prefix := treeBranchString(groupIdx, len(plan.ConcurrentGroups))
		
		if len(group) == 1 {
			// Single step
			step := stepMap[group[0]]
			b.WriteString(fmt.Sprintf("%s %s (%d)", prefix, step.Step.Description(), step.ID))
			if step.Step.Background() != nil {
				b.WriteString(" (background)")
			}
			if step.Step.ConcurrencyDisabled() {
				b.WriteString(" (sequential)")
			}
			b.WriteString("\n")
		} else {
			// Concurrent group
			b.WriteString(fmt.Sprintf("%s run following steps concurrently\n", prefix))
			
			// Build nested prefix for concurrent steps
			nestedPrefix := buildTreePrefix(prefix)
			
			for stepIdx, stepID := range group {
				step := stepMap[stepID]
				stepPrefix := nestedPrefix + treeBranchString(stepIdx, len(group))
				b.WriteString(fmt.Sprintf("%s %s (%d)", stepPrefix, step.Step.Description(), step.ID))
				if step.Step.Background() != nil {
					b.WriteString(" (background)")
				}
				if step.Step.ConcurrencyDisabled() {
					b.WriteString(" (sequential)")
				}
				b.WriteString("\n")
			}
		}
	}
}

// formatStagesWithTree formats stages using tree structure similar to mixed version framework.
func (tp *TestPlan) formatStagesWithTree(b *strings.Builder) {
	if len(tp.stages) == 0 {
		return
	}
	
	// Track step ID across all stages - just assign sequentially as we go
	stepID := 1
	
	for stageIdx, stage := range tp.stages {
		// Stage header with tree formatting
		stagePrefix := treeBranchString(stageIdx, len(tp.stages))
		
		// Handle special stages differently
		if stage.name == "setup" {
			b.WriteString(fmt.Sprintf("%s Setup Steps\n", stagePrefix))
		} else if stage.name == "after-test" {
			b.WriteString(fmt.Sprintf("%s After-Test Steps\n", stagePrefix))
		} else {
			// Regular user stage
			stageNumber := stage.index + 1
			b.WriteString(fmt.Sprintf("%s Stage %d: %s\n", stagePrefix, stageNumber, stage.name))
		}

		// Format stage execution plan with proper tree indentation and assign step IDs
		executionPlanIdx := tp.getExecutionPlanIndex(stage)
		if executionPlanIdx >= 0 && executionPlanIdx < len(tp.stageExecutionPlans) && tp.stageExecutionPlans[executionPlanIdx] != nil {
			plan := tp.stageExecutionPlans[executionPlanIdx]
			stepID = tp.formatStageExecutionPlanWithTreeAndStepIDs(b, plan, buildTreePrefix(stagePrefix), stepID)
		} else {
			// Fallback to simple step display with tree formatting and sequential step IDs
			nestedPrefix := buildTreePrefix(stagePrefix)
			steps := stage.Steps()
			for stepIdx, step := range steps {
				stepPrefix := nestedPrefix + treeBranchString(stepIdx, len(steps))
				b.WriteString(fmt.Sprintf("%s %s (%d)", stepPrefix, step.Description(), stepID))
				if step.Background() != nil {
					b.WriteString(" (background)")
				}
				if step.ConcurrencyDisabled() {
					b.WriteString(" (sequential)")
				}
				b.WriteString("\n")
				stepID++
			}
		}
	}
}

// getExecutionPlanIndex returns the index in stageExecutionPlans for a given stage.
// Returns -1 if no execution plan exists (e.g., for setup/after-test stages).
func (tp *TestPlan) getExecutionPlanIndex(stage *Stage) int {
	// Setup and after-test stages don't have execution plans
	if stage.name == "setup" || stage.name == "after-test" {
		return -1
	}
	
	// In the unified approach, we need to find the stage's position in the stages array
	// and map it to the correct execution plan index
	for i, s := range tp.stages {
		if s == stage {
			return i
		}
	}
	
	return -1 // Stage not found
}

// formatStageExecutionPlanWithTree formats a stage execution plan with tree indentation.
func (tp *TestPlan) formatStageExecutionPlanWithTree(b *strings.Builder, plan *StageExecutionPlan, stagePrefix string) {
	if len(plan.ConcurrentGroups) == 0 {
		return
	}
	
	// Create a map of step ID to execution step for quick lookup
	stepMap := make(map[int]*ExecutionStep)
	for _, execStep := range plan.ExecutionSteps {
		stepMap[execStep.ID] = execStep
	}
	
	// Format each group with tree structure
	for groupIdx, group := range plan.ConcurrentGroups {
		prefix := stagePrefix + treeBranchString(groupIdx, len(plan.ConcurrentGroups))
		
		if len(group) == 1 {
			// Single step
			step := stepMap[group[0]]
			b.WriteString(fmt.Sprintf("%s %s (%d)", prefix, step.Step.Description(), step.ID))
			if step.Step.Background() != nil {
				b.WriteString(" (background)")
			}
			if step.Step.ConcurrencyDisabled() {
				b.WriteString(" (sequential)")
			}
			b.WriteString("\n")
		} else {
			// Concurrent group
			b.WriteString(fmt.Sprintf("%s run following steps concurrently\n", prefix))
			
			// Build nested prefix for concurrent steps
			nestedPrefix := buildTreePrefix(prefix)
			
			for stepIdx, stepID := range group {
				step := stepMap[stepID]
				stepPrefix := nestedPrefix + treeBranchString(stepIdx, len(group))
				b.WriteString(fmt.Sprintf("%s %s (%d)", stepPrefix, step.Step.Description(), step.ID))
				if step.Step.Background() != nil {
					b.WriteString(" (background)")
				}
				if step.Step.ConcurrencyDisabled() {
					b.WriteString(" (sequential)")
				}
				b.WriteString("\n")
			}
		}
	}
}

// formatStageExecutionPlanWithTreeAndStepIDs formats a stage execution plan with tree indentation and assigns step IDs.
func (tp *TestPlan) formatStageExecutionPlanWithTreeAndStepIDs(b *strings.Builder, plan *StageExecutionPlan, stagePrefix string, startStepID int) int {
	if len(plan.ConcurrentGroups) == 0 {
		return startStepID
	}
	
	currentStepID := startStepID
	
	// Create a map of step ID to execution step for quick lookup
	stepMap := make(map[int]*ExecutionStep)
	for _, execStep := range plan.ExecutionSteps {
		stepMap[execStep.ID] = execStep
	}
	
	// Format each group with tree structure and assign step IDs
	for groupIdx, group := range plan.ConcurrentGroups {
		prefix := stagePrefix + treeBranchString(groupIdx, len(plan.ConcurrentGroups))
		
		if len(group) == 1 {
			// Single step
			step := stepMap[group[0]]
			b.WriteString(fmt.Sprintf("%s %s (%d)", prefix, step.Step.Description(), currentStepID))
			if step.Step.Background() != nil {
				b.WriteString(" (background)")
			}
			if step.Step.ConcurrencyDisabled() {
				b.WriteString(" (sequential)")
			}
			b.WriteString("\n")
			currentStepID++
		} else {
			// Concurrent group
			b.WriteString(fmt.Sprintf("%s run following steps concurrently\n", prefix))
			
			// Build nested prefix for concurrent steps
			nestedPrefix := buildTreePrefix(prefix)
			
			for stepIdx, stepID := range group {
				step := stepMap[stepID]
				stepPrefix := nestedPrefix + treeBranchString(stepIdx, len(group))
				b.WriteString(fmt.Sprintf("%s %s (%d)", stepPrefix, step.Step.Description(), currentStepID))
				if step.Step.Background() != nil {
					b.WriteString(" (background)")
				}
				if step.Step.ConcurrencyDisabled() {
					b.WriteString(" (sequential)")
				}
				b.WriteString("\n")
				currentStepID++
			}
		}
	}
	
	return currentStepID
}

