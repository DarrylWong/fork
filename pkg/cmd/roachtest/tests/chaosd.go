package tests

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
	"github.com/stretchr/testify/require"
)

func registerChaosd(r registry.Registry) {
	r.Add(registry.TestSpec{
		Name:             "chaosd/kv",
		Owner:            registry.OwnerTestEng,
		Cluster:          r.MakeClusterSpec(4, spec.WorkloadNode()),
		CompatibleClouds: registry.AllClouds,
		Suites:           registry.ManualOnly,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			c.Start(ctx, t.L(), option.NewStartOpts(option.NoBackupSchedule), install.MakeClusterSettings(), c.CRDBNodes())
			m := c.NewMonitor(ctx, c.CRDBNodes())

			db := c.Conn(ctx, t.L(), 1)
			defer db.Close()

			err := WaitFor3XReplication(ctx, t, t.L(), db)
			require.NoError(t, err)

			err = roachtestutil.InstallChaosd(ctx, c, c.CRDBNodes())
			require.NoError(t, err)

			err = c.RunE(ctx, option.WithNodes(c.WorkloadNode()), fmt.Sprintf("./cockroach workload init kv {pgurl%s}", c.CRDBNodes()))
			require.NoError(t, err)

			workloadStart := timeutil.Now()
			var workloadEnd time.Time
			m.Go(func(ctx context.Context) error {
				err = c.RunE(ctx, option.WithNodes(c.WorkloadNode()), fmt.Sprintf("./cockroach workload run kv --duration=5m {pgurl%s}", c.Range(2, 3)))
				workloadEnd = time.Now()
				return err
			})

			t.L().Printf("Running without failures for 3 minutes")
			time.Sleep(3 * time.Minute)

			failureInjectionTime := time.Now()

			// metrics
			adminUIAddrs, err := c.ExternalAdminUIAddr(ctx, t.L(), c.Nodes(2))
			require.NoError(t, err)
			adminURL := adminUIAddrs[0]

			response := mustGetMetrics(ctx, c, t, adminURL, install.SystemInterfaceName, workloadStart, failureInjectionTime, []tsQuery{
				{name: "cr.node.sql.query.count", queryType: total},
			})
			cum := response.Results[0].Datapoints
			totalQueriesNoFailure := cum[len(cum)-1].Value - cum[0].Value
			t.L().PrintfCtx(ctx, "%.2f qps without failure", totalQueriesNoFailure/failureInjectionTime.Sub(workloadStart).Seconds())
			//

			_, err = roachtestutil.DiskStallWrite(ctx, c, t.L(), c.Node(1))
			require.NoError(t, err)

			err = roachtestutil.FindExperiments(ctx, c)
			require.NoError(t, err)

			m.Wait()

			// metrics
			response = mustGetMetrics(ctx, c, t, adminURL, install.SystemInterfaceName, failureInjectionTime, workloadEnd, []tsQuery{
				{name: "cr.node.sql.query.count", queryType: total},
			})
			cum = response.Results[0].Datapoints
			totalQueriesWithFailure := cum[len(cum)-1].Value - cum[0].Value
			t.L().PrintfCtx(ctx, "%.2f qps with failure", totalQueriesWithFailure/workloadEnd.Sub(failureInjectionTime).Seconds())
			//
		},
	})
}
