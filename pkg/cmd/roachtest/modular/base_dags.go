// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package modular

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/errors"
)

// BaseDAG defines a template test structure with stages and core operations.
// Base DAGs provide the skeletal structure for test plans, which can then be
// augmented with additional random operations by the scheduler.
type BaseDAG interface {
	// Name returns the unique identifier for this base DAG.
	Name() string

	// Description returns a human-readable description of what this base DAG tests.
	Description() string

	// Build constructs the test structure by adding stages and core operations
	// to the provided Test. This method should create stages and add essential
	// operations that define the test's basic flow.
	Build(t *Test) error
}

// BaseDAGRegistry manages the global registry of base DAGs.
type BaseDAGRegistry struct {
	mu   sync.RWMutex
	dags map[string]BaseDAG
}

var (
	globalBaseDAGRegistry = &BaseDAGRegistry{
		dags: make(map[string]BaseDAG),
	}
)

// RegisterBaseDAG registers a base DAG in the global registry.
// Panics if a base DAG with the same name is already registered.
func RegisterBaseDAG(dag BaseDAG) {
	globalBaseDAGRegistry.Register(dag)
}

// GetBaseDAG retrieves a base DAG by name from the global registry.
func GetBaseDAG(name string) (BaseDAG, error) {
	return globalBaseDAGRegistry.Get(name)
}

// ListBaseDAGs returns the names of all registered base DAGs.
func ListBaseDAGs() []string {
	return globalBaseDAGRegistry.List()
}

// GetAllBaseDAGs returns all registered base DAGs.
func GetAllBaseDAGs() []BaseDAG {
	return globalBaseDAGRegistry.GetAll()
}

// Register registers a base DAG in this registry.
func (r *BaseDAGRegistry) Register(dag BaseDAG) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.dags[dag.Name()]; exists {
		panic(fmt.Sprintf("base DAG already registered: %s", dag.Name()))
	}

	r.dags[dag.Name()] = dag
}

// Get retrieves a base DAG by name.
func (r *BaseDAGRegistry) Get(name string) (BaseDAG, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	dag, ok := r.dags[name]
	if !ok {
		return nil, errors.Newf("base DAG not found: %s", name)
	}

	return dag, nil
}

// List returns the names of all registered base DAGs, sorted alphabetically.
func (r *BaseDAGRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.dags))
	for name := range r.dags {
		names = append(names, name)
	}

	sort.Strings(names)
	return names
}

// GetAll returns all registered base DAGs, sorted by name.
func (r *BaseDAGRegistry) GetAll() []BaseDAG {
	r.mu.RLock()
	defer r.mu.RUnlock()

	dags := make([]BaseDAG, 0, len(r.dags))
	for _, dag := range r.dags {
		dags = append(dags, dag)
	}

	// Sort by name for deterministic ordering
	sort.Slice(dags, func(i, j int) bool {
		return dags[i].Name() < dags[j].Name()
	})

	return dags
}

// Count returns the number of registered base DAGs.
func (r *BaseDAGRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.dags)
}

// simpleBaseDAG is a helper for creating base DAGs with a build function.
type simpleBaseDAG struct {
	name        string
	description string
	buildFunc   func(*Test) error
}

func (d *simpleBaseDAG) Name() string {
	return d.name
}

func (d *simpleBaseDAG) Description() string {
	return d.description
}

func (d *simpleBaseDAG) Build(t *Test) error {
	return d.buildFunc(t)
}

// NewSimpleBaseDAG creates a base DAG from a build function.
// This is a convenience helper for simple base DAGs that don't need custom types.
func NewSimpleBaseDAG(name, description string, buildFunc func(*Test) error) BaseDAG {
	return &simpleBaseDAG{
		name:        name,
		description: description,
		buildFunc:   buildFunc,
	}
}

func init() {
	// Register workload base DAG with TPCC and Bank initialization
	RegisterBaseDAG(NewSimpleBaseDAG(
		"workload",
		"Workload base DAG: setup (init bank/tpcc), stage1 (1 worker run+validate), stage2 (10 workers run+validate)",
		func(t *Test) error {
			// Store database names so we can reuse them in later stages
			var bankDB, tpccDB string

			// Setup stage - initialize workloads (cluster is assumed to be running)
			// Use the same approach as regular roachtests
			t.Setup("health check", func(ctx context.Context, l *logger.Logger, h *Helper) error {
				c := h.Cluster()
				// Check all CRDB nodes (automatically skips workload node)
				crdbNodes := c.CRDBNodes()
				l.Printf("Running health check on CRDB nodes...")

				for _, node := range crdbNodes {
					l.Printf("Pinging database on node %d...", node)
					db, err := c.ConnE(ctx, l, node)
					if err != nil {
						return fmt.Errorf("failed to connect to node %d: %w", node, err)
					}
					err = db.Ping()
					db.Close()
					if err != nil {
						return fmt.Errorf("database ping failed for node %d: %w", node, err)
					}
					l.Printf("Node %d is healthy", node)
				}

				l.Printf("All CRDB nodes are healthy")
				return nil
			})

			t.Setup("init bank workload", func(ctx context.Context, l *logger.Logger, h *Helper) error {
				c := h.Cluster()
				var err error
				bankDB, err = h.CreateRandomDatabase("bank")
				if err != nil {
					return fmt.Errorf("failed to create bank database: %w", err)
				}

				// Use workload init like regular roachtests do
				cmd := fmt.Sprintf("./cockroach workload init bank --rows=10 --db=%s {pgurl:%d}",
					bankDB, h.RandomAvailableNode())

				if err := c.RunE(ctx, option.WithNodes(c.WorkloadNode()), cmd); err != nil {
					return fmt.Errorf("failed to init bank workload: %w", err)
				}
				l.Printf("Bank workload initialized")
				return nil
			})

			t.Setup("init tpcc workload", func(ctx context.Context, l *logger.Logger, h *Helper) error {
				c := h.Cluster()
				var err error
				tpccDB, err = h.CreateRandomDatabase("tpcc")
				if err != nil {
					return fmt.Errorf("failed to create tpcc database: %w", err)
				}

				// Use workload init like regular roachtests do
				cmd := fmt.Sprintf("./cockroach workload init tpcc --warehouses=10 --db=%s {pgurl:%d}",
					tpccDB, h.RandomAvailableNode())

				if err := c.RunE(ctx, option.WithNodes(c.WorkloadNode()), cmd); err != nil {
					return fmt.Errorf("failed to init tpcc workload: %w", err)
				}
				l.Printf("TPCC workload initialized")
				return nil
			})

			// Stage 1: Simulate low throughput operations and validate
			stage1 := t.NewStage("low-throughput")
			t.InStage(stage1, "run low throughput tpcc", func(ctx context.Context, l *logger.Logger, h *Helper) error {
				c := h.Cluster()
				// Run TPCC workload with low concurrency (1 worker) for 10 seconds
				cmd := fmt.Sprintf("./cockroach workload run tpcc --duration=10s --db=%s {pgurl:%d}",
					tpccDB, h.RandomAvailableNode())

				if err := c.RunE(ctx, option.WithNodes(c.WorkloadNode()), cmd); err != nil {
					return fmt.Errorf("failed to run low throughput tpcc workload: %w", err)
				}
				l.Printf("Low throughput TPCC workload completed (1 worker, 10s)")
				return nil
			}).Then("validate after low throughput", func(ctx context.Context, l *logger.Logger, h *Helper) error {
				// Validate data integrity by checking row counts
				query := fmt.Sprintf("SELECT COUNT(*) FROM %s.warehouse", tpccDB)
				rows, err := h.Query(query)
				if err != nil {
					return fmt.Errorf("validation query failed: %w", err)
				}
				defer rows.Close()

				var count int
				if rows.Next() {
					if err := rows.Scan(&count); err != nil {
						return fmt.Errorf("failed to scan count: %w", err)
					}
				}

				if count != 10 {
					return fmt.Errorf("expected 10 warehouses, got %d", count)
				}

				l.Printf("Validation after low throughput completed successfully (%d warehouses)", count)
				return nil
			})

			// Stage 2: Simulate high throughput operations and validate
			stage2 := t.NewStage("high-throughput")
			t.InStage(stage2, "run high throughput tpcc", func(ctx context.Context, l *logger.Logger, h *Helper) error {
				c := h.Cluster()
				// Run TPCC workload with high concurrency (10 workers) for 10 seconds
				cmd := fmt.Sprintf("./cockroach workload run tpcc --duration=10s --db=%s {pgurl:%d}",
					tpccDB, h.RandomAvailableNode())

				if err := c.RunE(ctx, option.WithNodes(c.WorkloadNode()), cmd); err != nil {
					return fmt.Errorf("failed to run high throughput tpcc workload: %w", err)
				}
				l.Printf("High throughput TPCC workload completed (10 workers, 10s)")
				return nil
			}).Then("validate after high throughput", func(ctx context.Context, l *logger.Logger, h *Helper) error {
				// Validate data integrity by checking row counts
				query := fmt.Sprintf("SELECT COUNT(*) FROM %s.district", tpccDB)
				rows, err := h.Query(query)
				if err != nil {
					return fmt.Errorf("validation query failed: %w", err)
				}
				defer rows.Close()

				var count int
				if rows.Next() {
					if err := rows.Scan(&count); err != nil {
						return fmt.Errorf("failed to scan count: %w", err)
					}
				}

				if count != 100 {
					return fmt.Errorf("expected 100 districts, got %d", count)
				}

				l.Printf("Validation after high throughput completed successfully (%d districts)", count)
				return nil
			})

			return nil
		},
	))
}
