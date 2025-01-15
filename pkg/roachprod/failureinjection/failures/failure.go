package failures

import (
	"context"
	"strings"
)

type FailureMode interface {
	Description() string

	// Setup any dependencies required for the failure to be injected.
	Setup(ctx context.Context, args ...string) error

	// Inject a failure into the system.
	Inject(ctx context.Context, args ...string) error

	// Restore reverses the effects of Inject.
	Restore(ctx context.Context, args ...string) error

	// Cleanup uninstalls any dependencies that were installed by Setup.
	Cleanup(ctx context.Context) error
}

func parseArgs(args ...string) map[string]string {
	m := make(map[string]string)
	for _, arg := range args {
		key, value, _ := strings.Cut(arg, "=")
		m[key] = value
	}
	return m
}
