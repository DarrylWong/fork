// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package failureinjection

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil"
	"github.com/cockroachdb/cockroach/pkg/roachprod/failureinjection/failures"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
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
	f           Fataler
	c           cluster.Cluster
	readsToo    bool
	logsToo     bool
	diskStaller failures.FailureMode
}

var _ DiskStaller = (*cgroupDiskStaller)(nil)

func MakeCgroupDiskStaller(f Fataler, c cluster.Cluster, readsToo bool, logsToo bool) DiskStaller {
	diskStaller, err := failures.MakeCgroupDiskStaller(c.MakeNodes(), f.L(), c.IsSecure())
	if err != nil {
		f.Fatalf("failed to create cgroup disk staller: %v", err)
	}
	return &cgroupDiskStaller{f: f, c: c, readsToo: readsToo, logsToo: logsToo, diskStaller: diskStaller}
}

func (s *cgroupDiskStaller) DataDir() string { return "{store-dir}" }
func (s *cgroupDiskStaller) LogDir() string {
	return "logs"
}
func (s *cgroupDiskStaller) Setup(ctx context.Context) {
	l, logfile, err := roachtestutil.LoggerForCmd(s.f.L(), s.c.All(), "cgroup-setup")
	if err != nil {
		s.f.Fatalf("failed to create logger for cgroup disk staller: %v", err)
	}
	defer l.Close()
	s.f.L().Printf("setting up cgroups disk staller; details in: %s.log", logfile)

	if _, ok := s.c.Spec().ReusePolicy.(spec.ReusePolicyNone); !ok {
		// Safety measure.
		s.f.Fatalf("cluster needs ReusePolicyNone to support disk stalls")
	}
	args := failures.DiskStallArgs{
		// For now, all roachtests want to always stall writes. In the future
		// we might want to make this configurable.
		StallWrites: true,
		StallLogs:   s.logsToo,
		StallReads:  s.readsToo,
	}
	if err := s.diskStaller.Setup(ctx, l, args); err != nil {
		s.f.Fatalf("error setting up the disk staller: %v", err)
	}
}
func (s *cgroupDiskStaller) Cleanup(ctx context.Context) {
	l, logfile, err := roachtestutil.LoggerForCmd(s.f.L(), s.c.All(), "cgroup-cleanup")
	if err != nil {
		s.f.Fatalf("failed to create logger for cgroup disk staller: %v", err)
	}
	defer l.Close()
	s.f.L().Printf("cleaning up cgroups disk staller; details in: %s.log", logfile)

	if err := s.diskStaller.Cleanup(ctx, l); err != nil {
		s.f.Fatalf("error cleaning up the disk staller: %v", err)
	}
}

func (s *cgroupDiskStaller) Stall(ctx context.Context, nodes option.NodeListOption) {
	l, logfile, err := roachtestutil.LoggerForCmd(s.f.L(), s.c.All(), "cgroup-stall")
	if err != nil {
		s.f.Fatalf("failed to create logger for cgroup disk staller: %v", err)
	}
	defer l.Close()
	s.f.L().Printf("stalling disks on nodes: %s; details in: %s.log", nodes, logfile)

	args := failures.DiskStallArgs{
		Nodes: nodes.InstallNodes(),
	}
	if err := s.diskStaller.Inject(ctx, l, args); err != nil {
		s.f.Fatalf("error stalling the disk: %v", err)
	}
}

func (s *cgroupDiskStaller) Slow(
	ctx context.Context, nodes option.NodeListOption, bytesPerSecond int,
) {
	l, logfile, err := roachtestutil.LoggerForCmd(s.f.L(), s.c.All(), "cgroup-slow")
	if err != nil {
		s.f.Fatalf("failed to create logger for cgroup disk staller: %v", err)
	}
	defer l.Close()
	s.f.L().Printf("slowing disks on nodes: %s; details in: %s.log", nodes, logfile)

	args := failures.DiskStallArgs{
		Throughput: bytesPerSecond,
		Nodes:      nodes.InstallNodes(),
	}
	if err := s.diskStaller.Inject(ctx, l, args); err != nil {
		s.f.Fatalf("error slowing the disk: %v", err)
	}
}

func (s *cgroupDiskStaller) Unstall(ctx context.Context, nodes option.NodeListOption) {
	l, logfile, err := roachtestutil.LoggerForCmd(s.f.L(), s.c.All(), "cgroup-unstall")
	if err != nil {
		s.f.Fatalf("failed to create logger for cgroup disk staller: %v", err)
	}
	defer l.Close()
	s.f.L().Printf("unstalling disks on nodes: %s; details in: %s.log", nodes, logfile)

	args := failures.DiskStallArgs{
		Nodes: nodes.InstallNodes(),
	}
	if err := s.diskStaller.Restore(ctx, l, args); err != nil {
		s.f.Fatalf("error slowing the disk: %v", err)
	}
}

type dmsetupDiskStaller struct {
	f           Fataler
	c           cluster.Cluster
	diskStaller failures.FailureMode

	dev string // set in Setup; s.device() doesn't work when volume is not set up
}

var _ DiskStaller = (*dmsetupDiskStaller)(nil)

func (s *dmsetupDiskStaller) Setup(ctx context.Context) {
	l, logfile, err := roachtestutil.LoggerForCmd(s.f.L(), s.c.All(), "dmsetup-setup")
	if err != nil {
		s.f.Fatalf("failed to create logger for dmsetup disk staller: %v", err)
	}
	defer l.Close()
	s.f.L().Printf("setting up dmsetup; details in: %s.log", logfile)

	if _, ok := s.c.Spec().ReusePolicy.(spec.ReusePolicyNone); !ok {
		// We disable journaling and do all kinds of things below.
		s.f.Fatalf("cluster needs ReusePolicyNone to support disk stalls")
	}
	if err := s.diskStaller.Setup(ctx, l, failures.DiskStallArgs{}); err != nil {
		s.f.Fatalf("error setting up the disk staller: %v", err)
	}
}

func (s *dmsetupDiskStaller) Cleanup(ctx context.Context) {
	l, logfile, err := roachtestutil.LoggerForCmd(s.f.L(), s.c.All(), "dmsetup-cleanup")
	if err != nil {
		s.f.Fatalf("failed to create logger for dmsetup disk staller: %v", err)
	}
	defer l.Close()
	s.f.L().Printf("cleaning up dmsetup; details in: %s.log", logfile)

	if err := s.diskStaller.Cleanup(ctx, l); err != nil {
		s.f.Fatalf("error cleaning up the disk staller: %v", err)
	}
}

func (s *dmsetupDiskStaller) Stall(ctx context.Context, nodes option.NodeListOption) {
	l, logfile, err := roachtestutil.LoggerForCmd(s.f.L(), s.c.All(), "dmsetup-stall")
	if err != nil {
		s.f.Fatalf("failed to create logger for dmsetup disk staller: %v", err)
	}
	defer l.Close()
	s.f.L().Printf("stalling disks on nodes: %s; details in: %s.log", nodes, logfile)

	if err := s.diskStaller.Inject(ctx, l, failures.DiskStallArgs{Nodes: nodes.InstallNodes()}); err != nil {
		s.f.Fatalf("error stalling the disk: %v", err)
	}
}

func (s *dmsetupDiskStaller) Slow(
	ctx context.Context, nodes option.NodeListOption, bytesPerSecond int,
) {
	l, logfile, err := roachtestutil.LoggerForCmd(s.f.L(), s.c.All(), "dmsetup-slow")
	if err != nil {
		s.f.Fatalf("failed to create logger for dmsetup disk staller: %v", err)
	}
	defer l.Close()
	s.f.L().Printf("slowing disks on nodes: %s; details in: %s.log", nodes, logfile)

	args := failures.DiskStallArgs{
		Throughput: bytesPerSecond,
		Nodes:      nodes.InstallNodes(),
	}

	if err := s.diskStaller.Inject(ctx, l, args); err != nil {
		s.f.Fatalf("error slowing the disk: %v", err)
	}
}

func (s *dmsetupDiskStaller) Unstall(ctx context.Context, nodes option.NodeListOption) {
	l, logfile, err := roachtestutil.LoggerForCmd(s.f.L(), s.c.All(), "dmsetup-unstall")
	if err != nil {
		s.f.Fatalf("failed to create logger for dmsetup disk staller: %v", err)
	}
	defer l.Close()
	s.f.L().Printf("unstalling disks on nodes: %s; details in: %s.log", nodes, logfile)

	if err := s.diskStaller.Restore(ctx, l, failures.DiskStallArgs{Nodes: nodes.InstallNodes()}); err != nil {
		s.f.Fatalf("error unstalling the disk: %v", err)
	}
}

func (s *dmsetupDiskStaller) DataDir() string { return "{store-dir}" }
func (s *dmsetupDiskStaller) LogDir() string  { return "logs" }

func MakeDmsetupDiskStaller(f Fataler, c cluster.Cluster) DiskStaller {
	diskStaller, err := failures.MakeDmsetupDiskStaller(c.MakeNodes(), f.L(), c.IsSecure())
	if err != nil {
		f.Fatalf("failed to create dmsetup disk staller: %v", err)
	}
	return &dmsetupDiskStaller{f: f, c: c, diskStaller: diskStaller}
}
