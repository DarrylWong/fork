// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package repstream

import (
	"context"

	"github.com/cockroachdb/cockroach/pkg/kv/kvpb"
	"github.com/cockroachdb/cockroach/pkg/repstream/streampb"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
)

// EventSink abstracts where processed rangefeed events are delivered.
// eventStream's handler methods (OnValue, OnSSTable, etc.) call sink methods
// instead of directly interacting with streamEventBatcher / streamCh.
//
// For replication (PCR/LDR), the sink batches events, serializes to protobuf,
// and sends over pgwire. For changefeeds (CDC), the sink writes kvevent.Events
// to a kvevent.Writer.
type EventSink interface {
	// OnKV delivers a single KV event to the sink.
	OnKV(ctx context.Context, kv streampb.StreamEvent_KV) error
	// OnSST delivers an SSTable event to the sink.
	OnSST(ctx context.Context, sst kvpb.RangeFeedSSTable) error
	// OnDelRange delivers a delete range event to the sink.
	OnDelRange(ctx context.Context, dr kvpb.RangeFeedDeleteRange) error
	// OnSplitPoint delivers a manual split point to the sink.
	OnSplitPoint(ctx context.Context, key roachpb.Key) error
	// MaybeFlush flushes the sink if size thresholds are met.
	MaybeFlush(ctx context.Context) error
	// Flush unconditionally flushes any buffered events.
	Flush(ctx context.Context) error
	// Close releases resources held by the sink.
	Close(ctx context.Context)
}
