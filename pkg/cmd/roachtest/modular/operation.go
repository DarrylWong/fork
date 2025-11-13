package modular

import (
	"time"
)

// Operation defines a special type of Chain in the DAG. These are Chains that
// have been registered for reuse across multiple tests, and have additional
// constraints on when/where/how they can be run. Beyond test reuse, Operations
// may also be used in randomized environments, i.e. long running clusters, where
// we want to construct complex randomized tests.
// TODO: implement some common operations as well as a registry to enable reuse.
type Operation interface {
	Chain() Chain
	Name() string
	Precondition() bool
	Timeout() time.Duration
}
