// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package roachtestutil

import (
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/roachprod/failureinjection/failures"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// TODO: Think about if this is inefficient for tests that don't use failure injection
var failureRegistry = failures.NewFailureRegistry()

func GetFailer(c cluster.Cluster, failureModeName string, l *logger.Logger) (*failures.Failer, error) {
	connectionInfo := failures.ConnectionInfo{
		Secure:         c.IsSecure(),
		LocalCertsPath: c.LocalCertsDir(),
	}
	return failureRegistry.GetFailer(c.MakeNodes(c.CRDBNodes()), failureModeName, l, connectionInfo)
}
