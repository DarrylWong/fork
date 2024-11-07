package ficontroller

import (
	"context"
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/fiplanner"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/util/syncutil"
	"gopkg.in/yaml.v2"
)

type ControllerConfig struct {
	// TODO support more than just localhost
	Port int
	// PlanDir is the directory where the controller will store plan logs if
	// not specified by the plan itself. This is intended for standalone controller
	// usage, where the user must get the logs from the controller. If the controller
	// is in the same process as the test, it makes more sense for the controller to
	// write directly to desired location.
	PlanDir     string
	Concurrency int
	L           *logger.Logger
}

func NewController(config ControllerConfig) Controller {
	// TODO: add defaults
	if config.L == nil {
		newLogger, err := logger.RootLogger("fi-controller", logger.NoTee)
		if err != nil {
			// TODO: return error instead?
			panic(fmt.Sprintf("error creating logger: %v", err))
		}

		config.L = newLogger
	}
	return Controller{
		l:              config.L,
		DefaultPlanDir: config.PlanDir,
		PlanLogs:       make(map[string]string),
		ActivePlans:    make(map[string]*FailurePlan),
		PlanQueue:      make(chan *FailurePlan),
	}
}

func (c *Controller) Start(ctx context.Context) {
	c.ctx, c.cancel = context.WithCancel(ctx)
	go func() {
		defer c.Stop()
		for {
			select {
			case plan, ok := <-c.PlanQueue:
				if ok {
					go func() {
						// TODO: don't let a panic take down the server
						c.RunFailureInjectionTest(ctx, c.l, plan)
					}()
				}
			case <-c.ctx.Done():
				fmt.Printf("context cancelled, stopping server\n")
				return
			}
		}
	}()
}

func (c *Controller) Stop() {
	close(c.PlanQueue)
	c.cancel()
	c.l.Close()
	for _, plan := range c.ActivePlans {
		plan.mu.Lock()
		defer plan.mu.Unlock()
		if plan.mu.Status == Running {
			plan.mu.CancelFunc()
		}
	}
}

type PlanStatus string

const (
	NotFound   PlanStatus = "Not Found"
	NotStarted PlanStatus = "Not Started"
	Running    PlanStatus = "Running"
	Failed     PlanStatus = "Failed"
	Completed  PlanStatus = "Completed"
	Cancelled  PlanStatus = "Cancelled"
)

type ClusterInfo struct {
	ClusterSize int
	SQLPorts    []int
}

type FailurePlan struct {
	mu struct {
		syncutil.Mutex
		Status     PlanStatus
		CancelFunc func()
	}

	IsStatic      bool
	StaticPlan    fiplanner.StaticFailurePlan
	DynamicPlan   fiplanner.DynamicFailurePlan
	stepGenerator *fiplanner.StepGenerator
	Clusters      map[string]*ClusterInfo
}

func (plan *FailurePlan) getStatus() PlanStatus {
	plan.mu.Lock()
	defer plan.mu.Unlock()
	return plan.mu.Status
}

func (plan *FailurePlan) setStatus(status PlanStatus) {
	plan.mu.Lock()
	defer plan.mu.Unlock()
	plan.mu.Status = status
}

func (plan *FailurePlan) getPlanID() string {
	planID := plan.DynamicPlan.PlanID
	if plan.IsStatic {
		planID = plan.StaticPlan.PlanID
	}
	return planID
}

type Controller struct {
	ctx            context.Context
	cancel         context.CancelFunc
	l              *logger.Logger
	DefaultPlanDir string            // directory to store plan and plan logs
	PlanLogs       map[string]string // map of plan ID to plan logs
	ActivePlans    map[string]*FailurePlan
	PlanQueue      chan *FailurePlan
}

func (c *Controller) UploadFailureInjectionPlan(planBytes []byte, isStatic bool) (string, error) {
	fmt.Printf("Received plan: %s\n", planBytes)

	if isStatic {
		plan := &fiplanner.StaticFailurePlan{}
		err := yaml.Unmarshal(planBytes, plan)
		if err != nil {
			return "", err
		}

		c.ActivePlans[plan.PlanID] = &FailurePlan{
			IsStatic:   true,
			StaticPlan: *plan,
		}
		c.ActivePlans[plan.PlanID].setStatus(NotStarted)
		return plan.PlanID, nil
	}
	plan := &fiplanner.DynamicFailurePlan{}
	err := yaml.Unmarshal(planBytes, plan)
	if err != nil {
		return "", err
	}

	c.ActivePlans[plan.PlanID] = &FailurePlan{
		DynamicPlan:   *plan,
		stepGenerator: fiplanner.NewStepGenerator(*plan),
	}
	c.ActivePlans[plan.PlanID].setStatus(NotStarted)
	return plan.PlanID, nil
}

func (c *Controller) UpdateClusterState(planID string, clusterState map[string]*ClusterInfo) error {
	// TODO check this exists, lets make a helper
	plan := c.ActivePlans[planID]

	if plan.Clusters == nil {
		plan.Clusters = make(map[string]*ClusterInfo)
	}

	for name, clusterInfo := range clusterState {
		// TODO: check it doesn't already exist
		plan.Clusters[name] = clusterInfo
	}
	return nil
}

func (c *Controller) StartFailureInjection(planID string) error {
	plan, ok := c.ActivePlans[planID]
	if !ok {
		return fmt.Errorf("plan %s not found", planID)
	}
	// TODO check if plan already active
	plan.setStatus(Running)
	c.PlanQueue <- plan
	return nil
}

// TODO: should we have a distinct PauseFailureInjection as well?
func (c *Controller) StopFailureInjection(planID string) error {
	plan, ok := c.ActivePlans[planID]
	if !ok {
		// TODO check files if not found in cache
		return fmt.Errorf("plan %s not found", planID)
	}
	if plan.getStatus() != Running {
		return fmt.Errorf("plan %s is not running, status is %s", planID, plan.getStatus())
	}
	plan.setStatus(Cancelled)

	plan.mu.Lock()
	defer plan.mu.Unlock()
	plan.mu.CancelFunc()

	// TODO: mark plan for removal from ActivePlans?
	// We don't want to do it right away since someone might want to check the status,
	// but at the same time adding garbage collection may be a lot of unnecessary complexity.
	return nil
}

func (c *Controller) GetFailurePlanStatus(planID string) (PlanStatus, error) {
	if _, ok := c.ActivePlans[planID]; !ok {
		// TODO: check files if not found in activeplans
		return NotFound, nil
	}

	plan := c.ActivePlans[planID]

	//var planBytes []byte
	//var err error
	//if plan.IsStatic {
	//	planBytes, err = yaml.Marshal(plan.StaticPlan)
	//	if err != nil {
	//		return NotFound, err
	//	}
	//} else {
	//	planBytes, err = yaml.Marshal(plan.DynamicPlan)
	//	if err != nil {
	//		return NotFound, err
	//	}
	//}
	// TODO: either create getPlan etc. APIs or return them here as well

	return plan.getStatus(), nil
}

func (c *Controller) GetFailurePlanLogs() error {
	// TODO: implement
	return nil
}
