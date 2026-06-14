package server

import (
	"context"
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

// Server holds application state and dependencies.
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
	statsGroup        singleflight.Group
	obsGroup          singleflight.Group
	excludedTagsGroup singleflight.Group
	healthMu          sync.RWMutex
	healthCache       cachedHealth
	backgroundWg      sync.WaitGroup // tracks background goroutines for graceful shutdown
	obsCacheMu        sync.RWMutex
	obsCache          cachedObservatory
	excludedTagsMu    sync.RWMutex
	excludedTagsCache cachedExcludedTags
}

// cachedExcludedTags holds a short-lived cache for the excluded-tags set
// derived from the outbound list. Avoids duplicate ListOutbounds gRPC calls
// when /api/current-outbound (auto mode) is requested in quick succession.
type cachedExcludedTags struct {
	tags      map[string]struct{}
	timestamp time.Time
}

// cachedObservatory holds a short-lived cache for observatory gRPC responses
// to avoid duplicate calls when /api/current-outbound and /api/outbounds-status
// are requested in quick succession during page load.
type cachedObservatory struct {
	data      []OutboundStatusData
	timestamp time.Time
}

// NewServer creates a new Server instance.
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

	s.broadcaster = sse.NewBroadcaster(func() (any, error) {
		return s.getCombinedStats()
	}, computeBPS, sseUpdateInterval)

	return s
}

// RegisterHandlers registers all API routes.
func (s *Server) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", s.handleHealthCheck)
	mux.HandleFunc("HEAD /api/health", s.handleHealthCheck)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("GET /api/outbounds", s.handleGetOutbounds)
	mux.HandleFunc("GET /api/outbounds-status", s.handleGetOutboundsStatus)
	mux.HandleFunc("GET /api/current-outbound", s.handleGetCurrentOutbound)
	mux.HandleFunc("GET /api/stats-sse", s.handleStatsSSE)
	mux.HandleFunc("POST /api/switch-outbound", s.handleSwitchOutbound)
	mux.HandleFunc("GET /api/logs", s.handleGetLogs)
}

// Shutdown performs cleanup when the server is shutting down.
func (s *Server) Shutdown() {
	log.Println("正在取消广播器上下文...")
	s.shutdownCancel()
	log.Println("正在关闭 SSE 广播器...")
	s.broadcaster.Stop()
	log.Println("正在关闭 SSE 管理器...")
	s.sseManager.CloseAll()
	log.Println("正在等待后台 goroutine 退出...")
	s.backgroundWg.Wait()
}

// backgroundContext returns a context that is independent of any single
// request. It is the safe base for long-running gRPC operations that must
// survive across multiple coalesced callers (e.g. singleflight). Falls back
// to context.Background() when the server was not constructed via NewServer
// (e.g. in tests that build a *Server literal directly).
func (s *Server) backgroundContext() context.Context {
	if s.shutdownCtx != nil {
		return s.shutdownCtx
	}
	return context.Background()
}

const (
	grpcTimeout              = 4 * time.Second
	reconnectMonitorInterval = 5 * time.Second
	reconnectRecoveryTimeout = 10 * time.Second
	healthGroupKey           = "xray-health"
	combinedStatsGroupKey    = "combined-stats"
	observatoryGroupKey      = "observatory-status"
	excludedTagsGroupKey     = "excluded-tags"
)

// StartReconnectMonitor monitors gRPC connection state and reconnects on failure.
func (s *Server) StartReconnectMonitor(conn *grpc.ClientConn) {
	s.backgroundWg.Add(1)
	go func() {
		defer s.backgroundWg.Done()
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

			case connectivity.Shutdown:
				return
			}
		}
	}()
}

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
