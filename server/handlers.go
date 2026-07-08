package server

import (
	"encoding/json"
	"log"
	"net/http"
	"slices"
	"time"

	"xray-web-manager/middleware"
)

const apiTimeout = 5 * time.Second

type CurrentOutboundData struct {
	Current    string `json:"current"`
	Auto       bool   `json:"auto"`
	ActiveNode string `json:"active_node,omitempty"` // 自动模式下实际使用的节点
}

type configResponse struct {
	BalancerTag string `json:"balancer_tag"`
	LogType     string `json:"log_type"`
}

type successResponse struct {
	Status string `json:"status"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	logType := s.config.Log.Type
	if logType == "" {
		logType = "none"
	}
	jsonResponse(w, configResponse{BalancerTag: s.config.Xray.BalancerTag, LogType: logType}, http.StatusOK)
}

func jsonResponse(w http.ResponseWriter, data any, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("JSON 写入失败: %v", err)
	}
}

func jsonError(w http.ResponseWriter, message string, statusCode int, errorType string) {
	middleware.WriteJSONError(w, message, statusCode, errorType)
}

// cloneMap returns a shallow copy of m. Returns nil if m is nil.
func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return nil
	}
	c := make(map[K]V, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// cloneSlice returns a shallow copy of s using slices.Clone.
func cloneSlice[T any](s []T) []T {
	return slices.Clone(s)
}
