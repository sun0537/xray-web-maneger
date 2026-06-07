package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

type MockRoutingClient struct {
	routingpb.UnimplementedRoutingServiceServer
}

func (m *MockRoutingClient) SubscribeRoutingStats(ctx context.Context, in *routingpb.SubscribeRoutingStatsRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[routingpb.RoutingContext], error) {
	return nil, nil
}
func (m *MockRoutingClient) TestRoute(ctx context.Context, in *routingpb.TestRouteRequest, opts ...grpc.CallOption) (*routingpb.RoutingContext, error) {
	return nil, nil
}
func (m *MockRoutingClient) GetBalancerInfo(ctx context.Context, in *routingpb.GetBalancerInfoRequest, opts ...grpc.CallOption) (*routingpb.GetBalancerInfoResponse, error) {
	return &routingpb.GetBalancerInfoResponse{
		Balancer: &routingpb.BalancerMsg{},
	}, nil
}
func (m *MockRoutingClient) OverrideBalancerTarget(ctx context.Context, in *routingpb.OverrideBalancerTargetRequest, opts ...grpc.CallOption) (*routingpb.OverrideBalancerTargetResponse, error) {
	return nil, nil
}
func (m *MockRoutingClient) AddRule(ctx context.Context, in *routingpb.AddRuleRequest, opts ...grpc.CallOption) (*routingpb.AddRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClient) RemoveRule(ctx context.Context, in *routingpb.RemoveRuleRequest, opts ...grpc.CallOption) (*routingpb.RemoveRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClient) ListRule(ctx context.Context, in *routingpb.ListRuleRequest, opts ...grpc.CallOption) (*routingpb.ListRuleResponse, error) {
	return nil, nil
}

type MockObservatoryClient struct {
	observatorypb.UnimplementedObservatoryServiceServer
}

func (m *MockObservatoryClient) GetOutboundStatus(ctx context.Context, in *observatorypb.GetOutboundStatusRequest, opts ...grpc.CallOption) (*observatorypb.GetOutboundStatusResponse, error) {
	return &observatorypb.GetOutboundStatusResponse{
		Status: &observatory.ObservationResult{
			Status: []*observatory.OutboundStatus{},
		},
	}, nil
}

type MockObservatoryClientWithStatus struct {
	observatorypb.UnimplementedObservatoryServiceServer
}

func (m *MockObservatoryClientWithStatus) GetOutboundStatus(ctx context.Context, in *observatorypb.GetOutboundStatusRequest, opts ...grpc.CallOption) (*observatorypb.GetOutboundStatusResponse, error) {
	return &observatorypb.GetOutboundStatusResponse{
		Status: &observatory.ObservationResult{
			Status: []*observatory.OutboundStatus{
				{OutboundTag: "node-1", Alive: true, Delay: 50},
				{OutboundTag: "node-2", Alive: true, Delay: 120},
				{OutboundTag: "node-3", Alive: false, Delay: 0},
			},
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
		config:            config.Config{},
		observatoryClient: &MockObservatoryClientWithStatus{},
		startTime:         time.Time{},
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
		stats := StatsData{
			Uplink: 1024, Downlink: 2048, Uptime: 100,
			SysMem: 50000000, Goroutines: 10, Outbounds: []OutboundStatusData{},
		}
		b, err := json.Marshal(stats)
		return sse.RawEvent{JSON: b}, err
	}
	broadcaster := sse.NewBroadcaster(fetchFn, nil, 100*time.Millisecond)
	defer broadcaster.Stop()

	s := &Server{
		config:            config.Config{},
		statsClient:       &MockStatsClient{},
		sseManager:        sseMgr,
		handlerClient:     &MockHandlerClient{},
		routingClient:     &MockRoutingClient{},
		observatoryClient: &MockObservatoryClient{},
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

type MockRoutingClientSuccess struct {
	routingpb.UnimplementedRoutingServiceServer
}

func (m *MockRoutingClientSuccess) SubscribeRoutingStats(ctx context.Context, in *routingpb.SubscribeRoutingStatsRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[routingpb.RoutingContext], error) {
	return nil, nil
}
func (m *MockRoutingClientSuccess) TestRoute(ctx context.Context, in *routingpb.TestRouteRequest, opts ...grpc.CallOption) (*routingpb.RoutingContext, error) {
	return nil, nil
}
func (m *MockRoutingClientSuccess) OverrideBalancerTarget(ctx context.Context, in *routingpb.OverrideBalancerTargetRequest, opts ...grpc.CallOption) (*routingpb.OverrideBalancerTargetResponse, error) {
	return &routingpb.OverrideBalancerTargetResponse{}, nil
}
func (m *MockRoutingClientSuccess) GetBalancerInfo(ctx context.Context, in *routingpb.GetBalancerInfoRequest, opts ...grpc.CallOption) (*routingpb.GetBalancerInfoResponse, error) {
	return nil, nil
}
func (m *MockRoutingClientSuccess) AddRule(ctx context.Context, in *routingpb.AddRuleRequest, opts ...grpc.CallOption) (*routingpb.AddRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClientSuccess) RemoveRule(ctx context.Context, in *routingpb.RemoveRuleRequest, opts ...grpc.CallOption) (*routingpb.RemoveRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClientSuccess) ListRule(ctx context.Context, in *routingpb.ListRuleRequest, opts ...grpc.CallOption) (*routingpb.ListRuleResponse, error) {
	return nil, nil
}

func TestHandleSwitchOutbound(t *testing.T) {
	s := &Server{
		config: config.Config{
			Xray: config.XrayConfig{BalancerTag: "balancer"},
		},
		routingClient: &MockRoutingClientSuccess{},
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

type MockRoutingClientWithInfo struct {
	routingpb.UnimplementedRoutingServiceServer
	override *routingpb.OverrideInfo
}

func (m *MockRoutingClientWithInfo) GetBalancerInfo(ctx context.Context, in *routingpb.GetBalancerInfoRequest, opts ...grpc.CallOption) (*routingpb.GetBalancerInfoResponse, error) {
	resp := &routingpb.GetBalancerInfoResponse{}
	if m.override != nil {
		resp.Balancer = &routingpb.BalancerMsg{Override: m.override}
	} else {
		resp.Balancer = &routingpb.BalancerMsg{}
	}
	return resp, nil
}

func (m *MockRoutingClientWithInfo) OverrideBalancerTarget(ctx context.Context, in *routingpb.OverrideBalancerTargetRequest, opts ...grpc.CallOption) (*routingpb.OverrideBalancerTargetResponse, error) {
	return &routingpb.OverrideBalancerTargetResponse{}, nil
}

func (m *MockRoutingClientWithInfo) SubscribeRoutingStats(ctx context.Context, in *routingpb.SubscribeRoutingStatsRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[routingpb.RoutingContext], error) {
	return nil, nil
}
func (m *MockRoutingClientWithInfo) TestRoute(ctx context.Context, in *routingpb.TestRouteRequest, opts ...grpc.CallOption) (*routingpb.RoutingContext, error) {
	return nil, nil
}
func (m *MockRoutingClientWithInfo) AddRule(ctx context.Context, in *routingpb.AddRuleRequest, opts ...grpc.CallOption) (*routingpb.AddRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClientWithInfo) RemoveRule(ctx context.Context, in *routingpb.RemoveRuleRequest, opts ...grpc.CallOption) (*routingpb.RemoveRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClientWithInfo) ListRule(ctx context.Context, in *routingpb.ListRuleRequest, opts ...grpc.CallOption) (*routingpb.ListRuleResponse, error) {
	return nil, nil
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

type MockRoutingClientError struct {
	routingpb.UnimplementedRoutingServiceServer
}

func (m *MockRoutingClientError) GetBalancerInfo(ctx context.Context, in *routingpb.GetBalancerInfoRequest, opts ...grpc.CallOption) (*routingpb.GetBalancerInfoResponse, error) {
	return nil, assert.AnError
}
func (m *MockRoutingClientError) OverrideBalancerTarget(ctx context.Context, in *routingpb.OverrideBalancerTargetRequest, opts ...grpc.CallOption) (*routingpb.OverrideBalancerTargetResponse, error) {
	return nil, nil
}
func (m *MockRoutingClientError) SubscribeRoutingStats(ctx context.Context, in *routingpb.SubscribeRoutingStatsRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[routingpb.RoutingContext], error) {
	return nil, nil
}
func (m *MockRoutingClientError) TestRoute(ctx context.Context, in *routingpb.TestRouteRequest, opts ...grpc.CallOption) (*routingpb.RoutingContext, error) {
	return nil, nil
}
func (m *MockRoutingClientError) AddRule(ctx context.Context, in *routingpb.AddRuleRequest, opts ...grpc.CallOption) (*routingpb.AddRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClientError) RemoveRule(ctx context.Context, in *routingpb.RemoveRuleRequest, opts ...grpc.CallOption) (*routingpb.RemoveRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClientError) ListRule(ctx context.Context, in *routingpb.ListRuleRequest, opts ...grpc.CallOption) (*routingpb.ListRuleResponse, error) {
	return nil, nil
}
