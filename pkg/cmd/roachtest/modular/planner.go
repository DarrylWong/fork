package modular

// stagePlan represents a list of _ordered_ steps to be executed for a given stage,
// It is a legal linearization of the stage's DAG, i.e. what will actually be run
// by the test runner.
type stagePlan struct {
	stage *Stage
	steps []Step
}

// planFunc converts a given stage DAG into a legal stagePlan.
// TODO: implement some planning strategies that:
// 1. Attempts to fairly randomize the order of steps while respecting dependencies.
// 2. Attempts to inject failures at various points in the plan if legal.
// 3. Attempts to run steps concurrently randomly.
type planFunc func(s *Stage) (stagePlan, error)

type Planner struct {
	planFunc planFunc
	stages   []*Stage
}

func NewPlanner(stages []*Stage, planFn planFunc) *Planner {
	return &Planner{
		planFunc: planFn,
		stages:   stages,
	}
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
	return &TestPlan{}, nil
}

func (p *Planner) DAG() string {
	// TODO: implement pretty printing of the DAG.
	return "unimplemented"
}

type TestPlan struct{}

func (p *TestPlan) String() string {
	// TODO: implement pretty printing of the test plan similar to mixed version tests.
	return "unimplemented"
}
