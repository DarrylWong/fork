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
