// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package changefeedccl

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/cockroach/pkg/ccl/changefeedccl/cdcutils"
	"github.com/cockroachdb/cockroach/pkg/ccl/changefeedccl/changefeedbase"
	"github.com/cockroachdb/cockroach/pkg/ccl/changefeedccl/kvevent"
	"github.com/cockroachdb/cockroach/pkg/ccl/changefeedccl/schemafeed"
	"github.com/cockroachdb/cockroach/pkg/jobs/jobspb"
	"github.com/cockroachdb/cockroach/pkg/keys"
	"github.com/cockroachdb/cockroach/pkg/kv"
	"github.com/cockroachdb/cockroach/pkg/kv/kvclient/kvcoord"
	"github.com/cockroachdb/cockroach/pkg/kv/kvclient/rangefeed"
	"github.com/cockroachdb/cockroach/pkg/kv/kvpb"
	"github.com/cockroachdb/cockroach/pkg/repstream"
	"github.com/cockroachdb/cockroach/pkg/repstream/streampb"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/rpc/rpcbase"
	"github.com/cockroachdb/cockroach/pkg/sql"
	"github.com/cockroachdb/cockroach/pkg/sql/execinfra"
	"github.com/cockroachdb/cockroach/pkg/storage/enginepb"
	"github.com/cockroachdb/cockroach/pkg/util/admission/admissionpb"
	"github.com/cockroachdb/cockroach/pkg/util/ctxgroup"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
	"github.com/cockroachdb/cockroach/pkg/util/limit"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/mon"
	"github.com/cockroachdb/cockroach/pkg/util/span"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
	"github.com/cockroachdb/errors"
)

// startKVFeed creates a rangefeed that streams KV changes for the watched
// spans. Events are written as kvevent.Events to a kvevent.Writer buffer,
// which changeAggregator.tick() reads from unchanged.
func (ca *changeAggregator) startKVFeed(
	ctx context.Context,
	spans []roachpb.Span,
	initialHighWater hlc.Timestamp,
	needsInitialScan bool,
	config ChangefeedConfig,
	parentMemMon *mon.BytesMonitor,
	memLimit int64,
	opts changefeedbase.StatementOptions,
) (kvevent.Reader, chan struct{}, chan error, error) {
	cfg := ca.FlowCtx.Cfg
	kvFeedMemMon := mon.NewMonitorInheritWithLimit(
		mon.MakeName("kvFeed"), memLimit, parentMemMon, false, /* longLiving */
	)
	kvFeedMemMon.StartNoReserved(ctx, parentMemMon)

	var bufOpts []kvevent.BlockingBufferOption
	if ca.knobs.MakeKVFeedToAggregatorBufferKnobs != nil {
		bufOpts = append(bufOpts,
			kvevent.WithBlockingBufferTestingKnobs(ca.knobs.MakeKVFeedToAggregatorBufferKnobs()))
	}
	buf := kvevent.NewThrottlingBuffer(
		kvevent.NewMemBuffer(kvFeedMemMon.MakeBoundAccount(), &cfg.Settings.SV,
			&ca.metrics.KVFeedMetrics.AggregatorBufferMetrics, bufOpts...),
		cdcutils.NodeLevelThrottler(&cfg.Settings.SV, &ca.metrics.ThrottleMetrics))

	sink := &changefeedSink{writer: buf}

	schemaChange, err := config.Opts.GetSchemaChangeHandlingOptions()
	if err != nil {
		kvFeedMemMon.Stop(ctx)
		return nil, nil, nil, err
	}
	filters := config.Opts.GetFilters()

	initialScanOnly := config.EndTime == initialHighWater
	var sf schemafeed.SchemaFeed
	if schemaChange.Policy == changefeedbase.OptSchemaChangePolicyIgnore || initialScanOnly {
		sf = schemafeed.DoNothingSchemaFeed
	} else {
		sf = schemafeed.New(ctx, cfg, schemaChange.EventClass, ca.targets,
			initialHighWater, &ca.metrics.SchemaFeedMetrics, config.Opts.GetCanHandle(),
			isDBLevelChangefeed(ca.spec.Feed))
	}

	var initialSpanTimePairs []kvcoord.SpanTimePair
	for sp, ts := range ca.frontier.Entries() {
		initialSpanTimePairs = append(initialSpanTimePairs, kvcoord.SpanTimePair{
			Span:       sp,
			StartAfter: ts,
		})
	}

	execCfg := cfg
	sqlExecCfg := cfg.ExecutorConfig.(*sql.ExecutorConfig)

	errCh := make(chan error, 1)
	doneCh := make(chan struct{})
	if err := ca.FlowCtx.Stopper().RunAsyncTask(ctx, "changefeed-poller", func(ctx context.Context) {
		defer close(doneCh)
		defer kvFeedMemMon.Stop(ctx)
		kvFeedErr := runKVFeed(ctx, kvFeedConfig{
			sink:                 sink,
			spans:                spans,
			initialHighWater:     initialHighWater,
			initialSpanTimePairs: initialSpanTimePairs,
			needsInitialScan:     needsInitialScan,
			endTime:              config.EndTime,
			withDiff:             filters.WithDiff,
			withFiltering:        filters.WithFiltering,
			schemaFeed:           sf,
			schemaChangePolicy:   schemaChange.Policy,
			targets:              ca.targets,
			execCfg:              execCfg,
			rangeFeedFactory:     sqlExecCfg.RangeFeedFactory,
			db:                   sqlExecCfg.DB,
			codec:                execCfg.Codec,
			jobID:                ca.spec.JobID,
			mon:                  kvFeedMemMon,
			knobs:                ca.knobs.FeedKnobs,
		})

		// schemaChangeDetectedError and errChangefeedCompleted are internal
		// sentinels. For these cases, the kvfeed has already emitted
		// RESTART/EXIT resolved spans that the changeFrontier must process.
		// We must wait for the changeFrontier to see those spans and cancel
		// the context (via MoveToDraining) BEFORE closing the buffer. If we
		// close the buffer first, tick() sees ErrBufferClosed and the
		// changeAggregator drains before the changeFrontier processes the
		// boundary. This matches the old kvfeed's Run() behavior:
		// drain → close → wait for ctx.Done().
		var scErr schemaChangeDetectedError
		isInternalErr := errors.Is(kvFeedErr, errChangefeedCompleted) || errors.As(kvFeedErr, &scErr)

		if isInternalErr {
			// Wait for the changeFrontier to process the boundary resolved
			// spans and cancel the context. We must NOT close the buffer
			// before this — if we do, tick() sees ErrBufferClosed and the
			// changeAggregator drains before the changeFrontier processes
			// the RESTART/EXIT boundary. The changeFrontier's AtBoundary
			// check fires after processing the boundary resolved span,
			// which triggers MoveToDraining and context cancellation.
			<-ctx.Done()
			// Now close the buffer to release memory.
			if closeErr := buf.CloseWithReason(ctx, kvevent.ErrNormalRestartReason); closeErr != nil {
				kvFeedErr = errors.CombineErrors(kvFeedErr, closeErr)
			}
			errCh <- ctx.Err()
		} else {
			// Real error — close the buffer with the error as the reason.
			closeReason := kvFeedErr
			if closeReason == nil {
				closeReason = kvevent.ErrNormalRestartReason
			}
			if drainErr := buf.Drain(ctx); drainErr != nil {
				kvFeedErr = errors.CombineErrors(kvFeedErr, drainErr)
			}
			if closeErr := buf.CloseWithReason(ctx, closeReason); closeErr != nil {
				kvFeedErr = errors.CombineErrors(kvFeedErr, closeErr)
			}
			errCh <- kvFeedErr
		}
	}); err != nil {
		kvFeedMemMon.Stop(ctx)
		return nil, nil, nil, err
	}

	return buf, doneCh, errCh, nil
}

// kvFeedConfig holds the configuration for runKVFeed.
type kvFeedConfig struct {
	sink                 *changefeedSink
	spans                []roachpb.Span
	initialHighWater     hlc.Timestamp
	initialSpanTimePairs []kvcoord.SpanTimePair
	needsInitialScan     bool
	endTime              hlc.Timestamp
	withDiff             bool
	withFiltering        bool
	schemaFeed           schemafeed.SchemaFeed
	schemaChangePolicy   changefeedbase.SchemaChangePolicy
	targets              changefeedbase.Targets
	execCfg              *execinfra.ServerConfig
	rangeFeedFactory     *rangefeed.Factory
	db                   *kv.DB
	codec                keys.SQLCodec
	jobID                jobspb.JobID
	mon                  *mon.BytesMonitor
	knobs                KVFeedTestingKnobs
}

// changefeedHandler implements repstream.RangefeedHandler for changefeeds.
// It dispatches rangefeed events through an EventSink and handles CDC-specific
// concerns: schema change detection on frontier advance, end-time checks,
// checkpoint emission, and testing knobs.
//
// The handler is used as the adapter field when wiring rangefeed callbacks,
// following the same pattern as replication's eventStream. This allows an
// OrderedStreamHandler to be interposed later for ordered delivery.
type changefeedHandler struct {
	// sink receives processed rangefeed events via the EventSink interface,
	// matching the pattern used by replication's eventStream.
	sink repstream.EventSink
	// onCheckpoint is called for each rangefeed checkpoint to emit a resolved
	// span event. This is CDC-specific (resolved span emission is not part of
	// the EventSink interface) and is wired to changefeedSink.emitResolvedSpan.
	onCheckpoint func(ctx context.Context, span roachpb.Span, ts hlc.Timestamp) error
	schemaFeed   schemafeed.SchemaFeed
	endTime      hlc.Timestamp
	knobs        KVFeedTestingKnobs
	errCh        chan error

	// stopped is set when SetErr records a boundary error (schema change or
	// end-time). Once set, subsequent OnValue/OnCheckpoint calls are skipped
	// to prevent delivering events past the boundary. This matches the old
	// copyFromSourceToDestUntilTableEvent which dropped KVs at or after the
	// boundary timestamp.
	stopped atomic.Bool
}

var _ repstream.RangefeedHandler = (*changefeedHandler)(nil)

// SetErr implements repstream.RangefeedHandler.
func (h *changefeedHandler) SetErr(err error) bool {
	if err == nil {
		return false
	}
	h.stopped.Store(true)
	select {
	case h.errCh <- err:
	default:
	}
	return true
}

// OnValue implements repstream.RangefeedHandler.
func (h *changefeedHandler) OnValue(ctx context.Context, value *kvpb.RangeFeedValue) {
	if h.stopped.Load() {
		return
	}
	if h.knobs.OnRangeFeedValue != nil {
		if h.SetErr(h.knobs.OnRangeFeedValue()) {
			return
		}
	}
	h.SetErr(h.sink.OnKV(ctx, streampb.StreamEvent_KV{
		KeyValue:  roachpb.KeyValue{Key: value.Key, Value: value.Value},
		PrevValue: value.PrevValue,
	}))
}

// OnValues implements repstream.RangefeedHandler.
func (h *changefeedHandler) OnValues(ctx context.Context, values []kvpb.RangeFeedValue) {
	for i := range values {
		if h.stopped.Load() {
			return
		}
		h.OnValue(ctx, &values[i])
	}
}

// OnFrontier implements repstream.RangefeedHandler. In the unordered path this
// is called directly by the rangefeed on frontier advance; it delegates to
// OnFrontierAdvance. When an OrderedStreamHandler is interposed, the ordered
// adapter's OnFrontier flushes buffered events first, then calls
// OnFrontierAdvance — so the schema change and end-time checks always run
// after all events up to resolvedTS have been delivered.
func (h *changefeedHandler) OnFrontier(ctx context.Context, resolvedTS hlc.Timestamp) {
	h.OnFrontierAdvance(ctx, resolvedTS)
}

// OnCheckpoint implements repstream.RangefeedHandler.
func (h *changefeedHandler) OnCheckpoint(
	ctx context.Context, checkpoint *kvpb.RangeFeedCheckpoint,
) {
	if h.stopped.Load() {
		return
	}
	if h.knobs.ShouldSkipCheckpoint != nil && h.knobs.ShouldSkipCheckpoint(checkpoint) {
		return
	}
	if h.onCheckpoint != nil {
		h.SetErr(h.onCheckpoint(ctx, checkpoint.Span, checkpoint.ResolvedTS))
	}
}

// OnSSTable implements repstream.RangefeedHandler.
func (h *changefeedHandler) OnSSTable(
	ctx context.Context, sst *kvpb.RangeFeedSSTable, registeredSpan roachpb.Span,
) {
	h.SetErr(h.sink.OnSST(ctx, *sst))
}

// OnDeleteRange implements repstream.RangefeedHandler.
func (h *changefeedHandler) OnDeleteRange(
	ctx context.Context, delRange *kvpb.RangeFeedDeleteRange,
) {
	h.SetErr(h.sink.OnDelRange(ctx, *delRange))
}

// OnMetadata implements repstream.RangefeedHandler.
func (h *changefeedHandler) OnMetadata(_ context.Context, _ *kvpb.RangeFeedMetadata) {}

// OnInitialScanDone implements repstream.RangefeedHandler.
func (h *changefeedHandler) OnInitialScanDone(_ context.Context) {}

// OnFrontierAdvance implements repstream.RangefeedHandler. Contains the actual
// schema change boundary detection and end-time checks. Called directly by
// OnFrontier in the unordered path. When an OrderedStreamHandler is interposed,
// it is called after the ordered adapter flushes all buffered events up to
// resolvedTS, ensuring schema checks happen with correct event ordering.
func (h *changefeedHandler) OnFrontierAdvance(ctx context.Context, resolvedTS hlc.Timestamp) {
	if h.schemaFeed != nil && h.schemaFeed != schemafeed.DoNothingSchemaFeed {
		events, err := h.schemaFeed.Peek(ctx, resolvedTS.Next())
		if err != nil {
			h.SetErr(err)
			return
		}
		if len(events) > 0 {
			h.SetErr(&schemaChangeDetectedError{ts: resolvedTS})
			return
		}
	}
	endTimeReached := !h.endTime.IsEmpty() && h.endTime.LessEq(resolvedTS)
	if !endTimeReached && h.knobs.EndTimeReached != nil {
		endTimeReached = h.knobs.EndTimeReached()
	}
	if endTimeReached {
		h.SetErr(&errEndTimeReached{ts: resolvedTS})
	}
}

var errChangefeedCompleted = errors.New("changefeed completed")

// runKVFeed sets up a rangefeed, wires callbacks to the changefeedSink,
// and handles the scan/rangefeed/schema-change loop.
func runKVFeed(ctx context.Context, c kvFeedConfig) error {
	log.Changefeed.Infof(ctx, "kv feed starting")

	// Build the resume frontier from initial span-time pairs.
	frontier, err := span.MakeFrontier(c.spans...)
	if err != nil {
		return err
	}
	frontier = span.MakeConcurrentFrontier(frontier)
	defer frontier.Release()
	for _, stp := range c.initialSpanTimePairs {
		if _, err := frontier.Forward(stp.Span, stp.StartAfter); err != nil {
			return err
		}
	}

	// Start schema feed if needed. The schema feed polls system.descriptors
	// for table changes. If it fails, the kvfeed must also stop.
	var schemaFeedDone chan error
	if c.schemaFeed != schemafeed.DoNothingSchemaFeed {
		schemaFeedDone = make(chan error, 1)
		if err := c.execCfg.Stopper.RunAsyncTask(ctx, "changefeed-schema-feed", func(ctx context.Context) {
			schemaFeedDone <- c.schemaFeed.Run(ctx)
		}); err != nil {
			return err
		}
	}

	return runKVFeedLoop(ctx, c, frontier, schemaFeedDone)
}

// runKVFeedLoop is the main scan/rangefeed/schema-change loop.
// schemaFeedDone, if non-nil, is monitored for schema feed failures.
func runKVFeedLoop(
	ctx context.Context,
	c kvFeedConfig,
	frontier span.Frontier,
	schemaFeedDone chan error,
) error {
	emitResolved := func(ts hlc.Timestamp, boundary jobspb.ResolvedSpan_BoundaryType) error {
		for _, sp := range c.spans {
			if err := c.sink.emitResolvedSpan(ctx, sp, ts, boundary); err != nil {
				return err
			}
		}
		return nil
	}

	initialScanOnly := c.endTime == c.initialHighWater
	initialTimestamp := c.initialHighWater

	// Main loop: scan → rangefeed → schema change → repeat.
	for i := 0; ; i++ {
		if i == 0 && c.needsInitialScan {
			// Bug 4 fix: initial scan never requests diffs, matching old behavior.
			if err := runRangefeedWithScan(ctx, c, frontier, initialTimestamp, false /* withDiff */); err != nil {
				return err
			}
			// Advance frontier for all spans to the initial timestamp.
			for _, sp := range c.spans {
				if _, err := frontier.Forward(sp, initialTimestamp); err != nil {
					return err
				}
			}
			if initialScanOnly {
				if err := emitResolved(c.initialHighWater, jobspb.ResolvedSpan_EXIT); err != nil {
					return err
				}
				return errChangefeedCompleted
			}
		}

		// Run rangefeed until schema change boundary or end time.
		if err := runRangefeedUntilBoundary(ctx, c, frontier, schemaFeedDone); err != nil {
			var endTimeErr *errEndTimeReached
			if errors.As(err, &endTimeErr) {
				// Emit EXIT at endTime.Prev(), matching the old kvfeed which
				// clamped the frontier to boundary.Timestamp().Prev() before
				// emitting. This ensures the changefeed checkpoint reflects the
				// last timestamp before the end time, not the end time itself.
				if err := emitResolved(endTimeErr.ts.Prev(), jobspb.ResolvedSpan_EXIT); err != nil {
					return err
				}
				return errChangefeedCompleted
			}
			var schemaErr *schemaChangeDetectedError
			if !errors.As(err, &schemaErr) {
				return err
			}
			// Schema change detected — handle boundary.
		}

		boundaryTS := frontier.Frontier()
		schemaChangeTS := boundaryTS.Next()
		events, err := c.schemaFeed.Peek(ctx, schemaChangeTS)
		if err != nil {
			return err
		}

		boundaryType := jobspb.ResolvedSpan_BACKFILL
		primaryIndexChange, noColumnChanges := isPrimaryKeyChange(events, c.targets)
		if primaryIndexChange && (noColumnChanges ||
			c.schemaChangePolicy != changefeedbase.OptSchemaChangePolicyStop) {
			boundaryType = jobspb.ResolvedSpan_RESTART
		} else if c.schemaChangePolicy == changefeedbase.OptSchemaChangePolicyStop {
			boundaryType = jobspb.ResolvedSpan_EXIT
		}

		if c.schemaChangePolicy != changefeedbase.OptSchemaChangePolicyNoBackfill ||
			boundaryType == jobspb.ResolvedSpan_RESTART {
			if err := emitResolved(boundaryTS, boundaryType); err != nil {
				return err
			}
		}

		// Bug 1 fix: Always consume schema change events regardless of boundary
		// type, matching the old kvfeed's scanIfShould which called Pop before
		// deciding the boundary type. For RESTART/EXIT boundaries the kvfeed
		// returns an error that causes the job to restart; leaving unconsumed
		// events would cause stale detections on the next run.
		if _, err := c.schemaFeed.Pop(ctx, schemaChangeTS); err != nil {
			return err
		}

		if boundaryType == jobspb.ResolvedSpan_RESTART || boundaryType == jobspb.ResolvedSpan_EXIT {
			return schemaChangeDetectedError{ts: schemaChangeTS}
		}

		// BACKFILL boundary: scan the affected tables at the schema change
		// timestamp so downstream decoders use the correct schema version.
		if c.schemaChangePolicy != changefeedbase.OptSchemaChangePolicyNoBackfill {
			spansToScan := backfillSpansForEvents(events, c.codec, c.spans)
			if len(spansToScan) > 0 {
				// Filter out spans already at or past the scan timestamp.
				var spansToBackfill roachpb.SpanGroup
				spansToBackfill.Add(spansToScan...)
				for sp, ts := range frontier.Entries() {
					if schemaChangeTS.LessEq(ts) {
						spansToBackfill.Sub(sp)
					}
				}

				if spansToBackfill.Len() > 0 {
					c.sink.backfillTimestamp = schemaChangeTS
					if err := backfillScan(ctx, c, spansToBackfill.Slice(), schemaChangeTS); err != nil {
						c.sink.backfillTimestamp = hlc.Timestamp{}
						return err
					}
					c.sink.backfillTimestamp = hlc.Timestamp{}
				}

				// Advance frontier for scanned spans.
				for _, sp := range spansToScan {
					if _, err := frontier.Forward(sp, schemaChangeTS); err != nil {
						return err
					}
				}
			}
		}
	}
}

type errEndTimeReached struct {
	ts hlc.Timestamp
}

func (e *errEndTimeReached) Error() string {
	return fmt.Sprintf("end time %s reached", e.ts)
}

// quantizeTS rounds the walltime of ts down to the nearest multiple of
// granularity. If granularity is zero, ts is returned unchanged.
func quantizeTS(ts hlc.Timestamp, granularity time.Duration) hlc.Timestamp {
	if granularity == 0 {
		return ts
	}
	return hlc.Timestamp{
		WallTime: ts.WallTime - ts.WallTime%int64(granularity),
		Logical:  0,
	}
}

type schemaChangeDetectedError struct {
	ts hlc.Timestamp
}

func (e schemaChangeDetectedError) Error() string {
	return fmt.Sprintf("schema change detected at %s", e.ts)
}

// runRangefeedUntilBoundary starts a rangefeed and runs until a schema change
// boundary is detected or the end time is reached.
func runRangefeedUntilBoundary(
	ctx context.Context,
	c kvFeedConfig,
	frontier span.Frontier,
	schemaFeedDone chan error,
) error {
	errCh := make(chan error, 1)

	// Bug 5 fix: filter checkpoints against the current frontier (not
	// initialHighWater). The old physical_kv_feed.go dropped checkpoints
	// below cfg.Frontier, which tracks the rangefeed's starting position
	// and advances as the frontier moves.
	startFrontier := frontier.Frontier()
	quantizeDuration := changefeedbase.Quantize.Get(&c.execCfg.Settings.SV)
	handler := &changefeedHandler{
		sink: c.sink,
		onCheckpoint: func(ctx context.Context, sp roachpb.Span, ts hlc.Timestamp) error {
			// The rangefeed library quantizes its internal frontier but does
			// not modify the checkpoint event passed to this callback. Apply
			// the same quantization here so resolved timestamps written to
			// the buffer match the old kvfeed behavior.
			ts = quantizeTS(ts, quantizeDuration)
			if ts.IsEmpty() || ts.Less(startFrontier) {
				return nil
			}
			return c.sink.emitResolvedSpan(ctx, sp, ts, jobspb.ResolvedSpan_NONE)
		},
		schemaFeed: c.schemaFeed,
		endTime:    c.endTime,
		knobs:      c.knobs,
		errCh:      errCh,
	}

	// adapter is the RangefeedHandler that receives rangefeed callbacks.
	// Today this is the handler directly; an OrderedStreamHandler can be
	// interposed here for ordered delivery.
	var adapter repstream.RangefeedHandler = handler

	opts := []rangefeed.Option{
		rangefeed.WithPProfLabel("job", fmt.Sprintf("id=%d", c.jobID)),
		rangefeed.WithMemoryMonitor(c.mon),
		rangefeed.WithOnFrontierAdvance(adapter.OnFrontier),
		rangefeed.WithOnCheckpoint(adapter.OnCheckpoint),
		rangefeed.WithOnInternalError(func(ctx context.Context, err error) {
			handler.SetErr(err)
		}),
		rangefeed.WithOnValues(adapter.OnValues),
		rangefeed.WithOnSSTable(adapter.OnSSTable),
		rangefeed.WithOnDeleteRange(adapter.OnDeleteRange),
		rangefeed.WithFrontierQuantized(changefeedbase.Quantize.Get(&c.execCfg.Settings.SV)),
		rangefeed.WithDiff(c.withDiff),
		rangefeed.WithConsumerID(int64(c.jobID)),
		rangefeed.WithFiltering(c.withFiltering),
	}
	if c.knobs.BeforeScanRequest != nil {
		opts = append(opts, rangefeed.WithBeforeScanRequest(c.knobs.BeforeScanRequest))
	}
	if len(c.knobs.RangefeedOptions) > 0 {
		opts = append(opts, rangefeed.WithExtraRangeFeedOptions(c.knobs.RangefeedOptions...))
	}

	rf := c.rangeFeedFactory.New(
		fmt.Sprintf("changefeed-kvfeed-jobID=%d", c.jobID),
		frontier.Frontier(),
		adapter.OnValue,
		opts...,
	)

	if err := rf.StartFromFrontier(ctx, frontier); err != nil {
		return err
	}
	defer rf.Close()

	if c.knobs.OnRangeFeedStart != nil {
		c.knobs.OnRangeFeedStart(c.initialSpanTimePairs)
	}

	// Monitor the rangefeed error channel, the schema feed, and the context.
	// If the schema feed fails, the rangefeed must stop too.
	if schemaFeedDone == nil {
		schemaFeedDone = make(chan error) // will never fire
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case err := <-schemaFeedDone:
		// Schema feed exited. If it failed, return the error; otherwise
		// the context was cancelled and we'll pick that up.
		if err != nil {
			return err
		}
		return ctx.Err()
	}
}

// runRangefeedWithScan runs a rangefeed with initial scan.
func runRangefeedWithScan(
	ctx context.Context,
	c kvFeedConfig,
	frontier span.Frontier,
	initialTimestamp hlc.Timestamp,
	withDiff bool,
) error {
	errCh := make(chan error, 1)
	scanDone := make(chan struct{})

	handler := &changefeedHandler{
		sink:  c.sink,
		knobs: c.knobs,
		errCh: errCh,
	}
	// No onCheckpoint needed for initial scan — checkpoints during the scan
	// would have timestamps below the initial high water.

	var adapter repstream.RangefeedHandler = handler

	// Bug 4 fix: use the withDiff parameter instead of c.withDiff so
	// callers can control whether diffs are requested (initial scans
	// never request diffs, matching the old kvfeed behavior).
	opts := []rangefeed.Option{
		rangefeed.WithPProfLabel("job", fmt.Sprintf("id=%d", c.jobID)),
		rangefeed.WithMemoryMonitor(c.mon),
		rangefeed.WithInitialScan(func(ctx context.Context) {
			close(scanDone)
		}),
		rangefeed.WithRowTimestampInInitialScan(true),
		rangefeed.WithOnInternalError(func(ctx context.Context, err error) {
			handler.SetErr(err)
		}),
		rangefeed.WithOnValues(adapter.OnValues),
		rangefeed.WithDiff(withDiff),
		rangefeed.WithConsumerID(int64(c.jobID)),
		rangefeed.WithFiltering(c.withFiltering),
	}

	rf := c.rangeFeedFactory.New(
		fmt.Sprintf("changefeed-scan-jobID=%d", c.jobID),
		initialTimestamp,
		adapter.OnValue,
		opts...,
	)

	if err := rf.StartFromFrontier(ctx, frontier); err != nil {
		return err
	}
	defer rf.Close()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-scanDone:
		return nil
	}
}

// addSST processes an SSTable by scanning its contents and writing individual
// KVs through the sink. Uses repstream.ScanSST for the SST iteration.
func (s *changefeedSink) addSST(
	ctx context.Context, sst kvpb.RangeFeedSSTable, registeredSpan roachpb.Span,
) error {
	_ = registeredSpan
	// Changefeeds should not receive SST events. If they do, return an error.
	return errors.AssertionFailedf("unexpected SST ingestion in changefeed: %v", sst.Span)
}

// isPrimaryKeyChange checks if any of the schema change events correspond to
// a primary index change.
func isPrimaryKeyChange(
	events []schemafeed.TableEvent, targets changefeedbase.Targets,
) (isPrimaryIndexChange, hasNoColumnChanges bool) {
	hasNoColumnChanges = true
	for _, ev := range events {
		if ok, noColumnChange := schemafeed.IsPrimaryIndexChange(ev, targets); ok {
			isPrimaryIndexChange = true
			hasNoColumnChanges = hasNoColumnChanges && noColumnChange
		}
	}
	return isPrimaryIndexChange, isPrimaryIndexChange && hasNoColumnChanges
}

// backfillSpansForEvents returns the subset of watched spans that overlap with
// tables affected by the given schema change events. Primary-index-only changes
// are excluded since they don't require a backfill.
func backfillSpansForEvents(
	events []schemafeed.TableEvent, codec keys.SQLCodec, watchedSpans []roachpb.Span,
) []roachpb.Span {
	var spans []roachpb.Span
	for _, ev := range events {
		if schemafeed.IsOnlyPrimaryIndexChange(ev) {
			continue
		}
		tablePrefix := codec.TablePrefix(uint32(ev.After.GetID()))
		tableSpan := roachpb.Span{Key: tablePrefix, EndKey: tablePrefix.PrefixEnd()}
		for _, sp := range watchedSpans {
			if tableSpan.Overlaps(sp) {
				spans = append(spans, sp)
			}
		}
	}
	return spans
}

// backfillScan performs a targeted KV scan of the given spans at the specified
// timestamp. Each scanned KV is written to the changefeedSink as a backfill
// event (with the backfill timestamp set on the sink). The scan uses parallel
// requests split on range boundaries for efficiency.
func backfillScan(
	ctx context.Context, c kvFeedConfig, spans []roachpb.Span, scanTS hlc.Timestamp,
) error {
	log.Changefeed.Infof(ctx, "starting backfill scan of %d spans at %s", len(spans), scanTS)

	sender := c.db.NonTransactionalSender()
	distSender := sender.(*kv.CrossRangeTxnWrapperSender).Wrapped().(*kvcoord.DistSender)
	rangeSpans, numNodesHint, err := distSender.AllRangeSpans(ctx, spans)
	if err != nil {
		return err
	}

	// Intersect range spans with the requested spans so we only scan what's needed.
	var requests []roachpb.Span
	for _, rs := range rangeSpans {
		for _, sp := range spans {
			if inter := sp.Intersect(rs); inter.Valid() {
				requests = append(requests, inter)
			}
		}
	}

	maxConcurrent := 3 * numNodesHint
	if maxConcurrent > 100 {
		maxConcurrent = 100
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	if userMax := changefeedbase.ScanRequestLimit.Get(&c.execCfg.Settings.SV); userMax > 0 {
		maxConcurrent = int(userMax)
	}
	exportLim := limit.MakeConcurrentRequestLimiter("changefeedBackfillLimiter", maxConcurrent)

	g := ctxgroup.WithContext(ctx)
	for _, req := range requests {
		req := req
		limAlloc, err := exportLim.Begin(ctx)
		if err != nil {
			return errors.CombineErrors(err, g.Wait())
		}
		g.GoCtx(func(ctx context.Context) error {
			defer limAlloc.Release()
			return exportSpan(ctx, c, req, scanTS)
		})
	}
	return g.Wait()
}

// exportSpan scans a single span at the given timestamp, writing each KV as a
// backfill event to the sink. The sink's backfillTimestamp must already be set.
func exportSpan(
	ctx context.Context, c kvFeedConfig, sp roachpb.Span, scanTS hlc.Timestamp,
) error {
	txn := c.db.NewTxn(ctx, "changefeed backfill")
	if err := txn.SetFixedTimestamp(ctx, scanTS); err != nil {
		return err
	}
	targetBytes := changefeedbase.ScanRequestSize.Get(&c.execCfg.Settings.SV)
	for remaining := &sp; remaining != nil; {
		start := timeutil.Now()
		b := txn.NewBatch()
		r := kvpb.NewScan(remaining.Key, remaining.EndKey).(*kvpb.ScanRequest)
		r.ScanFormat = kvpb.BATCH_RESPONSE
		b.Header.TargetBytes = targetBytes
		b.Header.ConnectionClass = rpcbase.RangefeedClass
		b.AdmissionHeader = kvpb.AdmissionHeader{
			Priority:                 int32(admissionpb.BulkNormalPri),
			CreateTime:               start.UnixNano(),
			Source:                   kvpb.AdmissionHeader_FROM_SQL,
			NoMemoryReservedAtSource: true,
		}
		b.AddRawRequest(r)
		if c.knobs.BeforeScanRequest != nil {
			if err := c.knobs.BeforeScanRequest(b); err != nil {
				return err
			}
		}
		if err := txn.Run(ctx, b); err != nil {
			return errors.Wrapf(err, "fetching changes for %s", sp)
		}

		res := b.RawResponse().Responses[0].GetScan()
		if err := slurpScanResponse(ctx, c.sink, res, scanTS, c.withDiff, *remaining); err != nil {
			return err
		}
		if res.ResumeSpan != nil {
			consumed := roachpb.Span{Key: remaining.Key, EndKey: res.ResumeSpan.Key}
			if err := c.sink.emitResolvedSpan(ctx, consumed, scanTS,
				jobspb.ResolvedSpan_NONE); err != nil {
				return err
			}
		}
		remaining = res.ResumeSpan
	}
	return c.sink.emitResolvedSpan(ctx, sp, scanTS, jobspb.ResolvedSpan_NONE)
}

// slurpScanResponse iterates a ScanResponse and writes each KV as a backfill
// event to the sink. The sink's backfillTimestamp determines the schema version
// used for downstream decoding.
func slurpScanResponse(
	ctx context.Context,
	sink *changefeedSink,
	res *kvpb.ScanResponse,
	backfillTS hlc.Timestamp,
	withDiff bool,
	sp roachpb.Span,
) error {
	for _, br := range res.BatchResponses {
		for len(br) > 0 {
			var keyBytes, valBytes []byte
			var ts hlc.Timestamp
			var err error
			keyBytes, ts, valBytes, br, err = enginepb.ScanDecodeKeyValue(br)
			if err != nil {
				return errors.Wrapf(err, "decoding changes for %s", sp)
			}
			ev := kvevent.NewBackfillKVEvent(keyBytes, ts, valBytes, withDiff, backfillTS)
			if err := sink.writer.Add(ctx, ev); err != nil {
				return errors.Wrapf(err, "buffering changes for %s", sp)
			}
		}
	}
	return nil
}
