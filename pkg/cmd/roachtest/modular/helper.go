package modular

import (
	"context"
	gosql "database/sql"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	logger "log"
	"math/rand"
)

// Helper provides utilities for modular test steps.
type Helper struct {
	defaultService *Service
	rng            *rand.Rand
}

func (h *Helper) AvailableNodes() option.NodeListOption {
	return h.defaultService.AvailableNodes()
}

// Service implements helper functions on behalf of a specific
// service. Internal fields are provided by the testRunner struct,
// allowing us to connect to a specific node and check live the test
// runner's view of cluster versions, etc.
type Service struct {
	name       string
	ctx        context.Context
	connFunc   func(int) *gosql.DB
	stepLogger *logger.Logger
	monitor    test.Monitor
	cluster    cluster.Cluster
	nodes      option.NodeListOption
}

func (s *Service) AvailableNodes() option.NodeListOption {
	return s.monitor.AvailableNodes(s.name).Intersect(s.nodes)
}

func (s *Service) randomAvailableNode(rng *rand.Rand) int {
	nodes := s.AvailableNodes()
	return nodes.SeededRandNode(rng)[0]
}

// Connect returns a connection pool to the given node. Note that
// these connection pools are managed by the framework and therefore
// *must not* be closed. They are closed automatically when the test
// finishes.
func (s *Service) Connect(node int) *gosql.DB {
	return s.connFunc(node)
}

// RandomDB returns a connection pool to a random node in the
// cluster. Do *not* call `Close` on the pool returned (see comment on
// `Connect` function).
func (s *Service) RandomDB(rng *rand.Rand) (int, *gosql.DB) {
	node := s.randomAvailableNode(rng)
	return node, s.Connect(node)
}
