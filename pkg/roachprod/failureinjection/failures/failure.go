package failures

import "context"

type FailureMode interface {
	// Setup any dependencies required for the failure to be injected.
	Setup(ctx context.Context, Run func(Args ...[]interface{})) error

	// Inject a failure into the system.
	Inject(ctx context.Context, Run func(Args ...[]interface{})) error

	// Restore reverses the effects of Inject.
	Restore(ctx context.Context, Run func(Args ...[]interface{})) error

	// Cleanup uninstalls any dependencies that were installed by Setup.
	Cleanup(ctx context.Context, Run func(Args ...[]interface{})) error
}
