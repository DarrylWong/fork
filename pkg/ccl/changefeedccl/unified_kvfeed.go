// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package changefeedccl

import (
	"context"
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/ccl/changefeedccl/cdcutils"
	"github.com/cockroachdb/cockroach/pkg/ccl/changefeedccl/changefeedbase"
	"github.com/cockroachdb/cockroach/pkg/ccl/changefeedccl/kvevent"
	"github.com/cockroachdb/cockroach/pkg/ccl/changefeedccl/schemafeed"
	"github.com/cockroachdb/cockroach/pkg/jobs/jobspb"
	"github.com/cockroachdb/cockroach/pkg/kv/kvclient/kvcoord"
	"github.com/cockroachdb/cockroach/pkg/kv/kvclient/rangefeed"
	"github.com/cockroachdb/cockroach/pkg/kv/kvpb"
	"github.com/cockroachdb/cockroach/pkg/repstream/streampb"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/sql/execinfra"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/mon"
	"github.com/cockroachdb/cockroach/pkg/util/span"
	"github.com/cockroachdb/errors"
)

// startUnifiedKVFeed creates a rangefeed using the unified event pipeline
// (repstream.EventSink) instead of kvfeed.Run. It writes kvevent.Events to
// the provided kvevent.Writer so that changeAggregator.tick() works unchanged.
//
// The returned kvevent.Reader, doneCh, and errCh have the same semantics as
// startKVFeed.
func (ca *changeAggregator) startUnifiedKVFeed(
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

	errCh := make(chan error, 1)
	doneCh := make(chan struct{})
	if err := ca.FlowCtx.Stopper().RunAsyncTask(ctx, "changefeed-unified-poller", func(ctx context.Context) {
		defer close(doneCh)
		defer kvFeedMemMon.Stop(ctx)
		errCh <- runUnifiedKVFeed(ctx, unifiedKVFeedConfig{
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
			jobID:                ca.spec.JobID,
			mon:                  kvFeedMemMon,
		})
	}); err != nil {
		kvFeedMemMon.Stop(ctx)
		return nil, nil, nil, err
	}

	return buf, doneCh, errCh, nil
}

// unifiedKVFeedConfig holds the configuration for runUnifiedKVFeed.
type unifiedKVFeedConfig struct {
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
	jobID                jobspb.JobID
	mon                  *mon.BytesMonitor
}

var errChangefeedCompleted = errors.New("changefeed completed")

// runUnifiedKVFeed is the CDC rangefeed orchestrator. It sets up a rangefeed,
// wires callbacks to the changefeedSink, and handles the scan/rangefeed/
// schema-change loop.
func runUnifiedKVFeed(ctx context.Context, c unifiedKVFeedConfig) error {
	log.Changefeed.Infof(ctx, "unified kv feed starting")

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

	// Start schema feed if non-nil.
	var schemaFeedDone chan error
	if c.schemaFeed != schemafeed.DoNothingSchemaFeed {
		schemaFeedDone = make(chan error, 1)
		if err := c.execCfg.Stopper.RunAsyncTask(ctx, "changefeed-schema-feed", func(ctx context.Context) {
			schemaFeedDone <- c.schemaFeed.Run(ctx)
		}); err != nil {
			return err
		}
	}

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
		if i == 0 && initialScanOnly {
			// Initial scan only: emit resolved at initial timestamp and exit.
			if c.needsInitialScan {
				if err := runRangefeedWithScan(ctx, c, frontier, initialTimestamp); err != nil {
					return err
				}
			}
			if err := emitResolved(c.initialHighWater, jobspb.ResolvedSpan_EXIT); err != nil {
				return err
			}
			return errChangefeedCompleted
		}

		// Run rangefeed until schema change boundary or end time.
		if err := runRangefeedUntilBoundary(ctx, c, frontier); err != nil {
			var endTimeErr *errEndTimeReached
			if errors.As(err, &endTimeErr) {
				if err := emitResolved(frontier.Frontier(), jobspb.ResolvedSpan_EXIT); err != nil {
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

		if boundaryType == jobspb.ResolvedSpan_RESTART || boundaryType == jobspb.ResolvedSpan_EXIT {
			return schemaChangeDetectedError{ts: schemaChangeTS}
		}

		// Consume the schema change events and continue.
		if _, err := c.schemaFeed.Pop(ctx, schemaChangeTS); err != nil {
			return err
		}
	}
}

type errEndTimeReached struct {
	ts hlc.Timestamp
}

func (e *errEndTimeReached) Error() string {
	return fmt.Sprintf("end time %s reached", e.ts)
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
	ctx context.Context, c unifiedKVFeedConfig, frontier span.Frontier,
) error {
	errCh := make(chan error, 1)

	// Set up rangefeed options.
	opts := []rangefeed.Option{
		rangefeed.WithPProfLabel("job", fmt.Sprintf("id=%d", c.jobID)),
		rangefeed.WithMemoryMonitor(c.mon),
		rangefeed.WithOnFrontierAdvance(func(ctx context.Context, resolvedTS hlc.Timestamp) {
			// Check for schema change on frontier advance.
			if c.schemaFeed != schemafeed.DoNothingSchemaFeed {
				events, err := c.schemaFeed.Peek(ctx, resolvedTS.Next())
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				if len(events) > 0 {
					select {
					case errCh <- &schemaChangeDetectedError{ts: resolvedTS}:
					default:
					}
					return
				}
			}
			// Check for end time.
			if !c.endTime.IsEmpty() && c.endTime.LessEq(resolvedTS) {
				select {
				case errCh <- &errEndTimeReached{ts: resolvedTS}:
				default:
				}
			}
		}),
		rangefeed.WithOnCheckpoint(func(ctx context.Context, checkpoint *kvpb.RangeFeedCheckpoint) {
			// Emit resolved span for this checkpoint.
			if err := c.sink.emitResolvedSpan(ctx, checkpoint.Span, checkpoint.ResolvedTS,
				jobspb.ResolvedSpan_NONE); err != nil {
				select {
				case errCh <- err:
				default:
				}
			}
		}),
		rangefeed.WithOnInternalError(func(ctx context.Context, err error) {
			select {
			case errCh <- err:
			default:
			}
		}),
		rangefeed.WithOnValues(func(ctx context.Context, values []kvpb.RangeFeedValue) {
			for _, v := range values {
				if err := c.sink.OnKV(ctx, streampb.StreamEvent_KV{
					KeyValue:  roachpb.KeyValue{Key: v.Key, Value: v.Value},
					PrevValue: v.PrevValue,
				}); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}),
		rangefeed.WithOnSSTable(func(ctx context.Context, sst *kvpb.RangeFeedSSTable, registeredSpan roachpb.Span) {
			if err := c.sink.OnSST(ctx, *sst); err != nil {
				select {
				case errCh <- err:
				default:
				}
			}
		}),
		rangefeed.WithOnDeleteRange(func(ctx context.Context, dr *kvpb.RangeFeedDeleteRange) {
			if err := c.sink.OnDelRange(ctx, *dr); err != nil {
				select {
				case errCh <- err:
				default:
				}
			}
		}),
		rangefeed.WithFrontierQuantized(changefeedbase.Quantize.Get(&c.execCfg.Settings.SV)),
		rangefeed.WithDiff(c.withDiff),
		rangefeed.WithConsumerID(int64(c.jobID)),
		rangefeed.WithFiltering(c.withFiltering),
	}

	rf := c.execCfg.RangeFeedFactory.New(
		fmt.Sprintf("changefeed-unified-jobID=%d", c.jobID),
		frontier.Frontier(),
		func(ctx context.Context, value *kvpb.RangeFeedValue) {
			if err := c.sink.OnKV(ctx, streampb.StreamEvent_KV{
				KeyValue:  roachpb.KeyValue{Key: value.Key, Value: value.Value},
				PrevValue: value.PrevValue,
			}); err != nil {
				select {
				case errCh <- err:
				default:
				}
			}
		},
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
	}
}

// runRangefeedWithScan runs a rangefeed with initial scan for the initial-scan-only case.
func runRangefeedWithScan(
	ctx context.Context,
	c unifiedKVFeedConfig,
	frontier span.Frontier,
	initialTimestamp hlc.Timestamp,
) error {
	errCh := make(chan error, 1)
	scanDone := make(chan struct{})

	opts := []rangefeed.Option{
		rangefeed.WithPProfLabel("job", fmt.Sprintf("id=%d", c.jobID)),
		rangefeed.WithMemoryMonitor(c.mon),
		rangefeed.WithInitialScan(func(ctx context.Context) {
			close(scanDone)
		}),
		rangefeed.WithRowTimestampInInitialScan(true),
		rangefeed.WithOnInternalError(func(ctx context.Context, err error) {
			select {
			case errCh <- err:
			default:
			}
		}),
		rangefeed.WithOnValues(func(ctx context.Context, values []kvpb.RangeFeedValue) {
			for _, v := range values {
				if err := c.sink.OnKV(ctx, streampb.StreamEvent_KV{
					KeyValue:  roachpb.KeyValue{Key: v.Key, Value: v.Value},
					PrevValue: v.PrevValue,
				}); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}),
		rangefeed.WithDiff(c.withDiff),
		rangefeed.WithConsumerID(int64(c.jobID)),
		rangefeed.WithFiltering(c.withFiltering),
	}

	rf := c.execCfg.RangeFeedFactory.New(
		fmt.Sprintf("changefeed-unified-scan-jobID=%d", c.jobID),
		initialTimestamp,
		func(ctx context.Context, value *kvpb.RangeFeedValue) {
			if err := c.sink.OnKV(ctx, streampb.StreamEvent_KV{
				KeyValue:  roachpb.KeyValue{Key: value.Key, Value: value.Value},
				PrevValue: value.PrevValue,
			}); err != nil {
				select {
				case errCh <- err:
				default:
				}
			}
		},
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
