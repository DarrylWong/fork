package modular

import (
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
)

type DeploymentMode string

const (
	SystemOnlyDeployment      = DeploymentMode("system-only")
	SharedProcessDeployment   = DeploymentMode("shared-process")
	SeparateProcessDeployment = DeploymentMode("separate-process")
)

// ClusterSpec describes a cluster that a test expects to have.
type ClusterSpec struct {
	Name                    string
	DisabledDeploymentModes []DeploymentMode
	MinNodes, MaxNodes      int
	// Randomized values determined at cluster creation time
	ActualNodes    int
	DeploymentMode DeploymentMode
}

type WorkloadSpec struct {
	name     string
	numNodes int
}

// ServiceDescriptor encapsulates the information about where a
// service (system tenant or otherwise) is running.
type ServiceDescriptor struct {
	// Name is the name of the service ("system" for the system
	// tenant, or the tenant name otherwise.)
	Name string

	Type install.ServiceMode

	// Nodes is the set of nodes in the cluster where clients can
	// connect to that service.
	Nodes option.NodeListOption

	// StartID is the test step ID after which this service is
	// expected to be running and accepting client connections.
	StartID int
}

type Cluster interface {
	// Name is the friendly name of the cluster, used to differentiate between
	// multiple CRDB or workload clusters in a single test. This is different from
	// the name of the roachprod cluster, which is derived from the user and timestamp.
	Name() string
	Cluster() cluster.Cluster
}
type CockroachCluster struct {
	name     string
	Services []ServiceDescriptor
	cluster  cluster.Cluster
}

func (c *CockroachCluster) Name() string {
	return c.name
}

func (c *CockroachCluster) Cluster() cluster.Cluster {
	return c.cluster
}

type WorkloadCluster struct {
	Name    string
	Cluster cluster.Cluster
}

// ClusterOption configures a ClusterSpec.
type ClusterOption func(*ClusterSpec)

// WorkloadOption configures a WorkloadSpec.
type WorkloadOption func(*WorkloadSpec)

// StageOption configures a Stage.
type StageOption func(*Stage)

// StepOption configures a singleStep.
type StepOption func(*singleStep)

// MinNodes sets the minimum number of nodes for a cluster.
func MinNodes(n int) ClusterOption {
	return func(spec *ClusterSpec) {
		spec.MinNodes = n
	}
}

// MaxNodes sets the maximum number of nodes for a cluster.
func MaxNodes(n int) ClusterOption {
	return func(spec *ClusterSpec) {
		spec.MaxNodes = n
	}
}

// NodeCount sets the exact number of nodes for a cluster.
func NodeCount(n int) ClusterOption {
	return func(spec *ClusterSpec) {
		spec.MinNodes = n
		spec.MaxNodes = n
	}
}

// DisabledDeploymentModes disables specific deployment modes.
func DisabledDeploymentModes(modes ...DeploymentMode) ClusterOption {
	return func(spec *ClusterSpec) {
		spec.DisabledDeploymentModes = append(spec.DisabledDeploymentModes, modes...)
	}
}

// WorkloadNodeCount sets the number of nodes for a workload cluster.
func WorkloadNodeCount(n int) WorkloadOption {
	return func(spec *WorkloadSpec) {
		spec.numNodes = n
	}
}

// InBackground configures a step to run in the background.
func InBackground() StepOption {
	return func(step *singleStep) {
		step.background = make(shouldStop)
	}
}

// DisableFailureInjection configures a stage to disable failure injection.
func DisableFailureInjection() StageOption {
	return func(stage *Stage) {
		// This would configure the stage to disable failure injection
		// Implementation details would depend on the failure injection system
	}
}
