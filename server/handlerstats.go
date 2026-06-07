package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	statspb "xray-web-manager/internal/xray-proto/app/stats/command"

	"xray-web-manager/middleware"
	"xray-web-manager/sse"
)

const (
	sseUpdateInterval    = 2 * time.Second
	sseHeartbeatInterval = 15 * time.Second
	sseMaxLifetime       = 1 * time.Hour
)

var heartbeatBytes = []byte(": heartbeat\n\n")

type StatsData struct {
	Uplink      int64                `json:"uplink"`
	Downlink    int64                `json:"downlink"`
	UplinkBPS   float64              `json:"uplink_bps"`
	DownlinkBPS float64              `json:"downlink_bps"`
	Uptime      int64                `json:"uptime"`
	SysMem      uint64               `json:"sys_mem"`
	Goroutines  int                  `json:"goroutines"`
	Outbounds   []OutboundStatusData `json:"outbounds"`
	Degraded    bool                 `json:"degraded,omitempty"`
}

func (s *Server) handleStatsSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		errorMsg := "SSE 不受支持 (http.Flusher 不可用)"
		log.Printf("SSE 连接失败 [请求来源: %s]: %s", middleware.GetClientIP(r), errorMsg)
		jsonError(w, errorMsg, http.StatusInternalServerError, "server")
		return
	}

	conn := s.sseManager.Add(r.Context())
	if conn == nil {
		errorMsg := "服务器正在关闭"
		log.Printf("SSE 连接被拒绝 [请求来源: %s]: %s", middleware.GetClientIP(r), errorMsg)
		jsonError(w, errorMsg, http.StatusServiceUnavailable, "service_unavailable")
		return
	}

	ip := middleware.GetClientIP(r)
	log.Printf("SSE 连接已建立 [请求来源: %s]，当前连接数: %d", ip, s.sseManager.Count())
	defer func() {
		s.sseManager.Remove(conn)
		log.Printf("SSE 连接已断开 [请求来源: %s]，当前连接数: %d", ip, s.sseManager.Count())
		conn.Close()
	}()

	connCtx := conn.Context()

	statsCh := s.broadcaster.Subscribe()
	defer s.broadcaster.Unsubscribe(statsCh)

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	lifetime := time.NewTimer(sseMaxLifetime)
	defer lifetime.Stop()

	var initialJSON []byte
	if cached := s.broadcaster.LastBroadcastJSON(); cached != nil {
		initialJSON = cached
	} else {
		stats, statsErr := s.getCombinedStats(connCtx)
		if statsErr != nil {
			if !errors.Is(statsErr, context.Canceled) {
				log.Printf("SSE 初始数据获取失败，发送降级数据: %v", statsErr)
			}
			stats = StatsData{Outbounds: []OutboundStatusData{}}
		}
		b, marshalErr := json.Marshal(stats)
		if marshalErr != nil {
			log.Printf("SSE 初始数据序列化失败: %v", marshalErr)
		} else {
			initialJSON = b
		}
	}
	if initialJSON != nil {
		fmt.Fprintf(w, "event: update\ndata: %s\n\n", initialJSON)
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

		case payload, ok := <-statsCh:
			if !ok {
				return
			}
			var jsonData []byte
			switch v := payload.(type) {
			case sse.RawEvent:
				jsonData = v.JSON
			case []byte:
				jsonData = v
			default:
				var marshalErr error
				jsonData, marshalErr = json.Marshal(v)
				if marshalErr != nil {
					log.Printf("SSE 数据序列化失败: %v", marshalErr)
					continue
				}
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
		if errors.Is(queryErr, context.Canceled) {
			return stats, queryErr
		}
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
		if !errors.Is(sysErr, context.Canceled) {
			log.Printf("GetSysStats 错误 [系统统计]: %v", sysErr)
			stats.Degraded = true
			failedSources++
		}
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

// computeBPS enriches a StatsData payload with upload/download BPS values
// computed from the delta between the current snapshot and prev.
func computeBPS(data, prev any, tickAt, prevAt time.Time) any {
	if prev == nil || prevAt.IsZero() {
		return data
	}
	elapsed := tickAt.Sub(prevAt).Seconds()
	if elapsed <= 0 {
		return data
	}

	currRaw, ok1 := data.(sse.RawEvent)
	prevRaw, ok2 := prev.(sse.RawEvent)
	if !ok1 || !ok2 {
		return data
	}

	var curr, last StatsData
	if json.Unmarshal(currRaw.JSON, &curr) != nil || json.Unmarshal(prevRaw.JSON, &last) != nil {
		return data
	}

	curr.UplinkBPS = float64(curr.Uplink-last.Uplink) / elapsed
	if curr.UplinkBPS < 0 {
		curr.UplinkBPS = 0
	}
	curr.DownlinkBPS = float64(curr.Downlink-last.Downlink) / elapsed
	if curr.DownlinkBPS < 0 {
		curr.DownlinkBPS = 0
	}

	if enriched, err := json.Marshal(curr); err == nil {
		return sse.RawEvent{JSON: enriched}
	}
	return data
}
