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
	"sync/atomic"
	"time"

	statspb "xray-web-manager/internal/xray-proto/app/stats/command"

	"xray-web-manager/middleware"
)

const (
	sseUpdateInterval    = 2 * time.Second
	sseHeartbeatInterval = 15 * time.Second
	sseMaxLifetime       = 1 * time.Hour
)

var heartbeatBytes = []byte(": heartbeat\n\n")

var (
	sseEventPrefix = []byte("event: update\ndata: ")
	sseEventSuffix = []byte("\n\n")
)

// Throttle BPS warning logs to at most once every 5 minutes to avoid log spam
// when Xray restarts and counters reset. Uses atomic CAS so at most one
// goroutine wins the race and logs per interval window.
var (
	bpsUplinkWarnLast   atomic.Int64
	bpsDownlinkWarnLast atomic.Int64
)

// bpsWarnIntervalSec is the minimum seconds between BPS warning logs.
const bpsWarnIntervalSec = int64(300) // 5 minutes

// warnBPSOnce logs a BPS warning at most once per bpsWarnIntervalSec.
// Returns true if this call emitted the log.
func warnBPSOnce(last *atomic.Int64, format string, v ...any) bool {
	now := time.Now().Unix()
	prev := last.Load()
	if now-prev >= bpsWarnIntervalSec {
		if last.CompareAndSwap(prev, now) {
			log.Printf(format, v...)
			return true
		}
	}
	return false
}

// sseWriteEvent writes a single SSE "event: update\ndata: <json>\n\n" frame.
// Avoids the temporary buffer and string formatting of fmt.Fprintf for the
// hot broadcast path.
func sseWriteEvent(w http.ResponseWriter, flusher http.Flusher, jsonData []byte) error {
	if _, err := w.Write(sseEventPrefix); err != nil {
		return err
	}
	if _, err := w.Write(jsonData); err != nil {
		return err
	}
	if _, err := w.Write(sseEventSuffix); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

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

	// Remove the server-level WriteTimeout so the long-lived SSE stream
	// is not killed by the 30s deadline that protects non-SSE routes.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		// Non-fatal: the connection will still work, it just has a
		// 30s write deadline which may cause premature disconnects.
		log.Printf("SSE: 无法取消写超时: %v", err)
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		errorMsg := "SSE 不受支持 (http.Flusher 不可用)"
		log.Printf("SSE 连接失败 [请求来源: %s]: %s", middleware.ClientIPFromContext(r), errorMsg)
		jsonError(w, errorMsg, http.StatusInternalServerError, "server")
		return
	}

	conn := s.sseManager.Add(r.Context())
	if conn == nil {
		errorMsg := "服务器正在关闭"
		log.Printf("SSE 连接被拒绝 [请求来源: %s]: %s", middleware.ClientIPFromContext(r), errorMsg)
		jsonError(w, errorMsg, http.StatusServiceUnavailable, "service_unavailable")
		return
	}

	ip := middleware.ClientIPFromContext(r)
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
	if cached := s.broadcaster.LastBroadcast(); cached != nil {
		b, marshalErr := json.Marshal(cached)
		if marshalErr != nil {
			log.Printf("SSE 缓存数据序列化失败: %v", marshalErr)
		} else {
			initialJSON = b
		}
	}
	if initialJSON == nil {
		stats, statsErr := s.getCombinedStats()
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
		if err := sseWriteEvent(w, flusher, initialJSON); err != nil {
			if connCtx.Err() == nil {
				log.Printf("SSE 初始数据写入失败 [请求来源: %s]: %v", ip, err)
			}
			return
		}
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
			jsonData, marshalErr := json.Marshal(payload)
			if marshalErr != nil {
				log.Printf("SSE 数据序列化失败: %v", marshalErr)
				continue
			}
			if err := sseWriteEvent(w, flusher, jsonData); err != nil {
				if connCtx.Err() == nil {
					log.Printf("发送统计数据失败 [请求来源: %s]: %v", ip, err)
				}
				return
			}
		}
	}
}

func (s *Server) getCombinedStats() (StatsData, error) {
	// Use singleflight to coalesce concurrent requests for the same stats
	// data. Use s.backgroundContext() for gRPC calls so the coalesced
	// probe survives even if the caller disconnects.
	v, err, _ := s.statsGroup.Do(combinedStatsGroupKey, func() (any, error) {
		stats := StatsData{}

		var (
			wg            sync.WaitGroup
			queryResp     *statspb.QueryStatsResponse
			sysResp       *statspb.SysStatsResponse
			outboundStats []OutboundStatusData
			queryErr      error
			sysErr        error
		)

		grpcCtx, grpcCancel := context.WithTimeout(s.backgroundContext(), grpcTimeout)
		defer grpcCancel()

		wg.Add(3)
		go func() {
			defer wg.Done()
			queryResp, queryErr = s.statsClient.QueryStats(grpcCtx, &statspb.QueryStatsRequest{Pattern: "inbound", Reset_: false})
		}()
		go func() {
			defer wg.Done()
			sysResp, sysErr = s.statsClient.GetSysStats(grpcCtx, &statspb.SysStatsRequest{})
		}()
		go func() {
			defer wg.Done()
			outboundStats = s.getAllOutboundStatuses(grpcCtx)
		}()
		wg.Wait()

		failedSources := 0

		if queryErr != nil {
			if errors.Is(queryErr, context.Canceled) {
				return StatsData{}, queryErr
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
			if errors.Is(sysErr, context.Canceled) {
				return StatsData{}, sysErr
			}
			log.Printf("GetSysStats 错误 [系统统计]: %v", sysErr)
			stats.Degraded = true
			failedSources++
		} else if sysResp != nil {
			stats.Uptime = int64(sysResp.GetUptime())
			stats.SysMem = sysResp.GetSys()
			stats.Goroutines = int(sysResp.GetNumGoroutine())
		}

		if outboundStats == nil {
			stats.Outbounds = []OutboundStatusData{}
		} else {
			stats.Outbounds = outboundStats
		}

		// Only query and sys sources count as "failure" because observatory
		// returning nil means "no data" (not an error). The cached
		// Outbounds empty-slice path keeps the JSON shape stable.
		if failedSources >= 2 {
			return StatsData{}, fmt.Errorf("all data sources failed")
		}
		return stats, nil
	})

	if err != nil {
		return StatsData{}, err
	}
	data, ok := v.(StatsData)
	if !ok {
		return StatsData{}, fmt.Errorf("combined-stats: unexpected singleflight value type %T", v)
	}
	return data, nil
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

	curr, ok1 := data.(StatsData)
	last, ok2 := prev.(StatsData)
	if !ok1 || !ok2 {
		log.Printf("警告: computeBPS 类型断言失败: data=%T, prev=%T", data, prev)
		return data
	}

	curr.UplinkBPS = float64(curr.Uplink-last.Uplink) / elapsed
	if curr.UplinkBPS < 0 {
		warnBPSOnce(&bpsUplinkWarnLast, "警告: 上行 BPS 为负 (%.0f)，可能 Xray 已重启导致计数器归零", curr.UplinkBPS)
		curr.UplinkBPS = 0
	}
	curr.DownlinkBPS = float64(curr.Downlink-last.Downlink) / elapsed
	if curr.DownlinkBPS < 0 {
		warnBPSOnce(&bpsDownlinkWarnLast, "警告: 下行 BPS 为负 (%.0f)，可能 Xray 已重启导致计数器归零", curr.DownlinkBPS)
		curr.DownlinkBPS = 0
	}

	return curr
}
