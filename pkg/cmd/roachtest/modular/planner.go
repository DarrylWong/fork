package modular

import (
	"context"
	"fmt"
	"math/rand"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

type stagePlan struct {
	stage *stage
	steps []step
}

// planFunc converts a given stage into a legal ordering of steps to be executed,
// i.e. it returns a valid linearization of the DAG.
type planFunc func(s stage) (stagePlan, error)

type Planner struct {
	seed     int64
	rng      *rand.Rand
	stages   []stage
	planFunc planFunc

	ctx       context.Context
	logger    *logger.Logger
	cluster   cluster.Cluster
	crdbNodes option.NodeListOption
}

func (p *Planner) Plan() (*TestPlan, error) {
	var stagePlans []stagePlan
	for _, s := range p.stages {
		plan, err := p.planFunc(s)
		if err != nil {
			return nil, err
		}
		stagePlans = append(stagePlans, plan)
	}
	return &TestPlan{
		seed:       p.seed,
		rng:        p.rng,
		stagePlans: stagePlans,
		ctx:        p.ctx,
		logger:     p.logger,
		cluster:    p.cluster,
		crdbNodes:  p.crdbNodes,
	}, nil
}

func (p *Planner) DAG() string {
	return "unimplemented"
}

type TestPlan struct {
	seed       int64
	rng        *rand.Rand
	stagePlans []stagePlan

	// Test execution context
	ctx       context.Context
	logger    *logger.Logger
	cluster   cluster.Cluster
	crdbNodes option.NodeListOption
}

const (
	branchString       = "├──"
	nestedBranchString = "│   "
	lastBranchString   = "└──"
	lastBranchPadding  = "   "
)

func (p *TestPlan) String() string {
	var out strings.Builder

	// Header with metadata
	out.WriteString(fmt.Sprintf("Seed: %d\n\n", p.seed))
	out.WriteString("Plan:\n")

	// Print each stage with its steps
	for stageIdx, stagePlan := range p.stagePlans {
		stageName := stagePlan.stage.name
		if stageName == "" {
			stageName = fmt.Sprintf("stage %d", stageIdx+1)
		}

		stageBranch := getBranchPrefix(stageIdx, len(p.stagePlans))
		out.WriteString(fmt.Sprintf("%s %s\n", stageBranch, stageName))

		stageIndent := getStagePrefix(stageIdx, len(p.stagePlans))
		for stepIdx, step := range stagePlan.steps {
			stepPrefix := stageIndent + getBranchPrefix(stepIdx, len(stagePlan.steps))
			p.prettyPrintStep(&out, step, stepPrefix)
		}
	}

	return out.String()
}

func (p *TestPlan) prettyPrintStep(out *strings.Builder, s step, prefix string, extraContext ...string) {
	writeConcurrent := func(label string, concurrentStep *concurrentStep) {
		out.WriteString(fmt.Sprintf("%s %s\n", prefix, label))
		for i, subStep := range concurrentStep.steps {
			nestedPrefix := strings.ReplaceAll(prefix, branchString, nestedBranchString)
			nestedPrefix = strings.ReplaceAll(nestedPrefix, lastBranchString, lastBranchPadding)
			subPrefix := fmt.Sprintf("%s%s", nestedPrefix, getBranchPrefix(i, len(concurrentStep.steps)))
			var delayStr string
			if concurrentStep.delays[i] != 0 {
				delayStr = fmt.Sprintf("after %s delay", concurrentStep.delays[i])
			}
			p.prettyPrintStep(out, subStep, subPrefix, delayStr)
		}
	}

	// writeSingle is the function that generates the description for
	// a singleStep. It can include extra information, such as whether
	// there's a delay associated with the step (in the case of
	// concurrent execution).
	writeSingle := func(s step, extraContext ...string) {
		var extras string
		if contextStr := strings.Join(extraContext, ", "); contextStr != "" {
			extras = ", " + contextStr
		}

		out.WriteString(fmt.Sprintf(
			"%s %s%s (%d)\n", prefix, s.Description(), extras, s.runID,
		))
	}

	switch protocol := s.StepProtocol.(type) {
	case *SingleStep:
		writeSingle(s, extraContext...)
	case *concurrentStep:
		writeConcurrent(s.Description(), protocol)
	}
}

func getBranchPrefix(idx, sliceLen int) string {
	if idx == sliceLen-1 {
		return lastBranchString
	}
	return branchString
}

func getStagePrefix(idx, sliceLen int) string {
	if idx == sliceLen-1 {
		return lastBranchPadding
	}
	return nestedBranchString
}
