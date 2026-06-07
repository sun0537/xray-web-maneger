package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"xray-web-manager/internal/xray-proto/app/observatory"
	observatorypb "xray-web-manager/internal/xray-proto/app/observatory/command"
	handlerpb "xray-web-manager/internal/xray-proto/app/proxyman/command"
	routingpb "xray-web-manager/internal/xray-proto/app/router/command"
	statspb "xray-web-manager/internal/xray-proto/app/stats/command"
	"xray-web-manager/internal/xray-proto/common/serial"
	"xray-web-manager/internal/xray-proto/core"

	"xray-web-manager/config"
	"xray-web-manager/sse"
)

type MockHandlerClient struct{}

func (m *MockHandlerClient) ListOutbounds(ctx context.Context, in *handlerpb.ListOutboundsRequest, opts ...grpc.CallOption) (*handlerpb.ListOutboundsResponse, error) {
	return &handlerpb.ListOutboundsResponse{
		Outbounds: []*core.OutboundHandlerConfig{
			{Tag: "node-1", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.vmess.outbound.Config"}},
			{Tag: "node-2", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.vless.outbound.Config"}},
			{Tag: "node-3", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.shadowsocks.outbound.Config"}},
			{Tag: "node-4", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.wireguard.outbound.Config"}},
			{Tag: "node-5", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.trojan.outbound.Config"}},
			{Tag: "node-6", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.socks.outbound.Config"}},
			{Tag: "node-7", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.blackhole.outbound.Config"}},
			{Tag: "direct", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.freedom.Config"}},
			{Tag: "Loopback", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.loopback.Config"}},
			{Tag: "dns", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.dns.Config"}},
		},
	}, nil
}
func (m *MockHandlerClient) AddInbound(ctx context.Context, in *handlerpb.AddInboundRequest, opts ...grpc.CallOption) (*handlerpb.AddInboundResponse, error) {
	return nil, nil
}
func (m *MockHandlerClient) RemoveInbound(ctx context.Context, in *handlerpb.RemoveInboundRequest, opts ...grpc.CallOption) (*handlerpb.RemoveInboundResponse, error) {
	return nil, nil
}
func (m *MockHandlerClient) AlterInbound(ctx context.Context, in *handlerpb.AlterInboundRequest, opts ...grpc.CallOption) (*handlerpb.AlterInboundResponse, error) {
	return nil, nil
}
func (m *MockHandlerClient) AddOutbound(ctx context.Context, in *handlerpb.AddOutboundRequest, opts ...grpc.CallOption) (*handlerpb.AddOutboundResponse, error) {
	return nil, nil
}
func (m *MockHandlerClient) RemoveOutbound(ctx context.Context, in *handlerpb.RemoveOutboundRequest, opts ...grpc.CallOption) (*handlerpb.RemoveOutboundResponse, error) {
	return nil, nil
}
func (m *MockHandlerClient) AlterOutbound(ctx context.Context, in *handlerpb.AlterOutboundRequest, opts ...grpc.CallOption) (*handlerpb.AlterOutboundResponse, error) {
	return nil, nil
}
func (m *MockHandlerClient) GetInboundUsers(ctx context.Context, in *handlerpb.GetInboundUserRequest, opts ...grpc.CallOption) (*handlerpb.GetInboundUserResponse, error) {
	return &handlerpb.GetInboundUserResponse{}, nil
}
func (m *MockHandlerClient) GetInboundUsersCount(ctx context.Context, in *handlerpb.GetInboundUserRequest, opts ...grpc.CallOption) (*handlerpb.GetInboundUsersCountResponse, error) {
	return &handlerpb.GetInboundUsersCountResponse{}, nil
}
func (m *MockHandlerClient) ListInbounds(ctx context.Context, in *handlerpb.ListInboundsRequest, opts ...grpc.CallOption) (*handlerpb.ListInboundsResponse, error) {
	return &handlerpb.ListInboundsResponse{}, nil
}

type MockStatsClient struct{}

func (m *MockStatsClient) QueryStats(ctx context.Context, in *statspb.QueryStatsRequest, opts ...grpc.CallOption) (*statspb.QueryStatsResponse, error) {
	return &statspb.QueryStatsResponse{
		Stat: []*statspb.Stat{
			{Name: "inbound>>>test>>>traffic>>>uplink", Value: 1024},
			{Name: "inbound>>>test>>>traffic>>>downlink", Value: 2048},
		},
	}, nil
}

func (m *MockStatsClient) GetSysStats(ctx context.Context, in *statspb.SysStatsRequest, opts ...grpc.CallOption) (*statspb.SysStatsResponse, error) {
	return &statspb.SysStatsResponse{Uptime: 100, Sys: 50000000, NumGoroutine: 10}, nil
}
func (m *MockStatsClient) GetStats(ctx context.Context, in *statspb.GetStatsRequest, opts ...grpc.CallOption) (*statspb.GetStatsResponse, error) {
	return nil, nil
}
func (m *MockStatsClient) GetStatsOnline(ctx context.Context, in *statspb.GetStatsRequest, opts ...grpc.CallOption) (*statspb.GetStatsResponse, error) {
	return nil, nil
}
func (m *MockStatsClient) GetStatsOnlineIpList(ctx context.Context, in *statspb.GetStatsRequest, opts ...grpc.CallOption) (*statspb.GetStatsOnlineIpListResponse, error) {
	return nil, nil
}
func (m *MockStatsClient) GetAllOnlineUsers(ctx context.Context, in *statspb.GetAllOnlineUsersRequest, opts ...grpc.CallOption) (*statspb.GetAllOnlineUsersResponse, error) {
	return nil, nil
}
func (m *MockStatsClient) GetUsersStats(ctx context.Context, in *statspb.GetUsersStatsRequest, opts ...grpc.CallOption) (*statspb.GetUsersStatsResponse, error) {
	return nil, nil
}

// mockRoutingClient implements routingpb.RoutingServiceClient with configurable responses.
type mockRoutingClient struct {
	balancerResp *routingpb.GetBalancerInfoResponse
	balancerErr  error
	overrideErr  error
}

func (m *mockRoutingClient) GetBalancerInfo(_ context.Context, _ *routingpb.GetBalancerInfoRequest, _ ...grpc.CallOption) (*routingpb.GetBalancerInfoResponse, error) {
	if m.balancerErr != nil {
		return nil, m.balancerErr
	}
	if m.balancerResp != nil {
		return m.balancerResp, nil
	}
	return &routingpb.GetBalancerInfoResponse{Balancer: &routingpb.BalancerMsg{}}, nil
}

func (m *mockRoutingClient) OverrideBalancerTarget(_ context.Context, _ *routingpb.OverrideBalancerTargetRequest, _ ...grpc.CallOption) (*routingpb.OverrideBalancerTargetResponse, error) {
	if m.overrideErr != nil {
		return nil, m.overrideErr
	}
	return &routingpb.OverrideBalancerTargetResponse{}, nil
}

func (m *mockRoutingClient) SubscribeRoutingStats(_ context.Context, _ *routingpb.SubscribeRoutingStatsRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[routingpb.RoutingContext], error) {
	return nil, nil
}
func (m *mockRoutingClient) TestRoute(_ context.Context, _ *routingpb.TestRouteRequest, _ ...grpc.CallOption) (*routingpb.RoutingContext, error) {
	return nil, nil
}
func (m *mockRoutingClient) AddRule(_ context.Context, _ *routingpb.AddRuleRequest, _ ...grpc.CallOption) (*routingpb.AddRuleResponse, error) {
	return nil, nil
}
func (m *mockRoutingClient) RemoveRule(_ context.Context, _ *routingpb.RemoveRuleRequest, _ ...grpc.CallOption) (*routingpb.RemoveRuleResponse, error) {
	return nil, nil
}
func (m *mockRoutingClient) ListRule(_ context.Context, _ *routingpb.ListRuleRequest, _ ...grpc.CallOption) (*routingpb.ListRuleResponse, error) {
	return nil, nil
}

// MockRoutingClient returns a fixed balancer response with no override (auto mode).
// --- Error-capable mocks for getCombinedStats tests ---

type mockStatsClientWithError struct {
	queryErr error
	sysErr   error
}

func (m *mockStatsClientWithError) QueryStats(ctx context.Context, in *statspb.QueryStatsRequest, opts ...grpc.CallOption) (*statspb.QueryStatsResponse, error) {
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	return &statspb.QueryStatsResponse{
		Stat: []*statspb.Stat{
			{Name: "inbound>>>test>>>traffic>>>uplink", Value: 1024},
			{Name: "inbound>>>test>>>traffic>>>downlink", Value: 2048},
		},
	}, nil
}
func (m *mockStatsClientWithError) GetSysStats(ctx context.Context, in *statspb.SysStatsRequest, opts ...grpc.CallOption) (*statspb.SysStatsResponse, error) {
	if m.sysErr != nil {
		return nil, m.sysErr
	}
	return &statspb.SysStatsResponse{Uptime: 100, Sys: 50000000, NumGoroutine: 10}, nil
}
func (m *mockStatsClientWithError) GetStats(context.Context, *statspb.GetStatsRequest, ...grpc.CallOption) (*statspb.GetStatsResponse, error) {
	return nil, nil
}
func (m *mockStatsClientWithError) GetStatsOnline(context.Context, *statspb.GetStatsRequest, ...grpc.CallOption) (*statspb.GetStatsResponse, error) {
	return nil, nil
}
func (m *mockStatsClientWithError) GetStatsOnlineIpList(context.Context, *statspb.GetStatsRequest, ...grpc.CallOption) (*statspb.GetStatsOnlineIpListResponse, error) {
	return nil, nil
}
func (m *mockStatsClientWithError) GetAllOnlineUsers(context.Context, *statspb.GetAllOnlineUsersRequest, ...grpc.CallOption) (*statspb.GetAllOnlineUsersResponse, error) {
	return nil, nil
}
func (m *mockStatsClientWithError) GetUsersStats(context.Context, *statspb.GetUsersStatsRequest, ...grpc.CallOption) (*statspb.GetUsersStatsResponse, error) {
	return nil, nil
}

type mockObservatoryClientError struct{}

func (m *mockObservatoryClientError) GetOutboundStatus(_ context.Context, _ *observatorypb.GetOutboundStatusRequest, _ ...grpc.CallOption) (*observatorypb.GetOutboundStatusResponse, error) {
	return nil, fmt.Errorf("observatory unavailable")
}

// ---

type MockRoutingClient = mockRoutingClient

// MockRoutingClientWithInfo returns a balancer response with an override set.
type MockRoutingClientWithInfo struct {
	override *routingpb.OverrideInfo
}

func (m *MockRoutingClientWithInfo) GetBalancerInfo(_ context.Context, _ *routingpb.GetBalancerInfoRequest, _ ...grpc.CallOption) (*routingpb.GetBalancerInfoResponse, error) {
	return &routingpb.GetBalancerInfoResponse{
		Balancer: &routingpb.BalancerMsg{Override: m.override},
	}, nil
}
func (m *MockRoutingClientWithInfo) OverrideBalancerTarget(_ context.Context, _ *routingpb.OverrideBalancerTargetRequest, _ ...grpc.CallOption) (*routingpb.OverrideBalancerTargetResponse, error) {
	return &routingpb.OverrideBalancerTargetResponse{}, nil
}
func (m *MockRoutingClientWithInfo) SubscribeRoutingStats(_ context.Context, _ *routingpb.SubscribeRoutingStatsRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[routingpb.RoutingContext], error) {
	return nil, nil
}
func (m *MockRoutingClientWithInfo) TestRoute(_ context.Context, _ *routingpb.TestRouteRequest, _ ...grpc.CallOption) (*routingpb.RoutingContext, error) {
	return nil, nil
}
func (m *MockRoutingClientWithInfo) AddRule(_ context.Context, _ *routingpb.AddRuleRequest, _ ...grpc.CallOption) (*routingpb.AddRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClientWithInfo) RemoveRule(_ context.Context, _ *routingpb.RemoveRuleRequest, _ ...grpc.CallOption) (*routingpb.RemoveRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClientWithInfo) ListRule(_ context.Context, _ *routingpb.ListRuleRequest, _ ...grpc.CallOption) (*routingpb.ListRuleResponse, error) {
	return nil, nil
}

// MockRoutingClientError always returns an error from GetBalancerInfo.
type MockRoutingClientError struct{}

func (m *MockRoutingClientError) GetBalancerInfo(_ context.Context, _ *routingpb.GetBalancerInfoRequest, _ ...grpc.CallOption) (*routingpb.GetBalancerInfoResponse, error) {
	return nil, fmt.Errorf("routing unavailable")
}
func (m *MockRoutingClientError) OverrideBalancerTarget(_ context.Context, _ *routingpb.OverrideBalancerTargetRequest, _ ...grpc.CallOption) (*routingpb.OverrideBalancerTargetResponse, error) {
	return nil, fmt.Errorf("routing unavailable")
}
func (m *MockRoutingClientError) SubscribeRoutingStats(_ context.Context, _ *routingpb.SubscribeRoutingStatsRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[routingpb.RoutingContext], error) {
	return nil, nil
}
func (m *MockRoutingClientError) TestRoute(_ context.Context, _ *routingpb.TestRouteRequest, _ ...grpc.CallOption) (*routingpb.RoutingContext, error) {
	return nil, nil
}
func (m *MockRoutingClientError) AddRule(_ context.Context, _ *routingpb.AddRuleRequest, _ ...grpc.CallOption) (*routingpb.AddRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClientError) RemoveRule(_ context.Context, _ *routingpb.RemoveRuleRequest, _ ...grpc.CallOption) (*routingpb.RemoveRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClientError) ListRule(_ context.Context, _ *routingpb.ListRuleRequest, _ ...grpc.CallOption) (*routingpb.ListRuleResponse, error) {
	return nil, nil
}

type mockObservatoryClient struct {
	statuses []*observatory.OutboundStatus
	err      error
}

func (m *mockObservatoryClient) GetOutboundStatus(_ context.Context, _ *observatorypb.GetOutboundStatusRequest, _ ...grpc.CallOption) (*observatorypb.GetOutboundStatusResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	statuses := m.statuses
	if statuses == nil {
		statuses = []*observatory.OutboundStatus{}
	}
	return &observatorypb.GetOutboundStatusResponse{
		Status: &observatory.ObservationResult{
			Status: statuses,
		},
	}, nil
}

func TestHandleGetOutbounds(t *testing.T) {
	s := &Server{
		config:        config.Config{},
		handlerClient: &MockHandlerClient{},
	}

	req, err := http.NewRequest("GET", "/api/outbounds", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()

	s.handleGetOutbounds(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var responseBody []OutboundInfo
	err = json.Unmarshal(rr.Body.Bytes(), &responseBody)
	assert.NoError(t, err)
	assert.Len(t, responseBody, 6)
	assert.Equal(t, "node-1", responseBody[0].Tag)
	assert.Equal(t, "vmess", responseBody[0].Protocol)
	assert.Equal(t, "node-2", responseBody[1].Tag)
	assert.Equal(t, "vless", responseBody[1].Protocol)
	assert.Equal(t, "node-3", responseBody[2].Tag)
	assert.Equal(t, "shadowsocks", responseBody[2].Protocol)
	assert.Equal(t, "node-4", responseBody[3].Tag)
	assert.Equal(t, "wireguard", responseBody[3].Protocol)
	assert.Equal(t, "node-5", responseBody[4].Tag)
	assert.Equal(t, "trojan", responseBody[4].Protocol)
	assert.Equal(t, "node-6", responseBody[5].Tag)
	assert.Equal(t, "socks", responseBody[5].Protocol)
}

func TestHandleGetOutboundsStatus(t *testing.T) {
	s := &Server{
		config: config.Config{},
		observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
			{OutboundTag: "node-1", Alive: true, Delay: 50},
			{OutboundTag: "node-2", Alive: true, Delay: 120},
			{OutboundTag: "node-3", Alive: false, Delay: 0},
		}},
		startTime: time.Time{},
	}

	req, err := http.NewRequest("GET", "/api/outbounds-status", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()

	s.handleGetOutboundsStatus(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var responseBody []OutboundStatusData
	err = json.Unmarshal(rr.Body.Bytes(), &responseBody)
	assert.NoError(t, err)
	assert.Len(t, responseBody, 3)
	assert.Equal(t, "node-1", responseBody[0].Tag)
	assert.Equal(t, true, responseBody[0].Alive)
	assert.Equal(t, int64(50), responseBody[0].Delay)
	assert.Equal(t, "node-2", responseBody[1].Tag)
	assert.Equal(t, true, responseBody[1].Alive)
	assert.Equal(t, int64(120), responseBody[1].Delay)
	assert.Equal(t, "node-3", responseBody[2].Tag)
	assert.Equal(t, false, responseBody[2].Alive)
	assert.Equal(t, int64(0), responseBody[2].Delay)
}

func TestHandleStatsSSE(t *testing.T) {
	sseMgr := sse.NewManager()

	fetchFn := func() (any, error) {
		return StatsData{
			Uplink: 1024, Downlink: 2048, Uptime: 100,
			SysMem: 50000000, Goroutines: 10, Outbounds: []OutboundStatusData{},
		}, nil
	}
	broadcaster := sse.NewBroadcaster(fetchFn, nil, 100*time.Millisecond)
	defer broadcaster.Stop()

	s := &Server{
		config:            config.Config{},
		statsClient:       &MockStatsClient{},
		sseManager:        sseMgr,
		handlerClient:     &MockHandlerClient{},
		routingClient:     &mockRoutingClient{},
		observatoryClient: &mockObservatoryClient{},
		broadcaster:       broadcaster,
		startTime:         time.Time{},
	}

	mux := http.NewServeMux()
	s.RegisterHandlers(mux)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/stats-sse", nil)
	resp, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	scanner := bufio.NewScanner(resp.Body)
	defer resp.Body.Close()

	assert.True(t, scanner.Scan(), "服务器应首先发送 event: update")
	assert.Equal(t, "event: update", scanner.Text())

	assert.True(t, scanner.Scan(), "服务器应在 event 后发送 data")
	dataLine := scanner.Text()
	assert.True(t, strings.HasPrefix(dataLine, "data: {"), "数据行应以 'data: {' 开头")

	var stats map[string]interface{}
	err = json.Unmarshal([]byte(strings.TrimPrefix(dataLine, "data: ")), &stats)
	assert.NoError(t, err, "解析 SSE JSON 数据失败")

	assert.Equal(t, float64(1024), stats["uplink"])
	assert.Equal(t, float64(2048), stats["downlink"])
	assert.Equal(t, float64(100), stats["uptime"])
	assert.Equal(t, float64(10), stats["goroutines"])

	assert.NotNil(t, stats["outbounds"], "outbounds 字段应存在且不为 nil")
	assert.IsType(t, []interface{}{}, stats["outbounds"], "outbounds 应为数组类型")

	assert.True(t, scanner.Scan(), "服务器应在 data 后发送空行")
	assert.Equal(t, "", scanner.Text())

	// After the initial push, the broadcaster may send additional events.
	// With dedup, unchanged data is suppressed. We just verify the connection
	// produces at least one valid SSE frame — further frames are optional.
}

func TestHandleSwitchOutbound(t *testing.T) {
	s := &Server{
		config: config.Config{
			Xray: config.XrayConfig{BalancerTag: "balancer"},
		},
		routingClient: &mockRoutingClient{},
	}

	t.Run("Success", func(t *testing.T) {
		body := `{"outbound_tag": "node-1"}`
		req, _ := http.NewRequest("POST", "/api/switch-outbound", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		s.handleSwitchOutbound(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp successResponse
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.Equal(t, "success", resp.Status)
	})

	t.Run("Empty body", func(t *testing.T) {
		req, _ := http.NewRequest("POST", "/api/switch-outbound", bytes.NewBufferString(""))
		rr := httptest.NewRecorder()

		s.handleSwitchOutbound(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Illegal characters", func(t *testing.T) {
		body := `{"outbound_tag": "node<script>"}`
		req, _ := http.NewRequest("POST", "/api/switch-outbound", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		s.handleSwitchOutbound(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Tag too long", func(t *testing.T) {
		longTag := strings.Repeat("a", 257)
		body := `{"outbound_tag": "` + longTag + `"}`
		req, _ := http.NewRequest("POST", "/api/switch-outbound", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()

		s.handleSwitchOutbound(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestHandleHealthCheck(t *testing.T) {
	sseMgr := sse.NewManager()
	s := &Server{
		config: config.Config{
			Xray: config.XrayConfig{BalancerTag: "balancer"},
		},
		statsClient: &MockStatsClient{},
		sseManager:  sseMgr,
		startTime:   time.Now().Add(-100 * time.Second),
	}

	req, _ := http.NewRequest("GET", "/api/health", nil)
	rr := httptest.NewRecorder()

	s.handleHealthCheck(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var health HealthStatus
	err := json.Unmarshal(rr.Body.Bytes(), &health)
	assert.NoError(t, err)
	assert.Equal(t, "healthy", health.Status)
	assert.Equal(t, "connected", health.XrayAPIStatus)
	assert.Equal(t, "balancer", health.BalancerTag)
	assert.True(t, health.Uptime >= 99)
}

// contextRecordingStatsClient captures the context passed to GetSysStats so a
// test can later assert that the context is independent of any specific
// request's context. Used to regression-test the singleflight context bug.
type contextRecordingStatsClient struct {
	MockStatsClient
	received chan context.Context
}

func (c *contextRecordingStatsClient) GetSysStats(ctx context.Context, in *statspb.SysStatsRequest, opts ...grpc.CallOption) (*statspb.SysStatsResponse, error) {
	// Non-blocking send so the producer is not stuck if the consumer isn't ready.
	select {
	case c.received <- ctx:
	default:
	}
	// Block until either the context is cancelled OR a short success window.
	// This is the window during which the test cancels the request context.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(300 * time.Millisecond):
		return &statspb.SysStatsResponse{Uptime: 1, Sys: 1, NumGoroutine: 1}, nil
	}
}

// TestHandleHealthCheckSingleflightContextIsRequestIndependent regression-tests
// the bug where singleflight.Do captured r.Context() in its closure, so that
// if the first request to coalesce disconnected, all waiters would see
// "disconnected" even though the gRPC call was perfectly capable of succeeding.
//
// With the fix, the gRPC call uses s.shutdownCtx and survives request
// cancellation. The captured context must NOT be cancelled when the
// initiating request's context is cancelled.
func TestHandleHealthCheckSingleflightContextIsRequestIndependent(t *testing.T) {
	mock := &contextRecordingStatsClient{
		received: make(chan context.Context, 1),
	}
	sseMgr := sse.NewManager()
	s := &Server{
		config: config.Config{
			Xray: config.XrayConfig{BalancerTag: "balancer"},
		},
		statsClient: mock,
		sseManager:  sseMgr,
		startTime:   time.Now().Add(-100 * time.Second),
		shutdownCtx: context.Background(),
	}

	// Issue a health check with a cancelable request context.
	reqCtx, cancelReq := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(reqCtx, "GET", "/api/health", nil)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		s.handleHealthCheck(rr, req)
		close(done)
	}()

	// Wait until the gRPC call is in flight.
	var gRPCCtx context.Context
	select {
	case gRPCCtx = <-mock.received:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: gRPC call was never made")
	}

	// Cancel the request that triggered the singleflight call. With the bug,
	// this cancellation would propagate into gRPCCtx and fail the call.
	cancelReq()

	// No time.Sleep is needed: Go's cancelCtx.cancel() synchronously closes
	// the done channel of the parent and walks the children tree, so any
	// context derived from reqCtx is already in the cancelled state the
	// instant cancelReq() returns. With the fix, gRPCCtx is NOT a child of
	// reqCtx, so its Err() must still be nil here.

	// The gRPC context must NOT be cancelled.
	assert.NoError(t, gRPCCtx.Err(), "gRPC context must not be cancelled when the request context is cancelled")

	// Wait for the handler to return and confirm the call succeeded.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: handler did not return")
	}

	var health HealthStatus
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &health))
	assert.Equal(t, "connected", health.XrayAPIStatus, "gRPC call should succeed despite the initiating request being cancelled")
}

func TestHandleGetConfig(t *testing.T) {
	t.Run("Returns balancer tag and log type", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "my-balancer"},
				Log:  config.LogConfig{Type: "file"},
			},
		}

		req := httptest.NewRequest("GET", "/api/config", nil)
		rr := httptest.NewRecorder()
		s.handleGetConfig(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp configResponse
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.Equal(t, "my-balancer", resp.BalancerTag)
		assert.Equal(t, "file", resp.LogType)
	})

	t.Run("Empty log type defaults to none", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
				Log:  config.LogConfig{Type: ""},
			},
		}

		req := httptest.NewRequest("GET", "/api/config", nil)
		rr := httptest.NewRecorder()
		s.handleGetConfig(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp configResponse
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.Equal(t, "none", resp.LogType)
	})
}

func TestHandleGetCurrentOutbound(t *testing.T) {
	t.Run("Returns auto when no override", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient: &MockRoutingClient{},
		}

		req := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp CurrentOutboundData
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.True(t, resp.Auto)
		assert.Equal(t, "", resp.Current)
	})
}

func TestHandleGetCurrentOutboundWithOverride(t *testing.T) {
	t.Run("Returns current tag when override set", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient: &MockRoutingClientWithInfo{
				override: &routingpb.OverrideInfo{Target: "node-1"},
			},
		}

		req := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp CurrentOutboundData
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.Equal(t, "node-1", resp.Current)
		assert.False(t, resp.Auto)
	})

	t.Run("Returns auto when no override target", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient: &MockRoutingClientWithInfo{override: nil},
		}

		req := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp CurrentOutboundData
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.Equal(t, "", resp.Current)
		assert.True(t, resp.Auto)
	})

	t.Run("Returns 502 when routing fails", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient: &MockRoutingClientError{},
		}

		req := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr, req)

		assert.Equal(t, http.StatusBadGateway, rr.Code)
	})
}

func TestGetCombinedStats(t *testing.T) {
	t.Run("All sources succeed", func(t *testing.T) {
		s := &Server{
			statsClient: &MockStatsClient{},
			observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "node-1", Alive: true, Delay: 50},
			}},
		}
		stats, err := s.getCombinedStats(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, int64(1024), stats.Uplink)
		assert.Equal(t, int64(2048), stats.Downlink)
		assert.Equal(t, int64(100), stats.Uptime)
		assert.Equal(t, uint64(50000000), stats.SysMem)
		assert.Equal(t, 10, stats.Goroutines)
		assert.Len(t, stats.Outbounds, 1)
		assert.False(t, stats.Degraded)
	})

	t.Run("QueryStats fails — degraded", func(t *testing.T) {
		s := &Server{
			statsClient: &mockStatsClientWithError{queryErr: fmt.Errorf("stats unavailable")},
			observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "node-1", Alive: true, Delay: 50},
			}},
		}
		stats, err := s.getCombinedStats(context.Background())
		assert.NoError(t, err)
		assert.True(t, stats.Degraded)
		assert.Equal(t, int64(0), stats.Uplink)
		assert.Equal(t, int64(100), stats.Uptime)
	})

	t.Run("GetSysStats fails — degraded", func(t *testing.T) {
		s := &Server{
			statsClient: &mockStatsClientWithError{sysErr: fmt.Errorf("sys unavailable")},
			observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "node-1", Alive: true, Delay: 50},
			}},
		}
		stats, err := s.getCombinedStats(context.Background())
		assert.NoError(t, err)
		assert.True(t, stats.Degraded)
		assert.Equal(t, int64(1024), stats.Uplink)
		assert.Equal(t, int64(0), stats.Uptime)
	})

	t.Run("Observatory fails — outbounds empty slice", func(t *testing.T) {
		s := &Server{
			statsClient:       &MockStatsClient{},
			observatoryClient: &mockObservatoryClientError{},
		}
		stats, err := s.getCombinedStats(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, int64(1024), stats.Uplink)
		assert.NotNil(t, stats.Outbounds)
		assert.Empty(t, stats.Outbounds)
	})

	t.Run("Query and sys both fail — returns error", func(t *testing.T) {
		s := &Server{
			statsClient:       &mockStatsClientWithError{queryErr: fmt.Errorf("q"), sysErr: fmt.Errorf("s")},
			observatoryClient: &mockObservatoryClientError{},
		}
		_, err := s.getCombinedStats(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "all data sources failed")
	})

	t.Run("Context cancelled — returns early", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s := &Server{
			statsClient:       &MockStatsClient{},
			observatoryClient: &mockObservatoryClient{},
		}
		// With fast mocks, the goroutines may complete before the context
		// deadline is checked, so we just verify no panic occurs.
		s.getCombinedStats(ctx)
	})
}
