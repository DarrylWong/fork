// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package logical

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/crosscluster/logical/ldrtestutils"
	"github.com/cockroachdb/cockroach/pkg/crosscluster/replicationtestutils"
	"github.com/cockroachdb/cockroach/pkg/jobs"
	"github.com/cockroachdb/cockroach/pkg/jobs/jobspb"
	"github.com/cockroachdb/cockroach/pkg/kv/kvclient/rangefeed"
	"github.com/cockroachdb/cockroach/pkg/spanconfig"
	"github.com/cockroachdb/cockroach/pkg/testutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/jobutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/skip"
	"github.com/cockroachdb/cockroach/pkg/testutils/sqlutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/testcluster"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
)

// TestRevisionStreamLDRE2E exercises the full path from continuous
// backup (revlog) through LDR catch-up via the revision stream:
//
//  1. Start a test cluster with two databases (src, dst).
//  2. Create a table in src and start a continuous write loop.
//  3. Run BACKUP WITH REVISION STREAM to start the revlog sibling
//     job writing closed ticks to nodelocal storage.
//  4. Wait for the revlog to produce closed ticks while writes
//     continue.
//  5. Point the revision stream cluster setting at the backup URI.
//  6. Record a cursor timestamp, then wait for enough ticks to
//     accumulate so the cursor is well past the handoff threshold
//     (30s). Start LDR with that cursor.
//  7. LDR's producer-side rangefeed reads catch-up data from the
//     revision stream, hands off to a live KV rangefeed, and
//     eventually catches up to the present.
//  8. Assert that events were replayed from the revision stream,
//     the handoff occurred, and src and dst converge.
func TestRevisionStreamLDRE2E(t *testing.T) {
	defer leaktest.AfterTest(t)()
	skip.UnderDeadlock(t)
	defer log.Scope(t).Close(t)

	ctx := context.Background()

	tempDir, cleanupDir := testutils.TempDir(t)
	defer cleanupDir()

	handoffCh := make(chan struct{}, 1)
	var revStreamEvents atomic.Int64
	clusterArgs := base.TestClusterArgs{
		ServerArgs: base.TestServerArgs{
			DefaultTestTenant: base.TestDoesNotWorkWithExternalProcessMode(134857),
			ExternalIODir:     tempDir,
			Knobs: base.TestingKnobs{
				JobsTestingKnobs: jobs.NewTestingKnobsWithShortIntervals(),
				SpanConfig: &spanconfig.TestingKnobs{
					ManagerDisableJobCreation: true,
				},
				RangeFeed: &rangefeed.TestingKnobs{
					OnRevisionStreamHandoff: func(_ hlc.Timestamp) {
						select {
						case handoffCh <- struct{}{}:
						default:
						}
					},
					OnRevisionStreamEvent: func() {
						revStreamEvents.Add(1)
					},
				},
			},
		},
	}

	server := testcluster.StartTestCluster(t, 1, clusterArgs)
	defer server.Stopper().Stop(ctx)
	s := server.Server(0).ApplicationLayer()

	appSQL := sqlutils.MakeSQLRunner(s.SQLConn(t))
	appSQL.Exec(t, "CREATE DATABASE src")
	appSQL.Exec(t, "CREATE DATABASE dst")

	srcSQL := sqlutils.MakeSQLRunner(s.SQLConn(t, serverutils.DBName("src")))
	dstSQL := sqlutils.MakeSQLRunner(s.SQLConn(t, serverutils.DBName("dst")))

	// Operator-level settings must go through the system layer.
	sysSQL := sqlutils.MakeSQLRunner(server.SystemLayer(0).SQLConn(t))
	ldrtestutils.ApplyLowLatencyReplicationSettings(t, sysSQL, appSQL)

	srcSQL.Exec(t, "CREATE TABLE kv (k INT PRIMARY KEY, v STRING)")
	dstSQL.Exec(t, "CREATE TABLE kv (k INT PRIMARY KEY, v STRING)")

	// Start a background write loop that continuously upserts rows.
	var writeSeq atomic.Int64
	stopWrites := make(chan struct{})
	writesDone := make(chan struct{})
	writeConn := s.SQLConn(t, serverutils.DBName("src"))

	go func() {
		defer close(writesDone)
		for {
			select {
			case <-stopWrites:
				return
			default:
			}
			seq := writeSeq.Add(1)
			_, _ = writeConn.ExecContext(ctx,
				"UPSERT INTO kv VALUES ($1, $2)",
				seq%500,
				fmt.Sprintf("val-%d", seq))
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// ---------------------------------------------------------------
	// Phase 1: Start the continuous backup (revlog).
	// ---------------------------------------------------------------
	const backupDest = "nodelocal://1/revlog-ldr-e2e"
	sysSQL.Exec(t, "SET CLUSTER SETTING kv.rangefeed.enabled = true")
	srcSQL.Exec(t, fmt.Sprintf(
		"BACKUP DATABASE src INTO '%s' WITH REVISION STREAM", backupDest))

	revlogJobID := findRevlogSiblingJob(t, appSQL)
	t.Logf("revlog sibling job ID = %d", revlogJobID)
	jobutils.WaitForJobToRun(t, appSQL, revlogJobID)

	// Wait for several closed ticks so there's meaningful data in
	// the revision stream for the catch-up phase to replay.
	resolvedDir := filepath.Join(tempDir, "revlog-ldr-e2e", "log", "resolved")
	require.NoError(t, testutils.SucceedsWithinError(func() error {
		count, err := countPBFiles(resolvedDir)
		if err != nil {
			return err
		}
		if count < 3 {
			return errors.Newf("only %d closed-tick manifest(s) under %s, want >= 3",
				count, resolvedDir)
		}
		t.Logf("found %d closed-tick manifest(s)", count)
		return nil
	}, 120*time.Second))

	// ---------------------------------------------------------------
	// Phase 2: Point the producer at the revision stream, record a
	// cursor, and let more ticks accumulate.
	// ---------------------------------------------------------------
	appSQL.Exec(t, fmt.Sprintf(
		"SET CLUSTER SETTING physical_replication.producer.revision_stream.uri = '%s'",
		backupDest))

	cursor := s.Clock().Now()
	ticksAtCursor, err := countPBFiles(resolvedDir)
	require.NoError(t, err)
	t.Logf("cursor = %s (writes so far: %d, ticks: %d)", cursor, writeSeq.Load(), ticksAtCursor)

	// Wait for enough ticks to accumulate after the cursor so the
	// revision stream has data covering >30s (the handoff threshold).
	// With 10s tick width, 5 additional ticks gives ~50s of coverage.
	const minNewTicks = 5
	require.NoError(t, testutils.SucceedsWithinError(func() error {
		count, err := countPBFiles(resolvedDir)
		if err != nil {
			return err
		}
		if count < ticksAtCursor+minNewTicks {
			return errors.Newf("only %d ticks (%d new since cursor), want >= %d new",
				count, count-ticksAtCursor, minNewTicks)
		}
		t.Logf("found %d ticks (%d new since cursor)", count, count-ticksAtCursor)
		return nil
	}, 120*time.Second))

	// ---------------------------------------------------------------
	// Phase 3: Start LDR with cursor in the past.
	// ---------------------------------------------------------------
	srcURL := replicationtestutils.GetExternalConnectionURI(
		t, s, s, serverutils.DBName("src"))

	var ldrJobID jobspb.JobID
	dstSQL.QueryRow(t,
		"CREATE LOGICAL REPLICATION STREAM FROM TABLE kv ON $1 INTO TABLE kv WITH CURSOR=$2",
		srcURL.String(), cursor.AsOfSystemTime(),
	).Scan(&ldrJobID)
	t.Logf("LDR job ID = %d", ldrJobID)

	// ---------------------------------------------------------------
	// Phase 4: Assert revision stream handoff occurred.
	// ---------------------------------------------------------------
	select {
	case <-handoffCh:
		t.Logf("revision stream handoff detected")
	case <-time.After(2 * time.Minute):
		t.Fatal("timed out waiting for revision stream handoff")
	}

	replayedEvents := revStreamEvents.Load()
	t.Logf("revision stream replayed %d events", replayedEvents)
	require.Greater(t, replayedEvents, int64(0),
		"revision stream handoff fired but no events were replayed")

	// ---------------------------------------------------------------
	// Phase 5: Wait for convergence.
	// ---------------------------------------------------------------
	now := s.Clock().Now()
	ldrtestutils.WaitUntilReplicatedTime(t, now, dstSQL, ldrJobID)

	close(stopWrites)
	<-writesDone

	now = s.Clock().Now()
	ldrtestutils.WaitUntilReplicatedTime(t, now, dstSQL, ldrJobID)

	// Verify that more writes occurred than events replayed from
	// the revision stream, proving the live KV rangefeed delivered
	// events beyond what the revision stream covered.
	finalWrites := writeSeq.Load()
	t.Logf("final writes: %d, revision stream events: %d", finalWrites, replayedEvents)
	require.Greater(t, finalWrites, replayedEvents,
		"all events came from the revision stream; expected the live KV rangefeed to deliver additional events")

	var srcCount, dstCount int
	srcSQL.QueryRow(t, "SELECT count(*) FROM kv").Scan(&srcCount)
	dstSQL.QueryRow(t, "SELECT count(*) FROM kv").Scan(&dstCount)
	t.Logf("final row counts: src=%d dst=%d", srcCount, dstCount)
	require.Equal(t, srcCount, dstCount)

	srcSQL.CheckQueryResults(t,
		"SELECT k, v FROM src.kv ORDER BY k",
		dstSQL.QueryStr(t, "SELECT k, v FROM dst.kv ORDER BY k"))

	// ---------------------------------------------------------------
	// Cleanup.
	// ---------------------------------------------------------------
	appSQL.Exec(t, "CANCEL JOB $1", ldrJobID)
	appSQL.Exec(t, "CANCEL JOB $1", revlogJobID)
	jobutils.WaitForJobToCancel(t, appSQL, revlogJobID)
}

// TestRevisionStreamPartialCoverageE2E exercises the case where the
// revision stream covers only a subset of the tables being replicated.
//
//  1. Create two tables in src (kv1 and kv2) and matching tables in dst.
//  2. Run BACKUP TABLE src.kv1 WITH REVISION STREAM — the revlog only
//     covers kv1.
//  3. Start LDR replicating both kv1 and kv2 with a cursor in the past.
//  4. The revision stream replays catch-up data for kv1; KV catch-up
//     handles kv2 (its spans are not covered by the revlog).
//  5. Assert that revision stream events were replayed, the handoff
//     occurred, and both tables converge.
func TestRevisionStreamPartialCoverageE2E(t *testing.T) {
	defer leaktest.AfterTest(t)()
	skip.UnderDeadlock(t)
	defer log.Scope(t).Close(t)

	ctx := context.Background()

	tempDir, cleanupDir := testutils.TempDir(t)
	defer cleanupDir()

	handoffCh := make(chan struct{}, 1)
	var revStreamEvents atomic.Int64
	clusterArgs := base.TestClusterArgs{
		ServerArgs: base.TestServerArgs{
			DefaultTestTenant: base.TestDoesNotWorkWithExternalProcessMode(134857),
			ExternalIODir:     tempDir,
			Knobs: base.TestingKnobs{
				JobsTestingKnobs: jobs.NewTestingKnobsWithShortIntervals(),
				SpanConfig: &spanconfig.TestingKnobs{
					ManagerDisableJobCreation: true,
				},
				RangeFeed: &rangefeed.TestingKnobs{
					OnRevisionStreamHandoff: func(_ hlc.Timestamp) {
						select {
						case handoffCh <- struct{}{}:
						default:
						}
					},
					OnRevisionStreamEvent: func() {
						revStreamEvents.Add(1)
					},
				},
			},
		},
	}

	server := testcluster.StartTestCluster(t, 1, clusterArgs)
	defer server.Stopper().Stop(ctx)
	s := server.Server(0).ApplicationLayer()

	appSQL := sqlutils.MakeSQLRunner(s.SQLConn(t))
	appSQL.Exec(t, "CREATE DATABASE src")
	appSQL.Exec(t, "CREATE DATABASE dst")

	srcSQL := sqlutils.MakeSQLRunner(s.SQLConn(t, serverutils.DBName("src")))
	dstSQL := sqlutils.MakeSQLRunner(s.SQLConn(t, serverutils.DBName("dst")))

	sysSQL := sqlutils.MakeSQLRunner(server.SystemLayer(0).SQLConn(t))
	ldrtestutils.ApplyLowLatencyReplicationSettings(t, sysSQL, appSQL)

	srcSQL.Exec(t, "CREATE TABLE kv1 (k INT PRIMARY KEY, v STRING)")
	srcSQL.Exec(t, "CREATE TABLE kv2 (k INT PRIMARY KEY, v STRING)")
	dstSQL.Exec(t, "CREATE TABLE kv1 (k INT PRIMARY KEY, v STRING)")
	dstSQL.Exec(t, "CREATE TABLE kv2 (k INT PRIMARY KEY, v STRING)")

	// Write to both tables continuously.
	var writeSeq atomic.Int64
	stopWrites := make(chan struct{})
	writesDone := make(chan struct{})
	writeConn := s.SQLConn(t, serverutils.DBName("src"))

	go func() {
		defer close(writesDone)
		for {
			select {
			case <-stopWrites:
				return
			default:
			}
			seq := writeSeq.Add(1)
			v := fmt.Sprintf("val-%d", seq)
			k := seq % 500
			_, _ = writeConn.ExecContext(ctx, "UPSERT INTO kv1 VALUES ($1, $2)", k, v)
			_, _ = writeConn.ExecContext(ctx, "UPSERT INTO kv2 VALUES ($1, $2)", k, v)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// ---------------------------------------------------------------
	// Phase 1: Back up only kv1 — the revlog covers kv1 but not kv2.
	// ---------------------------------------------------------------
	const backupDest = "nodelocal://1/revlog-partial-e2e"
	sysSQL.Exec(t, "SET CLUSTER SETTING kv.rangefeed.enabled = true")
	srcSQL.Exec(t, fmt.Sprintf(
		"BACKUP TABLE kv1 INTO '%s' WITH REVISION STREAM", backupDest))

	revlogJobID := findRevlogSiblingJob(t, appSQL)
	t.Logf("revlog sibling job ID = %d", revlogJobID)
	jobutils.WaitForJobToRun(t, appSQL, revlogJobID)

	resolvedDir := filepath.Join(tempDir, "revlog-partial-e2e", "log", "resolved")
	require.NoError(t, testutils.SucceedsWithinError(func() error {
		count, err := countPBFiles(resolvedDir)
		if err != nil {
			return err
		}
		if count < 3 {
			return errors.Newf("only %d closed-tick manifest(s), want >= 3", count)
		}
		t.Logf("found %d closed-tick manifest(s)", count)
		return nil
	}, 120*time.Second))

	// ---------------------------------------------------------------
	// Phase 2: Configure revision stream URI, record cursor, wait
	// for ticks to accumulate past the handoff threshold.
	// ---------------------------------------------------------------
	appSQL.Exec(t, fmt.Sprintf(
		"SET CLUSTER SETTING physical_replication.producer.revision_stream.uri = '%s'",
		backupDest))

	cursor := s.Clock().Now()
	ticksAtCursor, err := countPBFiles(resolvedDir)
	require.NoError(t, err)
	t.Logf("cursor = %s (writes so far: %d, ticks: %d)", cursor, writeSeq.Load(), ticksAtCursor)

	const minNewTicks = 5
	require.NoError(t, testutils.SucceedsWithinError(func() error {
		count, err := countPBFiles(resolvedDir)
		if err != nil {
			return err
		}
		if count < ticksAtCursor+minNewTicks {
			return errors.Newf("only %d ticks (%d new since cursor), want >= %d new",
				count, count-ticksAtCursor, minNewTicks)
		}
		t.Logf("found %d ticks (%d new since cursor)", count, count-ticksAtCursor)
		return nil
	}, 120*time.Second))

	// ---------------------------------------------------------------
	// Phase 3: Start LDR replicating BOTH tables with cursor in
	// the past.
	// ---------------------------------------------------------------
	srcURL := replicationtestutils.GetExternalConnectionURI(
		t, s, s, serverutils.DBName("src"))

	var ldrJobID jobspb.JobID
	dstSQL.QueryRow(t,
		"CREATE LOGICAL REPLICATION STREAM FROM TABLES (kv1, kv2) ON $1 INTO TABLES (kv1, kv2) WITH CURSOR=$2",
		srcURL.String(), cursor.AsOfSystemTime(),
	).Scan(&ldrJobID)
	t.Logf("LDR job ID = %d", ldrJobID)

	// ---------------------------------------------------------------
	// Phase 4: Assert revision stream handoff and event replay.
	// ---------------------------------------------------------------
	select {
	case <-handoffCh:
		t.Logf("revision stream handoff detected")
	case <-time.After(2 * time.Minute):
		t.Fatal("timed out waiting for revision stream handoff")
	}

	replayedEvents := revStreamEvents.Load()
	t.Logf("revision stream replayed %d events", replayedEvents)
	require.Greater(t, replayedEvents, int64(0),
		"revision stream handoff fired but no events were replayed")

	// ---------------------------------------------------------------
	// Phase 5: Wait for convergence of both tables.
	// ---------------------------------------------------------------
	now := s.Clock().Now()
	ldrtestutils.WaitUntilReplicatedTime(t, now, dstSQL, ldrJobID)

	close(stopWrites)
	<-writesDone

	now = s.Clock().Now()
	ldrtestutils.WaitUntilReplicatedTime(t, now, dstSQL, ldrJobID)

	finalWrites := writeSeq.Load()
	t.Logf("final writes: %d, revision stream events: %d", finalWrites, replayedEvents)
	require.Greater(t, finalWrites, replayedEvents,
		"all events came from the revision stream; expected the live KV rangefeed to deliver additional events")

	// Verify both tables converged.
	for _, table := range []string{"kv1", "kv2"} {
		var srcCount, dstCount int
		srcSQL.QueryRow(t, fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&srcCount)
		dstSQL.QueryRow(t, fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&dstCount)
		t.Logf("table %s: src=%d dst=%d", table, srcCount, dstCount)
		require.Equal(t, srcCount, dstCount, "row count mismatch for table %s", table)

		srcSQL.CheckQueryResults(t,
			fmt.Sprintf("SELECT k, v FROM src.%s ORDER BY k", table),
			dstSQL.QueryStr(t, fmt.Sprintf("SELECT k, v FROM dst.%s ORDER BY k", table)))
	}

	// ---------------------------------------------------------------
	// Cleanup.
	// ---------------------------------------------------------------
	appSQL.Exec(t, "CANCEL JOB $1", ldrJobID)
	appSQL.Exec(t, "CANCEL JOB $1", revlogJobID)
	jobutils.WaitForJobToCancel(t, appSQL, revlogJobID)
}

// TestRevisionStreamFallbackE2E exercises the fallback path where the
// revlog job is canceled before LDR starts, causing the revision
// stream to run out of closed ticks and hand off to the live KV
// rangefeed for the remainder of catch-up.
//
//  1. Start a revlog via BACKUP WITH REVISION STREAM and wait for
//     ticks to accumulate.
//  2. Record a cursor, wait for more ticks, then cancel the revlog
//     job so no new ticks will be produced.
//  3. Start LDR with the cursor. The revision stream replays the
//     existing closed ticks, runs out, and hands off to KV.
//  4. Assert that some events were replayed, the handoff cursor is
//     well behind now (proving we ran out of ticks rather than
//     catching up), and src/dst converge.
func TestRevisionStreamFallbackE2E(t *testing.T) {
	defer leaktest.AfterTest(t)()
	skip.UnderDeadlock(t)
	defer log.Scope(t).Close(t)

	ctx := context.Background()

	tempDir, cleanupDir := testutils.TempDir(t)
	defer cleanupDir()

	handoffCh := make(chan hlc.Timestamp, 1)
	var revStreamEvents atomic.Int64

	clusterArgs := base.TestClusterArgs{
		ServerArgs: base.TestServerArgs{
			DefaultTestTenant: base.TestDoesNotWorkWithExternalProcessMode(134857),
			ExternalIODir:     tempDir,
			Knobs: base.TestingKnobs{
				JobsTestingKnobs: jobs.NewTestingKnobsWithShortIntervals(),
				SpanConfig: &spanconfig.TestingKnobs{
					ManagerDisableJobCreation: true,
				},
				RangeFeed: &rangefeed.TestingKnobs{
					OnRevisionStreamHandoff: func(cursor hlc.Timestamp) {
						select {
						case handoffCh <- cursor:
						default:
						}
					},
					OnRevisionStreamEvent: func() {
						revStreamEvents.Add(1)
					},
					// Use a very short threshold so the revision stream
					// won't hand off via "caught up" — it must run out
					// of ticks and hit the unclosed-tick path.
					RevisionStreamHandoffThreshold: time.Millisecond,
				},
			},
		},
	}

	server := testcluster.StartTestCluster(t, 1, clusterArgs)
	defer server.Stopper().Stop(ctx)
	s := server.Server(0).ApplicationLayer()

	appSQL := sqlutils.MakeSQLRunner(s.SQLConn(t))
	appSQL.Exec(t, "CREATE DATABASE src")
	appSQL.Exec(t, "CREATE DATABASE dst")

	srcSQL := sqlutils.MakeSQLRunner(s.SQLConn(t, serverutils.DBName("src")))
	dstSQL := sqlutils.MakeSQLRunner(s.SQLConn(t, serverutils.DBName("dst")))

	sysSQL := sqlutils.MakeSQLRunner(server.SystemLayer(0).SQLConn(t))
	ldrtestutils.ApplyLowLatencyReplicationSettings(t, sysSQL, appSQL)

	srcSQL.Exec(t, "CREATE TABLE kv (k INT PRIMARY KEY, v STRING)")
	dstSQL.Exec(t, "CREATE TABLE kv (k INT PRIMARY KEY, v STRING)")

	var writeSeq atomic.Int64
	stopWrites := make(chan struct{})
	writesDone := make(chan struct{})
	writeConn := s.SQLConn(t, serverutils.DBName("src"))

	go func() {
		defer close(writesDone)
		for {
			select {
			case <-stopWrites:
				return
			default:
			}
			seq := writeSeq.Add(1)
			_, _ = writeConn.ExecContext(ctx,
				"UPSERT INTO kv VALUES ($1, $2)",
				seq%500,
				fmt.Sprintf("val-%d", seq))
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// ---------------------------------------------------------------
	// Phase 1: Start the continuous backup (revlog).
	// ---------------------------------------------------------------
	const backupDest = "nodelocal://1/revlog-fallback-e2e"
	sysSQL.Exec(t, "SET CLUSTER SETTING kv.rangefeed.enabled = true")
	srcSQL.Exec(t, fmt.Sprintf(
		"BACKUP DATABASE src INTO '%s' WITH REVISION STREAM", backupDest))

	revlogJobID := findRevlogSiblingJob(t, appSQL)
	t.Logf("revlog sibling job ID = %d", revlogJobID)
	jobutils.WaitForJobToRun(t, appSQL, revlogJobID)

	resolvedDir := filepath.Join(tempDir, "revlog-fallback-e2e", "log", "resolved")
	require.NoError(t, testutils.SucceedsWithinError(func() error {
		count, err := countPBFiles(resolvedDir)
		if err != nil {
			return err
		}
		if count < 3 {
			return errors.Newf("only %d closed-tick manifest(s), want >= 3", count)
		}
		t.Logf("found %d closed-tick manifest(s)", count)
		return nil
	}, 120*time.Second))

	// Record cursor now — ticks will continue accumulating after
	// this point, building the data the revision stream will replay.
	cursor := s.Clock().Now()
	t.Logf("cursor = %s (writes so far: %d)", cursor, writeSeq.Load())

	// ---------------------------------------------------------------
	// Phase 2: Configure revision stream, accumulate a few more
	// ticks, then kill the revlog so no new ticks are produced.
	// The cursor was recorded before the initial tick wait, so it
	// is well behind the last closed tick.
	// ---------------------------------------------------------------
	appSQL.Exec(t, fmt.Sprintf(
		"SET CLUSTER SETTING physical_replication.producer.revision_stream.uri = '%s'",
		backupDest))

	ticksAtCursor, err := countPBFiles(resolvedDir)
	require.NoError(t, err)

	const minNewTicks = 3
	require.NoError(t, testutils.SucceedsWithinError(func() error {
		count, err := countPBFiles(resolvedDir)
		if err != nil {
			return err
		}
		if count < ticksAtCursor+minNewTicks {
			return errors.Newf("only %d ticks (%d new since cursor), want >= %d new",
				count, count-ticksAtCursor, minNewTicks)
		}
		t.Logf("found %d ticks (%d new since cursor)", count, count-ticksAtCursor)
		return nil
	}, 120*time.Second))

	// Cancel the revlog job so the revision stream has a finite
	// number of ticks to replay. The existing closed ticks on disk
	// remain, but no new ones will be produced.
	t.Logf("canceling revlog job before starting LDR")
	appSQL.Exec(t, "CANCEL JOB $1", revlogJobID)
	jobutils.WaitForJobToCancel(t, appSQL, revlogJobID)

	// ---------------------------------------------------------------
	// Phase 3: Start LDR. The revision stream will replay whatever
	// ticks exist, then hand off to KV when it runs out.
	// ---------------------------------------------------------------
	srcURL := replicationtestutils.GetExternalConnectionURI(
		t, s, s, serverutils.DBName("src"))

	var ldrJobID jobspb.JobID
	dstSQL.QueryRow(t,
		"CREATE LOGICAL REPLICATION STREAM FROM TABLE kv ON $1 INTO TABLE kv WITH CURSOR=$2",
		srcURL.String(), cursor.AsOfSystemTime(),
	).Scan(&ldrJobID)
	t.Logf("LDR job ID = %d", ldrJobID)

	// ---------------------------------------------------------------
	// Phase 4: Assert the revision stream ran out of ticks.
	// ---------------------------------------------------------------
	var handoffCursor hlc.Timestamp
	select {
	case handoffCursor = <-handoffCh:
		t.Logf("revision stream handoff at cursor %s", handoffCursor)
	case <-time.After(2 * time.Minute):
		t.Fatal("timed out waiting for revision stream handoff")
	}

	// With the handoff threshold set to 1ms, the only way to hand
	// off is via the unclosed-tick path (running out of ticks). The
	// handoff cursor should be well behind the present — at least
	// a few seconds, since the revlog was canceled before LDR started.
	lag := time.Since(handoffCursor.GoTime())
	t.Logf("handoff cursor lag: %s", lag)
	require.Greater(t, lag, time.Second,
		"handoff cursor is too close to now; expected handoff due to missing ticks, not catch-up")

	replayedEvents := revStreamEvents.Load()
	t.Logf("revision stream replayed %d events total", replayedEvents)
	require.Greater(t, replayedEvents, int64(0))

	// ---------------------------------------------------------------
	// Phase 5: Wait for convergence despite revlog being canceled.
	// ---------------------------------------------------------------
	now := s.Clock().Now()
	ldrtestutils.WaitUntilReplicatedTime(t, now, dstSQL, ldrJobID)

	close(stopWrites)
	<-writesDone

	now = s.Clock().Now()
	ldrtestutils.WaitUntilReplicatedTime(t, now, dstSQL, ldrJobID)

	finalWrites := writeSeq.Load()
	t.Logf("final writes: %d, revision stream events: %d", finalWrites, replayedEvents)
	require.Greater(t, finalWrites, replayedEvents,
		"all events came from the revision stream; expected KV rangefeed to deliver the rest")

	var srcCount, dstCount int
	srcSQL.QueryRow(t, "SELECT count(*) FROM kv").Scan(&srcCount)
	dstSQL.QueryRow(t, "SELECT count(*) FROM kv").Scan(&dstCount)
	t.Logf("final row counts: src=%d dst=%d", srcCount, dstCount)
	require.Equal(t, srcCount, dstCount)

	srcSQL.CheckQueryResults(t,
		"SELECT k, v FROM src.kv ORDER BY k",
		dstSQL.QueryStr(t, "SELECT k, v FROM dst.kv ORDER BY k"))

	// ---------------------------------------------------------------
	// Cleanup.
	// ---------------------------------------------------------------
	appSQL.Exec(t, "CANCEL JOB $1", ldrJobID)
}

// findRevlogSiblingJob queries system.jobs for the revlog sibling
// job created by BACKUP WITH REVISION STREAM. Retries briefly in
// case the job record has not been visible yet.
func findRevlogSiblingJob(t *testing.T, db *sqlutils.SQLRunner) jobspb.JobID {
	t.Helper()
	var jobID jobspb.JobID
	testutils.SucceedsSoon(t, func() error {
		row := db.DB.QueryRowContext(context.Background(),
			"SELECT id FROM system.jobs WHERE description LIKE 'REVLOG:%' ORDER BY created DESC LIMIT 1")
		if err := row.Scan(&jobID); err != nil {
			return errors.Wrap(err, "revlog sibling job not found yet")
		}
		return nil
	})
	return jobID
}

// countPBFiles counts .pb files under the given directory tree.
func countPBFiles(dir string) (int, error) {
	count := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".pb") {
			count++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	return count, nil
}
