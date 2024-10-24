package fiplanner

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/testutils/echotest"
	"github.com/stretchr/testify/require"
)

func Test_GenerateStaticPlan(t *testing.T) {
	testdataDir := filepath.Join("testdata", "static_planner")
	t.Run("basic plan", func(t *testing.T) {
		clusterSizes := []int{
			4,
		}
		spec := FailurePlanSpec{
			PlanID:         "1",
			ClusterNames:   []string{"test_cluster"},
			TolerateErrors: false,
			Seed:           1234,
			minWait:        10 * time.Second,
			maxWait:        1 * time.Minute,
		}
		planBytes, err := GenerateStaticPlan(clusterSizes, spec, 10)
		require.NoError(t, err)

		file := "basic_static_plan"
		echotest.Require(t, string(planBytes), filepath.Join(testdataDir, file))
	})
	t.Run("disable failure", func(t *testing.T) {
		clusterSizes := []int{
			4,
		}
		spec := FailurePlanSpec{
			PlanID:           "1",
			ClusterNames:     []string{"test_cluster"},
			TolerateErrors:   false,
			Seed:             1234,
			DisabledFailures: []string{"Node Restart"},
			minWait:          10 * time.Second,
			maxWait:          1 * time.Minute,
		}
		planBytes, err := GenerateStaticPlan(clusterSizes, spec, 5)
		require.NoError(t, err)

		file := "static_plan_disable_failure"
		echotest.Require(t, string(planBytes), filepath.Join(testdataDir, file))
	})

	t.Run("multiple clusters", func(t *testing.T) {
		clusterSizes := []int{
			4, 3, 9,
		}
		spec := FailurePlanSpec{
			PlanID:         "1",
			ClusterNames:   []string{"test_cluster_1", "test_cluster_2", "test_cluster_3"},
			TolerateErrors: false,
			Seed:           123456,
			minWait:        10 * time.Second,
			maxWait:        1 * time.Minute,
		}
		planBytes, err := GenerateStaticPlan(clusterSizes, spec, 10)
		require.NoError(t, err)

		file := "static_plan_multiple_clusters"
		echotest.Require(t, string(planBytes), filepath.Join(testdataDir, file))
	})
}
