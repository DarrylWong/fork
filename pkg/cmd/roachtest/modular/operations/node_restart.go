// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package operations

import (
	"context"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// NodeRestartOp implements the Operation interface for restarting a random node.
type NodeRestartOp struct {
	name    string
	builder *modular.OperationBuilder
}

// Chain returns the operation's chain of steps.
func (n *NodeRestartOp) Chain() modular.Chain {
	return n.builder.Chain
}

// Name returns the operation's name.
func (n *NodeRestartOp) Name() string {
	return n.name
}

func (n *NodeRestartOp) Timeout() time.Duration {
	return 10 * time.Minute
}

// NodeRestart creates an operation that randomly selects a node, shuts it down,
// and then restarts it. The first step acquires a lock on node availability,
// and the second step releases the lock.
func NodeRestart() modular.Operation {
	// Variable to store the selected node across steps
	var selectedNode int

	nodeAvailability := modular.NodeAvailability{}

	builder := modular.NewOperation("shut down random node",
		func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
			// Pick a random available node
			selectedNode = h.RandomAvailableNode()
			l.Printf("Stopping node %d", selectedNode)

			// Stop the selected node using the helper
			return h.StopNode(selectedNode)
		},
		modular.AcquireLock(nodeAvailability),
		modular.DisableConcurrency(),
	).Then("restart node",
		func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
			l.Printf("Restarting node %d", selectedNode)

			// Restart the previously stopped node using the helper
			return h.StartNode(selectedNode)
		},
		modular.ReleaseLock(nodeAvailability),
		modular.DisableConcurrency(),
	)

	return &NodeRestartOp{
		name:    "node-restart",
		builder: builder,
	}
}
