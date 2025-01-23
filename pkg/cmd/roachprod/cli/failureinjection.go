package cli

import (
	"context"
	"fmt"
	"github.com/cockroachdb/cockroach/pkg/roachprod/config"
	"github.com/cockroachdb/cockroach/pkg/roachprod/failureinjection/failures"
	"github.com/spf13/cobra"
	"os"
	"os/signal"
	"time"
)

var (
	failureDuration time.Duration
	restoreFailure  bool
)

func initFailureInjectionFlags(failureInjectionCmd *cobra.Command) {
	failureInjectionCmd.PersistentFlags().BoolVar(&restoreFailure, "restore", false, "Restore the failure injection.")
	failureInjectionCmd.PersistentFlags().DurationVar(&failureDuration, "duration", 0, "Duration to inject failure for before reverting. 0 to inject indefinitely until cancellation.")

}

func (cr *commandRegistry) FailureInjectionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "failure-injection [command] [flags...]",
		Short: "TODO",
		Long: `TODO
		failure-injection list to see available failure injection modes.
`,
	}
}

func (cr *commandRegistry) buildFIListCmd() *cobra.Command {
	fr := failures.NewFailureRegistry()
	fr.Register()
	return &cobra.Command{
		Use:   "list [regex]",
		Short: "Lists all available failure injection modes matching the regex.",
		Args:  cobra.MaximumNArgs(1),
		// Wraps the command execution with additional error handling
		Run: wrap(func(cmd *cobra.Command, args []string) (retErr error) {
			regex := ""
			if len(args) > 0 {
				regex = args[0]
			}
			matches := fr.List(regex)
			for _, match := range matches {
				fmt.Printf("%s\n", match)
			}
			return nil
		}),
	}
}

func (cr *commandRegistry) buildFIIptablesPartitionNode() *cobra.Command {
	fr := failures.NewFailureRegistry()
	fr.Register()
	return &cobra.Command{
		Use:   "iptables-partition-node <cluster>",
		Short: "TODO",
		Long: `TODO
		`,
		Args: cobra.MinimumNArgs(1),
		Run: wrap(func(cmd *cobra.Command, args []string) (retErr error) {
			ctx := context.Background()
			// TODO: Extract to helper
			partitioner, err := fr.GetFailure(args[0], "iptables-partition-node", config.Logger, isSecure)
			if err != nil {
				return err
			}
			err = partitioner.Setup(ctx, failures.PartitionNodeArgs{})
			if err != nil {
				return err
			}
			err = partitioner.Inject(ctx, failures.PartitionNodeArgs{})
			if err != nil {
				return err
			}

			ctrlCHandler(ctx, partitioner, failures.PartitionNodeArgs{})

			// If no duration was specified, wait indefinitely until the caller
			// cancels the context.
			if failureDuration == 0 {
				<-ctx.Done()
				return nil
			}

			// Wait for the caller to cancel the context or for the specified
			// time to pass.
			for {
				select {
				case <-ctx.Done():
					fmt.Printf("Context cancelled\n")
					return nil
				case <-time.After(failureDuration):
					fmt.Printf("time limit hit\n")
					return nil
				}
			}
		}),
	}
}

func revertFailure(ctx context.Context, failure failures.FailureMode, args failures.FailureArgs) {
	// If --inject-only is set, we're done
	// Best effort cleanup
	// TODO: make sure context has chance to cleanup
	err := failure.Restore(ctx, args)
	if err != nil {
		config.Logger.Printf("failed to restore failure: %v", err)
	}
	err = failure.Cleanup(ctx)
	if err != nil {
		config.Logger.Printf("failed to cleanup failure: %v", err)
	}
}

func ctrlCHandler(ctx context.Context, failure failures.FailureMode, args failures.FailureArgs) {
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)
	go func() {
		select {
		case <-signalCh:
		case <-ctx.Done():
			return
		}
		fmt.Printf("Signal received. Reverting failure injection and waiting up to a minute.")
		// Make sure there are no leftover clusters.
		cleanupCh := make(chan struct{})
		go func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
			revertFailure(cleanupCtx, failure, args)
			cancel()
			close(cleanupCh)
		}()
		// If we get a second CTRL-C, exit immediately.
		select {
		case <-signalCh:
			fmt.Printf("Second SIGINT received. Quitting. Failure might be still injected.")
		case <-cleanupCh:
		}
		os.Exit(2)
	}()
}
