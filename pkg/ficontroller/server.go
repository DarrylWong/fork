package ficontroller

import (
	"context"
	"fmt"
	"log"
	"net"

	"github.com/cockroachdb/cockroach/pkg/fiplanner"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v2"
)

type ControllerConfig struct {
	// TODO support more than just localhost
	Port        int
	PlanDir     string
	Concurrency int
}

func (config ControllerConfig) Start(ctx context.Context) error {
	c := &Controller{
		PlanDir:     config.PlanDir,
		PlanLogs:    make(map[string]string),
		ActivePlans: make(map[string]*FailurePlan),
	}
	return c.ListenAndServe(ctx, config.Port)
}

func (c *Controller) ListenAndServe(ctx context.Context, port int) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	server := grpc.NewServer()
	RegisterControllerServer(server, c)
	go func() {
		err = server.Serve(lis)
	}()

	<-ctx.Done()
	server.Stop()

	return err
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

type FailurePlan struct {
	// TODO add mutex
	Status        PlanStatus
	CancelFunc    func()
	IsStatic      bool
	StaticPlan    fiplanner.StaticFailurePlan
	DynamicPlan   fiplanner.DynamicFailurePlan
	stepGenerator *fiplanner.StepGenerator
	Clusters      map[string]*ClusterInfo
}

type Controller struct {
	PlanDir     string            // directory to store plan and plan logs
	PlanLogs    map[string]string // map of plan ID to plan logs
	ActivePlans map[string]*FailurePlan
}

func (c *Controller) UploadFailureInjectionPlan(
	ctx context.Context, req *UploadFailureInjectionPlanRequest,
) (*UploadFailureInjectionPlanResponse, error) {
	fmt.Printf("Received plan: %s\n", req.FailurePlan)

	if req.StaticPlan {
		plan := &fiplanner.StaticFailurePlan{}
		err := yaml.Unmarshal(req.FailurePlan, plan)
		if err != nil {
			return nil, err
		}

		c.ActivePlans[plan.PlanID] = &FailurePlan{
			IsStatic:   true,
			StaticPlan: *plan,
			Status:     NotStarted,
		}
		return &UploadFailureInjectionPlanResponse{plan.PlanID}, nil
	}
	plan := &fiplanner.DynamicFailurePlan{}
	err := yaml.Unmarshal(req.FailurePlan, plan)
	if err != nil {
		return nil, err
	}

	c.ActivePlans[plan.PlanID] = &FailurePlan{
		DynamicPlan:   *plan,
		Status:        NotStarted,
		stepGenerator: fiplanner.NewStepGenerator(*plan),
	}
	return &UploadFailureInjectionPlanResponse{plan.PlanID}, nil
}

func (c *Controller) UpdateClusterState(
	ctx context.Context, req *UpdateClusterStateRequest,
) (*UpdateClusterStateResponse, error) {
	// TODO check this exists, lets make a helper
	plan := c.ActivePlans[req.PlanID]

	if plan.Clusters == nil {
		plan.Clusters = make(map[string]*ClusterInfo)
	}

	for name, clusterInfo := range req.ClusterState {
		// TODO: check it doesn't already exist
		plan.Clusters[name] = clusterInfo
	}
	return &UpdateClusterStateResponse{}, nil
}

func (c *Controller) StartFailureInjection(
	ctx context.Context, req *StartFailureInjectionRequest,
) (*StartFailureInjectionResponse, error) {
	plan, ok := c.ActivePlans[req.PlanID]
	if !ok {
		return nil, fmt.Errorf("plan %s not found", req.PlanID)
	}
	// TODO check if plan already active
	plan.Status = Running
	go func() {
		c.RunFailureInjectionTest(ctx, plan)
	}()
	return &StartFailureInjectionResponse{}, nil
}

// TODO: should we have a distinct PauseFailureInjection as well?
func (c *Controller) StopFailureInjection(
	ctx context.Context, req *StopFailureInjectionRequest,
) (*StopFailureInjectionResponse, error) {
	plan, ok := c.ActivePlans[req.PlanID]
	if !ok {
		// TODO check files if not found in cache
		return nil, fmt.Errorf("plan %s not found", req.PlanID)
	}
	if plan.Status != Running {
		return nil, fmt.Errorf("plan %s is not running, status is %s", req.PlanID, plan.Status)
	}
	plan.Status = Cancelled
	plan.CancelFunc()

	// TODO: mark plan for removal from ActivePlans?
	// We don't want to do it right away since someone might want to check the status,
	// but at the same time adding garbage collection may be a lot of unnecessary complexity.
	return &StopFailureInjectionResponse{}, nil
}

func (c *Controller) GetFailurePlanStatus(
	ctx context.Context, req *GetFailurePlanStatusRequest,
) (*GetFailurePlanStatusResponse, error) {
	if _, ok := c.ActivePlans[req.PlanID]; !ok {
		// TODO: check files if not found in activeplans
		return &GetFailurePlanStatusResponse{
			PlanStatus: string(NotFound),
		}, nil
	}

	plan := c.ActivePlans[req.PlanID]

	var planBytes []byte
	var err error
	if plan.IsStatic {
		planBytes, err = yaml.Marshal(plan.StaticPlan)
		if err != nil {
			return nil, err
		}
	} else {
		planBytes, err = yaml.Marshal(plan.DynamicPlan)
		if err != nil {
			return nil, err
		}
	}

	return &GetFailurePlanStatusResponse{
		PlanStatus:   string(plan.Status),
		FailurePlan:  planBytes,
		ClusterState: plan.Clusters,
	}, nil
}

func (c *Controller) GetFailurePlanLogs(
	ctx context.Context, req *GetFailurePlanLogsRequest,
) (*GetFailurePlanLogsResponse, error) {
	return &GetFailurePlanLogsResponse{}, nil
}
