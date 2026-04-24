// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package tests

import (
	"context"
	gosql "database/sql"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/jobs/jobspb"
	"github.com/cockroachdb/cockroach/pkg/testutils"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
)

func registerLDRRevisionStreamTest(r registry.Registry) {
	clusterSpec := multiClusterSpec{
		leftNodes:  3,
		rightNodes: 3,
		clusterOpts: []spec.Option{
			spec.CPU(8),
			spec.WorkloadNode(),
			spec.WorkloadNodeCPU(8),
			spec.VolumeSize(100),
		},
	}

	r.Add(registry.TestSpec{
		Name:             "ldr/revision-stream",
		Owner:            registry.OwnerCDC,
		Timeout:          45 * time.Minute,
		CompatibleClouds: registry.OnlyGCE,
		Suites:           registry.Suites(registry.Nightly),
		Cluster:          clusterSpec.ToSpec(r),
		Leases:           registry.MetamorphicLeases,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			rng, seed := randutil.NewPseudoRand()
			t.L().Printf("random seed is %d", seed)
			mc := multiCluster{
				c:    c,
				rng:  rng,
				spec: clusterSpec,
			}
			setup, cleanup := mc.Start(ctx, t)
			defer cleanup()
			runLDRRevisionStream(ctx, t, c, setup)
		},
	})

	r.Add(registry.TestSpec{
		Name:             "ldr/revision-stream/gc-stress",
		Owner:            registry.OwnerCDC,
		Timeout:          60 * time.Minute,
		CompatibleClouds: registry.OnlyGCE,
		Suites:           registry.Suites(registry.Nightly),
		Cluster:          clusterSpec.ToSpec(r),
		Leases:           registry.MetamorphicLeases,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			rng, seed := randutil.NewPseudoRand()
			t.L().Printf("random seed is %d", seed)
			mc := multiCluster{
				c:    c,
				rng:  rng,
				spec: clusterSpec,
			}
			setup, cleanup := mc.Start(ctx, t)
			defer cleanup()
			runLDRRevisionStreamGCStress(ctx, t, c, setup)
		},
	})
}

func runLDRRevisionStream(
	ctx context.Context, t test.Test, c cluster.Cluster, setup multiClusterSetup,
) {
	duration := 10 * time.Minute
	maxBlockBytes := 1024
	tickWait := 10 * time.Minute

	if c.IsLocal() {
		duration = 30 * time.Second
		maxBlockBytes = 32
		tickWait = 30 * time.Second
	}

	dbName := "kv"
	tableName := "kv"

	kvWorkload := replicateKV{
		readPercent:             0,
		debugRunDuration:        duration,
		maxBlockBytes:           maxBlockBytes,
		initRows:                0,
		tolerateErrors:          true,
		initWithSplitAndScatter: !c.IsLocal(),
		uniform:                 true,
	}

	// ---------------------------------------------------------------
	// Phase 1: Init the workload on the left cluster, start the
	// revlog, record the cursor, then create matching schema on the
	// right cluster.
	// ---------------------------------------------------------------
	t.Status("initializing kv workload on left cluster")
	c.Run(ctx,
		option.WithNodes(setup.workloadNode),
		kvWorkload.sourceInitCmd("system", setup.left.nodes))

	const backupDest = "nodelocal://1/revlog"
	t.Status("starting continuous backup with revision stream")
	setup.left.sysSQL.Exec(t, fmt.Sprintf(
		"BACKUP DATABASE %s INTO '%s' WITH REVISION STREAM", dbName, backupDest))

	var revlogJobID int
	testutils.SucceedsWithin(t, func() error {
		return setup.left.db.QueryRow(
			"SELECT job_id FROM [SHOW JOBS] WHERE description LIKE 'REVLOG:%' ORDER BY created DESC LIMIT 1",
		).Scan(&revlogJobID)
	}, 30*time.Second)
	t.L().Printf("revlog sibling job ID = %d", revlogJobID)

	waitForJobRunning(t, setup.left.db, revlogJobID)
	t.L().Printf("revlog job is running")

	setup.left.sysSQL.Exec(t, fmt.Sprintf(
		"SET CLUSTER SETTING physical_replication.producer.revision_stream.uri = '%s'",
		backupDest))

	// Record the cursor after the revlog is running. LDR with
	// WITH CURSOR skips the initial scan, so only changes after
	// the cursor are replicated. We use initRows=0 so no data
	// predates the cursor.
	var cursorStr string
	setup.left.sysSQL.QueryRow(t,
		"SELECT cluster_logical_timestamp()").Scan(&cursorStr)
	t.L().Printf("recorded cursor = %s", cursorStr)

	setup.right.sysSQL.Exec(t, fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", dbName))
	setup.right.sysSQL.Exec(t, fmt.Sprintf("USE %s", dbName))
	setup.right.sysSQL.Exec(t, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (k INT8 NOT NULL, v BYTES NOT NULL, PRIMARY KEY (k ASC))", tableName))

	// ---------------------------------------------------------------
	// Phase 2: Start the workload and let it run while ticks
	// accumulate, so the revision stream has real data to replay.
	// ---------------------------------------------------------------
	monitor := c.NewDeprecatedMonitor(ctx, setup.left.nodes.Merge(setup.right.nodes))

	monitor.Go(func(ctx context.Context) error {
		t.Status("running kv workload")
		return c.RunE(ctx, option.WithNodes(setup.workloadNode),
			kvWorkload.sourceRunCmd("system", setup.left.nodes))
	})

	t.Status(fmt.Sprintf("waiting %s for ticks to accumulate with workload running", tickWait))
	select {
	case <-time.After(tickWait):
	case <-ctx.Done():
		return
	}

	// ---------------------------------------------------------------
	// Phase 3: Start LDR with cursor in the past. The revision
	// stream replays the ticks accumulated during the tick wait,
	// then hands off to the live KV rangefeed.
	// ---------------------------------------------------------------
	t.Status("creating external connection and starting LDR")
	externalConnCmd := "CREATE EXTERNAL CONNECTION IF NOT EXISTS '%s' AS '%s'"
	setup.right.sysSQL.Exec(t, fmt.Sprintf(
		externalConnCmd, leftExternalConn.Host, setup.left.PgURLForDatabase(dbName)))

	setup.right.sysSQL.Exec(t, fmt.Sprintf("USE %s", dbName))
	var ldrJobID int
	setup.right.sysSQL.QueryRow(t, fmt.Sprintf(
		"CREATE LOGICAL REPLICATION STREAM FROM TABLE %s ON $1 INTO TABLE %s WITH CURSOR=$2",
		tableName, tableName),
		leftExternalConn.String(), cursorStr,
	).Scan(&ldrJobID)
	t.L().Printf("LDR job ID = %d", ldrJobID)

	t.Status("waiting for initial catch-up")
	waitForReplicatedTime(t, ldrJobID, setup.right.db, getLogicalDataReplicationJobInfo, 5*time.Minute)

	// Wait for the workload to finish.
	monitor.Wait()

	// ---------------------------------------------------------------
	// Phase 6: Wait for convergence and verify correctness.
	// ---------------------------------------------------------------
	// Use the source cluster's timestamp to ensure all committed
	// writes are captured in the replication target.
	var nowStr string
	setup.left.sysSQL.QueryRow(t, "SELECT cluster_logical_timestamp()").Scan(&nowStr)
	now := timeutil.Now()
	t.Status("waiting for replicated time to catch up")
	waitForReplicatedTimeToReachTimestamp(
		t, ldrJobID, setup.right.db, getLogicalDataReplicationJobInfo, 5*time.Minute, now)

	// TODO(darryl): reduce GC TTL on the source cluster to stress
	// the revision stream path — ensuring KV catch-up would fail
	// without the revlog serving the catch-up scan.

	t.Status("verifying correctness via fingerprints")
	queryStmt := fmt.Sprintf("SHOW EXPERIMENTAL_FINGERPRINTS FROM TABLE %s.%s", dbName, tableName)

	leftFP := setup.left.sysSQL.QueryStr(t, queryStmt)
	rightFP := setup.right.sysSQL.QueryStr(t, queryStmt)
	require.Equal(t, leftFP, rightFP, "fingerprint mismatch for table %s", tableName)
	t.L().Printf("fingerprints match")
}

func runLDRRevisionStreamGCStress(
	ctx context.Context, t test.Test, c cluster.Cluster, setup multiClusterSetup,
) {
	gcTTL := 5 * time.Minute
	gcWait := 15 * time.Minute
	maxBlockBytes := 1024

	if c.IsLocal() {
		gcTTL = 30 * time.Second
		gcWait = 2 * time.Minute
		maxBlockBytes = 32
	}

	dbName := "kv"
	tableName := "kv"

	kvWorkload := replicateKV{
		readPercent:             0,
		debugRunDuration:        gcWait,
		maxBlockBytes:           maxBlockBytes,
		initRows:                1000,
		tolerateErrors:          true,
		initWithSplitAndScatter: !c.IsLocal(),
		uniform:                 true,
	}

	// ---------------------------------------------------------------
	// Phase 1: Init the workload and start the revision stream so it
	// captures all writes from this point forward.
	// ---------------------------------------------------------------
	t.Status("initializing kv workload on left cluster")
	c.Run(ctx,
		option.WithNodes(setup.workloadNode),
		kvWorkload.sourceInitCmd("system", setup.left.nodes))

	const backupDest = "nodelocal://1/revlog"
	t.Status("starting continuous backup with revision stream")
	setup.left.sysSQL.Exec(t, fmt.Sprintf(
		"BACKUP DATABASE %s INTO '%s' WITH REVISION STREAM", dbName, backupDest))

	var revlogJobID int
	testutils.SucceedsWithin(t, func() error {
		return setup.left.db.QueryRow(
			"SELECT job_id FROM [SHOW JOBS] WHERE description LIKE 'REVLOG:%' ORDER BY created DESC LIMIT 1",
		).Scan(&revlogJobID)
	}, 30*time.Second)
	t.L().Printf("revlog sibling job ID = %d", revlogJobID)

	waitForJobRunning(t, setup.left.db, revlogJobID)
	t.L().Printf("revlog job is running")

	var cursorStr string
	setup.left.sysSQL.QueryRow(t,
		"SELECT cluster_logical_timestamp()").Scan(&cursorStr)
	t.L().Printf("recorded cursor = %s", cursorStr)

	setup.right.sysSQL.Exec(t, fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", dbName))
	setup.right.sysSQL.Exec(t, fmt.Sprintf("USE %s", dbName))
	setup.right.sysSQL.Exec(t, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (k INT8 NOT NULL, v BYTES NOT NULL, PRIMARY KEY (k ASC))", tableName))

	// ---------------------------------------------------------------
	// Phase 2: Set aggressive GC TTL and run workload long enough for
	// GC to clear MVCC history at the cursor timestamp.
	// ---------------------------------------------------------------
	t.Status(fmt.Sprintf("setting gc.ttlseconds to %d on source", int(gcTTL.Seconds())))
	setup.left.sysSQL.Exec(t, fmt.Sprintf(
		"ALTER DATABASE %s CONFIGURE ZONE USING gc.ttlseconds = %d", dbName, int(gcTTL.Seconds())))

	t.Status(fmt.Sprintf("running workload for %s to let GC clear MVCC history", gcWait))
	c.Run(ctx, option.WithNodes(setup.workloadNode),
		kvWorkload.sourceRunCmd("system", setup.left.nodes))

	// ---------------------------------------------------------------
	// Phase 3: Attempt LDR WITHOUT revision stream. The cursor is now
	// older than GC TTL so the rangefeed catch-up scan should fail.
	// ---------------------------------------------------------------
	t.Status("creating external connection")
	externalConnCmd := "CREATE EXTERNAL CONNECTION IF NOT EXISTS '%s' AS '%s'"
	setup.right.sysSQL.Exec(t, fmt.Sprintf(
		externalConnCmd, leftExternalConn.Host, setup.left.PgURLForDatabase(dbName)))

	t.Status("starting LDR WITHOUT revision stream (expecting failure)")
	setup.right.sysSQL.Exec(t, fmt.Sprintf("USE %s", dbName))
	var noRevLogJobID int
	setup.right.sysSQL.QueryRow(t, fmt.Sprintf(
		"CREATE LOGICAL REPLICATION STREAM FROM TABLE %s ON $1 INTO TABLE %s WITH CURSOR=$2",
		tableName, tableName),
		leftExternalConn.String(), cursorStr,
	).Scan(&noRevLogJobID)
	t.L().Printf("LDR job (no revlog) ID = %d", noRevLogJobID)

	require.NoError(t, WaitForPaused(
		ctx, setup.right.db, jobspb.JobID(noRevLogJobID), 10*time.Minute))
	t.L().Printf("LDR job without revision stream paused as expected")

	// ---------------------------------------------------------------
	// Phase 4: Set the revision stream URI and start LDR again. The
	// revlog has the historical data so catch-up should succeed.
	// ---------------------------------------------------------------
	t.Status("setting revision stream URI on source")
	setup.left.sysSQL.Exec(t, fmt.Sprintf(
		"SET CLUSTER SETTING physical_replication.producer.revision_stream.uri = '%s'",
		backupDest))

	// Re-create the destination table to clear any partial state from the
	// failed job.
	setup.right.sysSQL.Exec(t, fmt.Sprintf("DROP TABLE IF EXISTS %s.%s", dbName, tableName))
	setup.right.sysSQL.Exec(t, fmt.Sprintf("USE %s", dbName))
	setup.right.sysSQL.Exec(t, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (k INT8 NOT NULL, v BYTES NOT NULL, PRIMARY KEY (k ASC))", tableName))

	t.Status("starting LDR WITH revision stream (expecting success)")
	var revLogJobID int
	setup.right.sysSQL.QueryRow(t, fmt.Sprintf(
		"CREATE LOGICAL REPLICATION STREAM FROM TABLE %s ON $1 INTO TABLE %s WITH CURSOR=$2",
		tableName, tableName),
		leftExternalConn.String(), cursorStr,
	).Scan(&revLogJobID)
	t.L().Printf("LDR job (with revlog) ID = %d", revLogJobID)

	t.Status("waiting for LDR catch-up via revision stream")
	waitForReplicatedTime(t, revLogJobID, setup.right.db, getLogicalDataReplicationJobInfo, 10*time.Minute)

	// ---------------------------------------------------------------
	// Phase 5: Verify correctness via fingerprints.
	// ---------------------------------------------------------------
	var nowStr string
	setup.left.sysSQL.QueryRow(t, "SELECT cluster_logical_timestamp()").Scan(&nowStr)
	now := timeutil.Now()
	t.Status("waiting for replicated time to catch up to now")
	waitForReplicatedTimeToReachTimestamp(
		t, revLogJobID, setup.right.db, getLogicalDataReplicationJobInfo, 5*time.Minute, now)

	t.Status("verifying correctness via fingerprints")
	queryStmt := fmt.Sprintf("SHOW EXPERIMENTAL_FINGERPRINTS FROM TABLE %s.%s", dbName, tableName)

	leftFP := setup.left.sysSQL.QueryStr(t, queryStmt)
	rightFP := setup.right.sysSQL.QueryStr(t, queryStmt)
	require.Equal(t, leftFP, rightFP, "fingerprint mismatch for table %s", tableName)
	t.L().Printf("fingerprints match")
}

func waitForJobRunning(t test.Test, db *gosql.DB, jobID int) {
	testutils.SucceedsWithin(t, func() error {
		var status string
		if err := db.QueryRow(
			"SELECT status FROM crdb_internal.system_jobs WHERE id = $1", jobID,
		).Scan(&status); err != nil {
			return err
		}
		if status != "running" {
			return errors.Newf("job %d status is %s, waiting for running", jobID, status)
		}
		return nil
	}, 2*time.Minute)
}
