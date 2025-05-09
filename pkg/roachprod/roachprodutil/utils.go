// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package roachprodutil

import (
	"github.com/cockroachdb/errors"
	"strconv"
	"strings"
)

func GetDiskDeviceCmd() string {
	return "lsblk -o NAME,MAJ:MIN,MOUNTPOINTS | grep /mnt/data1 | awk '{print $1, $2}'"
}

func ParseDiskDeviceResult(res string) (name string, major int, minor int, err error) {
	parts := strings.Split(strings.TrimSpace(res), " ")
	if len(parts) != 2 {
		return "", 0, 0, errors.Newf("unexpected output from lsblk: %s", res)
	}
	name = strings.TrimSpace(parts[0])
	majorStr, minorStr, found := strings.Cut(parts[1], ":")
	if !found {
		return "", 0, 0, errors.Newf("unexpected output from lsblk: %s", res)
	}
	if major, err = strconv.Atoi(majorStr); err != nil {
		return "", 0, 0, err
	}
	if minor, err = strconv.Atoi(minorStr); err != nil {
		return "", 0, 0, err
	}

	return name, major, minor, nil
}
