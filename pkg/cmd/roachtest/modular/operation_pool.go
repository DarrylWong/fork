// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package modular

import (
	"fmt"
	"math/rand"
	"regexp"
	"sort"

	"github.com/cockroachdb/errors"
)

// OperationPool manages a pool of operations with filtering and random selection capabilities.
type OperationPool struct {
	excludePatterns []*regexp.Regexp
	rng             *rand.Rand
}

// NewOperationPool creates a new operation pool with include/exclude filters.
// Include and exclude patterns are regular expressions matched against operation names.
// If includePatterns is empty, all operations are included by default.
// Operations matching any exclude pattern are removed from the pool.
func NewOperationPool(excludePatterns []string, seed int64) (*OperationPool, error) {
	pool := &OperationPool{
		rng: rand.New(rand.NewSource(seed)),
	}

	// Compile exclude patterns
	for _, pattern := range excludePatterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid exclude pattern: %s", pattern)
		}
		pool.excludePatterns = append(pool.excludePatterns, re)
	}

	return pool, nil
}

// GetAvailableOperations returns all operations from the global registry that match the filter criteria.
func (p *OperationPool) GetAvailableOperations() ([]Operation, error) {
	// Get all registered operations
	registeredOperations.mu.RLock()
	defer registeredOperations.mu.RUnlock()

	var available []Operation
	for name, op := range registeredOperations.operations {
		if p.matchesFilters(name) {
			available = append(available, op)
		}
	}

	// Sort for deterministic ordering
	sort.Slice(available, func(i, j int) bool {
		return available[i].Name() < available[j].Name()
	})

	return available, nil
}

// GetAvailableOperationNames returns names of all operations that match the filter criteria.
func (p *OperationPool) GetAvailableOperationNames() ([]string, error) {
	ops, err := p.GetAvailableOperations()
	if err != nil {
		return nil, err
	}

	names := make([]string, len(ops))
	for i, op := range ops {
		names[i] = op.Name()
	}
	return names, nil
}

// RandomSelect randomly selects n operations from the available pool.
// If n is greater than the number of available operations, all operations are returned.
// The selection is done with replacement, so the same operation can appear multiple times.
func (p *OperationPool) RandomSelect(n int) ([]Operation, error) {
	available, err := p.GetAvailableOperations()
	if err != nil {
		return nil, err
	}

	if len(available) == 0 {
		return nil, errors.New("no operations available after applying filters")
	}

	selected := make([]Operation, n)
	for i := 0; i < n; i++ {
		idx := p.rng.Intn(len(available))
		selected[i] = available[idx]
	}

	return selected, nil
}

// matchesFilters checks if an operation name matches the include/exclude filters.
func (p *OperationPool) matchesFilters(name string) bool {
	// Name must not match any exclude pattern
	for _, re := range p.excludePatterns {
		if re.MatchString(name) {
			return false
		}
	}

	return true
}

// Count returns the number of operations available after filtering.
func (p *OperationPool) Count() (int, error) {
	ops, err := p.GetAvailableOperations()
	if err != nil {
		return 0, err
	}
	return len(ops), nil
}

// ListOperations returns a formatted string listing all available operations.
func (p *OperationPool) ListOperations() (string, error) {
	names, err := p.GetAvailableOperationNames()
	if err != nil {
		return "", err
	}

	result := fmt.Sprintf("Available operations (%d):\n", len(names))
	for i, name := range names {
		result += fmt.Sprintf("  %d. %s\n", i+1, name)
	}

	return result, nil
}
