package operations

import (
	"context"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil"
	"github.com/cockroachdb/cockroach/pkg/roachprod"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"time"
)

// ReplicationFactorCycleOp implements the Operation interface for replication factor cycling.
type ReplicationFactorCycleOp struct {
	name    string
	builder *modular.OperationBuilder
}

// Chain returns the operation's chain of steps.
func (r *ReplicationFactorCycleOp) Chain() modular.Chain {
	return r.builder.Chain
}

// Name returns the operation's name.
func (r *ReplicationFactorCycleOp) Name() string {
	return r.name
}

func (r *ReplicationFactorCycleOp) Precondition() bool {
	return true
}

func (r *ReplicationFactorCycleOp) Timeout() time.Duration {
	// TODO: figure out how to make this more dynamic?
	return 5 * time.Hour
}

// ReplicationFactorCycle creates an operation that increases replication factor to 5,
// waits for replication to complete, then reduces it back to 3.
func ReplicationFactorCycle() modular.Operation {
	builder := modular.NewOperation("increase rebalance snapshot rate", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return h.SetClusterSetting("kv.snapshot_rebalance.max_rate", "2 GiB")
	}).Then("increase replication factor to 5", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return h.AlterAllRanges("num_replicas = 5")
	}).Then("wait for replication factor of 5", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		_, db := h.RandomDB()
		defer db.Close()
		return roachtestutil.WaitForReplication(ctx, l, db, 5, roachprod.AtLeastReplicationFactor)
	}).Then("decrease replication factor to 3", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return h.AlterAllRanges("num_replicas = 3")
	}).Then("wait for replication factor of 3", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		_, db := h.RandomDB()
		defer db.Close()
		return roachtestutil.WaitForReplication(ctx, l, db, 3, roachprod.AtLeastReplicationFactor)
	}).Then("restore rebalance snapshot rate", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		return h.ResetClusterSetting("kv.snapshot_rebalance.max_rate")
	})

	return &ReplicationFactorCycleOp{
		name:    "replication-factor-cycle",
		builder: builder,
	}
}
