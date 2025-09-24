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

// AddRandomIndexOp implements the Operation interface for adding random indexes.
type AddRandomIndexOp struct {
	name    string
	builder *modular.OperationBuilder
}

// Chain returns the operation's chain of steps.
func (a *AddRandomIndexOp) Chain() modular.Chain {
	return a.builder.Chain
}

// Name returns the operation's name.
func (a *AddRandomIndexOp) Name() string {
	return a.name
}

func (a *AddRandomIndexOp) Precondition() bool {
	return true
}

func (a *AddRandomIndexOp) Timeout() time.Duration {
	return 30 * time.Minute
}

// AddRandomIndex creates an operation that adds a random index to a random table.
// It picks a random database, random table with multiple columns, and creates
// various types of indexes (standard, inverted, hash, partial).
func AddRandomIndex() modular.Operation {
	builder := modular.NewOperation("add random index", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		rng, _ := randutil.NewPseudoRand()

		// Use SearchTable to find a table with multiple columns
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
			return nil // Not a failure, just no suitable tables
		}

		// Get the columns for the selected table
		columns, err := h.GetTableColumns(dbName, tableName)
		if err != nil {
			return fmt.Errorf("failed to get table columns: %w", err)
		}

		// Pick a random column (skip the first one to avoid primary key)
		colIndex := 1 + rng.Intn(len(columns)-1)
		colName := columns[colIndex].Name
		colType := columns[colIndex].Type

		// Generate various index options
		var indexArgs []string
		predicateClause := ""

		// Create partial index based on column type
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

		// Determine index type (inverted, hash, or regular)
		if typ, exists := types.OidToType[oid.Oid(colType)]; exists {
			if (typ.Family() == types.ArrayFamily ||
				typ.Family() == types.JsonFamily ||
				typ.Family() == types.GeographyFamily ||
				typ.Family() == types.GeometryFamily ||
				typ.Family() == types.StringFamily) && rng.Intn(2) == 0 {
				indexArgs = append(indexArgs, "INVERTED")
			} else if rng.Intn(2) == 0 {
				indexArgs = append(indexArgs, "USING HASH")
			}
		}

		l.Printf("Creating random index on table %s.%s column %s with options: %v",
			dbName, tableName, colName, indexArgs)

		// Create the index using the helper
		indexName, err := h.CreateIndex("random_idx", dbName, tableName, []string{colName}, indexArgs...)
		if err != nil {
			return fmt.Errorf("failed to create random index: %w", err)
		}

		l.Printf("Successfully created random index %s on %s.%s(%s)", indexName, dbName, tableName, colName)
		return nil
	})

	return &AddRandomIndexOp{
		name:    "add-random-index",
		builder: builder,
	}
}
