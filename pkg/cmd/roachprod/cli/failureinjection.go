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
)

func initFailureInjectionFlags(failureInjectionCmd *cobra.Command) {
	failureInjectionCmd.PersistentFlags().BoolVar(&restoreFailure, "restore", false, "Restore the failure injection.")
	failureInjectionCmd.PersistentFlags().DurationVar(&failureDuration, "duration", 0, "Duration to inject failure for before reverting. 0 to inject indefinitely until cancellation.")

	// Disk Stall Args
	failureInjectionCmd.PersistentFlags().BoolVar(&diskStallArgs.StallWrites, "stall-writes", true, "Stall writes")
	failureInjectionCmd.PersistentFlags().BoolVar(&diskStallArgs.StallReads, "stall-reads", false, "Stall reads")
	failureInjectionCmd.PersistentFlags().BoolVar(&diskStallArgs.StallLogs, "stall-logs", false, "Stall logs")
	failureInjectionCmd.PersistentFlags().IntVar(&diskStallArgs.Throughput, "throughput", 4, "Bytes per second to slow disk I/O to")
}

func (cr *commandRegistry) FailureInjectionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "failure-injection [command] [flags...]",
		Short: "injects failures into a cluster",
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
		Use:   fmt.Sprintf("%s <cluster> <partition-groups>", failures.IPTablesNetworkPartitionName),
		Short: "use iptables to create network partitions",
		Long: `Use iptables to create network partitions. 

Traffic is blocked between groups of nodes as specified by the partition-groups argument. 
The partition-groups argument is a colon-separated list of nodes, e.g. "1,3:2,4" will
partition nodes 1 and 3 from nodes 2 and 4.

If only one partition-group is specified, e.g. "1,3" the nodes in that group will be partitioned
from all other nodes in the cluster. Currently only two partition groups are supported.
		`,
		Args: cobra.RangeArgs(2, 3),
		Run: wrap(func(cmd *cobra.Command, args []string) (retErr error) {
			ctx := context.Background()
			cluster := args[0]
			c, err := roachprod.GetClusterFromCache(config.Logger, cluster, install.SecureOption(isSecure))
			partitioner, err := cr.failureRegistry.GetFailure(cluster, failures.IPTablesNetworkPartitionName, config.Logger, isSecure)
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

// TODO: dmsetup is brittle and resists reuse, consider removing it if
// further attempts to stabilize it fail.
func (cr *commandRegistry) buildDmsetupDiskStall() *cobra.Command {
	return &cobra.Command{
		Use:   fmt.Sprintf("%s <cluster> [--flags]", failures.DmsetupDiskStallName),
		Short: "use dmsetup to create disk stalls",
		Long: `Use dmsetup to create disk stalls. By default, only writes are stalled.

--stall-writes: stall writes, defaults to true

--stall-reads: stall reads

--stall-logs: stall logs

--throughput: currently not supported for dmsetup
		`,
		Args: cobra.ExactArgs(1),
		Run: wrap(func(cmd *cobra.Command, args []string) (retErr error) {
			ctx := context.Background()
			staller, err := cr.failureRegistry.GetFailure(args[0], failures.DmsetupDiskStallName, config.Logger, isSecure)
			if err != nil {
				return err
			}
			return runFailure(ctx, staller, failures.DiskStallArgs{
				StallWrites: diskStallArgs.StallWrites,
				StallReads:  diskStallArgs.StallReads,
				StallLogs:   diskStallArgs.StallLogs,
				Throughput:  diskStallArgs.Throughput,
			})
		}),
	}
}

func (cr *commandRegistry) buildCgroupDiskStall() *cobra.Command {
	return &cobra.Command{
		Use:   fmt.Sprintf("%s <cluster> [--flags]", failures.CgroupDiskStallName),
		Short: "use cgroups v2 to create disk stalls",
		Long: `Use cgroups v2 to create disk stalls. By default, only writes are stalled.

--stall-writes: stall writes, defaults to true

--stall-reads: stall reads

--stall-logs: stall logs

--throughput: bytes per second to stall disk to
`,
		Args: cobra.ExactArgs(1),
		Run: wrap(func(cmd *cobra.Command, args []string) (retErr error) {
			ctx := context.Background()
			staller, err := cr.failureRegistry.GetFailure(args[0], failures.CgroupDiskStallName, config.Logger, isSecure)
			if err != nil {
				return err
			}
			return runFailure(ctx, staller, failures.DiskStallArgs{
				StallWrites: diskStallArgs.StallWrites,
				StallReads:  diskStallArgs.StallReads,
				StallLogs:   diskStallArgs.StallLogs,
				Throughput:  diskStallArgs.Throughput,
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
