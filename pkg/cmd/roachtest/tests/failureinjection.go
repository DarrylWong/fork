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
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/failureinjection"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/stretchr/testify/require"
)

func registerFailureInjection(r registry.Registry) {
	// This test is a PoC showing how we can use the failure injection framework
	// on top of roachtests
	r.Add(registry.TestSpec{
		Name:                 "failure-injection/example",
		Owner:                registry.OwnerTestEng,
		CompatibleClouds:     registry.AllClouds,
		Suites:               registry.ManualOnly,
		Cluster:              r.MakeClusterSpec(4, spec.WorkloadNode()),
		FailureInjectionTest: true,
		FailureInjectionOpts: []failureinjection.Option{
			failureinjection.MinWait(10 * time.Second),
			failureinjection.MaxWait(30 * time.Second),
		},
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			c.Start(ctx, t.L(), option.DefaultStartOpts(), install.MakeClusterSettings(), c.CRDBNodes())

			cmd := tpccImportCmdWithCockroachBinary(test.DefaultCockroachPath, "", "tpcc", 10, fmt.Sprintf("{pgurl%s}", c.Node(1)))
			c.Run(ctx, option.WithNodes(c.WorkloadNode()), cmd)

			require.NoError(t, c.StartFailureInjectionPlan(ctx, t.L()))

			cmd = roachtestutil.NewCommand("./cockroach workload run tpcc").
				Arg("{pgurl%s}", c.CRDBNodes()).
				Flag("duration", "3m").
				Flag("warehouses", 10).
				Flag("ramp", "1m").
				String()
			c.Run(ctx, option.WithNodes(c.WorkloadNode()), cmd)
		},
	})
}
