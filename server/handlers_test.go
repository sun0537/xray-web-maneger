package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	// 导入 Xray gRPC 存根
	observatorypb "xray-web-manager/internal/xray-proto/app/observatory/command"
	handlerpb "xray-web-manager/internal/xray-proto/app/proxyman/command"
	routingpb "xray-web-manager/internal/xray-proto/app/router/command"
	statspb "xray-web-manager/internal/xray-proto/app/stats/command"
	"xray-web-manager/internal/xray-proto/common/serial"
	"xray-web-manager/internal/xray-proto/core"

	// 导入我们的 config 和 sse 包
	"xray-web-manager/config"
	"xray-web-manager/sse"
)

// ===== 1. 定义 Mock 客户端 =====

type MockHandlerClient struct {
}

// 实现 ListOutbounds 方法
func (m *MockHandlerClient) ListOutbounds(ctx context.Context, in *handlerpb.ListOutboundsRequest, opts ...grpc.CallOption) (*handlerpb.ListOutboundsResponse, error) {
	// “假”数据。
	return &handlerpb.ListOutboundsResponse{
		Outbounds: []*core.OutboundHandlerConfig{
			{
				Tag: "node-1",
				ProxySettings: &serial.TypedMessage{
					Type: "xray.proxy.vmess.outbound.Config",
				},
			},
			{
				Tag: "node-2",
				ProxySettings: &serial.TypedMessage{
					Type: "xray.proxy.vless.outbound.Config",
				},
			},
			{
				Tag: "node-3",
				ProxySettings: &serial.TypedMessage{
					Type: "xray.proxy.shadowsocks.outbound.Config",
				},
			},
			{
				Tag: "node-4",
				ProxySettings: &serial.TypedMessage{
					Type: "xray.proxy.wireguard.outbound.Config",
				},
			},
			{
				Tag: "node-5",
				ProxySettings: &serial.TypedMessage{
					Type: "xray.proxy.trojan.outbound.Config",
				},
			},
			{
				Tag: "node-6",
				ProxySettings: &serial.TypedMessage{
					Type: "xray.proxy.socks.outbound.Config",
				},
			},
			{
				Tag: "node-7",
				ProxySettings: &serial.TypedMessage{
					Type: "xray.proxy.blackhole.outbound.Config",
				},
			},
			{
				Tag: "direct",
				ProxySettings: &serial.TypedMessage{
					Type: "xray.proxy.freedom.Config",
				},
			},
			{
				Tag: "Loopback",
				ProxySettings: &serial.TypedMessage{
					Type: "xray.proxy.loopback.Config",
				},
			},
			{
				Tag: "dns",
				ProxySettings: &serial.TypedMessage{
					Type: "xray.proxy.dns.Config",
				},
			},
		},
	}, nil
}

// (在 server.go 中没有使用这些方法，所以可以暂时只实现 ListOutbounds)
// (但为了满足接口，必须把所有方法都加上)

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

// (为 StatsServiceClient 创建 Mock)
type MockStatsClient struct{}

// (实现 getCombinedStats 使用的方法)
func (m *MockStatsClient) QueryStats(ctx context.Context, in *statspb.QueryStatsRequest, opts ...grpc.CallOption) (*statspb.QueryStatsResponse, error) {
	// 返回假的流量数据
	return &statspb.QueryStatsResponse{
		Stat: []*statspb.Stat{
			{Name: "inbound>>>test>>>traffic>>>uplink", Value: 1024},
			{Name: "inbound>>>test>>>traffic>>>downlink", Value: 2048},
		},
	}, nil
}

func (m *MockStatsClient) GetSysStats(ctx context.Context, in *statspb.SysStatsRequest, opts ...grpc.CallOption) (*statspb.SysStatsResponse, error) {
	// 返回假的系统数据
	return &statspb.SysStatsResponse{
		Uptime:       100,
		Sys:          50000000,
		NumGoroutine: 10,
	}, nil
}

// (实现 GetStats，满足接口)
func (m *MockStatsClient) GetStats(ctx context.Context, in *statspb.GetStatsRequest, opts ...grpc.CallOption) (*statspb.GetStatsResponse, error) {
	return nil, nil
}
func (m *MockStatsClient) GetStatsOnline(ctx context.Context, in *statspb.GetStatsRequest, opts ...grpc.CallOption) (*statspb.GetStatsResponse, error) {
	return nil, nil
}
func (m *MockStatsClient) GetStatsOnlineIpList(ctx context.Context, in *statspb.GetStatsRequest, opts ...grpc.CallOption) (*statspb.GetStatsOnlineIpListResponse, error) {
	return nil, nil
}

// (为其他客户端创建空 Mock 存根，以便 NewServer 正常工作)
type MockRoutingClient struct {
	routingpb.UnimplementedRoutingServiceServer
}

func (m *MockRoutingClient) GetBalancerInfo(ctx context.Context, in *routingpb.GetBalancerInfoRequest, opts ...grpc.CallOption) (*routingpb.GetBalancerInfoResponse, error) {
	return nil, nil
}
func (m *MockRoutingClient) OverrideBalancerTarget(ctx context.Context, in *routingpb.OverrideBalancerTargetRequest, opts ...grpc.CallOption) (*routingpb.OverrideBalancerTargetResponse, error) {
	return nil, nil
}

// (注意：RoutingService 客户端接口还有 AddRule, RemoveRule, TestRoute, SubscribeRoutingStats)
// (为简洁起见，如果 NewServer 接受 nil，我们可以跳过它们，但注入一个完整的 Mock 更安全)
func (m *MockRoutingClient) AddRule(ctx context.Context, in *routingpb.AddRuleRequest, opts ...grpc.CallOption) (*routingpb.AddRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClient) RemoveRule(ctx context.Context, in *routingpb.RemoveRuleRequest, opts ...grpc.CallOption) (*routingpb.RemoveRuleResponse, error) {
	return nil, nil
}
func (m *MockRoutingClient) TestRoute(ctx context.Context, in *routingpb.TestRouteRequest, opts ...grpc.CallOption) (*routingpb.RoutingContext, error) {
	return nil, nil
}
func (m *MockRoutingClient) SubscribeRoutingStats(ctx context.Context, in *routingpb.SubscribeRoutingStatsRequest, opts ...grpc.CallOption) (routingpb.RoutingService_SubscribeRoutingStatsClient, error) {
	return nil, nil
}

type MockObservatoryClient struct {
	observatorypb.UnimplementedObservatoryServiceServer
}

func (m *MockObservatoryClient) GetOutboundStatus(ctx context.Context, in *observatorypb.GetOutboundStatusRequest, opts ...grpc.CallOption) (*observatorypb.GetOutboundStatusResponse, error) {
	return nil, nil
}

// ===== 2. 编写测试函数 =====

func TestHandleGetOutbounds(t *testing.T) {
	// --- A. 准备 (Arrange) ---

	// 1. 创建我们的 Mock 客户端
	mockHandlerClient := &MockHandlerClient{}

	// 2. 创建一个 Server 实例，并注入 Mock
	s := &Server{
		config:        config.Config{},
		handlerClient: mockHandlerClient, // <-- 注入 Mock
		// ... 其他 client 为 nil
	}

	// 3. 创建一个假的 HTTP 请求
	req, err := http.NewRequest("GET", "/api/outbounds", nil)
	if err != nil {
		t.Fatal(err)
	}

	// 4. 创建一个 "Response Recorder"
	rr := httptest.NewRecorder()

	// --- B. 执行 (Act) ---
	s.handleGetOutbounds(rr, req)

	// --- C. 断言 (Assert) ---
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

// ===== 3.  SSE 测试函数 =====

func TestHandleStatsSSE(t *testing.T) {
	// --- A. 准备 (Arrange) ---

	// 1. 创建 Mock 和 真实的 SSE Manager
	mockStatsClient := &MockStatsClient{}
	sseMgr := sse.NewManager() //

	// 2. 创建 Server 实例并注入
	s := &Server{
		config:      config.Config{},
		statsClient: mockStatsClient, // <-- 注入 Mock Stats
		sseManager:  sseMgr,
		// (注入其他空 Mock 以防止 nil panic)
		handlerClient:     &MockHandlerClient{},
		routingClient:     &MockRoutingClient{},
		observatoryClient: &MockObservatoryClient{},
	}

	// 3. 创建一个 Mux 并注册 *所有* handler
	// (这比只注册一个 handler 更接近真实情况)
	mux := http.NewServeMux()
	s.RegisterHandlers(mux) //

	// 4. (关键) 创建一个 httptest.Server，它支持 Flusher
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// --- B. 执行 (Act) ---

	// 5. 创建一个 HTTP 客户端连接到测试服务器
	// (设置一个5秒的超时，防止测试卡住)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/stats-sse", nil)
	resp, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 6. 准备读取 SSE 流
	scanner := bufio.NewScanner(resp.Body)
	defer resp.Body.Close()

	// --- C. 断言 (Assert) ---

	// handleStatsSSE 会立即发送第一条数据

	// 1. 读取 "event: update"
	assert.True(t, scanner.Scan(), "服务器应首先发送 event: update")
	assert.Equal(t, "event: update", scanner.Text())

	// 2. 读取 "data: {...}"
	assert.True(t, scanner.Scan(), "服务器应在 event 后发送 data")
	dataLine := scanner.Text()
	assert.True(t, strings.HasPrefix(dataLine, "data: {"), "数据行应以 'data: {' 开头")

	// 3. 解析 JSON 数据
	var stats map[string]interface{}
	err = json.Unmarshal([]byte(strings.TrimPrefix(dataLine, "data: ")), &stats)
	assert.NoError(t, err, "解析 SSE JSON 数据失败")

	// 4. 验证数据是否与 Mock 中定义的一致
	// (注意: JSON 解析数字时默认使用 float64)
	assert.Equal(t, float64(1024), stats["uplink"])
	assert.Equal(t, float64(2048), stats["downlink"])
	assert.Equal(t, float64(100), stats["uptime"])
	assert.Equal(t, float64(10), stats["goroutines"])

	// 5. 读取空行 (SSE 消息结束)
	assert.True(t, scanner.Scan(), "服务器应在 data 后发送空行")
	assert.Equal(t, "", scanner.Text())

	// 6. 测试 Ticker (在 handlers.go 中是 2 秒)
	// (我们将再次读取，确保 Ticker 触发了第二次推送)

	// 7. 读取第二次 "event: update"
	assert.True(t, scanner.Scan(), "服务器应在 2 秒后发送第二个 event")
	assert.Equal(t, "event: update", scanner.Text())
	assert.True(t, scanner.Scan(), "服务器应发送第二个 data") // (读取 data)
	assert.True(t, scanner.Scan(), "服务器应发送第二个空行")    // (读取空行)

	// 8. (测试完成) 关闭连接 (通过取消 context)
	// (cancel() 会导致 resp.Body.Close()，触发 ctx.Done()，
	// 从而退出 handleStatsSSE 中的循环)
}
