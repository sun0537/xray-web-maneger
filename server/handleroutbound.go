package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

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
		errorMsg := fmt.Sprintf("无法获取出站列表: %v", err)
		log.Printf("获取出站列表失败 [请求来源: %s]: %s", middleware.ClientIPFromContext(r), errorMsg)
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
		log.Printf("获取当前出站失败 [负载均衡器: %s, 请求来源: %s]: %s", s.config.Xray.BalancerTag, middleware.ClientIPFromContext(r), errorMsg)
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
		io.Copy(io.Discard, r.Body)
		errorMsg := fmt.Sprintf("无效的请求体: %v", err)
		log.Printf("切换出站失败 [请求来源: %s, 解码错误]: %s", middleware.ClientIPFromContext(r), errorMsg)
		jsonError(w, errorMsg, http.StatusBadRequest, "validation")
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
		errorMsg := fmt.Sprintf("切换出站失败: %v", err)
		log.Printf("切换出站失败 [目标: %s, 负载均衡器: %s, 请求来源: %s]: %s", reqBody.OutboundTag, s.config.Xray.BalancerTag, middleware.ClientIPFromContext(r), errorMsg)
		jsonError(w, errorMsg, http.StatusInternalServerError, "server")
		return
	}

	log.Printf("审计: 切换出站成功 [目标: %s, 请求来源: %s]", reqBody.OutboundTag, middleware.ClientIPFromContext(r))
	jsonResponse(w, successResponse{Status: "success"}, http.StatusOK)
}

func validateOutboundTag(tag string) error {
	if len(tag) > 256 {
		return fmt.Errorf("出站标签过长")
	}
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

func (s *Server) getAllOutboundStatuses(ctx context.Context) []OutboundStatusData {
	resp, err := s.observatoryClient.GetOutboundStatus(ctx, &observatorypb.GetOutboundStatusRequest{})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Printf("警告: GetOutboundStatus 失败: %v", err)
		}
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
