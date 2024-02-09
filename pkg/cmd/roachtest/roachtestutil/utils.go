// Copyright 2023 The Cockroach Authors.
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
	"strings"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/roachprod"
	"github.com/cockroachdb/cockroach/pkg/roachprod/config"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

// SystemInterfaceSystemdUnitName is a convenience function that
// returns the systemd unit name for the system interface
func SystemInterfaceSystemdUnitName() string {
	return install.VirtualClusterLabel(install.SystemInterfaceName, 0)
}

// SetDefaultSQLPort sets the SQL port to the default of 26257 if it is
// a non-local cluster. Local clusters don't support changing the port.
func SetDefaultSQLPort(c cluster.Cluster, opts *install.StartOpts) {
	if !c.IsLocal() {
		opts.SQLPort = config.DefaultSQLPort
	}
}

// SetDefaultAdminUIPort sets the AdminUI port to the default of 26258 if it is
// a non-local cluster. Local clusters don't support changing the port.
func SetDefaultAdminUIPort(c cluster.Cluster, opts *install.StartOpts) {
	if !c.IsLocal() {
		opts.AdminUIPort = config.DefaultAdminUIPort
	}
}

// CreateNewAdminUser is like CreateNewUser, but grants the user the admin role.
func CreateNewAdminUser(
	ctx context.Context,
	c cluster.Cluster,
	l *logger.Logger,
	node option.NodeListOption,
	user string,
	password string,
	auth install.PGAuthMode,
	args ...string,
) ([]string, error) {
	args = append(args, fmt.Sprintf(";GRANT ADMIN TO %s WITH ADMIN OPTION", user))
	return CreateNewUser(ctx, c, l, node, user, password, auth, args...)
}

// CreateNewUser is a convenience helper that creates a new user with a password
// and admin role if specified, as well as creating client certificates. It returns
// a list of external pgurls which can be used to authenticate with.
func CreateNewUser(
	ctx context.Context,
	c cluster.Cluster,
	l *logger.Logger,
	node option.NodeListOption,
	user string,
	password string,
	auth install.PGAuthMode,
	args ...string,
) ([]string, error) {
	cmd := fmt.Sprintf(`./cockroach sql --url={pgurl%s} -e "CREATE USER %s WITH PASSWORD '%s' %s"`, node, user, password, strings.Join(args, " "))
	if password == "" {
		cmd = fmt.Sprintf(`./cockroach sql --url={pgurl%s} -e "CREATE USER %s %s"`, node, user, strings.Join(args, " "))
	}

	if err := c.RunE(ctx, option.WithNodes(node), cmd); err != nil {
		return nil, err
	}

	// roachprod start already creates certificates for some users.
	// Trying to create certs again will error out.
	if user != "testuser" && user != "roach" {
		cmd = fmt.Sprintf(`./cockroach cert create-client %s --certs-dir=certs --ca-key=certs/ca.key`, user)
		if err := c.RunE(ctx, option.WithNodes(node), cmd); err != nil {
			return nil, err
		}
	}

	return roachprod.PgURL(ctx, l, c.MakeNodes(c.All()), "certs", roachprod.PGURLOptions{
		Auth: auth, Secure: c.IsSecure(), External: true, User: user, Password: password})
}
