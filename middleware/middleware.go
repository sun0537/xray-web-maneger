package middleware

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ErrorResponse is the standard JSON body for error responses.
type ErrorResponse struct {
	Error     string `json:"error"`
	ErrorType string `json:"error_type"`
}

// maxTrackedIPs caps the number of unique IPs tracked by rate limiters,
// preventing unbounded memory growth from distributed attacks.
const maxTrackedIPs = 10_000

// startCleanupLoop starts a background goroutine that periodically calls cleanup.
// It is safe to call multiple times — subsequent calls are no-ops.
// Parameters are pointers so the caller retains ownership of the stop channel.
func startCleanupLoop(mu *sync.Mutex, started *bool, stop *chan struct{},
	interval time.Duration, cleanup func()) {
	mu.Lock()
	defer mu.Unlock()
	if *started {
		return
	}
	*started = true
	*stop = make(chan struct{})
	stopCh := *stop // capture locally to avoid data race in select
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				cleanup()
			}
		}
	}()
}

// WriteJSONError writes a JSON error response with the given status code.
// Includes a fallback body preserving the original message if marshaling fails.
func WriteJSONError(w http.ResponseWriter, message string, statusCode int, errorType string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	body, err := json.Marshal(ErrorResponse{Error: message, ErrorType: errorType})
	if err != nil {
		log.Printf("JSON 错误响应序列化失败: %v", err)
		body = []byte(`{"error":` + strconv.Quote(message) + `,"error_type":` + strconv.Quote(errorType) + `}`)
	}
	if _, err := w.Write(body); err != nil {
		log.Printf("JSON 错误响应写入失败: %v", err)
	}
}
