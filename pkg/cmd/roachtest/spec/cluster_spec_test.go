// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package spec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClustersCompatible(t *testing.T) {
	t.Run("spec does not match", func(t *testing.T) {
		s1 := ClusterSpec{NodeCount: 4}
		s2 := ClusterSpec{NodeCount: 5}
		require.False(t, ClustersCompatible(s1, s2, GCE))
	})
	t.Run("spec has different lifetime", func(t *testing.T) {
		s1 := ClusterSpec{NodeCount: 5, Lifetime: 100}
		s2 := ClusterSpec{NodeCount: 5, Lifetime: 200}
		require.True(t, ClustersCompatible(s1, s2, GCE))
	})
	t.Run("spec has different GCE spec with cloud as GCE", func(t *testing.T) {
		s1 := ClusterSpec{NodeCount: 5}
		s2 := ClusterSpec{NodeCount: 5}
		s1.VolumeType = "mock_volume1"
		s2.VolumeType = "mock_volume2"
		require.False(t, ClustersCompatible(s1, s2, GCE))
	})
	t.Run("spec has different GCE spec with cloud as AWS", func(t *testing.T) {
		s1 := ClusterSpec{NodeCount: 5}
		s2 := ClusterSpec{NodeCount: 5}
		s1.VolumeType = "mock_volume1"
		s2.VolumeType = "mock_volume2"
		require.False(t, ClustersCompatible(s1, s2, AWS))
	})
	t.Run("spec has different spec with cloud as AWS", func(t *testing.T) {
		s1 := ClusterSpec{NodeCount: 5}
		s2 := ClusterSpec{NodeCount: 5}
		s1.GCE.MinCPUPlatform = "mock_platform1"
		s2.GCE.MinCPUPlatform = "mock_platform2"
		require.True(t, ClustersCompatible(s1, s2, AWS))
	})
}

func TestClustersRetainClearedInfo(t *testing.T) {
	// Adding a test in case we switch the ClustersCompatible signature to take
	// pointers to ClusterSpec in the future.
	t.Run("original structs are not modified", func(t *testing.T) {
		s1 := ClusterSpec{
			NodeCount:              5,
			ExposedMetamorphicInfo: map[string]string{"VolumeType": "io2"},
		}
		s2 := ClusterSpec{
			NodeCount:              5,
			ExposedMetamorphicInfo: map[string]string{"VolumeType": "gp3"},
		}

		ClustersCompatible(s1, s2, GCE)

		// Original data should still be there
		require.Equal(t, "io2", s1.ExposedMetamorphicInfo["VolumeType"])
		require.Equal(t, "gp3", s2.ExposedMetamorphicInfo["VolumeType"])
	})
}

func TestRandomizeVolumeTypeSyncsDiskCounts(t *testing.T) {
	params := RoachprodClusterConfig{
		Cloud: GCE,
	}

	t.Run("SSD count syncs to VolumeCount when only SSD is set", func(t *testing.T) {
		spec := ClusterSpec{
			NodeCount:           3,
			CPUs:                4,
			SSDs:                4,
			RandomizeVolumeType: true,
			ExposedMetamorphicInfo: make(map[string]string),
		}

		_, _, _, _, err := spec.RoachprodOpts(params)
		require.NoError(t, err)

		// Both should be synced to 4
		require.Equal(t, 4, spec.SSDs)
		require.Equal(t, 4, spec.VolumeCount)
	})

	t.Run("VolumeCount syncs to SSD when only VolumeCount is set", func(t *testing.T) {
		spec := ClusterSpec{
			NodeCount:           3,
			CPUs:                4,
			VolumeCount:         4,
			RandomizeVolumeType: true,
			ExposedMetamorphicInfo: make(map[string]string),
		}

		_, _, _, _, err := spec.RoachprodOpts(params)
		require.NoError(t, err)

		// Both should be synced to 4
		require.Equal(t, 4, spec.SSDs)
		require.Equal(t, 4, spec.VolumeCount)
	})

	t.Run("Both values respected when both are explicitly set", func(t *testing.T) {
		spec := ClusterSpec{
			NodeCount:           3,
			CPUs:                4,
			SSDs:                2,
			VolumeCount:         4,
			RandomizeVolumeType: true,
			ExposedMetamorphicInfo: make(map[string]string),
		}

		_, _, _, _, err := spec.RoachprodOpts(params)
		require.NoError(t, err)

		// Both should remain unchanged when explicitly set differently
		require.Equal(t, 2, spec.SSDs)
		require.Equal(t, 4, spec.VolumeCount)
	})

	t.Run("No sync when RandomizeVolumeType is not set", func(t *testing.T) {
		spec := ClusterSpec{
			NodeCount:              3,
			CPUs:                   4,
			SSDs:                   4,
			RandomizeVolumeType:    false,
			ExposedMetamorphicInfo: make(map[string]string),
		}

		_, _, _, _, err := spec.RoachprodOpts(params)
		require.NoError(t, err)

		// VolumeCount should remain at default (0) when RandomizeVolumeType is false
		require.Equal(t, 4, spec.SSDs)
		require.Equal(t, 0, spec.VolumeCount)
	})
}
