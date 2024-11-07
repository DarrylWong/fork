package failureinjection

// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

import (
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/fiplanner"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
)

func MakeFailureInjectionPlan(
	t test.Test, c cluster.Cluster, opts ...Option,
) fiplanner.DynamicFailurePlanSpec {
	_, seed := randutil.NewPseudoRand()

	var defaultDisabledFailures []string
	if c.IsLocal() {
		// Disable IP tables usage when running on locally.
		defaultDisabledFailures = append(defaultDisabledFailures, "Partition Node")
	}

	spec := fiplanner.DynamicFailurePlanSpec{
		User:             t.Name(),
		LogDir:           t.ArtifactsDir(),
		TolerateErrors:   true,
		DisabledFailures: defaultDisabledFailures,
		Seed:             seed,
	}
	defaultOpts := []Option{
		MinWait(1 * time.Minute),
		MaxWait(5 * time.Minute),
	}

	for _, o := range append(defaultOpts, opts...) {
		o(&spec)
	}

	return spec
}

type Option func(spec *fiplanner.DynamicFailurePlanSpec)

func DisabledFailureTypes(disabledFailureTypes []string) Option {
	return func(spec *fiplanner.DynamicFailurePlanSpec) {
		spec.DisabledFailures = append(spec.DisabledFailures, disabledFailureTypes...)
	}
}

func MinWait(minWait time.Duration) Option {
	return func(spec *fiplanner.DynamicFailurePlanSpec) {
		spec.MinWait = minWait
	}
}

func MaxWait(maxWait time.Duration) Option {
	return func(spec *fiplanner.DynamicFailurePlanSpec) {
		spec.MaxWait = maxWait
	}
}
