package middleware

import (
	"crypto/subtle"
	"log"
	"net/http"
	"sync"
	"time"
)

func BasicAuth(username, password string) func(http.Handler) http.Handler {
	if username == "" || password == "" {
		log.Println("警告: BasicAuth 未配置 (username/password 为空)，API 将不进行认证保护")
		return func(next http.Handler) http.Handler { return next }
	}

	startCleanupLoop(&authCleanupMu, &authCleanupStarted, &authCleanupStop,
		2*time.Minute, func() {
			cleanupRateLimiter(&authLimiter)
			cleanupRateLimiter(&noCredLimiter)
		})

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ClientIPFromContext(r)

			if authLimiter.isAuthBlocked(ip) {
				w.Header().Set("Retry-After", "300")
				WriteJSONError(w, "认证失败次数过多，请 5 分钟后再试", http.StatusTooManyRequests, "auth_rate_limit")
				return
			}

			user, pass, ok := r.BasicAuth()
			if !ok {
				// 浏览器尚未缓存凭证（用户还没输入密码），使用独立限流防止无限探测
				if noCredLimiter.isAuthBlocked(ip) {
					w.Header().Set("Retry-After", "60")
					WriteJSONError(w, "请求过于频繁，请稍后再试", http.StatusTooManyRequests, "rate_limit")
					return
				}
				noCredLimiter.recordAuthFailure(ip)
				writeUnauthorized(w)
				return
			}
			if subtle.ConstantTimeCompare([]byte(user), []byte(username)) != 1 ||
				subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
				authLimiter.recordAuthFailure(ip)
				writeUnauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeUnauthorized sends a 401 response with a WWW-Authenticate challenge
// and a JSON error body. Used by both the "no credentials" and "wrong
// credentials" paths in BasicAuth.
func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Xray Manager"`)
	WriteJSONError(w, "未授权 (Unauthorized)", http.StatusUnauthorized, "auth")
}

// maxTrackedIPs is defined in middleware.go and shared across rate limiters.

// authState tracks the auth-failure state for a single IP. Embedding both
// timestamps in a single value lets the limiter use one map instead of three,
// reducing memory and keeping related state co-located.
type authState struct {
	attempts    int
	lastAttempt time.Time
	blockedAt   time.Time
}

type simpleAuthRateLimit struct {
	sync.Mutex
	// states uses pointer values so mutations propagate without an explicit
	// write-back. This matches ratelimit.go's map[string]*slidingWindow and
	// removes the value-semantic trap where forgetting to write back would
	// silently drop the increment.
	states      map[string]*authState
	limit       int
	blockWindow time.Duration
}

var authLimiter = simpleAuthRateLimit{
	states:      make(map[string]*authState),
	limit:       5,
	blockWindow: 5 * time.Minute,
}

// noCredLimiter rate-limits requests that arrive without any Authorization
// header. This prevents attackers from using unauthenticated requests to
// probe the service without triggering the stricter auth-failure limiter.
// The limit is intentionally more lenient than authLimiter because browser
// first-loads and legitimate health checks may legitimately lack credentials.
var noCredLimiter = simpleAuthRateLimit{
	states:      make(map[string]*authState),
	limit:       30,
	blockWindow: 1 * time.Minute,
}

func (a *simpleAuthRateLimit) isAuthBlocked(ip string) bool {
	a.Lock()
	defer a.Unlock()

	st, ok := a.states[ip]
	if !ok {
		return false
	}
	if !st.blockedAt.IsZero() && time.Since(st.blockedAt) < a.blockWindow {
		return true
	}
	if !st.blockedAt.IsZero() {
		delete(a.states, ip)
	}
	return false
}

func (a *simpleAuthRateLimit) recordAuthFailure(ip string) {
	a.Lock()
	defer a.Unlock()

	st, tracked := a.states[ip]
	if !tracked {
		if len(a.states) >= maxTrackedIPs {
			// Evict a random entry to make room. True LRU eviction would
			// require O(n) scan; random eviction is O(1) and good enough
			// for a security rate limiter where the exact victim doesn't matter.
			for k := range a.states {
				delete(a.states, k)
				break
			}
		}
		st = &authState{}
		a.states[ip] = st
	}

	st.attempts++
	st.lastAttempt = time.Now()
	if st.attempts >= a.limit && st.blockedAt.IsZero() {
		st.blockedAt = time.Now()
		log.Printf("安全: IP %s 因认证失败次数过多已被临时封锁 %v", ip, a.blockWindow)
	}
}

var authCleanupMu sync.Mutex
var authCleanupStarted bool
var authCleanupStop chan struct{}

// cleanupRateLimiter removes expired entries from a rate limiter.
func cleanupRateLimiter(a *simpleAuthRateLimit) {
	a.Lock()
	defer a.Unlock()
	now := time.Now()
	for ip, st := range a.states {
		if !st.blockedAt.IsZero() && now.Sub(st.blockedAt) >= a.blockWindow {
			delete(a.states, ip)
			continue
		}
		if st.blockedAt.IsZero() && now.Sub(st.lastAttempt) >= a.blockWindow {
			delete(a.states, ip)
		}
	}
}
