package failures

import (
	"context"
	"fmt"
	"github.com/cockroachdb/cockroach/pkg/roachprod"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/errors"
	"strconv"
	"strings"
)

type PartitionNodeArgs struct {
	Node install.Nodes
}

type IPTablesPartitionNode struct {
	c                *install.SyncedCluster
	l                *logger.Logger
	partitionedNodes []install.Nodes
}

func MakeIPTablesPartitionNode(clusterName string, l *logger.Logger, secure bool) (*IPTablesPartitionNode, error) {
	c, err := roachprod.GetClusterFromCache(l, clusterName, install.SecureOption(secure))
	if err != nil {
		return nil, err
	}

	return &IPTablesPartitionNode{c: c, l: l, partitionedNodes: make([]install.Nodes, 0)}, nil
}

func (f *IPTablesPartitionNode) Description() string {
	return "iptables partition"
}

func (f *IPTablesPartitionNode) run(ctx context.Context, node install.Nodes, args ...string) error {
	if f.c.IsLocal() {
		f.l.Printf("Local cluster detected, skipping iptables command")
		return nil
	}
	cmd := strings.Join(args, " ")
	return f.c.Run(ctx, f.l, f.l.Stdout, f.l.Stderr, install.WithNodes(node), "iptables", cmd)
}

func (f *IPTablesPartitionNode) runOnSingleNode(ctx context.Context, node install.Nodes, args ...string) (install.RunResultDetails, error) {
	if f.c.IsLocal() {
		f.l.Printf("Local cluster detected, skipping iptables command")
		return install.RunResultDetails{}, nil
	}

	cmd := strings.Join(args, " ")
	res, err := f.c.RunWithDetails(ctx, f.l, install.WithNodes(node), "iptables", cmd)
	if err != nil {
		return install.RunResultDetails{}, err
	}
	return res[0], nil
}

func (f *IPTablesPartitionNode) Setup(_ context.Context, _ FailureArgs) error {
	// iptables is already installed by default on Ubuntu.
	return nil
}

func (f *IPTablesPartitionNode) Inject(ctx context.Context, args FailureArgs) error {
	targetNode := args.(PartitionNodeArgs).Node
	f.partitionedNodes = append(f.partitionedNodes, targetNode)

	var partitionNodeCmd = fmt.Sprintf(`
# ensure any failure fails the entire script.
set -e;

# Setting default filter policy
sudo iptables -P INPUT ACCEPT;
sudo iptables -P OUTPUT ACCEPT;

# Drop any node-to-node crdb traffic.
sudo iptables -A INPUT -p tcp --dport {pgport:%[1]d} -j DROP;
sudo iptables -A OUTPUT -p tcp --dport {pgport:%[1]d} -j DROP;

sudo iptables-save
`, targetNode[0])

	f.l.Printf("Partitioning node %d with cmd: %s", targetNode, partitionNodeCmd)
	return f.run(ctx, targetNode, partitionNodeCmd)
}

func (f *IPTablesPartitionNode) Restore(ctx context.Context, args FailureArgs) error {
	nodesToUnpartition := f.partitionedNodes
	if targetNode := args.(PartitionNodeArgs).Node; len(targetNode) != 0 {
		nodesToUnpartition = []install.Nodes{targetNode}
	}

	for _, targetNode := range nodesToUnpartition {
		var revertPartitionNodeCmd = fmt.Sprintf(`
set -e;
sudo iptables -D INPUT -p tcp --dport {pgport:%[1]d} -j DROP;
sudo iptables -D OUTPUT -p tcp --dport {pgport:%[1]d} -j DROP;
sudo iptables-save
`, targetNode[0])

		f.l.Printf("Reverting iptables partition on node %d with cmd: %s", targetNode, revertPartitionNodeCmd)

		if err := f.run(ctx, targetNode, revertPartitionNodeCmd); err != nil {
			return errors.Wrapf(err, "failed to revert iptables partition on node %d", targetNode)
		}
	}
	return nil
}

func (f *IPTablesPartitionNode) Cleanup(_ context.Context) error {
	return nil
}

// PacketsDropped returns the number of packets dropped to a given node due to an iptables rule.
func (f *IPTablesPartitionNode) PacketsDropped(ctx context.Context, node install.Nodes) (int, error) {
	res, err := f.runOnSingleNode(ctx, node, "sudo iptables -L -v -n")
	if err != nil {
		return 0, err
	}
	rows := strings.Split(res.Stdout, "\n")
	// iptables -L outputs rows in the order of: chain, fields, and then values.
	// We care about the values so only look at row 2.
	values := strings.Fields(rows[2])
	if len(values) == 0 {
		return 0, errors.Errorf("no configured iptables rules found:\n%s", res.Stdout)
	}
	packetsDropped, err := strconv.Atoi(values[0])
	return packetsDropped, errors.Wrapf(err, "could not find number of packets dropped, rules found:\n%s", res.Stdout)
}
