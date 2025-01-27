// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package failures

import (
	"context"
	"fmt"
	"github.com/cockroachdb/cockroach/pkg/roachprod"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/errors"
	"math/rand"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

type CGroupDiskStaller struct {
	c           *install.SyncedCluster
	l           *logger.Logger
	readOrWrite []bandwidthReadWrite
}

func MakeCgroupDiskStaller(clusterName string, l *logger.Logger, secure bool) (FailureMode, error) {
	c, err := roachprod.GetClusterFromCache(l, clusterName, install.SecureOption(secure))
	if err != nil {
		return nil, err
	}

	return &CGroupDiskStaller{c: c, l: l}, nil
}

func registerCgroupDiskStall(r *FailureRegistry) {
	r.add("cgroup-disk-stall", DiskStallArgs{}, MakeCgroupDiskStaller)
}

func (s *CGroupDiskStaller) run(ctx context.Context, nodes install.Nodes, args ...string) error {
	cmd := strings.Join(args, " ")
	s.l.Printf("cgroup: %s", cmd)
	return s.c.Run(ctx, s.l, s.l.Stdout, s.l.Stderr, install.WithNodes(nodes), fmt.Sprintf("cgroup: %s", cmd), cmd)
}

func (s *CGroupDiskStaller) runOnSingleNode(ctx context.Context, node install.Nodes, args ...string) (install.RunResultDetails, error) {
	cmd := strings.Join(args, " ")
	res, err := s.c.RunWithDetails(ctx, s.l, install.WithNodes(node), fmt.Sprintf("cgroup: %s", cmd), cmd)
	if err != nil {
		return install.RunResultDetails{}, err
	}
	return res[0], nil
}

func (s *CGroupDiskStaller) Description() string {
	// TODO: add info about failure so it can be used in CLI later
	return "cgroup disk staller"
}

type DiskStallArgs struct {
	LogsToo    bool
	ReadsToo   bool
	Throughput int
	Nodes      install.Nodes
}

func (a DiskStallArgs) Description() []string {
	return []string{
		"LogsToo: limit throughput to logs directory",
		"ReadsToo: limit read throughput",
		"Throughput: throughput in bytes per second, default is 4 if unspecified",
		"Nodes: nodes to stall",
	}
}

func (s *CGroupDiskStaller) Setup(ctx context.Context, args FailureArgs) error {
	if args.(DiskStallArgs).LogsToo {
		if err := s.run(ctx, s.c.Nodes, "mkdir -p {store-dir}/logs"); err != nil {
			return err
		}
		if err := s.run(ctx, s.c.Nodes, "rm -f logs && ln -s {store-dir}/logs logs || true"); err != nil {
			return err
		}
	}

	s.readOrWrite = []bandwidthReadWrite{writeBandwidth}
	if args.(DiskStallArgs).ReadsToo {
		s.readOrWrite = append(s.readOrWrite, readBandwidth)
	}
	return nil
}
func (s *CGroupDiskStaller) Cleanup(_ context.Context) error { return nil }

func (s *CGroupDiskStaller) Inject(ctx context.Context, args FailureArgs) error {
	// NB: I don't understand why, but attempting to set a bytesPerSecond={0,1}
	// results in Invalid argument from the io.max cgroupv2 API.
	bytesPerSecond := 4
	if args.(DiskStallArgs).Throughput > 0 {
		bytesPerSecond = args.(DiskStallArgs).Throughput
	}

	var nodes install.Nodes
	if nodes = args.(DiskStallArgs).Nodes; nodes == nil {
		nodes = s.c.Nodes
	}

	// Shuffle the order of read and write stall initiation.
	rand.Shuffle(len(s.readOrWrite), func(i, j int) {
		s.readOrWrite[i], s.readOrWrite[j] = s.readOrWrite[j], s.readOrWrite[i]
	})

	if err := s.setThroughput(ctx, s.readOrWrite, throughput{limited: true, bytesPerSecond: fmt.Sprintf("%d", bytesPerSecond)}, nodes); err != nil {
		return err
	}

	return nil
}

// Restore removes the cgroup disk stall.
//
// N.B. Caller must ensure to pass the same flags as called in Inject or the node may be left in a
// bad state. Although the cgroups v2 documentation suggests that any of the limits can be set
// independently, it appears that trying to unthrottle them independently deletes the io
// controller entirely, leaving the node in a bad state. For some reason, this doesn't
// happen if we unlimit all changed values at the same time, i.e. echo rbps=max > io.max
// && echo wbps=max > io.max deletes the controller, but echo rbps=max wbps=max > io.max does not.
func (s *CGroupDiskStaller) Restore(ctx context.Context, args FailureArgs) error {
	var nodes install.Nodes
	if nodes = args.(DiskStallArgs).Nodes; nodes == nil {
		nodes = s.c.Nodes
	}

	err := s.setThroughput(ctx, s.readOrWrite, throughput{limited: false}, nodes)
	if err != nil {
		// NB: We log the error and continue on because unstalling may not
		// succeed if the process has successfully exited.
		s.l.PrintfCtx(ctx, "error unstalling the disk; stumbling on: %v", err)
	}
	return nil
}

func (s *CGroupDiskStaller) device(ctx context.Context, node install.Nodes) (major, minor int, err error) {
	// TODO(jackson): Programmatically determine the device major,minor numbers.
	// eg,:
	//    deviceName := getDevice(s.t, s.c)
	//    `cat /proc/partitions` and find `deviceName`
	res, err := s.runOnSingleNode(ctx, node, "lsblk | grep /mnt/data1 | awk '{print $2}'")
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Split(strings.TrimSpace(res.Stdout), ":")
	if len(parts) != 2 {
		return 0, 0, errors.Newf("unexpected output from lsblk: %s", res.Stdout)
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, errors.Wrapf(err, "error when determining block device")
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, errors.Wrapf(err, "error when determining block device")
	}
	return major, minor, nil
}

type throughput struct {
	limited        bool
	bytesPerSecond string
}

type bandwidthReadWrite int8

const (
	readBandwidth bandwidthReadWrite = iota
	writeBandwidth
)

func (rw bandwidthReadWrite) cgroupV2BandwidthProp() string {
	switch rw {
	case readBandwidth:
		return "rbps"
	case writeBandwidth:
		return "wbps"
	default:
		panic("unreachable")
	}
}

func (s *CGroupDiskStaller) setThroughput(
	ctx context.Context, readOrWrite []bandwidthReadWrite, bw throughput, nodes install.Nodes,
) error {
	maj, min, err := s.device(ctx, nodes)
	if err != nil {
		return err
	}
	cockroachIOController := filepath.Join("/sys/fs/cgroup/system.slice", install.VirtualClusterLabel(install.SystemInterfaceName, 0)+".service", "io.max")

	var limits []string
	for _, rw := range readOrWrite {
		bytesPerSecondStr := "max"
		if bw.limited {
			bytesPerSecondStr = bw.bytesPerSecond
		}
		limits = append(limits, fmt.Sprintf("%s=%s", rw.cgroupV2BandwidthProp(), bytesPerSecondStr))
	}

	return s.run(ctx, nodes, "sudo", "/bin/bash", "-c", fmt.Sprintf(
		`'echo %d:%d %s > %s'`,
		maj,
		min,
		strings.Join(limits, " "),
		cockroachIOController,
	))
}

func GetDiskDevice(ctx context.Context, c *install.SyncedCluster, l *logger.Logger) (string, error) {
	res, err := c.RunWithDetails(ctx, l, install.WithNodes(c.Nodes[:1]), "Get Disk Device", "lsblk | grep /mnt/data1 | awk '{print $1}'")
	if err != nil {
		return "", errors.Wrapf(err, "error when determining block device")
	}
	return "/dev/" + strings.TrimSpace(res[0].Stdout), nil
}

type DMSetupDiskStaller struct {
	c *install.SyncedCluster
	l *logger.Logger

	dev string // set in Setup; s.device() doesn't work when volume is not set up
}

func MakeDmsetupDiskStaller(clusterName string, l *logger.Logger, secure bool) (FailureMode, error) {
	c, err := roachprod.GetClusterFromCache(l, clusterName, install.SecureOption(secure))
	if err != nil {
		return nil, err
	}

	return &DMSetupDiskStaller{c: c, l: l}, nil
}

func (s *DMSetupDiskStaller) run(ctx context.Context, nodes install.Nodes, args ...string) error {
	cmd := strings.Join(args, " ")
	return s.c.Run(ctx, s.l, s.l.Stdout, s.l.Stderr, install.WithNodes(nodes), fmt.Sprintf("dmsetup: %s", cmd), cmd)
}

func (s *DMSetupDiskStaller) Description() string {
	return "dmsetup disk staller"
}

func (s *DMSetupDiskStaller) Setup(ctx context.Context, _ FailureArgs) error {
	var err error
	if s.dev, err = GetDiskDevice(ctx, s.c, s.l); err != nil {
		return err
	}
	// snapd will run "snapd auto-import /dev/dm-0" via udev triggers when
	// /dev/dm-0 is created. This possibly interferes with the dmsetup create
	// reload, so uninstall snapd.
	if err = s.run(ctx, s.c.Nodes, `sudo apt-get purge -y snapd`); err != nil {
		return err
	}
	if err = s.run(ctx, s.c.Nodes, `sudo umount -f /mnt/data1 || true`); err != nil {
		return err
	}
	if err = s.run(ctx, s.c.Nodes, `sudo dmsetup remove_all`); err != nil {
		return err
	}
	// See https://github.com/cockroachdb/cockroach/issues/129619#issuecomment-2316147244.
	if err = s.run(ctx, s.c.Nodes, `sudo tune2fs -O ^has_journal `+s.dev); err != nil {
		return err
	}
	if err = s.run(ctx, s.c.Nodes, `echo "0 $(sudo blockdev --getsz `+s.dev+`) linear `+s.dev+` 0" | `+
		`sudo dmsetup create data1`); err != nil {

	}
	// This has occasionally been seen to fail with "Device or resource busy",
	// with no clear explanation. Try to find out who it is.
	if err = s.run(ctx, s.c.Nodes, "sudo bash -c 'ps aux; dmsetup status; mount; lsof'"); err != nil {
		return err
	}

	return s.run(ctx, s.c.Nodes, `sudo mount /dev/mapper/data1 /mnt/data1`)
}

func (s *DMSetupDiskStaller) Inject(ctx context.Context, args FailureArgs) error {
	nodes := args.(DiskStallArgs).Nodes
	return s.run(ctx, nodes, `sudo dmsetup suspend --noflush --nolockfs data1`)
}

func (s *DMSetupDiskStaller) Restore(ctx context.Context, args FailureArgs) error {
	nodes := args.(DiskStallArgs).Nodes
	return s.run(ctx, nodes, `sudo dmsetup resume data1`)
}

func (s *DMSetupDiskStaller) Cleanup(ctx context.Context) error {
	if err := s.run(ctx, s.c.Nodes, `sudo dmsetup resume data1`); err != nil {
		return err
	}
	if err := s.run(ctx, s.c.Nodes, `sudo umount /mnt/data1`); err != nil {
		return err
	}
	if err := s.run(ctx, s.c.Nodes, `sudo dmsetup remove_all`); err != nil {
		return err
	}
	if err := s.run(ctx, s.c.Nodes, `sudo tune2fs -O has_journal `+s.dev); err != nil {
		return err
	}
	if err := s.run(ctx, s.c.Nodes, `sudo mount /mnt/data1`); err != nil {
		return err
	}
	// Reinstall snapd in case subsequent tests need it.
	return s.run(ctx, s.c.Nodes, `sudo apt-get install -y snapd`)
}

func registerDmsetupDiskStall(r *FailureRegistry) {
	r.add("dmsetup-disk-stall", DiskStallArgs{}, MakeDmsetupDiskStaller)
}
