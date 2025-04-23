// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package failures

import (
	"context"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
)

const NoopFailureName = "noop"

func registerNoopFailure(r *FailureRegistry) {
	r.add(NoopFailureName, noopFailureArgs{}, MakeNoopFailure)
}

func MakeNoopFailure(
	clusterName string, l *logger.Logger, connectionInfo ConnectionInfo,
) (FailureMode, error) {
	return &noopFailureMode{}, nil
}

type noopFailureMode struct{}

type noopFailureArgs struct {
	InjectedError error
}

func (n noopFailureMode) Description() string {
	return ""
}

func (n noopFailureMode) Setup(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return args.(noopFailureArgs).InjectedError
}

func (n noopFailureMode) Inject(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return args.(noopFailureArgs).InjectedError
}

func (n noopFailureMode) Recover(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return args.(noopFailureArgs).InjectedError
}

func (n noopFailureMode) Cleanup(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return args.(noopFailureArgs).InjectedError
}

func (n noopFailureMode) WaitForFailureToPropagate(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return args.(noopFailureArgs).InjectedError
}

func (n noopFailureMode) WaitForFailureToRecover(ctx context.Context, l *logger.Logger, args FailureArgs) error {
	return args.(noopFailureArgs).InjectedError
}
