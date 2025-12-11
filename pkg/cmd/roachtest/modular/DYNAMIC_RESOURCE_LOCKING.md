# Resource Locking in Modular Roachtests

## Static Resource Locking

Operations declare resource access at construction time. The planner uses these declarations to build a dependency graph and determine which operations can run in parallel.

**Example operations from `pkg/cmd/roachtest/modular/operations/`:**

```go
// AddIndexToTable creates an operation that adds an index to a specific table.
// Since the table is known upfront, this uses simple steps with declarative resource access.
func AddIndexToTable(dbName, tableName, columnName string) modular.Operation {
    builder := modular.NewOperation(
        modular.NewStep(
            fmt.Sprintf("add index to %s.%s(%s)", dbName, tableName, columnName),
            func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
                indexName, err := h.CreateIndex("idx", dbName, tableName,
                    []string{columnName})
                if err != nil {
                    return fmt.Errorf("failed to create index: %w", err)
                }

                l.Printf("Successfully created index %s", indexName)
                return nil
            },
            // Declare the resource access upfront since we know the table at build time
            modular.AcquireLock(modular.SchemaChangeAccess{
                Database: dbName,
                Table:    tableName,
            }),
        ),
    )

    return &AddIndexOp{
        name:    fmt.Sprintf("add-index-%s-%s-%s", dbName, tableName, columnName),
        builder: builder,
    }
}

// AddColumnToTable creates an operation that adds a column to a specific table.
func AddColumnToTable(dbName, tableName, columnName, columnType string) modular.Operation {
    builder := modular.NewOperation(
        modular.NewStep(
            fmt.Sprintf("add column to %s.%s", dbName, tableName),
            func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
                colDef := fmt.Sprintf("%s %s", columnName, columnType)
                query := fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN %s", dbName, tableName, colDef)

                if err := h.Exec(query); err != nil {
                    return fmt.Errorf("failed to add column: %w", err)
                }

                l.Printf("Successfully added column %s to %s.%s", columnName, dbName, tableName)
                return nil
            },
            // Declare the resource access upfront
            modular.AcquireLock(modular.SchemaChangeAccess{
                Database: dbName,
                Table:    tableName,
            }),
        ),
    )

    return &AddColumnOp{
        name:    fmt.Sprintf("add-column-%s-%s-%s", dbName, tableName, columnName),
        builder: builder,
    }
}
```

**Test case demonstrating chain merging:**

```go
func TestStaticResourceLocking(t *testing.T) {
    // Setup stage creates the table
    test := modular.NewTest(ctx, l, c, crdbNodes)
    test.Setup("create test table", func(ctx, l, h) error {
        return h.Exec("CREATE TABLE test_db.tableA (id INT PRIMARY KEY, name STRING)")
    })

    // Create a stage with two operations that both access test_db.tableA
    stage := test.NewStage("schema changes")
    test.AddOperation(stage, AddIndexToTable("test_db", "tableA", "name"))
    test.AddOperation(stage, AddColumnToTable("test_db", "tableA", "email", "STRING"))

    // Before merge: Two parallel chains
    //    [add index to test_db.tableA(name)]    [add column to test_db.tableA]
    //
    // Both operations declare SchemaChangeAccess{Database: "test_db", Table: "tableA"}
    // The planner detects this conflict.
    //
    // After merge: Single sequential chain
    //    [add index to test_db.tableA(name)] → [add column to test_db.tableA]
    //
    // Operations are serialized because they access the same resource.

    planner := test.NewPlanner()
    planner.Run(ctx)
}
```

**DAG visualization:**

```
Before merge:
                                    [schema changes]
   ┌─────────────────────────────┐     ┌─────────────────────────────┐
   │ add index to test_db.tableA │     │ add column to test_db.tableA│
   │          (name)              │     │                             │
   └─────────────────────────────┘     └─────────────────────────────┘

After merge:
                                    [schema changes]
   ┌─────────────────────────────┐
   │ add index to test_db.tableA │
   │          (name)              │
   └─────────────────────────────┘
                 │
                 │
                 ▼
   ┌─────────────────────────────┐
   │ add column to test_db.tableA│
   │                             │
   └─────────────────────────────┘
```

Static locking requires knowing the resource at build time. Operations that select resources at runtime (e.g., "add index to a random table") cannot declare their locks statically.

---

## Dynamic Resource Access: Three Approaches

### Approach 1: Unique State Space Per Operation

Each operation creates and manages its own isolated resources.

**Example:**

```go
// AddIndexWithOwnState creates an operation that creates its own isolated table and adds an index.
func AddIndexWithOwnState() modular.Operation {
    var dbName, tableName string

    builder := modular.NewOperation(
        modular.NewStep("create unique table", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
            // Generate unique database and table names for this operation
            dbName = h.GenerateUniqueName("testdb")
            tableName = h.GenerateUniqueName("users")

            // Create database and table
            if err := h.Exec(fmt.Sprintf("CREATE DATABASE %s", dbName)); err != nil {
                return fmt.Errorf("failed to create database: %w", err)
            }

            if err := h.Exec(fmt.Sprintf(
                "CREATE TABLE %s.%s (id INT PRIMARY KEY, name STRING)",
                dbName, tableName)); err != nil {
                return fmt.Errorf("failed to create table: %w", err)
            }

            // Populate with data
            if err := h.Exec(fmt.Sprintf(
                "INSERT INTO %s.%s SELECT generate_series(1, 1000), 'user'",
                dbName, tableName)); err != nil {
                return fmt.Errorf("failed to populate table: %w", err)
            }

            l.Printf("Created unique table %s.%s", dbName, tableName)
            return nil
        }),
    ).Then(
        modular.NewStep("add index to unique table", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
            // Create index on our isolated table
            indexName, err := h.CreateIndex("idx", dbName, tableName, []string{"name"})
            if err != nil {
                return fmt.Errorf("failed to create index: %w", err)
            }

            l.Printf("Successfully created index %s on %s.%s", indexName, dbName, tableName)
            return nil
        }),
        // No locks needed - we own all our resources exclusively
    )

    return &AddIndexWithOwnStateOp{
        name:    "add-index-with-own-state",
        builder: builder,
    }
}
```

**Characteristics:**
- No lock conflicts - each operation owns its resources exclusively
- Full parallelization possible
- Each operation must create and populate its own data
- Doesn't test operations on shared state
- Equivalent to traditional isolated roachtest functions

**Limitation:** This approach essentially reverts to what roachtest already provides. It doesn't leverage shared state or test realistic multi-operation scenarios on the same cluster.

---

### Approach 2: Runtime Resource Locking

Steps acquire locks at runtime as they select resources.

**Example: `AddRandomIndex` from `pkg/cmd/roachtest/modular/operations/add_index.go`**

```go
// AddRandomIndex creates an operation that adds a random index to a random table.
func AddRandomIndex() modular.Operation {
    builder := modular.NewOperation(
        modular.NewStep("add random index", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
            rng, _ := randutil.NewPseudoRand()
            unlock := func() {}
            defer unlock()

            // SearchTable finds a table while acquiring a lock
            dbName, tableName, err := h.SearchTable(func(dbName, tableName string) bool {
                var success bool
                unlock, success = h.AcquireLock(modular.SchemaChangeAccess{
                    Database: dbName,
                    Table:    tableName,
                })
                if !success {
                    return false
                }

                columns, err := h.GetTableColumns(dbName, tableName)
                if err != nil {
                    l.Printf("error getting table columns: %v", err)
                    return false
                }
                return len(columns) >= 2 // Need at least 2 columns for indexing
            })
            if err != nil {
                l.Printf("No suitable table found for index creation: %v", err)
                return nil
            }

            // Get columns and pick one to index
            columns, _ := h.GetTableColumns(dbName, tableName)
            colIndex := 1 + rng.Intn(len(columns)-1)
            colName := columns[colIndex].Name

            // Create the index
            indexName, err := h.CreateIndex("random_idx", dbName, tableName,
                []string{colName})
            if err != nil {
                return fmt.Errorf("failed to create index: %w", err)
            }

            l.Printf("Successfully created index %s on %s.%s(%s)",
                indexName, dbName, tableName, colName)
            return nil
        }),
        // No static lock declaration - locks acquired at runtime
    )

    return &AddRandomIndexOp{
        name:    "add-random-index",
        builder: builder,
    }
}
```

**Test case demonstrating runtime lock conflicts:**

```go
func TestRuntimeResourceLocking(t *testing.T) {
    // Setup stage creates two tables
    test := modular.NewTest(ctx, l, c, crdbNodes)
    test.Setup("create test tables", func(ctx, l, h) error {
        h.Exec("CREATE TABLE test_db.tableA (id INT PRIMARY KEY, name STRING)")
        h.Exec("CREATE TABLE test_db.tableB (id INT PRIMARY KEY, value INT)")
        return nil
    })

    // Create a stage with two operations that both use runtime locking
    stage := test.NewStage("schema changes")
    test.AddOperation(stage, AddRandomIndex())  // May select tableA or tableB
    test.AddOperation(stage, AddRandomIndex())  // May select tableA or tableB

    // Before execution: DAG shows both operations in parallel
    //    [add random index]    [add random index]
    //
    // The DAG cannot show dependencies because resource selection happens at runtime.
    //
    // During execution (scenario 1): Both operations select tableA
    //    Operation 1: Searches tables, tries to lock tableA -> succeeds
    //    Operation 2: Searches tables, tries to lock tableA -> fails (already locked)
    //    Operation 2: Searches tables, tries to lock tableB -> succeeds
    //    Result: Operation 1 indexes tableA, Operation 2 indexes tableB
    //
    // During execution (scenario 2): Both operations select different tables
    //    Operation 1: Searches tables, tries to lock tableA -> succeeds
    //    Operation 2: Searches tables, tries to lock tableB -> succeeds
    //    Result: Both operations run in parallel, non-deterministic order
    //
    // The actual execution order and table selection is non-deterministic and
    // depends on timing, making it difficult to reason about the test behavior.

    planner := test.NewPlanner()
    planner.Run(ctx)
}
```

**Characteristics:**
- Resources selected and locked during step execution
- Dependencies only known at runtime
- Race conditions in resource selection
- DAG cannot show runtime dependencies
- Chain merging disabled - planner lacks dependency information

**Limitations:**

1. **Hidden dependencies**: Two operations selecting the same resource won't show a dependency in the DAG until runtime.
2. **Non-determinism**: Concurrent operations can race to select and lock the same resource, leading to unpredictable execution order.
3. **No chain merging**: Without upfront dependency information, the planner cannot optimize execution.

---

### Approach 3: Dynamic Pre-Planning

Before stage execution, operations run a planning phase to select resources and declare locks. Execution then proceeds with static locking semantics.

**Example: `AddRandomIndexDynamic` from `pkg/cmd/roachtest/modular/operations/add_index_dynamic.go`**

```go
// IndexPlan contains all the information needed to create an index,
// determined during the PrePlan phase.
type IndexPlan struct {
    DBName        string
    TableName     string
    ColumnName    string
    ColumnType    int32
    IndexArgs     []string
    PredicateText string
}

// AddRandomIndexDynamic creates an operation that adds a random index to a random table.
// This version uses DynamicStep to separate planning (table selection) from execution (index creation).
func AddRandomIndexDynamic() modular.Operation {
    builder := modular.NewDynamicOperation[*IndexPlan]("add random index (dynamic)").
        PrePlan(func(ctx context.Context, l *logger.Logger, h *modular.Helper) (*IndexPlan, error) {
            rng, _ := randutil.NewPseudoRand()

            // Search for a suitable table (this doesn't hold locks during planning)
            dbName, tableName, err := h.SearchTable(func(dbName, tableName string) bool {
                columns, err := h.GetTableColumns(dbName, tableName)
                if err != nil {
                    l.Printf("error getting table columns: %v", err)
                    return false
                }
                return len(columns) >= 2 // Need at least 2 columns for indexing
            })
            if err != nil {
                l.Printf("No suitable table found for index creation: %v", err)
                return nil, err
            }

            // Get the columns for the selected table
            columns, err := h.GetTableColumns(dbName, tableName)
            if err != nil {
                return nil, fmt.Errorf("failed to get table columns: %w", err)
            }

            // Pick a random column (skip the first one to avoid primary key)
            colIndex := 1 + rng.Intn(len(columns)-1)
            colName := columns[colIndex].Name
            colType := int32(columns[colIndex].Type)

            // Generate index options during planning
            var indexArgs []string
            if rng.Intn(2) == 0 {
                indexArgs = append(indexArgs, "USING HASH")
            }

            l.Printf("Planned index creation on %s.%s(%s) with options: %v",
                dbName, tableName, colName, indexArgs)

            return &IndexPlan{
                DBName:     dbName,
                TableName:  tableName,
                ColumnName: colName,
                ColumnType: colType,
                IndexArgs:  indexArgs,
            }, nil
        }).
        WithRun(func(ctx context.Context, l *logger.Logger, h *modular.Helper, plan *IndexPlan) error {
            if plan == nil {
                l.Printf("No index plan available, skipping index creation")
                return nil
            }

            l.Printf("Creating random index on table %s.%s column %s with options: %v",
                plan.DBName, plan.TableName, plan.ColumnName, plan.IndexArgs)

            // Create the index using the helper
            // The lock is acquired automatically by the runtime based on the resource declaration
            indexName, err := h.CreateIndex("random_idx", plan.DBName, plan.TableName,
                []string{plan.ColumnName}, plan.IndexArgs...)
            if err != nil {
                return fmt.Errorf("failed to create random index: %w", err)
            }

            l.Printf("Successfully created random index %s on %s.%s(%s)",
                indexName, plan.DBName, plan.TableName, plan.ColumnName)
            return nil
        }).
        WithDynamicResourceCallback(func(plan *IndexPlan) ([]modular.ResourceAccess, []modular.ResourceAccess) {
            access := modular.SchemaChangeAccess{
                Database: plan.DBName,
                Table:    plan.TableName,
            }.Resource(true)
            return []modular.ResourceAccess{access}, []modular.ResourceAccess{access}
        }).
        WithDynamicName(func(plan *IndexPlan) string {
            return fmt.Sprintf("add random index to %s.%s", plan.DBName, plan.TableName)
        })

    return &AddRandomIndexDynamicOp{
        name:    "add-random-index-dynamic",
        builder: modular.NewOperation(builder),
    }
}
```

**Test case demonstrating dynamic pre-planning with chain merging:**

```go
func TestDynamicPrePlanning(t *testing.T) {
    // Setup stage creates three tables
    test := modular.NewTest(ctx, l, c, crdbNodes)
    test.Setup("create test tables", func(ctx, l, h) error {
        h.Exec("CREATE TABLE test_db.users (id INT PRIMARY KEY, name STRING)")
        h.Exec("CREATE TABLE test_db.orders (id INT PRIMARY KEY, user_id INT)")
        h.Exec("CREATE TABLE test_db.products (id INT PRIMARY KEY, price DECIMAL)")
        return nil
    })

    // Create a stage with three operations using dynamic pre-planning
    stage := test.NewStage("schema changes")
    test.AddOperation(stage, AddRandomIndexDynamic())  // Operation A
    test.AddOperation(stage, AddRandomIndexDynamic())  // Operation B
    test.AddOperation(stage, AddRandomIndexDynamic())  // Operation C

    // Phase 1: PrePlan - Operations select resources (deterministic)
    //    Operation A: PrePlan() → selects "users" table → returns IndexPlan{DBName: "test_db", TableName: "users"}
    //    Operation B: PrePlan() → selects "users" table → returns IndexPlan{DBName: "test_db", TableName: "users"}
    //    Operation C: PrePlan() → selects "orders" table → returns IndexPlan{DBName: "test_db", TableName: "orders"}

    // Phase 2: Dependency Resolution - Planner calls WithDynamicResourceCallback on each plan
    //    Operation A plan → declares SchemaChangeAccess{Database: "test_db", Table: "users"}
    //    Operation B plan → declares SchemaChangeAccess{Database: "test_db", Table: "users"}
    //    Operation C plan → declares SchemaChangeAccess{Database: "test_db", Table: "orders"}
    //
    // Planner detects conflict: Operations A and B both access "test_db.users"

    // Before merge: Three parallel chains
    //    [add random index (dynamic)]    [add random index (dynamic)]    [add random index (dynamic)]
    //         (unknown table)                 (unknown table)                  (unknown table)
    //
    // After PrePlan and merge: Sequential and parallel chains with known tables
    //    [add random index to test_db.users] → [add random index to test_db.users]    [add random index to test_db.orders]
    //              (Operation A)                          (Operation B)                        (Operation C - parallel)
    //
    // Operations A and B are merged into a sequential chain because they access the same resource.
    // Operation C runs in parallel because it accesses a different resource.

    // Phase 3: Execution - Operations execute WithRun functions following the dependency graph
    //    Step 1: Operation A and Operation C run in parallel (different tables)
    //    Step 2: Operation B runs after Operation A completes (same table)

    planner := test.NewPlanner()
    planner.Run(ctx)
}
```

**Execution flow:**

1. **Pre-Plan Phase**: All operations in the stage execute their `PrePlan` functions sequentially, selecting resources and building plan objects.

2. **Dependency Resolution**: The planner calls `WithDynamicResourceCallback` on each plan to get resource declarations, then builds the dependency graph.

3. **Chain Merging**: Operations accessing the same resources are merged into sequential chains. Operations accessing different resources remain parallel.

4. **Execution Phase**: Operations execute their `WithRun` functions using the pre-planned decisions, following the merged dependency graph.

**Characteristics:**
- All resource decisions made before execution
- Dependencies visible in DAG
- Deterministic execution order
- Chain merging enabled
- Operations work on shared state
- Requires plan struct and two-phase pattern

**Drawbacks:**
- More verbose than runtime locking
- Requires separating planning logic from execution logic
- Need to define a plan struct to hold decisions

---

## Comparison

| Aspect | Unique State | Runtime Locking | Pre-Planning |
|--------|-------------|----------------|--------------|
| Dependencies in DAG | N/A | No | Yes |
| Deterministic | Yes | No | Yes |
| Chain Merging | Yes | No | Yes |
| Shared State Testing | No | Yes | Yes |
| Verbosity | High (setup) | Low | Medium |
| Framework Value | Low | Medium | High |

---

## Builder API

```go
modular.NewDynamicOperation[PlanType]("operation name").
    PrePlan(prePlanFunc).                      // Required: returns PlanType
    WithRun(runFunc).                          // Required: receives PlanType
    WithDynamicResourceCallback(resourceFunc). // Required: declares locks from PlanType
    WithDynamicName(nameFunc)                  // Optional: dynamic naming
```

The builder implements `StepProtocol` directly and can be used in operation chains without calling `.Build()`.
