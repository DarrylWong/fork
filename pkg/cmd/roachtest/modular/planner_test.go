package modular

import (
	"testing"

	"github.com/cockroachdb/cockroach/pkg/testutils/datapathutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/echotest"
)

func TestPrettyPrintPlan(t *testing.T) {
	plan := newTestPlan(12345, 10000).
		Stage("Setup").
		AddStep("Start Cluster").
		AddStep("Load TPCC Workload", "Load Bank Workload").
		Stage("Baseline Testing").
		AddStep("Run TPCC Workload", "Measure latency", "Measure throughput").
		AddStep("Verify consistency").
		Stage("Chaos Testing").
		AddStep("Inject Disk Stall").
		AddStep("Run TPCC Workload", "Measure latency", "Measure throughput").
		AddStep("Recover Disk Stall").
		AddStep("Verify consistency").
		Plan()

	echotest.Require(t, plan.String(), datapathutils.TestDataPath(t, "basic_planner"))
}
