// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package failures

import (
	"context"
	gosql "database/sql"
	"fmt"
	"github.com/cockroachdb/cockroach/pkg/util/retry"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach/pkg/roachprod"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
	"github.com/cockroachdb/errors"
)

// FailureArgs describes the args passed to a failure mode.
//
// For now, this interface is not necessarily needed. However, it sets up for
// future failure injection work when we want a failure controller to be able
// to parse args from a YAML file and pass them to a failure controller.
type FailureArgs interface {
}

// FailureMode describes a failure that can be injected into a system.
//
// For now, this interface is not necessarily needed, however it sets up for
// future failure injection work when we want a failure controller to be
// able to inject multiple different types of failures.
type FailureMode interface {
	Description() string

	// Setup any dependencies required for the failure to be injected.
	Setup(ctx context.Context, l *logger.Logger, args FailureArgs) error

	// Inject a failure into the system.
	Inject(ctx context.Context, l *logger.Logger, args FailureArgs) error

	// Recover reverses the effects of Inject. The same args passed to Inject
	// must be passed to Recover.
	Recover(ctx context.Context, l *logger.Logger, args FailureArgs) error

	// Cleanup uninstalls any dependencies that were installed by Setup.
	Cleanup(ctx context.Context, l *logger.Logger, args FailureArgs) error

	// WaitForFailureToPropagate waits until the failure is at full effect.
	WaitForFailureToPropagate(ctx context.Context, l *logger.Logger, args FailureArgs) error

	// WaitForFailureToRecover waits until the failure was recovered completely along with any side effects.
	WaitForFailureToRecover(ctx context.Context, l *logger.Logger, args FailureArgs) error
}

type diskDevice struct {
	name  string
	major int
	minor int
}

// GenericFailure is a generic helper struct that FailureModes can embed to
// provide commonly used functionality that doesn't differ between failure modes,
// e.g. running remote commands on the cluster.
type GenericFailure struct {
	// TODO(Darryl): support specifying virtual clusters
	c *install.SyncedCluster
	// runTitle is the title to prefix command output with.
	runTitle          string
	networkInterfaces []string
	diskDevice        diskDevice
	connCache         []*gosql.DB
}

func makeGenericFailure(
	clusterName string, l *logger.Logger, connectionInfo ConnectionInfo, failureModeName string,
) (*GenericFailure, error) {
	c, err := roachprod.GetClusterFromCache(l, clusterName, install.SecureOption(connectionInfo.Secure), install.PGUrlCertsDirOption(connectionInfo.LocalCertsPath))
	if err != nil {
		return nil, err
	}

	genericFailure := GenericFailure{c: c, runTitle: failureModeName, connCache: make([]*gosql.DB, len(c.Nodes))}
	return &genericFailure, nil
}

func (f *GenericFailure) Run(
	ctx context.Context, l *logger.Logger, node install.Nodes, args ...string,
) error {
	cmd := strings.Join(args, " ")
	l.Printf("running cmd: %s", cmd)
	// In general, most failures shouldn't be run locally out of caution.
	if f.c.IsLocal() {
		l.Printf("Local cluster detected, skipping command execution")
		return nil
	}
	return f.c.Run(ctx, l, l.Stdout, l.Stderr, install.WithNodes(node), fmt.Sprintf("%s-%d", f.runTitle, node), cmd)
}

func (f *GenericFailure) RunWithDetails(
	ctx context.Context, l *logger.Logger, node install.Nodes, args ...string,
) (install.RunResultDetails, error) {
	cmd := strings.Join(args, " ")
	// In general, most failures shouldn't be run locally out of caution.
	if f.c.IsLocal() {
		l.Printf("Local cluster detected, logging command instead of running:\n%s", cmd)
		return install.RunResultDetails{}, nil
	}
	res, err := f.c.RunWithDetails(ctx, l, install.WithNodes(node), fmt.Sprintf("%s-%d", f.runTitle, node), cmd)
	if err != nil {
		return install.RunResultDetails{}, err
	}
	return res[0], nil
}

func (f *GenericFailure) Conn(
	ctx context.Context, l *logger.Logger, node install.Nodes,
) (*gosql.DB, error) {
	nodeIdx := node[0] - 1
	if f.connCache[nodeIdx] == nil {
		desc, err := f.c.DiscoverService(ctx, node[0], "" /* virtualClusterName */, install.ServiceTypeSQL, 0 /* sqlInstance */)
		if err != nil {
			return nil, err
		}
		ip := f.c.Host(node[0])
		if ip == "" {
			return nil, errors.Errorf("empty ip for node %d", node)
		}
		authMode := install.DefaultAuthMode()
		if !f.c.Secure {
			authMode = install.AuthRootCert
		}
		nodeURL := f.c.NodeURL(ip, desc.Port, "" /* virtualClusterName */, desc.ServiceMode, authMode, "" /* database */)
		nodeURL = strings.Trim(nodeURL, "'")
		pgurl, err := url.Parse(nodeURL)
		if err != nil {
			return nil, err
		}
		vals := make(url.Values)
		vals.Add("connect_timeout", "30")
		nodeURL = pgurl.String() + "&" + vals.Encode()
		l.Printf("Creating connection to node %d at %s", node[0], nodeURL)
		f.connCache[nodeIdx], err = gosql.Open("postgres", nodeURL)
		if err != nil {
			return nil, err
		}
	}

	return f.connCache[nodeIdx], nil
}

func (f *GenericFailure) CloseConnections() {
	for _, db := range f.connCache {
		if db != nil {
			_ = db.Close()
		}
	}
}

// NetworkInterfaces returns the network interfaces used by the VMs in the cluster.
// Assumes that all VMs are using the same machine type and will have the same
// network interfaces.
func (f *GenericFailure) NetworkInterfaces(
	ctx context.Context, l *logger.Logger,
) ([]string, error) {
	if f.networkInterfaces == nil {
		res, err := f.c.RunWithDetails(ctx, l, install.WithNodes(f.c.Nodes[:1]), "Get Network Interfaces", "ip -o link show | awk -F ': ' '{print $2}'")
		if err != nil {
			return nil, errors.Wrapf(err, "error when determining network interfaces")
		}
		interfaces := strings.Split(strings.TrimSpace(res[0].Stdout), "\n")
		for _, iface := range interfaces {
			f.networkInterfaces = append(f.networkInterfaces, strings.TrimSpace(iface))
		}
	}
	return f.networkInterfaces, nil
}

func getDiskDevice(ctx context.Context, f *GenericFailure, l *logger.Logger) error {
	if f.diskDevice.name == "" {
		res, err := f.c.RunWithDetails(ctx, l, install.WithNodes(f.c.Nodes[:1]), "Get Disk Device", "lsblk -o NAME,MAJ:MIN,MOUNTPOINTS | grep /mnt/data1 | awk '{print $1, $2}'")
		if err != nil {
			return errors.Wrapf(err, "error when determining block device")
		}
		parts := strings.Split(strings.TrimSpace(res[0].Stdout), " ")
		if len(parts) != 2 {
			return errors.Newf("unexpected output from lsblk: %s", res[0].Stdout)
		}
		f.diskDevice.name = strings.TrimSpace(parts[0])
		major, minor, found := strings.Cut(parts[1], ":")
		if !found {
			return errors.Newf("unexpected output from lsblk: %s", res[0].Stdout)
		}
		if f.diskDevice.major, err = strconv.Atoi(major); err != nil {
			return err
		}
		if f.diskDevice.minor, err = strconv.Atoi(minor); err != nil {
			return err
		}
	}
	return nil
}

func (f *GenericFailure) DiskDeviceName(ctx context.Context, l *logger.Logger) (string, error) {
	if err := getDiskDevice(ctx, f, l); err != nil {
		return "", err
	}
	return "/dev/" + f.diskDevice.name, nil
}

func (f *GenericFailure) DiskDeviceMajorMinor(
	ctx context.Context, l *logger.Logger,
) (int, int, error) {
	if err := getDiskDevice(ctx, f, l); err != nil {
		return 0, 0, err
	}
	return f.diskDevice.major, f.diskDevice.minor, nil
}

func (f *GenericFailure) PingNode(
	ctx context.Context, l *logger.Logger, node install.Nodes,
) error {
	db, err := f.Conn(ctx, l, node)
	if err != nil {
		return err
	}
	return db.PingContext(ctx)
}

func (f *GenericFailure) WaitForSQLReady(
	ctx context.Context, l *logger.Logger, node install.Nodes, timeout time.Duration,
) error {
	start := timeutil.Now()
	err := retryForDuration(ctx, timeout, func() error {
		if err := f.PingNode(ctx, l, node); err == nil {
			l.Printf("Connected to node %d after %s", node, timeutil.Since(start))
			return nil
		}
		return errors.Newf("unable to connect to node %d", node)
	})

	return errors.Wrapf(err, "never connected to node %d after %s", node, timeout)
}

// WaitForSQLUnavailable pings a node until the SQL connection is unavailable.
func (f *GenericFailure) WaitForSQLUnavailable(
	ctx context.Context, l *logger.Logger, node install.Nodes, timeout time.Duration,
) error {
	start := timeutil.Now()
	err := retryForDuration(ctx, timeout, func() error {
		if err := f.PingNode(ctx, l, node); err != nil {
			l.Printf("Connections to node %d unavailable after %s", node, timeutil.Since(start))
			//nolint:returnerrcheck
			return nil
		}
		return errors.Newf("unable to connect to node %d", node)
	})

	return errors.Wrapf(err, "connections to node %d never unavailable after %s", node, timeout)
}

// WaitForProcessDeath checks systemd until the cockroach process is no longer running
// or the timeout is reached.
func (f *GenericFailure) WaitForProcessDeath(
	ctx context.Context, l *logger.Logger, node install.Nodes, timeout time.Duration,
) error {
	start := timeutil.Now()
	err := retryForDuration(ctx, timeout, func() error {
		res, err := f.RunWithDetails(ctx, l, node, "systemctl is-active cockroach-system.service")
		if err != nil {
			return err
		}
		status := strings.TrimSpace(res.Stdout)
		if status != "active" {
			l.Printf("n%d cockroach process exited after %s: %s", node, timeutil.Since(start), status)
			return nil
		}
		return errors.Newf("systemd reported n%d cockroach process as %s", node, status)
	})

	return errors.Wrapf(err, "n%d process never exited after %s", node, timeout)
}

func (f *GenericFailure) StopCluster(
	ctx context.Context, l *logger.Logger, stopOpts roachprod.StopOpts,
) error {
	return f.c.Stop(ctx, l, stopOpts.Sig, stopOpts.Wait, stopOpts.GracePeriod, "" /* VirtualClusterName*/)
}

func (f *GenericFailure) StartCluster(ctx context.Context, l *logger.Logger) error {
	return f.StartNodes(ctx, l, f.c.Nodes)
}

func (f *GenericFailure) StartNodes(
	ctx context.Context, l *logger.Logger, nodes install.Nodes,
) error {
	// Invoke the cockroach start script directly so we restart the nodes with the same
	// arguments as before.
	return f.Run(ctx, l, nodes, "./cockroach.sh")
}

// retryForDuration retries the given function until it returns nil or
// the context timeout is exceeded.
func retryForDuration(ctx context.Context, timeout time.Duration, fn func() error) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	retryOpts := retry.Options{MaxRetries: 0}
	r := retry.StartWithCtx(timeoutCtx, retryOpts)
	for r.Next() {
		if err := fn(); err == nil {
			return nil
		}
	}
	return errors.Newf("failed after %s", timeout)
}

// forEachNode is a helper function that calls fn for each node in nodes.
func forEachNode(nodes install.Nodes, fn func(install.Nodes) error) error {
	// TODO (darryl): Consider parallelizing this, for now all usages
	// are fast enough for sequential calls.
	for _, node := range nodes {
		if err := fn(install.Nodes{node}); err != nil {
			return err
		}
	}
	return nil
}
func (f *GenericFailure) WaitForReplication(
	ctx context.Context, l *logger.Logger, node install.Nodes,
) error {
	db, err := f.Conn(ctx, l, node)
	if err != nil {
		return err
	}

	numReplicasQuery := `SELECT substring(raw_config_sql FROM 'num_replicas\s*=\s*([0-9]+)') AS num_replicas
	FROM [SHOW ZONE CONFIGURATION FOR RANGE default];`

	rows, err := db.QueryContext(ctx, numReplicasQuery)
	if err != nil {
		return err
	}
	var replicationFactor int
	if rows.Next() {
		if err = rows.Scan(&replicationFactor); err != nil {
			return err
		}
	}

	var oldN int
	return runEveryN(ctx, 3*time.Second, func(done chan struct{}) error {
		var n int
		if err := db.QueryRowContext(
			ctx,
			fmt.Sprintf(
				"SELECT count(1) FROM crdb_internal.ranges WHERE array_length(replicas, 1) < %d",
				replicationFactor,
			),
		).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			l.Printf("up-replication complete")
			close(done)
			return nil
		}
		if oldN != n {
			l.Printf("still waiting for full replication (%d ranges left)", n)
		}
		oldN = n
		return nil
	})
}

type unbalancedRanges struct {
	// The store id of the unbalanced stores.
	storeIDs []int
	// Range counts for each store in storeIDs.
	rangeCounts []int
	// Average range count across all stores, not just the unbalanced ones.
	avgRangeCount float64
}

func findUnbalancedStores(ctx context.Context, db *gosql.DB, threshold float64) (unbalancedRanges, error) {
	lowerBound := 1 - threshold
	upperBound := 1 + threshold

	query := fmt.Sprintf(`WITH stats AS (
    SELECT AVG(range_count) AS mean_val
    FROM crdb_internal.kv_store_status
)
SELECT store_id, range_count, stats.mean_val
FROM crdb_internal.kv_store_status, stats
WHERE range_count < mean_val * %f
   OR range_count > mean_val * %f;
`, lowerBound, upperBound)

	var unablancedStores []int
	var rangeCounts []int
	var avgRanges float64
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return unbalancedRanges{}, err
	}
	for rows.Next() {
		var storeID, ranges int
		if err = rows.Scan(&storeID, &ranges, &avgRanges); err != nil {
			return unbalancedRanges{}, err
		}
		unablancedStores = append(unablancedStores, storeID)
		rangeCounts = append(rangeCounts, ranges)
	}
	return unbalancedRanges{
		storeIDs:      unablancedStores,
		rangeCounts:   rangeCounts,
		avgRangeCount: avgRanges,
	}, nil
}

// WaitForReplicaRebalance blocks until the replica count across each store is less than
// `range_rebalance_threshold` percent from the mean. Note that this doesn't wait for
// rebalancing to _fully_ finish; there can still be range events that happen after this.
// We don't know what kind of background workloads may be running concurrently and creating
// range events, so lets just get to a state "close enough", i.e. a state that the allocator
// would consider balanced.
func (f *GenericFailure) WaitForReplicaRebalance(
	ctx context.Context, l *logger.Logger, node install.Nodes,
) error {
	db, err := f.Conn(ctx, l, node)
	if err != nil {
		return err
	}

	// This is the threshold the db uses to determine that a store is under or overfull
	// and needs to be rebalanced.
	var threshold float64
	if err = db.QueryRowContext(
		ctx, "SHOW CLUSTER SETTING kv.allocator.range_rebalance_threshold",
	).Scan(&threshold); err != nil {
		return err
	}

	// If we query too soon after a node is added to the cluster, our calculation may
	// not include those stores. Make sure we observe a few consecutive intervals that confirm
	// our ranges are stable.
	consecutiveStableIntervals := 0
	return runEveryN(ctx, 5*time.Second, func(done chan struct{}) error {
		unbalanced, err := findUnbalancedStores(ctx, db, threshold)
		if err != nil {
			return err
		}

		if len(unbalanced.storeIDs) == 0 {
			consecutiveStableIntervals++
			l.Printf("all stores have range count within %.2f%% of the mean", threshold*100)
			if consecutiveStableIntervals > 2 {
				close(done)
			}
		} else {
			l.Printf("unbalanced stores: %v, ranges: %v, avg: %f", unbalanced.storeIDs, unbalanced.rangeCounts, unbalanced.avgRangeCount)
			consecutiveStableIntervals = 0
		}
		return nil
	})
}

// runEveryN is a helper that runs a func every `queryInterval` until
// the done channel is closed or the context is cancelled.
func runEveryN(
	ctx context.Context, queryInterval time.Duration, f func(done chan struct{}) error,
) error {
	var statsTimer timeutil.Timer
	defer statsTimer.Stop()
	statsTimer.Reset(queryInterval)
	done := make(chan struct{})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return nil
		case <-statsTimer.C:
			statsTimer.Read = true
			if err := f(done); err != nil {
				return err
			}
			statsTimer.Reset(queryInterval)
		}
	}
}

// WaitForRestartedNodesToStabilize is a helper that waits for nodes
// to stabilize after a restart.
func (f *GenericFailure) WaitForRestartedNodesToStabilize(ctx context.Context, l *logger.Logger, nodes install.Nodes) error {
	// First, we block until we are able to connect to each of the nodes
	// as we will use SQL connections to check the status of the cluster.
	if err := forEachNode(nodes, func(n install.Nodes) error {
		return f.WaitForSQLReady(ctx, l, n, time.Minute)
	}); err != nil {
		return err
	}

	// Then, we wait for ranges to be fully replicated. If the restarted nodes were only
	// briefly offline, this will block until the restarted nodes catch up. If the restarted
	// nodes were down long enough for the cluster to consider them dead, the ranges will
	// have been rebalanced to other nodes and this will be a noop.
	if err := f.WaitForReplication(ctx, l, nodes); err != nil {
		return err
	}

	// Finally, we also have to block until the cluster is done rebalancing replicas.
	// If replicas were not moved around during the downtime, this will likely be a noop.
	return f.WaitForReplicaRebalance(ctx, l, nodes)
}
