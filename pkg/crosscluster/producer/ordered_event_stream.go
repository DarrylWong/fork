// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package producer

import (
	"context"

	"github.com/cockroachdb/cockroach/pkg/kv"
	"github.com/cockroachdb/cockroach/pkg/repstream"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/eval"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/types"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
)

// orderedEventStreamAdapter wraps a repstream.OrderedStreamHandler and
// adds eval.ValueGenerator delegation to the underlying eventStream.
// This is needed because the replication producer uses OrderedStreamHandler
// as a ValueGenerator for SQL-level streaming over pgwire.
type orderedEventStreamAdapter struct {
	*repstream.OrderedStreamHandler
	handler *eventStream
}

var _ eval.ValueGenerator = (*orderedEventStreamAdapter)(nil)

func newOrderedEventStreamAdapter(
	handler *eventStream, config *repstream.OrderedBufferConfig, initialScanTs hlc.Timestamp,
) *orderedEventStreamAdapter {
	return &orderedEventStreamAdapter{
		OrderedStreamHandler: repstream.NewOrderedStreamHandler(handler, config, initialScanTs),
		handler:              handler,
	}
}

// ValueGenerator implementation: delegate to handler (which is an eval.ValueGenerator).
func (h *orderedEventStreamAdapter) ResolvedType() *types.T {
	return h.handler.ResolvedType()
}

func (h *orderedEventStreamAdapter) Start(ctx context.Context, txn *kv.Txn) error {
	return h.handler.Start(ctx, txn)
}

func (h *orderedEventStreamAdapter) Next(ctx context.Context) (bool, error) {
	return h.handler.Next(ctx)
}

func (h *orderedEventStreamAdapter) Values() (tree.Datums, error) {
	return h.handler.Values()
}

func (h *orderedEventStreamAdapter) Close(ctx context.Context) {
	h.handler.Close(ctx)
	h.handler.SetErr(h.OrderedStreamHandler.Close(ctx))
}
