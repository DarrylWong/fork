package modular

// The possible permutations of a modular test can be represented
// as a directed acyclic graph (DAG). A DAG consists of stages, which
// contain one or more chains of steps. A chain represents a dependency
// between steps, such that all steps in a chain must be run in order.
// However, chains contain no dependencies with other chains, thus any
// interleaving of steps among chains is allowed.

// Stage represents one or more chains that all converge at the start and
// end of the stage.
type Stage struct {
	name   string
	index  int
	chains []chain

	// Stage options
	failureInjectionDisabled bool
	// The maximum number of steps that can be run concurrently in this stage.
	maxStepConcurrency int
}

// chain represents a sequence of step groups that must be executed in order.
type chain []stepGroup

// stepGroup represents one or more testSteps in a chain that share the same
// dependencies but can be run interchangeably or concurrently.
type stepGroup []testStep

// testStep represents the smallest unit of work in a modular plan.
type testStep struct {
	StepProtocol
	// Lazily assigned once the graph is finalized.
	order stepOrder
}

// stepOrder encodes the position of the step in the graph, such that we can
// easily determine if two steps are dependent on each other.
type stepOrder struct {
	chainID int
	depth   int
}

func (s *Stage) LongestChain() int {
	var longestChainLength int
	for _, c := range s.chains {
		if len(c) > longestChainLength {
			longestChainLength = len(c)
		}
	}
	return longestChainLength
}

// MaxConcurrentSteps returns the maximum number of steps that can be run concurrently in this stage.
func (s *Stage) MaxConcurrentSteps() int {
	maxSteps := 0
	for _, ch := range s.chains {
		maxGroupSize := 0
		for _, gr := range ch {
			if len(gr) > maxGroupSize {
				maxGroupSize = len(gr)
			}
		}
		maxSteps = maxSteps + maxGroupSize
	}

	return maxSteps
}

// Steps returns a flattened view of the stage, returning all
// steps in order for each chain.
func (s *Stage) Steps() []testStep {
	var steps []testStep
	for _, ch := range s.chains {
		for _, group := range ch {
			steps = append(steps, group...)
		}
	}
	return steps
}

// MaxConcurrentSteps returns the maximum number of steps that can be run concurrently in this chain.
func (ch *chain) MaxConcurrentSteps() int {
	maxSteps := 0
	for _, gr := range *ch {
		if len(gr) > maxSteps {
			maxSteps = len(gr)
		}
	}
	return maxSteps
}
