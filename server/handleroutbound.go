package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	observatorypb "xray-web-manager/internal/xray-proto/app/observatory/command"
	handlerpb "xray-web-manager/internal/xray-proto/app/proxyman/command"
	routingpb "xray-web-manager/internal/xray-proto/app/router/command"
	serial "xray-web-manager/internal/xray-proto/common/serial"

	"xray-web-manager/middleware"
)

var excludeProtocols = map[string]struct{}{
	"loopback":  {},
	"freedom":   {},
	"dns":       {},
	"blackhole": {},
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

func (s *Server) handleGetOutbounds(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()

	resp, err := s.handlerClient.ListOutbounds(ctx, &handlerpb.ListOutboundsRequest{})
	if err != nil {
		log.Printf("获取出站列表失败 [请求来源: %s]: %v", middleware.ClientIPFromContext(r), err)
		jsonError(w, "获取出站列表失败", http.StatusInternalServerError, "server")
		return
	}
	if resp == nil {
		jsonError(w, "收到空的出站列表响应", http.StatusBadGateway, "bad_gateway")
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
	if len(parts) < 3 {
		// 不足 3 段，返回最后一段
		return strings.ToLower(parts[len(parts)-1])
	}
	return strings.ToLower(parts[2])
}

func isValidOutbound(protocolName string) bool {
	_, excluded := excludeProtocols[protocolName]
	return !excluded
}

func (s *Server) handleGetOutboundsStatus(w http.ResponseWriter, r *http.Request) {
	statuses := s.getAllOutboundStatuses()
	jsonResponse(w, statuses, http.StatusOK)
}

func (s *Server) handleGetCurrentOutbound(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()

	resp, err := s.routingClient.GetBalancerInfo(ctx, &routingpb.GetBalancerInfoRequest{
		Tag: s.config.Xray.BalancerTag,
	})
	if err != nil {
		log.Printf("获取当前出站失败 [负载均衡器: %s, 请求来源: %s]: %v", s.config.Xray.BalancerTag, middleware.ClientIPFromContext(r), err)
		jsonError(w, "获取负载均衡器信息失败", http.StatusBadGateway, "bad_gateway")
		return
	}
	if resp == nil {
		jsonError(w, "收到空的负载均衡器响应", http.StatusBadGateway, "bad_gateway")
		return
	}

	current := ""
	auto := true
	if resp.Balancer != nil && resp.Balancer.Override != nil && resp.Balancer.Override.Target != "" {
		current = resp.Balancer.Override.Target
		auto = false
	}

	// 自动模式下，使用 observatory 缓存数据找到最佳可用节点（微秒级，无需并行）
	activeNode := ""
	if auto {
		activeNode = s.findBestAvailableNode()
	}

	jsonResponse(w, CurrentOutboundData{Current: current, Auto: auto, ActiveNode: activeNode}, http.StatusOK)
}

// findBestAvailableNode 使用 observatory 数据找到延迟最低的可用节点
// 优先选择 Alive && Delay > 0 的节点中延迟最低的；
// 若所有 alive 节点的 Delay 均为 0（探测尚未完成），则 fallback 到第一个 alive 节点。
// 排除 excludeProtocols 中的协议（freedom/blackhole/dns/loopback），避免选择非代理节点。
func (s *Server) findBestAvailableNode() string {
	if s.observatoryClient == nil {
		return ""
	}

	statuses := s.getAllOutboundStatuses()
	if len(statuses) == 0 {
		return ""
	}

	excluded := s.buildExcludedTags()

	var bestNode string
	var minDelay int64 = math.MaxInt64
	var firstAlive string

	for _, status := range statuses {
		if _, isExcluded := excluded[status.Tag]; isExcluded {
			continue
		}
		if status.Alive {
			if firstAlive == "" {
				firstAlive = status.Tag
			}
			if status.Delay > 0 {
				if status.Delay < minDelay {
					minDelay = status.Delay
					bestNode = status.Tag
				}
			}
		}
	}

	if bestNode != "" {
		return bestNode
	}
	// Fallback: 所有 alive 节点的 Delay 均为 0（探测尚未完成），返回第一个 alive 节点
	return firstAlive
}

// excludedTagsSnapshot returns a defensive copy of the cached excluded-tags
// set, or nil if the cache is stale. The caller owns the returned map and
// may modify it freely without corrupting the shared cache.
func (s *Server) excludedTagsSnapshot() map[string]struct{} {
	s.excludedTagsMu.RLock()
	defer s.excludedTagsMu.RUnlock()
	if time.Since(s.excludedTagsCache.timestamp) < observatoryCacheTTL {
		return cloneMap(s.excludedTagsCache.tags)
	}
	return nil
}

// buildExcludedTags returns a cached set of tags whose protocols are in
// excludeProtocols (freedom, blackhole, dns, loopback). Uses a TTL cache
// with singleflight to avoid duplicate ListOutbounds gRPC calls when
// /api/current-outbound is requested repeatedly in auto mode.
//
// NOTE: This method intentionally uses s.backgroundContext() rather than a
// per-request context. Since singleflight coalesces concurrent callers into a
// single gRPC call, using a request context would mean one caller disconnecting
// cancels the shared call for all other waiting callers. The fixed apiTimeout
// ensures gRPC calls are bounded even when clients disconnect.
func (s *Server) buildExcludedTags() map[string]struct{} {
	if snapshot := s.excludedTagsSnapshot(); snapshot != nil {
		return snapshot
	}

	s.excludedTagsGroup.Do(excludedTagsGroupKey, func() (any, error) {
		excluded := make(map[string]struct{})
		if s.handlerClient == nil {
			s.updateExcludedTagsCache(excluded)
			return excluded, nil
		}
		ctx, cancel := context.WithTimeout(s.backgroundContext(), apiTimeout)
		defer cancel()
		resp, err := s.handlerClient.ListOutbounds(ctx, &handlerpb.ListOutboundsRequest{})
		if err != nil {
			log.Printf("警告: 获取出站列表失败，跳过协议过滤: %v", err)
			s.updateExcludedTagsCache(excluded)
			return excluded, nil
		}
		if resp == nil {
			s.updateExcludedTagsCache(excluded)
			return excluded, nil
		}
		for _, ob := range resp.Outbounds {
			if _, isExcluded := excludeProtocols[parseProtocol(ob.ProxySettings)]; isExcluded {
				excluded[ob.Tag] = struct{}{}
			}
		}
		s.updateExcludedTagsCache(excluded)
		return excluded, nil
	})

	// Singleflight completed and updated the cache; read directly from cache
	// instead of copying from the Do() result, avoiding a redundant allocation.
	if snapshot := s.excludedTagsSnapshot(); snapshot != nil {
		return snapshot
	}
	// Should be extremely rare (cache expired between write and read).
	return map[string]struct{}{}
}

// updateExcludedTagsCache unconditionally writes tags into the excluded-tags cache.
func (s *Server) updateExcludedTagsCache(tags map[string]struct{}) {
	s.excludedTagsMu.Lock()
	s.excludedTagsCache = cachedExcludedTags{tags: cloneMap(tags), timestamp: time.Now()}
	s.excludedTagsMu.Unlock()
}

func (s *Server) handleSwitchOutbound(w http.ResponseWriter, r *http.Request) {
	var reqBody struct {
		OutboundTag string `json:"outbound_tag"`
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		io.Copy(io.Discard, r.Body)
		log.Printf("切换出站失败 [请求来源: %s, 解码错误]: %v", middleware.ClientIPFromContext(r), err)
		jsonError(w, "无效的请求体", http.StatusBadRequest, "validation")
		return
	}

	if err := validateOutboundTag(reqBody.OutboundTag); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest, "validation")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
	defer cancel()

	_, err := s.routingClient.OverrideBalancerTarget(ctx, &routingpb.OverrideBalancerTargetRequest{
		BalancerTag: s.config.Xray.BalancerTag,
		Target:      reqBody.OutboundTag,
	})
	if err != nil {
		log.Printf("切换出站失败 [目标: %s, 负载均衡器: %s, 请求来源: %s]: %v", reqBody.OutboundTag, s.config.Xray.BalancerTag, middleware.ClientIPFromContext(r), err)
		jsonError(w, "切换出站失败", http.StatusInternalServerError, "server")
		return
	}

	// Invalidate health cache so the next health check reflects the new state.
	s.healthMu.Lock()
	s.healthCache = cachedHealth{stale: true}
	s.healthMu.Unlock()

	// Invalidate observatory cache so the next status check reflects the new state.
	s.obsCacheMu.Lock()
	s.obsCache = cachedObservatory{}
	s.obsCacheMu.Unlock()

	// NOTE: excludedTagsCache is intentionally NOT cleared here. Switching the
	// outbound only changes the balancer route override, not the outbound
	// configuration list, so the set of excluded-protocol tags stays the same.

	log.Printf("审计: 切换出站成功 [目标: %s, 请求来源: %s]", reqBody.OutboundTag, middleware.ClientIPFromContext(r))
	jsonResponse(w, successResponse{Status: "success"}, http.StatusOK)
}

func validateOutboundTag(tag string) error {
	if len(tag) > 256 {
		return fmt.Errorf("出站标签过长")
	}
	// Empty tag resets the balancer override, restoring auto mode.
	if tag == "" {
		return nil
	}
	for _, c := range tag {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == ':') {
			return fmt.Errorf("出站标签包含非法字符")
		}
	}
	return nil
}

// observatoryCacheTTL is the duration for which observatory data is cached.
// A short TTL avoids duplicate gRPC calls when /api/current-outbound and
// /api/outbounds-status are requested in quick succession during page load.
const observatoryCacheTTL = 2 * time.Second

// updateObsCache unconditionally writes data into the observatory cache.
// Callers (inside singleflight) only reach this when the cache is stale,
// so the timestamp guard is unnecessary. Stores a defensive copy of the
// slice so the caller's data is decoupled from the cache.
func (s *Server) updateObsCache(data []OutboundStatusData) {
	snapshot := cloneSlice(data)
	s.obsCacheMu.Lock()
	s.obsCache = cachedObservatory{data: snapshot, timestamp: time.Now()}
	s.obsCacheMu.Unlock()
}

// getAllOutboundStatuses returns cached observatory data or fetches fresh data.
// The returned slice is a copy; callers may modify it freely.
// Always returns a non-nil slice (empty on error or when no data is available).
// All logging and cache updates happen inside singleflight so concurrent callers
// see exactly one log line and one cache write per coalesced gRPC call.
//
// NOTE: This method intentionally uses s.backgroundContext() rather than a
// per-request context. Since singleflight coalesces concurrent callers into a
// single gRPC call, using a request context would mean one caller disconnecting
// cancels the shared call for all other waiting callers. The fixed apiTimeout
// ensures gRPC calls are bounded even when clients disconnect.
func (s *Server) getAllOutboundStatuses() []OutboundStatusData {
	if s.observatoryClient == nil {
		return []OutboundStatusData{}
	}

	s.obsCacheMu.RLock()
	if time.Since(s.obsCache.timestamp) < observatoryCacheTTL {
		cached := s.obsCache.data
		s.obsCacheMu.RUnlock()
		return cloneSlice(cached)
	}
	s.obsCacheMu.RUnlock()

	// Singleflight: only the winning goroutine executes the gRPC call and
	// writes to cache. All losers block until the winner finishes.
	s.obsGroup.Do(observatoryGroupKey, func() (any, error) {
		ctx, cancel := context.WithTimeout(s.backgroundContext(), apiTimeout)
		defer cancel()

		resp, err := s.observatoryClient.GetOutboundStatus(ctx, &observatorypb.GetOutboundStatusRequest{})
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("警告: GetOutboundStatus 失败: %v", err)
			}
			s.updateObsCache([]OutboundStatusData{})
			return nil, nil
		}

		if resp.Status == nil || resp.Status.Status == nil {
			s.updateObsCache([]OutboundStatusData{})
			return nil, nil
		}

		statuses := make([]OutboundStatusData, 0, len(resp.Status.Status))
		for _, status := range resp.Status.Status {
			statuses = append(statuses, OutboundStatusData{
				Tag:   status.OutboundTag,
				Alive: status.Alive,
				Delay: status.Delay,
			})
		}

		s.updateObsCache(statuses)
		return nil, nil
	})

	// Singleflight completed and updated the cache; read directly from cache
	// instead of copying from the Do() result, avoiding a redundant allocation.
	if snapshot := s.obsSnapshot(); snapshot != nil {
		return snapshot
	}
	// Should be extremely rare (cache expired between write and read).
	return []OutboundStatusData{}
}

// obsSnapshot returns a defensive copy of the cached observatory data,
// or nil if the cache is stale.
func (s *Server) obsSnapshot() []OutboundStatusData {
	s.obsCacheMu.RLock()
	defer s.obsCacheMu.RUnlock()
	if time.Since(s.obsCache.timestamp) < observatoryCacheTTL {
		return cloneSlice(s.obsCache.data)
	}
	return nil
}
