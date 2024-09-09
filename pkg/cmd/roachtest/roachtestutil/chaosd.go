// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package roachtestutil

import (
	"context"
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/roachprod/vm"
)

const chaosd = "sudo ./chaosd/chaosd"

func InstallChaosd(ctx context.Context, c cluster.Cluster, nodes option.NodeListOption) error {
	arch := "amd64"
	if c.Spec().Arch == vm.ArchARM64 {
		arch = "arm64"
	}
	if err := c.RunE(ctx, option.WithNodes(nodes), "test -e chaosd"); err == nil {
		return nil
	}
	const downloadChaosd = "curl -fsSL -o chaosd-v1.4.0-linux-%[1]s.tar.gz https://mirrors.chaos-mesh.org/chaosd-v1.4.0-linux-%[1]s.tar.gz"
	err := c.RunE(ctx, option.WithNodes(nodes), fmt.Sprintf(downloadChaosd, arch))
	if err != nil {
		return err
	}

	const unzipChaosd = "tar zxvf chaosd-v1.4.0-linux-%s.tar.gz"
	err = c.RunE(ctx, option.WithNodes(nodes), fmt.Sprintf(unzipChaosd, arch))
	if err != nil {
		return err
	}

	const renameChaosd = "mv chaosd-v1.4.0-linux-%s chaosd"
	err = c.RunE(ctx, option.WithNodes(nodes), fmt.Sprintf(renameChaosd, arch))
	if err != nil {
		return err
	}

	const installIPset = "sudo apt-get -qq install ipset"
	return c.RunE(ctx, option.WithNodes(nodes), installIPset)
}

// Successfully set the rules but doesn't work :/
func PartitionNode(
	ctx context.Context, c cluster.Cluster, l *logger.Logger, node option.NodeListOption,
) (string, error) {
	ip, err := c.InternalIP(ctx, l, node)
	if err != nil {
		return "", err
	}
	const partitionNode = chaosd + " attack network partition -i %s -d ens5"
	var allNodesExceptPartitioned option.NodeListOption
	for _, n := range c.CRDBNodes() {
		if node[0] != n {
			allNodesExceptPartitioned = append(allNodesExceptPartitioned, n)
		}
	}
	res, err := c.RunWithDetails(ctx, l, option.WithNodes(allNodesExceptPartitioned), fmt.Sprintf(partitionNode, ip[0]))
	if err != nil {
		return "", err
	}

	if res[0].Err != nil {
		return "", res[0].Err
	}

	l.Printf("partition node res: %s", res[0].Stdout)
	return res[0].Stdout, nil
}

// Seems flaky if it works or not, increasing the numbers would probably help
func DiskStallWrite(
	ctx context.Context, c cluster.Cluster, l *logger.Logger, node option.NodeListOption,
) (string, error) {
	const diskStallCmd = chaosd + " attack disk add-payload write -n 32 -p /mnt/data1/cockroach -s 100G"
	res, err := c.RunWithDetails(ctx, l, option.WithNodes(node), diskStallCmd)
	if err != nil {
		return "", err
	}

	if res[0].Err != nil {
		return "", res[0].Err
	}

	l.Printf("disk stall write node res: %s", res[0].Stdout)
	return res[0].Stdout, nil
}

func FindExperiments(ctx context.Context, c cluster.Cluster) error {
	const findExperiments = chaosd + " search --all"
	return c.RunE(ctx, option.WithNodes(c.CRDBNodes()), findExperiments)
}
