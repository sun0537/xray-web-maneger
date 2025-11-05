package middleware

import (
	"log"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"
)

// --- 1. 日志中间件 (Logger) --- 以捕获状态码
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}
func (lrw *loggingResponseWriter) Flush() {
	flusher, ok := lrw.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// 包装 ResponseWriter
		lrw := &loggingResponseWriter{w, http.StatusOK}

		// 调用下一个中间件或处理器
		next.ServeHTTP(lrw, r)

		log.Printf("%s %s %d %v", r.Method, r.URL.Path, lrw.statusCode, time.Since(start))
	})
}

// --- 2. panic恢复中间件 (Recovery) ---
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				// 打印完整的堆栈跟踪
				log.Printf("!!! HTTP Handler Panic: %v", err)
				log.Printf("!!! Stacktrace:\n%s", debug.Stack())

				// 隐藏内部错误，只返回 500
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// --- 3. 来源检查中间件 (CheckOrigin) ---
// CheckOrigin 是一个“工厂函数”，它接收配置并返回一个中间件
func CheckOrigin(allowedOrigins []string) func(http.Handler) http.Handler {
	// 如果配置为空，返回一个“什么都不做”的中间件
	if len(allowedOrigins) == 0 {
		return func(next http.Handler) http.Handler {
			return next // 直接传递
		}
	}

	// 提前创建 map 以提高查找效率
	allowedMap := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowedMap[origin] = true
	}
	log.Printf("安全: 已启用 Origin 检查, 允许的来源: %v", allowedOrigins)

	// 这是返回的实际中间件
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. 优先检查 Origin 头部
			origin := r.Header.Get("Origin")

			// 2. 回退检查 Referer
			if origin == "" {
				referer := r.Header.Get("Referer")
				if referer != "" {
					u, err := url.Parse(referer)
					if err == nil {
						origin = u.Scheme + "://" + u.Host
					}
				}
			}

			// 3. 移除末尾斜杠
			origin = strings.TrimRight(origin, "/")

			// 4. 验证
			if !allowedMap[origin] {
				log.Printf("警告: 拒绝了来自非法 Origin 的请求: %s", origin)
				// (使用 handlers.go 中的 jsonError 格式)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":"非法请求来源 (Invalid Origin)"}`))
				return
			}

			// 5. 验证通过
			next.ServeHTTP(w, r)
		})
	}
}
