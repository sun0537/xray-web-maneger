package middleware

import (
	"crypto/subtle"
	"log"
	"net"
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

var writerPool = sync.Pool{
	New: func() interface{} {
		return &loggingResponseWriter{}
	},
}

func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := writerPool.Get().(*loggingResponseWriter)
		lrw.ResponseWriter = w
		lrw.statusCode = http.StatusOK
		defer func() {
			log.Printf("%s %s %d %v", r.Method, r.URL.Path, lrw.statusCode, time.Since(start))
			lrw.ResponseWriter = nil
			writerPool.Put(lrw)
		}()
		next.ServeHTTP(lrw, r)
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
	sync.Mutex
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

var limiterOnce sync.Once

func startCleanup() {
	limiterOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(2 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				limiter.Lock()
				now := time.Now()
				for ip, sw := range limiter.requests {
					if now.Sub(sw.startTime) > limiter.window {
						delete(limiter.requests, ip)
					}
				}
				limiter.Unlock()
			}
		}()
	})
}

// checkAndRecord atomically checks the rate limit and records the request.
// Returns true if the request should be allowed.
func (rl *rateLimiter) checkAndRecord(ip string) bool {
	rl.Lock()
	defer rl.Unlock()

	now := time.Now()

	sw, exists := rl.requests[ip]
	if !exists || now.Sub(sw.startTime) > rl.window {
		rl.requests[ip] = &slidingWindow{count: 1, startTime: now}
		return true
	}

	if sw.count >= rl.limit {
		return false
	}

	sw.count++

	rl.cleanupCount++
	if rl.cleanupCount >= 100 {
		rl.cleanupStaleKeys(now)
		rl.cleanupCount = 0
	}
	return true
}

func (rl *rateLimiter) cleanupStaleKeys(now time.Time) {
	for ip, sw := range rl.requests {
		if now.Sub(sw.startTime) > rl.window*2 {
			delete(rl.requests, ip)
		}
	}
}

// getClientIP extracts the client IP from RemoteAddr.
func getClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// 速率限制中间件
func RateLimit(next http.Handler) http.Handler {
	startCleanup()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)
		if !limiter.checkAndRecord(ip) {
			http.Error(w, "请求过于频繁，请稍后再试", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; script-src 'self'")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func BasicAuth(username, password string) func(http.Handler) http.Handler {
	if username == "" || password == "" {
		log.Println("警告: BasicAuth 未配置 (username/password 为空)，API 将不进行认证保护")
		return func(next http.Handler) http.Handler { return next }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok ||
				subtle.ConstantTimeCompare([]byte(user), []byte(username)) != 1 ||
				subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="Xray Manager"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
