// Copyright 2023 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package tests

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/clusterupgrade"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/prometheus"
	"github.com/cockroachdb/cockroach/pkg/testutils/release"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
)

func registerIndexBackfill(r registry.Registry) {
	clusterSpec := r.MakeClusterSpec(
		10, /* nodeCount */
		spec.CPU(8),
		spec.WorkloadNode(),
		spec.WorkloadNodeCPU(8),
		spec.VolumeSize(500),
		spec.GCEVolumeType("pd-ssd"),
		spec.GCEMachineType("n2-standard-8"),
		spec.GCEZones("us-east1-b"),
	)

	r.Add(registry.TestSpec{
		Name:             "admission-control/index-backfill",
		Timeout:          6 * time.Hour,
		Owner:            registry.OwnerAdmissionControl,
		Benchmark:        true,
		CompatibleClouds: registry.OnlyGCE,
		Suites:           registry.ManualOnly,
		// TODO(aaditya): Revisit this as part of #111614.
		//Suites:           registry.Suites(registry.Weekly),
		//Tags:             registry.Tags(`weekly`),
		Cluster:        clusterSpec,
		SnapshotPrefix: "tpce-100k-n9",
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			// Set up TPC-E with 100k customers. Do so using a published
			// CRDB release, since we'll use this state to generate disk
			// snapshots.
			runTPCE(ctx, t, c, tpceOptions{
				start: func(ctx context.Context, t test.Test, c cluster.Cluster) {
					pred, err := release.LatestPredecessor(t.BuildVersion())
					if err != nil {
						t.Fatal(err)
					}

					path, err := clusterupgrade.UploadCockroach(
						ctx, t, t.L(), c, c.All(), clusterupgrade.MustParseVersion(pred),
					)
					if err != nil {
						t.Fatal(err)
					}

					// Copy over the binary to ./cockroach and run it from
					// there. This test captures disk snapshots, which are
					// fingerprinted using the binary version found in this
					// path. The reason it can't just poke at the running
					// CRDB process is because when grabbing snapshots, CRDB
					// is not running.
					c.Run(ctx, option.WithNodes(c.All()), fmt.Sprintf("cp %s ./cockroach", path))
					settings := install.MakeClusterSettings(install.NumRacksOption(len(c.CRDBNodes())))
					startOpts := option.NewStartOpts(option.NoBackupSchedule)
					roachtestutil.SetDefaultSQLPort(c, &startOpts.RoachprodOpts)
					roachtestutil.SetDefaultAdminUIPort(c, &startOpts.RoachprodOpts)
					if err := c.StartE(ctx, t.L(), startOpts, settings, c.CRDBNodes()); err != nil {
						t.Fatal(err)
					}
				},
				customers:          100_000,
				disablePrometheus:  true,
				snapshotName:       t.SnapshotPrefix(),
				estimatedSetupTime: 4 * time.Hour,
				nodes:              len(c.CRDBNodes()),
				cpus:               clusterSpec.CPUs,
				ssds:               1,
				onlySetup:          true,
			})

			promCfg := &prometheus.Config{}
			promCfg.WithPrometheusNode(c.WorkloadNode().InstallNodes()[0]).
				WithNodeExporter(c.CRDBNodes().InstallNodes()).
				WithCluster(c.CRDBNodes().InstallNodes()).
				WithGrafanaDashboard("https://go.crdb.dev/p/index-admission-control-grafana").
				WithScrapeConfigs(
					prometheus.MakeWorkloadScrapeConfig("workload", "/",
						makeWorkloadScrapeNodes(
							c.WorkloadNode().InstallNodes()[0],
							[]workloadInstance{{nodes: c.WorkloadNode()}},
						),
					),
				)

			// Run the foreground TPC-E workload for 20k customers for 1hr. Run
			// large index backfills while it's running.
			runTPCE(ctx, t, c, tpceOptions{
				customers:        100_000,
				activeCustomers:  20_000,
				threads:          400,
				skipCleanup:      true,
				ssds:             1,
				skipInit:         true,
				nodes:            clusterSpec.NodeCount - 1,
				cpus:             clusterSpec.CPUs,
				prometheusConfig: promCfg,
				workloadDuration: time.Hour + 30*time.Minute,
				during: func(ctx context.Context) error {
					db := c.Conn(ctx, t.L(), 1)
					defer db.Close()

					// Defeat https://github.com/cockroachdb/cockroach/issues/98311.
					if _, err := db.ExecContext(ctx,
						"SET CLUSTER SETTING kv.gc_ttl.strict_enforcement.enabled = false;",
					); err != nil {
						t.Fatal(err)
					}

					t.Status(fmt.Sprintf("recording baseline performance (<%s)", 5*time.Minute))
					time.Sleep(5 * time.Minute)

					// Choose index creations and primary key changes that would
					// take ~30 minutes each. Offset them by 5 minutes.
					//
					// TODO(irfansharif): These now take closer to an hour after
					// https://github.com/cockroachdb/cockroach/pull/109085. Do
					// something about it if customers complain.
					m := c.NewDeprecatedMonitor(ctx, c.CRDBNodes())
					m.Go(func(ctx context.Context) error {
						t.Status(fmt.Sprintf("starting index creation (<%s)", 30*time.Minute))
						_, err := db.ExecContext(ctx,
							fmt.Sprintf("CREATE INDEX index_%s ON tpce.cash_transaction (ct_dts)",
								timeutil.Now().Format("20060102_T150405"),
							),
						)
						t.Status("finished index creation")
						return err
					})
					m.Go(func(ctx context.Context) error {
						// TODO(irfansharif): Is the re-entrant? As in,
						// effective when re-running the roachtest against the
						// same cluster that's already run the test once? Useful
						// to make it so if possible, to run things more
						// iteratively.
						time.Sleep(5 * time.Minute)
						t.Status(fmt.Sprintf("starting primary key change (<%s)", 30*time.Minute))
						_, err := db.ExecContext(ctx,
							"ALTER TABLE tpce.holding_history ALTER PRIMARY KEY USING COLUMNS (hh_h_t_id ASC, hh_t_id ASC, hh_before_qty ASC)",
						)
						t.Status("finished primary key change")
						return err
					})
					m.Wait()

					t.Status(fmt.Sprintf("waiting for workload to finish (<%s)", 50*time.Minute))
					return nil
				},
			})
		},
	})
}
