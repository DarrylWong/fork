// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/build"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	_ "github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular/operations" // Register modular operations
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/task"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/roachprod"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/version"
	"github.com/spf13/cobra"
)

// runModularDemo creates a temporary local cluster, runs the scheduler, and cleans up.
func runModularDemo(cmd *cobra.Command) error {
	ctx := context.Background()

	// Get demo node count
	nodes, _ := cmd.Flags().GetInt("nodes")
	if nodes < 1 || nodes > 9 {
		return errors.New("--nodes must be between 1 and 9 for local clusters")
	}

	// Create artifacts directory for demo
	artifactsDir := fmt.Sprintf("artifacts/modular-demo/%d", timeutil.Now().Unix())
	if err := os.MkdirAll(artifactsDir, 0755); err != nil {
		return errors.Wrap(err, "failed to create artifacts directory")
	}

	// Create stdout logger for setup
	logPath := filepath.Join(artifactsDir, "demo.log")
	l, err := logger.RootLogger(logPath, logger.TeeToStdout)
	if err != nil {
		return errors.Wrap(err, "failed to create logger")
	}

	l.Printf("Creating local cluster with %d nodes...", nodes)
	clusterFactory := newClusterFactory(
		os.Getenv("USER"),
		"", // no cluster ID
		artifactsDir,
		getClusterRegistry(),
		1, // concurrent creations
	)

	clusterCfg := clusterConfig{
		spec:         spec.MakeClusterSpec(nodes, spec.WorkloadNode()),
		localCluster: true, // This ensures proper local cluster setup
	}

	cluster, _, err := clusterFactory.newCluster(
		ctx,
		clusterCfg,
		func(s string) { l.Printf("Cluster creation: %s", s) },
		logger.TeeToStdout,
	)
	if err != nil {
		return errors.Wrap(err, "failed to create local cluster")
	}

	// Ensure cleanup happens
	defer func() {
		l.Printf("Destroying local cluster...")
		cluster.Destroy(ctx, dontCloseLogger, l)
	}()

	// Create a minimal test for the demo cluster
	// We need this before calling Start() because Start() accesses c.t
	demoTest, err := newModularSchedulerTest(cluster.Name(), l, artifactsDir, ctx, cluster)
	if err != nil {
		return errors.Wrap(err, "failed to create demo test")
	}
	cluster.setTest(demoTest)

	// Stage the cockroach binary found in the directory (similar to roachtest)
	if err := cluster.Stage(ctx, l, "cockroach", "", ".", cluster.All()); err != nil {
		return errors.Wrap(err, "failed to stage cockroach binary")
	}

	// Start the cluster
	cluster.Start(ctx, l, option.DefaultStartOpts(), install.MakeClusterSettings(), cluster.CRDBNodes())

	l.Printf("Demo cluster ready, running modular scheduler (3 iterations)...")

	// Parse scheduler flags
	seed, _ := cmd.Flags().GetInt64("seed")
	baseDAGs, _ := cmd.Flags().GetStringSlice("base-dags")
	excludeOps, _ := cmd.Flags().GetStringSlice("exclude-operations")
	opsPerStageStr, _ := cmd.Flags().GetString("operations-per-stage")

	// Parse operations-per-stage range
	opsPerStage := [2]int{2, 5} // default
	parts := strings.Split(opsPerStageStr, "-")
	if len(parts) == 2 {
		min, err1 := strconv.Atoi(parts[0])
		max, err2 := strconv.Atoi(parts[1])
		if err1 == nil && err2 == nil {
			opsPerStage = [2]int{min, max}
		}
	}

	// Create scheduler config
	config := SchedulerConfig{
		BaseDAGNames:       baseDAGs,
		ExcludeOperations:  excludeOps,
		OperationsPerStage: opsPerStage,
		Seed:               seed,
		TestName:           fmt.Sprintf("modular-demo-%s", cluster.Name()),
	}

	// Create scheduler
	scheduler, err := NewScheduler(config, l, cluster)
	if err != nil {
		return errors.Wrap(err, "failed to create scheduler")
	}

	// Print summary
	l.Printf("%s", scheduler.GetSummary())

	// Run 3 iterations of the scheduler
	for i := 1; i <= 3; i++ {
		l.Printf("\n========== Starting iteration %d/3 ==========", i)

		// Defer artifact collection to ensure it happens even if the test fails
		func() {
			defer func() {
				l.Printf("Collecting artifacts for iteration %d...", i)
				collectModularArtifacts(ctx, l, cluster)
			}()

			// Run one iteration
			if err := scheduler.RunIteration(ctx, demoTest); err != nil {
				l.Errorf("scheduler iteration %d failed: %v", i, err)
			}
		}()

		l.Printf("========== Completed iteration %d/3 ==========\n", i)
	}

	l.Printf("All 3 scheduler iterations completed successfully")
	return nil
}

// runModularScheduler runs the modular scheduler on an existing cluster.
func runModularScheduler(cmd *cobra.Command, clusterName string) error {
	ctx := context.Background()

	artifactsDir := fmt.Sprintf("artifacts/modular/%s/%d", clusterName, timeutil.Now().Unix())
	if err := os.MkdirAll(artifactsDir, 0755); err != nil {
		return errors.Wrap(err, "failed to create artifacts directory")
	}

	logPath := filepath.Join(artifactsDir, "test.log")
	l, err := logger.RootLogger(logPath, logger.TeeToStdout)
	if err != nil {
		return errors.Wrap(err, "failed to create logger")
	}
	l.Printf("Artifacts will be stored in: %s", artifactsDir)

	// Load clusters from roachprod
	if err := roachprod.LoadClusters(); err != nil {
		return errors.Wrap(err, "failed to load clusters")
	}

	// Get the cluster from roachprod
	syncedCluster, err := roachprod.GetClusterFromCache(l, clusterName)
	if err != nil {
		return errors.Wrapf(err, "cluster %q not found", clusterName)
	}

	// Create a cluster spec based on the existing cluster
	// For local clusters, designate the last node as a workload node
	clusterSpec := spec.MakeClusterSpec(len(syncedCluster.VMs), spec.WorkloadNode())

	// Attach to the existing cluster using roachtest's infrastructure
	cluster, err := attachToExistingCluster(
		ctx,
		clusterName,
		l,
		clusterSpec,
		attachOpt{skipWipe: true},
		getClusterRegistry(),
	)
	if err != nil {
		return errors.Wrap(err, "failed to attach to cluster")
	}

	// Create the roachprod user so we can use the same auth as regular roachtests.
	// Regular roachtests call c.Start() which creates this user automatically.
	// Since we're attaching to an existing cluster, we need to create it ourselves.
	if err := createRoachprodUser(ctx, l, syncedCluster); err != nil {
		return errors.Wrap(err, "failed to create roachprod user")
	}

	// Get actually running nodes from the cluster
	runningNodes := make([]int, 0)
	for i := 1; i <= cluster.Spec().NodeCount; i++ {
		// Check if node is running by trying to connect
		status, err := cluster.RunWithDetailsSingleNode(ctx, l, option.WithNodes(cluster.Node(i)), "echo running")
		if err == nil && status.Err == nil {
			runningNodes = append(runningNodes, i)
		}
	}

	if len(runningNodes) == 0 {
		return errors.New("no running nodes found in cluster")
	}

	l.Printf("Found %d running nodes: %v", len(runningNodes), runningNodes)

	// Create testImpl for the scheduler using our constructor
	t, err := newModularSchedulerTest(clusterName, l, artifactsDir, ctx, cluster)
	if err != nil {
		return errors.Wrap(err, "failed to create test")
	}

	// Set the test on the cluster so it can run commands properly
	cluster.setTest(t)

	// Check if the cluster is running in secure mode and fetch certs if needed
	// We do this by checking if certs exist on node 1
	checkCmd := "test -d certs && echo 'secure' || echo 'insecure'"
	result, err := cluster.RunWithDetailsSingleNode(ctx, l, option.WithNodes(cluster.Node(1)), checkCmd)
	if err != nil {
		l.Printf("Warning: failed to check if cluster is secure: %v", err)
	} else if strings.TrimSpace(result.Stdout) == "secure" {
		l.Printf("Cluster is running in secure mode, fetching certificates...")
		if err := cluster.RefetchCertsFromNode(ctx, 1); err != nil {
			return errors.Wrap(err, "failed to fetch certificates from secure cluster")
		}
		l.Printf("Certificates fetched successfully")
	} else {
		l.Printf("Cluster is running in insecure mode")
	}

	// Parse flags
	seed, _ := cmd.Flags().GetInt64("seed")
	baseDAGs, _ := cmd.Flags().GetStringSlice("base-dags")
	excludeOps, _ := cmd.Flags().GetStringSlice("exclude-operations")
	opsPerStageStr, _ := cmd.Flags().GetString("operations-per-stage")

	// Parse operations-per-stage range
	opsPerStage := [2]int{2, 5} // default
	parts := strings.Split(opsPerStageStr, "-")
	if len(parts) == 2 {
		min, err1 := strconv.Atoi(parts[0])
		max, err2 := strconv.Atoi(parts[1])
		if err1 == nil && err2 == nil {
			opsPerStage = [2]int{min, max}
		}
	}

	// Create scheduler config
	config := SchedulerConfig{
		BaseDAGNames:       baseDAGs,
		ExcludeOperations:  excludeOps,
		OperationsPerStage: opsPerStage,
		Seed:               seed,
		TestName:           fmt.Sprintf("modular-%s", clusterName),
	}

	// Create scheduler
	scheduler, err := NewScheduler(config, l, cluster)
	if err != nil {
		return errors.Wrap(err, "failed to create scheduler")
	}

	// Print summary
	l.Printf("%s", scheduler.GetSummary())

	// Defer artifact collection to ensure it happens even if the test fails
	defer func() {
		l.Printf("Collecting artifacts...")
		collectModularArtifacts(ctx, l, cluster)
	}()

	// Run one iteration
	if err := scheduler.RunIteration(ctx, t); err != nil {
		return errors.Wrap(err, "scheduler iteration failed")
	}

	l.Printf("Modular scheduler completed successfully")
	return nil
}

// newModularSchedulerTest creates a testImpl for modular scheduler tests.
// This is our own constructor that sets up the minimal infrastructure needed
// for scheduler tests to run. In the future, we may create a custom test spec
// type specifically for scheduler tests instead of using registry.TestSpec.
func newModularSchedulerTest(
	clusterName string,
	l *logger.Logger,
	artifactsDir string,
	ctx context.Context,
	c cluster.Cluster,
) (*testImpl, error) {
	binaryVersion, err := version.Parse(build.BinaryVersion())
	if err != nil {
		return nil, err
	}
	t := &testImpl{
		// TODO: might have to make this an interface?
		spec: &registry.TestSpec{
			Name:  fmt.Sprintf("modular-%s", clusterName),
			Owner: registry.OwnerTestEng,
		},
		cockroach:          "cockroach",
		deprecatedWorkload: "workload",
		buildVersion:       &binaryVersion,
		artifactsDir:       artifactsDir,
		artifactsSpec:      "",
		debug:              false,
	}
	t.ReplaceL(l)

	// Initialize task manager for background goroutines
	t.taskManager = task.NewManager(ctx, l)

	monitor := newTestMonitor(ctx, t, c.(*clusterImpl))
	t.monitor = monitor.monitor
	monitor.start()

	return t, nil
}

// getClusterRegistry returns the global cluster registry
func getClusterRegistry() *clusterRegistry {
	// This should return the singleton cluster registry instance
	// For now, create a new one (this may need adjustment based on roachtest's architecture)
	return newClusterRegistry()
}

// createRoachprodUser creates the roachprod user on an existing cluster.
// This is the same user that c.Start() creates automatically for new clusters.
// If the user already exists, this function just logs a warning and continues.
func createRoachprodUser(ctx context.Context, l *logger.Logger, c *install.SyncedCluster) error {
	if !c.Secure {
		return nil // Only needed for secure clusters
	}

	const username = install.DefaultUser     // "roachprod"
	const password = install.DefaultPassword // "cockroachdb"

	// Try to create user - IF NOT EXISTS makes this idempotent
	// Note: On existing clusters where the user already exists, this may fail
	// with certain auth configurations, which is fine.
	createStmt := fmt.Sprintf("CREATE USER IF NOT EXISTS %s WITH LOGIN PASSWORD '%s'", username, password)
	results, err := c.ExecSQL(
		ctx, l, c.Nodes[:1], install.SystemInterfaceName, 0, /* sqlInstance */
		install.AuthRootCert, "", /* database */
		[]string{"-e", createStmt})

	if err != nil || results[0].Err != nil {
		// If user creation fails, assume the user already exists and continue
		l.Printf("Note: Could not create user %s (may already exist): %v", username, errors.CombineErrors(err, results[0].Err))
		l.Printf("Continuing with assumption that user %s exists...", username)
		return nil
	}

	// Grant admin privileges - this may fail if already granted, which is fine
	grantStmt := fmt.Sprintf("GRANT ADMIN TO %s WITH ADMIN OPTION", username)
	results, err = c.ExecSQL(
		ctx, l, c.Nodes[:1], install.SystemInterfaceName, 0, /* sqlInstance */
		install.AuthRootCert, "", /* database */
		[]string{"-e", grantStmt})

	if err != nil || results[0].Err != nil {
		l.Printf("Note: Could not grant admin to %s (may already have it): %v", username, errors.CombineErrors(err, results[0].Err))
	} else {
		l.Printf("Successfully created user %s with admin privileges", username)
	}

	return nil
}

// collectModularArtifacts collects logs and other artifacts from the cluster.
// This mirrors the artifact collection done by regular roachtests.
func collectModularArtifacts(ctx context.Context, l *logger.Logger, c cluster.Cluster) {
	l.Printf("Fetching cluster logs...")
	if err := c.FetchLogs(ctx, l); err != nil {
		l.Printf("Warning: failed to fetch logs: %s", err)
	}

	l.Printf("Artifact collection completed")
}
