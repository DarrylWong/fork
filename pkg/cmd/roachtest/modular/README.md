1. Test writers declare their tests in a modular way, first they declare the type of cluster(s) they need.

"I want a cluster that has at least 3 nodes and 8 CPUs, it can only run in system and shared process mode"

// Creates a roachprod cluster with the following randomized options,
// stages the cockroach binary, along with any other setup
c := t.AddCluster(
  option.MinNodes(3),
  option.CPU(8)
  option.DisabledDeploymentModes(SeparateProcess)
)

"I want a workload cluster with one node"

// Creates a roachprod cluster with one node, stages the workload binary.
workload := t.AddWorkloadCluster(
  option.NodeCount(1)
)

2. Test writers use these clusters to declare test steps. Test steps are declarative statements that are defined in stages:

"I want to setup a local prom instance on the cluster"

// This schedules a testStep to run before the "test begins", i.e. before CRDB is started.
// Multiple setup steps can be declared and they will run in the order they were added.
t.Setup(func(ctx context.Context, t test.Test, h helper) {
  // mock implementation
  operation.InstallPrometheus(ctx, c)
})

"After the cluster starts, I want to import tpcc fixtures"

workloadImportStage := t.NewStage("workload import")

// Multiple InStage steps can be declared. They are run concurrently if run in the same stage, 
// but sequentially based on the stage number.
t.InStage(workloadImportStage, func(ctx context.Context, t test.Test, h helper) {)
  // mock implementation
  operation.ImportTPCC(ctx, c, workload)
})

"At the same time as tpcc import, I concurrently want to run a bank import to load cold data"
t.InStage(workloadImportStage, func(ctx context.Context, t test.Test, h helper) {
  // mock implementation
  operation.ImportBank(ctx, c, workload)
})

"After both imports are done, I want to run tpcc workload"
workloadRunStage := t.NewStage("tpcc workload run")
t.InStage(workloadRun, func(ctx context.Context, t test.Test, h helper) {
  // mock implementation
  operation.RunTPCC(ctx, c, workload, duration=1h)
}, modular.InBackground())

"Then, I want to split and scatter ranges, I want this to be repeated multiple times."
// This stage will be repeated between 5 and 10 times, with a random interval between each repetition.
scatterStage := t.NewStage("split and scatter", modular.Repeat(5, 10), modular.DelayInterval(30*time.Second, 2*time.Minute))
t.InStage(scatterStage, func(ctx context.Context, t test.Test, h helper) {
// mock implementation
operation.ScatterRanges(ctx, c)
})


"After the test is done, I want to run tpcc consistency checks"
// This runs after all other stages are complete, it is different than
// just adding an extra stage as any background steps will be cancelled before this runs.
t.AfterTest(func(ctx context.Context, t test.Test, h helper) {
  // mock implementation
  operation.RunTPCCConsistencyChecks(ctx, c, workload)
})

3. This will be parsed into a TestPlan that encodes all the steps and any state required to run them.
4. The testplan is executed by a test runner.
