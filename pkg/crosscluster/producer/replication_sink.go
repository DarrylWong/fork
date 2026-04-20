// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package producer

import (
	"context"
	"sync/atomic"

	"github.com/cockroachdb/cockroach/pkg/kv/kvpb"
	"github.com/cockroachdb/cockroach/pkg/repstream"
	"github.com/cockroachdb/cockroach/pkg/repstream/streampb"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/settings"
	"github.com/cockroachdb/cockroach/pkg/sql"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/util/protoutil"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
	"github.com/golang/snappy"
)

// replicationSink implements EventSink for PCR/LDR. It batches events via
// streamEventBatcher, serializes to protobuf, optionally compresses, and sends
// over streamCh for pgwire delivery to a remote consumer.
type replicationSink struct {
	seb      streamEventBatcher
	seqNum   *uint64
	debug    *streampb.DebugProducerStatusHolder
	streamCh chan tree.Datums
	spec     streampb.StreamPartitionSpec
	execCfg  *sql.ExecutorConfig
	sv       *settings.Values

	consumerReady *atomic.Bool
}

var _ repstream.EventSink = (*replicationSink)(nil)

// OnKV implements EventSink.
func (s *replicationSink) OnKV(_ context.Context, kv streampb.StreamEvent_KV) error {
	s.seb.addKV(kv)
	return nil
}

// OnSST implements EventSink.
func (s *replicationSink) OnSST(_ context.Context, sst kvpb.RangeFeedSSTable) error {
	s.seb.addSST(sst)
	return nil
}

// OnDelRange implements EventSink.
func (s *replicationSink) OnDelRange(_ context.Context, dr kvpb.RangeFeedDeleteRange) error {
	s.seb.addDelRange(dr)
	return nil
}

// OnSplitPoint implements EventSink.
func (s *replicationSink) OnSplitPoint(_ context.Context, key roachpb.Key) error {
	s.seb.addSplitPoint(key)
	return nil
}

// MaybeFlush implements EventSink.
func (s *replicationSink) MaybeFlush(ctx context.Context) error {
	// If the consumer is ready to ingest, flush at a lower threshold. This
	// ensures the consumer always has work to do.
	//
	// If the consumer is not ready, the larger batch delays the flush call,
	// preventing the slow consumer from blocking rangefeed progress and avoiding
	// catchup scans.
	if s.seb.size > int(s.spec.Config.BatchByteSize) {
		return s.flush(ctx, streampb.FlushFull)
	}
	if s.consumerReady.Load() && s.seb.size > minBatchByteSize {
		return s.flush(ctx, streampb.FlushReady)
	}
	return nil
}

// Flush implements EventSink.
func (s *replicationSink) Flush(ctx context.Context) error {
	return s.flush(ctx, streampb.FlushCheckpoint)
}

// Close implements EventSink.
func (s *replicationSink) Close(_ context.Context) {}

func (s *replicationSink) flush(ctx context.Context, reason streampb.FlushReason) error {
	if s.seb.size == 0 {
		return nil
	}
	defer s.seb.reset()

	if debugSettingDropData.Get(s.sv) {
		return nil
	}

	*s.seqNum++
	s.debug.Flushed(int64(s.seb.size), reason, *s.seqNum)

	return s.sendFlush(ctx, &streampb.StreamEvent{
		StreamSeq: *s.seqNum,
		Batch:     &s.seb.batch,
	})
}

func (s *replicationSink) sendFlush(ctx context.Context, event *streampb.StreamEvent) error {
	event.EmitUnixNanos = timeutil.Now().UnixNano()
	data, err := protoutil.Marshal(event)
	if err != nil {
		return err
	}
	if s.spec.Compressed {
		data = snappy.Encode(nil, data)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.streamCh <- tree.Datums{tree.NewDBytes(tree.DBytes(data))}:
		return nil
	}
}
