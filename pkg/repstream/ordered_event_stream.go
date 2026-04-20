// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package repstream

import (
	"context"

	"github.com/cockroachdb/cockroach/pkg/kv/kvpb"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/storage"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
	"github.com/cockroachdb/errors"
)

// RangefeedHandler is the interface for processing rangefeed events. An event
// stream delegates event handling to its adapter, which implements this
// interface. Implementations include the event stream itself (unordered) and
// OrderedStreamHandler (disk-backed ordering wrapper).
type RangefeedHandler interface {
	OnValue(ctx context.Context, value *kvpb.RangeFeedValue)
	OnValues(ctx context.Context, values []kvpb.RangeFeedValue)
	OnFrontier(ctx context.Context, timestamp hlc.Timestamp)
	OnCheckpoint(ctx context.Context, checkpoint *kvpb.RangeFeedCheckpoint)
	OnSSTable(ctx context.Context, sst *kvpb.RangeFeedSSTable, registeredSpan roachpb.Span)
	OnDeleteRange(ctx context.Context, delRange *kvpb.RangeFeedDeleteRange)
	OnMetadata(ctx context.Context, metadata *kvpb.RangeFeedMetadata)
	OnInitialScanDone(ctx context.Context)
	// OnFrontierAdvance is called by the ordered adapter after it has fed all
	// events up to and including resolvedTs into the handler's batch; the handler
	// should flush that batch to the consumer.
	OnFrontierAdvance(ctx context.Context, resolvedTs hlc.Timestamp)
	SetErr(error) bool
}

// OrderedStreamHandler wraps a RangefeedHandler and buffers KVs, delivering
// them in (timestamp, key) order on frontier advance. SSTable events are
// handled by reading their KVs and adding each to the buffer.
type OrderedStreamHandler struct {
	Handler    RangefeedHandler
	buffer     *OrderedBuffer
	ResolvedTs hlc.Timestamp // Last flushed resolved timestamp for checkpoint generation.
}

// NewOrderedStreamHandler returns an ordered adapter that wraps the given
// handler.
func NewOrderedStreamHandler(
	handler RangefeedHandler, config *OrderedBufferConfig, initialScanTs hlc.Timestamp,
) *OrderedStreamHandler {
	return &OrderedStreamHandler{
		Handler:    handler,
		buffer:     NewOrderedBuffer(*config),
		ResolvedTs: initialScanTs,
	}
}

func (h *OrderedStreamHandler) SetErr(err error) bool {
	return h.Handler.SetErr(err)
}

func (h *OrderedStreamHandler) OnValue(ctx context.Context, value *kvpb.RangeFeedValue) {
	h.Handler.SetErr(h.buffer.Add(ctx, value))
}

func (h *OrderedStreamHandler) OnValues(ctx context.Context, values []kvpb.RangeFeedValue) {
	for i := range values {
		h.OnValue(ctx, &values[i])
	}
}

func (h *OrderedStreamHandler) HandleFrontier(ctx context.Context, resolvedTs hlc.Timestamp) error {
	err := h.buffer.FlushToDisk(ctx, resolvedTs)
	if err != nil {
		return err
	}
	var iterExhausted bool
	var events []kvpb.RangeFeedEvent

	// iterate until the iterator is exhausted. This is necessary because GetEventsFromDisk
	// returns early when the batch threshold is reached.
	for !iterExhausted {
		events, iterExhausted, err = h.buffer.GetEventsFromDisk(ctx, resolvedTs)
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.Val != nil {
				h.Handler.OnValue(ctx, e.Val)
			} else if e.DeleteRange != nil {
				h.Handler.OnDeleteRange(ctx, e.DeleteRange)
			} else {
				return errors.AssertionFailedf("unexpected RangeFeedEvent variant: %v", e)
			}
		}
	}
	h.Handler.OnFrontierAdvance(ctx, resolvedTs)
	h.ResolvedTs = resolvedTs
	return nil
}

func (h *OrderedStreamHandler) OnFrontier(ctx context.Context, resolvedTs hlc.Timestamp) {
	h.Handler.SetErr(h.HandleFrontier(ctx, resolvedTs))
}

func (h *OrderedStreamHandler) OnCheckpoint(
	ctx context.Context, checkpoint *kvpb.RangeFeedCheckpoint,
) {
	h.Handler.OnCheckpoint(ctx, checkpoint)
}

func (h *OrderedStreamHandler) handleSSTable(
	ctx context.Context, sst *kvpb.RangeFeedSSTable, registeredSpan roachpb.Span,
) error {
	// Read each KV from the SST and add to the buffer so they are delivered in
	// (timestamp, key) order with rangefeed KVs.
	scanWithin := sst.Span
	if !registeredSpan.Contains(sst.Span) {
		scanWithin = registeredSpan
	}
	return ScanSST(sst, scanWithin,
		func(kv storage.MVCCKeyValue) error {
			v, err := storage.DecodeValueFromMVCCValue(kv.Value)
			if err != nil {
				return err
			}
			return h.buffer.Add(ctx, &kvpb.RangeFeedValue{
				Key:   kv.Key.Key,
				Value: roachpb.Value{RawBytes: v.RawBytes, Timestamp: kv.Key.Timestamp},
			})
		},
		func(rk storage.MVCCRangeKeyValue) error {
			return h.buffer.AddDelRange(ctx, &kvpb.RangeFeedDeleteRange{
				Span:      roachpb.Span{Key: rk.RangeKey.StartKey, EndKey: rk.RangeKey.EndKey},
				Timestamp: rk.RangeKey.Timestamp,
			})
		},
	)
}

func (h *OrderedStreamHandler) OnSSTable(
	ctx context.Context, sst *kvpb.RangeFeedSSTable, registeredSpan roachpb.Span,
) {
	h.Handler.SetErr(h.handleSSTable(ctx, sst, registeredSpan))
}

func (h *OrderedStreamHandler) OnDeleteRange(
	ctx context.Context, delRange *kvpb.RangeFeedDeleteRange,
) {
	h.Handler.SetErr(h.buffer.AddDelRange(ctx, delRange))
}

func (h *OrderedStreamHandler) OnMetadata(ctx context.Context, metadata *kvpb.RangeFeedMetadata) {
}

func (h *OrderedStreamHandler) OnInitialScanDone(ctx context.Context) {
	// Use h.ResolvedTs, which is initialized to InitialScanTimestamp.
	h.Handler.SetErr(h.HandleFrontier(ctx, h.ResolvedTs))
	h.Handler.OnInitialScanDone(ctx)
}

// OnFrontierAdvance is required by RangefeedHandler but is only invoked on the
// handler (the event stream), not on the adapter. No-op here.
func (h *OrderedStreamHandler) OnFrontierAdvance(_ context.Context, _ hlc.Timestamp) {}

// Close releases resources held by the ordered buffer.
func (h *OrderedStreamHandler) Close(ctx context.Context) error {
	return h.buffer.Close(ctx)
}
