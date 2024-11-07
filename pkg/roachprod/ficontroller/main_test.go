package ficontroller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/fiplanner"
	"github.com/stretchr/testify/require"
)

func StartController(t *testing.T, ctx context.Context) *Controller {
	runFunc = func(args ...string) error {
		fmt.Printf("Running: %v\n", args)
		return nil
	}

	config := ControllerConfig{
		Port:    8081,
		PlanDir: t.TempDir(),
	}
	controller := NewController(config)
	controller.Start(ctx)
	return &controller
}

func Test_UploadStaticPlan(t *testing.T) {
	ctx := context.Background()
	client := StartController(t, ctx)

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

	planID, err := client.UploadFailureInjectionPlan(planBytes, true)
	require.NoError(t, err)

	clusterInfo := ClusterInfo{
		ClusterSize: 3,
		SQLPorts:    []int{1234, 12345, 123456},
	}

	clusterState := map[string]*ClusterInfo{"test_cluster": &clusterInfo}
	err = client.UpdateClusterState(planID, clusterState)
	require.NoError(t, err)

	err = client.StartFailureInjection(planID)
	require.NoError(t, err)

	// In actual deployments, the runner would subscribe to the event channel and listen for a completion event.
	status, err := client.GetFailurePlanStatus(planID)
	require.NoError(t, err)
	fmt.Printf("Plan status: %v\n", status)
	for status != Completed {
		time.Sleep(10 * time.Second)
		status, err = client.GetFailurePlanStatus(planID)
		require.NoError(t, err)
		fmt.Printf("Plan status: %v\n", status)
	}

	// Plan is already completed, should fail.
	err = client.StopFailureInjection(planID)
	require.Error(t, err)
	status, err = client.GetFailurePlanStatus(planID)
	require.NoError(t, err)
	fmt.Printf("Plan status: %v\n", status)
}

func Test_UploadDynamicPlan(t *testing.T) {
	ctx := context.Background()
	client := StartController(t, ctx)

	spec := fiplanner.DynamicFailurePlanSpec{
		User:           "test-user",
		TolerateErrors: true,
		Seed:           1234,
		MinWait:        1 * time.Second,
		MaxWait:        2 * time.Second,
	}
	planBytes, err := spec.GeneratePlan()

	planID, err := client.UploadFailureInjectionPlan(planBytes, false)
	require.NoError(t, err)

	clusterInfo := ClusterInfo{
		ClusterSize: 3,
		SQLPorts:    []int{1234, 12345, 123456},
	}

	clusterState := map[string]*ClusterInfo{"test_cluster": &clusterInfo}
	err = client.UpdateClusterState(planID, clusterState)
	require.NoError(t, err)

	err = client.StartFailureInjection(planID)
	require.NoError(t, err)

	// In actual deployments, the runner would subscribe to the event channel and listen for a completion event.
	status, err := client.GetFailurePlanStatus(planID)
	require.NoError(t, err)
	fmt.Printf("Plan status: %v\n", status)

	time.Sleep(5 * time.Second)

	status, err = client.GetFailurePlanStatus(planID)
	require.NoError(t, err)
	fmt.Printf("Plan status: %v\n", status)
}
