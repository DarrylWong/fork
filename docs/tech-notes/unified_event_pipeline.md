# Unified Event Pipeline: Replacing kvfeed and event_stream

## Motivation

Today CockroachDB has two independent implementations that stream KV changes
from rangefeeds:

1. **kvfeed** (`pkg/ccl/changefeedccl/kvfeed/`) — used by changefeeds (CDC)
2. **event_stream** (`pkg/crosscluster/producer/event_stream.go`) — used by
   PCR and LDR

Both do fundamentally the same thing: set up rangefeeds on a set of spans,
consume rangefeed events, and deliver them to a downstream consumer. The
divergence is in consumer-specific behavior: changefeeds need schema-change
detection and backfills; LDR needs origin-ID filtering and may eventually need
schema coordination; PCR needs none of this.

Maintaining two implementations means duplicated bug fixes, divergent feature
sets (e.g., event_stream handles SSTs and DeleteRanges; kvfeed errors/ignores
them), and an inability to share improvements across consumers.

This document proposes replacing both with a single **unified event pipeline**
that uses callbacks to handle consumer-specific behavior.

## Current Architecture

### kvfeed

```
                        ┌──────────────┐
                        │  SchemaFeed  │ (polls system.descriptors)
                        └──────┬───────┘
                               │ Peek/Pop
                        ┌──────▼───────┐
     DistSender         │              │
    .RangeFeed ──────►  │   kvFeed     │ ──► kvevent.Writer ──► changeAggregator
     (channel)          │   .run()     │
                        │              │
                        └──────────────┘
                          scan ◄──► rangefeed
                          (alternating loop)
```

Key characteristics:
- Uses `DistSender.RangeFeed` directly (the low-level API)
- Runs a scan/rangefeed alternating loop: `scanIfShould` → `runUntilTableEvent` → repeat
- Schema changes detected via `schemafeed.SchemaFeed.Peek()` on every resolved event
- SSTable events cause an error; DeleteRange events are silently ignored
- Events buffered through `kvevent.Buffer` (memory-accounted `memBuf`) then
  copied to `kvevent.Writer` via `copyFromSourceToDestUntilTableEvent`
- Synchronous `Run()` that blocks until completion or schema-change boundary

### event_stream

```
                        ┌──────────────────┐
   rangefeed.Factory    │                  │
       .New() ──────►   │  eventStream     │ ──► streamCh ──► pgwire ──► consumer
    (callbacks)         │                  │
                        │  (+ optional     │
                        │   OrderedStream- │
                        │   Handler)       │
                        └──────────────────┘
```

Key characteristics:
- Uses `rangefeed.Factory` (the managed higher-level API)
- No schema change detection; no scan/rangefeed alternation
- SSTable events handled: forwarded whole or scanned into individual KVs
- DeleteRange events forwarded to consumer
- Events batched via `streamEventBatcher`, serialized as protobuf, sent over pgwire
- Optional MVCC-ordered delivery via `OrderedStreamHandler` with disk-backed buffer
- Implements `eval.ValueGenerator` for SQL-level streaming

### What differs between them

| Aspect | kvfeed | event_stream |
|--------|--------|--------------|
| Rangefeed API | `DistSender.RangeFeed` (low-level) | `rangefeed.Factory` (managed) |
| Schema changes | Detected + backfill loop | None |
| SSTable events | Error | Handle (forward or scan) |
| DeleteRange events | Ignore | Forward |
| Event delivery | `kvevent.Writer` (local buffer) | `streamCh` (network, protobuf) |
| Ordering | Per-range only | Optional total (ts, key) ordering |
| Lifecycle | Synchronous `Run()` | Async `ValueGenerator` |

### What is shared

- Both consume the same rangefeed event types (`RangeFeedValue`, `RangeFeedCheckpoint`, etc.)
- Both maintain a span frontier to track progress
- Both support initial scans, frontier-based resumption, filtering, and diffs
- Both need to handle backpressure from slow consumers

## Proposed Design

### Core Idea

The `RangefeedHandler` methods on `eventStream` (`OnValue`, `OnSSTable`,
`OnDeleteRange`, etc.) are shared event-handling logic. What differs
between consumers is where processed events are delivered:

- **PCR/LDR**: batch via `streamEventBatcher`, serialize to protobuf,
  compress, send over `streamCh` → pgwire to a remote consumer.
- **CDC**: write `kvevent.Event`s to a `kvevent.Writer` (the existing
  changefeed buffer), consumed locally by `changeAggregator.tick()`.

The fix: extract the delivery mechanism into an `eventSink` interface.
`eventStream` keeps all handler logic (`OnValue`, `OnSSTable`, etc.)
and delegates delivery to the sink. For replication, the sink is the
existing batching + serialization + `streamCh` path. For changefeeds,
the sink writes `kvevent.Event`s to a `kvevent.Writer`.

```
Before:
  eventStream.OnValue → seb.addKV() → maybeFlushBatch() → protobuf → streamCh
  (hardcoded replication delivery)

After:
  eventStream.OnValue → sink.OnKV() → sink.MaybeFlush()
  where sink is:
    replicationSink  → seb.addKV()  → protobuf → streamCh  (PCR/LDR)
    changefeedSink   → kvevent.Writer.Add()                 (CDC)
```

`eventStream` keeps: rangefeed setup, frontier, `errCh`, callback
wiring, `Start()`/`Close()`, handler methods (`OnValue`, `OnSSTable`,
etc.), and (for replication) `ValueGenerator` interface + `streamCh` +
`Next()`/`Values()`.

### Package Location

The event stream infrastructure moves from `pkg/crosscluster/producer/`
to `pkg/repstream/`. This avoids changefeeds importing a `crosscluster`
package, which is semantically wrong since CDC is single-cluster.

**`pkg/repstream/`** — generic event stream infrastructure:
- `EventSink` interface
- `eventStream` (rangefeed orchestration, frontier, handler dispatch)
- `RangefeedHandler` interface + `OrderedStreamHandler`
- `OrderedBuffer` (disk-backed sorted buffer)
- `streamEventBatcher`
- `ScanSST` helper (moved from `replicationutils`)
- `StreamReplicationMinCheckpointFrequency` setting (moved from
  `crosscluster`)

**`pkg/crosscluster/producer/`** — PCR/LDR-specific code:
- `replicationSink` (batching + protobuf + pgwire delivery)
- `streamPartition()` (replication-specific constructor)
- `producer_job.go`, `replication_manager.go`, `stream_lifetime.go`
- `span_config_event_stream.go`

**`pkg/ccl/changefeedccl/`** — CDC-specific code:
- `changefeedSink` (writes `kvevent.Event` to `kvevent.Writer`)

Both `crosscluster/producer` and `changefeedccl` import `repstream`
for the shared infrastructure. Neither imports the other.

### eventSink Interface

```go
// eventSink abstracts the delivery mechanism for processed rangefeed
// events. eventStream's handler methods (OnValue, OnSSTable, etc.)
// call sink methods instead of directly interacting with
// streamEventBatcher / streamCh.
type eventSink interface {
    OnKV(ctx context.Context, kv streampb.StreamEvent_KV) error
    OnSST(ctx context.Context, sst kvpb.RangeFeedSSTable) error
    OnDelRange(ctx context.Context, dr kvpb.RangeFeedDeleteRange) error
    OnSplitPoint(ctx context.Context, key roachpb.Key) error
    MaybeFlush(ctx context.Context) error
    Flush(ctx context.Context) error
    Close(ctx context.Context) error
}
```

### RangefeedHandler — already exported (done)

The `RangefeedHandler` interface was exported in the first step of
PR 1. `eventStream` still implements it directly — this is unchanged
by the sink abstraction. The handler methods stay on `eventStream`;
they just call through to `eventSink` instead of hardcoding the
replication delivery path.

### Sink Implementations

#### replicationSink (PCR/LDR)

Mechanical extraction of existing code from `eventStream`. Contains
`streamEventBatcher`, batching thresholds, `seqNum`, and references
to `eventStream` for `sendFlush`/`streamCh` access:

- `OnKV` → `seb.addKV()`
- `OnSST` → `seb.addSST()`
- `OnDelRange` → `seb.addDelRange()`
- `OnSplitPoint` → `seb.addSplitPoint()`
- `MaybeFlush` → size-based flush check (current `maybeFlushBatch`)
- `Flush` → serialize batch to protobuf, compress, send over `streamCh`

This is the existing code, just moved behind the interface.

#### changefeedSink (CDC)

Writes `kvevent.Event`s directly to a `kvevent.Writer`, the existing
changefeed buffer. No batching, no protobuf, no `streamCh`:

```go
type changefeedSink struct {
    writer           kvevent.Writer
    backfillTimestamp hlc.Timestamp
}

func (s *changefeedSink) OnKV(ctx context.Context, kv streampb.StreamEvent_KV) error {
    ev := kvevent.MakeKVEvent(kv, s.backfillTimestamp)
    return s.writer.Add(ctx, ev)
}

func (s *changefeedSink) OnSST(ctx context.Context, sst kvpb.RangeFeedSSTable) error {
    return errors.AssertionFailedf("unexpected SST in changefeed")
}

func (s *changefeedSink) OnDelRange(ctx context.Context, dr kvpb.RangeFeedDeleteRange) error {
    return nil // changefeeds ignore delete ranges
}
```

`changeAggregator.tick()` is **completely unchanged** — it reads
`kvevent.Event` from `kvevent.Reader` exactly as today. `ConsumeEvent`
is unchanged. The existing `kvevent.Buffer` handles backpressure and
memory accounting.

Later, once kvfeed is deleted and the unified path is the only path,
the `kvevent.Buffer` intermediate step can be removed and the
changefeed sink can write to a channel directly. This is a separate
cleanup step.

#### OrderedStreamHandler — unchanged

Wraps `eventStream` (which is a `RangefeedHandler`). The ordered
handler calls `eventStream.OnValue` / `OnDeleteRange` etc. after
reordering, so events flow through the same sink. Works for both
replication and changefeed sinks.

### Schema Change Detection

When an `OnSchemaChangeFn` callback is provided, `eventStream` starts
a schemafeed internally. When no callback is provided, no schemafeed
is started.

Detection works by checking `SchemaWatcher.Peek()` on frontier advance.
When a boundary is detected, the rangefeed is stopped and the callback
is invoked.

Since schema change detection lives on `eventStream` (the orchestrator),
all consumers get it for free — changefeeds, LDR, and any future
consumer.

### Initial Scans and Backfills

No change to the scan mechanism. `eventStream` uses rangefeed's
built-in `WithInitialScan` as it does today. Scan results flow
through `RangefeedHandler.OnValue` like any other event.

For changefeed backfills (schema-change-triggered re-scans), the
changefeed handler tracks `backfillTimestamp` as internal state, set
when the `OnSchemaChangeFn` returns `SchemaChangeBackfill`. The
handler stamps events with this timestamp so the downstream decoder
uses the correct schema version.

## Migration Path

### Feature gate for incremental rollout

A temporary global `bool` switches between kvfeed and the new path:

```go
// pkg/ccl/changefeedccl/changefeedbase/options.go
// Temporary — delete before merging.
var UseUnifiedEventPipeline = false
```

The switch point is in `changeAggregator.startKVFeed`. When true, it
creates an `eventStream` with a `changefeedHandler` instead of calling
`kvfeed.Run`. When false, existing behavior is unchanged.

**Testing**: flip the var in `TestMain` or individual tests to run the
existing changefeed test suite against the new path.

PCR and LDR don't need a gate — `RangefeedHandler` is exported and the
existing `eventStream` methods are renamed, but behavior is unchanged.

## kvfeed Coverage Checklist

This section audits every kvfeed behavior and maps it to the unified
pipeline design, ensuring nothing is dropped.

### kvFeed struct fields → pipeline mapping

| kvfeed field | Where it goes |
|---|---|
| `spans` | `Config.Spans` |
| `withDiff` | `Config.WithDiff` |
| `withFiltering` | `Config.WithFiltering` |
| `withInitialBackfill` | `Config.NeedsInitialScan` |
| `withBulkDelivery` | `Config.WithBulkDelivery` |
| `withFrontierQuantize` | `Config.FrontierQuantize` |
| `consumerID` | `Config.ConsumerID` |
| `initialHighWater` | `Config.InitialTimestamp` |
| `initialSpanTimePairs` | `Config.ResumeFrom` |
| `endTime` | `Config.EndTime` |
| `writer` | `changefeedSink` writes to `kvevent.Writer` (same buffer, no change to consumer) |
| `codec` | Pipeline internals (needed for schemafeed span construction) |
| `tableFeed` | Pipeline-internal schemafeed (started when `OnSchemaChange` non-nil) |
| `scanner` | Replaced by rangefeed's built-in scan |
| `physicalFeed` | Replaced by `rangefeed.Factory` |
| `bufferFactory` | `changefeedSink` writes to `kvevent.Writer`; buffer stays for now, eliminated later |
| `targets` | Pipeline internals (needed for schemafeed table filtering) |
| `schemaChangeEvents` | Passed to schemafeed internally |
| `schemaChangePolicy` | Captured in `OnSchemaChangeFn` closure |
| `onBackfillCallback` | changefeedHandler fires SLI metrics when backfill state is set |
| `rangeObserver` | Currently unused in kvfeed (field exists but never populated) |
| `timers` | Pipeline internals for scoped timer metrics |
| `knobs` | See TestingKnobs section below |

### Config fields (kvfeed.Config → pipeline Config)

| kvfeed.Config field | Pipeline equivalent |
|---|---|
| `Settings` | Pipeline accepts `*cluster.Settings` |
| `DB` | Used by schemafeed + scanner; pipeline internals |
| `Codec` | Pipeline internals |
| `Clock` | Pipeline internals |
| `Spans` | `Config.Spans` |
| `Targets` | Pipeline internals (for schemafeed) |
| `Writer` | `changefeedSink` writes to `kvevent.Writer` (same buffer) |
| `Metrics` | `changefeedSink` owns metrics |
| `MonitoringCfg` | `changefeedSink` fires metrics directly |
| `MM` | Rangefeed's `WithMemoryMonitor` replaces changefeed memory accounting |
| `WithDiff` | `Config.WithDiff` |
| `SchemaChangeEvents` | Pipeline internals (schemafeed config) |
| `SchemaChangePolicy` | `OnSchemaChangeFn` closure |
| `SchemaFeed` | Pipeline-internal; created when `OnSchemaChange` non-nil |
| `NeedsInitialScan` | `Config.NeedsInitialScan` |
| `InitialHighWater` | `Config.InitialTimestamp` |
| `InitialSpanTimePairs` | `Config.ResumeFrom` |
| `EndTime` | `Config.EndTime` |
| `WithFiltering` | `Config.WithFiltering` |
| `WithBulkDelivery` | `Config.WithBulkDelivery` |
| `WithFrontierQuantize` | `Config.FrontierQuantize` |
| `Knobs` | See below |
| `ScopedTimers` | Pipeline internals |
| `ConsumerID` | `Config.ConsumerID` |

### kvFeed.run() main loop behaviors

| Behavior | Pipeline handling |
|---|---|
| Build `rangeFeedResumeFrontier` from `initialSpanTimePairs` | `Pipeline.Run()` builds frontier from `Config.ResumeFrom` |
| `scanIfShould` on each iteration | `Pipeline.scanIfNeeded()` — uses rangefeed built-in scan; sink gets `OnScanStart`/`OnScanCompleted` |
| `initialScanOnly` (endTime == initialHighWater) | Pipeline detects this and exits after scan with `OnCheckpoint` at `EndTime` |
| `runUntilTableEvent` — start rangefeed, copy events, stop at schema boundary | `Pipeline.runRangefeed()` — rangefeed.Factory with sink callbacks; schemafeed.Peek on frontier advance |
| Schema change boundary detection via `tableFeed.Peek()` | Pipeline's internal schemafeed.Peek() in frontier advance callback |
| `emitResolved` with boundary types (BACKFILL, RESTART, EXIT) | `OnSchemaChange` callback emits these via the sink before returning |
| `schemaChangeDetectedError` return to trigger restart | `SchemaChangeStop` return from callback |
| `isPrimaryKeyChange` logic | Lives in changefeed's `OnSchemaChangeFn` |
| `errChangefeedCompleted` on end time | Pipeline returns nil; caller detects completion |

### copyFromSourceToDestUntilTableEvent behaviors

| Behavior | Pipeline handling |
|---|---|
| Read events from memBuf (intermediate buffer) | Not needed — rangefeed.Factory delivers directly to sink via callbacks |
| `checkForTableEvent` on every event timestamp | Pipeline checks schemafeed.Peek() on frontier advance |
| `checkCopyBoundary` — skip KVs past boundary, stop at boundary | Pipeline stops the rangefeed at the boundary timestamp |
| Forward frontier on resolved events | Pipeline frontier management (built into rangefeed.Factory) |
| `endTime` boundary — emit final checkpoint at `endTime.Prev()` | Pipeline emits final `OnCheckpoint` at `EndTime.Prev()` |
| Memory allocation release for skipped events | Not needed — no intermediate kvevent.Buffer between rangefeed and sink |

### physical_kv_feed.go (rangefeed event handling)

| Behavior | Pipeline handling |
|---|---|
| `RangeFeedValue` → `kvevent.MakeKVEvent` → memBuf | `RangeFeedValue` → `eventStream.OnValue` → `sink.OnKV` |
| `RangeFeedCheckpoint` → quantize → filter below frontier → memBuf | Quantization via frontier quantize setting; frontier filtering in eventStream; `eventStream.OnCheckpoint` |
| `RangeFeedSSTable` → error | `handler.OnSSTable` — changefeedHandler returns error; replication handler handles |
| `RangeFeedDeleteRange` → ignore | `handler.OnDeleteRange` — changefeedHandler ignores; replication handler forwards |
| `RangeFeedBulkEvents` → recursive handling | eventStream unpacks bulk events and calls handler methods individually |
| 128-element channel buffer between rangefeed and memBuf | Not needed — rangefeed.Factory uses callbacks, no channel |
| `quantizeTS` for resolved timestamps | eventStream applies quantization before calling `handler.OnCheckpoint` |

### scanner.go (backfill scan)

| Behavior | Pipeline handling |
|---|---|
| `scanRequestScanner.Scan` — parallel KV ScanRequests | Replaced by rangefeed built-in scan (also does parallel ScanRequests via `WithInitialScanParallelismFn`) |
| `BackfillKVEvent` with `backfillTimestamp` | changefeedHandler tracks `backfillTimestamp` as state, tags events |
| `BackfillResolvedEvent` per completed span | changefeedHandler emits via channel on span completion |
| Memory acquisition via `tryAcquireMemory` | Rangefeed's `WithMemoryMonitor` handles memory bounding |
| `onBackfillRangeCallback` for SLI metrics | changefeedHandler fires metrics when backfill state changes |
| `getRangesToProcess` for range-aligned span splitting | Rangefeed built-in scan handles this via `divideAndSendScanRequests` |
| `BATCH_RESPONSE` scan format | Rangefeed built-in scan uses standard `Scan` format (functionally equivalent) |

### TestingKnobs

`eventStream` needs testing knobs for changefeed behavior. kvfeed knobs
map as follows:

| kvfeed knob | eventStream equivalent |
|---|---|
| `BeforeScanRequest` | Passed to rangefeed scan config |
| `OnRangeFeedValue` | Called in `OnValue` path |
| `ShouldSkipCheckpoint` | Called before `handler.OnCheckpoint` |
| `OnRangeFeedStart` | Called when rangefeed starts |
| `EndTimeReached` | Checked in end-time boundary logic |
| `RangefeedOptions` | Appended to rangefeed options |

### MonitoringConfig callbacks

| Callback | Pipeline handling |
|---|---|
| `OnBackfillCallback` | `changefeedSink` fires when backfill state changes |
| `OnBackfillRangeCallback` | `changefeedSink` fires on backfill range progress |

### Naming conventions preserved from kvfeed

- **`RangefeedHandler`** — already exists, just exported
- **`eventSink`** — new interface for delivery abstraction
- **`replicationSink`** — extracted from `eventStream` (PCR/LDR)
- **`changefeedSink`** — new (CDC), writes to `kvevent.Writer`
- **`schemaChangeDetectedError`** → internal sentinel in eventStream
- **`errChangefeedCompleted`** / **`errEndTimeReached`** → internal
  sentinels (same names)
- **`SchemaChangePolicy`** / **`SchemaChangeEventClass`** → unchanged,
  used by changefeed's `OnSchemaChangeFn` closure
- **`BackfillTimestamp`** → field on `changefeedSink` (same name)
- **`physicalFeedFactory`** → gone (replaced by `rangefeed.Factory`)
- **`kvScanner`** → gone (replaced by rangefeed built-in scan)
- **`kvevent.Writer`** / **`kvevent.Reader`** / **`kvevent.Buffer`** →
  kept initially for changefeedSink; eliminated in follow-up
- **`copyBoundary`** / **`copyFromSourceToDestUntilTableEvent`** →
  eliminated; no intermediate buffer copy
- **`MonitoringConfig`** → changefeedSink fires metrics directly

## Follow-up: Ordered Delivery for Changefeeds

### Motivation

The `OrderedStreamHandler` + `OrderedBuffer` in event_stream provides total
`(timestamp, key)` ordering via a disk-backed sorted buffer. Today this is
used only by LDR (via `WithMvccOrdering`). Once the pipeline is unified,
changefeeds could opt into this ordering as a gated cluster setting for
use cases that benefit from deterministic output ordering.

### Correctness constraint: schema change boundaries

The ordered buffer cannot be naively flipped on for changefeeds. Today,
kvfeed's `copyFromSourceToDestUntilTableEvent` checks
`schemafeed.Peek(ts)` on **every event's timestamp** and skips KVs at or
past a schema change boundary. This prevents events written under a new
schema from being decoded with the old schema.

The ordered buffer flushes all events up to the frontier in one batch.
The frontier only advances when all ranges have resolved — which may be
past the schema change timestamp. Without modification, events past the
schema boundary would be delivered to the sink and decoded with the wrong
schema.

Example:
- Schema change at T=10
- Range A: KVs at T=8, T=9, resolves to T=12
- Range B: KVs at T=7, T=11, resolves to T=12
- Frontier advances to T=12
- Ordered buffer flushes T=7, T=8, T=9, **T=11** ← decoded with old schema, wrong

### Fix: clamp flush to schema boundary

The ordered buffer already supports partial flushes — `FlushToDisk` and
`GetEventsFromDisk` both take a `resolvedTimestamp` and only return events
with `ts <= resolvedTimestamp`. The fix is to clamp the flush timestamp
to the schema change boundary before flushing:

```go
func (h *OrderedStreamHandler) onFrontier(ctx context.Context, resolvedTs hlc.Timestamp) {
    // Before flushing, check if there's a schema change before resolvedTs.
    if boundaryTs, ok := h.schemaFeed.Peek(ctx, resolvedTs); ok {
        // Only flush up to the boundary. Events past it stay in the buffer.
        resolvedTs = boundaryTs.Prev()
    }
    h.handleFrontier(ctx, resolvedTs)
}
```

With this:
1. Frontier advances to T=12
2. Schema change detected at T=10
3. Buffer flushes only events ≤ T=9 (`boundary.Prev()`)
4. Pipeline stops, invokes `OnSchemaChange` callback
5. After backfill/restart, remaining events (T=11) are handled with new schema

This fits naturally into the unified pipeline — the pipeline already owns
the schemafeed and controls frontier-based flushing. The ordered buffer
just becomes another consumer of the boundary timestamp.

### Implementation plan

1. Add schema boundary awareness to `OrderedStreamHandler.onFrontier` as
   shown above. The schemafeed reference comes from the pipeline, passed
   through at construction time.
2. Add a cluster setting (gated, default off):
   ```
   changefeed.ordered_delivery.enabled
   ```
3. When enabled, the changefeedSink wraps itself with the
   `OrderedStreamHandler`, same pattern as `replicationSink` does today.
4. Performance tradeoffs (latency spikes at frontier advance, memory/disk
   pressure from buffering between frontiers) should be documented and
   measured before enabling by default.

### Not blocked on unified pipeline

This could technically be built on the current kvfeed too, but it's
much cleaner with the unified pipeline since the ordered buffer, schema
boundary detection, and frontier management are already in the same
place.

## Implementation Plan

### PR 1: Introduce EventSink, extract replicationSink, move generic infrastructure to repstream

1. Export `RangefeedHandler` methods (capitalize). **Done.**
2. Define `EventSink` interface in `pkg/repstream/`.
3. Extract `replicationSink` from `eventStream` in
   `pkg/crosscluster/producer/`:
   - Move batching/flushing logic off `eventStream` onto
     `replicationSink`.
   - `replicationSink` implements `EventSink`.
4. Update `eventStream` handler methods to call through the sink.
   **Done.**
5. Move shared event stream building blocks from
   `pkg/crosscluster/producer/` to `pkg/repstream/`:
   - `EventSink` interface **Done.**
   - `RangefeedHandler` interface + `OrderedStreamHandler` **Done.**
   - `OrderedBuffer` + `OrderedBufferConfig` **Done.**
   - `ScanSST` (from `replicationutils`) **Done.**
   - `streamEventBatcher` stays in producer (replication-specific).
   - `eventStream` stays in producer (has replication-specific code;
     CDC will build its own rangefeed setup using the shared
     primitives from `repstream`).
6. `streamPartition()` stays in `crosscluster/producer/`, creates
   `replicationSink` and `orderedEventStreamAdapter` (thin wrapper
   adding `eval.ValueGenerator` for pgwire streaming).

**Current status**: Steps 1–5 are done. All production code and
tests compile. `eventStream` stays in producer — CDC will have its
own rangefeed orchestrator using the shared `repstream` building
blocks rather than sharing `eventStream`.

**Validation**: all existing PCR and LDR tests must pass unchanged.
Purely a refactor — no behavioral change.

### PR 2: Schema change support in eventStream

1. Add `OnSchemaChangeFn`, `SchemaChangeAction`, `SchemaChangeEvent`
   types to `pkg/repstream/`.
2. Add a `SchemaWatcher` interface:
   ```go
   type SchemaWatcher interface {
       Run(ctx context.Context) error
       Peek(ctx context.Context, atOrBefore hlc.Timestamp) ([]SchemaChangeEvent, error)
       Pop(ctx context.Context, atOrBefore hlc.Timestamp) ([]SchemaChangeEvent, error)
   }
   ```
3. Add optional `SchemaWatcher` and `OnSchemaChangeFn` fields to
   `eventStream`.
4. Wire into `eventStream.Start()`:
   - Start `SchemaWatcher.Run` in a goroutine if non-nil
   - Check `Peek()` on frontier advance
   - On boundary: stop rangefeed, `Pop()` events, invoke
     `OnSchemaChangeFn`, act on `SchemaChangeAction`
5. Tests for schema boundary detection with mock `SchemaWatcher`.

### PR 3: Changefeed sink behind feature gate

1. Add `var UseUnifiedEventPipeline = false` in changefeedbase.
2. Create `changefeedSink` implementing `EventSink` in
   `pkg/ccl/changefeedccl/`:
   - `OnKV` → wraps as `kvevent.Event`, writes to `kvevent.Writer`
   - `OnSST` → error
   - `OnDelRange` → no-op
   - Tracks `backfillTimestamp` as internal state
3. Create schemafeed adapter implementing `SchemaWatcher` — wraps
   `schemafeed.SchemaFeed`.
4. Create `makeChangefeedSchemaChangeCallback` with policy logic.
5. Modify `changeAggregator.startKVFeed`:
   - When gate is on, create an `eventStream` (from `repstream`)
     with `changefeedSink` instead of calling `kvfeed.Run`
   - Return the same `kvevent.Reader` (the buffer) to the caller
   - `tick()` is **unchanged** — reads `kvevent.Event` as before
   - `ConsumeEvent` is **unchanged**
6. Wire behind `UseUnifiedEventPipeline`, flip in tests, fix failures.

### PR 4: Delete kvfeed

1. Remove `UseUnifiedEventPipeline` var and the branch.
2. Delete `pkg/ccl/changefeedccl/kvfeed/`.
3. Clean up any remaining references.

### PR 5 (follow-up): Remove kvevent.Buffer from changefeed path

1. Replace `changefeedSink` → `kvevent.Writer` with direct channel.
2. Adapt `changeAggregator.tick()` to read from channel.
3. Remove `kvevent.Writer`/`Reader`/`Buffer` if no longer used.

## Open Questions

### 1. Schema boundary timing: per-event vs per-frontier-advance

kvfeed checks `schemafeed.Peek()` on every event timestamp via
`copyFromSourceToDestUntilTableEvent`. The new approach checks on
frontier advance only. This means events between the schema change
timestamp and the next frontier advance could reach the handler.

This is safe because:
- The frontier only advances when all ranges have resolved past a
  timestamp. If a schema change happened at T, the frontier advances
  to T only after all ranges are past T.
- `schemafeed.Peek(frontierTs)` detects the change at T.
- `eventStream` stops the rangefeed. Events already delivered to the
  handler at timestamps > T haven't been decoded yet (decoding happens
  downstream in `changeAggregator`).
- The changefeed handler can tag these events so the decoder knows to
  use the new schema, or the `OnSchemaChangeFn` callback can flush/
  discard them before returning.

This needs careful testing with the existing changefeed schema change
tests to verify no behavioral regression.

### 2. Boundary resolved span emission

kvfeed emits resolved spans with boundary types (`BACKFILL`, `RESTART`,
`EXIT`) to signal the `changeAggregator`. This is changefeed-specific.
The `OnSchemaChangeFn` callback emits these via the handler's channel
before returning its action. `eventStream` doesn't know about boundary
types.

### 3. ConsumeEvent adaptation — resolved

With the `eventSink` approach, `changefeedSink` writes `kvevent.Event`
to `kvevent.Writer`. `ConsumeEvent` receives the same `kvevent.Event`
type as today — no adaptation needed. This is a non-issue as long as
the `kvevent.Buffer` is kept in the initial integration.

When the buffer is removed in the follow-up (PR 5), `ConsumeEvent`
will need to accept a different type. At that point kvfeed is already
deleted, so it's a straightforward refactor.
