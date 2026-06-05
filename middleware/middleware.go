package middleware

import (
	"context"
	"crypto/subtle"
	"encoding/json"
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

type ctxKey struct{}

var clientIPKey ctxKey

func Logger(trustProxy bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, trustProxy)
		r = r.WithContext(context.WithValue(r.Context(), clientIPKey, ip))

		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		defer func() {
			log.Printf("%s %s %s %d %v", ip, r.Method, r.URL.Path, lrw.statusCode, time.Since(start))
		}()
		next.ServeHTTP(lrw, r)
	})
}

// GetClientIP returns the client IP cached by the Logger middleware.
// Falls back to clientIP(r, false) if Logger is not in the chain.
func GetClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey).(string); ok {
		return ip
	}
	return clientIP(r, false)
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

var limiterMu sync.Mutex
var limiterStarted bool
var limiterStop chan struct{}

func startCleanup() {
	limiterMu.Lock()
	defer limiterMu.Unlock()
	if limiterStarted {
		return
	}
	limiterStarted = true
	limiterStop = make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-limiterStop:
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

				authLimiter.Lock()
				for ip, t := range authLimiter.blockedAt {
					if time.Since(t) >= authLimiter.blockWindow {
						delete(authLimiter.blockedAt, ip)
						delete(authLimiter.attempts, ip)
					}
				}
				for ip := range authLimiter.attempts {
					if _, blocked := authLimiter.blockedAt[ip]; !blocked {
						delete(authLimiter.attempts, ip)
					}
				}
				authLimiter.Unlock()
			}
		}
	}()
}

// StopCleanup stops the background cleanup goroutine for the rate limiter.
// Call this during server shutdown to release resources.
func StopCleanup() {
	limiterMu.Lock()
	defer limiterMu.Unlock()
	if limiterStop != nil {
		close(limiterStop)
		limiterStop = nil
	}
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
		// Integer division (elapsed/rl.window) floors to the number of complete
		// windows to advance. This is intentional: the previous window's count
		// is blended via overlap weighting, and any gap beyond one full window
		// is absorbed by the reset of currCount.
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
		if now.Sub(sw.windowStart) > rl.window*2 {
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

// ErrorResponse is the standard JSON body for error responses.
type ErrorResponse struct {
	Error     string `json:"error"`
	ErrorType string `json:"error_type"`
}

// simpleAuthRateLimit tracks failed authentication attempts per IP.
// It uses a simple counter (not sliding window) since brute-force
// protection is more important than smooth rate limiting here.
type simpleAuthRateLimit struct {
	sync.Mutex
	attempts    map[string]int
	blockedAt   map[string]time.Time
	limit       int
	blockWindow time.Duration
}

var authLimiter = simpleAuthRateLimit{
	attempts:    make(map[string]int),
	blockedAt:   make(map[string]time.Time),
	limit:       5,
	blockWindow: 5 * time.Minute,
}

// isAuthBlocked checks if an IP is currently blocked from authentication.
func (a *simpleAuthRateLimit) isAuthBlocked(ip string) bool {
	a.Lock()
	defer a.Unlock()

	if blocked, ok := a.blockedAt[ip]; ok {
		if time.Since(blocked) < a.blockWindow {
			return true
		}
		// Block expired, reset
		delete(a.blockedAt, ip)
		a.attempts[ip] = 0
	}
	return false
}

// recordAuthFailure increments the failure counter and blocks the IP
// if the limit is exceeded.
func (a *simpleAuthRateLimit) recordAuthFailure(ip string) {
	a.Lock()
	defer a.Unlock()

	a.attempts[ip]++
	if a.attempts[ip] >= a.limit {
		a.blockedAt[ip] = time.Now()
		log.Printf("安全: IP %s 因认证失败次数过多已被临时封锁 %v", ip, a.blockWindow)
	}
}

// RateLimit creates a middleware that limits requests per IP using the global limiter.
func RateLimit(trustProxy bool, next http.Handler) http.Handler {
	startCleanup()
	return limiter.middleware(trustProxy, next)
}

// middleware returns an http.Handler that enforces rate limiting on this instance.
func (rl *rateLimiter) middleware(trustProxy bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, trustProxy)
		if !rl.checkAndRecord(ip) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			body, _ := json.Marshal(ErrorResponse{
				Error:     "请求过于频繁，请稍后再试",
				ErrorType: "rate_limit",
			})
			w.Write(body)
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
			ip := GetClientIP(r)

			// Check if this IP is temporarily blocked due to too many auth failures
			if authLimiter.isAuthBlocked(ip) {
				w.Header().Set("Retry-After", "300")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(ErrorResponse{
					Error:     "认证失败次数过多，请 5 分钟后再试",
					ErrorType: "auth_rate_limit",
				})
				return
			}

			user, pass, ok := r.BasicAuth()
			if !ok ||
				subtle.ConstantTimeCompare([]byte(user), []byte(username)) != 1 ||
				subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
				authLimiter.recordAuthFailure(ip)
				w.Header().Set("WWW-Authenticate", `Basic realm="Xray Manager"`)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				body, err := json.Marshal(ErrorResponse{
					Error:     "Unauthorized",
					ErrorType: "auth",
				})
				if err != nil {
					w.Write([]byte(`{"error":"Unauthorized","error_type":"auth"}`))
				} else {
					w.Write(body)
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
