package server

import (
	"context"
	"log"
	"net/http"
	"time"

	statspb "xray-web-manager/internal/xray-proto/app/stats/command"
)

type cachedHealth struct {
	status    string
	timestamp time.Time
}

type HealthStatus struct {
	Status         string `json:"status"`
	XrayAPIStatus  string `json:"xray_api_status"`
	SSEConnections int    `json:"sse_connections"`
	Uptime         int64  `json:"uptime"`
	BalancerTag    string `json:"balancer_tag"`
	Timestamp      int64  `json:"timestamp"`
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	s.healthMu.RLock()
	cached := s.healthCache
	s.healthMu.RUnlock()

	xrayStatus := cached.status
	if time.Since(cached.timestamp) > 10*time.Second {
		result, err, _ := s.healthGroup.Do(healthGroupKey, func() (any, error) {
			s.healthMu.RLock()
			fresh := s.healthCache
			s.healthMu.RUnlock()
			if time.Since(fresh.timestamp) <= 10*time.Second {
				return fresh.status, nil
			}

			// Use a context independent of any specific request so the gRPC call
			// survives even if the request that triggered singleflight.Do
			// disconnects. Otherwise a slow or disconnected first caller
			// would cancel the coalesced probe and poison all waiters.
			ctx, cancel := context.WithTimeout(s.backgroundContext(), 2*time.Second)
			defer cancel()
			if _, err := s.statsClient.GetSysStats(ctx, &statspb.SysStatsRequest{}); err != nil {
				return "disconnected", err
			}
			return "connected", nil
		})
		if status, ok := result.(string); ok {
			xrayStatus = status
			cacheTime := time.Now()
			if err != nil {
				log.Printf("健康检查: Xray gRPC 不可达: %v", err)
				// 健康检查失败时缩短缓存有效期至 3 秒 (10-7)，以便更快重试。
				cacheTime = time.Now().Add(-7 * time.Second)
			}
			s.healthMu.Lock()
			s.healthCache = cachedHealth{status: xrayStatus, timestamp: cacheTime}
			s.healthMu.Unlock()
		}
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
