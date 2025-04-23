// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package roachtestutil

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/roachprod/failureinjection/failures"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

type DiskStaller interface {
	Setup(ctx context.Context)
	Cleanup(ctx context.Context)
	Stall(ctx context.Context, nodes option.NodeListOption)
	Slow(ctx context.Context, nodes option.NodeListOption, bytesPerSecond int)
	Unstall(ctx context.Context, nodes option.NodeListOption)
	DataDir() string
	LogDir() string
}

type NoopDiskStaller struct{}

var _ DiskStaller = NoopDiskStaller{}

func (n NoopDiskStaller) Cleanup(ctx context.Context)                            {}
func (n NoopDiskStaller) DataDir() string                                        { return "{store-dir}" }
func (n NoopDiskStaller) LogDir() string                                         { return "logs" }
func (n NoopDiskStaller) Setup(ctx context.Context)                              {}
func (n NoopDiskStaller) Slow(_ context.Context, _ option.NodeListOption, _ int) {}
func (n NoopDiskStaller) Stall(_ context.Context, _ option.NodeListOption)       {}
func (n NoopDiskStaller) Unstall(_ context.Context, _ option.NodeListOption)     {}

type Fataler interface {
	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})
	L() *logger.Logger
}

type cgroupDiskStaller struct {
	failer *failures.Failer
	args   failures.DiskStallArgs
	f      Fataler
	c      cluster.Cluster
}

var _ DiskStaller = (*cgroupDiskStaller)(nil)

func MakeCgroupDiskStaller(f Fataler, c cluster.Cluster, readsToo bool, logsToo bool) DiskStaller {
	failer, err := GetFailer(c, failures.CgroupsDiskStallName, f.L())
	if err != nil {
		f.Fatal(err)
	}

	args := failures.DiskStallArgs{
		StallLogs:    logsToo,
		StallWrites:  true,
		StallReads:   readsToo,
		RestartNodes: false,
		Nodes:        c.CRDBNodes().InstallNodes(),
	}

	return &cgroupDiskStaller{
		failer: failer,
		args:   args,
		f:      f,
		c:      c,
	}
}

func (s *cgroupDiskStaller) DataDir() string { return "{store-dir}" }
func (s *cgroupDiskStaller) LogDir() string {
	return "logs"
}
func (s *cgroupDiskStaller) Setup(ctx context.Context) {
	if _, ok := s.c.Spec().ReusePolicy.(spec.ReusePolicyNone); !ok {
		// Safety measure.
		s.f.Fatalf("cluster needs ReusePolicyNone to support disk stalls")
	}

	s.args.Nodes = s.c.CRDBNodes().InstallNodes()
	if err := s.failer.Setup(ctx, s.f.L(), s.args); err != nil {
		s.f.Fatal(err)
	}
}
func (s *cgroupDiskStaller) Cleanup(ctx context.Context) {
	s.args.Nodes = s.c.CRDBNodes().InstallNodes()
	if err := s.failer.Cleanup(ctx, s.f.L()); err != nil {
		s.f.Fatal(err)
	}
}

func (s *cgroupDiskStaller) Stall(ctx context.Context, nodes option.NodeListOption) {
	s.args.Throughput = 0
	s.args.Nodes = nodes.InstallNodes()
	if err := s.failer.Inject(ctx, s.f.L(), s.args); err != nil {
		s.f.Fatal(err)
	}
}

func (s *cgroupDiskStaller) Slow(
	ctx context.Context, nodes option.NodeListOption, bytesPerSecond int,
) {
	s.args.Throughput = bytesPerSecond
	s.args.Nodes = nodes.InstallNodes()
	if err := s.failer.Inject(ctx, s.f.L(), s.args); err != nil {
		s.f.Fatal(err)
	}
}

func (s *cgroupDiskStaller) Unstall(ctx context.Context, nodes option.NodeListOption) {
	if err := s.failer.Recover(ctx, s.f.L()); err != nil {
		s.f.Fatal(err)
	}
}

func MakeDmsetupDiskStaller(f Fataler, c cluster.Cluster) DiskStaller {
	failureMode, err := GetFailureMode(c, failures.CgroupsDiskStallName, f.L())
	if err != nil {
		f.Fatal(err)
	}

	args := failures.DiskStallArgs{
		RestartNodes: false,
		Nodes:        c.CRDBNodes().InstallNodes(),
	}

	return &dmsetupDiskStaller{
		failureMode: failureMode,
		args:        args,
		f:           f,
		c:           c,
	}
}

type dmsetupDiskStaller struct {
	failureMode failures.FailureMode
	args        failures.DiskStallArgs
	f           Fataler
	c           cluster.Cluster
}

var _ DiskStaller = (*dmsetupDiskStaller)(nil)

func (s *dmsetupDiskStaller) Setup(ctx context.Context) {
	if _, ok := s.c.Spec().ReusePolicy.(spec.ReusePolicyNone); !ok {
		// We disable journaling and do all kinds of things below.
		s.f.Fatalf("cluster needs ReusePolicyNone to support disk stalls")
	}
	s.args.Nodes = s.c.CRDBNodes().InstallNodes()
	if err := s.failureMode.Setup(ctx, s.f.L(), s.args); err != nil {
		s.f.Fatal(err)
	}
}

func (s *dmsetupDiskStaller) Cleanup(ctx context.Context) {
	s.args.Nodes = s.c.CRDBNodes().InstallNodes()
	if err := s.failureMode.Cleanup(ctx, s.f.L(), s.args); err != nil {
		s.f.Fatal(err)
	}
}

func (s *dmsetupDiskStaller) Stall(ctx context.Context, nodes option.NodeListOption) {
	s.args.Nodes = nodes.InstallNodes()
	if err := s.failureMode.Inject(ctx, s.f.L(), s.args); err != nil {
		s.f.Fatal(err)
	}
}

func (s *dmsetupDiskStaller) Slow(
	ctx context.Context, nodes option.NodeListOption, bytesPerSecond int,
) {
	// TODO(baptist): Consider https://github.com/kawamuray/ddi.
	s.f.Fatal("Slow is not supported for dmsetupDiskStaller")
}

func (s *dmsetupDiskStaller) Unstall(ctx context.Context, nodes option.NodeListOption) {
	s.args.Nodes = nodes.InstallNodes()
	if err := s.failureMode.Recover(ctx, s.f.L(), s.args); err != nil {
		s.f.Fatal(err)
	}
}

func (s *dmsetupDiskStaller) DataDir() string { return "{store-dir}" }
func (s *dmsetupDiskStaller) LogDir() string  { return "logs" }
