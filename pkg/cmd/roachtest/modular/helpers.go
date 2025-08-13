package modular

import (
	"fmt"
	"math/rand"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
)

// GetCluster returns a cluster by name from the helper.
func (h *Helper) GetCluster(name string) (Cluster, error) {
	if cluster, exists := h.clusters[name]; exists {
		return cluster, nil
	}
	return nil, fmt.Errorf("cluster %s not found", name)
}

// GetWorkloadCluster returns a workload cluster by name from the helper.
func (h *Helper) GetWorkloadCluster(name string) (*WorkloadCluster, error) {
	if cluster, exists := h.workloadClusters[name]; exists {
		return cluster, nil
	}
	return nil, fmt.Errorf("workload cluster %s not found", name)
}

// AddCluster adds a cluster to the helper.
func (h *Helper) AddCluster(name string, c cluster.Cluster) {
	cockroachCluster := &CockroachCluster{
		name:     name,
		Services: make([]ServiceDescriptor, 0),
		cluster:  c,
	}
	h.clusters[name] = cockroachCluster
}

// AddWorkloadCluster adds a workload cluster to the helper.
func (h *Helper) AddWorkloadCluster(name string, c cluster.Cluster) {
	h.workloadClusters[name] = &WorkloadCluster{
		Name:    name,
		Cluster: c,
	}
}

// Random returns the helper's random number generator.
func (h *Helper) Random() *rand.Rand {
	return h.rng
}

// RandomChoice returns a random element from the given slice.
func (h *Helper) RandomChoice(choices []string) string {
	if len(choices) == 0 {
		return ""
	}
	return choices[h.rng.Intn(len(choices))]
}

// RandomInt returns a random integer between min and max (inclusive).
func (h *Helper) RandomInt(min, max int) int {
	if min >= max {
		return min
	}
	return min + h.rng.Intn(max-min+1)
}

// RandomDuration returns a random duration between min and max.
func (h *Helper) RandomDuration(min, max int) int {
	return h.RandomInt(min, max)
}
