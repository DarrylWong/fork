// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package repstream

import (
	"context"

	"github.com/cockroachdb/cockroach/pkg/util/hlc"
)

// SchemaChangeAction tells the event stream what to do after a schema change
// boundary is detected and the OnSchemaChangeFn callback has run.
type SchemaChangeAction int

const (
	// SchemaChangePause indicates that the event stream should stop the
	// rangefeed at the boundary timestamp. The caller is responsible for
	// restarting or cleaning up.
	SchemaChangePause SchemaChangeAction = iota + 1
	// SchemaChangeContinue indicates that the event stream should continue
	// past the schema change boundary without stopping.
	SchemaChangeContinue
)

// SchemaWatcher monitors for schema changes and reports them at specific
// timestamps. Implementations wrap consumer-specific schema feeds (e.g.
// schemafeed.SchemaFeed for changefeeds). The event stream checks for
// boundaries on frontier advance and delegates the policy decision to the
// OnSchemaChangeFn callback.
//
// The interface uses an opaque `any` event type because schema change
// event contents are consumer-specific (e.g. TableEvent for CDC). The
// event stream does not interpret the events — it only needs to know
// whether a boundary exists and at what timestamp.
type SchemaWatcher interface {
	// Run starts the schema watcher. It should be called before Peek or Pop.
	// It blocks until the context is cancelled or an error occurs.
	Run(ctx context.Context) error

	// Peek returns the timestamp of the earliest schema change at or before
	// atOrBefore, along with the events. Returns nil events if no schema
	// change exists. This is non-destructive.
	Peek(ctx context.Context, atOrBefore hlc.Timestamp) (events []any, err error)

	// Pop returns and removes schema change events at or before atOrBefore.
	Pop(ctx context.Context, atOrBefore hlc.Timestamp) (events []any, err error)
}

// OnSchemaChangeFn is called by the event stream when a schema change boundary
// is detected. The callback receives the boundary timestamp and the events
// from SchemaWatcher.Peek. It should perform any consumer-specific actions
// (e.g. emitting boundary resolved spans for CDC) and return the action the
// event stream should take.
type OnSchemaChangeFn func(
	ctx context.Context, boundaryTS hlc.Timestamp, events []any,
) (SchemaChangeAction, error)
