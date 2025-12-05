// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package modular

import (
	"fmt"
	"sync"
)

type ResourceAction string

// Action type constants - these represent operation types
const (
	// ActionAny indicates that all types of access to a resource should be locked.
	// e.g. To DROP a table, we want to lock all actions on that table, but we are okay
	// with running a schema change to the same table we are restoring to.
	ActionAny              ResourceAction = "*"
	ActionSchemaChange     ResourceAction = "schema_change"
	ActionRestore          ResourceAction = "restore"
	ActionClusterSetting   ResourceAction = "cluster_setting"
	ActionNodeAvailability ResourceAction = "node_availability"
)

// ResourceAccess represents a resource that a step accesses.
// It consists of an action type (e.g., "restore", "schema_change") and a path to the resource.
type ResourceAccess struct {
	Action ResourceAction
	Path   ResourcePath
	Lock   bool // true = exclusive lock
}

// ResourcePath represents a hierarchical resource path (database/table, cluster setting, etc.)
type ResourcePath interface {
	ConflictsWith(other ResourcePath) bool
	String() string
}

// DatabaseResource represents a database or table resource.
// If Table is empty, it represents the entire database.
type DatabaseResource struct {
	Database string
	Table    string
}

func (r DatabaseResource) ConflictsWith(other ResourcePath) bool {
	o, ok := other.(DatabaseResource)
	if !ok {
		return false
	}

	if r.Database != o.Database {
		return false
	}

	if r.Table == "" || o.Table == "" {
		return true
	}

	return r.Table == o.Table
}

func (r DatabaseResource) String() string {
	res := fmt.Sprintf("db:%s", r.Database)
	if r.Table != "" {
		res += fmt.Sprintf("->table:%s", r.Table)
	}
	return res
}

type ClusterSettingResource struct {
	Name string
}

func (r ClusterSettingResource) ConflictsWith(other ResourcePath) bool {
	_, ok := other.(ClusterSettingResource)
	if !ok {
		return false
	}

	return r.Name == other.(ClusterSettingResource).Name
}

func (r ClusterSettingResource) String() string {
	return r.Name
}

type NodeAvailabilityResource struct {
	NodeID int
}

func (r NodeAvailabilityResource) ConflictsWith(other ResourcePath) bool {
	// For now, we treat all node unavailability as incompatible. In the future
	// we can make this more granular by checking which nodes are down and comparing
	// it to RF.
	_, ok := other.(NodeAvailabilityResource)
	return ok
}

func (r NodeAvailabilityResource) String() string {
	return fmt.Sprintf("n%d", r.NodeID)
}

// String returns a human-readable representation of the resource access.
func (r *ResourceAccess) String() string {
	return fmt.Sprintf("%s->%s", r.Action, r.Path)
}

// ConflictsWith checks if two resource accesses conflict with each other.
// Conflict rules:
//   - If neither has Lock=true, they don't conflict (non-exclusive access)
//   - If at least one has Lock=true, they conflict if:
//   - Actions match (or one is ActionAny wildcard)
//   - Resource paths conflict
func (r *ResourceAccess) ConflictsWith(other *ResourceAccess) bool {
	if r == nil || other == nil {
		return false
	}

	// If neither has locks, no conflict (both are non-exclusive accesses)
	if !r.Lock && !other.Lock {
		return false
	}

	// Check if actions match.
	actionsMatch := r.Action == other.Action ||
		r.Action == ActionAny || other.Action == ActionAny

	if !actionsMatch {
		return false
	}
	return r.Path.ConflictsWith(other.Path)
}

// Resource represents a resource that can be accessed or locked.
type Resource interface {
	Resource(lock bool) ResourceAccess
}

// AcquireAccess creates a StepOption for non-exclusive access to a resource.
// Multiple operations can access the same resource concurrently.
func AcquireAccess(r Resource) StepOption {
	return func(s *SingleStep) {
		res := r.Resource(false)
		s.resources.accesses = append(s.resources.accesses, res)
	}
}

// ReleaseAccess stops non-exclusive access to a resource.
func ReleaseAccess(r Resource) StepOption {
	return func(s *SingleStep) {
		res := r.Resource(false)
		s.resources.releases = append(s.resources.releases, res)
	}
}

func AcquireAndReleaseAccess(r Resource) StepOption {
	return func(s *SingleStep) {
		res := r.Resource(false)
		s.resources.accesses = append(s.resources.accesses, res)
		s.resources.releases = append(s.resources.releases, res)
	}
}

// AcquireLock creates a StepOption that acquires an exclusive lock on a resource.
// Must be paired with ReleaseLock to release the lock.
func AcquireLock(r Resource) StepOption {
	return func(s *SingleStep) {
		res := r.Resource(true)
		s.resources.accesses = append(s.resources.accesses, res)
	}
}

// ReleaseLock releases an exclusive lock on a resource acquired earlier.
func ReleaseLock(r Resource) StepOption {
	return func(s *SingleStep) {
		res := r.Resource(true)
		s.resources.releases = append(s.resources.releases, res)
	}
}

func AcquireAndReleaseLock(r Resource) StepOption {
	return func(s *SingleStep) {
		res := r.Resource(true)
		s.resources.accesses = append(s.resources.accesses, res)
		s.resources.releases = append(s.resources.releases, res)
	}
}

// DatabaseAccess locks all operation types on a database.
type DatabaseAccess struct {
	Database string
}

func (d DatabaseAccess) Resource(lock bool) ResourceAccess {
	return ResourceAccess{
		Action: ActionAny,
		Path: DatabaseResource{
			Database: d.Database,
		},
		Lock: lock,
	}
}

// TableAccess locks all operation types on a table.
type TableAccess struct {
	Database string
	Table    string
}

func (t TableAccess) Resource(lock bool) ResourceAccess {
	return ResourceAccess{
		Action: ActionAny,
		Path: DatabaseResource{
			Database: t.Database,
			Table:    t.Table,
		},
		Lock: lock,
	}
}

// ClusterSettingAccess represents a cluster setting resource.
type ClusterSettingAccess struct {
	Name string
}

func (c ClusterSettingAccess) Resource(lock bool) ResourceAccess {
	return ResourceAccess{
		Action: ActionClusterSetting,
		Path: ClusterSettingResource{
			Name: c.Name,
		},
		Lock: lock,
	}
}

// SchemaChangeAccess represents a schema change resource.
type SchemaChangeAccess struct {
	Database string
	Table    string
}

func (s SchemaChangeAccess) Resource(lock bool) ResourceAccess {
	return ResourceAccess{
		Action: ActionSchemaChange,
		Path: DatabaseResource{
			Database: s.Database,
			Table:    s.Table,
		},
		Lock: lock,
	}
}

// RestoreAccess represents restore resource.
type RestoreAccess struct {
	Database string
	Table    string
}

func (s RestoreAccess) Resource(lock bool) ResourceAccess {
	return ResourceAccess{
		Action: ActionRestore,
		Path: DatabaseResource{
			Database: s.Database,
			Table:    s.Table,
		},
		Lock: lock,
	}
}

// NodeAvailability represents node availability resource.
type NodeAvailability struct{}

func (n NodeAvailability) Resource(lock bool) ResourceAccess {
	return ResourceAccess{
		Action: ActionNodeAvailability,
		Path:   NodeAvailabilityResource{},
		Lock:   lock,
	}
}

// RuntimeLockManager tracks currently held locks during test execution.
// It provides thread-safe lock acquisition and release at runtime.
type RuntimeLockManager struct {
	mu        sync.Mutex
	heldLocks map[string]ResourceAccess
}

// NewRuntimeLockManager creates a new RuntimeLockManager.
func NewRuntimeLockManager() *RuntimeLockManager {
	return &RuntimeLockManager{
		heldLocks: make(map[string]ResourceAccess),
	}
}

func (r *RuntimeLockManager) Acquire(access ResourceAccess) (func(), bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for conflicts with currently held locks
	if r.hasConflict(access) {
		return nil, false
	}

	// No conflicts, acquire the lock
	r.heldLocks[access.String()] = access

	// Return unlock function
	return func() {
		r.release(access)
	}, true
}

// AcquireLock attempts to acquire an exclusive lock on a resource without blocking.
// Returns an unlock function and true if successful.
func (r *RuntimeLockManager) AcquireLock(resource Resource) (func(), bool) {
	access := resource.Resource(true)
	return r.Acquire(access)
}

// AcquireAccess attempts to acquire non-exclusive access to a resource without blocking.
// Returns an unlock function and true if successful.
func (r *RuntimeLockManager) AcquireAccess(resource Resource) (func(), bool) {
	access := resource.Resource(false)
	return r.Acquire(access)
}

// hasConflict checks if the given access conflicts with any currently held locks.
// Must be called with mu held.
func (r *RuntimeLockManager) hasConflict(access ResourceAccess) bool {
	for _, heldLock := range r.heldLocks {
		if access.ConflictsWith(&heldLock) {
			return true
		}
	}
	return false
}

// release releases a previously acquired resource access/lock.
func (r *RuntimeLockManager) release(access ResourceAccess) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.heldLocks, access.String())
}

// IsLocked checks if a resource access would conflict with currently held locks.
func (r *RuntimeLockManager) IsLocked(access ResourceAccess) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.hasConflict(access)
}
