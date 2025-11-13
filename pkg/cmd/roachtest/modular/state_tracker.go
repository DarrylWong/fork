package modular

// ClusterStateTracker tracks cluster state changes made during test execution
// to allow restoration to the original state on failure as well as more intelligent
// helper functions.
// TODO: implement cluster state tracking. Any modification to the cluster should
// go through this tracker first, which means we must create adequate helpers
// for test writers to use and disallow direct manipulation.
type ClusterStateTracker struct{}
