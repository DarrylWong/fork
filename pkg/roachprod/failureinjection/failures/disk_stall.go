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

func MakeCgroupDiskStaller(clusterName string, l *logger.Logger) (FailureMode, error) {
	c, err := roachprod.GetClusterFromCache(l, clusterName)
	if err != nil {
		return nil, err
	}

	return &CGroupDiskStaller{c: c, l: l}, nil
}

func (s *CGroupDiskStaller) Run(ctx context.Context, args ...string) error {
	return s.c.Run(ctx, s.l, s.l.Stdout, s.l.Stderr, install.WithNodes(s.c.Nodes), "dmsetup", strings.Join(args, " "))
}

func (s *CGroupDiskStaller) RunOnSingleNode(ctx context.Context, args ...string) (install.RunResultDetails, error) {
	res, err := s.c.RunWithDetails(ctx, s.l, install.WithNodes(s.c.Nodes[:1]), "dmsetup", strings.Join(args, " "))
	if err != nil {
		return install.RunResultDetails{}, err
	}
	return res[0], nil
}

func (s *CGroupDiskStaller) Description() string {
	// TODO: add info about flags so it can be used in CLI later
	return "cgroup disk staller"
}

func (s *CGroupDiskStaller) Setup(ctx context.Context, args ...string) error {
	argsMap := parseArgs(args...)
	if _, ok := argsMap["logs-too"]; ok {
		if err := s.Run(ctx, "mkdir -p {store-dir}/logs"); err != nil {
			return err
		}
		if err := s.Run(ctx, "rm -f logs && ln -s {store-dir}/logs logs || true"); err != nil {
			return err
		}
	}

	s.readOrWrite = []bandwidthReadWrite{writeBandwidth}
	if _, ok := argsMap["reads-too"]; ok {
		s.readOrWrite = append(s.readOrWrite, readBandwidth)
	}
	return nil
}
func (s *CGroupDiskStaller) Cleanup(_ context.Context) error { return nil }

func (s *CGroupDiskStaller) Inject(ctx context.Context, args ...string) error {
	bytesPerSecond := "4"
	argsMap := parseArgs(args...)
	if bps, ok := argsMap["throughput"]; ok {
		bytesPerSecond = bps
	}

	return s.Slow(ctx, bytesPerSecond)
}

func (s *CGroupDiskStaller) Slow(
	ctx context.Context, bytesPerSecond string,
) error {
	// Shuffle the order of read and write stall initiation.
	rand.Shuffle(len(s.readOrWrite), func(i, j int) {
		s.readOrWrite[i], s.readOrWrite[j] = s.readOrWrite[j], s.readOrWrite[i]
	})
	for _, rw := range s.readOrWrite {
		if err := s.setThroughput(ctx, rw, throughput{limited: true, bytesPerSecond: bytesPerSecond}); err != nil {
			return err
		}
	}

	return nil
}

func (s *CGroupDiskStaller) Restore(ctx context.Context, _ ...string) error {
	for _, rw := range s.readOrWrite {
		err := s.setThroughput(ctx, rw, throughput{limited: false})
		if err != nil {
			s.l.PrintfCtx(ctx, "error unstalling the disk; stumbling on: %v", err)
		}
		// NB: We log the error and continue on because unstalling may not
		// succeed if the process has successfully exited.
	}
	return nil
}

func (s *CGroupDiskStaller) device(ctx context.Context) (major, minor int, err error) {
	// TODO(jackson): Programmatically determine the device major,minor numbers.
	// eg,:
	//    deviceName := getDevice(s.t, s.c)
	//    `cat /proc/partitions` and find `deviceName`
	res, err := s.RunOnSingleNode(ctx, "lsblk | grep /mnt/data1 | awk '{print $2}'")
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
	ctx context.Context, rw bandwidthReadWrite, bw throughput,
) error {
	maj, min, err := s.device(ctx)
	if err != nil {
		return err
	}
	cockroachIOController := filepath.Join("/sys/fs/cgroup/system.slice", install.VirtualClusterLabel(install.SystemInterfaceName, 0)+".service", "io.max")

	bytesPerSecondStr := "max"
	if bw.limited {
		bytesPerSecondStr = bw.bytesPerSecond
	}
	return s.Run(ctx, "sudo", "/bin/bash", "-c", fmt.Sprintf(
		`'echo %d:%d %s=%s > %s'`,
		maj,
		min,
		rw.cgroupV2BandwidthProp(),
		bytesPerSecondStr,
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

func MakeDmsetupDiskStaller(clusterName string, l *logger.Logger) (FailureMode, error) {
	c, err := roachprod.GetClusterFromCache(l, clusterName)
	if err != nil {
		return nil, err
	}

	return &DMSetupDiskStaller{c: c, l: l}, nil
}

func (s *DMSetupDiskStaller) Run(ctx context.Context, args ...string) error {
	return s.c.Run(ctx, s.l, s.l.Stdout, s.l.Stderr, install.WithNodes(s.c.Nodes), "dmsetup", strings.Join(args, " "))
}

func (s *DMSetupDiskStaller) Description() string {
	return "dmsetup disk staller"
}

func (s *DMSetupDiskStaller) Setup(ctx context.Context, args ...string) error {
	var err error
	if s.dev, err = GetDiskDevice(ctx, s.c, s.l); err != nil {
		return err
	}
	// snapd will run "snapd auto-import /dev/dm-0" via udev triggers when
	// /dev/dm-0 is created. This possibly interferes with the dmsetup create
	// reload, so uninstall snapd.
	if err = s.Run(ctx, `sudo apt-get purge -y snapd`); err != nil {
		return err
	}
	if err = s.Run(ctx, `sudo umount -f /mnt/data1 || true`); err != nil {
		return err
	}
	if err = s.Run(ctx, `sudo dmsetup remove_all`); err != nil {
		return err
	}
	// See https://github.com/cockroachdb/cockroach/issues/129619#issuecomment-2316147244.
	if err = s.Run(ctx, `sudo tune2fs -O ^has_journal `+s.dev); err != nil {
		return err
	}
	if err = s.Run(ctx, `echo "0 $(sudo blockdev --getsz `+s.dev+`) linear `+s.dev+` 0" | `+
		`sudo dmsetup create data1`); err != nil {

	}
	// This has occasionally been seen to fail with "Device or resource busy",
	// with no clear explanation. Try to find out who it is.
	if err = s.Run(ctx, "sudo bash -c 'ps aux; dmsetup status; mount; lsof'"); err != nil {
		return err
	}

	return s.Run(ctx, `sudo mount /dev/mapper/data1 /mnt/data1`)
}

func (s *DMSetupDiskStaller) Inject(ctx context.Context, _ ...string) error {
	return s.Run(ctx, `sudo dmsetup suspend --noflush --nolockfs data1`)
}

func (s *DMSetupDiskStaller) Restore(ctx context.Context, _ ...string) error {
	return s.Run(ctx, `sudo dmsetup resume data1`)
}

func (s *DMSetupDiskStaller) Cleanup(ctx context.Context) error {
	if err := s.Run(ctx, `sudo dmsetup resume data1`); err != nil {
		return err
	}
	if err := s.Run(ctx, `sudo umount /mnt/data1`); err != nil {
		return err
	}
	if err := s.Run(ctx, `sudo dmsetup remove_all`); err != nil {
		return err
	}
	if err := s.Run(ctx, `sudo tune2fs -O has_journal `+s.dev); err != nil {
		return err
	}
	if err := s.Run(ctx, `sudo mount /mnt/data1`); err != nil {
		return err
	}
	// Reinstall snapd in case subsequent tests need it.
	return s.Run(ctx, `sudo apt-get install -y snapd`)
}
