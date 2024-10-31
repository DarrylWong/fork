package ficontroller

import (
	"context"
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"
)

type ControllerConfig struct {
	port int
}

func (c ControllerConfig) Start(ctx context.Context, standalone bool) error {
	return c.ListenAndServe(ctx, standalone)
}

func (c ControllerConfig) ListenAndServe(ctx context.Context, standalone bool) error {
	lis, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", c.port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	server := grpc.NewServer()
	req := &UpdateClusterStateRequest{
		PlanID:       "12345",
		ClusterState: make(map[string]*ClusterInfo),
	}
	req.ClusterState["cluster1"] = &ClusterInfo{
		ClusterSize:      4,
		ConnectionString: "localhost:8080",
	}
	return server.Serve(lis)
}
