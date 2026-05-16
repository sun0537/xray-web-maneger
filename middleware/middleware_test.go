package middleware

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

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

	// --- 用例 4: 无 Origin / Referer (同源请求应放行) ---
	t.Run("No Origin same-origin pass", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/switch", nil)
		rr := httptest.NewRecorder()

		testHandler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code, "无 Origin 的同源请求应放行")
		assert.Equal(t, "OK", rr.Body.String())
	})

	// --- 用例 5: 配置为空时 (应跳过检查) ---
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
	log.SetOutput(&buf)
	f()
	log.SetOutput(os.Stderr)
	return buf.String()
}
