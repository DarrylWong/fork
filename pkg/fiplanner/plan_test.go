package fiplanner

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/fiplanner/failures"
	"github.com/cockroachdb/cockroach/pkg/testutils/echotest"
	"github.com/stretchr/testify/require"
)

const testUser = "unit_test"

// setupPlannerUnitTest replaces planner functions with mocks to make for
// easier/more deterministic unit tests.
func setupPlannerUnitTest() func() {
	// Replace the time post fix with a static string.
	generatePlanIDHook = func(user string) string {
		return fmt.Sprintf("%s-12345", user)
	}

	// Select just a few valid failure types instead of all of them.
	// This will make it so our tests don't change every time we add
	// a new failure type.
	registerFailuresHook = failures.UnitTestRegisterFailures

	return func() {
		generatePlanIDHook = generatePlanID
		registerFailuresHook = failures.RegisterFailures
	}
}

func Test_GenerateStaticPlan(t *testing.T) {
	defer setupPlannerUnitTest()()

	testdataDir := filepath.Join("testdata", "static_planner")
	t.Run("basic plan", func(t *testing.T) {
		clusterSizes := []int{
			4,
		}
		spec := StaticFailurePlanSpec{
			User:           testUser,
			ClusterNames:   []string{"test_cluster"},
			ClusterSizes:   clusterSizes,
			TolerateErrors: true,
			Seed:           1234,
			NumSteps:       10,
			minWait:        10 * time.Second,
			maxWait:        1 * time.Minute,
		}
		planBytes, err := spec.GeneratePlan()
		require.NoError(t, err)

		file := "basic_static_plan"
		echotest.Require(t, string(planBytes), filepath.Join(testdataDir, file))
	})
	t.Run("disable failure", func(t *testing.T) {
		clusterSizes := []int{
			4,
		}
		spec := StaticFailurePlanSpec{
			User:             testUser,
			ClusterNames:     []string{"test_cluster"},
			ClusterSizes:     clusterSizes,
			TolerateErrors:   false,
			Seed:             1234,
			DisabledFailures: []string{"Node Restart", "Page Fault"},
			NumSteps:         5,
			minWait:          10 * time.Second,
			maxWait:          1 * time.Minute,
		}
		planBytes, err := spec.GeneratePlan()
		require.NoError(t, err)

		file := "static_plan_disable_failure"
		echotest.Require(t, string(planBytes), filepath.Join(testdataDir, file))
	})

	t.Run("multiple clusters", func(t *testing.T) {
		clusterSizes := []int{
			4, 3, 9,
		}
		spec := StaticFailurePlanSpec{
			User:         testUser,
			ClusterNames: []string{"test_cluster_1", "test_cluster_2", "test_cluster_3"},
			ClusterSizes: clusterSizes,
			Seed:         123456,
			NumSteps:     10,
			minWait:      10 * time.Second,
			maxWait:      1 * time.Minute,
		}
		planBytes, err := spec.GeneratePlan()
		require.NoError(t, err)

		file := "static_plan_multiple_clusters"
		echotest.Require(t, string(planBytes), filepath.Join(testdataDir, file))
	})
}

func Test_GenerateDynamicPlan(t *testing.T) {
	defer setupPlannerUnitTest()()

	testdataDir := filepath.Join("testdata", "dynamic_planner")
	t.Run("basic plan", func(t *testing.T) {
		spec := DynamicFailurePlanSpec{
			User:           testUser,
			TolerateErrors: true,
			Seed:           1234,
			minWait:        10 * time.Second,
			maxWait:        1 * time.Minute,
		}
		planBytes, err := spec.GeneratePlan()
		require.NoError(t, err)

		file := "basic_dynamic_plan"
		echotest.Require(t, string(planBytes), filepath.Join(testdataDir, file))
	})
}

func Test_DynamicPlanGenerateStep(t *testing.T) {
	defer setupPlannerUnitTest()()

	testdataDir := filepath.Join("testdata", "dynamic_planner")
	t.Run("basic plan", func(t *testing.T) {
		spec := DynamicFailurePlanSpec{
			User:           testUser,
			TolerateErrors: true,
			Seed:           1234,
			minWait:        10 * time.Second,
			maxWait:        1 * time.Minute,
		}
		planBytes, err := spec.GeneratePlan()
		require.NoError(t, err)

		file := "basic_dynamic_plan"
		echotest.Require(t, string(planBytes), filepath.Join(testdataDir, file))
	})
}
