// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/cockroachdb/cockroach/pkg/server/serverpb (interfaces: TenantStatusServer)

// Package mocks is a generated GoMock package.
package mocks

import (
	context "context"
	reflect "reflect"

	roachpb "github.com/cockroachdb/cockroach/pkg/roachpb"
	serverpb "github.com/cockroachdb/cockroach/pkg/server/serverpb"
	gomock "github.com/golang/mock/gomock"
)

// MockTenantStatusServer is a mock of TenantStatusServer interface.
type MockTenantStatusServer struct {
	ctrl     *gomock.Controller
	recorder *MockTenantStatusServerMockRecorder
}

// MockTenantStatusServerMockRecorder is the mock recorder for MockTenantStatusServer.
type MockTenantStatusServerMockRecorder struct {
	mock *MockTenantStatusServer
}

// NewMockTenantStatusServer creates a new mock instance.
func NewMockTenantStatusServer(ctrl *gomock.Controller) *MockTenantStatusServer {
	mock := &MockTenantStatusServer{ctrl: ctrl}
	mock.recorder = &MockTenantStatusServerMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockTenantStatusServer) EXPECT() *MockTenantStatusServerMockRecorder {
	return m.recorder
}

// DownloadSpan mocks base method.
func (m *MockTenantStatusServer) DownloadSpan(arg0 context.Context, arg1 *serverpb.DownloadSpanRequest) (*serverpb.DownloadSpanResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DownloadSpan", arg0, arg1)
	ret0, _ := ret[0].(*serverpb.DownloadSpanResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DownloadSpan indicates an expected call of DownloadSpan.
func (mr *MockTenantStatusServerMockRecorder) DownloadSpan(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DownloadSpan", reflect.TypeOf((*MockTenantStatusServer)(nil).DownloadSpan), arg0, arg1)
}

// HotRangesV2 mocks base method.
func (m *MockTenantStatusServer) HotRangesV2(arg0 context.Context, arg1 *serverpb.HotRangesRequest) (*serverpb.HotRangesResponseV2, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "HotRangesV2", arg0, arg1)
	ret0, _ := ret[0].(*serverpb.HotRangesResponseV2)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// HotRangesV2 indicates an expected call of HotRangesV2.
func (mr *MockTenantStatusServerMockRecorder) HotRangesV2(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "HotRangesV2", reflect.TypeOf((*MockTenantStatusServer)(nil).HotRangesV2), arg0, arg1)
}

// NetworkConnectivity mocks base method.
func (m *MockTenantStatusServer) NetworkConnectivity(arg0 context.Context, arg1 *serverpb.NetworkConnectivityRequest) (*serverpb.NetworkConnectivityResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "NetworkConnectivity", arg0, arg1)
	ret0, _ := ret[0].(*serverpb.NetworkConnectivityResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// NetworkConnectivity indicates an expected call of NetworkConnectivity.
func (mr *MockTenantStatusServerMockRecorder) NetworkConnectivity(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "NetworkConnectivity", reflect.TypeOf((*MockTenantStatusServer)(nil).NetworkConnectivity), arg0, arg1)
}

// Nodes mocks base method.
func (m *MockTenantStatusServer) Nodes(arg0 context.Context, arg1 *serverpb.NodesRequest) (*serverpb.NodesResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Nodes", arg0, arg1)
	ret0, _ := ret[0].(*serverpb.NodesResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Nodes indicates an expected call of Nodes.
func (mr *MockTenantStatusServerMockRecorder) Nodes(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Nodes", reflect.TypeOf((*MockTenantStatusServer)(nil).Nodes), arg0, arg1)
}

// Ranges mocks base method.
func (m *MockTenantStatusServer) Ranges(arg0 context.Context, arg1 *serverpb.RangesRequest) (*serverpb.RangesResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Ranges", arg0, arg1)
	ret0, _ := ret[0].(*serverpb.RangesResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Ranges indicates an expected call of Ranges.
func (mr *MockTenantStatusServerMockRecorder) Ranges(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Ranges", reflect.TypeOf((*MockTenantStatusServer)(nil).Ranges), arg0, arg1)
}

// Regions mocks base method.
func (m *MockTenantStatusServer) Regions(arg0 context.Context, arg1 *serverpb.RegionsRequest) (*serverpb.RegionsResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Regions", arg0, arg1)
	ret0, _ := ret[0].(*serverpb.RegionsResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Regions indicates an expected call of Regions.
func (mr *MockTenantStatusServerMockRecorder) Regions(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Regions", reflect.TypeOf((*MockTenantStatusServer)(nil).Regions), arg0, arg1)
}

// SpanStats mocks base method.
func (m *MockTenantStatusServer) SpanStats(arg0 context.Context, arg1 *roachpb.SpanStatsRequest) (*roachpb.SpanStatsResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "SpanStats", arg0, arg1)
	ret0, _ := ret[0].(*roachpb.SpanStatsResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// SpanStats indicates an expected call of SpanStats.
func (mr *MockTenantStatusServerMockRecorder) SpanStats(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SpanStats", reflect.TypeOf((*MockTenantStatusServer)(nil).SpanStats), arg0, arg1)
}

// TenantRanges mocks base method.
func (m *MockTenantStatusServer) TenantRanges(arg0 context.Context, arg1 *serverpb.TenantRangesRequest) (*serverpb.TenantRangesResponse, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "TenantRanges", arg0, arg1)
	ret0, _ := ret[0].(*serverpb.TenantRangesResponse)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// TenantRanges indicates an expected call of TenantRanges.
func (mr *MockTenantStatusServerMockRecorder) TenantRanges(arg0, arg1 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "TenantRanges", reflect.TypeOf((*MockTenantStatusServer)(nil).TenantRanges), arg0, arg1)
}
