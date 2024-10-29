package failures

import (
	"fmt"
	"math/rand"
	"os"
)

// FailureSpec describes a failure that can be chosen by the
// failure injection framework.
type FailureSpec struct {
	Name string
	// GenerateArgs generates a map of arguments that can be used to
	// configure the parameters of the failure.
	GenerateArgs func(rng *rand.Rand) map[string]string
}

type FailureRegistry struct {
	m                map[string]*FailureSpec
	failures         []string
	disabledFailures map[string]bool
}

func MakeFailureRegistry(disabledFailures []string) FailureRegistry {
	disabledFailuresMap := make(map[string]bool)

	for _, failure := range disabledFailures {
		disabledFailuresMap[failure] = true
	}

	return FailureRegistry{
		m:                make(map[string]*FailureSpec),
		disabledFailures: disabledFailuresMap,
	}
}

func (r *FailureRegistry) Add(spec FailureSpec) {
	if _, ok := r.m[spec.Name]; ok {
		fmt.Fprintf(os.Stderr, "failure %s already registered\n", spec.Name)
		os.Exit(1)
	}

	if _, ok := r.disabledFailures[spec.Name]; ok {
		return
	}

	r.failures = append(r.failures, spec.Name)
	r.m[spec.Name] = &spec
}

// GetRandomFailure returns a random failure from the registry,
// accounting for disabled failures.
func (r *FailureRegistry) GetRandomFailure(rng *rand.Rand) FailureSpec {
	failure := r.failures[rng.Intn(len(r.failures))]
	return *r.m[failure]
}

// RegisterFailures contains all available failure types.
func RegisterFailures(r *FailureRegistry) {
	registerNodeRestart(r)
	registerLimitBandwidth(r)
	registerPageFault(r)
	registerDiskStall(r)
}

// UnitTestRegisterFailures is a subset of all available failure types.
// This is a unit test helper that can replace ReplaceFailures, so that
// our datadriven unit tests don't change every time we add a new failure type.
func UnitTestRegisterFailures(r *FailureRegistry) {
	registerNodeRestart(r)
	registerLimitBandwidth(r)
	registerPageFault(r)
	registerDiskStall(r)
}
