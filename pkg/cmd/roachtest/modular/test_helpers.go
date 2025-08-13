package modular

import (
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/golang/mock/gomock"
)

// CreateMockCluster creates a gomock cluster for testing purposes.
// This function is intended to be used with TestingKnobs.CreateClusterFn.
//
// Example usage:
//
//	testDef := NewTest("test", 12345)
//	testDef.SetTestingKnobs(&TestingKnobs{
//	    CreateClusterFn: CreateMockCluster,
//	})
//
// For more control over the mock, tests can create their own function:
//
//	ctrl := gomock.NewController(t)
//	testDef.SetTestingKnobs(&TestingKnobs{
//	    CreateClusterFn: func(name string, nodes int, mode DeploymentMode) cluster.Cluster {
//	        mockCluster := cluster.NewMockCluster(ctrl)
//	        mockCluster.EXPECT().Name().Return(name).AnyTimes()
//	        // Add more expectations as needed
//	        return mockCluster
//	    },
//	})
func CreateMockCluster(name string, nodes int, mode DeploymentMode) cluster.Cluster {
	// Create a mock controller with a minimal test reporter
	ctrl := gomock.NewController(&mockTestReporter{})
	mockCluster := cluster.NewMockCluster(ctrl)

	// Set up basic expectations that most tests might need
	mockCluster.EXPECT().Name().Return(name).AnyTimes()

	return mockCluster
}

// mockTestReporter is a minimal gomock.TestReporter implementation
type mockTestReporter struct{}

func (m *mockTestReporter) Errorf(format string, args ...interface{}) {}
func (m *mockTestReporter) Fatalf(format string, args ...interface{}) {}
