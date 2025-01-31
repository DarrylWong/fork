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

type PartitionType int

const (
	// Bidirectional drops traffic in both directions.
	Bidirectional PartitionType = iota
	// Incoming drops incoming traffic from the destination
	Incoming
	// Outgoing drops outgoing traffic to the destination
	Outgoing
)

type NetworkPartition struct {
	Source      install.Nodes
	Destination install.Nodes
	Type        PartitionType
}
type NetworkPartitionArgs struct {
	// Each group is partitioned from all other groups, i.e. all packets
	// between nodes in different groups are dropped. If only one group is
	// specified, it is partitioned from all other nodes in the cluster.
	//
	// TODO: Add support for more complex network partitions, e.g. "soft" partitions
	// where not all packets are dropped.
	Partitions []NetworkPartition

	NodesToRestore install.Nodes
}

type IPTablesPartitionNode struct {
	c *install.SyncedCluster
}

func MakeIPTablesPartitionNode(clusterName string, l *logger.Logger, secure bool) (FailureMode, error) {
	c, err := roachprod.GetClusterFromCache(l, clusterName, install.SecureOption(secure))
	if err != nil {
		return nil, err
	}

	return &IPTablesPartitionNode{c: c}, nil
}

const IPTablesNetworkPartitionName = "iptables-network-partition"

func registerIPTablesPartitionNode(r *FailureRegistry) {
	r.add(IPTablesNetworkPartitionName, NetworkPartitionArgs{}, MakeIPTablesPartitionNode)
}

func (f *IPTablesPartitionNode) Description() string {
	return "iptables partition"
}

func (f *IPTablesPartitionNode) run(ctx context.Context, l *logger.Logger, node install.Nodes, args ...string) error {
	if f.c.IsLocal() {
		l.Printf("Local cluster detected, skipping iptables command")
		return nil
	}
	cmd := strings.Join(args, " ")
	return f.c.Run(ctx, l, l.Stdout, l.Stderr, install.WithNodes(node), "iptables", cmd)
}

func (f *IPTablesPartitionNode) runOnSingleNode(ctx context.Context, l *logger.Logger, node install.Nodes, args ...string) (install.RunResultDetails, error) {
	if f.c.IsLocal() {
		l.Printf("Local cluster detected, skipping iptables command")
		return install.RunResultDetails{}, nil
	}

	cmd := strings.Join(args, " ")
	res, err := f.c.RunWithDetails(ctx, l, install.WithNodes(node), "iptables", cmd)
	if err != nil {
		return install.RunResultDetails{}, err
	}
	return res[0], nil
}

func (f *IPTablesPartitionNode) Setup(_ context.Context, _ *logger.Logger, _ FailureArgs) error {
	// iptables is already installed by default on Ubuntu.
	return nil
}

// When dropping both input and output, make sure we drop packets in both
// directions for both the inbound and outbound TCP connections, such that we
// get a proper black hole. Only dropping one direction for both of INPUT and
// OUTPUT will still let e.g. TCP retransmits through, which may affect the
// TCP stack behavior and is not representative of real network outages.
//
// For the asymmetric partitions, only drop packets in one direction since
// this is representative of accidental firewall rules we've seen cause such
// outages in the wild.
const (
	partitionTemplate = `
# ensure any failure fails the entire script.
set -e;

# Setting default filter policy
sudo iptables -P INPUT ACCEPT;
sudo iptables -P OUTPUT ACCEPT;

%s

sudo iptables-save
`

	bidirectionalPartitionCmd = `
# Drop all incoming and outgoing traffic to the ip address.
sudo iptables -A INPUT  -s {ip:%[1]d} -p tcp --dport {pgport:%[1]d} -j DROP;
sudo iptables -A OUTPUT -d {ip:%[1]d} -p tcp --dport {pgport:%[1]d} -j DROP;
sudo iptables -A INPUT  -s {ip:%[1]d} -p tcp --sport {pgport:%[1]d} -j DROP;
sudo iptables -A OUTPUT -d {ip:%[1]d} -p tcp --sport {pgport:%[1]d} -j DROP;
sudo iptables -A INPUT  -s {ip:%[1]d:public} -p tcp --dport {pgport:%[1]d} -j DROP;
sudo iptables -A OUTPUT -d {ip:%[1]d:public} -p tcp --dport {pgport:%[1]d} -j DROP;
sudo iptables -A INPUT  -s {ip:%[1]d:public} -p tcp --sport {pgport:%[1]d} -j DROP;
sudo iptables -A OUTPUT -d {ip:%[1]d:public} -p tcp --sport {pgport:%[1]d} -j DROP;
`
	asymmetricInputPartitionCmd = `
# Drop all incoming traffic from the ip address.
sudo iptables -A INPUT  -s {ip:%[1]d} -p tcp --dport {pgport:%[1]d} -j DROP;
sudo iptables -A INPUT  -s {ip:%[1]d:public} -p tcp --dport {pgport:%[1]d} -j DROP;
`

	asymmetricOutputPartitionCmd = `
# Drop all outgoing traffic to the ip address.
sudo iptables -A OUTPUT  -s {ip:%[1]d} -p tcp --dport {pgport:%[1]d} -j DROP;
sudo iptables -A OUTPUT  -s {ip:%[1]d:public} -p tcp --dport {pgport:%[1]d} -j DROP;
`

	dropAllRulesCmd = `
	set -e;
sudo iptables -F INPUT;
sudo iptables -F OUTPUT;
sudo iptables-save
`
)

func (f *IPTablesPartitionNode) createPartition(ctx context.Context, l *logger.Logger, partition NetworkPartition) error {
	for _, destinationNode := range partition.Destination {
		var cmd string
		switch partition.Type {
		case Bidirectional:
			cmd = fmt.Sprintf(partitionTemplate, fmt.Sprintf(bidirectionalPartitionCmd, destinationNode))
		case Incoming:
			cmd = fmt.Sprintf(partitionTemplate, fmt.Sprintf(asymmetricInputPartitionCmd, destinationNode))
		case Outgoing:
			cmd = fmt.Sprintf(partitionTemplate, fmt.Sprintf(asymmetricOutputPartitionCmd, destinationNode))
		default:
			panic("unhandled default case")
		}
		l.Printf("Dropping packets from nodes %d to node %d with cmd: %s", partition.Source, destinationNode, cmd)
		if err := f.run(ctx, l, partition.Source, cmd); err != nil {
			return err
		}
	}

	return nil
}

func (f *IPTablesPartitionNode) Inject(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	partitions := args.(NetworkPartitionArgs).Partitions
	for _, partition := range partitions {
		if err := f.createPartition(ctx, l, partition); err != nil {
			return err
		}
	}
	return nil
}

func (f *IPTablesPartitionNode) Restore(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	nodesToUnpartition := args.(NetworkPartitionArgs).NodesToRestore
	if nodesToUnpartition == nil {
		nodesToUnpartition = f.c.Nodes
	}

	for _, targetNode := range nodesToUnpartition {
		l.Printf("Reverting iptables partition on node %d with cmd: %s", targetNode, dropAllRulesCmd)
		if err := f.run(ctx, l, install.Nodes{targetNode}, dropAllRulesCmd); err != nil {
			return errors.Wrapf(err, "failed to revert iptables partition on node %d", targetNode)
		}
	}
	return nil
}

func (f *IPTablesPartitionNode) Cleanup(_ context.Context, _ *logger.Logger) error {
	return nil
}

// PacketsDropped returns the number of packets dropped to a given node due to an iptables rule.
func (f *IPTablesPartitionNode) PacketsDropped(ctx context.Context, l *logger.Logger, node install.Nodes) (int, error) {
	res, err := f.runOnSingleNode(ctx, l, node, "sudo iptables -L -v -n")
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
