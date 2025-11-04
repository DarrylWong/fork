# Modular Test Framework

The modular test framework enables building complex, exploratory tests by declaring *what* needs to happen and their dependencies, not *how* they execute. The framework randomly generates different valid execution orderings on each test run, helping discover bugs that only appear under specific timing or concurrency conditions.

## Motivation

### Exploratory

Roachtests work in an imperative fashion - tests are comprised of rigidly ordered steps that always execute in the same sequence. It is not uncommon to see bugs manifest only under certain interleavings of steps, ones we may not be testing at all. Once we start considering concurrent steps, the state space of possible executions we aren't covering explodes.

While some tests attempt to provide this via randomized delays or goroutine synchronization, this places the burden of implementation on the test author and are often omitted.

Our new framework should provide an expressive yet simple way to explore this massive state space.

### Modularity

There's a lack of reusable code across roachtests, which leads test authors to write narrow, low coverage tests. 

Consider upreplicating a cluster or a network partition between nodes. These are operations that we expect to see in regular production, so it makes sense to test even if it is not the main feature we are stressing. However, doing so currently requires implementing said operations for each test, which incentivizes testing features in isolation.

While there have been attempts to create test driver frameworks on top of roachtest with shared helpers (e.g., mixed-version framework), there's still significant code duplication across these drivers. What may work for one test driver may not be reusable for another. The framework is fundamentally limited in this sense by its lack of cluster state awareness. Consider our upreplication example, even attempting to set the zone configs for a cluster requires us knowing what tables/ranges were created by the test so we can apply the configs everywhere.

Our new framework should allow declaring composable operations once, which can be reused for all tests effortlessly.

### Unification

We currently maintain both the roachtest framework and the DRT operation framework which are largely disjoint in features. DRT operations cannot be used in roachtests and roachtests cannot be run on DRT clusters. Improvements to one framework must be ported to the other, which is often neglected. Within roachtest we also maintain the mixed version framework which again suffers from the same issues.

While the original motivations for these separate frameworks still exist, we can greatly reduce the maintenance burden by unifying them into a single underlying framework.

## Glossary

### Step

A unit of work that runs from start to end without being paused (assuming no failures). Steps are atomic units that won't be "preempted" during execution.

Example: Scraping metrics and asserting on latency values - the entire function runs without intentional pauses.

```go
func assertSQLLatency(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
    latencies := []float64{}
    timeout := time.After(5 * time.Minute)
    ticker := time.NewTicker(10 * time.Second)

    for {
        select {
        case <-timeout:
            return assertLatency(latencies)
        case <-ticker.C:
            latencies = append(latencies, sampleLatency(h))
        }
    }
}
```

### Operation

A group of related steps that have ordering dependencies between them. Operations define sequences of steps that must execute in a specific order, but can interleave with other operations. In simpler terms, an operation is "something" we want to test.

Example: Running a TPCC workload is an operation that can be broken down to steps: initialization, execution, and consistency checking - these must happen in order, but other operations can run in between.

```go
tpccOp := modular.NewOperation("init TPCC", initTPCCStep).
    Then("run TPCC", runTPCCStep).
    Then("check TPCC consistency", checkConsistencyStep)
```

### Stage

A group of one or more operations that share a common stage start and end dependency. Stages provide a convenience grouping for writing complex tests where certain operations must fully complete before others begin.

Example: A mixed-version upgrade can be broken into distinct stages (initial upgrade, rollback, finalization) where each stage must fully complete before the next starts.

```go
initialUpgradeStage := mt.NewStage("initial upgrade")
mt.InStage(initialUpgradeStage, "rolling restart", rollingRestartStep).
    And("user hooks", userHooksStep)

rollbackStage := mt.NewStage("rollback")
mt.InStage(rollbackStage, "rollback nodes", rollbackStep).
    And("user hooks", userHooksStep)
```

### DAG (Directed Acyclic Graph)

A sequential list of one or more stages that encodes all possible permutations of step orderings that a modular test can take. The DAG represents the full space of valid execution paths.

Example: consider the following DAG. We can run the TPCC workload before, after, or concurrently with increasing the replication factor as they share no dependencies beside stage start. However, we must wait for TPCC workload to finish before dropping the table due to the dependency.

```md
                                                 [setup]
                                          ┌───────────────────┐
                                          │  importing tpcc   │
                                          │     workload      │
                                          │                   │
                                          └───────────────────┘
                                                    │
                                                    │
                                                    │
                                                    │
                                                 [test]
                                                    │
                          ┼─────────────────────────┼─────────────────────────┼
                          ▼                         ▼                         ▼
                ┌───────────────────┐     ┌───────────────────┐     ┌───────────────────┐
                │   running TPCC    │     │    increasing     │     │  copy bank table  │
                │workload for 1 hour│     │replication factor │     │                   │
                │                   │     │       to 5        │     │                   │
                └───────────────────┘     └───────────────────┘     └───────────────────┘
                          │                         │                         │
                          │                         │                         │
                          │                         │                         │
                          │                         │                         │
                          ▼                         ▼                         │
                ┌───────────────────┐     ┌───────────────────┐               │
                │   dropping TPCC   │     │    waiting for    │               │
                │      tables       │     │    replication    │               │
                │                   │     │                   │               │
                └───────────────────┘     └───────────────────┘               │
                          │                         │                         │
                          │                         │                         │
                          │                         │                         │
                          │                         │                         │
                          │                         ▼                         │
                          │               ┌───────────────────┐               │
                          │               │  sleeping for 10  │               │
                          │               │      minutes      │               │
                          │               │                   │               │
                          │               └───────────────────┘               │
                          │                         │                         │
                          │                         │                         │
                          │                         │                         │
                          │                         │                         │
                          │                         ▼                         │
                          │               ┌───────────────────┐               │
                          │               │    decreasing     │               │
                          │               │replication factor │               │
                          │               │       to 3        │               │
                          │               └───────────────────┘               │
                          │                         │                         │
                          │                         │                         │
                          ┼─────────────────────────┼─────────────────────────┼
                                                    │
                                              [after-test]
                                                    │
                                                    │
                                                    ▼
                                          ┌───────────────────┐
                                          │ TPCC consistency  │
                                          │      checks       │
                                          │                   │
                                          └───────────────────┘

```

### Test Plan

A single linearization of the DAG, deterministically influenced by a test run's RNG seed, that gives an ordered list of steps for a specific test run to execute. Each test run generates one test plan from the DAG.

Example: consider the DAG above, one possible test plan might be:

```md
Seed: 1234567890
Plan:
├── setup
│   └── importing tpcc workload (1)
├── test
│   ├── run following steps concurrently
│   │   ├── running TPCC workload for 1 hour (2)
│   │   └── copy bank table (3)
│   ├── increasing replication factor to 5 (4)
│   ├── dropping TPCC tables (5)
│   ├── waiting for replication (6)
│   ├── sleeping for 10 minutes (7)
│   └── decreasing replication factor to 3 (8)
└── after-test
    └── TPCC consistency checks (9)
```

A different run with a different seed might produce:

```md
Seed: 9876543210
Plan:
├── setup
│   └── importing tpcc workload (1)
├── test
│   ├── increasing replication factor to 5 (2)
│   ├── waiting for replication (3)
│   ├── run following steps concurrently
│   │   ├── running TPCC workload for 1 hour (4)
│   │   ├── copy bank table (5)
│   │   └── sleeping for 10 minutes (6)
│   ├── dropping TPCC tables (7)
│   └── decreasing replication factor to 3 (8)
└── after-test
    └── TPCC consistency checks (9)
```
