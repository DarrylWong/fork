package ficontroller

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach/pkg/failureinjection"
	"github.com/cockroachdb/cockroach/pkg/fiplanner"
	"github.com/cockroachdb/cockroach/pkg/roachprod"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/errors"
)

// 1. Server gets request to start a plan.
// 2. Server spins up a worker to execute the plan.

func (c *Controller) RunFailureInjectionTest(
	ctx context.Context, l *logger.Logger, plan *FailurePlan,
) {
	runCtx, cancel := context.WithCancel(context.Background())

	plan.mu.Lock()
	plan.mu.CancelFunc = cancel
	plan.mu.Unlock()
	defer func() {
		plan.mu.CancelFunc()
	}()

	dir := c.GetLogDir(plan)
	stepsFile := sanitizeFileName(fmt.Sprintf("%s-steps.log", plan.getPlanID()))
	stepLogger, err := l.ChildLogger(filepath.Join(dir, stepsFile))
	if err != nil {
		// TODO error handling
		fmt.Printf("error creating logger: %v\n", err)
		return
	}

	logFile := sanitizeFileName(fmt.Sprintf("%s.log", plan.getPlanID()))
	runLogger, err := l.ChildLogger(filepath.Join(dir, logFile))
	if err != nil {
		// TODO error handling
		fmt.Printf("error creating logger: %v\n", err)
		return
	}

	for stepID := 1; plan.getStatus() == Running; stepID++ {
		select {
		case <-runCtx.Done():
			return
		default:
		}

		step, err := plan.NextStep(ctx, stepLogger, stepID)
		if err != nil {
			// TODO error handling
			runLogger.Printf("error getting next step: %v", err)
			plan.setStatus(Failed)
			return
		}
		// TODO cleaner way of exiting
		if plan.getStatus() != Running {
			return
		}
		err = plan.ExecuteStep(runCtx, runLogger, step)
		if err != nil {
			runLogger.Printf("error executing step: %v", err)
			// TODO error handling
			plan.setStatus(Failed)
			return
		}
	}
}

func (plan *FailurePlan) NextStep(
	ctx context.Context, l *logger.Logger, stepID int,
) (fiplanner.FailureStep, error) {
	if plan.IsStatic {
		if stepID <= 0 {
			return fiplanner.FailureStep{}, errors.Newf("step %d is not defined in plan", stepID)
		}
		if stepID > len(plan.StaticPlan.Steps) {
			plan.setStatus(Completed)
			return fiplanner.FailureStep{}, nil
		}
		return plan.StaticPlan.Steps[stepID-1], nil
	}

	// TODO this should be extracted outside/reworked so we don't have to do this
	var clusterNames []string
	var clusterSizes []int
	for clusterName, cluster := range plan.Clusters {
		clusterNames = append(clusterNames, clusterName)
		clusterSizes = append(clusterSizes, cluster.ClusterSize)
	}

	newStep, err := plan.stepGenerator.GenerateStep(stepID, clusterNames, clusterSizes)
	if err != nil {
		return fiplanner.FailureStep{}, err
	}
	l.Printf("%s", newStep)
	return newStep, nil
}

var runFunc func(args ...string) error

func (plan *FailurePlan) ExecuteStep(
	ctx context.Context, l *logger.Logger, step fiplanner.FailureStep,
) error {
	l.Printf("executing step: %d", step.StepID)
	clusterInfo := plan.Clusters[step.Cluster]
	// TODO: the controller should have the info needed to establish SSH connections
	// itself, so that way we don't couple it too tightly with roachprod. For now we're
	// just using roachprod as a POC because it's easy.
	if runFunc == nil {
		runFunc = func(args ...string) error {
			return roachprod.Run(ctx, l, fmt.Sprintf("%s:%d", step.Cluster, step.Node), "", "", true, l.Stdout, l.Stderr, args, install.DefaultRunOptions())
		}
	}

	failure, err := parseStep(l, clusterInfo, step)
	if err != nil {
		return err
	}

	err = failure.Setup(runFunc)
	if err != nil {
		return err
	}

	err = failure.Attack(runFunc)
	if err != nil {
		return err
	}
	l.Printf("pausing for %s", step.Delay)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(step.Delay):
	}
	l.Printf("reverting failure")
	err = failure.Restore(runFunc)
	if err != nil {
		return err
	}
	return nil
}

// TODO: think about better ways to do this, this will get long fast.
// Maybe failureStep should contain a method to convert to failureMode since
// controller should have to care about the specifics of the failure args.
func parseStep(
	l *logger.Logger, clusterInfo *ClusterInfo, step fiplanner.FailureStep,
) (failureinjection.FailureMode, error) {
	logFunc := l.Printf

	switch step.FailureType {
	case "Node Restart":
		return failureinjection.NodeRestart{
			LogFunc:            logFunc,
			GracefulRestart:    mustParseBool(step.Args["graceful_restart"]),
			WaitForReplication: mustParseBool(step.Args["wait_for_replication"]),
		}, nil
	case "Disk Stall":
		types := step.Args["type"]
		return failureinjection.DiskStall{
			LogFunc:    logFunc,
			ReadStall:  strings.Contains(types, "read"),
			WriteStall: strings.Contains(types, "write"),
		}, nil
	case "Page Fault":
		types := step.Args["type"]
		return failureinjection.PageFault{
			LogFunc:    logFunc,
			MajorFault: strings.Contains(types, "major"),
			MinorFault: strings.Contains(types, "minor"),
		}, nil
	case "Limit Bandwidth":
		return failureinjection.LimitBandwidth{
			LogFunc: logFunc,
			Rate:    step.Args["rate"],
		}, nil
	case "Partition Node":
		return failureinjection.PartitionNode{
			LogFunc: logFunc,
			Port:    clusterInfo.SQLPorts[step.Node-1],
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

func (c *Controller) GetLogDir(p *FailurePlan) string {
	dir := p.DynamicPlan.LogDir
	if p.IsStatic {
		dir = p.StaticPlan.LogDir
	}
	if dir == "" {
		dir = c.DefaultPlanDir
	}
	return dir
}

var fileNameRegex = regexp.MustCompile("[^a-zA-Z0-9.]+")

func sanitizeFileName(name string) string {
	name = fileNameRegex.ReplaceAllString(name, "-")
	return strings.ToLower(name)
}
