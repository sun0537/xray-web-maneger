package server

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"time"

	"xray-web-manager/config"
	"xray-web-manager/sse"

	observatorypb "xray-web-manager/internal/xray-proto/app/observatory/command"
	handlerpb "xray-web-manager/internal/xray-proto/app/proxyman/command"
	routingpb "xray-web-manager/internal/xray-proto/app/router/command"
	statspb "xray-web-manager/internal/xray-proto/app/stats/command"

	"google.golang.org/grpc"
)

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
}

// NewServer 是一个构造函数，用于创建 Server 实例
func NewServer(cfg config.Config, conn *grpc.ClientConn, sseMgr *sse.Manager, startTime time.Time) *Server {
	s := &Server{
		config:            cfg,
		handlerClient:     handlerpb.NewHandlerServiceClient(conn),
		routingClient:     routingpb.NewRoutingServiceClient(conn),
		observatoryClient: observatorypb.NewObservatoryServiceClient(conn),
		statsClient:       statspb.NewStatsServiceClient(conn),
		sseManager:        sseMgr,
		startTime:         startTime,
	}

	s.broadcaster = sse.NewBroadcaster(func() ([]byte, error) {
		stats, err := s.getCombinedStats(context.Background())
		if err != nil {
			return nil, err
		}
		return json.Marshal(stats)
	}, sseUpdateInterval)
	s.broadcaster.Start()

	return s
}

// RegisterHandlers 负责注册所有路由
func (s *Server) RegisterHandlers(mux *http.ServeMux) {
	// API 路由
	mux.HandleFunc("/api/health", s.handleHealthCheck)
	mux.HandleFunc("/api/config", s.handleGetConfig)
	mux.HandleFunc("/api/outbounds", s.handleGetOutbounds)
	mux.HandleFunc("/api/outbound-status", s.handleGetOutboundStatus)
	mux.HandleFunc("/api/outbounds-status", s.handleGetOutboundsStatus)
	mux.HandleFunc("/api/current-outbound", s.handleGetCurrentOutbound)
	mux.HandleFunc("/api/stats-sse", s.handleStatsSSE)
	mux.HandleFunc("/api/switch-outbound", s.handleSwitchOutbound)
}

// Shutdown 封装了服务关闭时的清理逻辑
func (s *Server) Shutdown() {
	log.Println("正在关闭 SSE 广播器...")
	s.broadcaster.Stop()
	log.Println("正在关闭 SSE 管理器...")
	s.sseManager.CloseAll()
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
