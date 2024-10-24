package fiplanner

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_GenerateStaticPlan(t *testing.T) {
	t.Run("basic plan", func(t *testing.T) {
		clusterSizes := []int{
			4,
		}
		spec := FailurePlanSpec{
			PlanID:         "1",
			ClusterNames:   []string{"test_cluster"},
			TolerateErrors: false,
			Seed:           12345,
			minWait:        10 * time.Second,
			maxWait:        1 * time.Minute,
		}
		err := GenerateStaticPlan(clusterSizes, spec, 10)
		require.NoError(t, err)
	})
}
