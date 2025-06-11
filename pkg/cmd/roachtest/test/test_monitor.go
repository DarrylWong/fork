// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package test

import (
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
)

// Monitor is an interface for monitoring cockroach processes during a test.
type Monitor interface {
	ExpectDeaths(nodes option.NodeListOption)
	ResetDeaths(nodes option.NodeListOption)
	ExpectNodeHealth(nodes install.Nodes, event install.MonitorExpectedNodeHealth)
}
