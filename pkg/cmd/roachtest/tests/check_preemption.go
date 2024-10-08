// Copyright 2018 The Cockroach Authors.
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
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
)

func registerCheckPreemption(r registry.Registry) {
	runCheckPreemption := func(ctx context.Context, t test.Test, c cluster.Cluster) {
		c.Start(ctx, t.L(), option.DefaultStartOpts(), install.MakeClusterSettings(), c.All())
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Minute):
				return
			}
		}
	}

	r.Add(registry.TestSpec{
		Name:             fmt.Sprintf("checkVMPreemption"),
		Owner:            registry.OwnerTestEng,
		Cluster:          r.MakeClusterSpec(3, spec.UseSpotVMs()),
		CompatibleClouds: registry.Clouds(spec.GCE),
		Suites:           registry.ManualOnly,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			runCheckPreemption(ctx, t, c)
		},
	})
}
