package middleware

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// --- 测试 Recovery (panic恢复) ---
func TestRecovery(t *testing.T) {
	// 1. 创建一个 handler，它一定会 panic
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	// 2. 准备请求和记录器
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()

	// 3. 将 panic handler 包装在 Recovery 中间件 里
	testHandler := Recovery(panicHandler)

	// 4. 捕获日志输出，确保它打印了堆栈跟踪
	logOutput := captureLog(func() {
		// 5. 执行请求
		testHandler.ServeHTTP(rr, req)
	})

	// 6. 断言
	assert.Equal(t, http.StatusInternalServerError, rr.Code, "状态码应为 500")
	assert.Contains(t, rr.Body.String(), "Internal Server Error", "响应体应为通用错误")
	assert.Contains(t, logOutput, "!!! HTTP Handler Panic: test panic", "日志应包含 panic 信息")
	assert.Contains(t, logOutput, "Stacktrace:", "日志应包含堆栈跟踪")
}

// --- 测试 CheckOrigin (来源检查) ---
func TestCheckOrigin(t *testing.T) {
	// 1. 创建一个简单的 "OK" handler
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// 2. 定义允许的来源
	allowedOrigins := []string{"http://localhost:8080", "http://app.prod.com"}

	// 3. 创建中间件
	middleware := CheckOrigin(allowedOrigins)
	testHandler := middleware(okHandler)

	// --- 用例 1: 允许的 Origin ---
	t.Run("Valid Origin", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/switch", nil)
		req.Header.Set("Origin", "http://localhost:8080")
		rr := httptest.NewRecorder()

		testHandler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "OK", rr.Body.String())
	})

	// --- 用例 2: 不允许的 Origin ---
	t.Run("Invalid Origin", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/switch", nil)
		req.Header.Set("Origin", "http://evil.com")
		rr := httptest.NewRecorder()

		testHandler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code, "状态码应为 403")
		assert.Contains(t, rr.Body.String(), "非法请求来源", "响应体应包含错误信息")
	})

	// --- 用例 3: 允许的 Referer (当 Origin 为空时) ---
	t.Run("Valid Referer fallback", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/switch", nil)
		// (Origin 为空)
		req.Header.Set("Referer", "http://app.prod.com/some/path")
		rr := httptest.NewRecorder()

		testHandler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "OK", rr.Body.String())
	})

	// --- 用例 4: 无 Origin 的 GET 请求 (安全方法应放行) ---
	t.Run("No Origin GET pass", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/status", nil)
		rr := httptest.NewRecorder()

		testHandler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code, "无 Origin 的 GET 请求应放行")
		assert.Equal(t, "OK", rr.Body.String())
	})

	// --- 用例 5: 无 Origin 的 POST 请求 (状态变更方法应拒绝) ---
	t.Run("No Origin POST blocked", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/switch", nil)
		rr := httptest.NewRecorder()

		testHandler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code, "无 Origin 的 POST 请求应被拒绝")
		assert.Contains(t, rr.Body.String(), "缺少 Origin 头", "响应体应包含错误信息")
	})

	// --- 用例 6: 配置为空时 (应跳过检查) ---
	t.Run("Empty config skips check", func(t *testing.T) {
		emptyMiddleware := CheckOrigin([]string{})
		emptyHandler := emptyMiddleware(okHandler)

		req := httptest.NewRequest("POST", "/api/switch", nil)
		req.Header.Set("Origin", "http://evil.com") // 即使是恶意
		rr := httptest.NewRecorder()

		emptyHandler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code, "当配置为空时，应跳过检查")
	})
}

// 辅助函数：用于捕获 log.Printf 的输出
func captureLog(f func()) string {
	var buf bytes.Buffer
	origOutput := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0) // suppress timestamps for predictable output
	f()
	log.SetOutput(origOutput)
	log.SetFlags(origFlags)
	return buf.String()
}

func resetLimiter() {
	limiter.Lock()
	limiter.requests = make(map[string]*slidingWindow)
	limiter.cleanupCount = 0
	limiter.Unlock()
}

func resetAuthLimiter() {
	authLimiter.Lock()
	authLimiter.states = make(map[string]*authState)
	authLimiter.Unlock()
}

func TestRateLimit(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	t.Run("Requests within limit pass", func(t *testing.T) {
		rl := &rateLimiter{
			requests: make(map[string]*slidingWindow),
			limit:    100,
			window:   time.Minute,
		}
		handler := rl.middleware(false, okHandler)

		for i := 0; i < 10; i++ {
			req := httptest.NewRequest("GET", "/api/test", nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code, "request %d should pass", i)
		}
	})

	t.Run("Requests over limit get 429", func(t *testing.T) {
		rl := &rateLimiter{
			requests: make(map[string]*slidingWindow),
			limit:    5,
			window:   time.Minute,
		}
		handler := rl.middleware(false, okHandler)

		for i := 0; i < 5; i++ {
			req := httptest.NewRequest("GET", "/api/test", nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code, "request %d should pass", i)
		}

		req := httptest.NewRequest("GET", "/api/test", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusTooManyRequests, rr.Code, "6th request should be rate limited")

		var resp ErrorResponse
		err := json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.NoError(t, err)
		assert.Equal(t, "rate_limit", resp.ErrorType)
	})

	t.Run("Different IPs tracked independently", func(t *testing.T) {
		rl := &rateLimiter{
			requests: make(map[string]*slidingWindow),
			limit:    2,
			window:   time.Minute,
		}
		handler := rl.middleware(true, okHandler)

		for i := 0; i < 2; i++ {
			req := httptest.NewRequest("GET", "/api/test", nil)
			req.Header.Set("X-Real-IP", "1.1.1.1")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusOK, rr.Code)
		}

		req := httptest.NewRequest("GET", "/api/test", nil)
		req.Header.Set("X-Real-IP", "1.1.1.1")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusTooManyRequests, rr.Code, "IP 1.1.1.1 should be rate limited")

		req2 := httptest.NewRequest("GET", "/api/test", nil)
		req2.Header.Set("X-Real-IP", "2.2.2.2")
		rr2 := httptest.NewRecorder()
		handler.ServeHTTP(rr2, req2)
		assert.Equal(t, http.StatusOK, rr2.Code, "IP 2.2.2.2 should still be allowed")
	})
}

func TestAuthLimiter(t *testing.T) {
	t.Run("Block after too many failures", func(t *testing.T) {
		resetAuthLimiter()
		authLimiter.limit = 3

		ip := "10.0.0.1"
		assert.False(t, authLimiter.isAuthBlocked(ip))

		for i := 0; i < 3; i++ {
			authLimiter.recordAuthFailure(ip)
		}

		assert.True(t, authLimiter.isAuthBlocked(ip), "IP should be blocked after 3 failures")
	})

	t.Run("Block expires after window", func(t *testing.T) {
		resetAuthLimiter()
		authLimiter.limit = 2
		authLimiter.blockWindow = 50 * time.Millisecond

		ip := "10.0.0.2"
		authLimiter.recordAuthFailure(ip)
		authLimiter.recordAuthFailure(ip)
		assert.True(t, authLimiter.isAuthBlocked(ip))

		time.Sleep(60 * time.Millisecond)

		assert.False(t, authLimiter.isAuthBlocked(ip), "block should have expired")
	})

	t.Run("Different IPs independent", func(t *testing.T) {
		resetAuthLimiter()
		authLimiter.limit = 2

		authLimiter.recordAuthFailure("10.0.0.3")
		authLimiter.recordAuthFailure("10.0.0.3")

		assert.True(t, authLimiter.isAuthBlocked("10.0.0.3"))
		assert.False(t, authLimiter.isAuthBlocked("10.0.0.4"), "different IP should not be blocked")
	})
}
