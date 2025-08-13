package modular

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/task"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/version"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"go/token"
)

// TestPlanString tests the String() method of TestPlan.
func TestPlanner(t *testing.T) {
	mod := NewTest("basic plan", 12345)
	ctrl := gomock.NewController(t)
	mod.WithCreateClusterFn(func() cluster.Cluster {
		mockCluster := cluster.NewMockCluster(ctrl)
		mockCluster.EXPECT().Name().Return("my-cluster").AnyTimes()
		return mockCluster
	})

	// Add cluster with options
	c := mod.AddCluster(
		MinNodes(3),
		MaxNodes(5),
		DisabledDeploymentModes(SeparateProcessDeployment),
	)

	// Add workload cluster
	workloadCluster := mod.AddWorkloadCluster(WorkloadNodeCount(1))

	// Install prometheus and grafana, then after that is done, start a background loop to scrape metrics.
	mod.Setup("installing prom" /* step name */, baselineStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		l.Printf("Installing Prometheus and Grafana")
		return nil
	})
	mod.Setup("starting histogram scrape loop" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		l.Printf("starting histogram scrape loop")
		time.Sleep(5 * time.Second)
		l.Printf("measured perf as 400 qps")
		return nil
	}, InBackground())
	mod.Setup("importing tpcc workload" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
		l.Printf("importing TPCC workload")
		return nil
	})

	baselineStage := mod.NewStage("baseline", DisableFailureInjection())
	chaosStage := mod.NewStage("chaos")
	for stage := range []Stage{baselineStage, chaosStage} {
		// Run TPCC workload
		mod.InStage("running TPCC workload for 1 hour" /* step name */, baselineStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("running TPCC workload for 1 hour")
			return nil
		}).Then("dropping TPCC tables" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("dropping TPCC tables")
			return nil
		})

		mod.InStage("increasing replication factor to 5" /* step name */, baselineStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("increasing replication factor to 5")
			return nil
		}).Then("waiting for replication" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("waiting for replication")
			return nil
		}).Then("sleeping for 10 minutes" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("sleeping for 10 minutes")
			return nil
		}).Then("decreasing replication factor to 3" /* step name */, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("decreasing replication factor to 3")
			return nil
		})

		mod.InStage("copy bank table" /* step name */, baselineStage, func(ctx context.Context, l *logger.Logger, h *Helper) error {
			l.Printf("copy bank table")
			return nil
		})
	}

	// Add after-test step, which is run after prometheus scraping is turned off.
	mod.AfterTest("TPCC consistency checks", func(ctx context.Context, l *logger.Logger, h *Helper) error {
		l.Printf("running TPCC consistency checks")
		return nil
	})
}
