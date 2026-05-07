// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"context"
	gosql "database/sql"
	"math/rand"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/util/fsm"
	"github.com/cockroachdb/errors"
)

// WorkerConfig is the static configuration a worker receives from the
// orchestrator at startup. The same config is shared by every worker on the
// same shard / sub-DAG group; per-worker variation comes from the worker's
// RNG, which decides PK assignments and op selection.
type WorkerConfig struct {
	// DB is the connection the worker uses for all transactions. Each worker
	// owns its own *sql.DB (or a pool with at least one dedicated conn) so
	// per-worker transaction state is independent.
	DB *gosql.DB

	// Sorted, Sub, Dropped describe the sub-DAG the worker drives. All
	// workers receive the same sub-DAG. Cross-worker PK overlap depends on
	// PKPool: nil means each chain samples fresh from each column's full
	// type domain (collisions are incidental); non-nil means sampling
	// from a shared, bounded pool (collisions are deliberate).
	Sorted  []*Table
	Sub     *FKGraph
	Dropped []FKEdge

	// PKPool, when non-nil, is the orchestrator-built pool that AssignPKs
	// samples from. All workers in a generation share the same pool so
	// they collide on PK values across the same bounded keyspace.
	PKPool PKValuePool

	// Mix controls the relative frequency of Upsert/Update/Delete events
	// when the chain is in the Exists state. Gone always advances via Upsert.
	Mix OpMix

	// MinChainLen and MaxChainLen bound the number of txns per chain. A
	// chain runs MinChainLen + rng.Intn(MaxChainLen-MinChainLen+1) txns
	// against a single PK assignment before fresh PKs are picked. Longer
	// chains build up more per-row history (UPSERT → UPDATE → DELETE →
	// UPSERT) within a single worker stream, which the destination must
	// apply in order.
	MinChainLen, MaxChainLen int

	// TolerateSrcErrors controls whether source-side errors (FK violations,
	// serialization errors) are logged and skipped or returned as fatal.
	// True for the workload's normal operation; false for unit tests that
	// want to assert no errors occur.
	TolerateSrcErrors bool
}

// Worker drives one stream of chained transactions against a sub-DAG. It is
// not safe to share across goroutines; create one Worker per goroutine.
type Worker struct {
	cfg WorkerConfig
	rng *rand.Rand
}

// NewWorker constructs a worker with its own RNG. Pass independent RNGs
// across workers so they make independent op-mix and PK-sampling choices.
func NewWorker(cfg WorkerConfig, rng *rand.Rand) *Worker {
	return &Worker{cfg: cfg, rng: rng}
}

// ChainResult summarizes one Run: how many txns the chain attempted, how
// many committed, and any source error that ended the chain early. The
// committed/attempted ratio is the success ratio under contention; the
// FailedEvent + FailErr fields let the caller categorize what kind of
// contention was hit.
type ChainResult struct {
	Attempted int
	Committed int
	// RowsWritten is the total number of rows written by committed events
	// in this chain (one per table for upsert/delete walks, one for
	// updates). Useful for tracking actual replication volume, since a
	// committed_txn can range from 1 row (update) to |sub-DAG tables|
	// (upsert/delete walk).
	RowsWritten int
	// RetryAttempted counts upsert events that hit a unique-violation from
	// Gone and triggered a retry against the existing row's PK.
	// RetrySucceeded counts the subset where the retry committed. Together
	// they expose how often the workload is running in the saturated
	// UC-violation regime and how effective the retry path is.
	RetryAttempted int
	RetrySucceeded int
	// FailedEvent is the FSM event whose action returned the source error
	// that ended the chain. Nil if the chain ran to completion. Useful for
	// breaking down contention failures by op type.
	FailedEvent fsm.Event
	// FailErr is the underlying error returned by the failed action. Nil
	// when the chain ran to completion. Used for classifying the contention
	// type (FK violation vs. serialization vs. unique violation, etc.).
	FailErr error
}

// FailureClass returns a coarse categorization of FailErr suitable for
// reporting and aggregation. Returns "" if the chain succeeded.
func (r ChainResult) FailureClass() string {
	if r.FailErr == nil {
		return ""
	}
	return classifyError(r.FailErr)
}

// Run executes one chain: pick a fresh PK assignment, then drive the FSM
// through MinChainLen..MaxChainLen txns. Each FSM event runs in its own
// transaction so the chain produces a sequence of separately-committed
// txns the destination must apply in order.
//
// Source-side errors are absorbed when TolerateSrcErrors is true; the chain
// ends early on the first absorbed error so the per-chain state machine
// doesn't drift from the database. The early-ended attempt still counts in
// Attempted (it was tried) but not in Committed.
func (w *Worker) Run(ctx context.Context) (ChainResult, error) {
	pks, err := AssignPKs(w.rng, w.cfg.Sorted, w.cfg.Sub, w.cfg.PKPool)
	if err != nil {
		return ChainResult{}, errors.Wrap(err, "assigning PKs")
	}

	// chainExtended is mutated per event with the current tx; the machine
	// holds a stable pointer to it for the lifetime of the chain.
	ext := &chainExtended{
		rng:     w.rng,
		sorted:  w.cfg.Sorted,
		sub:     w.cfg.Sub,
		dropped: w.cfg.Dropped,
		pks:     pks,
	}
	machine := fsm.MakeMachine(chainTransitions, chainStateGone{}, ext)

	var res ChainResult
	chainLen := w.pickChainLen()
	for i := 0; i < chainLen; i++ {
		event := w.cfg.Mix.pickEvent(w.rng, machine.CurState())
		res.Attempted++
		ext.lastRowsWritten = 0
		retried, err := w.applyEvent(ctx, &machine, ext, event)
		if retried.attempted {
			res.RetryAttempted++
		}
		if retried.succeeded {
			res.RetrySucceeded++
		}
		if err != nil {
			if w.cfg.TolerateSrcErrors && isSourceError(err) {
				// Source error: the FSM state may no longer reflect the DB
				// state (e.g. a parent disappeared mid-UPSERT). End the chain
				// to avoid further state divergence.
				res.FailedEvent = event
				res.FailErr = err
				return res, nil
			}
			return res, err
		}
		res.Committed++
		res.RowsWritten += ext.lastRowsWritten
	}
	return res, nil
}

// retryOutcome reports whether applyEvent took the retry path on a given
// event. attempted is set whenever the retry lookup fired; succeeded is the
// subset where the retry committed.
type retryOutcome struct {
	attempted bool
	succeeded bool
}

// applyEvent opens a transaction, applies the event via the FSM (which runs
// the corresponding action against ext.tx), and commits (or rolls back on
// error). On commit failure, the FSM state has already advanced — but the
// caller treats commit failure the same as action failure: end the chain.
//
// Special case: a unique-violation upsert from Gone means the row chain's
// chosen non-PK unique values collided with an existing row. Rather than
// abort the chain (which would waste the chain's setup and stall progress
// once the keyspace saturates), the worker rewrites the failing table's PK
// to the existing row's PK and retries the upsert once. The second upsert
// hits the PK-update path instead of insert+UC violation.
func (w *Worker) applyEvent(
	ctx context.Context, m *fsm.Machine, ext *chainExtended, event fsm.Event,
) (retryOutcome, error) {
	tx, err := w.cfg.DB.BeginTx(ctx, nil)
	if err != nil {
		return retryOutcome{}, errors.Wrap(err, "begin")
	}
	ext.tx = tx

	if err := m.Apply(ctx, event); err != nil {
		_ = tx.Rollback()
		if _, isUpsert := event.(eventUpsert); isUpsert && classifyError(err) == "unique_violation" {
			outcome, perr := w.retryUpsert(ctx, m, ext, err)
			if perr != nil {
				return outcome, perr
			}
			if outcome.succeeded {
				return outcome, nil
			}
			return outcome, err
		}
		return retryOutcome{}, err
	}
	return retryOutcome{}, tx.Commit()
}

// retryUpsert handles a unique-violation upsert from Gone by looking up the
// existing row on the failing table (via one of its non-PK UCs) and re-
// running the upsert with that row's PK swapped into ext.pks. The second
// upsert hits the PK-update path instead of insert+UC violation. Returns
// (true, nil) when the retry committed (caller treats the event as
// successful), (false, nil) when no retry was possible (caller propagates
// the original error), or (_, err) on a fatal secondary failure.
func (w *Worker) retryUpsert(
	ctx context.Context, m *fsm.Machine, ext *chainExtended, origErr error,
) (retryOutcome, error) {
	var ue *UpsertError
	if !errors.As(origErr, &ue) {
		return retryOutcome{}, nil
	}
	var failingTable *Table
	for _, t := range ext.sorted {
		if t.Name == ue.Table {
			failingTable = t
			break
		}
	}
	if failingTable == nil {
		return retryOutcome{}, nil
	}

	// Past this point we've committed to attempting a retry — even if the
	// lookup or retry fails, the attempt counter advances so observers see
	// the workload tried.
	outcome := retryOutcome{attempted: true}

	// Use a short-lived read-only tx for the lookup so we don't entangle it
	// with the upsert retry. Could share a tx, but keeping them separate
	// makes the lookup independently retryable.
	lookupTx, err := w.cfg.DB.BeginTx(ctx, nil)
	if err != nil {
		return outcome, errors.Wrap(err, "retry lookup begin")
	}
	existingPK, err := LookupExistingPK(ctx, lookupTx, failingTable, ue.Row)
	_ = lookupTx.Rollback()
	if err != nil {
		return outcome, errors.Wrap(err, "retry lookup")
	}
	if existingPK == nil {
		return outcome, nil
	}
	ext.pks[failingTable.Name] = existingPK

	// Pin the original row so the retry preserves the UC values that
	// already exist on the target row. Without this, buildRow would
	// regenerate fresh random UC values that would likely collide again.
	if ext.pinnedRows == nil {
		ext.pinnedRows = make(map[string]emittedRow, 1)
	}
	ext.pinnedRows[failingTable.Name] = ue.Row
	defer func() { delete(ext.pinnedRows, failingTable.Name) }()

	// Restart the upsert in a fresh transaction.
	tx, err := w.cfg.DB.BeginTx(ctx, nil)
	if err != nil {
		return outcome, errors.Wrap(err, "retry begin")
	}
	ext.tx = tx
	applyErr := m.Apply(ctx, eventUpsert{})
	if applyErr != nil {
		// The retry hit another contention error. Roll back and let the
		// caller report the original violation; the chain ends.
		_ = tx.Rollback()   //nolint:returnerrcheck
		return outcome, nil //nolint:returnerrcheck
	}
	if err := tx.Commit(); err != nil {
		return outcome, nil //nolint:returnerrcheck
	}
	outcome.succeeded = true
	return outcome, nil
}

func (w *Worker) pickChainLen() int {
	if w.cfg.MaxChainLen <= w.cfg.MinChainLen {
		return w.cfg.MinChainLen
	}
	return w.cfg.MinChainLen + w.rng.Intn(w.cfg.MaxChainLen-w.cfg.MinChainLen+1)
}

// sourceErrorClass maps a coarse classification name to substring patterns
// that identify the error in the wire message. We match strings (rather than
// pgcode) so the workload runs uniformly against any Postgres-compatible
// target. Order matters only for reporting consistency — the classifier
// returns the first matching class.
var sourceErrorClass = []struct {
	class    string
	patterns []string
}{
	{"fk_violation", []string{
		"foreign key violation",
		"violates foreign key constraint",
	}},
	{"serialization", []string{
		"restart transaction",
		"RETRY_SERIALIZABLE",
		"RETRY_WRITE_TOO_OLD",
	}},
	{"unique_violation", []string{
		"duplicate key value",
		"violates unique constraint",
	}},
	// SQLSTATE 22003 covers any value the source can't represent: integer
	// overflow (including expression-index sums of two near-max ints),
	// out-of-range time/timestamp/interval, decimal precision/scale. The
	// workload generates datums at the extremes of each type's domain via
	// randgen.RandDatum, so these are tolerable source-side rejections.
	{"out_of_range", []string{
		"out of range",
	}},
}

// isSourceError reports whether err is a tolerable source-side failure
// produced by concurrent contention. Connection-level and assertion errors
// fall through and are returned to the caller.
func isSourceError(err error) bool {
	return classifyError(err) != ""
}

// classifyError returns the source-error class name (e.g. "fk_violation")
// or "" if err is not a known contention error. Used by ChainResult to
// surface what kind of contention each failed chain hit.
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, c := range sourceErrorClass {
		for _, p := range c.patterns {
			if strings.Contains(msg, p) {
				return c.class
			}
		}
	}
	return ""
}
