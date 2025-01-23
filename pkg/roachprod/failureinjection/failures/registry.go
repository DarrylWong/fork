package failures

import (
	"fmt"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"regexp"
)

type failureSpec struct {
	makeFailureFunc func(clusterName string, l *logger.Logger, secure bool) (FailureMode, error)
	args            FailureArgs
}
type FailureRegistry struct {
	failures map[string]failureSpec
}

func NewFailureRegistry() *FailureRegistry {
	return &FailureRegistry{
		failures: make(map[string]failureSpec),
	}
}

func (r *FailureRegistry) Register() {
	registerCgroupDiskStall(r)
	registerDmsetupDiskStall(r)
	registerIPTablesPartitionNode(r)
}

func (r *FailureRegistry) add(failureName string, args FailureArgs, makeFailureFunc func(clusterName string, l *logger.Logger, secure bool) (FailureMode, error)) {
	if _, ok := r.failures[failureName]; ok {
		panic(fmt.Sprintf("failure %s already exists", failureName))
	}

	// TODO: this needs to register the failure/flags with the CLI
	// Idea: pass a map of arg -> *flag.FlagSet, and then use that to register the flags
	// need to figure out the init order, this might not work
	r.failures[failureName] = failureSpec{
		makeFailureFunc: makeFailureFunc,
		args:            args,
	}
}

func (r *FailureRegistry) List(regex string) []string {
	var filter *regexp.Regexp
	if regex == "" {
		filter = regexp.MustCompile(`.`)
	} else {
		filter = regexp.MustCompile(regex)
	}

	var matches []string
	for name, _ := range r.failures {
		if filter.MatchString(name) {
			matches = append(matches, name)
		}
	}
	return matches
}

func (r *FailureRegistry) GetFailure(clusterName, failureName string, l *logger.Logger, secure bool) (FailureMode, error) {
	spec, ok := r.failures[failureName]
	if !ok {
		return nil, fmt.Errorf("unknown failure %s", failureName)
	}
	return spec.makeFailureFunc(clusterName, l, secure)
}
