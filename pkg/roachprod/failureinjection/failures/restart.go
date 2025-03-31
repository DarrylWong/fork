// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package failures

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/roachprod"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/roachprod/vm"
	"time"
)

type VMRestart struct {
	GenericFailure
}

type VMRestartArgs struct {
	GracefulShutdown bool
	Nodes            install.Nodes
}

const VMRestartName = "vm-restart"

func MakeVMRestart(clusterName string, l *logger.Logger, secure bool,
) (FailureMode, error) {
	c, err := roachprod.GetClusterFromCache(l, clusterName, install.SecureOption(secure))
	if err != nil {
		return nil, err
	}

	genericFailure := GenericFailure{c: c, runTitle: VMRestartName}
	return &VMRestart{GenericFailure: genericFailure}, nil
}

func registerVMRestart(r *FailureRegistry) {
	r.add(VMRestartName, DiskStallArgs{}, MakeVMRestart)
}

func (f VMRestart) Description() string {
	return VMRestartName
}

func (f VMRestart) Setup(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return nil
}

func (f VMRestart) Inject(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	restartArgs := args.(VMRestartArgs)
	if restartArgs.GracefulShutdown {
		if err := f.Run(ctx, l, restartArgs.Nodes, "sudo reboot"); err != nil {
			l.Printf("reboot cmd exited with: %v", err)
		}
		err := retryForDuration(ctx, 5*time.Minute, func() error {
			return f.Run(ctx, l, restartArgs.Nodes, "")
		})
		if err != nil {
			return err
		}
	} else {
		vms, err := f.InstallNodesToVMs(restartArgs.Nodes)
		if err != nil {
			return err
		}
		if err := vm.FanOut(vms, func(p vm.Provider, vms vm.List) error {
			return p.Reset(l, vms)
		}); err != nil {
			return err
		}
	}
	return f.StartNodes(ctx, l, restartArgs.Nodes)
}

func (f VMRestart) Recover(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return nil
}

func (f VMRestart) Cleanup(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return nil
}

func (f VMRestart) WaitForFailureToPropagate(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return nil
}

func (f VMRestart) WaitForFailureToRecover(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return nil
}
