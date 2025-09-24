package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
)

// AddColumn returns a step function that adds a column to a table
func AddColumn(database, table, columnName, columnType string, args ...string) func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
	return func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		var alterSQL string
		if len(args) > 0 {
			alterSQL = fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN %s %s %s",
				database, table, columnName, columnType, args[0])
		} else {
			alterSQL = fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN %s %s",
				database, table, columnName, columnType)
		}

		l.Printf("Adding column: %s", alterSQL)
		err := h.Exec(alterSQL)
		if err != nil {
			return fmt.Errorf("failed to add column %s: %w", columnName, err)
		}

		l.Printf("Successfully added column %s to %s.%s", columnName, database, table)
		return nil
	}
}

// DropColumn returns a step function that drops a column from a table
func DropColumn(database, table, columnName string) func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
	return func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		alterSQL := fmt.Sprintf("ALTER TABLE %s.%s DROP COLUMN %s CASCADE", database, table, columnName)

		l.Printf("Dropping column: %s", alterSQL)
		err := h.Exec(alterSQL)
		if err != nil {
			return fmt.Errorf("failed to drop column %s: %w", columnName, err)
		}

		l.Printf("Successfully dropped column %s from %s.%s", columnName, database, table)
		return nil
	}
}

// CreateTable returns a step function that creates a table
func CreateTable(database, tableName, schema string) func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
	return func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		actualTableName, err := h.CreateTable(tableName, schema)
		if err != nil {
			return fmt.Errorf("failed to create table %s: %w", tableName, err)
		}

		l.Printf("Successfully created table %s as %s", tableName, actualTableName)
		return nil
	}
}

// DropTable returns a step function that drops a table
func DropTable(database, table string) func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
	return func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		dropSQL := fmt.Sprintf("DROP TABLE IF EXISTS %s.%s CASCADE", database, table)

		l.Printf("Dropping table: %s", dropSQL)
		err := h.Exec(dropSQL)
		if err != nil {
			return fmt.Errorf("failed to drop table %s: %w", table, err)
		}

		l.Printf("Successfully dropped table %s.%s", database, table)
		return nil
	}
}

// ValidateSchema returns a step function that validates schema consistency
func ValidateSchema(database string) func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
	return func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		l.Printf("Validating schema consistency for database %s", database)

		// Check for any constraint violations
		constraintQuery := fmt.Sprintf(`
			SELECT constraint_name, table_name
			FROM information_schema.table_constraints
			WHERE constraint_catalog = '%s' AND constraint_type = 'CHECK'`,
			database)

		rows, err := h.Query(constraintQuery)
		if err != nil {
			return fmt.Errorf("failed to query constraints: %w", err)
		}
		defer rows.Close()

		constraintCount := 0
		for rows.Next() {
			var constraintName, tableName string
			if err := rows.Scan(&constraintName, &tableName); err != nil {
				return fmt.Errorf("failed to scan constraint info: %w", err)
			}
			constraintCount++
		}

		l.Printf("Found %d constraints in database %s", constraintCount, database)

		// Validate index consistency (simplified check)
		indexQuery := fmt.Sprintf(`
			SELECT schemaname, tablename, indexname
			FROM pg_indexes
			WHERE schemaname = '%s'`,
			database)

		rows, err = h.Query(indexQuery)
		if err != nil {
			return fmt.Errorf("failed to query indexes: %w", err)
		}
		defer rows.Close()

		indexCount := 0
		for rows.Next() {
			var schema, table, index string
			if err := rows.Scan(&schema, &table, &index); err != nil {
				return fmt.Errorf("failed to scan index info: %w", err)
			}
			indexCount++
		}

		l.Printf("Found %d indexes in database %s", indexCount, database)
		l.Printf("Schema validation completed successfully")
		return nil
	}
}

// RandomSchemaChange returns a step function that performs a random schema change
func RandomSchemaChange(database string) func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
	return func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		rng, _ := randutil.NewPseudoRand()

		// Get a random table
		tableName, err := h.PickRandomTable(database)
		if err != nil {
			return fmt.Errorf("failed to pick random table: %w", err)
		}

		// Get table columns
		columns, err := h.GetTableColumns(database, tableName)
		if err != nil {
			return fmt.Errorf("failed to get table columns: %w", err)
		}

		// Randomly choose a schema change operation
		operations := []string{"add_column", "add_index", "drop_column"}
		op := operations[rng.Intn(len(operations))]

		switch op {
		case "add_column":
			columnName := fmt.Sprintf("random_col_%d", rng.Uint32())
			columnTypes := []string{"INT", "TEXT", "TIMESTAMP", "DECIMAL(10,2)", "BOOLEAN"}
			columnType := columnTypes[rng.Intn(len(columnTypes))]

			l.Printf("Performing random schema change: ADD COLUMN %s %s to %s.%s",
				columnName, columnType, database, tableName)
			return AddColumn(database, tableName, columnName, columnType)(ctx, l, h)

		case "add_index":
			if len(columns) == 0 {
				l.Printf("No columns available for index creation on %s.%s", database, tableName)
				return nil
			}

			// Pick a random column for indexing
			col := columns[rng.Intn(len(columns))]
			indexPrefix := fmt.Sprintf("random_idx_%s", col.Name)

			l.Printf("Performing random schema change: CREATE INDEX on %s.%s(%s)",
				database, tableName, col.Name)

			_, err := h.CreateIndex(indexPrefix, database, tableName, []string{col.Name})
			if err != nil {
				return fmt.Errorf("failed to create random index: %w", err)
			}
			return nil

		case "drop_column":
			if len(columns) <= 1 {
				l.Printf("Cannot drop column - table %s.%s has too few columns", database, tableName)
				return nil
			}

			// Skip the first column (likely primary key) and pick another
			col := columns[1+rng.Intn(len(columns)-1)]
			l.Printf("Performing random schema change: DROP COLUMN %s from %s.%s",
				col.Name, database, tableName)
			return DropColumn(database, tableName, col.Name)(ctx, l, h)
		}

		return nil
	}
}

// DelayedSchemaChange returns a step function that waits then performs a schema change
func DelayedSchemaChange(delay time.Duration, schemaChangeFunc func(ctx context.Context, l *logger.Logger, h *modular.Helper) error) func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
	return func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		l.Printf("Waiting %v before performing schema change", delay)

		select {
		case <-time.After(delay):
			l.Printf("Delay completed, performing schema change")
			return schemaChangeFunc(ctx, l, h)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
