// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

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
	"github.com/cockroachdb/cockroach/pkg/ficontroller"
	"github.com/cockroachdb/cockroach/pkg/fiplanner"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func registerFailureInjection(r registry.Registry) {
	// This test is a PoC showing how we can use the failure injection framework
	// on top of roachtests
	r.Add(registry.TestSpec{
		Name:             "failure-injection/example",
		Owner:            registry.OwnerTestEng,
		CompatibleClouds: registry.AllClouds,
		Suites:           registry.ManualOnly,
		Cluster:          r.MakeClusterSpec(4, spec.WorkloadNode()),
		RequiresLicense:  false,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			// This work would be done by the test runner framework in a real deployment
			config := ficontroller.ControllerConfig{Port: 8081}
			go func() {
				err := config.Start(ctx)
				require.NoError(t, err)
			}()

			plan := fiplanner.DynamicFailurePlanSpec{
				User:    "test-user", // Should use roachprod user or cluster name
				MinWait: 10 * time.Second,
				MaxWait: 30 * time.Second,
			}
			planBytes, err := plan.GeneratePlan()
			require.NoError(t, err)
			conn, err := grpc.DialContext(ctx, "localhost:8081", grpc.WithInsecure())
			require.NoError(t, err)
			client := ficontroller.NewControllerClient(conn)
			resp, err := client.UploadFailureInjectionPlan(ctx, &ficontroller.UploadFailureInjectionPlanRequest{FailurePlan: planBytes})
			require.NoError(t, err)
			planID := resp.PlanID
			ip, _ := c.ExternalIP(ctx, t.L(), c.Node(1))
			_, err = client.UpdateClusterState(ctx, &ficontroller.UpdateClusterStateRequest{PlanID: planID, ClusterState: map[string]*ficontroller.ClusterInfo{
				c.Name(): {
					ClusterSize:      int32(c.Spec().NodeCount),
					ConnectionString: ip[0],
				},
			}})

			defer func() {
				_, err = client.StopFailureInjection(ctx, &ficontroller.StopFailureInjectionRequest{PlanID: planID})
				require.NoError(t, err)
			}()

			// Starting here is what should actually be in a roachtest
			c.Start(ctx, t.L(), option.DefaultStartOpts(), install.MakeClusterSettings(), c.CRDBNodes())

			cmd := tpccImportCmdWithCockroachBinary(test.DefaultCockroachPath, "", "tpcc", 10, fmt.Sprintf("{pgurl%s}", c.Node(1)))
			c.Run(ctx, option.WithNodes(c.WorkloadNode()), cmd)

			_, err = client.StartFailureInjection(ctx, &ficontroller.StartFailureInjectionRequest{PlanID: planID})
			require.NoError(t, err)

			cmd = roachtestutil.NewCommand("./cockroach workload run tpcc").
				Arg("{pgurl%s}", c.CRDBNodes()).
				Flag("duration", "10m").
				Flag("warehouses", 10).
				Flag("ramp", "1m").
				String()
			c.Run(ctx, option.WithNodes(c.WorkloadNode()), cmd)
		},
	})
}
