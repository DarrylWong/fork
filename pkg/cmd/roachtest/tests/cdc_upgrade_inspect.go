// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package tests

import (
	"context"
	gosql "database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/cockroachdb/cockroach/pkg/ccl/changefeedccl/cdctest"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachprod/grafana"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/cdcutil"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/clusterupgrade"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/testutils/release"
	"github.com/cockroachdb/cockroach/pkg/util/syncutil"
	"github.com/cockroachdb/errors"
)

func registerCDCUpgradeInspect(r registry.Registry) {
	r.Add(registry.TestSpec{
		Name:             "cdc/upgrade-inspect",
		Owner:            registry.OwnerCDC,
		Cluster:          r.MakeClusterSpec(11, spec.CPU(4), spec.WorkloadNode()),
		Timeout:          4 * time.Hour,
		CompatibleClouds: registry.AllClouds,
		Suites:                     registry.ManualOnly,
		RequiresDeprecatedWorkload: true,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			runCDCUpgradeInspect(ctx, t, c)
		},
	})
}

func runCDCUpgradeInspect(ctx context.Context, t test.Test, c cluster.Cluster) {
	crdbNodes := c.Range(1, 9)
	kafkaNode := c.Node(10)
	workloadNode := c.Node(11)

	// Determine versions.
	currentVersion := clusterupgrade.CurrentVersion()
	predecessorVersionStr, err := release.LatestPredecessor(&currentVersion.Version)
	if err != nil {
		t.Fatal(err)
	}
	predecessorVersion := clusterupgrade.MustParseVersion(predecessorVersionStr)
	t.L().Printf("predecessor version: %s, current version: %s", predecessorVersion, currentVersion)

	// Phase 1: Setup on predecessor version.
	t.Status("uploading binaries")
	predecessorBinary, err := clusterupgrade.UploadCockroach(ctx, t, t.L(), c, crdbNodes, predecessorVersion)
	if err != nil {
		t.Fatal(err)
	}
	_, err = clusterupgrade.UploadCockroach(ctx, t, t.L(), c, crdbNodes, currentVersion)
	if err != nil {
		t.Fatal(err)
	}

	// Upload workload binary to workload node.
	workloadBinary, _, err := clusterupgrade.UploadWorkload(ctx, t, t.L(), c, workloadNode, predecessorVersion)
	if err != nil {
		t.Fatal(err)
	}

	// Start cluster on predecessor version.
	t.Status("starting cluster on " + predecessorVersion.String())
	startOpts := option.DefaultStartOpts()
	clusterSettings := install.MakeClusterSettings(
		install.BinaryOption(predecessorBinary),
		install.EnvOption(envVars),
	)
	if err := c.StartE(ctx, t.L(), startOpts, clusterSettings, crdbNodes); err != nil {
		t.Fatal(err)
	}

	db := c.Conn(ctx, t.L(), 1)
	defer db.Close()

	// Prevent auto-finalization.
	t.Status("setting preserve_downgrade_option")
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("SET CLUSTER SETTING cluster.preserve_downgrade_option = '%s'", predecessorVersion.Series()),
	); err != nil {
		t.Fatal(err)
	}

	// Enable child metrics so changefeed metrics are visible.
	if _, err := db.ExecContext(ctx, "SET CLUSTER SETTING server.child_metrics.enabled = true"); err != nil {
		t.Fatal(err)
	}

	// Set up Kafka.
	t.Status("setting up Kafka")
	kafka, tearDownKafka := setupKafka(ctx, t, c, kafkaNode)
	defer tearDownKafka()

	// Stop and restart Kafka to clear any stale data from previous runs.
	kafka.restart(ctx, "kafka")

	for _, topic := range []string{targetTable, "canary"} {
		if err := kafka.createTopic(ctx, topic); err != nil {
			t.Fatal(err)
		}
	}

	// Init bank workload.
	t.Status("initializing bank workload")
	const (
		inspectRanges = 500
		inspectRows   = 100_000
	)
	initCmd := fmt.Sprintf(
		"%s init bank --ranges=%d --rows=%d {pgurl%s}",
		workloadBinary, inspectRanges, inspectRows, crdbNodes,
	)
	if err := c.RunE(ctx, option.WithNodes(workloadNode), initCmd); err != nil {
		t.Fatal(err)
	}

	// Scatter ranges.
	if _, err := db.ExecContext(ctx, "ALTER TABLE bank.bank SCATTER"); err != nil {
		t.Fatal(err)
	}

	// Create a canary table for easy manual verification. Insert rows manually
	// via the canary script and check them in Kafka to verify end-to-end flow.
	if _, err := db.ExecContext(ctx, `CREATE TABLE bank.canary (
		id INT PRIMARY KEY,
		val STRING NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}

	// Create shadow table for fingerprint validation.
	if _, err := db.ExecContext(ctx, "CREATE TABLE bank.fprint (id INT PRIMARY KEY, balance INT, payload STRING)"); err != nil {
		t.Fatal(err)
	}

	// Set up Kafka consumer for validation.
	consumer, err := kafka.newConsumer(ctx, targetTable, nil /* stopper */)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.close()

	// Set up validators.
	tableName := fmt.Sprintf("%s.%s", targetDB, targetTable)
	fprintV, err := cdctest.NewFingerprintValidator(db, tableName, "bank.fprint",
		consumer.partitions, 0)
	if err != nil {
		t.Fatal(err)
	}
	validators := cdctest.Validators{
		cdctest.NewOrderValidator(tableName),
		fprintV,
	}
	validator := cdctest.NewCountValidator(validators)

	// Force span-level checkpointing.
	t.Status("configuring span-level checkpointing")
	for _, stmt := range []string{
		`SET CLUSTER SETTING changefeed.frontier_checkpoint_frequency = '1s'`,
		`SET CLUSTER SETTING changefeed.frontier_highwater_lag_checkpoint_threshold = '1us'`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	// Create changefeed.
	t.Status("creating changefeed")
	options := map[string]string{
		"updated":  "",
		"resolved": fmt.Sprintf("'%s'", resolvedInterval),
	}
	metamorphic := cdcutil.NewMetamorphicSettings(t.L())
	// Disable settings that may not exist on older versions.
	metamorphic.Disable(cdcutil.DistributionStrategy)
	jobID, err := newChangefeedCreator(db, db, t.L(),
		fmt.Sprintf("%s, bank.canary", tableName),
		kafka.sinkURL(ctx), metamorphic).
		With(options).
		Create()
	if err != nil {
		t.Fatal(err)
	}
	t.L().Printf("created changefeed job %d", jobID)

	// Start background workload.
	t.Status("starting background workload")
	workloadCmd := fmt.Sprintf(
		"%s run bank --max-rate=10 --tolerate-errors {pgurl%s}",
		workloadBinary, crdbNodes,
	)
	t.Go(func(ctx context.Context, l *logger.Logger) error {
		return c.RunE(ctx, option.WithNodes(workloadNode), workloadCmd)
	})

	// Start latency verifier.
	latencyStopCh := make(chan struct{})
	verifier := makeLatencyVerifier(
		"changefeed-upgrade", 0, 10*time.Minute, t.L(),
		func(db *gosql.DB, jobID int) (jobInfo, error) {
			return getChangefeedInfo(db, jobID)
		},
		t.Status, true, /* tolerateErrors */
	)
	t.Go(func(ctx context.Context, l *logger.Logger) error {
		return verifier.pollLatencyUntilJobSucceeds(ctx, db, jobID, time.Second, latencyStopCh)
	})

	// Start background Kafka consumer that buffers messages.
	kafkaBuf := &kafkaBuffer{}
	t.Go(func(ctx context.Context, l *logger.Logger) error {
		kafkaBuf.run(ctx, l, consumer)
		return nil
	})

	// Helper to drain and validate buffered kafka messages.
	drainAndValidate := func(nodeForDB int) {
		liveDB := c.Conn(ctx, t.L(), nodeForDB)
		defer liveDB.Close()
		fprintV.DBFunc(func(f func(*gosql.DB) error) error {
			return f(liveDB)
		})

		msgs := kafkaBuf.drain()
		t.L().Printf("draining %d buffered kafka messages through validators", len(msgs))
		for i, m := range msgs {
			if i > 0 && i%1000 == 0 {
				t.L().Printf("validated %d/%d messages", i, len(msgs))
			}
			updated, resolved, err := cdctest.ParseJSONValueTimestamps(m.Value)
			if err != nil {
				t.L().Printf("WARNING: failed to parse timestamps: %s", err)
				continue
			}
			partitionStr := strconv.Itoa(int(m.Partition))
			if len(m.Key) > 0 {
				if err := validator.NoteRow(partitionStr, string(m.Key), string(m.Value), updated, m.Topic); err != nil {
					t.L().Printf("WARNING: validator NoteRow error: %s", err)
				}
			} else {
				if err := validator.NoteResolved(partitionStr, resolved); err != nil {
					t.L().Printf("WARNING: validator NoteResolved error: %s", err)
				}
			}
		}
		if failures := validator.Failures(); len(failures) > 0 {
			t.L().Printf("VALIDATION FAILURES:\n%s", strings.Join(failures, "\n"))
		}
		t.L().Printf("validation stats: %d rows, %d resolved (with rows: %d)",
			validator.NumRows, validator.NumResolved, validator.NumResolvedWithRows)
	}

	// Helper to log job status.
	logJobStatus := func() string {
		info, err := getChangefeedInfo(db, jobID)
		if err != nil {
			msg := fmt.Sprintf("job status: error fetching: %s", err)
			t.L().Printf(msg)
			return msg
		}
		msg := fmt.Sprintf("job status: %s, highwater: %s, error: %s",
			info.GetStatus(), info.GetHighWater(), info.GetError())
		t.L().Printf(msg)
		return msg
	}

	// Marker file wait+inspect helper. phaseMarker is the path to the
	// phase-level marker; pass "" for steps not inside a rolling phase.
	waitAndInspect := func(name, description string, phaseMarker string) {
		if err := c.AddGrafanaAnnotation(ctx, t.L(), grafana.AddAnnotationRequest{
			Text: name,
		}); err != nil {
			t.L().Printf("WARNING: failed to add grafana annotation: %s", err)
		}
		jobStatus := logJobStatus()
		fullDesc := fmt.Sprintf("%s\n\nJob ID: %d\n%s\n\n%s", name, jobID, jobStatus, description)
		waitForMarkerFile(ctx, t, name, fullDesc, phaseMarker)
	}

	inspectQueries := fmt.Sprintf(`Suggested inspection queries:

  -- Job status
  SELECT job_id, status, high_water_timestamp, error, running_status
  FROM [SHOW CHANGEFEED JOB %d];

  -- Checkpoint data in job_info
  SELECT info_key, length(value)
  FROM system.job_info
  WHERE job_id = %d AND info_key LIKE '~changefeed/%%';

  -- Protected timestamps
  SELECT * FROM crdb_internal.kv_protected_ts_records
  WHERE meta_type = 'jobs' AND meta::INT = %d;

  -- Cluster version
  SHOW CLUSTER SETTING version;

  -- Node versions
  SELECT node_id, server_version FROM crdb_internal.gossip_nodes;
`, jobID, jobID, jobID)

	// WAIT: changefeed created on predecessor version.
	waitAndInspect("CHANGEFEED_CREATED",
		fmt.Sprintf("Changefeed running on %s. Inspect initial state.\n\n%s", predecessorVersion, inspectQueries),
		"" /* no phase marker */)

	// Phase 2: Rolling upgrade to current version.
	t.Status("beginning rolling upgrade")
	upgradePhase := writePhaseMarker(t, "ROLLING_UPGRADE",
		fmt.Sprintf("Rolling upgrade from %s to %s", predecessorVersion, currentVersion))
	for _, n := range crdbNodes {
		t.Status(fmt.Sprintf("upgrading node %d to %s", n, currentVersion))
		if err := clusterupgrade.RestartNodesWithNewBinary(
			ctx, t, t.L(), c, c.Node(n), startOpts, currentVersion,
		); err != nil {
			t.Fatal(err)
		}

		waitAndInspect(
			fmt.Sprintf("NODE_%d_UPGRADED", n),
			fmt.Sprintf("Node %d upgraded to %s. Mixed-version state.\n\n%s", n, currentVersion, inspectQueries),
			upgradePhase,
		)
	}
	removePhaseMarker(t, upgradePhase)

	waitAndInspect("UPGRADE_COMPLETE",
		fmt.Sprintf("All nodes on %s. Upgrade not yet finalized.\n\n%s", currentVersion, inspectQueries),
		"" /* no phase marker */)

	// Phase 3: Rolling rollback to predecessor version (before finalization).
	t.Status("beginning rolling rollback")

	rollbackPhase := writePhaseMarker(t, "ROLLING_ROLLBACK",
		fmt.Sprintf("Rolling rollback from %s to %s", currentVersion, predecessorVersion))
	for _, n := range crdbNodes {
		t.Status(fmt.Sprintf("rolling back node %d to %s", n, predecessorVersion))
		if err := clusterupgrade.RestartNodesWithNewBinary(
			ctx, t, t.L(), c, c.Node(n), startOpts, predecessorVersion,
		); err != nil {
			t.Fatal(err)
		}

		waitAndInspect(
			fmt.Sprintf("NODE_%d_ROLLEDBACK", n),
			fmt.Sprintf("Node %d rolled back to %s.\n\n%s", n, predecessorVersion, inspectQueries),
			rollbackPhase,
		)
	}
	removePhaseMarker(t, rollbackPhase)

	waitAndInspect("ROLLBACK_COMPLETE",
		fmt.Sprintf("All nodes back on %s.\n\n%s", predecessorVersion, inspectQueries),
		"" /* no phase marker */)

	// Phase 4: Re-upgrade and finalize.
	t.Status("beginning re-upgrade")
	reupgradePhase := writePhaseMarker(t, "ROLLING_REUPGRADE",
		fmt.Sprintf("Re-upgrade from %s to %s", predecessorVersion, currentVersion))
	for _, n := range crdbNodes {
		t.Status(fmt.Sprintf("re-upgrading node %d to %s", n, currentVersion))
		if err := clusterupgrade.RestartNodesWithNewBinary(
			ctx, t, t.L(), c, c.Node(n), startOpts, currentVersion,
		); err != nil {
			t.Fatal(err)
		}

		waitAndInspect(
			fmt.Sprintf("NODE_%d_REUPGRADED", n),
			fmt.Sprintf("Node %d re-upgraded to %s.\n\n%s", n, currentVersion, inspectQueries),
			reupgradePhase,
		)
	}
	removePhaseMarker(t, reupgradePhase)

	t.Status("finalizing upgrade")
	if _, err := db.ExecContext(ctx, "RESET CLUSTER SETTING cluster.preserve_downgrade_option"); err != nil {
		t.Fatal(err)
	}

	// Wait for finalization.
	if err := clusterupgrade.WaitForClusterUpgrade(
		ctx, t.L(), crdbNodes,
		func(n int) *gosql.DB { return c.Conn(ctx, t.L(), n) },
		clusterupgrade.DefaultUpgradeTimeout,
	); err != nil {
		t.Fatal(err)
	}

	waitAndInspect("UPGRADE_FINALIZED",
		fmt.Sprintf("Upgrade finalized to %s.\n\n%s", currentVersion, inspectQueries),
		"" /* no phase marker */)

	// Phase 5: Final.
	t.Status("test completing")
	close(latencyStopCh)

	// Log latency results.
	t.L().Printf("max seen steady latency: %s", verifier.maxSeenSteadyLatency)
	verifier.maybeLogLatencyHist()

	// Final validation: drain all buffered kafka messages through validators.
	t.Status("running final validation")
	drainAndValidate(1)

	t.L().Printf("final validation stats: %d rows, %d resolved (with rows: %d)",
		validator.NumRows, validator.NumResolved, validator.NumResolvedWithRows)
	if failures := validator.Failures(); len(failures) > 0 {
		t.L().Printf("FINAL VALIDATION FAILURES:\n%s", strings.Join(failures, "\n"))
	}

	waitAndInspect("TEST_COMPLETE",
		fmt.Sprintf("Test complete. Review final state.\n\n%s", inspectQueries),
		"" /* no phase marker */)

	t.L().Printf("cdc/upgrade-inspect completed successfully")
}

// kafkaBuffer accumulates Kafka consumer messages in a thread-safe buffer.
type kafkaBuffer struct {
	mu struct {
		syncutil.Mutex
		buf []*sarama.ConsumerMessage
	}
}

func (kb *kafkaBuffer) run(ctx context.Context, l *logger.Logger, consumer *topicConsumer) {
	for {
		m, err := consumer.next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			l.Printf("kafka consumer error: %s", err)
			return
		}
		if m == nil {
			return
		}
		func() {
			kb.mu.Lock()
			defer kb.mu.Unlock()
			kb.mu.buf = append(kb.mu.buf, m)
		}()
	}
}

func (kb *kafkaBuffer) drain() []*sarama.ConsumerMessage {
	kb.mu.Lock()
	defer kb.mu.Unlock()
	msgs := kb.mu.buf
	kb.mu.buf = nil
	return msgs
}

// writePhaseMarker creates a phase-level marker file. Deleting a phase marker
// unblocks all remaining step-level waits within that phase. Returns the marker
// path so it can be cleaned up at the end of the phase.
func writePhaseMarker(t test.Test, name string, description string) string {
	markerPath := filepath.Join(t.ArtifactsDir(), "PHASE_"+name)
	desc := fmt.Sprintf(
		"Phase: %s\nDelete this file to skip all remaining steps in this phase.\n\n%s",
		name, description,
	)
	if err := os.WriteFile(markerPath, []byte(desc), 0644); err != nil {
		t.L().Printf("WARNING: failed to write phase marker %s: %s", markerPath, err)
	} else {
		t.L().Printf("wrote phase marker: %s", markerPath)
	}
	return markerPath
}

// removePhaseMarker removes the phase marker file at the end of a phase.
func removePhaseMarker(t test.Test, path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.L().Printf("WARNING: failed to remove phase marker %s: %s", path, err)
	}
}

// waitForMarkerFile writes a step-level marker file to the artifacts directory
// and waits until either the step marker or the phase marker is deleted before
// continuing. If phaseMarkerPath is empty, only the step marker is checked.
func waitForMarkerFile(
	ctx context.Context, t test.Test, name string, description string, phaseMarkerPath string,
) {
	stepPath := filepath.Join(t.ArtifactsDir(), "WAIT_"+name)
	hint := "Delete this file to advance one step."
	if phaseMarkerPath != "" {
		hint += fmt.Sprintf(
			"\nOr delete %s to skip all remaining steps in this phase.",
			filepath.Base(phaseMarkerPath),
		)
	}
	fullDesc := fmt.Sprintf("%s\n\n%s", hint, description)
	if err := os.WriteFile(stepPath, []byte(fullDesc), 0644); err != nil {
		t.L().Printf("WARNING: failed to write marker file %s: %s", stepPath, err)
		return
	}
	t.Status("waiting for marker deletion: " + name)
	t.L().Printf("wrote marker file: %s -- delete to continue", stepPath)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
			stepGone := markerDeleted(stepPath)
			phaseGone := phaseMarkerPath != "" && markerDeleted(phaseMarkerPath)
			if stepGone || phaseGone {
				if phaseGone && !stepGone {
					_ = os.Remove(stepPath)
				}
				reason := name
				if phaseGone {
					reason = name + " (phase marker deleted)"
				}
				t.Status("marker deleted, continuing: " + reason)
				t.L().Printf("marker deleted: %s", reason)
				return
			}
		}
	}
}

func markerDeleted(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist)
}
