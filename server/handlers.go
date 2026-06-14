package server

import (
	"encoding/json"
	"log"
	"net/http"
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	body, err := json.Marshal(middleware.ErrorResponse{Error: message, ErrorType: errorType})
	if err != nil {
		log.Printf("JSON 错误响应序列化失败: %v", err)
		body = []byte(`{"error":"internal error","error_type":"server"}`)
	}
	if _, err := w.Write(body); err != nil {
		log.Printf("JSON 错误响应写入失败: %v", err)
	}
}
