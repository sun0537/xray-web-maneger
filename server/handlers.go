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
)

const (
	grpcTimeout          = 4 * time.Second
	sseUpdateInterval    = 2 * time.Second
	sseHeartbeatInterval = 15 * time.Second
	apiTimeout           = 5 * time.Second
)

var excludeProtocols = map[string]bool{
	"loopback":  true,
	"freedom":   true,
	"dns":       true,
	"blackhole": true,
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

type ErrorResponse struct {
	Error     string `json:"error"`
	ErrorType string `json:"error_type"`
}

type StatsData struct {
	Uplink     int64                `json:"uplink"`
	Downlink   int64                `json:"downlink"`
	Uptime     int64                `json:"uptime"`
	SysMem     uint64               `json:"sys_mem"`
	Goroutines int                  `json:"goroutines"`
	Outbounds  []OutboundStatusData `json:"outbounds"`
}

type CurrentOutboundData struct {
	Current string `json:"current"`
	Auto    bool   `json:"auto"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]string{
		"balancer_tag": s.config.Xray.BalancerTag,
	}, http.StatusOK)
}

func (s *Server) handleGetOutbounds(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()

	resp, err := s.handlerClient.ListOutbounds(ctx, &handlerpb.ListOutboundsRequest{})
	if err != nil {
		errorMsg := fmt.Sprintf("无法获取出站列表: %v", err)
		log.Printf("获取出站列表失败 [请求来源: %s]: %s", r.RemoteAddr, errorMsg)
		jsonError(w, errorMsg, http.StatusInternalServerError)
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
	if len(parts) > 2 {
		return strings.ToLower(parts[2])
	}
	return "unknown"
}

func isValidOutbound(protocolName string) bool {
	return !excludeProtocols[protocolName]
}

func (s *Server) handleGetOutboundStatus(w http.ResponseWriter, r *http.Request) {
	tag := r.URL.Query().Get("tag")
	if tag == "" {
		errorMsg := "缺少 'tag' 查询参数"
		log.Printf("获取出站状态失败 [请求来源: %s]: %s", r.RemoteAddr, errorMsg)
		jsonError(w, errorMsg, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()

	statuses := s.getAllOutboundStatuses(ctx)

	for _, st := range statuses {
		if st.Tag == tag {
			jsonResponse(w, st, http.StatusOK)
			return
		}
	}

	log.Printf("未在观测结果中找到 tag: %s (请求来源: %s)", tag, r.RemoteAddr)
	jsonResponse(w, OutboundStatusData{Tag: tag, Alive: false, Delay: 0}, http.StatusNotFound)
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
		log.Printf("获取当前出站失败 [负载均衡器: %s, 请求来源: %s]: %s", s.config.Xray.BalancerTag, r.RemoteAddr, errorMsg)
		jsonError(w, errorMsg, http.StatusBadGateway)
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
		log.Printf("切换出站失败 [请求来源: %s, 解码错误]: %s", r.RemoteAddr, errorMsg)
		jsonError(w, errorMsg, http.StatusBadRequest)
		return
	}

	if len(reqBody.OutboundTag) > 256 {
		jsonError(w, "出站标签过长", http.StatusBadRequest)
		return
	}
	if reqBody.OutboundTag != "" {
		for _, c := range reqBody.OutboundTag {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
				c == '-' || c == '_' || c == '.' || c == ':') {
				jsonError(w, "出站标签包含非法字符", http.StatusBadRequest)
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
		log.Printf("切换出站失败 [目标: %s, 负载均衡器: %s, 请求来源: %s]: %s", reqBody.OutboundTag, s.config.Xray.BalancerTag, r.RemoteAddr, errorMsg)
		jsonError(w, errorMsg, http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]string{"status": "success"}, http.StatusOK)
}

func (s *Server) handleStatsSSE(w http.ResponseWriter, r *http.Request) {
	log.Printf("SSE 连接请求 - 请求来源: %s", r.RemoteAddr)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		errorMsg := "SSE not supported (http.Flusher not available)"
		log.Printf("SSE 连接失败 [请求来源: %s]: %s", r.RemoteAddr, errorMsg)
		jsonError(w, errorMsg, http.StatusInternalServerError)
		return
	}

	conn := s.sseManager.Add(r.Context())
	if conn == nil {
		errorMsg := "Server is shutting down"
		log.Printf("SSE 连接失败 [请求来源: %s]: %s", r.RemoteAddr, errorMsg)
		jsonError(w, errorMsg, http.StatusServiceUnavailable)
		return
	}
	defer s.sseManager.Remove(conn)
	defer conn.Close()

	connCtx := conn.Context()

	statsCh := s.broadcaster.Subscribe()
	defer s.broadcaster.Unsubscribe(statsCh)

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	log.Printf("SSE 客户端已连接 - 请求来源: %s", r.RemoteAddr)

	// Push initial stats immediately so the client sees data without waiting for the first tick
	if stats, err := s.getCombinedStats(connCtx); err == nil {
		if jsonData, jerr := json.Marshal(stats); jerr == nil {
			fmt.Fprintf(w, "event: update\ndata: %s\n\n", jsonData)
			flusher.Flush()
		}
	}

	for {
		select {
		case <-connCtx.Done():
			if connCtx.Err() == context.Canceled {
				log.Printf("SSE 客户端已断开连接(正常) - 请求来源: %s", r.RemoteAddr)
			} else {
				log.Printf("SSE 连接结束 (非预期) - 请求来源: %s, 错误: %v", r.RemoteAddr, connCtx.Err())
			}
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
					log.Printf("发送统计数据失败 [请求来源: %s]: %v", r.RemoteAddr, err)
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

	if queryErr != nil {
		errorType := "permanent"
		if isTemporaryError(queryErr) {
			errorType = "temporary"
		}
		log.Printf("QueryStats 错误 [类型: %s, 上下文: inbound流量统计]: %v", errorType, queryErr)
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
		errorType := "permanent"
		if isTemporaryError(sysErr) {
			errorType = "temporary"
		}
		log.Printf("GetSysStats 错误 [类型: %s, 上下文: 系统统计]: %v", errorType, sysErr)
	} else if sysResp != nil {
		stats.Uptime = int64(sysResp.GetUptime())
		stats.SysMem = sysResp.GetSys()
		stats.Goroutines = int(sysResp.GetNumGoroutine())
	}

	stats.Outbounds = outboundStats
	return stats, nil
}

func isTemporaryError(err error) bool {
	errMsg := strings.ToLower(err.Error())
	for _, tempErr := range temporaryErrors {
		if strings.Contains(errMsg, tempErr) {
			return true
		}
	}
	return false
}

var temporaryErrors = []string{
	"timeout", "deadline exceeded", "connection refused",
	"no route to host", "connection reset", "dial tcp",
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
	health := HealthStatus{
		Status:         "healthy",
		XrayAPIStatus:  "connected",
		SSEConnections: s.sseManager.Count(),
		Uptime:         int64(time.Since(s.startTime).Seconds()),
		BalancerTag:    s.config.Xray.BalancerTag,
		Timestamp:      time.Now().Unix(),
	}

	// Probe Xray API connectivity with a real lightweight RPC call
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if _, err := s.statsClient.GetSysStats(ctx, &statspb.SysStatsRequest{}); err != nil {
		health.XrayAPIStatus = "disconnected"
		health.Status = "degraded"
	}

	jsonResponse(w, health, http.StatusOK)
}

func jsonError(w http.ResponseWriter, message string, statusCode int) {
	errorType := "unknown"
	if strings.Contains(message, "连接") || strings.Contains(message, "网络") {
		errorType = "network"
	} else if strings.Contains(message, "认证") || strings.Contains(message, "证书") || strings.Contains(message, "tls") {
		errorType = "auth"
	} else if strings.Contains(message, "参数") || strings.Contains(message, "查询") {
		errorType = "validation"
	} else if strings.Contains(message, "服务不可用") || strings.Contains(message, "关闭") {
		errorType = "service_unavailable"
	} else if strings.Contains(message, "内部") || strings.Contains(message, "服务器") {
		errorType = "server"
	}

	log.Printf("API Error [类型: %s, 状态码: %d]: %s", errorType, statusCode, message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(ErrorResponse{Error: message, ErrorType: errorType})
}
