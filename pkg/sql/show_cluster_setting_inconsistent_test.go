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
	"github.com/cockroachdb/cockroach/pkg/testutils/skip"
	"github.com/cockroachdb/cockroach/pkg/testutils/testcluster"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/stretchr/testify/require"
)

// TestShowClusterSettingVersionInconsistentState tests the behavior of
// SHOW CLUSTER SETTING version when the cluster is in an inconsistent state
// where the in-memory version differs from the KV version.
//
// This simulates the scenario where:
// 1. An in-memory version bump succeeds locally
// 2. The KV version (system.settings) hasn't been updated yet
// 3. SHOW CLUSTER SETTING version gets stuck retrying
func TestShowClusterSettingVersionInconsistentState(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)
	skip.UnderStress(t, "test is time-sensitive")

	ctx := context.Background()

	// Create a single-node cluster for simplicity
	tc := testcluster.StartTestCluster(t, 1, base.TestClusterArgs{
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

	// Get initial version from KV
	db := tc.ServerConn(0)
	var initialKVVersion string
	err := db.QueryRow("SELECT value FROM system.settings WHERE name = 'version'").Scan(&initialKVVersion)
	require.NoError(t, err)
	t.Logf("Initial KV version: %s", initialKVVersion)

	// Manually set the in-memory version to a fence version
	// without updating the KV version. This simulates a partial upgrade.
	srv := tc.Server(0)
	st := srv.ClusterSettings()

	fenceVersion := clusterversion.Latest.Version().FenceVersion()
	require.True(t, fenceVersion.IsFence(), "test version should be a fence")

	cv := clusterversion.ClusterVersion{Version: fenceVersion}
	err = st.Version.SetActiveVersion(ctx, cv)
	require.NoError(t, err)
	t.Logf("Set in-memory version to: %s (IsFence=%v)", fenceVersion, fenceVersion.IsFence())

	// Verify in-memory version was set
	inMemory := st.Version.ActiveVersion(ctx)
	require.Equal(t, fenceVersion, inMemory.Version)

	// Verify KV version is still the old version
	var kvVersion string
	err = db.QueryRow("SELECT value FROM system.settings WHERE name = 'version'").Scan(&kvVersion)
	require.NoError(t, err)
	require.Equal(t, initialKVVersion, kvVersion)
	t.Logf("KV version unchanged: %s", kvVersion)

	// Now fence version check should allow this mismatch
	// because the local version is a fence version
	var showVersion string
	err = db.QueryRow("SHOW CLUSTER SETTING version").Scan(&showVersion)
	require.NoError(t, err, "SHOW CLUSTER SETTING should succeed for fence version mismatch")
	t.Logf("SHOW CLUSTER SETTING succeeded with version: %s", showVersion)
}

// TestShowClusterSettingVersionNonFenceInconsistency tests the case where
// the local in-memory version is a non-fence version that differs from KV.
// This is the specific case observed in the bug report where the local
// version was step-002 (Internal=2, even, not a fence) while KV was 25.4.
func TestShowClusterSettingVersionNonFenceInconsistency(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()

	// Create a single-node cluster for simplicity
	tc := testcluster.StartTestCluster(t, 1, base.TestClusterArgs{
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

	// Get initial version
	db := tc.ServerConn(0)
	var initialVersion string
	err := db.QueryRow("SELECT value FROM system.settings WHERE name = 'version'").Scan(&initialVersion)
	require.NoError(t, err)
	t.Logf("Initial version: %s", initialVersion)

	// Manually bump the in-memory version to a non-fence version
	// without updating the KV version. This simulates the bug scenario.
	srv := tc.Server(0)
	st := srv.ClusterSettings()

	// Create a non-fence version (Internal=2, which is even)
	nonFenceVersion := clusterversion.Latest.Version()
	require.False(t, nonFenceVersion.IsFence(), "test version should not be a fence")

	cv := clusterversion.ClusterVersion{Version: nonFenceVersion}
	err = st.Version.SetActiveVersion(ctx, cv)
	require.NoError(t, err)
	t.Logf("Set in-memory version to: %s (IsFence=%v)", nonFenceVersion, nonFenceVersion.IsFence())

	// Verify in-memory version was set
	inMemory := st.Version.ActiveVersion(ctx)
	require.Equal(t, nonFenceVersion, inMemory.Version)

	// Verify KV version is still the old version
	var kvVersion string
	err = db.QueryRow("SELECT value FROM system.settings WHERE name = 'version'").Scan(&kvVersion)
	require.NoError(t, err)
	require.Equal(t, initialVersion, kvVersion)
	t.Logf("KV version unchanged: %s", kvVersion)

	// Now query SHOW CLUSTER SETTING version
	// Since the local version is NOT a fence, the fence version check won't help
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var showVersion string
	err = db.QueryRowContext(ctx, "SHOW CLUSTER SETTING version").Scan(&showVersion)

	// This should fail because:
	// 1. Local in-memory version (non-fence) != KV version
	// 2. The fence version exception doesn't apply (IsFence() returns false)
	// 3. It will retry until timeout
	require.Error(t, err)
	t.Logf("SHOW CLUSTER SETTING error: %v", err)

	require.True(t,
		testutils.IsError(err, "context deadline exceeded") ||
		testutils.IsError(err, "query execution canceled") ||
		testutils.IsError(err, "context canceled"),
		"expected timeout/cancellation error, got: %v", err)
}
