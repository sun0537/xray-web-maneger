package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCleanupStaleKeys(t *testing.T) {
	resetLimiter()

	now := time.Now()
	limiter.Lock()
	limiter.requests["old-ip"] = slidingWindow{
		currCount:   5,
		windowStart: now.Add(-3 * time.Minute),
	}
	limiter.requests["new-ip"] = slidingWindow{
		currCount:   5,
		windowStart: now.Add(-30 * time.Second),
	}
	limiter.cleanupStaleKeys(now)
	limiter.Unlock()

	limiter.Lock()
	_, oldExists := limiter.requests["old-ip"]
	_, newExists := limiter.requests["new-ip"]
	limiter.Unlock()

	assert.False(t, oldExists, "stale key should be cleaned up")
	assert.True(t, newExists, "recent key should be kept")
}

func TestSlidingWindowTransitions(t *testing.T) {
	rl := &rateLimiter{
		requests: make(map[string]slidingWindow),
		limit:    10,
		window:   time.Minute,
	}

	t.Run("New IP first request passes", func(t *testing.T) {
		assert.True(t, rl.checkAndRecord("1.2.3.4"))
	})

	t.Run("Expired window resets", func(t *testing.T) {
		rl.requests["5.6.7.8"] = slidingWindow{
			currCount:   20,
			windowStart: time.Now().Add(-3 * time.Minute),
		}
		assert.True(t, rl.checkAndRecord("5.6.7.8"), "expired window should reset and allow")
	})

	t.Run("Sliding window blend allows under limit", func(t *testing.T) {
		rl2 := &rateLimiter{
			requests: make(map[string]slidingWindow),
			limit:    10,
			window:   time.Minute,
		}
		now := time.Now()
		rl2.requests["blend-ip"] = slidingWindow{
			prevCount:   8,
			currCount:   0,
			windowStart: now.Add(-61 * time.Second),
		}
		assert.True(t, rl2.checkAndRecord("blend-ip"))
	})
}

func TestLoggingResponseWriterFlush(t *testing.T) {
	inner := httptest.NewRecorder()
	lrw := &loggingResponseWriter{ResponseWriter: inner, statusCode: http.StatusOK}

	lrw.WriteHeader(http.StatusNotFound)
	assert.Equal(t, http.StatusNotFound, lrw.statusCode)

	lrw.Flush()

	unwrapped := lrw.Unwrap()
	assert.Equal(t, inner, unwrapped)
}

func TestClientIP(t *testing.T) {
	t.Run("RemoteAddr without proxy", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		ip := clientIP(req, false)
		assert.Equal(t, "192.168.1.1", ip)
	})

	t.Run("X-Real-IP with proxy trusted", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Real-IP", "10.0.0.1")
		ip := clientIP(req, true)
		assert.Equal(t, "10.0.0.1", ip)
	})

	t.Run("X-Forwarded-For with proxy trusted", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Forwarded-For", "10.0.0.2, 10.0.0.3")
		ip := clientIP(req, true)
		assert.Equal(t, "10.0.0.2", ip)
	})

	t.Run("Proxy headers ignored when not trusted", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		req.Header.Set("X-Real-IP", "10.0.0.1")
		ip := clientIP(req, false)
		assert.Equal(t, "192.168.1.1", ip)
	})
}

func TestGetClientIPFallback(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "172.16.0.1:9999"
	ip := GetClientIP(req)
	assert.Equal(t, "172.16.0.1", ip)
}

func TestBasicAuthDisabled(t *testing.T) {
	handler := BasicAuth("", "")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code, "empty credentials should skip auth")
}
