package modular

// Test is the main struct test writers will interact with. It is used to
// construct a DAG of the test, which is then converted to a test plan
// and executed.
type Test struct {
	// setupStage is a special Stage in the plan used for initializing the test.
	// Steps are run sequentially as declared i.e. no randomization, and no failure
	// injection is attempted.
	setupStage *Stage
	// stages of the test, represented as a DAG.
	stages []*Stage
	// afterTestStage is like setupStage but run after the test is completed.
	afterTestStage *Stage
	options        TestOptions
}

type TestOptions struct{}
type TestOption func(options *TestOptions)
