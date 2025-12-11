# Dynamic Operations in Modular Framework

This document describes the dynamic operations pattern in the modular testing framework and provides a roadmap for polishing it into a production-ready feature.

## Table of Contents
1. [Overview](#overview)
2. [Core Concepts](#core-concepts)
3. [Current State](#current-state)
4. [Issues & Gaps](#issues--gaps)
5. [Polishing Plan](#polishing-plan)
6. [Success Criteria](#success-criteria)

## Overview

Dynamic operations allow test operations to make runtime decisions (like selecting which table to modify) **before** chain merging occurs. This enables:

1. **Proper chain merging**: The planner knows which resources each operation will access
2. **Schema adaptation**: Operations can adapt to schema changes between test stages
3. **Better parallelism**: Non-conflicting operations run concurrently while conflicting ones are serialized

### The Problem We Solved

**Before (Old Pattern):**
```go
func AddRandomColumn() modular.Operation {
    return modular.NewOperation(
        modular.NewStep("add random column", func(ctx, l, h) error {
            // ❌ Table selection happens DURING execution
            dbName, tableName, err := h.SearchTable(...)
            // ❌ Planner can't see which table will be selected
            // ❌ Can't properly merge with other operations on same table
        })
    )
}
```

**After (Dynamic Pattern):**
```go
func AddRandomColumnDynamic() modular.Operation {
    return modular.NewDynamicOperation(
        "add random column (dynamic)",
        // ✅ PrePlan: Select table BEFORE chain merging
        func(ctx, l, h) (*Plan, error) {
            dbName, tableName, err := h.SearchTable(...)
            return &Plan{Database: dbName, Table: tableName}, nil
        },
        // ✅ Run: Use the pre-selected table
        func(ctx, l, h, plan) error {
            h.Exec(fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN...", plan.Database, plan.Table))
        },
        // ✅ Declare resources based on plan
        modular.WithDynamicResourceCallback(func(plan) (acquire, release) {
            return []ResourceAccess{SchemaChangeAccess{plan.Database, plan.Table}.Resource(true)}, ...
        }),
        // ✅ Update name to show selected table
        modular.WithDynamicName(func(plan) string {
            return fmt.Sprintf("add random column to %s.%s", plan.Database, plan.Table)
        }),
    )
}
```

## Core Concepts

### 1. PrePlan Phase

The PrePlan phase runs **before chain merging** and is responsible for:
- Making all random decisions
- Selecting resources (tables, nodes, databases)
- Returning a Plan object with all decisions captured

**Key Properties:**
- Read-only (queries database schema)
- Fast (< 1 second per operation)
- Deterministic given the same cluster state
- Can fail gracefully (skip operation if no suitable resource)

### 2. Resource Declaration

Operations declare what resources they'll access via `WithDynamicResourceCallback`:

```go
modular.WithDynamicResourceCallback(func(plan *MyPlan) (acquire, release []ResourceAccess) {
    access := modular.SchemaChangeAccess{
        Database: plan.Database,
        Table:    plan.Table,
    }.Resource(true) // true = write lock, false = read lock

    // Both acquire and release usually the same
    return []ResourceAccess{access}, []ResourceAccess{access}
})
```

**Resource Types:**
- `SchemaChangeAccess{Database, Table}` - DDL operations
- `RestoreAccess{Database, Table}` - Backup/restore operations
- `NodeAvailabilityResource{NodeID}` - Node operations
- `ReadAccess{Database, Table}` - Read-only operations (future)

### 3. Chain Merging

The planner automatically merges operations that conflict on the same resources:

```
Input (5 operations):
1. add_index to tpcc.customer
2. add_column to tpcc.customer
3. add_index to tpcc.order
4. node_restart node 2
5. add_column to tpcc.order

After chain merging:
Chain A: add_index(tpcc.customer) → add_column(tpcc.customer)
Chain B: add_index(tpcc.order) → add_column(tpcc.order)
Chain C: node_restart(2)

Chains A, B, C run in parallel!
```

### 4. Dynamic Name Updates

Operations update their display name based on the plan:

```go
modular.WithDynamicName(func(plan *MyPlan) string {
    return fmt.Sprintf("add index to %s.%s", plan.Database, plan.Table)
})
```

This makes DAG visualization and logs much more informative.

## Current State

### Implemented ✅

**Dynamic Operations:**
1. `AddRandomIndexDynamic()` - adds index to random table
2. `AddRandomColumnDynamic()` - adds column to random table
3. `InspectTableDynamic()` - validates random table
4. `BackupRestoreDatabaseDynamic()` - backs up/restores random database
5. `BackupRestoreDynamic()` - backs up/restores random table
6. `NodeRestart()` - restarts random node

**Core Infrastructure:**
- `NewDynamicOperation()` builder
- `WithDynamicResourceCallback()` option
- `WithDynamicName()` option
- Chain merging algorithm
- Dynamic test plan execution
- Resource locking system

**Example Test:**
- `modular/example/dynamic-resource` - Demonstrates all dynamic operations

### Not Yet Implemented ❌

See [Issues & Gaps](#issues--gaps) section below.

## Issues & Gaps

### 1. Inconsistency - Old Pattern Still Exists

**Problem:** Non-dynamic versions still exist:
- `AddRandomColumn()` - uses old pattern with runtime table selection
- `InspectTable()` - uses old pattern with runtime table selection

**Impact:** Users might use the wrong one, missing out on chain merging benefits.

**Solution:** Deprecate old versions, add migration guide.

### 2. Missing Documentation

**Problem:** No comprehensive guide exists.

**Gaps:**
- When to use dynamic vs static operations
- How to write a dynamic operation (step-by-step)
- Resource types and their semantics
- PrePlan best practices
- Chain merging explanation

**Solution:** Write README.md and developer guide (see [Phase 1](#phase-1-foundation--documentation-week-1)).

### 3. Resource Tracking Gap

**Problem:** Operations can't expose created resources to later stages.

**Current Workaround:**
```go
// Must use pattern matching to find restored databases
rows, _ := db.Query("SELECT database_name FROM [SHOW DATABASES] WHERE database_name LIKE '%_restored_%'")
```

**Desired:**
```go
// Operations register what they create
h.RegisterCreatedResource(CreatedResource{Type: "database", Name: restoredName, ...})

// Later stages query the registry
resources := h.GetCreatedResources("database")
```

**Solution:** Implement resource registry (see [Phase 3](#phase-3-resource-tracking-enhancement-week-2)).

### 4. Error Handling

**Problems:**
- What happens if PrePlan finds no suitable table?
- Should operation skip or fail the test?
- Error messages are too generic

**Solution:** Add graceful degradation and better error messages (see [Phase 5](#phase-5-error-handling--robustness-week-3)).

### 5. Testing Coverage

**Gaps:**
- No unit tests for dynamic operation pattern
- No tests for edge cases (empty databases, deleted resources)
- No tests for chain merging correctness
- Only one integration test

**Solution:** Add comprehensive test suite (see [Phase 4](#phase-4-testing-infrastructure-week-2-3)).

### 6. Performance

**Concerns:**
- PrePlan adds overhead (database queries)
- No caching of discovered resources
- Each operation queries independently

**Solution:** Add caching and parallel PrePlan (see [Phase 6](#phase-6-performance-optimization-week-3-4)).

### 7. API Clarity

**Problems:**
- Not obvious when to use `NewOperation` vs `NewDynamicOperation`
- Resource declaration is verbose
- No helpers for common patterns

**Solution:** Add API simplification helpers (see [Phase 2](#phase-2-api-improvements-week-1-2)).

### 8. Backup/Restore Local Testing

**Problem:** Requires GCS credentials, fails in local mode.

**Solution:** Add mock backend for local testing (see [Phase 4.3](#43-add-mock-backend-for-backuprestore)).

## Polishing Plan

### Phase 1: Foundation & Documentation (Week 1)

**Goal:** Make the feature understandable and approachable.

#### 1.1 Write README.md
**File:** `/pkg/cmd/roachtest/modular/README.md`

**Sections:**
1. Modular framework overview
2. Static vs Dynamic operations
3. Writing a dynamic operation (tutorial)
4. Resource types reference
5. Chain merging explanation
6. PrePlan best practices
7. Common patterns and examples

#### 1.2 Add GoDoc Comments
- Document `NewDynamicOperation()` parameters
- Document `WithDynamicResourceCallback()` contract
- Document `WithDynamicName()` usage
- Document all resource types

#### 1.3 Developer Guide
**File:** `/pkg/cmd/roachtest/modular/CONTRIBUTING.md`

**Contents:**
- How to add a new operation
- Testing guidelines
- When to make an operation dynamic
- Common pitfalls

**Deliverables:**
- [ ] README.md (comprehensive guide)
- [ ] CONTRIBUTING.md (developer guide)
- [ ] GoDoc comments on all public APIs
- [ ] Migration guide (old pattern → new pattern)

### Phase 2: API Improvements (Week 1-2)

**Goal:** Make the API more ergonomic and harder to misuse.

#### 2.1 Deprecate Old Pattern
```go
// Deprecated: Use AddRandomColumnDynamic for proper chain merging.
func AddRandomColumn() modular.Operation { ... }
```

#### 2.2 Helper Functions
```go
// Helper for common table operation pattern
func NewTableOperation(
    name string,
    selectTable func(*Helper) (db, table string, err error),
    execute func(*Helper, db, table string) error,
    writeLock bool,
) modular.Operation { ... }
```

#### 2.3 Simplified Resource Declaration
```go
// Instead of verbose callback:
modular.WithTableResource(func(plan) (db, table string, write bool) {
    return plan.DB, plan.Table, true
})
```

**Deliverables:**
- [ ] Deprecation markers on old operations
- [ ] Helper functions for common patterns
- [ ] Simplified resource declaration API
- [ ] Examples using new helpers

### Phase 3: Resource Tracking Enhancement (Week 2)

**Goal:** Solve the resource tracking gap.

#### 3.1 Resource Registry API
```go
type CreatedResource struct {
    Type     string // "database", "table", "index", "user"
    Name     string
    Parent   string // for tables: database name
    Metadata map[string]string
}

func (h *Helper) RegisterCreatedResource(r CreatedResource)
func (h *Helper) GetCreatedResources(resourceType string) []CreatedResource
func (h *Helper) FindResources(filter func(CreatedResource) bool) []CreatedResource
```

#### 3.2 Update Operations
```go
// In BackupRestoreDatabaseDynamic:
h.RegisterCreatedResource(CreatedResource{
    Type:   "database",
    Name:   plan.RestoreDBName,
    Metadata: map[string]string{"source": plan.DBName},
})
```

#### 3.3 Update Tests
```go
// Clean up using registry instead of pattern matching
resources := h.GetCreatedResources("database")
for _, r := range resources {
    h.Exec(fmt.Sprintf("DROP DATABASE %s CASCADE", r.Name))
}
```

**Deliverables:**
- [ ] CreatedResource type and registry
- [ ] Helper methods for registration/query
- [ ] Update all operations to register resources
- [ ] Update tests to use registry
- [ ] Remove TODO comment about this issue

### Phase 4: Testing Infrastructure (Week 2-3)

**Goal:** Comprehensive test coverage.

#### 4.1 Unit Tests
**File:** `/pkg/cmd/roachtest/modular/operations/dynamic_test.go`

**Tests:**
- PrePlan callback is called before Run
- Dynamic resource declaration works
- Dynamic name update works
- PrePlan failure handling
- Skip behavior for missing resources

#### 4.2 Integration Tests
**File:** `/pkg/cmd/roachtest/tests/modular_test.go`

**Tests:**
- Dynamic table selection across stages
- Schema adaptation (table deletion)
- Concurrent dynamic operations
- Chain merging correctness
- Resource locking

#### 4.3 Mock Backend for Backup/Restore
```go
type MockBackupStorage struct {
    backups map[string][]byte
}

func NewMockBackupStorage() *MockBackupStorage
func (m *MockBackupStorage) Store(path string, data []byte) error
func (m *MockBackupStorage) Load(path string) ([]byte, error)
```

Enable in tests:
```go
if c.IsLocal() {
    operations.SetBackupBackend(&MockBackupStorage{})
}
```

**Deliverables:**
- [ ] Unit test suite
- [ ] Integration test suite
- [ ] Mock backup backend
- [ ] 80%+ code coverage

### Phase 5: Error Handling & Robustness (Week 3)

**Goal:** Handle edge cases gracefully.

#### 5.1 Graceful Degradation
```go
// Return "skip" plan if no suitable resource
func AddRandomIndexDynamic() modular.Operation {
    prePlan := func(ctx, l, h) (*Plan, error) {
        db, table, err := h.SearchTable(...)
        if err != nil {
            return &Plan{Skip: true, Reason: "no suitable tables"}, nil
        }
        return &Plan{DB: db, Table: table}, nil
    }

    run := func(ctx, l, h, plan) error {
        if plan.Skip {
            l.Printf("Skipping: %s", plan.Reason)
            return nil // Not a failure
        }
        // Normal execution
    }
}
```

#### 5.2 Better Error Messages
```go
// Detailed errors with context
return fmt.Errorf("no suitable table found: searched %d databases, %d tables total, "+
    "none matched criteria (need: table with >=1 column, not system table)",
    dbCount, tableCount)
```

#### 5.3 PrePlan Timeouts
```go
func (h *Helper) SearchTable(filter func(string, string) bool) (string, string, error) {
    ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
    defer cancel()
    // Search with timeout protection
}
```

**Deliverables:**
- [ ] Skip mechanism for missing resources
- [ ] Detailed error messages with context
- [ ] Timeout protection for PrePlan
- [ ] Tests for all error paths

### Phase 6: Performance Optimization (Week 3-4)

**Goal:** Minimize PrePlan overhead.

#### 6.1 Resource Caching
```go
type ResourceCache struct {
    tables    []TableInfo
    databases []string
    nodes     []int
    cachedAt  time.Time
    ttl       time.Duration
}

func (h *Helper) RefreshCache() error { ... }
func (h *Helper) CachedSearchTable(...) (string, string, error) { ... }
```

#### 6.2 Parallel PrePlan
```go
func (p *Planner) parallelPrePlan(ops []Operation) error {
    // Run all PrePlans in parallel (they're read-only)
    var wg sync.WaitGroup
    for _, op := range ops {
        wg.Add(1)
        go func(op Operation) {
            defer wg.Done()
            op.PrePlan(...)
        }(op)
    }
    wg.Wait()
}
```

**Deliverables:**
- [ ] Resource cache implementation
- [ ] Parallel PrePlan execution
- [ ] Performance benchmarks
- [ ] Verify <10% overhead target

### Phase 7: Polish & Examples (Week 4)

**Goal:** Production-ready feature with great examples.

#### 7.1 Example Tests
1. `modular/example/schema-changes` - DDL with dynamic selection
2. `modular/example/node-chaos` - Node operations
3. `modular/example/backup-restore` - Backup/restore workflows
4. `modular/example/multi-stage` - Multi-stage schema evolution

#### 7.2 Metrics
```go
type OperationMetrics struct {
    PrePlanDuration  time.Duration
    RunDuration      time.Duration
    ResourcesLocked  int
    ChainMergeCount  int
}

func (m *Modular) GetMetrics() OperationMetrics
```

#### 7.3 Validation
```go
func validateOperation(op Operation) error {
    // Validate dynamic ops have required callbacks
    // Validate resource types are correct
    // Validate names are descriptive
}
```

**Deliverables:**
- [ ] 4 comprehensive example tests
- [ ] Metrics/observability
- [ ] Operation validation
- [ ] Performance profiling results

## Implementation Priority

### P0: Must Have (For Initial Release)
- [x] Dynamic operation pattern ✅
- [x] Chain merging ✅
- [x] Example test ✅
- [ ] Basic documentation (README.md)
- [ ] Deprecate old operations
- [ ] Error handling for missing resources

### P1: Should Have (For Production)
- [ ] Resource tracking registry
- [ ] Comprehensive testing
- [ ] Developer guide
- [ ] Mock backend for local testing
- [ ] API simplification helpers

### P2: Nice to Have (For Optimization)
- [ ] Resource caching
- [ ] Parallel PrePlan
- [ ] Metrics/observability
- [ ] Validation framework

## Success Criteria

**Quantitative:**
1. ✅ 6+ dynamic operations implemented
2. 📊 80%+ code coverage for dynamic infrastructure
3. 📊 <10% PrePlan overhead vs total test time
4. 📊 0 flaky tests due to dynamic selection

**Qualitative:**
1. 📚 New contributor can add dynamic operation in <30min (measured via user study)
2. 📚 90%+ of questions answered by docs (measured via issue tracker)
3. 🎯 Zero confusion about static vs dynamic (measured via code review feedback)
4. 🎯 All operations follow consistent pattern (measured via lint rules)

## Next Steps (Priority Order)

1. **Documentation** (Phase 1) - Unblocks adoption
2. **Resource Registry** (Phase 3) - Solves known limitation
3. **Testing** (Phase 4) - Ensures reliability
4. **Error Handling** (Phase 5) - Production hardening
5. **API Improvements** (Phase 2) - Better developer experience
6. **Performance** (Phase 6) - Optimization
7. **Polish** (Phase 7) - Final touches

## Timeline Estimate

- **Week 1**: Phase 1 (Documentation) + Phase 2 (API)
- **Week 2**: Phase 3 (Registry) + Phase 4 (Testing)
- **Week 3**: Phase 5 (Errors) + Phase 6 (Performance)
- **Week 4**: Phase 7 (Polish) + Buffer

**Total: 4 weeks to production-ready feature**

---

*Last Updated: 2025-12-18*
