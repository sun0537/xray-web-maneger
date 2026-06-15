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
	limiter.requests["old-ip"] = &slidingWindow{
		currCount:   5,
		windowStart: now.Add(-3 * time.Minute),
	}
	limiter.requests["new-ip"] = &slidingWindow{
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
		requests: make(map[string]*slidingWindow),
		limit:    10,
		window:   time.Minute,
	}

	t.Run("New IP first request passes", func(t *testing.T) {
		assert.True(t, rl.checkAndRecord("1.2.3.4"))
	})

	t.Run("Expired window resets", func(t *testing.T) {
		rl.requests["5.6.7.8"] = &slidingWindow{
			currCount:   20,
			windowStart: time.Now().Add(-3 * time.Minute),
		}
		assert.True(t, rl.checkAndRecord("5.6.7.8"), "expired window should reset and allow")
	})

	t.Run("Sliding window blend allows under limit", func(t *testing.T) {
		rl2 := &rateLimiter{
			requests: make(map[string]*slidingWindow),
			limit:    10,
			window:   time.Minute,
		}
		now := time.Now()
		rl2.requests["blend-ip"] = &slidingWindow{
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

func TestClientIPFromContextFallback(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "172.16.0.1:9999"
	ip := ClientIPFromContext(req)
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

func TestBasicAuthSuccess(t *testing.T) {
	resetAuthLimiter()
	resetNoCredLimiter()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("protected"))
	})
	handler := BasicAuth("admin", "secret")(inner)

	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "protected", rr.Body.String())
}

func TestBasicAuthFailure(t *testing.T) {
	resetAuthLimiter()
	resetNoCredLimiter()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := BasicAuth("admin", "secret")(inner)

	t.Run("Wrong password", func(t *testing.T) {
		resetAuthLimiter()
		req := httptest.NewRequest("GET", "/", nil)
		req.SetBasicAuth("admin", "wrong")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Contains(t, rr.Header().Get("WWW-Authenticate"), "Basic")
	})

	t.Run("No credentials", func(t *testing.T) {
		resetAuthLimiter()
		resetNoCredLimiter()
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("Missing credentials do not count as auth failures", func(t *testing.T) {
		resetAuthLimiter()
		resetNoCredLimiter()
		authLimiter.limit = 2

		// 多次无凭证请求不应触发 auth 限流
		for i := 0; i < 10; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "10.0.0.88:1234"
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusUnauthorized, rr.Code)
		}

		// 无凭证请求后，错误密码仍然应该正常计入
		req := httptest.NewRequest("GET", "/", nil)
		req.SetBasicAuth("admin", "wrong")
		req.RemoteAddr = "10.0.0.88:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)

		// 第二次错误密码：记录失败并封锁（但本次请求仍返回 401）
		req = httptest.NewRequest("GET", "/", nil)
		req.SetBasicAuth("admin", "wrong")
		req.RemoteAddr = "10.0.0.88:1234"
		rr = httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)

		// 第三次请求：已被封锁，返回 429
		req = httptest.NewRequest("GET", "/", nil)
		req.SetBasicAuth("admin", "wrong")
		req.RemoteAddr = "10.0.0.88:1234"
		rr = httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusTooManyRequests, rr.Code)
	})

	t.Run("No-credential requests are rate limited independently", func(t *testing.T) {
		resetAuthLimiter()
		resetNoCredLimiter()
		noCredLimiter.limit = 3

		// 前 3 次无凭证请求应返回 401
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "10.0.0.77:1234"
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusUnauthorized, rr.Code)
		}

		// 第 4 次无凭证请求应触发限流返回 429
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.77:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusTooManyRequests, rr.Code)
		assert.Equal(t, "60", rr.Header().Get("Retry-After"))
	})

	t.Run("Blocked after too many failures", func(t *testing.T) {
		resetAuthLimiter()
		resetNoCredLimiter()
		authLimiter.limit = 2

		for i := 0; i < 2; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			req.SetBasicAuth("admin", "wrong")
			req.RemoteAddr = "10.0.0.99:1234"
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
		}

		req := httptest.NewRequest("GET", "/", nil)
		req.SetBasicAuth("admin", "wrong")
		req.RemoteAddr = "10.0.0.99:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusTooManyRequests, rr.Code)
		assert.Equal(t, "300", rr.Header().Get("Retry-After"))
	})
}

func TestSecurityHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := SecurityHeaders(inner)

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, "nosniff", rr.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rr.Header().Get("X-Frame-Options"))
	assert.Contains(t, rr.Header().Get("Content-Security-Policy"), "default-src 'self'")
	assert.Equal(t, "strict-origin-when-cross-origin", rr.Header().Get("Referrer-Policy"))
}

func TestLogger(t *testing.T) {
	t.Run("Logs request and sets status", func(t *testing.T) {
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("ok"))
		})
		handler := Logger(false, inner)

		req := httptest.NewRequest("GET", "/api/test", nil)
		req.RemoteAddr = "1.2.3.4:5678"
		rr := httptest.NewRecorder()

		logOutput := captureLog(func() {
			handler.ServeHTTP(rr, req)
		})

		assert.Equal(t, http.StatusCreated, rr.Code)
		assert.Contains(t, logOutput, "1.2.3.4")
		assert.Contains(t, logOutput, "GET")
		assert.Contains(t, logOutput, "/api/test")
		assert.Contains(t, logOutput, "201")
	})

	t.Run("ClientIPFromContext from context", func(t *testing.T) {
		var capturedIP string
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedIP = ClientIPFromContext(r)
			w.WriteHeader(http.StatusOK)
		})
		handler := Logger(true, inner)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Real-IP", "10.0.0.5")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.Equal(t, "10.0.0.5", capturedIP)
	})
}

func TestRateLimitTrustProxy(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rl := &rateLimiter{
		requests: make(map[string]*slidingWindow),
		limit:    3,
		window:   time.Minute,
	}
	handler := rl.middleware(true, okHandler)

	// Requests from same X-Real-IP should share the counter
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.Header.Set("X-Real-IP", "10.0.0.1")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	}

	// 4th request from same IP should be rate limited
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Real-IP", "10.0.0.1")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusTooManyRequests, rr.Code)

	// Request from different IP should pass
	req2 := httptest.NewRequest("GET", "/api/test", nil)
	req2.Header.Set("X-Real-IP", "10.0.0.2")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	assert.Equal(t, http.StatusOK, rr2.Code)
}

func TestStopCleanup(t *testing.T) {
	InitRateLimiter()
	startAuthLimiterCleanup()

	StopCleanup()

	assert.Nil(t, limiterStop)
	assert.Nil(t, authCleanupStop)
	assert.False(t, limiterStarted)
	assert.False(t, authCleanupStarted)
}
