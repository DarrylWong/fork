package roachtestutil

import (
	"bytes"
	"context"
	gosql "database/sql"
	"encoding/json"
	"fmt"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"io"
	"net/http"
)

// SQLProxy is a helper to help spin up SQL proxy and a directory service for roachtests.
type SQLProxy struct {
	c             cluster.Cluster
	l             *logger.Logger
	tenantIDCache map[string]int
	proxyOpts     install.SQLProxyOpts
	proxyNode     option.NodeListOption
	directoryNode option.NodeListOption
	dirOpts       install.DirectoryServerOpts
	httpClient    *http.Client
	httpURL       string
}

func NewSQLProxy(
	c cluster.Cluster,
	l *logger.Logger,
	proxyNode option.NodeListOption,
	directoryNode option.NodeListOption,
) *SQLProxy {
	return &SQLProxy{
		c:             c,
		l:             l,
		tenantIDCache: make(map[string]int),
		proxyNode:     proxyNode,
		directoryNode: directoryNode,
		httpClient:    http.DefaultClient,
	}
}

// Start starts both the directory server and the SQL proxy.
func (p *SQLProxy) Start(ctx context.Context) error {
	p.dirOpts = install.DirectoryServerOpts{}
	err := p.c.StartProxyDirectory(ctx, p.l, p.directoryNode, p.dirOpts)
	if err != nil {
		return err
	}
	externalIPs, err := p.c.ExternalIP(ctx, p.l, p.directoryNode)
	if err != nil {
		return err
	}
	internalIPs, err := p.c.InternalIP(ctx, p.l, p.directoryNode)
	if err != nil {
		return err
	}

	p.httpURL = fmt.Sprintf("http://%s:%d", externalIPs[0], install.DirectoryServerHTTPPort(p.dirOpts))
	p.proxyOpts = install.SQLProxyOpts{
		DirectoryAddr: fmt.Sprintf("%s:%d", internalIPs[0], install.DirectoryServerGRPCPort(p.dirOpts)),
		Insecure:      !p.c.IsSecure(),
		SkipVerify:    true,
	}
	return p.c.StartProxy(ctx, p.l, p.proxyNode, p.proxyOpts)
}

// AddTenant adds a new tenant to the directory server.
func (p *SQLProxy) AddTenant(ctx context.Context, name string) error {
	// Enable session revival tokens for connection migration
	db := p.c.Conn(ctx, p.l, 1)
	defer db.Close()
	_, err := db.ExecContext(ctx, `ALTER TENANT $1 SET CLUSTER SETTING server.user_login.session_revival_token.enabled = true`, name)
	if err != nil {
		return err
	}

	tenantID, err := TenantID(ctx, p.l, p.c, name)
	if err != nil {
		return err
	}

	reqBody := map[string]interface{}{
		"tenant_id":           tenantID,
		"name":                name,
		"allowed_cidr_ranges": []string{"0.0.0.0/0"}, // Allow all IPs for roachtests
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/api/create-tenant", p.httpURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create tenant failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	p.l.Printf("Created tenant %d (%s) in directory server", tenantID, name)
	return nil
}

// AddPod adds a new pod for the specified tenant in the directory server.
func (p *SQLProxy) AddPod(ctx context.Context, nodes option.NodeListOption, virtualClusterName string, sqlInstance int) error {
	tenantID, err := TenantID(ctx, p.l, p.c, virtualClusterName)
	if err != nil {
		return err
	}
	addrs, err := p.PodAddr(ctx, nodes, virtualClusterName, sqlInstance)
	if err != nil {
		return err
	}

	for _, addr := range addrs {
		reqBody := map[string]interface{}{
			"tenant_id": tenantID,
			"addr":      addr,
		}

		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}

		url := fmt.Sprintf("%s/api/add-pod", p.httpURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.httpClient.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("add pod failed: status=%d, body=%s", resp.StatusCode, string(body))
		}
		resp.Body.Close()

		p.l.Printf("Added pod %s to tenant %s", addr, virtualClusterName)
	}
	return nil
}

func (p *SQLProxy) RemovePod(ctx context.Context, nodes option.NodeListOption, virtualClusterName string, sqlInstance int) error {
	tenantID, err := TenantID(ctx, p.l, p.c, virtualClusterName)
	if err != nil {
		return err
	}
	addrs, err := p.PodAddr(ctx, nodes, virtualClusterName, sqlInstance)
	if err != nil {
		return err
	}

	for _, addr := range addrs {
		reqBody := map[string]interface{}{
			"tenant_id": tenantID,
			"addr":      addr,
		}

		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}

		url := fmt.Sprintf("%s/api/remove-pod", p.httpURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.httpClient.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("remove pod failed: status=%d, body=%s", resp.StatusCode, string(body))
		}
		resp.Body.Close()

		p.l.Printf("Removed pod %s from tenant %s", addr, virtualClusterName)
	}
	return nil
}

func (p *SQLProxy) DrainPod(ctx context.Context, nodes option.NodeListOption, virtualClusterName string, sqlInstance int) error {
	// TODO(darryl): CRITICAL BUG - The proxy's directory cache does not refresh when pod states
	// change via the HTTP API. After calling DrainPod here, the directory server correctly
	// marks pods as DRAINING (confirmed via /info endpoint), but the proxy's balancer still
	// sees them as RUNNING because:
	//
	// 1. The static directory server has no pod watcher mechanism
	// 2. The proxy's directory cache (pkg/ccl/sqlproxyccl/tenant/directory_cache.go) only
	//    refreshes when ReportFailure() is called (on connection failures)
	// 3. There's no TTL or periodic refresh for the cache entries
	//
	// Evidence from logs (balancer.go:298):
	//   "REBALANCE TENANT: Found pod 10.142.0.198:29000 for tenant 3, state=RUNNING"
	//   (even after DrainPod was called and /info shows state=DRAINING)
	//
	// As a result, the balancer never collects draining pod assignments
	// (collectDrainingPodAssignments returns 0), and session migration never happens.
	//
	// Possible fixes:
	// 1. Add InvalidateCache() method that calls ReportFailure() to force cache refresh
	// 2. Add a notification/callback mechanism to the static directory server
	// 3. Implement periodic cache refresh with TTL
	// 4. Add pod watcher support to the static directory server

	tenantID, err := TenantID(ctx, p.l, p.c, virtualClusterName)
	if err != nil {
		return err
	}
	addrs, err := p.PodAddr(ctx, nodes, virtualClusterName, sqlInstance)
	if err != nil {
		return err
	}

	for _, addr := range addrs {
		reqBody := map[string]interface{}{
			"tenant_id": tenantID,
			"addr":      addr,
		}

		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}

		url := fmt.Sprintf("%s/api/drain-pod", p.httpURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.httpClient.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("drain pod failed: status=%d, body=%s", resp.StatusCode, string(body))
		}
		resp.Body.Close()

		p.l.Printf("Drained pod %s from tenant %s", addr, virtualClusterName)
	}
	return nil
}

func (p *SQLProxy) TenantID(ctx context.Context, virtualClusterName string) (int, error) {
	if id, found := p.tenantIDCache[virtualClusterName]; found {
		return id, nil
	}
	id, err := TenantID(ctx, p.l, p.c, virtualClusterName)
	if err != nil {
		return 0, err
	}
	p.tenantIDCache[virtualClusterName] = id
	return id, nil
}

func (p *SQLProxy) PodAddr(ctx context.Context, nodes option.NodeListOption, virtualClusterName string, sqlInstance int) ([]string, error) {
	internalIPs, err := p.c.InternalIP(ctx, p.l, nodes)
	if err != nil {
		return nil, err
	}

	sqlPorts, err := p.c.SQLPorts(ctx, p.l, nodes, virtualClusterName, sqlInstance)
	if err != nil {
		return nil, err
	}

	if len(sqlPorts) != len(internalIPs) {
		return nil, fmt.Errorf("SQL ports and IPs count mismatch: %d ports, %d IPs",
			len(sqlPorts), len(internalIPs))
	}

	var addrs []string
	for i := range internalIPs {
		addr := fmt.Sprintf("%s:%d", internalIPs[i], sqlPorts[i])
		addrs = append(addrs, addr)
	}

	return addrs, nil
}

func (p *SQLProxy) InternalURL(ctx context.Context, virtualClusterName string) (string, error) {
	tenantID, err := p.TenantID(ctx, virtualClusterName)
	if err != nil {
		return "", err
	}
	return p.c.ProxyURL(p.l, p.proxyNode, virtualClusterName, tenantID, p.proxyOpts, false /* external */)
}

func (p *SQLProxy) ExternalURL(ctx context.Context, virtualClusterName string) (string, error) {
	tenantID, err := p.TenantID(ctx, virtualClusterName)
	if err != nil {
		return "", err
	}
	return p.c.ProxyURL(p.l, p.proxyNode, virtualClusterName, tenantID, p.proxyOpts, true /* external */)
}

func (p *SQLProxy) Conn(ctx context.Context, virtualClusterName string) (*gosql.DB, error) {
	tenantID, err := p.TenantID(ctx, virtualClusterName)
	if err != nil {
		return nil, err
	}
	return p.c.ProxyConn(p.l, p.proxyNode, virtualClusterName, tenantID, p.proxyOpts)
}
