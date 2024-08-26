// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package operations

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/operation"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
)

type cleanupNetworkPartition struct {
	nodeID int
}

// Cleanup removes the network partition created by the operation.
func (np *cleanupNetworkPartition) Cleanup(
	ctx context.Context, o operation.Operation, c cluster.Cluster,
) {
	o.Status(fmt.Sprintf("remove the partition on node n%d", np.nodeID))
	c.Run(ctx, option.WithNodes(c.Node(np.nodeID)), `sudo iptables -F`)
}

// createNetworkPartition creates a network partition between two random nodes.
func createNetworkPartialPartition(
	ctx context.Context, o operation.Operation, c cluster.Cluster,
) registry.OperationCleanup {
	nodeCount := c.Spec().NodeCount
	if nodeCount <= 1 {
		o.Fatal("not enough nodes to create a partition")
	}
	nodeID := rand.Intn(nodeCount) + 1

	// Create a partial partition between two random nodes.
	var otherNodeID int
	// Choose a different nodeID to partition from.
	for {
		otherNodeID = rand.Intn(nodeCount) + 1
		if otherNodeID != nodeID {
			break
		}
	}

	// Get the internal IPs of the remote node.
	ips, err := c.InternalIP(ctx, o.L(), c.Node(otherNodeID))
	if err != nil {
		o.Fatal(err)
	}
	otherNodeIP := ips[0]
	o.Status(fmt.Sprintf("creating a partition between nodes n%d and n%d", nodeID, otherNodeID))
	// Block all input and output traffic between the two nodes.
	c.Run(ctx, option.WithNodes(c.Node(nodeID)), fmt.Sprintf(`sudo iptables -A INPUT  -p tcp -s %s -j DROP`, otherNodeIP))
	c.Run(ctx, option.WithNodes(c.Node(nodeID)), fmt.Sprintf(`sudo iptables -A OUTPUT -p tcp -d %s -j DROP`, otherNodeIP))

	return &cleanupNetworkPartition{nodeID: nodeID}
}

// createNetworkFullPartition creates a network partition between a random node
// and all other nodes.
func createNetworkFullPartition(
	ctx context.Context, o operation.Operation, c cluster.Cluster,
) registry.OperationCleanup {
	nodeCount := c.Spec().NodeCount
	if nodeCount <= 1 {
		o.Fatal("not enough nodes to create a partition")
	}
	nodeID := rand.Intn(nodeCount) + 1

	// Drop bi-directional traffic between the node and all other nodes on the
	// pgport.
	o.Status(fmt.Sprintf("partition node n%d from the cluster", nodeID))
	c.Run(ctx, option.WithNodes(c.Node(nodeID)), fmt.Sprintf(`sudo iptables -A INPUT  -p tcp --sport {pgport:%d} -j DROP`, nodeID))
	c.Run(ctx, option.WithNodes(c.Node(nodeID)), fmt.Sprintf(`sudo iptables -A OUTPUT -p tcp --sport {pgport:%d} -j DROP`, nodeID))
	c.Run(ctx, option.WithNodes(c.Node(nodeID)), fmt.Sprintf(`sudo iptables -A INPUT  -p tcp --dport {pgport:%d} -j DROP`, nodeID))
	c.Run(ctx, option.WithNodes(c.Node(nodeID)), fmt.Sprintf(`sudo iptables -A OUTPUT -p tcp --dport {pgport:%d} -j DROP`, nodeID))

	return &cleanupNetworkPartition{nodeID: nodeID}
}

// createSwizzleNetworkPartition selects a random subset of nodes in the cluster.
// Then, it “clogs” (stops) each of their network connections one by one over a few seconds.
// Finally, it unclogs them in a random order, again one by one, until they are all up.
func createSwizzleNetworkPartition(
	ctx context.Context, o operation.Operation, c cluster.Cluster,
) registry.OperationCleanup {
	nodeCount := c.Spec().NodeCount
	if nodeCount <= 1 {
		o.Fatal("not enough nodes to create a partition")
	}
	numToClog := rand.Intn(nodeCount/2) + 1
	var nodes []int
	for i := 0; i < nodeCount; i++ {
		nodes = append(nodes, i+1)
	}

	rand.Shuffle(len(nodes), func(i int, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})
	nodes = nodes[:numToClog]

	for _, nodeID := range nodes {
		// Drop bi-directional traffic between the node and all other nodes on the
		// pgport.
		o.Status(fmt.Sprintf("partition node n%d from the cluster", nodeID))
		c.Run(ctx, option.WithNodes(c.Node(nodeID)), fmt.Sprintf(`sudo iptables -A INPUT  -p tcp --sport {pgport:%d} -j DROP`, nodeID))
		c.Run(ctx, option.WithNodes(c.Node(nodeID)), fmt.Sprintf(`sudo iptables -A OUTPUT -p tcp --sport {pgport:%d} -j DROP`, nodeID))
		c.Run(ctx, option.WithNodes(c.Node(nodeID)), fmt.Sprintf(`sudo iptables -A INPUT  -p tcp --dport {pgport:%d} -j DROP`, nodeID))
		c.Run(ctx, option.WithNodes(c.Node(nodeID)), fmt.Sprintf(`sudo iptables -A OUTPUT -p tcp --dport {pgport:%d} -j DROP`, nodeID))
	}
	return &cleanupSwizzleNetworkPartition{nodesClogged: nodes}
}

type cleanupSwizzleNetworkPartition struct {
	nodesClogged []int
}

// Cleanup removes the network partition created by the operation.
func (np *cleanupSwizzleNetworkPartition) Cleanup(
	ctx context.Context, o operation.Operation, c cluster.Cluster,
) {
	rand.Shuffle(len(np.nodesClogged), func(i int, j int) {
		np.nodesClogged[i], np.nodesClogged[j] = np.nodesClogged[j], np.nodesClogged[i]
	})

	for _, nodeID := range np.nodesClogged {
		o.Status(fmt.Sprintf("remove the partition on node n%d", nodeID))
		c.Run(ctx, option.WithNodes(c.Node(nodeID)), `sudo iptables -F`)
	}
}

// registerNetworkPartition registers both the full and partial network
// partition operations.
func registerNetworkPartition(r registry.Registry) {
	r.AddOperation(registry.OperationSpec{
		Name:             "network-partition/full",
		Owner:            registry.OwnerKV,
		Timeout:          1 * time.Minute,
		CompatibleClouds: registry.AllClouds,
		Dependencies:     []registry.OperationDependency{registry.OperationRequiresZeroUnderreplicatedRanges},
		Run:              createNetworkFullPartition,
	})
	r.AddOperation(registry.OperationSpec{
		Name:             "network-partition/partial",
		Owner:            registry.OwnerKV,
		Timeout:          1 * time.Minute,
		CompatibleClouds: registry.AllClouds,
		Dependencies:     []registry.OperationDependency{registry.OperationRequiresZeroUnderreplicatedRanges},
		Run:              createNetworkPartialPartition,
	})
	r.AddOperation(registry.OperationSpec{
		Name:             "network-partition/swizzle",
		Owner:            registry.OwnerKV,
		Timeout:          1 * time.Minute,
		CompatibleClouds: registry.AllClouds,
		Dependencies:     []registry.OperationDependency{registry.OperationRequiresZeroUnderreplicatedRanges},
		Run:              createSwizzleNetworkPartition,
	})
}
