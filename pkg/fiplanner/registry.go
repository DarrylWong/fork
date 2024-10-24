package fiplanner

import (
	"fmt"
	"math/rand"
	"os"
)

type failureSpec struct {
	Name         string
	GenerateArgs func(rng *rand.Rand) map[string]string
}

type failureRegistry struct {
	m                map[string]*failureSpec
	failures         []string
	disabledFailures map[string]bool
}

func makeFailureRegistry(spec FailurePlanSpec) failureRegistry {
	disabledFailures := make(map[string]bool)

	for _, failure := range spec.DisabledFailures {
		disabledFailures[failure] = true
	}

	return failureRegistry{
		m:                make(map[string]*failureSpec),
		disabledFailures: disabledFailures,
	}
}

func (r *failureRegistry) Add(spec failureSpec) {
	if _, ok := r.m[spec.Name]; ok {
		fmt.Fprintf(os.Stderr, "failure %s already registered\n", spec.Name)
		os.Exit(1)
	}

	if _, ok := r.disabledFailures[spec.Name]; ok {
		fmt.Fprintf(os.Stderr, "failure %s is disabled by failure plan spec\n", spec.Name)
	}

	r.failures = append(r.failures, spec.Name)
	r.m[spec.Name] = &spec
}

func (r *failureRegistry) GetRandomFailure(rng *rand.Rand) failureSpec {
	failure := r.failures[rng.Intn(len(r.failures))]
	return *r.m[failure]
}

func RegisterFailures(r *failureRegistry) {
	registerNodeRestart(r)
}

func registerNodeRestart(r *failureRegistry) {
	gen := func(rng *rand.Rand) map[string]string {
		args := make(map[string]string)
		if rng.Float64() > 0.5 {
			args["graceful-restart"] = "true"
		}

		if rng.Float64() > 0.5 {
			args["wait-for-replication"] = "true"
		}

		return args
	}
	r.Add(failureSpec{
		Name:         "Node Restart",
		GenerateArgs: gen,
	})
}
