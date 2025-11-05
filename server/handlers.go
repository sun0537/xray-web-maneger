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
	grpcTimeout          = 4 * time.Second  // gRPC 调用超时
	sseUpdateInterval    = 2 * time.Second  // SSE 数据推送间隔
	sseHeartbeatInterval = 15 * time.Second // SSE 心跳间隔
	apiTimeout           = 5 * time.Second  // 适用于 http handler 的通用超时
)

type OutboundInfo struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
}

// handleGetConfig 返回前端所需的配置
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]interface{}{
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

	// 更改为新结构
	outbounds := make([]OutboundInfo, 0, len(resp.Outbounds))
	for _, outbound := range resp.Outbounds {
		if outbound.Tag != "" {
			protocolName := parseProtocol(outbound.ProxySettings)
			if isValidOutbound(protocolName) {
				outboundInfo := OutboundInfo{
					Tag:      outbound.Tag,
					Protocol: protocolName,
				}
				outbounds = append(outbounds, outboundInfo)
			}
		}
	}

	jsonResponse(w, outbounds, http.StatusOK)
}

// (用于解析 ProxySettings 中的协议类型)
func parseProtocol(settings *serial.TypedMessage) string {
	protocolName := "unknown"

	// *serial.TypedMessage 直接包含 Type 字符串。
	// 格式: "xray.proxy.vmess.outbound.Config"
	typeString := settings.Type
	if typeString == "" {
		return protocolName
	}

	// 按 "." 分割
	parts := strings.Split(typeString, ".")

	// 我们正数第三个部分 (e.g., "vmess", "freedom", "vless")
	if len(parts) > 2 {
		protocolName = parts[2]
	}

	return protocolName
}

func isValidOutbound(protocolName string) bool {

	// 排除系统标签
	excludeTags := []string{"loopback", "freedom", "dns", "blackhole"}
	for _, exclude := range excludeTags {
		if strings.ToLower(protocolName) == exclude {
			return false
		}
	}
	return true
}

func (s *Server) handleGetOutboundStatus(w http.ResponseWriter, r *http.Request) {
	tag := r.URL.Query().Get("tag")
	if tag == "" {
		jsonError(w, "缺少 'tag' 查询参数", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()

	resp, err := s.observatoryClient.GetOutboundStatus(ctx, &observatorypb.GetOutboundStatusRequest{})
	if err != nil {
		jsonError(w, "无法获取出站状态 (gRPC): "+err.Error(), http.StatusInternalServerError)
		return
	}

	if resp.Status == nil || resp.Status.Status == nil {
		jsonError(w, "观测结果为空或格式无效", http.StatusInternalServerError)
		return
	}

	for _, status := range resp.Status.Status {
		if status.OutboundTag == tag {
			jsonResponse(w, status, http.StatusOK)
			return
		}
	}

	log.Printf("未在观测结果中找到 tag: %s", tag)
	jsonResponse(w, map[string]interface{}{"alive": false, "delay": 0, "tag": tag, "error": "not_found"}, http.StatusNotFound)
}

func (s *Server) handleGetCurrentOutbound(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()

	req := &routingpb.GetBalancerInfoRequest{
		Tag: s.config.Xray.BalancerTag,
	}

	resp, err := s.routingClient.GetBalancerInfo(ctx, req)
	if err != nil {
		jsonResponse(w, map[string]interface{}{"current": "", "auto": true}, http.StatusOK)
		return
	}

	current := ""
	auto := true

	if resp.Balancer != nil && resp.Balancer.Override != nil && resp.Balancer.Override.Target != "" {
		current = resp.Balancer.Override.Target
		auto = false
	}

	jsonResponse(w, map[string]interface{}{
		"current": current,
		"auto":    auto,
	}, http.StatusOK)
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

	req := &routingpb.OverrideBalancerTargetRequest{
		BalancerTag: s.config.Xray.BalancerTag,
		Target:      reqBody.OutboundTag,
	}

	_, err := s.routingClient.OverrideBalancerTarget(ctx, req)
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

	// 注册 SSE 连接
	conn, ctx := s.sseManager.Add(r.Context())
	if conn == nil {
		// 服务器正在关闭，拒绝新连接
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

	// 立即发送第一次数据
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
				if ctx.Err() != nil {
				} else {
					log.Printf("发送统计数据失败: %v", err)
				}
			}
		}
	}
}

func (s *Server) getCombinedStats(ctx context.Context) (map[string]interface{}, error) {
	var totalUplink int64 = 0
	var totalDownlink int64 = 0

	queryReq := &statspb.QueryStatsRequest{Pattern: "inbound", Reset_: false}
	gCtx, cancel := context.WithTimeout(ctx, grpcTimeout)
	defer cancel()

	queryResp, err := s.statsClient.QueryStats(gCtx, queryReq)
	if err != nil {
		log.Printf("警告: QueryStats 失败: %v", err)
		// 不返回错误，使用默认值继续
	} else {
		for _, stat := range queryResp.Stat {
			if strings.HasSuffix(stat.Name, ">>>traffic>>>uplink") {
				totalUplink += stat.Value
			} else if strings.HasSuffix(stat.Name, ">>>traffic>>>downlink") {
				totalDownlink += stat.Value
			}
		}
	}

	var sysUptime int64
	var sysMem uint64
	var sysGoroutines int

	sysReq := &statspb.SysStatsRequest{}
	gCtx2, cancel2 := context.WithTimeout(ctx, grpcTimeout)
	defer cancel2()

	sysResp, err := s.statsClient.GetSysStats(gCtx2, sysReq)
	if err != nil {
		log.Printf("警告: GetSysStats 失败: %v", err)
		// 不返回错误，使用默认值
	} else if sysResp != nil {
		sysUptime = int64(sysResp.GetUptime())
		sysMem = sysResp.GetSys()
		sysGoroutines = int(sysResp.GetNumGoroutine())
	}

	return map[string]interface{}{
		"uplink":     totalUplink,
		"downlink":   totalDownlink,
		"uptime":     sysUptime,
		"sys_mem":    sysMem,
		"goroutines": sysGoroutines,
	}, nil
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
