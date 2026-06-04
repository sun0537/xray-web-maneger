package server

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"xray-web-manager/config"
	"xray-web-manager/sse"

	observatorypb "xray-web-manager/internal/xray-proto/app/observatory/command"
	handlerpb "xray-web-manager/internal/xray-proto/app/proxyman/command"
	routingpb "xray-web-manager/internal/xray-proto/app/router/command"
	statspb "xray-web-manager/internal/xray-proto/app/stats/command"
)

type cachedHealth struct {
	status    string
	timestamp time.Time
}

// Server 结构体现在持有配置、SSE 管理器和启动时间
type Server struct {
	config            config.Config
	handlerClient     handlerpb.HandlerServiceClient
	routingClient     routingpb.RoutingServiceClient
	observatoryClient observatorypb.ObservatoryServiceClient
	statsClient       statspb.StatsServiceClient
	sseManager        *sse.Manager
	broadcaster       *sse.Broadcaster
	startTime         time.Time
	shutdownCtx       context.Context
	shutdownCancel    context.CancelFunc
	healthGroup       singleflight.Group
	healthMu          sync.RWMutex
	healthCache       cachedHealth
}

// NewServer 是一个构造函数，用于创建 Server 实例
func NewServer(cfg config.Config, conn *grpc.ClientConn, sseMgr *sse.Manager, startTime time.Time) *Server {
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	s := &Server{
		config:            cfg,
		handlerClient:     handlerpb.NewHandlerServiceClient(conn),
		routingClient:     routingpb.NewRoutingServiceClient(conn),
		observatoryClient: observatorypb.NewObservatoryServiceClient(conn),
		statsClient:       statspb.NewStatsServiceClient(conn),
		sseManager:        sseMgr,
		startTime:         startTime,
		shutdownCtx:       shutdownCtx,
		shutdownCancel:    shutdownCancel,
	}

	s.broadcaster = sse.NewBroadcaster(func() ([]byte, error) {
		stats, err := s.getCombinedStats(s.shutdownCtx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(stats)
	}, sseUpdateInterval)

	return s
}

// RegisterHandlers 负责注册所有路由
func (s *Server) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", s.handleHealthCheck)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("GET /api/outbounds", s.handleGetOutbounds)
	mux.HandleFunc("GET /api/outbounds-status", s.handleGetOutboundsStatus)
	mux.HandleFunc("GET /api/current-outbound", s.handleGetCurrentOutbound)
	mux.HandleFunc("GET /api/stats-sse", s.handleStatsSSE)
	mux.HandleFunc("POST /api/switch-outbound", s.handleSwitchOutbound)
}

// Shutdown 封装了服务关闭时的清理逻辑
func (s *Server) Shutdown() {
	log.Println("正在取消广播器上下文...")
	s.shutdownCancel()
	log.Println("正在关闭 SSE 广播器...")
	s.broadcaster.Stop()
	log.Println("正在关闭 SSE 管理器...")
	s.sseManager.CloseAll()
}

// reconnectMonitorInterval is how often we check gRPC connection health.
const reconnectMonitorInterval = 5 * time.Second

// reconnectRecoveryTimeout is the maximum time to wait for a reconnection
// to reach Ready state after forcing conn.Connect().
const reconnectRecoveryTimeout = 10 * time.Second

// StartReconnectMonitor starts a background goroutine that monitors the
// gRPC connection state. If the connection enters TRANSIENT_FAILURE, it
// forces a reconnection attempt. This handles the case where Xray is
// restarted while this service is running.
// The monitor stops when the server's shutdown context is cancelled.
func (s *Server) StartReconnectMonitor(conn *grpc.ClientConn) {
	go func() {
		ticker := time.NewTicker(reconnectMonitorInterval)
		defer ticker.Stop()

		wasConnected := true

		for {
			select {
			case <-s.shutdownCtx.Done():
				return
			case <-ticker.C:
			}

			switch conn.GetState() {
			case connectivity.Ready:
				if !wasConnected {
					log.Println("gRPC 连接已恢复")
					wasConnected = true
				}

			case connectivity.TransientFailure:
				if wasConnected {
					log.Printf("gRPC 连接断开 (状态: %s)，尝试重连...", connectivity.TransientFailure)
					wasConnected = false
				}

				conn.Connect()
				if s.waitForReconnect(conn) {
					log.Println("gRPC 连接重连成功")
					wasConnected = true
				} else {
					log.Printf("gRPC 重连超时 (状态: %s)，将在 %v 后重试", conn.GetState(), reconnectMonitorInterval)
				}

			case connectivity.Idle, connectivity.Connecting:
				// Idle: unused connection, gRPC will auto-wake on next RPC.
				// Connecting: in-flight transition, check again next tick.

			case connectivity.Shutdown:
				// Connection is permanently closed; nothing to monitor.
				return
			}
		}
	}()
}

// waitForReconnect blocks until conn reaches Ready, the server is shutting
// down, or reconnectRecoveryTimeout elapses. Returns true if Ready.
func (s *Server) waitForReconnect(conn *grpc.ClientConn) bool {
	ctx, cancel := context.WithTimeout(s.shutdownCtx, reconnectRecoveryTimeout)
	defer cancel()

	for conn.GetState() != connectivity.Ready {
		if !conn.WaitForStateChange(ctx, conn.GetState()) {
			return false
		}
	}
	return true
}

// RegisterFrontend 负责注册静态文件服务
func RegisterFrontend(mux *http.ServeMux, devMode bool, frontendFS embed.FS) {
	if devMode {
		log.Println("开发模式：使用外部 frontend/ 文件夹")
		mux.Handle("/", http.FileServer(http.Dir("frontend")))
	} else {
		log.Println("生产模式：使用嵌入的前端文件")
		subFS, err := fs.Sub(frontendFS, "frontend")
		if err != nil {
			log.Fatalf("无法创建子文件系统: %v", err)
		}
		mux.Handle("/", http.FileServer(http.FS(subFS)))
	}
}
