package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
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

type OutboundInfo struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
}

type OutboundStatusData struct {
	Tag   string `json:"tag"`
	Alive bool   `json:"alive"`
	Delay int64  `json:"delay"`
}

type StatsData struct {
	Uplink     int64                `json:"uplink"`
	Downlink   int64                `json:"downlink"`
	Uptime     int64                `json:"uptime"`
	SysMem     uint64               `json:"sys_mem"`
	Goroutines int                  `json:"goroutines"`
	Outbounds  []OutboundStatusData `json:"outbounds,omitempty"`
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
		jsonError(w, "无法获取出站列表: "+err.Error(), http.StatusInternalServerError)
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
		return parts[2]
	}
	return "unknown"
}

func isValidOutbound(protocolName string) bool {
	return !excludeProtocols[strings.ToLower(protocolName)]
}

func (s *Server) handleGetOutboundStatus(w http.ResponseWriter, r *http.Request) {
	tag := r.URL.Query().Get("tag")
	if tag == "" {
		jsonError(w, "缺少 'tag' 查询参数", http.StatusBadRequest)
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

	log.Printf("未在观测结果中找到 tag: %s", tag)
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
		jsonResponse(w, CurrentOutboundData{Current: "", Auto: true}, http.StatusOK)
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
	if r.Method != http.MethodPost {
		jsonError(w, "仅支持 POST 方法", http.StatusMethodNotAllowed)
		return
	}

	var reqBody struct {
		OutboundTag string `json:"outbound_tag"`
	}

	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		jsonError(w, "无效的请求体: "+err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()

	_, err := s.routingClient.OverrideBalancerTarget(ctx, &routingpb.OverrideBalancerTargetRequest{
		BalancerTag: s.config.Xray.BalancerTag,
		Target:      reqBody.OutboundTag,
	})
	if err != nil {
		jsonError(w, "切换出站失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]string{"status": "success"}, http.StatusOK)
}

func (s *Server) handleStatsSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, "SSE not supported (http.Flusher not available)", http.StatusInternalServerError)
		return
	}

	conn, ctx := s.sseManager.Add(r.Context())
	if conn == nil {
		jsonError(w, "Server is shutting down", http.StatusServiceUnavailable)
		return
	}
	defer s.sseManager.Remove(conn)
	defer conn.Close()

	ticker := time.NewTicker(sseUpdateInterval)
	defer ticker.Stop()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	log.Println("SSE 客户端已连接，开始推送统计数据")

	if err := s.sendStatsUpdate(ctx, w, flusher); err != nil {
		log.Printf("发送初始统计数据失败: %v", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.Canceled {
				log.Println("SSE 客户端已断开连接(正常)")
			} else {
				log.Printf("SSE 连接结束 (非预期): %v", ctx.Err())
			}
			return

		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()

		case <-ticker.C:
			if err := s.sendStatsUpdate(ctx, w, flusher); err != nil {
				if ctx.Err() == nil {
					log.Printf("发送统计数据失败: %v", err)
				}
				return
			}
		}
	}
}

func (s *Server) getCombinedStats(ctx context.Context) (StatsData, error) {
	stats := StatsData{}

	gCtx, cancel := context.WithTimeout(ctx, grpcTimeout)
	defer cancel()

	queryResp, err := s.statsClient.QueryStats(gCtx, &statspb.QueryStatsRequest{Pattern: "inbound", Reset_: false})
	if err != nil {
		log.Printf("警告: QueryStats 失败: %v", err)
	} else {
		for _, stat := range queryResp.Stat {
			if strings.HasSuffix(stat.Name, ">>>traffic>>>uplink") {
				stats.Uplink += stat.Value
			} else if strings.HasSuffix(stat.Name, ">>>traffic>>>downlink") {
				stats.Downlink += stat.Value
			}
		}
	}

	sysResp, err := s.statsClient.GetSysStats(gCtx, &statspb.SysStatsRequest{})
	if err != nil {
		log.Printf("警告: GetSysStats 失败: %v", err)
	} else if sysResp != nil {
		stats.Uptime = int64(sysResp.GetUptime())
		stats.SysMem = sysResp.GetSys()
		stats.Goroutines = int(sysResp.GetNumGoroutine())
	}

	stats.Outbounds = s.getAllOutboundStatuses(ctx)
	return stats, nil
}

func (s *Server) getAllOutboundStatuses(ctx context.Context) []OutboundStatusData {
	gCtx, cancel := context.WithTimeout(ctx, grpcTimeout)
	defer cancel()

	resp, err := s.observatoryClient.GetOutboundStatus(gCtx, &observatorypb.GetOutboundStatusRequest{})
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

func (s *Server) sendStatsUpdate(ctx context.Context, w http.ResponseWriter, flusher http.Flusher) error {
	stats, err := s.getCombinedStats(ctx)
	if err != nil {
		return fmt.Errorf("获取统计数据失败: %w", err)
	}

	jsonData, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("序列化 JSON 失败: %w", err)
	}

	if _, err := fmt.Fprintf(w, "event: update\ndata: %s\n\n", jsonData); err != nil {
		return fmt.Errorf("写入 SSE 数据失败: %w", err)
	}

	flusher.Flush()
	return nil
}

func jsonResponse(w http.ResponseWriter, data interface{}, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("写入 JSON 响应失败: %v", err)
	}
}

func jsonError(w http.ResponseWriter, message string, statusCode int) {
	log.Println("API Error:", message)
	jsonResponse(w, map[string]string{"error": message}, statusCode)
}
