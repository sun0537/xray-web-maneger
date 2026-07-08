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
	"sync"
	"sync/atomic"
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

type MockStatsClient struct {
	baseStatsClient
}

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

// baseRoutingClient provides no-op stubs for rarely-used RoutingServiceClient methods.
// Embed in test mocks to avoid repeating identical return-nil-nil implementations.
type baseRoutingClient struct{}

func (baseRoutingClient) SubscribeRoutingStats(_ context.Context, _ *routingpb.SubscribeRoutingStatsRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[routingpb.RoutingContext], error) {
	return nil, nil
}
func (baseRoutingClient) TestRoute(_ context.Context, _ *routingpb.TestRouteRequest, _ ...grpc.CallOption) (*routingpb.RoutingContext, error) {
	return nil, nil
}
func (baseRoutingClient) AddRule(_ context.Context, _ *routingpb.AddRuleRequest, _ ...grpc.CallOption) (*routingpb.AddRuleResponse, error) {
	return nil, nil
}
func (baseRoutingClient) RemoveRule(_ context.Context, _ *routingpb.RemoveRuleRequest, _ ...grpc.CallOption) (*routingpb.RemoveRuleResponse, error) {
	return nil, nil
}
func (baseRoutingClient) ListRule(_ context.Context, _ *routingpb.ListRuleRequest, _ ...grpc.CallOption) (*routingpb.ListRuleResponse, error) {
	return nil, nil
}

// baseStatsClient provides no-op stubs for rarely-used StatsServiceClient methods.
type baseStatsClient struct{}

func (baseStatsClient) GetStats(context.Context, *statspb.GetStatsRequest, ...grpc.CallOption) (*statspb.GetStatsResponse, error) {
	return nil, nil
}
func (baseStatsClient) GetStatsOnline(context.Context, *statspb.GetStatsRequest, ...grpc.CallOption) (*statspb.GetStatsResponse, error) {
	return nil, nil
}
func (baseStatsClient) GetStatsOnlineIpList(context.Context, *statspb.GetStatsRequest, ...grpc.CallOption) (*statspb.GetStatsOnlineIpListResponse, error) {
	return nil, nil
}
func (baseStatsClient) GetAllOnlineUsers(context.Context, *statspb.GetAllOnlineUsersRequest, ...grpc.CallOption) (*statspb.GetAllOnlineUsersResponse, error) {
	return nil, nil
}
func (baseStatsClient) GetUsersStats(context.Context, *statspb.GetUsersStatsRequest, ...grpc.CallOption) (*statspb.GetUsersStatsResponse, error) {
	return nil, nil
}

// mockRoutingClient implements routingpb.RoutingServiceClient with configurable responses.
type mockRoutingClient struct {
	baseRoutingClient
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

// MockRoutingClient returns a fixed balancer response with no override (auto mode).
// --- Error-capable mocks for getCombinedStats tests ---

type mockStatsClientWithError struct {
	baseStatsClient
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

type mockObservatoryClientError struct{}

func (m *mockObservatoryClientError) GetOutboundStatus(_ context.Context, _ *observatorypb.GetOutboundStatusRequest, _ ...grpc.CallOption) (*observatorypb.GetOutboundStatusResponse, error) {
	return nil, fmt.Errorf("observatory unavailable")
}

// ---

type MockRoutingClient = mockRoutingClient

// mockHandlerClientCustom wraps MockHandlerClient but overrides ListOutbounds
// to return caller-supplied outbounds. Used by findBestAvailableNode tests
// that need specific protocol/tag combinations.
type mockHandlerClientCustom struct {
	MockHandlerClient
	outbounds []*core.OutboundHandlerConfig
}

func (m *mockHandlerClientCustom) ListOutbounds(_ context.Context, _ *handlerpb.ListOutboundsRequest, _ ...grpc.CallOption) (*handlerpb.ListOutboundsResponse, error) {
	return &handlerpb.ListOutboundsResponse{Outbounds: m.outbounds}, nil
}

// MockRoutingClientWithInfo returns a balancer response with an override set.
type MockRoutingClientWithInfo struct {
	baseRoutingClient
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

// MockRoutingClientError always returns an error from GetBalancerInfo.
type MockRoutingClientError struct {
	baseRoutingClient
}

func (m *MockRoutingClientError) GetBalancerInfo(_ context.Context, _ *routingpb.GetBalancerInfoRequest, _ ...grpc.CallOption) (*routingpb.GetBalancerInfoResponse, error) {
	return nil, fmt.Errorf("routing unavailable")
}
func (m *MockRoutingClientError) OverrideBalancerTarget(_ context.Context, _ *routingpb.OverrideBalancerTargetRequest, _ ...grpc.CallOption) (*routingpb.OverrideBalancerTargetResponse, error) {
	return nil, fmt.Errorf("routing unavailable")
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

	fetchFn := func() (StatsData, error) {
		return StatsData{
			Uplink: 1024, Downlink: 2048, Uptime: 100,
			SysMem: 50000000, Goroutines: 10, Outbounds: []OutboundStatusData{},
		}, nil
	}
	broadcaster := sse.NewBroadcaster(fetchFn, nil, degradedStatsData, 100*time.Millisecond)
	defer broadcaster.Stop()

	s := &Server{
		config:            config.Config{},
		statsClient:       &MockStatsClient{},
		sseManager:        sseMgr,
		handlerClient:     &MockHandlerClient{},
		routingClient:     &MockRoutingClient{},
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
		routingClient: &MockRoutingClient{},
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

func TestHandleGetCurrentOutboundActiveNode(t *testing.T) {
	t.Run("Auto mode returns best available node", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			handlerClient:     &MockHandlerClient{},
			observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "node-1", Alive: true, Delay: 100},
				{OutboundTag: "node-2", Alive: true, Delay: 50},
				{OutboundTag: "node-3", Alive: false, Delay: 0},
			}},
		}

		req := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp CurrentOutboundData
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.True(t, resp.Auto)
		assert.Equal(t, "", resp.Current)
		assert.Equal(t, "node-2", resp.ActiveNode)
	})

	t.Run("Auto mode with no healthy nodes returns empty active node", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			handlerClient:     &MockHandlerClient{},
			observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "node-1", Alive: false, Delay: 0},
				{OutboundTag: "node-2", Alive: false, Delay: 0},
			}},
		}

		req := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp CurrentOutboundData
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.True(t, resp.Auto)
		assert.Equal(t, "", resp.ActiveNode)
	})

	t.Run("Auto mode with nil observatory client returns empty active node", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			handlerClient:     &MockHandlerClient{},
			observatoryClient: nil,
		}

		req := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp CurrentOutboundData
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.True(t, resp.Auto)
		assert.Equal(t, "", resp.ActiveNode)
	})

	t.Run("Auto mode with observatory error returns empty active node", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			handlerClient:     &MockHandlerClient{},
			observatoryClient: &mockObservatoryClientError{},
		}

		req := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp CurrentOutboundData
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.True(t, resp.Auto)
		assert.Equal(t, "", resp.ActiveNode)
	})

	t.Run("Auto mode fallback to first alive node when all delays zero", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			handlerClient:     &MockHandlerClient{},
			observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "node-1", Alive: true, Delay: 0},
				{OutboundTag: "node-2", Alive: true, Delay: 0},
			}},
		}

		req := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp CurrentOutboundData
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.True(t, resp.Auto)
		assert.Equal(t, "node-1", resp.ActiveNode)
	})

	t.Run("Manual mode does not populate active node", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient: &MockRoutingClientWithInfo{
				override: &routingpb.OverrideInfo{Target: "node-1"},
			},
			handlerClient: &MockHandlerClient{},
			observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "node-1", Alive: true, Delay: 50},
				{OutboundTag: "node-2", Alive: true, Delay: 100},
			}},
		}

		req := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp CurrentOutboundData
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.False(t, resp.Auto)
		assert.Equal(t, "node-1", resp.Current)
		assert.Equal(t, "", resp.ActiveNode)
	})

	t.Run("Picks node with lowest delay among alive nodes", func(t *testing.T) {
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			handlerClient:     &MockHandlerClient{},
			observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "fast-node", Alive: true, Delay: 10},
				{OutboundTag: "medium-node", Alive: true, Delay: 200},
				{OutboundTag: "dead-node", Alive: false, Delay: 0},
				{OutboundTag: "slow-node", Alive: true, Delay: 500},
			}},
		}

		req := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp CurrentOutboundData
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.True(t, resp.Auto)
		assert.Equal(t, "fast-node", resp.ActiveNode)
	})
}

func TestFindBestAvailableNode(t *testing.T) {
	t.Run("Returns empty when observatory client is nil", func(t *testing.T) {
		s := &Server{handlerClient: &MockHandlerClient{}, observatoryClient: nil}
		result := s.findBestAvailableNode()
		assert.Equal(t, "", result)
	})

	t.Run("Returns empty when no statuses", func(t *testing.T) {
		s := &Server{handlerClient: &MockHandlerClient{}, observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{}}}
		result := s.findBestAvailableNode()
		assert.Equal(t, "", result)
	})

	t.Run("Returns empty when observatory returns error", func(t *testing.T) {
		s := &Server{handlerClient: &MockHandlerClient{}, observatoryClient: &mockObservatoryClientError{}}
		result := s.findBestAvailableNode()
		assert.Equal(t, "", result)
	})

	t.Run("Skips dead nodes and zero-delay nodes", func(t *testing.T) {
		s := &Server{handlerClient: &MockHandlerClient{}, observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
			{OutboundTag: "dead", Alive: false, Delay: 100},
			{OutboundTag: "zero-delay", Alive: true, Delay: 0},
			{OutboundTag: "alive", Alive: true, Delay: 50},
		}}}
		result := s.findBestAvailableNode()
		assert.Equal(t, "alive", result)
	})

	t.Run("Returns lowest delay node", func(t *testing.T) {
		s := &Server{handlerClient: &MockHandlerClient{}, observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
			{OutboundTag: "slow", Alive: true, Delay: 300},
			{OutboundTag: "fast", Alive: true, Delay: 20},
			{OutboundTag: "medium", Alive: true, Delay: 100},
		}}}
		result := s.findBestAvailableNode()
		assert.Equal(t, "fast", result)
	})

	t.Run("Returns empty when all nodes are dead", func(t *testing.T) {
		s := &Server{handlerClient: &MockHandlerClient{}, observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
			{OutboundTag: "dead1", Alive: false, Delay: 0},
			{OutboundTag: "dead2", Alive: false, Delay: 0},
		}}}
		result := s.findBestAvailableNode()
		assert.Equal(t, "", result)
	})

	t.Run("Fallback to first alive node when all delays are zero", func(t *testing.T) {
		s := &Server{handlerClient: &MockHandlerClient{}, observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
			{OutboundTag: "dead", Alive: false, Delay: 0},
			{OutboundTag: "alive-1", Alive: true, Delay: 0},
			{OutboundTag: "alive-2", Alive: true, Delay: 0},
		}}}
		result := s.findBestAvailableNode()
		assert.Equal(t, "alive-1", result, "所有 alive 节点 Delay=0 时应 fallback 到第一个 alive 节点")
	})

	t.Run("Prefers measured node over zero-delay fallback", func(t *testing.T) {
		s := &Server{handlerClient: &MockHandlerClient{}, observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
			{OutboundTag: "unmeasured", Alive: true, Delay: 0},
			{OutboundTag: "measured", Alive: true, Delay: 100},
		}}}
		result := s.findBestAvailableNode()
		assert.Equal(t, "measured", result, "有已探测节点时应优先选择，而非 fallback")
	})

	t.Run("Excludes nodes with non-proxy protocols", func(t *testing.T) {
		s := &Server{
			handlerClient: &mockHandlerClientCustom{
				outbounds: []*core.OutboundHandlerConfig{
					{Tag: "freedom-out", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.freedom.Config"}},
					{Tag: "proxy-out", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.vmess.outbound.Config"}},
					{Tag: "blackhole-out", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.blackhole.outbound.Config"}},
				},
			},
			observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "freedom-out", Alive: true, Delay: 10},
				{OutboundTag: "proxy-out", Alive: true, Delay: 50},
				{OutboundTag: "blackhole-out", Alive: true, Delay: 20},
			}},
		}
		result := s.findBestAvailableNode()
		assert.Equal(t, "proxy-out", result, "应选择代理节点，排除 freedom/blackhole 等非代理协议")
	})

	t.Run("Returns empty when all alive nodes have excluded protocols", func(t *testing.T) {
		s := &Server{
			handlerClient: &mockHandlerClientCustom{
				outbounds: []*core.OutboundHandlerConfig{
					{Tag: "freedom-out", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.freedom.Config"}},
					{Tag: "dns-out", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.dns.Config"}},
				},
			},
			observatoryClient: &mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "freedom-out", Alive: true, Delay: 10},
				{OutboundTag: "dns-out", Alive: true, Delay: 20},
			}},
		}
		result := s.findBestAvailableNode()
		assert.Equal(t, "", result, "所有 alive 节点都是排除协议时应返回空")
	})
}

func TestBuildExcludedTags(t *testing.T) {
	t.Run("Nil handler client returns empty set", func(t *testing.T) {
		s := &Server{handlerClient: nil}
		tags := s.buildExcludedTags()
		assert.Empty(t, tags)
	})

	t.Run("Caches result within TTL", func(t *testing.T) {
		mock := &callCountingHandlerClient{
			outbounds: []*core.OutboundHandlerConfig{
				{Tag: "freedom-out", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.freedom.Config"}},
				{Tag: "proxy-out", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.vmess.outbound.Config"}},
			},
		}
		s := &Server{handlerClient: mock}

		tags1 := s.buildExcludedTags()
		assert.Equal(t, int32(1), mock.callCount.Load())
		assert.Contains(t, tags1, "freedom-out")
		assert.NotContains(t, tags1, "proxy-out")

		// Second call within TTL should use cache.
		tags2 := s.buildExcludedTags()
		assert.Equal(t, int32(1), mock.callCount.Load(), "TTL 内应使用缓存，不重复调用 ListOutbounds")
		assert.Contains(t, tags2, "freedom-out")
	})

	t.Run("Concurrent calls are coalesced by singleflight", func(t *testing.T) {
		mock := newGatedHandlerClient([]*core.OutboundHandlerConfig{
			{Tag: "freedom-out", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.freedom.Config"}},
			{Tag: "proxy-out", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.vmess.outbound.Config"}},
		})
		s := &Server{handlerClient: mock}

		const n = 10
		results := make([]map[string]struct{}, n)
		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			idx := i
			go func() {
				defer wg.Done()
				results[idx] = s.buildExcludedTags()
			}()
		}

		<-mock.ready
		close(mock.release)
		wg.Wait()

		count := mock.callCount.Load()
		assert.Equal(t, int32(1), count, "并发调用应被 singleflight 合并为 1 次 ListOutbounds，实际 %d 次", count)
		for i, r := range results {
			assert.Contains(t, r, "freedom-out", "goroutine %d 应包含 freedom-out", i)
			assert.NotContains(t, r, "proxy-out", "goroutine %d 不应包含 proxy-out", i)
		}
	})

	t.Run("Cache expires and refetches", func(t *testing.T) {
		mock := &callCountingHandlerClient{
			outbounds: []*core.OutboundHandlerConfig{
				{Tag: "dns-out", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.dns.Config"}},
			},
		}
		s := &Server{handlerClient: mock}

		s.buildExcludedTags()
		assert.Equal(t, int32(1), mock.callCount.Load())

		// Manually expire cache.
		s.excludedTagsMu.Lock()
		s.excludedTagsCache = cachedExcludedTags{}
		s.excludedTagsMu.Unlock()

		s.buildExcludedTags()
		assert.Equal(t, int32(2), mock.callCount.Load(), "缓存过期后应重新调用 ListOutbounds")
	})
}

// callCountingHandlerClient wraps MockHandlerClient and counts ListOutbounds calls.
type callCountingHandlerClient struct {
	MockHandlerClient
	outbounds []*core.OutboundHandlerConfig
	callCount atomic.Int32
}

func (m *callCountingHandlerClient) ListOutbounds(_ context.Context, _ *handlerpb.ListOutboundsRequest, _ ...grpc.CallOption) (*handlerpb.ListOutboundsResponse, error) {
	m.callCount.Add(1)
	return &handlerpb.ListOutboundsResponse{Outbounds: m.outbounds}, nil
}

// gatedHandlerClient blocks ListOutbounds until release is closed,
// allowing deterministic concurrency testing of singleflight.
type gatedHandlerClient struct {
	MockHandlerClient
	outbounds []*core.OutboundHandlerConfig
	ready     chan struct{}
	release   chan struct{}
	callCount atomic.Int32
}

func newGatedHandlerClient(outbounds []*core.OutboundHandlerConfig) *gatedHandlerClient {
	return &gatedHandlerClient{
		outbounds: outbounds,
		ready:     make(chan struct{}, 16),
		release:   make(chan struct{}),
	}
}

func (m *gatedHandlerClient) ListOutbounds(_ context.Context, _ *handlerpb.ListOutboundsRequest, _ ...grpc.CallOption) (*handlerpb.ListOutboundsResponse, error) {
	m.callCount.Add(1)
	select {
	case m.ready <- struct{}{}:
	default:
	}
	<-m.release
	return &handlerpb.ListOutboundsResponse{Outbounds: m.outbounds}, nil
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
		stats, err := s.getCombinedStats()
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
		stats, err := s.getCombinedStats()
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
		stats, err := s.getCombinedStats()
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
		stats, err := s.getCombinedStats()
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
		_, err := s.getCombinedStats()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "stats and sys data sources failed")
	})

	t.Run("Shutdown context cancelled — returns early", func(t *testing.T) {
		shutdownCtx, cancel := context.WithCancel(context.Background())
		cancel()
		s := &Server{
			statsClient:       &MockStatsClient{},
			observatoryClient: &mockObservatoryClient{},
			shutdownCtx:       shutdownCtx,
		}
		// With fast mocks, the goroutines may complete before the context
		// deadline is checked, so we just verify no panic occurs.
		s.getCombinedStats()
	})
}

// callCountingStatsClient wraps MockStatsClient and counts GetSysStats calls.
type callCountingStatsClient struct {
	MockStatsClient
	sysCalls atomic.Int32
}

func (c *callCountingStatsClient) GetSysStats(ctx context.Context, in *statspb.SysStatsRequest, opts ...grpc.CallOption) (*statspb.SysStatsResponse, error) {
	c.sysCalls.Add(1)
	return c.MockStatsClient.GetSysStats(ctx, in, opts...)
}

// TestHandleHealthCheckStaleFlag verifies that when the health cache has
// stale=true (set after an outbound switch), the next health check bypasses
// the cache and makes a fresh gRPC call, even if the cache timestamp is fresh.
func TestHandleHealthCheckStaleFlag(t *testing.T) {
	mock := &callCountingStatsClient{}
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

	// 1. First health check: no cache, so gRPC should be called.
	req1 := httptest.NewRequest("GET", "/api/health", nil)
	rr1 := httptest.NewRecorder()
	s.handleHealthCheck(rr1, req1)
	assert.Equal(t, http.StatusOK, rr1.Code)
	assert.Equal(t, int32(1), mock.sysCalls.Load(), "第一次健康检查应触发 gRPC 调用")

	// 2. Second health check immediately: cache is fresh (< 10s), stale=false,
	//    so gRPC should NOT be called again.
	req2 := httptest.NewRequest("GET", "/api/health", nil)
	rr2 := httptest.NewRecorder()
	s.handleHealthCheck(rr2, req2)
	assert.Equal(t, http.StatusOK, rr2.Code)
	assert.Equal(t, int32(1), mock.sysCalls.Load(), "缓存新鲜时不应重复调用 gRPC")

	// 3. Simulate outbound switch: mark cache as stale.
	s.healthMu.Lock()
	s.healthCache = cachedHealth{status: "connected", timestamp: time.Now(), stale: true}
	s.healthMu.Unlock()

	// 4. Third health check: cache exists and timestamp is fresh, BUT stale=true,
	//    so gRPC MUST be called.
	req3 := httptest.NewRequest("GET", "/api/health", nil)
	rr3 := httptest.NewRecorder()
	s.handleHealthCheck(rr3, req3)
	assert.Equal(t, http.StatusOK, rr3.Code)
	assert.Equal(t, int32(2), mock.sysCalls.Load(), "stale=true 时应跳过缓存并调用 gRPC")

	// 5. Verify the response reflects the fresh data.
	var health HealthStatus
	json.Unmarshal(rr3.Body.Bytes(), &health)
	assert.Equal(t, "connected", health.XrayAPIStatus)
}

// callCountingObservatoryClient wraps mockObservatoryClient and counts
// GetOutboundStatus calls so tests can verify caching behavior.
type callCountingObservatoryClient struct {
	mockObservatoryClient
	callCount atomic.Int32
}

func (c *callCountingObservatoryClient) GetOutboundStatus(ctx context.Context, in *observatorypb.GetOutboundStatusRequest, opts ...grpc.CallOption) (*observatorypb.GetOutboundStatusResponse, error) {
	c.callCount.Add(1)
	return c.mockObservatoryClient.GetOutboundStatus(ctx, in, opts...)
}

func TestObservatoryCache(t *testing.T) {
	t.Run("Consecutive calls within TTL use cache", func(t *testing.T) {
		mock := &callCountingObservatoryClient{
			mockObservatoryClient: mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "node-1", Alive: true, Delay: 50},
			}},
		}
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			observatoryClient: mock,
		}

		// First call should hit gRPC.
		req1 := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr1 := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr1, req1)
		assert.Equal(t, http.StatusOK, rr1.Code)
		assert.Equal(t, int32(1), mock.callCount.Load(), "第一次调用应触发 gRPC")

		// Second call within TTL should use cache.
		req2 := httptest.NewRequest("GET", "/api/outbounds-status", nil)
		rr2 := httptest.NewRecorder()
		s.handleGetOutboundsStatus(rr2, req2)
		assert.Equal(t, http.StatusOK, rr2.Code)
		assert.Equal(t, int32(1), mock.callCount.Load(), "TTL 内的第二次调用应使用缓存，不触发 gRPC")
	})

	t.Run("Call after TTL expiry triggers new gRPC call", func(t *testing.T) {
		mock := &callCountingObservatoryClient{
			mockObservatoryClient: mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "node-1", Alive: true, Delay: 50},
			}},
		}
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			observatoryClient: mock,
		}

		// First call.
		req1 := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr1 := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr1, req1)
		assert.Equal(t, int32(1), mock.callCount.Load())

		// Manually expire the cache.
		s.obsCacheMu.Lock()
		s.obsCache = cachedObservatory{}
		s.obsCacheMu.Unlock()

		// Second call should hit gRPC again.
		req2 := httptest.NewRequest("GET", "/api/outbounds-status", nil)
		rr2 := httptest.NewRecorder()
		s.handleGetOutboundsStatus(rr2, req2)
		assert.Equal(t, int32(2), mock.callCount.Load(), "缓存过期后应重新调用 gRPC")
	})

	t.Run("Outbound switch invalidates observatory cache", func(t *testing.T) {
		mock := &callCountingObservatoryClient{
			mockObservatoryClient: mockObservatoryClient{statuses: []*observatory.OutboundStatus{
				{OutboundTag: "node-1", Alive: true, Delay: 50},
			}},
		}
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			observatoryClient: mock,
		}

		// First call to populate cache.
		req1 := httptest.NewRequest("GET", "/api/current-outbound", nil)
		rr1 := httptest.NewRecorder()
		s.handleGetCurrentOutbound(rr1, req1)
		assert.Equal(t, int32(1), mock.callCount.Load())

		// Switch outbound should invalidate cache.
		body := `{"outbound_tag": "node-1"}`
		req2 := httptest.NewRequest("POST", "/api/switch-outbound", strings.NewReader(body))
		req2.Header.Set("Content-Type", "application/json")
		rr2 := httptest.NewRecorder()
		s.handleSwitchOutbound(rr2, req2)
		assert.Equal(t, http.StatusOK, rr2.Code)

		// Next call should hit gRPC again (cache was invalidated).
		req3 := httptest.NewRequest("GET", "/api/outbounds-status", nil)
		rr3 := httptest.NewRecorder()
		s.handleGetOutboundsStatus(rr3, req3)
		assert.Equal(t, int32(2), mock.callCount.Load(), "切换出站后缓存应失效，重新调用 gRPC")
	})
}

// gatedObservatoryClient blocks GetOutboundStatus until release is closed,
// allowing deterministic concurrency testing of singleflight.
// ready is a buffered channel; each goroutine sends on it before waiting for release.
type gatedObservatoryClient struct {
	mockObservatoryClient
	ready     chan struct{}
	release   chan struct{}
	callCount atomic.Int32
}

func (c *gatedObservatoryClient) GetOutboundStatus(ctx context.Context, in *observatorypb.GetOutboundStatusRequest, opts ...grpc.CallOption) (*observatorypb.GetOutboundStatusResponse, error) {
	c.callCount.Add(1)
	select {
	case c.ready <- struct{}{}:
	default:
	}
	<-c.release
	return c.mockObservatoryClient.GetOutboundStatus(ctx, in, opts...)
}

// newGatedObservatoryClient creates a client with the given statuses and
// freshly allocated ready/release channels.
func newGatedObservatoryClient(statuses []*observatory.OutboundStatus) *gatedObservatoryClient {
	return &gatedObservatoryClient{
		mockObservatoryClient: mockObservatoryClient{statuses: statuses},
		ready:                 make(chan struct{}, 16),
		release:               make(chan struct{}),
	}
}

// errorObservatoryClient always returns an error.
type errorObservatoryClient struct {
	callCount atomic.Int32
}

func (c *errorObservatoryClient) GetOutboundStatus(_ context.Context, _ *observatorypb.GetOutboundStatusRequest, _ ...grpc.CallOption) (*observatorypb.GetOutboundStatusResponse, error) {
	c.callCount.Add(1)
	return nil, fmt.Errorf("observatory unavailable")
}

func TestObservatorySingleflight(t *testing.T) {
	t.Run("Concurrent calls are coalesced into one gRPC call", func(t *testing.T) {
		mock := newGatedObservatoryClient([]*observatory.OutboundStatus{
			{OutboundTag: "node-1", Alive: true, Delay: 50},
		})
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			observatoryClient: mock,
		}

		// Fire N concurrent requests while cache is empty.
		const n = 10
		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				s.getAllOutboundStatuses()
			}()
		}

		// Wait until at least one goroutine is inside GetOutboundStatus
		// (and therefore all N are blocked on singleflight).
		<-mock.ready

		// Release all blocked goroutines.
		close(mock.release)
		wg.Wait()

		// Singleflight should coalesce concurrent callers into a single gRPC call.
		count := mock.callCount.Load()
		assert.Equal(t, int32(1), count, "并发调用应被 singleflight 合并为 1 次 gRPC 调用，实际 %d 次", count)
	})

	t.Run("Concurrent calls all receive valid results", func(t *testing.T) {
		mock := newGatedObservatoryClient([]*observatory.OutboundStatus{
			{OutboundTag: "node-1", Alive: true, Delay: 50},
			{OutboundTag: "node-2", Alive: true, Delay: 100},
		})
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			observatoryClient: mock,
		}

		const n = 10
		results := make([][]OutboundStatusData, n)
		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			idx := i
			go func() {
				defer wg.Done()
				results[idx] = s.getAllOutboundStatuses()
			}()
		}

		<-mock.ready
		close(mock.release)
		wg.Wait()

		for i, r := range results {
			assert.Len(t, r, 2, "goroutine %d 应收到 2 条状态", i)
			assert.Equal(t, "node-1", r[0].Tag)
		}
	})
}

func TestObservatoryErrorCaching(t *testing.T) {
	t.Run("Error result is cached within TTL", func(t *testing.T) {
		mock := &errorObservatoryClient{}
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			observatoryClient: mock,
		}

		// First call should hit gRPC and get error.
		r1 := s.getAllOutboundStatuses()
		assert.Equal(t, int32(1), mock.callCount.Load())
		assert.Empty(t, r1, "错误时应返回空 slice")

		// Second call within TTL should use cached empty result, not retry gRPC.
		r2 := s.getAllOutboundStatuses()
		assert.Equal(t, int32(1), mock.callCount.Load(), "TTL 内不应重试 gRPC")
		assert.Empty(t, r2, "缓存的错误结果应返回空 slice")
	})

	t.Run("Error cache expires and retries gRPC", func(t *testing.T) {
		mock := &errorObservatoryClient{}
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			observatoryClient: mock,
		}

		// First call.
		s.getAllOutboundStatuses()
		assert.Equal(t, int32(1), mock.callCount.Load())

		// Manually expire cache.
		s.obsCacheMu.Lock()
		s.obsCache = cachedObservatory{}
		s.obsCacheMu.Unlock()

		// Next call should retry gRPC.
		s.getAllOutboundStatuses()
		assert.Equal(t, int32(2), mock.callCount.Load(), "缓存过期后应重试 gRPC")
	})

	t.Run("Concurrent error calls are coalesced", func(t *testing.T) {
		errMock := newGatedErrorObservatoryClient()
		s := &Server{
			config: config.Config{
				Xray: config.XrayConfig{BalancerTag: "balancer"},
			},
			routingClient:     &MockRoutingClient{},
			observatoryClient: errMock,
		}

		const n = 10
		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func() {
				defer wg.Done()
				s.getAllOutboundStatuses()
			}()
		}

		<-errMock.ready
		close(errMock.release)
		wg.Wait()

		count := errMock.callCount.Load()
		assert.Equal(t, int32(1), count, "并发错误调用应被 singleflight 合并为 1 次，实际 %d 次", count)
	})
}

// gatedErrorObservatoryClient blocks GetOutboundStatus until release is closed,
// then returns an error. Used for deterministic singleflight concurrency tests.
type gatedErrorObservatoryClient struct {
	ready     chan struct{}
	release   chan struct{}
	callCount atomic.Int32
}

func newGatedErrorObservatoryClient() *gatedErrorObservatoryClient {
	return &gatedErrorObservatoryClient{
		ready:   make(chan struct{}, 16),
		release: make(chan struct{}),
	}
}

func (c *gatedErrorObservatoryClient) GetOutboundStatus(_ context.Context, _ *observatorypb.GetOutboundStatusRequest, _ ...grpc.CallOption) (*observatorypb.GetOutboundStatusResponse, error) {
	c.callCount.Add(1)
	select {
	case c.ready <- struct{}{}:
	default:
	}
	<-c.release
	return nil, fmt.Errorf("observatory unavailable")
}
