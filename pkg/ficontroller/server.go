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
		PlanDir:   config.PlanDir,
		Plans:     make(map[string]string),
		PlanCache: make(map[string]*FailurePlan),
	}
	return c.ListenAndServe(ctx, config.Port)
}

func (c *Controller) ListenAndServe(ctx context.Context, port int) error {
	lis, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	server := grpc.NewServer()
	RegisterControllerServer(server, c)
	go func() {
		err = server.Serve(lis)
	}()
	select {
	case <-ctx.Done():
	}

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
	Status        PlanStatus
	CancelFunc    func()
	IsStatic      bool
	StaticPlan    fiplanner.StaticFailurePlan
	DynamicPlan   fiplanner.DynamicFailurePlan
	stepGenerator *fiplanner.StepGenerator
	Clusters      map[string]*ClusterInfo
}

type Controller struct {
	PlanDir   string            // directory to store plans
	Plans     map[string]string // map of name to plan file
	PlanCache map[string]*FailurePlan
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

		c.PlanCache[plan.PlanID] = &FailurePlan{
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

	c.PlanCache[plan.PlanID] = &FailurePlan{
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
	plan := c.PlanCache[req.PlanID]

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
	plan, ok := c.PlanCache[req.PlanID]
	if !ok {
		// TODO check files
		return nil, fmt.Errorf("plan %s not found", req.PlanID)
	}
	// TODO check if plan already active
	plan.Status = Running
	go func() {
		c.RunFailureInjectionTest(ctx, plan)
	}()
	return &StartFailureInjectionResponse{}, nil
}

func (c *Controller) StopFailureInjection(
	ctx context.Context, req *StopFailureInjectionRequest,
) (*StopFailureInjectionResponse, error) {
	plan, ok := c.PlanCache[req.PlanID]
	if !ok {
		// TODO check files if not found in cache
		return nil, fmt.Errorf("plan %s not found", req.PlanID)
	}
	if plan.Status != Running {
		return nil, fmt.Errorf("plan %s is not running, status is %s", req.PlanID, plan.Status)
	}
	plan.Status = Cancelled
	plan.CancelFunc()
	return &StopFailureInjectionResponse{}, nil
}

func (c *Controller) GetFailurePlanStatus(
	ctx context.Context, req *GetFailurePlanStatusRequest,
) (*GetFailurePlanStatusResponse, error) {
	if _, ok := c.PlanCache[req.PlanID]; !ok {
		return &GetFailurePlanStatusResponse{
			PlanStatus: string(NotFound),
		}, nil
	}

	plan := c.PlanCache[req.PlanID]

	var planBytes []byte
	var err error
	if plan.IsStatic {
		planBytes, err = yaml.Marshal(plan.StaticPlan)
		if err != nil {
			return nil, err
		}
	} else {
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
