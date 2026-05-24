package middleware

import (
	"log"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

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
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(lrw, r)
		log.Printf("%s %s %d %v", r.Method, r.URL.Path, lrw.statusCode, time.Since(start))
	})
}

func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("!!! HTTP Handler Panic: %v", err)
				log.Printf("!!! Stacktrace:\n%s", debug.Stack())
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func CheckOrigin(allowedOrigins []string) func(http.Handler) http.Handler {
	if len(allowedOrigins) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	allowedMap := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowedMap[origin] = true
	}
	log.Printf("安全: 已启用 Origin 检查, 允许的来源: %v", allowedOrigins)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin == "" {
				referer := r.Header.Get("Referer")
				if referer != "" {
					if u, err := url.Parse(referer); err == nil {
						origin = u.Scheme + "://" + u.Host
					}
				}
			}

			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			origin = strings.TrimRight(origin, "/")

			if !allowedMap[origin] {
				log.Printf("警告: 拒绝了来自非法 Origin 的请求: %s", origin)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":"非法请求来源 (Invalid Origin)"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// 速率限制相关数据结构
type slidingWindow struct {
	count     int
	startTime time.Time
}

type rateLimiter struct {
	sync.RWMutex
	requests     map[string]*slidingWindow
	limit        int
	window       time.Duration
	cleanupCount int
}

var limiter = rateLimiter{
	requests: make(map[string]*slidingWindow),
	limit:    100,
	window:   time.Minute,
}

func (rl *rateLimiter) isRateLimited(ip string) bool {
	rl.RLock()
	defer rl.RUnlock()

	sw, exists := rl.requests[ip]
	if !exists {
		return false
	}

	now := time.Now()
	if now.Sub(sw.startTime) > rl.window {
		return false
	}

	return sw.count >= rl.limit
}

func (rl *rateLimiter) recordRequest(ip string) {
	rl.Lock()
	defer rl.Unlock()

	now := time.Now()

	sw, exists := rl.requests[ip]
	if !exists || now.Sub(sw.startTime) > rl.window {
		rl.requests[ip] = &slidingWindow{count: 1, startTime: now}
	} else {
		sw.count++
	}

	rl.cleanupCount++
	if rl.cleanupCount >= 100 {
		rl.cleanupStaleKeys(now)
		rl.cleanupCount = 0
	}
}

func (rl *rateLimiter) cleanupStaleKeys(now time.Time) {
	for ip, sw := range rl.requests {
		if now.Sub(sw.startTime) > rl.window*2 {
			delete(rl.requests, ip)
		}
	}
}

// 速率限制中间件
func RateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if limiter.isRateLimited(ip) {
			http.Error(w, "请求过于频繁，请稍后再试", http.StatusTooManyRequests)
			return
		}
		limiter.recordRequest(ip)
		next.ServeHTTP(w, r)
	})
}

func BasicAuth(username, password string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if username == "" || password == "" {
				next.ServeHTTP(w, r)
				return
			}

			user, pass, ok := r.BasicAuth()
			if !ok || user != username || pass != password {
				w.Header().Set("WWW-Authenticate", `Basic realm="Xray Manager"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
