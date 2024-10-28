package failures

import (
	"fmt"
	"math/rand"
	"os"
)

type FailureSpec struct {
	Name         string
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
		fmt.Fprintf(os.Stderr, "failure %s is disabled by failure plan spec\n", spec.Name)
		return
	}

	r.failures = append(r.failures, spec.Name)
	r.m[spec.Name] = &spec
}

func (r *FailureRegistry) GetRandomFailure(rng *rand.Rand) FailureSpec {
	failure := r.failures[rng.Intn(len(r.failures))]
	return *r.m[failure]
}

func RegisterFailures(r *FailureRegistry) {
	registerNodeRestart(r)
	registerLimitBandwidth(r)
	registerPageFault(r)
	registerDiskStall(r)
}

func UnitTestRegisterFailures(r *FailureRegistry) {
	registerNodeRestart(r)
	registerLimitBandwidth(r)
	registerPageFault(r)
	registerDiskStall(r)
}
