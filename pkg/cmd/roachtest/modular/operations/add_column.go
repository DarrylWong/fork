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
)

// AddRandomColumnOp implements the Operation interface for adding random columns.
type AddRandomColumnOp struct {
	name    string
	builder *modular.OperationBuilder
}

// Chain returns the operation's chain of steps.
func (a *AddRandomColumnOp) Chain() modular.Chain {
	return a.builder.Chain
}

// Name returns the operation's name.
func (a *AddRandomColumnOp) Name() string {
	return a.name
}

func (a *AddRandomColumnOp) Timeout() time.Duration {
	return 30 * time.Minute
}

// AddRandomColumn creates an operation that adds a random column to a random table.
// It picks a random database, random table, and creates a column with a random type
// and optional constraints.
func AddRandomColumn() modular.Operation {
	builder := modular.NewOperation("add random column", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		rng, _ := randutil.NewPseudoRand()

		unlock := func() {}
		defer unlock()
		dbName, tableName, err := h.SearchTable(func(dbName, tableName string) bool {
			var success bool
			unlock, success = h.AcquireLock(modular.SchemaChangeAccess{
				Database: dbName,
				Table:    tableName,
			})
			return success
		})
		if err != nil {
			l.Printf("No suitable table found for column addition: %v", err)
			return nil // Not a failure, just no suitable tables
		}

		// Pick a random column type
		columnTypes := []*types.T{
			types.Int,
			types.String,
			types.Bool,
			types.Float,
			types.Decimal,
			types.Timestamp,
			types.Uuid,
			types.Bytes,
			types.Jsonb,
			types.MakeArray(types.Int),
		}
		colType := columnTypes[rng.Intn(len(columnTypes))]

		// Generate column name
		colName := fmt.Sprintf("col_%d", time.Now().UnixNano()%1000000)

		// Build column definition
		colDef := fmt.Sprintf("%s %s", colName, colType.SQLString())

		// Add optional constraints/defaults
		var constraints []string

		// 30% chance of NULL constraint
		if rng.Intn(10) < 3 {
			constraints = append(constraints, "NULL")
		}

		// 20% chance of adding a default value
		if rng.Intn(10) < 2 {
			defaultValue := randgen.RandDatum(rng, colType, true)
			if defaultValue != tree.DNull {
				defaultStr := tree.AsStringWithFlags(defaultValue, tree.FmtParsable)
				constraints = append(constraints, fmt.Sprintf("DEFAULT %s", defaultStr))
			}
		}

		// Append constraints to column definition
		if len(constraints) > 0 {
			for _, constraint := range constraints {
				colDef += " " + constraint
			}
		}

		l.Printf("Adding column to table %s.%s: %s", dbName, tableName, colDef)

		// Execute the ALTER TABLE ADD COLUMN statement
		query := fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN %s", dbName, tableName, colDef)
		if err := h.Exec(query); err != nil {
			return fmt.Errorf("failed to add column: %w", err)
		}

		l.Printf("Successfully added column %s to %s.%s", colName, dbName, tableName)
		return nil
	})

	return &AddRandomColumnOp{
		name:    "add-random-column",
		builder: builder,
	}
}
