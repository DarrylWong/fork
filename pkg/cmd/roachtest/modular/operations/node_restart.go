// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package operations

import (
	"context"
	"fmt"
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

// NodeRestartPlan contains the selected node for the restart operation.
type NodeRestartPlan struct {
	NodeID int
}

// NodeRestart creates an operation that randomly selects a node, shuts it down,
// and then restarts it. Uses DynamicStep to select the node during PrePlan.
func NodeRestart() modular.Operation {
	builder := modular.NewDynamicOperation[*NodeRestartPlan]("restart node").
		PrePlan(func(ctx context.Context, l *logger.Logger, h *modular.Helper) (*NodeRestartPlan, error) {
			nodeID := h.RandomAvailableNode()
			l.Printf("Selected node %d for restart", nodeID)
			return &NodeRestartPlan{NodeID: nodeID}, nil
		}).
		WithRun(func(ctx context.Context, l *logger.Logger, h *modular.Helper, plan *NodeRestartPlan) error {
			l.Printf("Stopping node %d", plan.NodeID)
			if err := h.StopNode(plan.NodeID); err != nil {
				return err
			}

			l.Printf("Restarting node %d", plan.NodeID)
			return h.StartNode(plan.NodeID)
		}).
		WithDynamicResourceCallback(func(plan *NodeRestartPlan) ([]modular.ResourceAccess, []modular.ResourceAccess) {
			access := modular.ResourceAccess{
				Action: modular.ActionNodeAvailability,
				Path: modular.NodeAvailabilityResource{
					NodeID: plan.NodeID,
				},
				Lock: true,
			}
			return []modular.ResourceAccess{access}, []modular.ResourceAccess{access}
		}).
		WithDynamicName(func(plan *NodeRestartPlan) string {
			return fmt.Sprintf("restart node %d", plan.NodeID)
		})

	return &NodeRestartOp{
		name:    "node-restart-dynamic",
		builder: modular.NewOperation(builder),
	}
}
