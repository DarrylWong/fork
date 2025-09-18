package modular

import (
	"sync"
	"time"
)

// Operation is an interface for operations that can be reused across tests.
type Operation interface {
	Chain() Chain
	Name() string
	Precondition() bool
	Timeout() time.Duration
}

// OperationRegistry manages registered operations, i.e. ones that can
// be used in long running tests.
type OperationRegistry struct {
	mu         sync.RWMutex
	operations map[string]Operation
}

// Global registry instance
var registeredOperations = NewOperationRegistry()

// NewOperationRegistry creates a new operation registry.
func NewOperationRegistry() *OperationRegistry {
	return &OperationRegistry{
		operations: make(map[string]Operation),
	}
}

// Register adds an operation to the registry.
func (r *OperationRegistry) Register(op Operation) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if op.Name() == "" {
		panic("operation name cannot be empty")
	}

	if _, exists := r.operations[op.Name()]; exists {
		panic("operation already registered")
	}

	r.operations[op.Name()] = op
}

// Get retrieves an operation by name.
func (r *OperationRegistry) Get(name string) (Operation, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	op, exists := r.operations[name]
	return op, exists
}

// GetOperation retrieves an operation from the global registry.
func GetOperation(name string) (Operation, bool) {
	return registeredOperations.Get(name)
}
