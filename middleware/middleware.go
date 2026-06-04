package middleware

import (
	"context"
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

func (lrw *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return lrw.ResponseWriter
}

var writerPool = sync.Pool{
	New: func() interface{} {
		return &loggingResponseWriter{}
	},
}

type ctxKey struct{}

var clientIPKey ctxKey

var trustProxyHeaders bool

// SetTrustProxyHeaders configures whether to trust X-Real-IP / X-Forwarded-For headers.
func SetTrustProxyHeaders(trust bool) {
	trustProxyHeaders = trust
}

func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, trustProxyHeaders)
		r = r.WithContext(context.WithValue(r.Context(), clientIPKey, ip))

		start := time.Now()
		lrw := writerPool.Get().(*loggingResponseWriter)
		lrw.ResponseWriter = w
		lrw.statusCode = http.StatusOK
		defer func() {
			log.Printf("%s %s %s %d %v", ip, r.Method, r.URL.Path, lrw.statusCode, time.Since(start))
			lrw.ResponseWriter = nil
			writerPool.Put(lrw)
		}()
		next.ServeHTTP(lrw, r)
	})
}

// GetClientIP returns the client IP cached by the Logger middleware.
// Falls back to clientIP(r, trustProxyHeaders) if Logger is not in the chain.
func GetClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey).(string); ok {
		return ip
	}
	return clientIP(r, trustProxyHeaders)
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

	allowedMap := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowedMap[origin] = struct{}{}
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
				switch r.Method {
				case http.MethodGet, http.MethodHead, http.MethodOptions:
					// Safe methods: allow without Origin (same-origin navigation, etc.)
				default:
					log.Printf("警告: 拒绝了缺少 Origin 头的状态变更请求: %s %s", r.Method, r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					w.Write([]byte(`{"error":"缺少 Origin 头 (Missing Origin header)"}`))
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			origin = strings.TrimRight(origin, "/")

			if _, ok := allowedMap[origin]; !ok {
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
	prevCount   int
	currCount   int
	windowStart time.Time
}

type rateLimiter struct {
	sync.Mutex
	requests     map[string]slidingWindow
	limit        int
	window       time.Duration
	cleanupCount int
}

var limiter = rateLimiter{
	requests: make(map[string]slidingWindow),
	limit:    100,
	window:   time.Minute,
}

var limiterOnce sync.Once

func startCleanup(ctx context.Context) {
	limiterOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(2 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					limiter.Lock()
					now := time.Now()
					for ip, sw := range limiter.requests {
						if now.Sub(sw.windowStart) > limiter.window*2 {
							delete(limiter.requests, ip)
						}
					}
					limiter.Unlock()
				}
			}
		}()
	})
}

// checkAndRecord atomically checks the rate limit and records the request.
// Uses a sliding window algorithm: the effective count blends the previous
// window's count (weighted by overlap) with the current window's count.
// Returns true if the request should be allowed.
func (rl *rateLimiter) checkAndRecord(ip string) bool {
	rl.Lock()
	defer rl.Unlock()

	now := time.Now()
	sw, exists := rl.requests[ip]

	if !exists {
		rl.requests[ip] = slidingWindow{currCount: 1, windowStart: now}
		return true
	}

	elapsed := now.Sub(sw.windowStart)

	if elapsed > rl.window*2 {
		rl.requests[ip] = slidingWindow{currCount: 1, windowStart: now}
		return true
	}

	if elapsed > rl.window {
		sw.prevCount = sw.currCount
		sw.currCount = 0
		sw.windowStart = sw.windowStart.Add(rl.window * (elapsed / rl.window))
		elapsed = now.Sub(sw.windowStart)
	}

	overlap := 1.0 - float64(elapsed)/float64(rl.window)
	effective := float64(sw.prevCount)*overlap + float64(sw.currCount)

	if effective >= float64(rl.limit) {
		rl.requests[ip] = sw
		return false
	}

	sw.currCount++
	rl.requests[ip] = sw

	rl.cleanupCount++
	if rl.cleanupCount >= 100 {
		rl.cleanupStaleKeys(now)
		rl.cleanupCount = 0
	}
	return true
}

func (rl *rateLimiter) cleanupStaleKeys(now time.Time) {
	for ip, sw := range rl.requests {
		if now.Sub(sw.windowStart) > rl.window {
			delete(rl.requests, ip)
		}
	}
}

// clientIP extracts the client IP from request headers or RemoteAddr.
// When trustProxy is false, only RemoteAddr is used to prevent IP spoofing.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if idx := strings.Index(xff, ","); idx > 0 {
				return strings.TrimSpace(xff[:idx])
			}
			return strings.TrimSpace(xff)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// 速率限制中间件
func RateLimit(ctx context.Context, next http.Handler) http.Handler {
	startCleanup(ctx)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, trustProxyHeaders)
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
