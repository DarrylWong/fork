// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package changefeedccl

import (
	"context"

	"github.com/cockroachdb/cockroach/pkg/ccl/changefeedccl/kvevent"
	"github.com/cockroachdb/cockroach/pkg/jobs/jobspb"
	"github.com/cockroachdb/cockroach/pkg/kv/kvpb"
	"github.com/cockroachdb/cockroach/pkg/repstream"
	"github.com/cockroachdb/cockroach/pkg/repstream/streampb"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
	"github.com/cockroachdb/errors"
)

// changefeedSink implements repstream.EventSink for changefeeds. It converts
// rangefeed events into kvevent.Events and writes them to a kvevent.Writer,
// which is the existing changefeed buffer consumed by changeAggregator.tick().
type changefeedSink struct {
	writer kvevent.Writer

	// backfillTimestamp is set when the pipeline is running a backfill scan.
	// All events during a backfill are stamped with this so the downstream
	// decoder uses the correct schema version.
	backfillTimestamp hlc.Timestamp
}

var _ repstream.EventSink = (*changefeedSink)(nil)

// OnKV implements repstream.EventSink.
func (s *changefeedSink) OnKV(ctx context.Context, kv streampb.StreamEvent_KV) error {
	rfEvent := &kvpb.RangeFeedEvent{
		Val: &kvpb.RangeFeedValue{
			Key:       kv.KeyValue.Key,
			Value:     kv.KeyValue.Value,
			PrevValue: kv.PrevValue,
		},
	}
	var ev kvevent.Event
	if !s.backfillTimestamp.IsEmpty() {
		ev = kvevent.MakeKVEvent(rfEvent)
		// TODO(darryl): set backfill timestamp on the event once we have
		// a kvevent constructor that supports it without allocating.
	} else {
		ev = kvevent.MakeKVEvent(rfEvent)
	}
	return s.writer.Add(ctx, ev)
}

// OnSST implements repstream.EventSink. Changefeeds do not support SST
// ingestion; return an error so the rangefeed restarts and replays the
// writes as individual KVs.
func (s *changefeedSink) OnSST(_ context.Context, sst kvpb.RangeFeedSSTable) error {
	return errors.AssertionFailedf("unexpected SST ingestion in changefeed: %v", sst.Span)
}

// OnDelRange implements repstream.EventSink. Changefeeds ignore delete
// range events (same as kvfeed today).
func (s *changefeedSink) OnDelRange(_ context.Context, _ kvpb.RangeFeedDeleteRange) error {
	return nil
}

// OnSplitPoint implements repstream.EventSink. Changefeeds don't need
// manual split point metadata.
func (s *changefeedSink) OnSplitPoint(_ context.Context, _ roachpb.Key) error {
	return nil
}

// MaybeFlush implements repstream.EventSink. The kvevent.Writer handles
// its own buffering and backpressure, so this is a no-op.
func (s *changefeedSink) MaybeFlush(_ context.Context) error {
	return nil
}

// Flush implements repstream.EventSink. The kvevent.Writer handles its
// own buffering, so this is a no-op.
func (s *changefeedSink) Flush(_ context.Context) error {
	return nil
}

// Close implements repstream.EventSink.
func (s *changefeedSink) Close(_ context.Context) {}

// emitResolvedSpan writes a resolved span event to the kvevent.Writer.
// This is used by the changefeed rangefeed orchestrator to emit checkpoint
// and schema change boundary events.
func (s *changefeedSink) emitResolvedSpan(
	ctx context.Context,
	span roachpb.Span,
	ts hlc.Timestamp,
	boundaryType jobspb.ResolvedSpan_BoundaryType,
) error {
	ev := kvevent.NewBackfillResolvedEvent(span, ts, boundaryType)
	return s.writer.Add(ctx, ev)
}
