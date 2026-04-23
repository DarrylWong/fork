// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package revlogjob

import (
	"bytes"
	"context"
	"sort"

	"github.com/cockroachdb/cockroach/pkg/cloud"
	"github.com/cockroachdb/cockroach/pkg/keys"
	"github.com/cockroachdb/cockroach/pkg/kv/kvclient/rangefeed"
	"github.com/cockroachdb/cockroach/pkg/kv/kvpb"
	"github.com/cockroachdb/cockroach/pkg/revlog"
	"github.com/cockroachdb/cockroach/pkg/revlog/revlogpb"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/descbuilder"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/descpb"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/errors"
)

// ErrScopeTerminated signals that scope.Terminated returned true
// and the writer should exit successfully. Callers translate it
// into clean job completion, not an error.
var ErrScopeTerminated = errors.New("revlogjob: scope terminated")

// runDescFeed subscribes one rangefeed on system.descriptor and
// dispatches its events to handleValue (descriptor changes) and
// the manager's descriptor frontier (checkpoints). Returns when
// ctx is cancelled, the scope terminates, or the rangefeed
// errors terminally.
func runDescFeed(
	ctx context.Context,
	factory *rangefeed.Factory,
	codec keys.SQLCodec,
	scope Scope,
	manager *TickManager,
	es cloud.ExternalStorage,
	startHLC hlc.Timestamp,
	initialSpans []roachpb.Span,
) error {
	descSpan := roachpb.Span{
		Key:    codec.DescMetadataPrefix(),
		EndKey: codec.DescMetadataPrefix().PrefixEnd(),
	}

	state := &descFeedState{
		scope:        scope,
		manager:      manager,
		es:           es,
		codec:        codec,
		lastSpans:    cloneSpans(initialSpans),
		lastSpansSet: true,
	}

	eventsCh := make(chan rangefeedEvent, 256)
	errCh := make(chan error, 1)

	rf, err := factory.RangeFeed(ctx, "revlog-descfeed",
		[]roachpb.Span{descSpan}, startHLC,
		func(ctx context.Context, v *kvpb.RangeFeedValue) {
			select {
			case eventsCh <- rangefeedEvent{value: v}:
			case <-ctx.Done():
			}
		},
		rangefeed.WithDiff(false),
		rangefeed.WithOnCheckpoint(
			func(ctx context.Context, cp *kvpb.RangeFeedCheckpoint) {
				select {
				case eventsCh <- rangefeedEvent{checkpoint: cp}:
				case <-ctx.Done():
				}
			}),
		rangefeed.WithOnInternalError(func(ctx context.Context, err error) {
			select {
			case errCh <- err:
			case <-ctx.Done():
			}
		}),
	)
	if err != nil {
		return errors.Wrap(err, "starting descriptor rangefeed")
	}
	defer rf.Close()

	for {
		select {
		case ev := <-eventsCh:
			switch {
			case ev.value != nil:
				if err := state.handleValue(ctx, ev.value); err != nil {
					return err
				}
			case ev.checkpoint != nil:
				if err := manager.ForwardDescFrontier(ctx, ev.checkpoint.ResolvedTS); err != nil {
					return err
				}
			}
		case err := <-errCh:
			return errors.Wrap(err, "descriptor rangefeed internal error")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// descFeedState carries the per-feed mutable state. Only the
// single dispatcher goroutine in runDescFeed touches it, so no
// locking is needed.
type descFeedState struct {
	scope   Scope
	manager *TickManager
	es      cloud.ExternalStorage
	codec   keys.SQLCodec

	// lastSpans is the most recently written coverage span set,
	// used to diff against newly-resolved spans on each
	// in-scope event. lastSpansSet=false means no coverage entry
	// has been written yet — the next span computation always
	// writes.
	lastSpans    []roachpb.Span
	lastSpansSet bool
}

// handleValue processes one descriptor-row update: writes the
// schema delta, writes a coverage entry if the resolved span set
// changed, and signals termination if the scope dissolved.
//
// Tombstones (zero-payload rangefeed values) are always written
// without consulting Matches — the prior version's row was in
// scope for us to observe it at all, and tombstones are tiny.
func (s *descFeedState) handleValue(ctx context.Context, v *kvpb.RangeFeedValue) error {
	descID, err := s.codec.DecodeDescMetadataID(v.Key)
	if err != nil {
		log.Dev.VInfof(ctx, 2,
			"revlogjob: descfeed skipping non-descriptor key %s: %v", v.Key, err)
		return nil
	}
	desc, err := decodeDescriptorValue(&v.Value)
	if err != nil {
		return errors.Wrapf(err, "decoding descriptor %d at %s", descID, v.Value.Timestamp)
	}
	if desc != nil && !s.scope.Matches(desc) {
		return nil
	}

	if err := revlog.WriteSchemaDesc(
		ctx, s.es, v.Value.Timestamp, descpb.ID(descID), desc,
	); err != nil {
		return errors.Wrapf(err,
			"writing schema delta for desc %d at %s", descID, v.Value.Timestamp)
	}

	newSpans, err := s.scope.Spans(ctx, v.Value.Timestamp)
	if err != nil {
		return errors.Wrap(err, "recomputing scope spans after descriptor change")
	}
	if !s.lastSpansSet || !sameSpans(s.lastSpans, newSpans) {
		s.lastSpans = cloneSpans(newSpans)
		s.lastSpansSet = true
		if err := revlog.WriteCoverage(ctx, s.es, revlogpb.Coverage{
			EffectiveFrom: v.Value.Timestamp,
			Scope:         s.scope.String(),
			Spans:         newSpans,
		}); err != nil {
			return errors.Wrapf(err,
				"writing coverage transition at %s", v.Value.Timestamp)
		}
	}

	// Empty resolved spans is the only state in which Terminated
	// can flip true; skip the check otherwise (avoids a catalog
	// load per descriptor change).
	if len(newSpans) == 0 {
		terminated, err := s.scope.Terminated(ctx, v.Value.Timestamp)
		if err != nil {
			return errors.Wrap(err, "checking scope termination")
		}
		if terminated {
			return ErrScopeTerminated
		}
	}
	return nil
}

// decodeDescriptorValue extracts a *descpb.Descriptor from a
// rangefeed value. A zero-length value (tombstone — the row was
// deleted from system.descriptor) is signaled by returning
// (nil, nil).
func decodeDescriptorValue(v *roachpb.Value) (*descpb.Descriptor, error) {
	if len(v.RawBytes) == 0 {
		return nil, nil
	}
	b, err := descbuilder.FromSerializedValue(v)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	return b.BuildImmutable().DescriptorProto(), nil
}

// sameSpans reports whether two span slices contain the same
// spans in the same order. Coverage diff cares only about set
// equality, but spansForAllTableIndexes returns merged-and-sorted
// spans deterministically, so order equality is a tighter and
// cheaper check.
func sameSpans(a, b []roachpb.Span) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i].Key, b[i].Key) || !bytes.Equal(a[i].EndKey, b[i].EndKey) {
			return false
		}
	}
	return true
}

// cloneSpans returns a deep copy of the given span slice.
func cloneSpans(in []roachpb.Span) []roachpb.Span {
	out := make([]roachpb.Span, len(in))
	for i, sp := range in {
		out[i].Key = append(roachpb.Key(nil), sp.Key...)
		out[i].EndKey = append(roachpb.Key(nil), sp.EndKey...)
	}
	// Defensive sort — caller may already pass sorted spans.
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].Key, out[j].Key) < 0
	})
	return out
}
