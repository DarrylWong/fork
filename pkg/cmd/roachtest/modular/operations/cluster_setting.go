// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
)

// ClusterSettingSpec defines a cluster setting and its possible values
type ClusterSettingSpec struct {
	Name   string
	Values []interface{}
}

// ClusterSettingOptions contains configuration for cluster setting operations
type ClusterSettingOptions struct {
	// RevertProbability is the probability (0.0-1.0) that the setting will be reverted
	// Default is 0.5 (50% chance)
	RevertProbability float64
	// Settings is the list of cluster settings to choose from
	// If nil, uses a default set of safe settings
	Settings []ClusterSettingSpec
	// SleepDuration is how long to wait before potentially reverting (if 0, reverts immediately)
	SleepDuration time.Duration
}

// ClusterSettingOp implements the Operation interface for cluster setting changes
type ClusterSettingOp struct {
	name    string
	builder *modular.OperationBuilder
	opts    ClusterSettingOptions
}

// Chain returns the operation's chain of steps
func (cs *ClusterSettingOp) Chain() modular.Chain {
	return cs.builder.Chain
}

// Name returns the operation's name
func (cs *ClusterSettingOp) Name() string {
	return cs.name
}

func (cs *ClusterSettingOp) Timeout() time.Duration {
	return 10*time.Minute + cs.opts.SleepDuration
}

// defaultClusterSettings returns a safe set of cluster settings that can be changed
func defaultClusterSettings() []ClusterSettingSpec {
	return []ClusterSettingSpec{
		{
			Name:   "kv.expiration_leases_only.enabled",
			Values: []interface{}{true, false},
		},
		{
			Name:   "kv.raft.leader_fortification.fraction_enabled",
			Values: []interface{}{"0", "0.25", "0.5", "0.75", "1.0"},
		},
		{
			Name:   "kv.transaction.write_buffering.enabled",
			Values: []interface{}{true, false},
		},
		{
			Name:   "obs.tablemetadata.automatic_updates.enabled",
			Values: []interface{}{true, false},
		},
		{
			Name:   "storage.wal_failover.unhealthy_op_threshold",
			Values: []interface{}{5, 50, 100, 150, 200, 250},
		},
		{
			Name:   "kv.rangefeed.enabled",
			Values: []interface{}{true, false},
		},
		{
			Name:   "server.declined_reservation_timeout",
			Values: []interface{}{"5s", "10s", "30s", "1m"},
		},
		{
			Name:   "sql.stats.automatic_collection.enabled",
			Values: []interface{}{true, false},
		},
		{
			Name:   "storage.ingest_split.enabled",
			Values: []interface{}{true, false},
		},
		{
			Name:   "storage.sstable.compression_algorithm",
			Values: []interface{}{"snappy", "zstd"},
		},
		{
			Name:   "kv.rangefeed.buffered_sender.enabled",
			Values: []interface{}{true, false},
		},
	}
}

// ChangeClusterSetting creates an operation that changes a random cluster setting
// and optionally reverts it based on the configured probability
func ChangeClusterSetting(c cluster.Cluster, opts ClusterSettingOptions) modular.Operation {
	// Set defaults
	if opts.RevertProbability == 0 {
		opts.RevertProbability = 0.5 // 50% chance of reverting
	}
	if opts.Settings == nil {
		opts.Settings = defaultClusterSettings()
	}

	// Make all random decisions upfront
	rng, _ := randutil.NewPseudoRand()
	selectedSetting := opts.Settings[rng.Intn(len(opts.Settings))]
	selectedValue := selectedSetting.Values[rng.Intn(len(selectedSetting.Values))]
	willRevert := rng.Float64() < opts.RevertProbability

	// Build the operation chain
	builder := modular.NewOperation("change cluster setting", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		l.Printf("setting cluster setting %s to %v", selectedSetting.Name, selectedValue)
		return h.SetClusterSetting(selectedSetting.Name, fmt.Sprintf("%v", selectedValue))
	})

	// Add optional sleep before reverting (only if we're going to revert)
	if willRevert && opts.SleepDuration > 0 {
		builder = builder.Then("sleep before reverting", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
			l.Printf("sleeping %s before reverting", opts.SleepDuration)
			time.Sleep(opts.SleepDuration)
			return nil
		})
	}

	// Conditionally add revert step
	if willRevert {
		builder = builder.Then("revert cluster setting", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
			l.Printf("reverting cluster setting %s to default", selectedSetting.Name)
			return h.ResetClusterSetting(selectedSetting.Name)
		})
	}

	opName := fmt.Sprintf("cluster-setting/%s=%v", selectedSetting.Name, selectedValue)
	if willRevert {
		opName += "/revert"
	}

	return &ClusterSettingOp{
		name:    opName,
		builder: builder,
		opts:    opts,
	}
}
