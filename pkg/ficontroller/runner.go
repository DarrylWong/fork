package ficontroller

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
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

	dir := c.GetLogDir(plan)
	stepsFile := sanitizeFileName(fmt.Sprintf("%s-steps.log", plan.getPlanID()))
	stepLogger, err := newLogger(filepath.Join(dir, stepsFile))
	if err != nil {
		// TODO error handling
		fmt.Printf("error creating logger: %v\n", err)
		return
	}

	logFile := sanitizeFileName(fmt.Sprintf("%s.log", plan.getPlanID()))
	runLogger, err := newLogger(filepath.Join(dir, logFile))
	if err != nil {
		// TODO error handling
		fmt.Printf("error creating logger: %v\n", err)
		return
	}

	for stepID := 1; plan.Status == Running; stepID++ {
		select {
		case <-runCtx.Done():
			return
		default:
		}

		step, err := plan.NextStep(ctx, stepLogger, stepID)
		if err != nil {
			// TODO error handling
			runLogger.Printf("error getting next step: %v", err)
			plan.Status = Failed
			return
		}
		// TODO cleaner way of exiting
		if plan.Status != Running {
			return
		}
		err = plan.ExecuteStep(runCtx, runLogger, step)
		if err != nil {
			runLogger.Printf("error executing step: %v", err)
			// TODO error handling
			plan.Status = Failed
			return
		}
	}
}

func (plan *FailurePlan) NextStep(
	ctx context.Context, l *logger, stepID int,
) (fiplanner.FailureStep, error) {
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

	newStep, err := plan.stepGenerator.GenerateStep(stepID, clusterNames, clusterSizes)
	if err != nil {
		return fiplanner.FailureStep{}, err
	}
	l.Printf("%s", newStep)
	return newStep, nil
}

func (plan *FailurePlan) ExecuteStep(
	ctx context.Context, l *logger, step fiplanner.FailureStep,
) error {
	l.Printf("executing step: %d", step.StepID)
	//clusterInfo := plan.Clusters[step.Cluster]
	// TODO: the controller should have the info needed to establish SSH connections
	// itself, so that way we don't couple it too tightly with roachprod. For now we're
	// just using roachprod as a POC because it's easy.
	runFunc := func(args ...string) error {
		args = []string{"run", fmt.Sprintf("%s:%d", step.Cluster, step.Node), strings.Join(args, " ")}
		//args = append(args, clusterInfo.ConnectionString[step.Node-1])
		cmd := exec.Command("./roachprod", args...)
		l.Printf("running command: %s", cmd.String())
		return cmd.Run()
	}

	failure, err := parseStep(l, step)
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
func parseStep(l *logger, step fiplanner.FailureStep) (failureinjection.FailureMode, error) {
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
