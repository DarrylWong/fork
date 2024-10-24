package fiplanner

import "time"

type FailurePlanSpec struct {
	PlanID string
	// Cluster(s) to target. Note the support for test that use multiple
	// clusters i.e. c2c.
	ClusterNames []string
	// If true, continue executing the plan even if some steps fail.
	TolerateErrors bool
	// Seed used to generate new failure injection steps.
	Seed             int64
	DisabledFailures []string
	// How long to inject a failure. Time range of acceptable pause times.
	// The actual pause time is randomly chosen based off the seed when
	// each step is generated.
	minWait time.Duration
	maxWait time.Duration
}
