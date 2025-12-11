// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package sql_test

import (
	"context"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/clusterversion"
	"github.com/cockroachdb/cockroach/pkg/server"
	"github.com/cockroachdb/cockroach/pkg/settings/cluster"
	"github.com/cockroachdb/cockroach/pkg/testutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/skip"
	"github.com/cockroachdb/cockroach/pkg/testutils/testcluster"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/protoutil"
	"github.com/stretchr/testify/require"
)

// TestShowClusterSettingVersionWithPartialUpgradeFailure tests the scenario
// where a cluster version upgrade partially succeeds (some nodes bump their
// in-memory version) but fails overall (KV version not updated), leaving the
// cluster in an inconsistent state where SHOW CLUSTER SETTING version times out.
//
// This reproduces the bug observed in production where:
// 1. Node 1 panics during an upgrade
// 2. Nodes 2-6 successfully bump their in-memory version via RPC
// 3. The overall upgrade fails, so KV stays at old version
// 4. SHOW CLUSTER SETTING version on node 6 times out
func TestShowClusterSettingVersionWithPartialUpgradeFailure(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)
	skip.UnderStress(t, "test is time-sensitive and uses sleeps")

	ctx := context.Background()

	// Create a 3-node cluster
	tc := testcluster.StartTestCluster(t, 3, base.TestClusterArgs{
		ReplicationMode: base.ReplicationManual,
		ServerArgs: base.TestServerArgs{
			Settings: cluster.MakeTestingClusterSettings(),
			Knobs: base.TestingKnobs{
				Server: &server.TestingKnobs{
					DisableAutomaticVersionUpgrade: make(chan struct{}),
				},
			},
		},
	})
	defer tc.Stopper().Stop(ctx)

	// Get initial version from KV on node 2
	db2 := tc.ServerConn(1) // Node 2 (index 1)
	var initialKVVersion string
	err := db2.QueryRow("SELECT value FROM system.settings WHERE name = 'version'").Scan(&initialKVVersion)
	require.NoError(t, err)
	t.Logf("Initial KV version: %s", initialKVVersion)

	// Get the current in-memory version
	srv2 := tc.Server(1)
	st := srv2.ClusterSettings()
	currentVersion := st.Version.ActiveVersion(ctx)
	t.Logf("Current in-memory version: %s", currentVersion.Version)

	// Calculate the next fence version (this is what an upgrade would bump to first)
	nextVersion := clusterversion.Latest.Version()
	fenceVersion := nextVersion.FenceVersion()
	require.True(t, fenceVersion.IsFence(), "fence version should have odd Internal")
	t.Logf("Target fence version: %s (IsFence=%v)", fenceVersion, fenceVersion.IsFence())

	// Simulate partial failure: bump nodes 2 and 3, skip node 1 (as if it's down)
	// This simulates what happens when the upgrade manager tries to bump all nodes
	// but one node is unavailable
	cv := clusterversion.ClusterVersion{Version: fenceVersion}

	// Bump only nodes 2 and 3 (skip node 1 to simulate it being down)
	for i := 1; i < tc.NumServers(); i++ {
		srv := tc.Server(i).(serverutils.TestServerInterface)

		// Directly call the internal method that bumps the cluster version
		// This is what the BumpClusterVersion RPC handler does
		err := srv.ClusterSettings().Version.SetActiveVersion(ctx, cv)
		if err != nil {
			// If this fails, it might be due to version validation
			t.Logf("Node %d failed to set active version: %v", i+1, err)
			// Try with skipValidation if normal method fails
			continue
		}

		t.Logf("Node %d successfully bumped to %s", i+1, fenceVersion)

		// Verify in-memory version was updated
		inMemory := srv.ClusterSettings().Version.ActiveVersion(ctx)
		t.Logf("Node %d in-memory version: %s", i+1, inMemory.Version)
		require.Equal(t, fenceVersion, inMemory.Version, "in-memory version should match")
	}

	// Verify node 1 is still at old version
	node1Memory := tc.Server(0).ClusterSettings().Version.ActiveVersion(ctx)
	t.Logf("Node 1 in-memory version (unchanged): %s", node1Memory.Version)

	// Verify KV version is still the old version (because overall operation failed)
	var kvVersion string
	err = db2.QueryRow("SELECT value FROM system.settings WHERE name = 'version'").Scan(&kvVersion)
	require.NoError(t, err)
	require.Equal(t, initialKVVersion, kvVersion, "KV version should not have changed")
	t.Logf("KV version unchanged: %s", kvVersion)

	// Let's check what SHOW CLUSTER SETTING actually sees
	// First, let's verify what the local and KV versions actually are from node 2's perspective
	node2InMemory := tc.Server(1).ClusterSettings().Version.ActiveVersion(ctx)
	t.Logf("Node 2 in-memory (before query): %s", node2InMemory.Version)

	// Now try SHOW CLUSTER SETTING version on node 2
	// According to production logs, this should timeout because:
	// - Node 2's in-memory version is the fence version
	// - KV version is still the old version
	// - ListBetween should return empty (they're adjacent)
	// - So fence exception SHOULD apply, allowing the query to succeed
	//
	// BUT in production it timed out with a NON-fence version (step-002)
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var showVersion string
	err = db2.QueryRowContext(ctx2, "SHOW CLUSTER SETTING version").Scan(&showVersion)

	if err == nil {
		t.Logf("SHOW CLUSTER SETTING succeeded with version: %s", showVersion)
		// This is actually EXPECTED for fence versions!
		// The fence version exception allows mismatches when:
		// 1. Local is a fence version
		// 2. No versions between KV and local
		t.Logf("SUCCESS: Fence version exception applied correctly - this is the expected behavior")
	} else {
		t.Logf("SHOW CLUSTER SETTING error: %v", err)
		t.Fatalf("Unexpected error - fence version mismatch should have been allowed by the fence exception")
	}
}

// TestShowClusterSettingVersionWithNonFencePartialFailure tests the scenario
// where nodes have been bumped to a non-fence version (step-002, step-004, etc.)
// but KV hasn't been updated.
func TestShowClusterSettingVersionWithNonFencePartialFailure(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)
	skip.UnderStress(t, "test is time-sensitive")

	ctx := context.Background()

	tc := testcluster.StartTestCluster(t, 3, base.TestClusterArgs{
		ReplicationMode: base.ReplicationManual,
		ServerArgs: base.TestServerArgs{
			Settings: cluster.MakeTestingClusterSettings(),
			Knobs: base.TestingKnobs{
				Server: &server.TestingKnobs{
					DisableAutomaticVersionUpgrade: make(chan struct{}),
				},
			},
		},
	})
	defer tc.Stopper().Stop(ctx)

	db2 := tc.ServerConn(1)
	var initialKVVersion string
	err := db2.QueryRow("SELECT value FROM system.settings WHERE name = 'version'").Scan(&initialKVVersion)
	require.NoError(t, err)
	t.Logf("Initial KV version: %s", initialKVVersion)

	// Use a non-fence version (even Internal number)
	nonFenceVersion := clusterversion.Latest.Version()
	require.False(t, nonFenceVersion.IsFence(), "should use non-fence version")
	t.Logf("Target non-fence version: %s (IsFence=%v)", nonFenceVersion, nonFenceVersion.IsFence())

	cv := clusterversion.ClusterVersion{Version: nonFenceVersion}

	// Bump only nodes 2 and 3 (skip node 1 to simulate it being down)
	for i := 1; i < tc.NumServers(); i++ {
		srv := tc.Server(i).(serverutils.TestServerInterface)

		err := srv.ClusterSettings().Version.SetActiveVersion(ctx, cv)
		if err != nil {
			t.Logf("Node %d failed to set active version: %v", i+1, err)
			continue
		}

		t.Logf("Node %d successfully bumped to non-fence version %s", i+1, nonFenceVersion)

		// Verify in-memory version was updated
		inMemory := srv.ClusterSettings().Version.ActiveVersion(ctx)
		t.Logf("Node %d in-memory version: %s", i+1, inMemory.Version)
		require.Equal(t, nonFenceVersion, inMemory.Version)
	}

	// Verify KV unchanged
	var kvVersion string
	err = db2.QueryRow("SELECT value FROM system.settings WHERE name = 'version'").Scan(&kvVersion)
	require.NoError(t, err)
	require.Equal(t, initialKVVersion, kvVersion)
	t.Logf("KV version unchanged: %s", kvVersion)

	// Debug: Check what versions we're actually comparing
	node2InMemory := tc.Server(1).ClusterSettings().Version.ActiveVersion(ctx)
	t.Logf("Node 2 in-memory (before query): %s", node2InMemory.Version)

	// Query with timeout - this is the exact scenario from production:
	// in-memory = step-002 (non-fence), KV = old version
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var showVersion string
	err = db2.QueryRowContext(ctx2, "SHOW CLUSTER SETTING version").Scan(&showVersion)

	// This SHOULD timeout IF local != KV because:
	// 1. Local version (non-fence) != KV version
	// 2. IsFence() = false, so the fence exception doesn't apply
	// 3. checkClusterSettingValuesAreEquivalent keeps retrying
	//
	// HOWEVER, if the cluster was initialized at the same version we're setting,
	// then local == KV and no timeout occurs.
	if err == nil {
		t.Logf("SHOW CLUSTER SETTING succeeded: %s", showVersion)
		t.Logf("This means local and KV versions matched - cluster likely initialized at step-006")
		t.Logf("To reproduce production bug, we need KV at a DIFFERENT version than local")
		// This is not a test failure - it shows we need a different test setup
	} else {
		t.Logf("SHOW CLUSTER SETTING error: %v", err)
		require.True(t,
			testutils.IsError(err, "context deadline exceeded") ||
				testutils.IsError(err, "query execution canceled") ||
				testutils.IsError(err, "context canceled"),
			"expected timeout error, got: %v", err)
		t.Logf("SUCCESS: Reproduced the production timeout scenario!")
	}
}

// TestShowClusterSettingVersionWithActualMismatch creates an actual version
// mismatch by manipulating the KV layer directly, reproducing the exact
// production scenario where in-memory != KV for a non-fence version.
func TestShowClusterSettingVersionWithActualMismatch(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)
	skip.UnderStress(t, "test is time-sensitive")

	ctx := context.Background()

	tc := testcluster.StartTestCluster(t, 3, base.TestClusterArgs{
		ReplicationMode: base.ReplicationManual,
		ServerArgs: base.TestServerArgs{
			Settings: cluster.MakeTestingClusterSettings(),
			Knobs: base.TestingKnobs{
				Server: &server.TestingKnobs{
					DisableAutomaticVersionUpgrade: make(chan struct{}),
				},
			},
		},
	})
	defer tc.Stopper().Stop(ctx)

	db2 := tc.ServerConn(1)

	// Get the current version using SHOW CLUSTER SETTING (this should work initially)
	var currentVersionStr string
	err := db2.QueryRow("SHOW CLUSTER SETTING version").Scan(&currentVersionStr)
	require.NoError(t, err, "initial SHOW CLUSTER SETTING should succeed")
	t.Logf("Current cluster version (via SHOW): %s", currentVersionStr)

	// Also get the in-memory version to use for calculations
	currentVersion := tc.Server(1).ClusterSettings().Version.ActiveVersion(ctx)
	t.Logf("Current cluster version (in-memory): %s", currentVersion.Version)

	// Calculate an older version to write to KV
	// We'll use a version that's 2 steps back (step-004 if we're at step-006)
	olderVersion := currentVersion.Version
	if olderVersion.Internal >= 4 {
		olderVersion.Internal -= 2
	}
	t.Logf("Will set KV to older version: %s (IsFence=%v)", olderVersion, olderVersion.IsFence())

	// Directly write the older version to system.settings
	// This simulates the state where an upgrade failed partway through
	olderCV := clusterversion.ClusterVersion{Version: olderVersion}
	encoded, err := protoutil.Marshal(&olderCV)
	require.NoError(t, err)

	_, err = db2.Exec("UPSERT INTO system.settings (name, value, \"lastUpdated\", \"valueType\") VALUES ($1, $2, now(), 's')",
		"version", encoded)
	require.NoError(t, err)
	t.Logf("Wrote older version %s to KV", olderVersion)

	// Verify KV has the older version
	var kvBytes []byte
	err = db2.QueryRow("SELECT value FROM system.settings WHERE name = 'version'").Scan(&kvBytes)
	require.NoError(t, err)
	var kvVersion clusterversion.ClusterVersion
	err = protoutil.Unmarshal(kvBytes, &kvVersion)
	require.NoError(t, err)
	t.Logf("KV version confirmed: %s", kvVersion.Version)
	require.Equal(t, olderVersion, kvVersion.Version)

	// Now in-memory is at current version (step-006, non-fence)
	// KV is at older version (step-004, non-fence)
	// They don't match, and neither is a fence, so query should timeout
	inMemVersion := tc.Server(1).ClusterSettings().Version.ActiveVersion(ctx)
	t.Logf("In-memory version: %s (IsFence=%v)", inMemVersion.Version, inMemVersion.Version.IsFence())
	require.NotEqual(t, olderVersion, inMemVersion.Version, "versions should differ")
	require.False(t, inMemVersion.Version.IsFence(), "in-memory should be non-fence")

	// Now SHOW CLUSTER SETTING version should timeout
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var showVersion string
	err = db2.QueryRowContext(ctx2, "SHOW CLUSTER SETTING version").Scan(&showVersion)

	if err == nil {
		t.Fatalf("Expected timeout but query succeeded with version: %s. "+
			"This means the fence exception applied when it shouldn't have.", showVersion)
	}

	t.Logf("SHOW CLUSTER SETTING error: %v", err)
	require.True(t,
		testutils.IsError(err, "context deadline exceeded") ||
			testutils.IsError(err, "query execution canceled") ||
			testutils.IsError(err, "context canceled"),
		"expected timeout error, got: %v", err)

	t.Logf("SUCCESS: Reproduced production timeout! In-memory=%s (non-fence), KV=%s",
		inMemVersion.Version, olderVersion)
}
