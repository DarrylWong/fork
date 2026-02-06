// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package rangescanstats

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/util/ctxgroup"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/rangedesc"
	"github.com/cockroachdb/cockroach/pkg/util/rangescanstats/rangescanstatspb"
	"github.com/cockroachdb/cockroach/pkg/util/span"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
)

// RangeStatsPoller manages a goroutine that polls the total number of ranges
// and their scanning status. Close must be called to avoid leaking the
// goroutine.
type RangeStatsPoller struct {
	cancel func()
	g      ctxgroup.Group
	stats  atomic.Pointer[rangescanstatspb.RangeStats]
}

// StatsHandlerFunc is a function that consumes polled range stats.
type StatsHandlerFunc func(r *RangeStatsPoller, totalRangeCount, scanningRangeCount, laggingRangeCount int64)

func StartStatsPoller(
	ctx context.Context,
	interval time.Duration,
	spans []roachpb.Span,
	frontier span.Frontier,
	ranges rangedesc.IteratorFactory,
	laggingSpanThreshold time.Duration,
	callbackFunc StatsHandlerFunc,
) *RangeStatsPoller {
	ctx, cancel := context.WithCancel(ctx)
	poller := &RangeStatsPoller{
		cancel: cancel,
		g:      ctxgroup.WithContext(ctx),
	}
	poller.g.GoCtx(func(ctx context.Context) error {
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			totalRangeCount, scanningRangeCount, laggingRangeCount, err := computeRangeStats(ctx, spans, frontier, ranges, laggingSpanThreshold)
			if err != nil {
				log.Dev.Warningf(ctx, "unable to calculate range scan stats: %v", err)
			} else {
				callbackFunc(poller, totalRangeCount, scanningRangeCount, laggingRangeCount)
			}

			log.VEventf(ctx, 1, "publishing range scan stats: totalRanges=%d, scanningRanges=%d, laggingRanges=%d", totalRangeCount, scanningRangeCount, laggingRangeCount)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-tick.C:
				//continue
			}
		}
	})
	return poller
}

// Close cancels the internal context and waits for the goroutine to exit.
func (r *RangeStatsPoller) Close() {
	r.cancel()
	_ = r.g.Wait()
}

// MaybeStats returns the most recent stats if they are available or null if
// the initial stats calculation is not ready.
func (r *RangeStatsPoller) MaybeStats() *rangescanstatspb.RangeStats {
	return r.stats.Load()
}

// StoreStatsHandler is a StatsHandler that stores the most recent stats in
// the given RangeStatsPoller. The stats can be retrieved using MaybeStats.
func StoreStatsHandler(r *RangeStatsPoller, totalRangeCount, scanningRangeCount, laggingRangeCount int64) {
	stats := &rangescanstatspb.RangeStats{
		RangeCount:         totalRangeCount,
		ScanningRangeCount: scanningRangeCount,
		LaggingRangeCount:  laggingRangeCount,
	}
	r.stats.Store(stats)
}

func computeRangeStats(
	ctx context.Context,
	spans []roachpb.Span,
	frontier span.Frontier,
	ranges rangedesc.IteratorFactory,
	laggingSpanThreshold time.Duration,
) (totalRangeCount int64, scanningRangeCount int64, laggingRangeCount int64, err error) {
	for _, initialSpan := range spans {
		lazyIterator, err := ranges.NewLazyIterator(ctx, initialSpan, 100)
		if err != nil {
			return 0, 0, 0, err
		}
		for ; lazyIterator.Valid(); lazyIterator.Next() {
			now := timeutil.Now()
			rangeSpan := roachpb.Span{
				Key:    lazyIterator.CurRangeDescriptor().StartKey.AsRawKey(),
				EndKey: lazyIterator.CurRangeDescriptor().EndKey.AsRawKey(),
			}
			totalRangeCount += 1
			for _, timestamp := range frontier.SpanEntries(rangeSpan) {
				if timestamp.IsEmpty() {
					scanningRangeCount += 1
					break
				} else if now.Sub(timestamp.GoTime()) > laggingSpanThreshold {
					laggingRangeCount += 1
					break
				}
			}
		}
		if lazyIterator.Error() != nil {
			return 0, 0, 0, lazyIterator.Error()
		}
	}
	return totalRangeCount, scanningRangeCount, laggingRangeCount, nil
}
