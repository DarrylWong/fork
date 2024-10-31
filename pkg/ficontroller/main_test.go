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
	config := ControllerConfig{port: 8081}
	go func() {
		err := config.Start(ctx)
		require.NoError(t, err)
	}()
}

func Test_UploadStaticPlan(t *testing.T) {
	ctx := context.Background()
	StartController(t, ctx)

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
		MinWait:        10 * time.Second,
		MaxWait:        1 * time.Minute,
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

	status, err := client.GetFailurePlanStatus(ctx, &GetFailurePlanStatusRequest{PlanID: planID})
	require.NoError(t, err)
	fmt.Printf("Plan status: %v\n", status.PlanStatus)
	fmt.Printf("Cluster State: %v\n", status.ClusterState)
	fmt.Printf("Stored Plan: %s\n", status.FailurePlan)

	_, err = client.StopFailureInjection(ctx, &StopFailureInjectionRequest{PlanID: planID})
	require.NoError(t, err)
	status, err = client.GetFailurePlanStatus(ctx, &GetFailurePlanStatusRequest{PlanID: planID})
	require.NoError(t, err)
	fmt.Printf("Plan status: %v\n", status.PlanStatus)
}
