// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package failures

import (
	"context"
	"fmt"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"strings"
	"time"
)

const CPUStressName = "cpu-stress"

func registerCPUStressNameFailure(r *FailureRegistry) {
	r.add(CPUStressName, CPUStressArgs{}, MakeCPUStressFailure)
}

func MakeCPUStressFailure(
	clusterName string, l *logger.Logger, connectionInfo ConnectionInfo,
) (FailureMode, error) {
	genericFailure, err := makeGenericFailure(clusterName, l, connectionInfo, CPUStressName)
	if err != nil {
		return nil, err
	}

	return &CPUStressFailure{GenericFailure: *genericFailure}, nil
}

type CPUStressFailure struct {
	GenericFailure
	systemdName string
}

type CPUStressArgs struct {
	Nodes install.Nodes
	// What CPU usage each worker will stress a CPU core to. stress-ng by default will round-
	// robin each worker amongst all cores, so the overall CPU usage can be thought of as
	// (LoadPerWorker * Workers) / CPU Cores. If unspecified, stress-ng defaults to 100.
	LoadPerWorker int
	// How many threads to stress CPU on. If unspecified, stress-ng defaults to the number of
	// cores on the VM.
	Workers int
	// Override the default stress-ng options the failureinjection framework uses.
	// By default, we use settings to ensure the CPU is saturated to the LoadPerWorker specified.
	// However, we keep this override if the user knows what they are doing with stress-ng.
	CustomStressNgOpts []string
}

// TODO: rename p to f
func (f CPUStressFailure) Description() string {
	return CPUStressName
}

func (f CPUStressFailure) Setup(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return install.Install(ctx, l, f.c, []string{"stress-ng"})
}

func buildStressCPUCmd(args CPUStressArgs) string {
	cmd := "stress-ng"
	cmd += fmt.Sprintf(" --cpu %d", args.Workers)
	if args.LoadPerWorker > 0 {
		cmd += fmt.Sprintf(" --cpu-load %d", args.LoadPerWorker)
	}
	if args.CustomStressNgOpts != nil {
		cmd += fmt.Sprintf(" %s", strings.Join(args.CustomStressNgOpts, " "))
	} else {
		// Without these two settings, stress-ng will struggle to saturate the CPU to
		// the LoadPerWorker specified.
		cmd += " --cpu-method sqrt" + " --cpu-load-slice 10"
	}
	return cmd
}

func (f CPUStressFailure) Inject(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	cpuArgs := args.(CPUStressArgs)
	unitName := fmt.Sprintf("stressng-%d", time.Now().Unix())
	f.systemdName = unitName
	cmd := fmt.Sprintf("sudo systemd-run  --unit=%s", unitName) + buildStressCPUCmd(cpuArgs)
	if l.File != nil {
		// Redirect output to the log file if it exists.
		cmd += fmt.Sprintf(" >> %s 2>&1", l.File.Name())
	}
	err := f.Run(ctx, l, cpuArgs.Nodes, cmd)
	if err != nil {
		return err
	}
	l.Printf("stress-ng service name: %s", f.systemdName)
	return nil
}

func (f CPUStressFailure) Recover(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	cpuArgs := args.(CPUStressArgs)
	if err := f.Run(ctx, l, cpuArgs.Nodes, fmt.Sprintf("sudo systemctl stop %s", f.systemdName)); err != nil {
		return err
	}
	return f.Run(ctx, l, cpuArgs.Nodes, fmt.Sprintf("sudo systemctl reset-failed %s", f.systemdName))
}

func (f CPUStressFailure) Cleanup(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return f.Recover(ctx, l, args)
}

func (f CPUStressFailure) WaitForFailureToPropagate(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	// TODO: block until the CPU is saturated to the LoadPerWorker specified.
	return nil
}

func (f CPUStressFailure) WaitForFailureToRecover(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	// TODO: block until the CPU is no longer saturated to the LoadPerWorker specified.
	return nil
}
