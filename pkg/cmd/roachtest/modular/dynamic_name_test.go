package modular

import (
	"context"
	"io"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/stretchr/testify/require"
)

func TestDynamicName(t *testing.T) {
	// Create a simple plan type for testing
	type TestPlan struct {
		TableName string
		Action    string
	}

	// Create a dynamic step with a dynamic name callback
	step := NewDynamicStep(
		"generic operation",
		// PrePlan: select a table
		func(ctx context.Context, l *logger.Logger, h *Helper) (*TestPlan, error) {
			return &TestPlan{
				TableName: "users",
				Action:    "add_index",
			}, nil
		},
		// Run: execute the operation
		func(ctx context.Context, l *logger.Logger, h *Helper, plan *TestPlan) error {
			return nil
		},
		// Dynamic name callback
		WithDynamicName(func(plan *TestPlan) string {
			return plan.Action + " on table " + plan.TableName
		}),
	)

	// Before PrePlan, should return the base description
	require.Equal(t, "generic operation", step.Description())

	// Create a nil logger for testing
	cfg := logger.Config{
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
	l, err := cfg.NewLogger("")
	require.NoError(t, err)

	// Call PrePlan
	ctx := context.Background()
	h := &Helper{}
	_, err = step.PrePlan(ctx, l, h)
	require.NoError(t, err)

	// After PrePlan, should return the dynamic name
	require.Equal(t, "add_index on table users", step.Description())
}
