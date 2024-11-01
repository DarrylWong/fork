package ficontroller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach/pkg/failureinjection"
	"github.com/cockroachdb/cockroach/pkg/fiplanner"
	"github.com/cockroachdb/errors"
)

// 1. Server gets request to start a plan.
// 2. Server spins up a worker to execute the plan.

func (c *Controller) RunFailureInjectionTest(ctx context.Context, plan *FailurePlan) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	plan.CancelFunc = cancel
	for stepID := 1; plan.Status == Running; stepID++ {
		select {
		case <-runCtx.Done():
			return
		default:
		}

		step, err := plan.NextStep(ctx, stepID)
		if err != nil {
			// TODO error handling
			fmt.Printf("error getting next step: %v", err)
			return
		}
		// TODO cleaner way of exiting
		if plan.Status != Running {
			return
		}
		err = plan.ExecuteStep(runCtx, step)
		if err != nil {
			// TODO error handling
			return
		}
	}
}

func (plan *FailurePlan) NextStep(ctx context.Context, stepID int) (fiplanner.FailureStep, error) {
	if plan.IsStatic {
		if stepID <= 0 {
			return fiplanner.FailureStep{}, errors.Newf("step %d is not defined in plan", stepID)
		}
		if stepID > len(plan.StaticPlan.Steps) {
			plan.Status = Completed
			return fiplanner.FailureStep{}, nil
		}
		return plan.StaticPlan.Steps[stepID-1], nil
	}

	// TODO this should be extracted outside/reworked so we don't have to do this
	var clusterNames []string
	var clusterSizes []int
	for clusterName, cluster := range plan.Clusters {
		clusterNames = append(clusterNames, clusterName)
		clusterSizes = append(clusterSizes, int(cluster.ClusterSize))
	}

	return plan.stepGenerator.GenerateStep(stepID, clusterNames, clusterSizes)
}

func (plan *FailurePlan) ExecuteStep(ctx context.Context, step fiplanner.FailureStep) error {
	clusterInfo := plan.Clusters[step.Cluster]
	// TODO actually do this stuff
	// TODO this should be logged in it's own per plan log
	fmt.Printf("connecting to %s\n", clusterInfo.ConnectionString)

	fmt.Printf("executing step %v\n", step)
	failure, err := parseStep(step)
	if err != nil {
		return err
	}
	err = failure.Setup(func() {})
	if err != nil {
		return err
	}

	err = failure.Attack(func() {})
	if err != nil {
		return err
	}
	fmt.Printf("pausing for %s\n", step.Delay)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(step.Delay):
	}
	fmt.Printf("reverting failure\n")
	err = failure.Restore(func() {})
	if err != nil {
		return err
	}
	return nil
}

// TODO: think about better ways to do this, this will get long fast.
// Maybe failureStep should contain a method to convert to failureMode since
// controller should have to care about the specifics of the failure args.
func parseStep(step fiplanner.FailureStep) (failureinjection.FailureMode, error) {
	switch step.FailureType {
	case "Node Restart":
		return failureinjection.NodeRestart{
			GracefulRestart:    mustParseBool(step.Args["graceful_restart"]),
			WaitForReplication: mustParseBool(step.Args["wait_for_replication"]),
		}, nil
	case "Disk Stall":
		types := step.Args["type"]
		return failureinjection.DiskStall{
			ReadStall:  strings.Contains(types, "read"),
			WriteStall: strings.Contains(types, "write"),
		}, nil
	case "Page Fault":
		types := step.Args["type"]
		return failureinjection.PageFault{
			MajorFault: strings.Contains(types, "major"),
			MinorFault: strings.Contains(types, "minor"),
		}, nil
	case "Limit Bandwidth":
		return failureinjection.LimitBandwidth{
			Rate: step.Args["rate"],
		}, nil
	}
	return nil, errors.Newf("unknown failure type %s", step.FailureType)
}

func mustParseBool(arg string) bool {
	b, err := strconv.ParseBool(arg)
	if err != nil {
		return false
	}
	return b
}
