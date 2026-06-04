package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	observatorypb "xray-web-manager/internal/xray-proto/app/observatory/command"
	handlerpb "xray-web-manager/internal/xray-proto/app/proxyman/command"
	routingpb "xray-web-manager/internal/xray-proto/app/router/command"
	statspb "xray-web-manager/internal/xray-proto/app/stats/command"
	serial "xray-web-manager/internal/xray-proto/common/serial"

	"xray-web-manager/middleware"
)

const (
	grpcTimeout          = 4 * time.Second
	sseUpdateInterval    = 2 * time.Second
	sseHeartbeatInterval = 15 * time.Second
	apiTimeout           = 5 * time.Second
	sseMaxLifetime       = 1 * time.Hour
)

var excludeProtocols = map[string]struct{}{
	"loopback":  {},
	"freedom":   {},
	"dns":       {},
	"blackhole": {},
}

var heartbeatBytes = []byte(": heartbeat\n\n")

type OutboundInfo struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
}

type OutboundStatusData struct {
	Tag   string `json:"tag"`
	Alive bool   `json:"alive"`
	Delay int64  `json:"delay"`
}

type HealthStatus struct {
	Status         string `json:"status"`
	XrayAPIStatus  string `json:"xray_api_status"`
	SSEConnections int    `json:"sse_connections"`
	Uptime         int64  `json:"uptime"`
	BalancerTag    string `json:"balancer_tag"`
	Timestamp      int64  `json:"timestamp"`
}

type StatsData struct {
	Uplink     int64                `json:"uplink"`
	Downlink   int64                `json:"downlink"`
	Uptime     int64                `json:"uptime"`
	SysMem     uint64               `json:"sys_mem"`
	Goroutines int                  `json:"goroutines"`
	Outbounds  []OutboundStatusData `json:"outbounds"`
	Degraded   bool                 `json:"degraded,omitempty"`
}

type CurrentOutboundData struct {
	Current string `json:"current"`
	Auto    bool   `json:"auto"`
}

type configResponse struct {
	BalancerTag string `json:"balancer_tag"`
}

type successResponse struct {
	Status string `json:"status"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, configResponse{BalancerTag: s.config.Xray.BalancerTag}, http.StatusOK)
}

func (s *Server) handleGetOutbounds(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()

	resp, err := s.handlerClient.ListOutbounds(ctx, &handlerpb.ListOutboundsRequest{})
	if err != nil {
		errorMsg := fmt.Sprintf("无法获取出站列表: %v", err)
		log.Printf("获取出站列表失败 [请求来源: %s]: %s", middleware.GetClientIP(r), errorMsg)
		jsonError(w, errorMsg, http.StatusInternalServerError, "server")
		return
	}

	outbounds := make([]OutboundInfo, 0, len(resp.Outbounds))
	for _, outbound := range resp.Outbounds {
		if outbound.Tag == "" {
			continue
		}
		protocolName := parseProtocol(outbound.ProxySettings)
		if isValidOutbound(protocolName) {
			outbounds = append(outbounds, OutboundInfo{
				Tag:      outbound.Tag,
				Protocol: protocolName,
			})
		}
	}

	jsonResponse(w, outbounds, http.StatusOK)
}

func parseProtocol(settings *serial.TypedMessage) string {
	if settings == nil || settings.Type == "" {
		return "unknown"
	}
	parts := strings.Split(settings.Type, ".")
	if len(parts) >= 3 {
		return strings.ToLower(parts[2])
	}
	return strings.ToLower(parts[len(parts)-1])
}

func isValidOutbound(protocolName string) bool {
	_, excluded := excludeProtocols[protocolName]
	return !excluded
}

func (s *Server) handleGetOutboundsStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()

	statuses := s.getAllOutboundStatuses(ctx)
	if statuses == nil {
		statuses = []OutboundStatusData{}
	}
	jsonResponse(w, statuses, http.StatusOK)
}

func (s *Server) handleGetCurrentOutbound(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()

	resp, err := s.routingClient.GetBalancerInfo(ctx, &routingpb.GetBalancerInfoRequest{
		Tag: s.config.Xray.BalancerTag,
	})
	if err != nil {
		errorMsg := fmt.Sprintf("获取负载均衡器信息失败: %v", err)
		log.Printf("获取当前出站失败 [负载均衡器: %s, 请求来源: %s]: %s", s.config.Xray.BalancerTag, middleware.GetClientIP(r), errorMsg)
		jsonError(w, errorMsg, http.StatusBadGateway, "bad_gateway")
		return
	}

	current := ""
	auto := true
	if resp.Balancer != nil && resp.Balancer.Override != nil && resp.Balancer.Override.Target != "" {
		current = resp.Balancer.Override.Target
		auto = false
	}

	jsonResponse(w, CurrentOutboundData{Current: current, Auto: auto}, http.StatusOK)
}

func (s *Server) handleSwitchOutbound(w http.ResponseWriter, r *http.Request) {
	var reqBody struct {
		OutboundTag string `json:"outbound_tag"`
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		errorMsg := fmt.Sprintf("无效的请求体: %v", err)
		log.Printf("切换出站失败 [请求来源: %s, 解码错误]: %s", middleware.GetClientIP(r), errorMsg)
		jsonError(w, errorMsg, http.StatusBadRequest, "validation")
		return
	}

	if len(reqBody.OutboundTag) > 256 {
		jsonError(w, "出站标签过长", http.StatusBadRequest, "validation")
		return
	}
	if reqBody.OutboundTag != "" {
		for _, c := range reqBody.OutboundTag {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
				c == '-' || c == '_' || c == '.' || c == ':') {
				jsonError(w, "出站标签包含非法字符", http.StatusBadRequest, "validation")
				return
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()

	_, err := s.routingClient.OverrideBalancerTarget(ctx, &routingpb.OverrideBalancerTargetRequest{
		BalancerTag: s.config.Xray.BalancerTag,
		Target:      reqBody.OutboundTag,
	})
	if err != nil {
		errorMsg := fmt.Sprintf("切换出站失败: %v", err)
		log.Printf("切换出站失败 [目标: %s, 负载均衡器: %s, 请求来源: %s]: %s", reqBody.OutboundTag, s.config.Xray.BalancerTag, middleware.GetClientIP(r), errorMsg)
		jsonError(w, errorMsg, http.StatusInternalServerError, "server")
		return
	}

	jsonResponse(w, successResponse{Status: "success"}, http.StatusOK)
}

func (s *Server) handleStatsSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		errorMsg := "SSE not supported (http.Flusher not available)"
		log.Printf("SSE 连接失败 [请求来源: %s]: %s", middleware.GetClientIP(r), errorMsg)
		jsonError(w, errorMsg, http.StatusInternalServerError, "server")
		return
	}

	conn := s.sseManager.Add(r.Context())
	if conn == nil {
		errorMsg := "Server is shutting down"
		log.Printf("SSE 连接被拒绝 [请求来源: %s]: %s", middleware.GetClientIP(r), errorMsg)
		jsonError(w, errorMsg, http.StatusServiceUnavailable, "service_unavailable")
		return
	}
	defer conn.Close()

	ip := middleware.GetClientIP(r)
	log.Printf("SSE 连接已建立 [请求来源: %s]，当前连接数: %d", ip, s.sseManager.Count())
	defer func() {
		s.sseManager.Remove(conn)
		log.Printf("SSE 连接已断开 [请求来源: %s]，当前连接数: %d", ip, s.sseManager.Count())
	}()

	connCtx := conn.Context()

	statsCh := s.broadcaster.Subscribe()
	defer s.broadcaster.Unsubscribe(statsCh)

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	// Force reconnection after max lifetime to ensure auth is re-validated
	lifetime := time.NewTimer(sseMaxLifetime)
	defer lifetime.Stop()

	// Push initial stats immediately so the client sees data without waiting for the first tick
	stats, statsErr := s.getCombinedStats(connCtx)
	if statsErr != nil {
		log.Printf("SSE 初始数据获取失败，发送降级数据: %v", statsErr)
		stats = StatsData{Outbounds: []OutboundStatusData{}}
	}
	if jsonData, jerr := json.Marshal(stats); jerr == nil {
		fmt.Fprintf(w, "event: update\ndata: %s\n\n", jsonData)
		flusher.Flush()
	}

	for {
		select {
		case <-connCtx.Done():
			if connCtx.Err() != context.Canceled {
				log.Printf("SSE 连接结束 (非预期) - 请求来源: %s, 错误: %v", ip, connCtx.Err())
			}
			return

		case <-lifetime.C:
			log.Printf("SSE 连接达到最大存活时间 (%v)，强制重连 [请求来源: %s]", sseMaxLifetime, ip)
			return

		case <-heartbeat.C:
			if _, err := w.Write(heartbeatBytes); err != nil {
				return
			}
			flusher.Flush()

		case jsonData, ok := <-statsCh:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "event: update\ndata: %s\n\n", jsonData); err != nil {
				if connCtx.Err() == nil {
					log.Printf("发送统计数据失败 [请求来源: %s]: %v", ip, err)
				}
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) getCombinedStats(ctx context.Context) (StatsData, error) {
	stats := StatsData{}

	gCtx, cancel := context.WithTimeout(ctx, grpcTimeout)
	defer cancel()

	var (
		wg            sync.WaitGroup
		queryResp     *statspb.QueryStatsResponse
		sysResp       *statspb.SysStatsResponse
		outboundStats []OutboundStatusData
		queryErr      error
		sysErr        error
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		queryResp, queryErr = s.statsClient.QueryStats(gCtx, &statspb.QueryStatsRequest{Pattern: "inbound", Reset_: false})
	}()
	go func() {
		defer wg.Done()
		sysResp, sysErr = s.statsClient.GetSysStats(gCtx, &statspb.SysStatsRequest{})
	}()
	go func() {
		defer wg.Done()
		outboundStats = s.getAllOutboundStatuses(gCtx)
	}()
	wg.Wait()

	failedSources := 0

	if queryErr != nil {
		log.Printf("QueryStats 错误 [inbound流量统计]: %v", queryErr)
		stats.Degraded = true
		failedSources++
	} else if queryResp != nil {
		for _, stat := range queryResp.Stat {
			if strings.HasSuffix(stat.Name, ">>>traffic>>>uplink") {
				stats.Uplink += stat.Value
			} else if strings.HasSuffix(stat.Name, ">>>traffic>>>downlink") {
				stats.Downlink += stat.Value
			}
		}
	}

	if sysErr != nil {
		log.Printf("GetSysStats 错误 [系统统计]: %v", sysErr)
		stats.Degraded = true
		failedSources++
	} else if sysResp != nil {
		stats.Uptime = int64(sysResp.GetUptime())
		stats.SysMem = sysResp.GetSys()
		stats.Goroutines = int(sysResp.GetNumGoroutine())
	}

	if outboundStats == nil {
		failedSources++
	}

	stats.Outbounds = outboundStats

	if failedSources >= 3 {
		return stats, fmt.Errorf("all data sources failed")
	}
	return stats, nil
}

func (s *Server) getAllOutboundStatuses(ctx context.Context) []OutboundStatusData {
	resp, err := s.observatoryClient.GetOutboundStatus(ctx, &observatorypb.GetOutboundStatusRequest{})
	if err != nil {
		log.Printf("警告: GetOutboundStatus 失败: %v", err)
		return nil
	}

	if resp.Status == nil || resp.Status.Status == nil {
		return nil
	}

	result := make([]OutboundStatusData, 0, len(resp.Status.Status))
	for _, status := range resp.Status.Status {
		result = append(result, OutboundStatusData{
			Tag:   status.OutboundTag,
			Alive: status.Alive,
			Delay: status.Delay,
		})
	}
	return result
}

func jsonResponse(w http.ResponseWriter, data any, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("JSON 写入失败: %v", err)
	}
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	s.healthMu.RLock()
	cached := s.healthCache
	s.healthMu.RUnlock()

	xrayStatus := cached.status
	if time.Since(cached.timestamp) > 10*time.Second {
		result, err, _ := s.healthGroup.Do("xray-health", func() (interface{}, error) {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if _, err := s.statsClient.GetSysStats(ctx, &statspb.SysStatsRequest{}); err != nil {
				return "disconnected", err
			}
			return "connected", nil
		})
		if err != nil {
			log.Printf("健康检查失败: %v", err)
		}
		if status, ok := result.(string); ok {
			xrayStatus = status
			cacheTime := time.Now()
			if err != nil {
				// On failure, backdate the timestamp by 7s so the cache expires in ~3s,
				// allowing a quick retry without flooding the gRPC backend.
				cacheTime = cacheTime.Add(-7 * time.Second)
			}
			s.healthMu.Lock()
			s.healthCache = cachedHealth{status: xrayStatus, timestamp: cacheTime}
			s.healthMu.Unlock()
		}
		// If type assertion fails (shouldn't happen), don't update the cache —
		// keep the old cached value as-is to avoid stamping a stale timestamp.
	}

	health := HealthStatus{
		Status:         "healthy",
		XrayAPIStatus:  xrayStatus,
		SSEConnections: s.sseManager.Count(),
		Uptime:         int64(time.Since(s.startTime).Seconds()),
		BalancerTag:    s.config.Xray.BalancerTag,
		Timestamp:      time.Now().Unix(),
	}
	if xrayStatus != "connected" {
		health.Status = "degraded"
	}

	jsonResponse(w, health, http.StatusOK)
}

func jsonError(w http.ResponseWriter, message string, statusCode int, errorType string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(middleware.ErrorResponse{Error: message, ErrorType: errorType}); err != nil {
		log.Printf("JSON error response write failed: %v", err)
	}
}
