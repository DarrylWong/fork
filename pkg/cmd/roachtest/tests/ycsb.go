// Copyright 2018 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package tests

import (
	"context"
	gosql "database/sql"
	"fmt"
	"os"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachprod/grafana"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/config"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/prometheus"
	"github.com/stretchr/testify/require"
)

const envYCSBFlags = "ROACHTEST_YCSB_FLAGS"

func registerYCSB(r registry.Registry) {
	workloads := []string{"A", "B", "C", "D", "E", "F"}
	cpusConfigs := []int{8, 32}
	cpusWithReadCommitted := 32
	cpusWithGlobalMVCCRangeTombstone := 32

	// concurrencyConfigs contains near-optimal concurrency levels for each
	// (workload, cpu count) combination. All of these figures were tuned on GCP
	// n1-standard instance types. We should consider implementing a search for
	// the optimal concurrency level in the roachtest itself (see kvbench).
	concurrencyConfigs := map[string] /* workload */ map[int] /* cpus */ int{
		"A": {8: 96, 32: 144},
		"B": {8: 144, 32: 192},
		"C": {8: 144, 32: 192},
		"D": {8: 96, 32: 144},
		"E": {8: 96, 32: 144},
		"F": {8: 96, 32: 144},
	}

	runYCSB := func(
		ctx context.Context, t test.Test, c cluster.Cluster, wl string, cpus int, readCommitted, rangeTombstone bool, stress bool,
	) {
		// For now, we only want to run the zfs tests on GCE, since only GCE supports
		// starting roachprod instances on zfs.
		if c.Spec().FileSystem == spec.Zfs && c.Cloud() != spec.GCE {
			t.Skip("YCSB zfs benchmark can only be run on GCE", "")
		}

		nodes := c.Spec().NodeCount - 1

		conc, ok := concurrencyConfigs[wl][cpus]
		if !ok {
			t.Fatalf("missing concurrency for (workload, cpus) = (%s, %d)", wl, cpus)
		}

		settings := install.MakeClusterSettings()
		if rangeTombstone {
			settings.Env = append(settings.Env, "COCKROACH_GLOBAL_MVCC_RANGE_TOMBSTONE=true")
		}

		c.Put(ctx, t.DeprecatedWorkload(), "./workload", c.Node(nodes+1))
		startOpts := option.NewStartOpts(option.NoBackupSchedule)
		if stress {
			conc = 1200
			fmt.Printf("concurrency = %d\n", conc)
			startOpts = option.DefaultStartOpts()
			startOpts.RoachprodOpts.AdminUIPort = config.DefaultAdminUIPort
		}
		c.Start(ctx, t.L(), startOpts, settings, c.Range(1, nodes))

		if stress {
			startOpts.RoachprodOpts.AdminUIPort = config.DefaultAdminUIPort
			cfg := (&prometheus.Config{}).
				WithPrometheusNode(c.Node(c.Spec().NodeCount).InstallNodes()[0]).
				WithCluster(c.All().InstallNodes()).
				WithNodeExporter(c.All().InstallNodes())

			if err := c.StartGrafana(ctx, t.L(), cfg); err != nil {
				t.Fatal(err)
			}
		}
		defer func() {
			if t.IsDebug() || !stress {
				return // nothing to do
			}
			if err := c.StopGrafana(ctx, t.L(), t.ArtifactsDir()); err != nil {
				t.L().ErrorfCtx(ctx, "error(s) shutting down prom/grafana: %s", err)
			}
		}()

		db := c.Conn(ctx, t.L(), 1)
		err := enableIsolationLevels(ctx, t, db)
		require.NoError(t, err)
		err = WaitFor3XReplication(ctx, t, t.L(), db)
		require.NoError(t, err)
		require.NoError(t, db.Close())

		t.Status("running workload")
		m := c.NewMonitor(ctx, c.Range(1, nodes))
		m.Go(func(ctx context.Context) error {
			var args string
			args += " --ramp=" + ifLocal(c, "0s", "2m")
			if stress {
				args += " --duration=" + ifLocal(c, "10s", "2h")
			} else {
				args += " --duration=" + ifLocal(c, "10s", "30m")
			}

			if readCommitted {
				args += " --isolation-level=read_committed"
			}
			if envFlags := os.Getenv(envYCSBFlags); envFlags != "" {
				args += " " + envFlags
			}
			cmd := fmt.Sprintf(
				"./workload run ycsb --init --insert-count=1000000 --workload=%s --tolerate-errors --concurrency=%d"+
					" --splits=%d --histograms="+t.PerfArtifactsDir()+"/stats.json"+args+
					" {pgurl:1-%d}",
				wl, conc, nodes, nodes)
			c.AddGrafanaAnnotation(ctx, t.L(), grafana.AddAnnotationRequest{Text: cmd})
			c.Run(ctx, option.WithNodes(c.Node(nodes+1)), cmd)
			return nil
		})
		m.Wait()
	}

	for _, wl := range workloads {
		for _, cpus := range cpusConfigs {
			var name string
			if cpus == 8 { // support legacy test name which didn't include cpu
				name = fmt.Sprintf("ycsb/%s/nodes=3", wl)
			} else {
				name = fmt.Sprintf("ycsb/%s/nodes=3/cpu=%d", wl, cpus)
			}
			wl, cpus := wl, cpus
			r.Add(registry.TestSpec{
				Name:      name,
				Owner:     registry.OwnerTestEng,
				Benchmark: true,
				Cluster:   r.MakeClusterSpec(4, spec.CPU(cpus)),
				Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
					runYCSB(ctx, t, c, wl, cpus, false /* readCommitted */, false /* rangeTombstone */, true)
				},
				CompatibleClouds: registry.AllClouds,
				Suites:           registry.Suites(registry.Nightly),
			})

			if wl == "A" {
				r.Add(registry.TestSpec{
					Name:      fmt.Sprintf("zfs/ycsb/%s/nodes=3/cpu=%d", wl, cpus),
					Owner:     registry.OwnerStorage,
					Benchmark: true,
					Cluster:   r.MakeClusterSpec(4, spec.CPU(cpus), spec.SetFileSystem(spec.Zfs)),
					Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
						runYCSB(ctx, t, c, wl, cpus, false /* readCommitted */, false /* rangeTombstone */, false)
					},
					CompatibleClouds: registry.AllExceptAWS,
					Suites:           registry.Suites(registry.Nightly),
				})
			}

			if cpus == cpusWithReadCommitted {
				r.Add(registry.TestSpec{
					Name:      fmt.Sprintf("%s/isolation-level=read-committed", name),
					Owner:     registry.OwnerTestEng,
					Benchmark: true,
					Cluster:   r.MakeClusterSpec(4, spec.CPU(cpus)),
					Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
						runYCSB(ctx, t, c, wl, cpus, true /* readCommitted */, false /* rangeTombstone */, false)
					},
					CompatibleClouds: registry.AllClouds,
					Suites:           registry.Suites(registry.Nightly),
				})
			}

			if cpus == cpusWithGlobalMVCCRangeTombstone {
				r.Add(registry.TestSpec{
					Name:      fmt.Sprintf("%s/mvcc-range-keys=global", name),
					Owner:     registry.OwnerTestEng,
					Benchmark: false,
					Cluster:   r.MakeClusterSpec(4, spec.CPU(cpus)),
					Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
						runYCSB(ctx, t, c, wl, cpus, false /* readCommitted */, true /* rangeTombstone */, false)
					},
					CompatibleClouds: registry.AllClouds,
					Suites:           registry.Suites(registry.Nightly),
				})
			}
		}
	}
	//r.Add(registry.TestSpec{
	//	Name:      "ycsb/A/nodes=50/cpu=8",
	//	Owner:     registry.OwnerTestEng,
	//	Benchmark: true,
	//	Cluster:   r.MakeClusterSpec(50, spec.CPU(8)),
	//	Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
	//		runYCSB(ctx, t, c, "A", 8, false /* readCommitted */, false /* rangeTombstone */, true)
	//	},
	//	CompatibleClouds: registry.AllClouds,
	//	Suites:           registry.ManualOnly,
	//})
	r.Add(registry.TestSpec{
		Name:      "ycsb/F/nodes=50/cpu=8",
		Owner:     registry.OwnerTestEng,
		Benchmark: true,
		Cluster:   r.MakeClusterSpec(50, spec.CPU(8)),
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			runYCSB(ctx, t, c, "F", 8, false /* readCommitted */, false /* rangeTombstone */, true)
		},
		CompatibleClouds: registry.AllClouds,
		Suites:           registry.ManualOnly,
	})
	//r.Add(registry.TestSpec{
	//	Name:      "ycsb/D/nodes=50/cpu=8",
	//	Owner:     registry.OwnerTestEng,
	//	Benchmark: true,
	//	Cluster:   r.MakeClusterSpec(50, spec.CPU(8)),
	//	Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
	//		runYCSB(ctx, t, c, "D", 8, false /* readCommitted */, false /* rangeTombstone */, true)
	//	},
	//	CompatibleClouds: registry.AllClouds,
	//	Suites:           registry.ManualOnly,
	//})
}

func enableIsolationLevels(ctx context.Context, t test.Test, db *gosql.DB) error {
	for _, cmd := range []string{
		// NOTE: even as we switch the default value of these setting to true on
		// master, we should keep these to ensure that the settings are configured
		// properly in mixed-version roachtests.
		`SET CLUSTER SETTING sql.txn.read_committed_isolation.enabled = 'true';`,
		`SET CLUSTER SETTING sql.txn.snapshot_isolation.enabled = 'true';`,
	} {
		if _, err := db.ExecContext(ctx, cmd); err != nil {
			return err
		}
	}
	return nil
}
