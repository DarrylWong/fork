package cli

import (
	"context"
	"fmt"
	"github.com/cockroachdb/cockroach/pkg/roachprod"
	"github.com/cockroachdb/cockroach/pkg/roachprod/config"
	"github.com/cockroachdb/cockroach/pkg/roachprod/failureinjection/failures"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/spf13/cobra"
	"os"
	"os/signal"
	"strings"
	"time"
)

var (
	failureDuration time.Duration
	restoreFailure  bool
	diskStallArgs   failures.DiskStallArgs
	nodesToRestore  []int
)

func initFailureInjectionFlags(failureInjectionCmd *cobra.Command) {
	failureInjectionCmd.PersistentFlags().BoolVar(&restoreFailure, "restore", false, "Restore the failure injection.")
	failureInjectionCmd.PersistentFlags().DurationVar(&failureDuration, "duration", 0, "Duration to inject failure for before reverting. 0 to inject indefinitely until cancellation.")

	// Network Partition Args
	failureInjectionCmd.PersistentFlags().IntSliceVar(&nodesToRestore, "nodes-to-restore", nil, "List of nodes to revert network partitions for.")

	// Disk Stall Args
	failureInjectionCmd.PersistentFlags().BoolVar(&diskStallArgs.ReadsToo, "reads-too", false, "Stall reads.")
	failureInjectionCmd.PersistentFlags().BoolVar(&diskStallArgs.LogsToo, "logs-too", false, "Stall logs.")
	failureInjectionCmd.PersistentFlags().IntVar(&diskStallArgs.Throughput, "throughput", 4, "Bytes per second to slow disk I/O to.")
}

func (cr *commandRegistry) FailureInjectionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "failure-injection [command] [flags...]",
		Short: "TODO",
		Long: `TODO
		failure-injection list to see available failure injection modes.
`,
	}
	fr := failures.NewFailureRegistry()
	fr.Register()
	cr.failureRegistry = fr
	initFailureInjectionFlags(cmd)
	return cmd
}

func (cr *commandRegistry) buildFIListCmd() *cobra.Command {
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
			matches := cr.failureRegistry.List(regex)
			for _, match := range matches {
				fmt.Printf("%s\n", match)
			}
			return nil
		}),
	}
}

func (cr *commandRegistry) buildIptablesPartitionNode() *cobra.Command {
	return &cobra.Command{
		Use:   fmt.Sprintf("%s <cluster> <partition-groups>", failures.IPTablesPartitionNodeName),
		Short: "TODO",
		Long: `TODO
		`,
		Args: cobra.RangeArgs(2, 3),
		Run: wrap(func(cmd *cobra.Command, args []string) (retErr error) {
			ctx := context.Background()
			cluster := args[0]
			c, err := roachprod.GetClusterFromCache(config.Logger, cluster, install.SecureOption(isSecure))
			partitioner, err := cr.failureRegistry.GetFailure(cluster, failures.IPTablesPartitionNodeName, config.Logger, isSecure)
			if err != nil {
				return err
			}
			failureArgs := failures.NetworkPartitionArgs{}

			groups := strings.Split(args[1], ":")
			for _, group := range groups {
				nodes, err := install.ListNodes(group, len(c.Nodes))
				if err != nil {
					return err
				}
				failureArgs.PartitionGroups = append(failureArgs.PartitionGroups, nodes)
			}

			if len(args) == 3 {
				failureArgs.NodesToRestore, err = install.ListNodes(args[2], len(c.Nodes))
				if err != nil {
					return err
				}
			} else {
				failureArgs.NodesToRestore = failureArgs.PartitionGroups[0]
			}

			return runFailure(ctx, partitioner, failureArgs)
		}),
	}
}

func (cr *commandRegistry) buildDmsetupDiskStall() *cobra.Command {
	return &cobra.Command{
		Use:   fmt.Sprintf("%s <cluster> [--flags]", failures.DmsetupDiskStallName),
		Short: "TODO",
		Long: `TODO
		`,
		Args: cobra.MinimumNArgs(1),
		Run: wrap(func(cmd *cobra.Command, args []string) (retErr error) {
			ctx := context.Background()
			staller, err := cr.failureRegistry.GetFailure(args[0], failures.DmsetupDiskStallName, config.Logger, isSecure)
			if err != nil {
				return err
			}
			return runFailure(ctx, staller, failures.DiskStallArgs{
				ReadsToo:   diskStallArgs.ReadsToo,
				LogsToo:    diskStallArgs.LogsToo,
				Throughput: diskStallArgs.Throughput,
			})
		}),
	}
}

func (cr *commandRegistry) buildCgroupDiskStall() *cobra.Command {
	return &cobra.Command{
		Use:   fmt.Sprintf("%s <cluster> [--flags]", failures.CgroupDiskStallName),
		Short: "TODO",
		Long: `TODO
		`,
		Args: cobra.MinimumNArgs(1),
		Run: wrap(func(cmd *cobra.Command, args []string) (retErr error) {
			ctx := context.Background()
			staller, err := cr.failureRegistry.GetFailure(args[0], failures.CgroupDiskStallName, config.Logger, isSecure)
			if err != nil {
				return err
			}
			return runFailure(ctx, staller, failures.DiskStallArgs{
				ReadsToo:   diskStallArgs.ReadsToo,
				LogsToo:    diskStallArgs.LogsToo,
				Throughput: diskStallArgs.Throughput,
			})
		}),
	}
}

func runFailure(ctx context.Context, failure failures.FailureMode, args failures.FailureArgs) error {
	err := failure.Setup(ctx, config.Logger, args)
	if err != nil {
		return err
	}
	err = failure.Inject(ctx, config.Logger, args)
	if err != nil {
		return err
	}

	ctrlCHandler(ctx, failure, args)

	// If no duration was specified, wait indefinitely until the caller
	// cancels the context.
	if failureDuration == 0 {
		config.Logger.Printf("waiting indefinitely before reverting failure on cancellation\n")
		<-ctx.Done()
		return nil
	}

	config.Logger.Printf("waiting for %s before reverting failure\n", failureDuration)
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(failureDuration):
		config.Logger.Printf("time limit hit\n")
		revertFailure(ctx, failure, args)
		return nil
	}
}

func revertFailure(ctx context.Context, failure failures.FailureMode, args failures.FailureArgs) {
	// Best effort cleanup
	err := failure.Restore(ctx, config.Logger, args)
	if err != nil {
		config.Logger.Printf("failed to restore failure: %v", err)
	}
	err = failure.Cleanup(ctx, config.Logger)
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
		config.Logger.Printf("SIGINT received. Reverting failure injection and waiting up to a minute.")
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
			config.Logger.Printf("Second SIGINT received. Quitting. Failure might be still injected.")
		case <-cleanupCh:
		}
		os.Exit(2)
	}()
}
