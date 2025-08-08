package tests

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
)

func registerStartWithoutQuorum(r registry.Registry) {
	r.Add(registry.TestSpec{
		Name:             "start-without-quorum",
		Owner:            registry.OwnerTestEng,
		Cluster:          r.MakeClusterSpec(3),
		CompatibleClouds: registry.OnlyLocal,
		Suites:           registry.ManualOnly,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			c.Start(ctx, t.L(), option.DefaultStartOpts(), install.MakeClusterSettings(), c.All())
			c.Stop(ctx, t.L(), option.DefaultStopOpts(), c.All())
			c.Start(ctx, t.L(), option.DefaultStartOpts(), install.MakeClusterSettings(), c.Node(1))
		},
	})
}
