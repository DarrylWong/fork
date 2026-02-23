// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package tests

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/clusterupgrade"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/mixedversion"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/errors"
)

func registerSQLProxyUpgrade(r registry.Registry) {
	r.Add(registry.TestSpec{
		Name:             "sql-proxy-upgrade",
		Timeout:          3 * time.Hour,
		Cluster:          r.MakeClusterSpec(5),
		CompatibleClouds: registry.CloudsWithServiceRegistration,
		Suites:           registry.Suites(registry.MixedVersion, registry.Nightly),
		Monitor:          true,
		Randomized:       true,
		Owner:            registry.OwnerTestEng,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			runSQLProxyUpgrade(ctx, t, c)
		},
	})
}

// runSQLProxyUpgrade verifies that a workload running through the SQL
// proxy continues without errors during a rolling upgrade of the
// tenant. The test:
//
//  1. Uses a system-only mixed version test for the storage cluster.
//  2. Starts a separate-process tenant on all storage nodes.
//  3. Starts the SQL proxy + directory server and routes traffic
//     through it.
//  4. Runs a KV workload through the proxy.
//  5. After the storage cluster upgrade finalizes, performs a rolling
//     restart of the tenant, draining each pod from the directory
//     server before stopping and re-registering after restart.
//  6. Verifies the workload completes without errors.
func runSQLProxyUpgrade(ctx context.Context, t test.Test, c cluster.Cluster) {
	const (
		numStorageNodes    = 4
		virtualClusterName = "proxy-upgrade-tenant"
		kvDuration         = 10 * time.Minute
		rampDuration       = 2 * time.Minute
	)

	storageNodes := c.Range(1, numStorageNodes)
	// Use the last node for the proxy and directory server.
	proxyNode := c.Node(numStorageNodes + 1)

	mvt := mixedversion.NewTest(ctx, t, t.L(), c, storageNodes,
		mixedversion.MinimumSupportedVersion("v23.2.0"),
		mixedversion.EnabledDeploymentModes(mixedversion.SystemOnlyDeployment),
		mixedversion.AlwaysUseLatestPredecessors,
		mixedversion.NeverUseFixtures,
		mixedversion.ClusterSettingOption(
			install.EnvOption([]string{"COCKROACH_TRUST_CLIENT_PROVIDED_SQL_REMOTE_ADDR=true"}),
		),
	)

	var sqlProxy *roachtestutil.SQLProxy

	// startTenant starts the tenant process on all storage nodes with
	// the given version.
	startTenant := func(
		ctx context.Context, l *logger.Logger, v *clusterupgrade.Version,
	) error {
		binaryPath, err := clusterupgrade.UploadCockroach(ctx, t, l, c, storageNodes, v)
		if err != nil {
			return errors.Wrapf(err, "uploading cockroach %s", v)
		}
		startOpts := option.StartVirtualClusterOpts(
			virtualClusterName, storageNodes, option.NoBackupSchedule,
		)
		settings := install.MakeClusterSettings(
			install.BinaryOption(binaryPath),
			install.EnvOption([]string{"COCKROACH_TRUST_CLIENT_PROVIDED_SQL_REMOTE_ADDR=true"}),
		)
		return c.StartServiceForVirtualClusterE(ctx, l, startOpts, settings)
	}

	// startProxy starts the SQL proxy and directory server, registers
	// the tenant and all pods.
	startProxy := func(ctx context.Context, l *logger.Logger) error {
		sqlProxy = roachtestutil.NewSQLProxy(c, l, proxyNode, proxyNode)
		if err := sqlProxy.Start(ctx); err != nil {
			return errors.Wrap(err, "starting SQL proxy")
		}
		if err := sqlProxy.AddTenant(ctx, virtualClusterName); err != nil {
			return errors.Wrap(err, "adding tenant to directory server")
		}
		if err := sqlProxy.AddPod(ctx, storageNodes, virtualClusterName, 0); err != nil {
			return errors.Wrap(err, "adding pods to directory server")
		}
		return nil
	}

	// rollingRestartTenant performs a rolling restart of the tenant,
	// draining each pod from the proxy before stopping the process
	// and re-registering after restart.
	rollingRestartTenant := func(
		ctx context.Context, l *logger.Logger, v *clusterupgrade.Version,
	) error {
		binaryPath, err := clusterupgrade.UploadCockroach(ctx, t, l, c, storageNodes, v)
		if err != nil {
			return errors.Wrapf(err, "uploading cockroach %s", v)
		}

		for _, node := range storageNodes {
			n := c.Node(node)

			// Drain the pod so the proxy migrates sessions away.
			l.Printf("draining pod on n%d", node)
			if err := sqlProxy.DrainPod(ctx, n, virtualClusterName, 0); err != nil {
				return errors.Wrapf(err, "draining pod on n%d", node)
			}

			// Wait for sessions to migrate off this node.
			l.Printf("waiting for sessions to drain from n%d", node)
			tenantDB, err := c.ConnE(ctx, l, node, option.VirtualClusterName(virtualClusterName))
			if err != nil {
				return errors.Wrapf(err, "connecting to tenant on n%d", node)
			}
			if err := sqlProxy.WaitForDrain(ctx, tenantDB); err != nil {
				l.Printf("WARNING: drain wait failed on n%d: %v", node, err)
			}
			tenantDB.Close()

			// Stop the tenant process.
			l.Printf("stopping tenant on n%d", node)
			stopOpts := option.StopVirtualClusterOpts(
				virtualClusterName, n, option.Graceful(shutdownGracePeriod),
			)
			if err := c.StopServiceForVirtualClusterE(ctx, l, stopOpts); err != nil {
				return errors.Wrapf(err, "stopping tenant on n%d", node)
			}

			// Restart with the new version.
			l.Printf("starting tenant on n%d with version %s", node, v)
			startOpts := option.StartVirtualClusterOpts(
				virtualClusterName, n, option.NoBackupSchedule,
			)
			settings := install.MakeClusterSettings(
				install.BinaryOption(binaryPath),
				install.EnvOption([]string{"COCKROACH_TRUST_CLIENT_PROVIDED_SQL_REMOTE_ADDR=true"}),
			)
			if err := c.StartServiceForVirtualClusterE(ctx, l, startOpts, settings); err != nil {
				return errors.Wrapf(err, "starting tenant on n%d", node)
			}

			// Re-register the pod with the directory server.
			l.Printf("re-registering pod on n%d", node)
			if err := sqlProxy.AddPod(ctx, n, virtualClusterName, 0); err != nil {
				return errors.Wrapf(err, "re-registering pod on n%d", node)
			}
		}
		return nil
	}

	// After the storage cluster starts, start the tenant and proxy.
	mvt.OnStartup(
		"start tenant and proxy",
		func(ctx context.Context, l *logger.Logger, rng *rand.Rand, h *mixedversion.Helper) error {
			if err := startTenant(ctx, l, h.Context().FromVersion); err != nil {
				return err
			}
			if err := startProxy(ctx, l); err != nil {
				return err
			}

			// Initialize the workload through the proxy.
			proxyURL, err := sqlProxy.InternalURL(ctx, virtualClusterName)
			if err != nil {
				return errors.Wrap(err, "getting proxy URL")
			}
			binaryPath, err := clusterupgrade.UploadCockroach(ctx, t, l, c, proxyNode, h.Context().FromVersion)
			if err != nil {
				return errors.Wrapf(err, "uploading cockroach to proxy node")
			}
			cmd := fmt.Sprintf(
				"%s workload init kv '%s'",
				binaryPath, proxyURL,
			)
			return c.RunE(ctx, option.WithNodes(proxyNode), cmd)
		},
	)

	// After the storage cluster upgrade finalizes, run KV through the
	// proxy while performing a rolling restart of the tenant.
	mvt.AfterUpgradeFinalized(
		"rolling restart tenant with proxy",
		func(ctx context.Context, l *logger.Logger, rng *rand.Rand, h *mixedversion.Helper) error {
			proxyURL, err := sqlProxy.InternalURL(ctx, virtualClusterName)
			if err != nil {
				return errors.Wrap(err, "getting proxy URL")
			}
			binaryPath, err := clusterupgrade.UploadCockroach(ctx, t, l, c, proxyNode, h.Context().ToVersion)
			if err != nil {
				return errors.Wrapf(err, "uploading cockroach to proxy node")
			}

			// Start the workload in the background.
			workloadErrCh := make(chan error, 1)
			h.Go(func(ctx context.Context, l *logger.Logger) error {
				cmd := fmt.Sprintf(
					"%s workload run kv --duration %s --ramp %s '%s'",
					binaryPath, kvDuration, rampDuration, proxyURL,
				)
				err := c.RunE(ctx, option.WithNodes(proxyNode), cmd)
				workloadErrCh <- err
				return err
			})

			// Let the workload ramp up before starting the rolling restart.
			l.Printf("waiting %s for workload to ramp up", rampDuration)
			select {
			case <-time.After(rampDuration):
			case <-ctx.Done():
				return ctx.Err()
			}

			// Perform the rolling restart while the workload runs.
			if err := rollingRestartTenant(ctx, l, h.Context().ToVersion); err != nil {
				return err
			}

			// Wait for the workload to finish.
			if err := <-workloadErrCh; err != nil {
				return errors.Wrap(err, "kv workload failed during rolling restart")
			}
			return nil
		},
	)

	mvt.Run()
}
