// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package roachtestutil

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/vm"
)

// ApplyOrCreateSnapshots is attempts to find existing snapshots for the given spec and apply them to the cluster.
// If snapshots don't exist, it inits the cluster according to the spec and creates the snapshots for
// future use.
func ApplyOrCreateSnapshots(
	ctx context.Context,
	t test.Test,
	c cluster.Cluster,
	nodes option.NodeListOption,
	snapshotPrefix string,
	init func() error,
) error {
	// Try to find existing snapshots.
	snapshots, err := c.ListSnapshots(ctx, vm.VolumeSnapshotListOpts{
		NamePrefix: snapshotPrefix,
	})
	if err != nil {
		return err
	}

	if len(snapshots) > 0 {
		return c.ApplySnapshots(ctx, snapshots, nodes)
	}

	// No snapshots found, init the cluster and create them.
	t.L().Printf("no snapshots found with prefix %q, creating new snapshots", snapshotPrefix)

	if err = init(); err != nil {
		return err
	}

	c.Stop(ctx, t.L(), option.DefaultStopOpts(), nodes)

	// Clear gossip bootstrap metadata to prevent clusters started from these
	// snapshots from attempting to connect to the original cluster.
	c.Run(ctx, option.WithNodes(nodes), "./cockroach debug clear-gossip-bootstrap {store-dir}")

	t.L().Printf("creating volume snapshots with prefix '%s'", snapshotPrefix)
	snapshots, err = c.CreateSnapshot(ctx, snapshotPrefix, nodes)
	if err != nil {
		return err
	}

	t.L().Printf("successfully created %d volume snapshot(s):", len(snapshots))
	for i, snapshot := range snapshots {
		t.L().Printf("  %d. %s (ID: %s)", i+1, snapshot.Name, snapshot.ID)
	}

	return nil
}
