// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package cli

import (
	"testing"

	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgcode"
	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgerror"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
)

// TestIsRetryError tests the isRetryError function.
func TestIsRetryError(t *testing.T) {
	// Test with a transaction retry error (pgcode 40001)
	retryErr := pgerror.New(pgcode.SerializationFailure, "transaction retry error")
	require.True(t, isRetryError(retryErr), "should detect pgcode 40001 as retry error")

	// Test with a regular error
	regularErr := errors.New("some other error")
	require.False(t, isRetryError(regularErr), "should not detect regular error as retry error")

	// Test with a different pgcode error
	uniqueViolation := pgerror.New(pgcode.UniqueViolation, "unique constraint violation")
	require.False(t, isRetryError(uniqueViolation), "should not detect unique violation as retry error")

	// Test with a wrapped retry error
	wrappedRetryErr := errors.Wrap(retryErr, "wrapped")
	require.True(t, isRetryError(wrappedRetryErr), "should detect wrapped pgcode 40001 as retry error")

	// Test with a different error wrapped in a retry error
	anotherErr := pgerror.New(pgcode.SerializationFailure, "RETRY_WRITE_TOO_OLD")
	require.True(t, isRetryError(anotherErr), "should detect RETRY_WRITE_TOO_OLD as retry error")
}
