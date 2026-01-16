package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/sql/randgen"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/types"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
	"github.com/lib/pq/oid"
)

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

// AddRandomIndexDynamicOp implements the Operation interface for adding random indexes
// using the DynamicStep pattern with proper resource locking.
type AddRandomIndexDynamicOp struct {
	name    string
	builder *modular.OperationBuilder
}

// Chain returns the operation's chain of steps.
func (a *AddRandomIndexDynamicOp) Chain() modular.Chain {
	return a.builder.Chain
}

// Name returns the operation's name.
func (a *AddRandomIndexDynamicOp) Name() string {
	return a.name
}

func (a *AddRandomIndexDynamicOp) Timeout() time.Duration {
	return 30 * time.Minute
}

// AddRandomIndexDynamic creates an operation that adds a random index to a random table.
// This version uses DynamicStep to separate planning (table selection) from execution (index creation).
//
// The operation works in two phases:
// 1. PrePlan: Select a random table with suitable columns (without holding locks)
// 2. Run: Create the index on the selected table (with proper schema change locks)
func AddRandomIndexDynamic() modular.Operation {
	builder := modular.NewDynamicOperation[*IndexPlan]("add index").
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
			predicateClause := ""

			// Determine index type first (inverted, hash, or regular)
			// These must come before WHERE clause in SQL syntax
			if _, exists := types.OidToType[oid.Oid(colType)]; exists {
				// For now, skip INVERTED indexes entirely to avoid type compatibility issues
				// Only use HASH or regular indexes
				if rng.Intn(2) == 0 {
					indexArgs = append(indexArgs, "USING HASH")
				}
			}

			// Create partial index based on column type
			// WHERE clause must come after USING HASH or INVERTED
			if typ, exists := types.OidToType[oid.Oid(colType)]; exists {
				randomValue := randgen.RandDatum(rng, typ, false)
				predicates := []string{"<", ">", "<=", ">=", "=", "<>"}
				predicate := predicates[rng.Intn(len(predicates))]
				str := tree.AsStringWithFlags(randomValue, tree.FmtParsable)

				// 50% chance of making partial index
				if rng.Intn(2) != 0 {
					predicateClause = fmt.Sprintf("WHERE (%s %s %s)", colName, predicate, str)
					indexArgs = append(indexArgs, predicateClause)
				}
			}

			l.Printf("Planned index creation on %s.%s(%s) with options: %v",
				dbName, tableName, colName, indexArgs)

			return &IndexPlan{
				DBName:        dbName,
				TableName:     tableName,
				ColumnName:    colName,
				ColumnType:    colType,
				IndexArgs:     indexArgs,
				PredicateText: predicateClause,
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
			// The lock is acquired automatically by the runtime based on the resource declaration below
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

// AddIndexToTable creates an operation that adds an index to a specific table.
// Since the table is known upfront, this uses simple steps with declarative resource access.
func AddIndexToTable(dbName, tableName, columnName string) modular.Operation {
	builder := modular.NewOperation(
		modular.NewStep(
			fmt.Sprintf("add index to %s.%s(%s)", dbName, tableName, columnName),
			func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
				rng, _ := randutil.NewPseudoRand()

				// Get the columns for the table
				columns, err := h.GetTableColumns(dbName, tableName)
				if err != nil {
					return fmt.Errorf("failed to get table columns: %w", err)
				}

				// Find the specified column
				var colType int32
				found := false
				for _, col := range columns {
					if col.Name == columnName {
						colType = int32(col.Type)
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("column %s not found in table %s.%s", columnName, dbName, tableName)
				}

				// Generate index options based on column type
				var indexArgs []string
				if typ, exists := types.OidToType[oid.Oid(colType)]; exists {
					// INVERTED indexes only work for JSONB, arrays, and geo types
					// (not plain STRING/TEXT columns)
					if (typ.Family() == types.ArrayFamily ||
						typ.Family() == types.JsonFamily ||
						typ.Family() == types.GeographyFamily ||
						typ.Family() == types.GeometryFamily) && rng.Intn(2) == 0 {
						indexArgs = append(indexArgs, "INVERTED")
					} else if rng.Intn(2) == 0 {
						indexArgs = append(indexArgs, "USING HASH")
					}
				}

				l.Printf("Creating index on %s.%s(%s) with options: %v",
					dbName, tableName, columnName, indexArgs)

				// Create the index (lock is automatically acquired via AcquireLock option)
				indexName, err := h.CreateIndex("idx", dbName, tableName,
					[]string{columnName}, indexArgs...)
				if err != nil {
					return fmt.Errorf("failed to create index: %w", err)
				}

				l.Printf("Successfully created index %s on %s.%s(%s)",
					indexName, dbName, tableName, columnName)
				return nil
			},
			// Declare the resource access upfront since we know the table at build time
			modular.AcquireLock(modular.SchemaChangeAccess{
				Database: dbName,
				Table:    tableName,
			}),
		),
	)

	return &AddRandomIndexDynamicOp{
		name:    fmt.Sprintf("add-index-%s-%s-%s", dbName, tableName, columnName),
		builder: builder,
	}
}
