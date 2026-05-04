// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package txnapply

import "github.com/cockroachdb/cockroach/pkg/util/metric"

var (
	metaBlockedTxns = metric.Metadata{
		Name: "logical_replication.txn_applier.blocked_txns",
		Help: "Number of transactions the applier has received but not yet " +
			"written, blocked on either a txn dependency or the event horizon",
		Measurement: "Transactions",
		Unit:        metric.Unit_COUNT,
		Category:    metric.Metadata_LOGICAL_DATA_REPLICATION,
	}
	metaReadyTxns = metric.Metadata{
		Name: "logical_replication.txn_applier.ready_txns",
		Help: "Number of transactions that the applier has received and " +
			"are ready to be committed",
		Measurement: "Transactions",
		Unit:        metric.Unit_COUNT,
		Category:    metric.Metadata_LOGICAL_DATA_REPLICATION,
	}
)

// Metrics holds the txn-mode applier metrics for an LDR job. The struct is
// shared by all appliers in the job; each applier updates the gauges directly
// so the reported value is the sum across appliers.
type Metrics struct {
	BlockedTxns *metric.Gauge
	ReadyTxns   *metric.Gauge
}

// MetricStruct implements the metric.Struct interface.
func (*Metrics) MetricStruct() {}

// MakeMetrics constructs the txn-mode applier metrics.
func MakeMetrics() *Metrics {
	return &Metrics{
		BlockedTxns: metric.NewGauge(metaBlockedTxns),
		ReadyTxns:   metric.NewGauge(metaReadyTxns),
	}
}
