package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

type TPCCExtraOptions struct {
	// Use a existing database instead of creating one.
	databaseName         string
	rampDuration         time.Duration
	binaryPath           string
	skipConsistencyCheck bool
	// extraInitArgs allows passing additional arguments to the init command.
	extraInitArgs string

	// extraRunArgs allows passing additional arguments to the run command.
	extraRunArgs string
}

// TPCCOp implements the Operation interface for TPCC workload operations.
type TPCCOp struct {
	name       string
	builder    *modular.OperationBuilder
	warehouses int
	duration   time.Duration

	opts TPCCExtraOptions
}

// Chain returns the operation's chain of steps.
func (t *TPCCOp) Chain() modular.Chain {
	return t.builder.Chain
}

// Name returns the operation's name.
func (t *TPCCOp) Name() string {
	return t.name
}

func (t *TPCCOp) Precondition() bool {
	return true
}

func (t *TPCCOp) Timeout() time.Duration {
	// TODO: lets make this dynamic?
	return t.duration + 2*time.Hour
}

// TPCC creates a TPCC operation with the given options.
func TPCC(c cluster.Cluster, warehouses int, duration time.Duration, opts TPCCExtraOptions) modular.Operation {
	dbName := opts.databaseName
	binaryPath := test.DefaultCockroachPath
	if opts.binaryPath != "" {
		binaryPath = opts.binaryPath
	}

	builder := modular.NewOperation("init tpcc workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		var err error
		if dbName == "" {
			dbName, err = h.CreateDatabase("tpcc")
		}

		if err != nil {
			return err
		}
		cmd := roachtestutil.NewCommand("%s workload fixtures import tpcc", binaryPath).
			Flag("warehouses", warehouses).
			Flag("db", dbName).
			Arg("%s", opts.extraInitArgs).
			Arg("{pgurl:%d}", h.RandomAvailableNode()).
			String()

		return c.RunE(ctx, option.WithNodes(c.WorkloadNode()), cmd)
	}).Then("run tpcc workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		cmd := roachtestutil.NewCommand("%s workload run tpcc", binaryPath).
			Flag("warehouses", warehouses).
			Flag("db", dbName).
			MaybeFlag(opts.rampDuration > 0, "ramp", opts.rampDuration).
			Flag("duration", duration).
			Arg("%s", opts.extraRunArgs).
			Arg("{pgurl%s}", h.AvailableNodes()).
			String()

		return c.RunE(ctx, option.WithNodes(c.WorkloadNode()), cmd)
	}).MaybeThen(!opts.skipConsistencyCheck, "check tpcc workload", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		cmd := roachtestutil.NewCommand("%s workload check tpcc", binaryPath).
			Flag("warehouses", warehouses).
			Flag("db", dbName).
			Arg("{pgurl:%d}", h.RandomAvailableNode()).
			String()

		return c.RunE(ctx, option.WithNodes(c.WorkloadNode()), cmd)
	})

	return &TPCCOp{
		name:       fmt.Sprintf("tpcc/warehouses=%d/duration=%d", warehouses, duration),
		builder:    builder,
		warehouses: warehouses,
		duration:   duration,
		opts:       opts,
	}
}
