// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package txnwriter

import (
	"context"
	"fmt"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/crosscluster/logical/ldrdecoder"
	"github.com/cockroachdb/cockroach/pkg/crosscluster/logical/sqlwriter"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/errors"
)

// ApplyBatch attempts to apply each of the transactions. The apply status for
// every transaction is tracked in the ApplyResult. ApplyBatch applies all of
// the transactions in a single transaction and internally uses Savepoints to
// allow partial application of transactions.
//
// NOTE: the error indicates there was an issue applying the batch. If there is
// a conflict that should send a row to the DLQ, that conflict is recorded in
// the ApplyResult.
func (tw *transactionWriter) ApplyBatch(
	ctx context.Context, transactions []ldrdecoder.Transaction,
) ([]ApplyResult, error) {
	for _, transaction := range transactions {
		for _, row := range transaction.WriteSet {
			err := tw.initTable(ctx, row.TableID)
			if err != nil {
				return nil, err
			}
		}
	}

	results := make([]ApplyResult, len(transactions))

	err := tw.session.Txn(ctx, func(ctx context.Context) error {
		// We clear the results because the Txn may be retried.
		clear(results)
		return tw.tryApply(ctx, transactions, results)
	})
	if err == nil {
		// DNM: log DLQs from the first-attempt path.
		logDLQs(ctx, "first-attempt", transactions, results)
		return results, nil
	}
	if !errors.Is(err, sqlwriter.ErrStalePreviousValue) {
		return nil, err
	}

	// DNM: log refresh-retry trigger.
	log.Dev.Infof(ctx,
		"DNM-apply: first attempt failed with stale previous value, refreshing batch of %d txns",
		len(transactions))

	err = tw.session.Txn(ctx, func(ctx context.Context) error {
		clear(results)
		refreshed, err := tw.refresh(ctx, transactions, results)
		if err != nil {
			return err
		}
		return tw.tryApply(ctx, refreshed, results)
	})
	if err != nil {
		return nil, err
	}

	// DNM: surface any DLQ outcomes from the retry path so they can be
	// correlated with the refresh log lines above.
	logDLQs(ctx, "after-refresh", transactions, results)

	return results, nil
}

// DNM: logDLQs prints one line per DLQ'd txn with its writeset shape so we can
// correlate DLQ outcomes with refresh and decode-time logs.
func logDLQs(
	ctx context.Context, phase string, transactions []ldrdecoder.Transaction, results []ApplyResult,
) {
	for i := range results {
		if results[i].DlqReason == nil {
			continue
		}
		log.Dev.Infof(ctx,
			"DNM-apply: phase=%s txn=%s DLQ: %v; writeset=%s",
			phase, transactions[i].TxnID.Timestamp, results[i].DlqReason,
			formatWriteSetForLog(transactions[i].WriteSet))
	}
}

// DNM: formatWriteSetForLog renders a writeset for diagnostic logging.
func formatWriteSetForLog(rows []ldrdecoder.DecodedRow) string {
	parts := make([]string, len(rows))
	for i, r := range rows {
		op := "?"
		switch {
		case r.IsDeleteRow():
			op = "DELETE"
		case r.IsTombstoneUpdate():
			op = "TOMBSTONE"
		case r.IsInsertRow():
			op = "INSERT"
		case r.IsUpdateRow():
			op = "UPDATE"
		}
		parts[i] = fmt.Sprintf("%s(table=%d row_len=%d prev_len=%d)",
			op, r.TableID, len(r.Row), len(r.PrevRow))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func (tw *transactionWriter) tryApply(
	ctx context.Context, txn []ldrdecoder.Transaction, results []ApplyResult,
) error {
	for i, transaction := range txn {
		// NOTE: Rolling back savepoints only works if the transactions have
		// non-overlapping write sets. If two transactions write to the same row,
		// the previous value for the successor transaction is the value written by
		// the predecessor, which won't be correct if the predecessor is aborted.
		err := tw.session.Savepoint(ctx, func(ctx context.Context) error {
			applied, err := tw.tryApplyTransaction(ctx, transaction)
			results[i].AppliedRows = applied.AppliedRows
			// We accumulate the LWW losers since some of them were filtered out
			// during the refresh.
			results[i].LwwLoserRows = applied.LwwLoserRows + results[i].LwwLoserRows
			return err
		})
		if err != nil && sqlwriter.CanDLQError(err) != nil {
			return err
		} else if err != nil {
			results[i] = ApplyResult{DlqReason: err}
		}
	}
	return nil
}

func (tw *transactionWriter) tryApplyTransaction(
	ctx context.Context, transaction ldrdecoder.Transaction,
) (ApplyResult, error) {
	lwwLosers := 0
	for _, row := range transaction.WriteSet {
		tableWriter := tw.tableWriters[row.TableID]
		switch {
		case row.IsDeleteRow():
			err := tableWriter.DeleteRow(ctx, transaction.TxnID.Timestamp, row.PrevRow)
			if err != nil {
				return ApplyResult{}, err
			}
		case row.IsTombstoneUpdate():
			// TODO(jeffswenson): handle the tombstone update case. For ordered mode,
			// this case is only needed for racing updates.
		case row.IsInsertRow():
			err := tableWriter.InsertRow(ctx, transaction.TxnID.Timestamp, row.Row)
			if sqlwriter.IsLwwLoser(err) {
				lwwLosers++
				continue
			}
			if err != nil {
				return ApplyResult{}, err
			}
		case row.IsUpdateRow():
			err := tableWriter.UpdateRow(
				ctx,
				transaction.TxnID.Timestamp,
				row.PrevRow,
				row.Row,
			)
			if err != nil {
				return ApplyResult{}, err
			}
		default:
			return ApplyResult{}, errors.AssertionFailedf("unhandled row case: %v", row)
		}
	}
	result := ApplyResult{
		AppliedRows:  len(transaction.WriteSet) - lwwLosers,
		LwwLoserRows: lwwLosers,
	}
	return result, nil
}
