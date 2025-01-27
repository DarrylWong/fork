package failureinjection

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/roachprod/failureinjection/failures"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

type NetworkPartitioner struct {
	c                  cluster.Cluster
	l                  *logger.Logger
	networkPartitioner *failures.IPTablesPartitionNode
}

func MakeNetworkPartitioner(c cluster.Cluster, l *logger.Logger) (*NetworkPartitioner, error) {
	networkPartitioner, err := failures.MakeIPTablesPartitionNode(c.MakeNodes(), l, c.IsSecure())
	if err != nil {
		return nil, err
	}
	return &NetworkPartitioner{c: c, l: l, networkPartitioner: networkPartitioner.(*failures.IPTablesPartitionNode)}, nil
}

func (f *NetworkPartitioner) PartitionNode(ctx context.Context, nodes option.NodeListOption) error {
	args := failures.PartitionNodeArgs{
		Nodes: nodes.InstallNodes(),
	}
	return f.networkPartitioner.Inject(ctx, args)
}

func (f *NetworkPartitioner) RestoreNode(ctx context.Context, nodes option.NodeListOption) error {
	args := failures.PartitionNodeArgs{
		Nodes: nodes.InstallNodes(),
	}
	return f.networkPartitioner.Restore(ctx, args)
}

func (f *NetworkPartitioner) RestoreAll(ctx context.Context) error {
	return f.networkPartitioner.Restore(ctx, failures.PartitionNodeArgs{})
}

// PacketsDropped returns the number of packets dropped by the network partitioner.
func (f *NetworkPartitioner) PacketsDropped(ctx context.Context, node option.NodeListOption) (int, error) {
	return f.networkPartitioner.PacketsDropped(ctx, node.InstallNodes())
}
