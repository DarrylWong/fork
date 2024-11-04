package ficontroller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/fiplanner"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func StartController(t *testing.T, ctx context.Context) {
	config := ControllerConfig{Port: 8081}
	go func() {
		err := config.Start(ctx)
		require.NoError(t, err)
	}()
}

func Test_UploadStaticPlan(t *testing.T) {
	ctx := context.Background()
	controllerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	StartController(t, controllerCtx)

	conn, err := grpc.DialContext(ctx, "localhost:8081", grpc.WithInsecure())
	require.NoError(t, err)
	client := NewControllerClient(conn)

	spec := fiplanner.StaticFailurePlanSpec{
		User:           "test-user",
		ClusterNames:   []string{"test_cluster"},
		ClusterSizes:   []int{3},
		TolerateErrors: true,
		Seed:           1234,
		NumSteps:       10,
		MinWait:        1 * time.Second,
		MaxWait:        2 * time.Second,
	}
	planBytes, err := spec.GeneratePlan()

	resp, err := client.UploadFailureInjectionPlan(ctx, &UploadFailureInjectionPlanRequest{planBytes, true})
	require.NoError(t, err)

	planID := resp.PlanID

	clusterInfo := ClusterInfo{
		ClusterSize:      3,
		ConnectionString: "TODO: implement me",
	}

	clusterState := map[string]*ClusterInfo{"test_cluster": &clusterInfo}
	_, err = client.UpdateClusterState(ctx, &UpdateClusterStateRequest{PlanID: planID, ClusterState: clusterState})
	require.NoError(t, err)

	_, err = client.StartFailureInjection(ctx, &StartFailureInjectionRequest{PlanID: planID})
	require.NoError(t, err)

	// In actual deployments, the runner would subscribe to the event channel and listen for a completion event.
	status, err := client.GetFailurePlanStatus(ctx, &GetFailurePlanStatusRequest{PlanID: planID})
	require.NoError(t, err)
	fmt.Printf("Plan status: %v\n", status.PlanStatus)
	for status.PlanStatus != string(Completed) {
		time.Sleep(10 * time.Second)
		status, err = client.GetFailurePlanStatus(ctx, &GetFailurePlanStatusRequest{PlanID: planID})
		require.NoError(t, err)
		fmt.Printf("Plan status: %v\n", status.PlanStatus)
	}

	// Plan is already completed, should fail.
	_, err = client.StopFailureInjection(ctx, &StopFailureInjectionRequest{PlanID: planID})
	require.Error(t, err)
	status, err = client.GetFailurePlanStatus(ctx, &GetFailurePlanStatusRequest{PlanID: planID})
	require.NoError(t, err)
	fmt.Printf("Plan status: %v\n", status.PlanStatus)
}

func Test_UploadDynamicPlan(t *testing.T) {
	ctx := context.Background()
	StartController(t, ctx)

	conn, err := grpc.DialContext(ctx, "localhost:8081", grpc.WithInsecure())
	require.NoError(t, err)
	client := NewControllerClient(conn)

	spec := fiplanner.DynamicFailurePlanSpec{
		User:           "test-user",
		TolerateErrors: true,
		Seed:           1234,
		MinWait:        1 * time.Second,
		MaxWait:        2 * time.Second,
	}
	planBytes, err := spec.GeneratePlan()

	resp, err := client.UploadFailureInjectionPlan(ctx, &UploadFailureInjectionPlanRequest{planBytes, false})
	require.NoError(t, err)

	planID := resp.PlanID

	clusterInfo := ClusterInfo{
		ClusterSize:      3,
		ConnectionString: "TODO: implement me",
	}

	clusterState := map[string]*ClusterInfo{"test_cluster": &clusterInfo}
	_, err = client.UpdateClusterState(ctx, &UpdateClusterStateRequest{PlanID: planID, ClusterState: clusterState})
	require.NoError(t, err)

	_, err = client.StartFailureInjection(ctx, &StartFailureInjectionRequest{PlanID: planID})
	require.NoError(t, err)

	// In actual deployments, the runner would subscribe to the event channel and listen for a completion event.
	status, err := client.GetFailurePlanStatus(ctx, &GetFailurePlanStatusRequest{PlanID: planID})
	require.NoError(t, err)
	fmt.Printf("Plan status: %v\n", status.PlanStatus)

	time.Sleep(5 * time.Second)

	_, err = client.StopFailureInjection(ctx, &StopFailureInjectionRequest{PlanID: planID})
	require.NoError(t, err)
	status, err = client.GetFailurePlanStatus(ctx, &GetFailurePlanStatusRequest{PlanID: planID})
	require.NoError(t, err)
	fmt.Printf("Plan status: %v\n", status.PlanStatus)
}
