package failureinjection

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil"
	"github.com/cockroachdb/cockroach/pkg/roachprod/failureinjection/failures"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
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
	l, logfile, err := roachtestutil.LoggerForCmd(f.l, f.c.All(), "create-network-partition")
	if err != nil {
		f.l.Printf("failed to create logger: %v", err)
	}
	defer l.Close()
	f.l.Printf("creating network partition on node %s; details in: %s.log", nodes, logfile)

	args := failures.NetworkPartitionArgs{
		PartitionGroups: []install.Nodes{nodes.InstallNodes()},
	}
	return f.networkPartitioner.Inject(ctx, l, args)
}

func (f *NetworkPartitioner) RestoreNode(ctx context.Context, nodes option.NodeListOption) error {
	l, logfile, err := roachtestutil.LoggerForCmd(f.l, f.c.All(), "restore-network-partition")
	if err != nil {
		f.l.Printf("failed to create logger: %v", err)
	}
	defer l.Close()
	f.l.Printf("restoring network partition on node %s; details in: %s.log", nodes, logfile)

	args := failures.NetworkPartitionArgs{
		NodesToRestore: nodes.InstallNodes(),
	}
	return f.networkPartitioner.Restore(ctx, l, args)
}

func (f *NetworkPartitioner) RestoreAll(ctx context.Context) error {
	l, logfile, err := roachtestutil.LoggerForCmd(f.l, f.c.All(), "restore-all-network-partition")
	if err != nil {
		f.l.Printf("failed to create logger: %v", err)
	}
	defer l.Close()
	f.l.Printf("restoring network partition on all nodes; details in: %s.log", logfile)

	return f.networkPartitioner.Restore(ctx, l, failures.NetworkPartitionArgs{})
}

// PacketsDropped returns the number of packets dropped by the network partitioner.
func (f *NetworkPartitioner) PacketsDropped(ctx context.Context, node option.NodeListOption) (int, error) {
	l, logfile, err := roachtestutil.LoggerForCmd(f.l, f.c.All(), "packets-dropped")
	if err != nil {
		f.l.Printf("failed to create logger: %v", err)
	}
	defer l.Close()
	f.l.Printf("checking packets dropped; details in: %s.log", logfile)

	return f.networkPartitioner.PacketsDropped(ctx, l, node.InstallNodes())
}
