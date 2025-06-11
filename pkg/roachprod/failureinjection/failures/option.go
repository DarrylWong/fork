// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package failures

import "github.com/cockroachdb/cockroach/pkg/roachprod/install"

type ClusterOptionFunc func(*ClusterOptions)

func Secure(secure bool) ClusterOptionFunc {
	return func(o *ClusterOptions) {
		o.secure = secure
	}
}

func LocalCertsPath(certs string) ClusterOptionFunc {
	return func(o *ClusterOptions) {
		o.localCertsPath = certs
	}
}

func ExpectNodeHealthFunc(
	fn func(nodes install.Nodes, health install.MonitorExpectedNodeHealth),
) ClusterOptionFunc {
	return func(o *ClusterOptions) {
		o.monitorFunc = fn
	}
}
