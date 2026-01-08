#!/usr/bin/env bash
set -euo pipefail

# Script to start the directory server (used by roachtests)
# This server provides dynamic routing information to SQL proxies
# The script builds the Go binary on the node and runs it

COCKROACH_BINARY="${1}"
shift
GRPC_PORT="${1}"
shift
HTTP_PORT="${1}"
shift
LOG_DIR="${1}"
shift
LOCAL="${1:-}"

mkdir -p "${LOG_DIR}"

# Get the cockroach binary directory to find the Go workspace
COCKROACH_DIR=$(dirname "${COCKROACH_BINARY}")

# Create a temporary directory for building
BUILD_DIR=$(mktemp -d)
trap "rm -rf ${BUILD_DIR}" EXIT

# Write the directory server Go code
cat > "${BUILD_DIR}/main.go" <<'GOCODE'
// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

// directory-server is a simple wrapper around TestStaticDirectoryServer
// that exposes both GRPC (for proxy) and HTTP (for control API).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/cockroachdb/cockroach/pkg/ccl/sqlproxyccl/tenant"
	"github.com/cockroachdb/cockroach/pkg/ccl/sqlproxyccl/tenantdirsvr"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/util/stop"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
	"google.golang.org/grpc"
)

var (
	grpcPort = flag.Int("grpc-port", 26258, "GRPC port for proxy connections")
	httpPort = flag.Int("http-port", 26259, "HTTP port for control API")
)

type server struct {
	dir *tenantdirsvr.TestStaticDirectoryServer
}

func main() {
	flag.Parse()

	ctx := context.Background()
	stopper := stop.NewStopper()
	defer stopper.Stop(ctx)

	// Create directory server
	dir := tenantdirsvr.NewTestStaticDirectoryServer(stopper, nil)
	if err := dir.Start(ctx); err != nil {
		log.Fatalf("Failed to start directory: %v", err)
	}

	srv := &server{dir: dir}

	// Start GRPC server
	grpcServer := grpc.NewServer()
	tenant.RegisterDirectoryServer(grpcServer, dir)

	grpcListener, err := net.Listen("tcp", fmt.Sprintf(":%d", *grpcPort))
	if err != nil {
		log.Fatalf("Failed to listen on GRPC port %d: %v", *grpcPort, err)
	}

	go func() {
		log.Printf("GRPC server listening on port %d", *grpcPort)
		if err := grpcServer.Serve(grpcListener); err != nil {
			log.Printf("GRPC server error: %v", err)
		}
	}()

	// Start HTTP control API
	http.HandleFunc("/api/create-tenant", srv.handleCreateTenant)
	http.HandleFunc("/api/add-pod", srv.handleAddPod)
	http.HandleFunc("/api/remove-pod", srv.handleRemovePod)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})

	httpListener, err := net.Listen("tcp", fmt.Sprintf(":%d", *httpPort))
	if err != nil {
		log.Fatalf("Failed to listen on HTTP port %d: %v", *httpPort, err)
	}

	go func() {
		log.Printf("HTTP API listening on port %d", *httpPort)
		if err := http.Serve(httpListener, nil); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	log.Printf("Directory server ready (GRPC:%d, HTTP:%d)", *grpcPort, *httpPort)

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Printf("Shutting down...")
}

func (s *server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TenantID uint64 `json:"tenant_id"`
		Name     string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tenantID := roachpb.MustMakeTenantID(req.TenantID)
	s.dir.CreateTenant(tenantID, &tenant.Tenant{
		TenantID:    req.TenantID,
		ClusterName: req.Name,
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *server) handleAddPod(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TenantID uint64 `json:"tenant_id"`
		Addr     string `json:"addr"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tenantID := roachpb.MustMakeTenantID(req.TenantID)
	success := s.dir.AddPod(tenantID, &tenant.Pod{
		TenantID:       req.TenantID,
		Addr:           req.Addr,
		State:          tenant.RUNNING,
		StateTimestamp: timeutil.Now(),
	})

	if !success {
		http.Error(w, "Failed to add pod", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *server) handleRemovePod(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TenantID uint64 `json:"tenant_id"`
		Addr     string `json:"addr"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tenantID := roachpb.MustMakeTenantID(req.TenantID)
	success := s.dir.RemovePod(tenantID, req.Addr)

	if !success {
		http.Error(w, "Failed to remove pod", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
GOCODE

# Initialize go.mod if needed
cd "${BUILD_DIR}"
go mod init directory-server 2>/dev/null || true

# Add cockroach dependency - find the cockroach module
COCKROACH_MODULE_DIR=$(cd "${COCKROACH_DIR}/.." && pwd)
echo "replace github.com/cockroachdb/cockroach => ${COCKROACH_MODULE_DIR}" >> go.mod

# Build the binary
echo "Building directory server..."
go build -o directory-server main.go >> "${LOG_DIR}/directory-server-build.log" 2>&1

if [ $? -ne 0 ]; then
	echo "Failed to build directory server. See ${LOG_DIR}/directory-server-build.log for details"
	cat "${LOG_DIR}/directory-server-build.log"
	exit 1
fi

# Run the binary
BINARY="${BUILD_DIR}/directory-server"
ARGS=(
  "--grpc-port=${GRPC_PORT}"
  "--http-port=${HTTP_PORT}"
)

if [[ -n "${LOCAL}" ]]; then
  # For local clusters, run in background with a PID file
  PID_FILE="${LOG_DIR}/directory-server.pid"
  rm -f "${PID_FILE}"

  "${BINARY}" "${ARGS[@]}" >> "${LOG_DIR}/directory-server.stdout.log" 2>> "${LOG_DIR}/directory-server.stderr.log" &
  PID=$!
  echo ${PID} > "${PID_FILE}"
  echo "directory-server started with PID ${PID}: $(date)" | tee -a "${LOG_DIR}/roachprod.log"
  exit 0
else
  # For remote clusters, use systemd
  SERVICE_NAME="directory-server"
  SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

  sudo systemctl stop ${SERVICE_NAME}.service || true
  sudo systemctl daemon-reload

  # Create systemd service file
  sudo tee ${SERVICE_FILE} > /dev/null <<EOF
[Unit]
Description=CockroachDB Directory Server (Testing)
After=network.target

[Service]
Type=simple
ExecStart=${BINARY} ${ARGS[@]}
StandardOutput=append:${LOG_DIR}/directory-server.stdout.log
StandardError=append:${LOG_DIR}/directory-server.stderr.log
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF

  sudo systemctl daemon-reload
  sudo systemctl start ${SERVICE_NAME}.service
  sudo systemctl enable ${SERVICE_NAME}.service

  echo "directory-server started via systemd: $(date)" | tee -a "${LOG_DIR}/roachprod.log"
  exit 0
fi
