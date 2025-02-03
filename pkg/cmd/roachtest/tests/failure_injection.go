package tests

import (
	"context"
	"fmt"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/failureinjection/failures"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/errors"
	"math/rand"
	"strings"
	"time"
)

type failureSmokeTest struct {
	testName    string
	failureName string
	args        failures.FailureArgs

	// Whether to wipe the cluster before the failure is injected.
	wipeCluster bool
	// Validate that the failure was injected correctly
	validateFailure func(ctx context.Context, l *logger.Logger, c cluster.Cluster) error
	validateRestore func(ctx context.Context, l *logger.Logger, c cluster.Cluster) error
}

func (t *failureSmokeTest) run(ctx context.Context, l *logger.Logger, c cluster.Cluster, fr *failures.FailureRegistry) error {
	failure, err := fr.GetFailure(c.MakeNodes(), t.failureName, l, c.IsSecure())
	if err != nil {
		return err
	}
	if err = failure.Setup(ctx, l, t.args); err != nil {
		return err
	}
	if err = failure.Inject(ctx, l, t.args); err != nil {
		return err
	}
	// Allow the failure to take effect.
	l.Printf("sleeping for 30s to allow failure to take effect")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(30 * time.Second):
	}
	if err = t.validateFailure(ctx, l, c); err != nil {
		return err
	}
	if err = failure.Restore(ctx, l, t.args); err != nil {
		return err
	}
	if err = t.validateRestore(ctx, l, c); err != nil {
		return err
	}
	if err = failure.Cleanup(ctx, l); err != nil {
		return err
	}
	return nil
}

func (t *failureSmokeTest) noopRun(ctx context.Context, l *logger.Logger, c cluster.Cluster, fr *failures.FailureRegistry) error {
	if err := t.validateFailure(ctx, l, c); err == nil {
		return errors.New("no failure was injected but validation still passed")
	}
	if err := t.validateRestore(ctx, l, c); err != nil {
		return errors.Wrapf(err, "no failure was injected but post restore validation still failed")
	}
	return nil
}

var bidirectionalNetworkPartitionTest = failureSmokeTest{
	testName:    "bidirectional network partition",
	failureName: failures.IPTablesNetworkPartitionName,
	args: failures.NetworkPartitionArgs{
		Partitions: []failures.NetworkPartition{{
			Source:      install.Nodes{1},
			Destination: install.Nodes{3},
			Type:        failures.Bidirectional,
		}},
	},
	validateFailure: func(ctx context.Context, l *logger.Logger, c cluster.Cluster) error {
		if err := c.Install(ctx, l, c.CRDBNodes(), "nmap"); err != nil {
			return err
		}
		blocked, err := checkPortBlocked(ctx, l, c, c.Nodes(1), c.Nodes(3))
		if err != nil {
			return err
		}
		if !blocked {
			return fmt.Errorf("expected connections from node 1 to node 3 to be blocked")
		}

		blocked, err = checkPortBlocked(ctx, l, c, c.Nodes(1), c.Nodes(2))
		if err != nil {
			return err
		}
		if blocked {
			return fmt.Errorf("expected connections from node 1 to node 2 to not be blocked")
		}
		return nil
	},
	validateRestore: func(ctx context.Context, l *logger.Logger, c cluster.Cluster) error {
		blocked, err := checkPortBlocked(ctx, l, c, c.Nodes(1), c.Nodes(3))
		if err != nil {
			return err
		}
		if blocked {
			return fmt.Errorf("expected connections from node 1 to node 3 to not be blocked")
		}
		return err
	},
}

var asymmetricNetworkPartitionTest = failureSmokeTest{
	testName:    "asymmetric network partition",
	failureName: failures.IPTablesNetworkPartitionName,
	args: failures.NetworkPartitionArgs{
		Partitions: []failures.NetworkPartition{{
			Source:      install.Nodes{1},
			Destination: install.Nodes{3},
			Type:        failures.Incoming,
		}},
	},
	validateFailure: func(ctx context.Context, l *logger.Logger, c cluster.Cluster) error {
		if err := c.Install(ctx, l, c.CRDBNodes(), "nmap"); err != nil {
			return err
		}
		blocked, err := checkPortBlocked(ctx, l, c, c.Nodes(1), c.Nodes(3))
		if err != nil {
			return err
		}
		if blocked {
			return fmt.Errorf("expected connections from node 1 to node 3 to be open")
		}

		blocked, err = checkPortBlocked(ctx, l, c, c.Nodes(3), c.Nodes(1))
		if err != nil {
			return err
		}
		if !blocked {
			return fmt.Errorf("expected connections from node 3 to node 1 to be blocked")
		}
		return nil
	},
	validateRestore: func(ctx context.Context, l *logger.Logger, c cluster.Cluster) error {
		blocked, err := checkPortBlocked(ctx, l, c, c.Nodes(1), c.Nodes(3))
		if err != nil {
			return err
		}
		if blocked {
			return fmt.Errorf("expected connections from node 1 to node 3 to not be blocked")
		}
		return err
	},
}

func checkPortBlocked(ctx context.Context, l *logger.Logger, c cluster.Cluster, from, to option.NodeListOption) (bool, error) {
	res, err := c.RunWithDetailsSingleNode(ctx, l, option.WithNodes(from), fmt.Sprintf("nmap -p {pgport%[1]s} {ip%[1]s} -oG - | awk '/Ports:/{print $5}'", to))
	if err != nil {
		return false, err
	}
	return strings.Contains(res.Stdout, "filtered"), nil
}

func runFailureSmokeTest(ctx context.Context, t test.Test, c cluster.Cluster) {
	c.Start(ctx, t.L(), option.DefaultStartOpts(), install.MakeClusterSettings(), c.CRDBNodes())
	c.Run(ctx, option.WithNodes(c.WorkloadNode()), "./cockroach workload init tpcc --warehouses=100 {pgurl:1}")
	cancel := t.GoWithCancel(func(goCtx context.Context, l *logger.Logger) error {
		return c.RunE(goCtx, option.WithNodes(c.WorkloadNode()), "./cockroach workload run tpcc --tolerate-errors --warehouses=100 {pgurl:1-3}")
	})
	defer cancel()
	fr := failures.NewFailureRegistry()
	fr.Register()

	var failureSmokeTests = []failureSmokeTest{
		bidirectionalNetworkPartitionTest,
		asymmetricNetworkPartitionTest,
	}

	rand.Shuffle(len(failureSmokeTests), func(i, j int) {
		failureSmokeTests[i], failureSmokeTests[j] = failureSmokeTests[j], failureSmokeTests[i]
	})

	for _, test := range failureSmokeTests {
		t.L().Printf("running %s test", test.testName)
		if err := test.run(ctx, t.L(), c, fr); err != nil {
			t.Fatal(err)
		}
		t.L().Printf("%s test complete", test.testName)
	}
}

func runNoopSmokeTest(ctx context.Context, t test.Test, c cluster.Cluster) {
	c.Start(ctx, t.L(), option.DefaultStartOpts(), install.MakeClusterSettings(), c.CRDBNodes())
	c.Run(ctx, option.WithNodes(c.WorkloadNode()), "./cockroach workload init tpcc --warehouses=100 {pgurl:1}")
	cancel := t.GoWithCancel(func(goCtx context.Context, l *logger.Logger) error {
		return c.RunE(goCtx, option.WithNodes(c.WorkloadNode()), "./cockroach workload run tpcc --tolerate-errors --warehouses=100 {pgurl:1-3}")
	})
	defer cancel()
	fr := failures.NewFailureRegistry()
	fr.Register()

	var failureSmokeTests = []failureSmokeTest{
		bidirectionalNetworkPartitionTest,
		asymmetricNetworkPartitionTest,
	}

	rand.Shuffle(len(failureSmokeTests), func(i, j int) {
		failureSmokeTests[i], failureSmokeTests[j] = failureSmokeTests[j], failureSmokeTests[i]
	})

	for _, test := range failureSmokeTests {
		t.L().Printf("running %s test", test.testName)
		if err := test.noopRun(ctx, t.L(), c, fr); err != nil {
			t.Fatal(err)
		}
		t.L().Printf("%s test complete", test.testName)
	}
}

func registerFISmokeTest(r registry.Registry) {
	r.Add(registry.TestSpec{
		Name:             "failure-injection-smoke-test",
		Owner:            registry.OwnerTestEng,
		Cluster:          r.MakeClusterSpec(4, spec.WorkloadNode(), spec.CPU(2), spec.WorkloadNodeCPU(2), spec.ReuseNone()),
		CompatibleClouds: registry.OnlyGCE, // dmsetup only configured for gce
		Suites:           registry.ManualOnly,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			runFailureSmokeTest(ctx, t, c)
		},
	})
	r.Add(registry.TestSpec{
		Name:             "failure-injection-noop-smoke-test",
		Owner:            registry.OwnerTestEng,
		Cluster:          r.MakeClusterSpec(4, spec.WorkloadNode(), spec.CPU(2), spec.WorkloadNodeCPU(2), spec.ReuseNone()),
		CompatibleClouds: registry.OnlyGCE, // dmsetup only configured for gce
		Suites:           registry.ManualOnly,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			runNoopSmokeTest(ctx, t, c)
		},
	})
}
