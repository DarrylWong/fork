// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package tenantdirsvr

import (
	"context"

	"github.com/cockroachdb/cockroach/pkg/ccl/sqlproxyccl/tenant"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/util/syncutil"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
	"github.com/gogo/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// TestMultiBackendDirectoryServer is a directory server that returns multiple
// pre-defined addresses for all tenants, enabling load balancing across
// backends. It is expected that SQL pods are listening on those addresses.
//
// The metadata of such tenants will not have a clusterName returned, so
// validation of cluster names through the directory cache will be skipped.
type TestMultiBackendDirectoryServer struct {
	// podAddrs refers to the addresses of the SQL pods. Each address consists
	// of both the host and port (e.g. "127.0.0.1:26257").
	podAddrs []string

	mu struct {
		syncutil.Mutex

		// deleted indicates that the tenant has been deleted. A NotFound
		// error will be returned when trying to resume a SQL pod, or read the
		// tenant's metadata.
		deleted map[roachpb.TenantID]struct{}
	}
}

var _ tenant.DirectoryServer = &TestMultiBackendDirectoryServer{}

// NewTestMultiBackendDirectoryServer constructs a new multi-backend directory
// server that supports load balancing across multiple pod addresses.
func NewTestMultiBackendDirectoryServer(podAddrs []string) (*TestMultiBackendDirectoryServer, *grpc.Server) {
	dir := &TestMultiBackendDirectoryServer{podAddrs: podAddrs}
	dir.mu.deleted = make(map[roachpb.TenantID]struct{})
	grpcServer := grpc.NewServer()
	tenant.RegisterDirectoryServer(grpcServer, dir)
	return dir, grpcServer
}

// ListPods returns a list of RUNNING pods, one for each configured address.
// The addresses are the same regardless of tenant ID. If the tenant has been
// deleted, no pods will be returned.
//
// ListPods implements the tenant.DirectoryServer interface.
func (d *TestMultiBackendDirectoryServer) ListPods(
	ctx context.Context, req *tenant.ListPodsRequest,
) (*tenant.ListPodsResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.mu.deleted[roachpb.MustMakeTenantID(req.TenantID)]; ok {
		return &tenant.ListPodsResponse{}, nil
	}

	// Create one pod for each address to enable load balancing across backends.
	pods := make([]*tenant.Pod, len(d.podAddrs))
	for i, addr := range d.podAddrs {
		pods[i] = &tenant.Pod{
			TenantID:       req.TenantID,
			Addr:           addr,
			State:          tenant.RUNNING,
			StateTimestamp: timeutil.Now(),
		}
	}

	return &tenant.ListPodsResponse{
		Pods: pods,
	}, nil
}

// WatchPods is a no-op for the multi-backend directory.
//
// WatchPods implements the tenant.DirectoryServer interface.
func (d *TestMultiBackendDirectoryServer) WatchPods(
	req *tenant.WatchPodsRequest, server tenant.Directory_WatchPodsServer,
) error {
	// Instead of returning right away, we block until context is done.
	// This prevents the proxy server from constantly trying to establish
	// a watch in test environments, causing spammy logs.
	<-server.Context().Done()
	return nil
}

// EnsurePod is a no-op for the multi-backend directory since it assumes that
// SQL pods are actively listening at the associated pod addresses. However, if
// the tenant has been deleted, a GRPC NotFound error will be returned. This
// would mimic the behavior that we have in the actual tenant directory.
//
// EnsurePod implements the tenant.DirectoryServer interface.
func (d *TestMultiBackendDirectoryServer) EnsurePod(
	ctx context.Context, req *tenant.EnsurePodRequest,
) (*tenant.EnsurePodResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.mu.deleted[roachpb.MustMakeTenantID(req.TenantID)]; ok {
		return nil, status.Errorf(codes.NotFound, "tenant has been deleted")
	}
	return &tenant.EnsurePodResponse{}, nil
}

// GetTenant returns an empty response regardless of tenants. However, if the
// tenant has been deleted, a GRPC NotFound error will be returned.
//
// GetTenant implements the tenant.DirectoryServer interface.
func (d *TestMultiBackendDirectoryServer) GetTenant(
	ctx context.Context, req *tenant.GetTenantRequest,
) (*tenant.GetTenantResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.mu.deleted[roachpb.MustMakeTenantID(req.TenantID)]; ok {
		return nil, status.Errorf(codes.NotFound, "tenant has been deleted")
	}
	return &tenant.GetTenantResponse{
		Tenant: &tenant.Tenant{
			TenantID: req.TenantID,
			// Note that we do not return a ClusterName field here. Doing this
			// skips the clusterName validation in the directory cache which
			// makes testing easier.
			//
			// If we hardcoded a cluster name here, all connection strings will
			// need to be updated to use that cluster name, including the one
			// used by the ORM tests, which is currently hardcoded to
			// "prancing-pony": https://github.com/cockroachdb/cockroach-go/blob/e1659d1d/testserver/tenant.go#L244
			ClusterName:       "",
			AllowedCIDRRanges: []string{"0.0.0.0/0"},
		},
	}, nil
}

// WatchTenants is a no-op for the multi-backend directory.
//
// WatchTenants implements the tenant.DirectoryServer interface.
func (d *TestMultiBackendDirectoryServer) WatchTenants(
	req *tenant.WatchTenantsRequest, server tenant.Directory_WatchTenantsServer,
) error {
	// Instead of returning right away, we block until context is done.
	// This prevents the proxy server from constantly trying to establish
	// a watch in test environments, causing spammy logs.
	<-server.Context().Done()
	return nil
}

// DeleteTenant marks the given tenant as deleted, so that a NotFound error
// will be returned for certain directory server endpoints. This also changes
// the behavior of ListPods so no pods are returned.
func (d *TestMultiBackendDirectoryServer) DeleteTenant(tenantID roachpb.TenantID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mu.deleted[tenantID] = struct{}{}
}
