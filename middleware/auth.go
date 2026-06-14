package middleware

import (
	"crypto/subtle"
	"encoding/json"
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

	startAuthLimiterCleanup()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ClientIPFromContext(r)

			if authLimiter.isAuthBlocked(ip) {
				w.Header().Set("Retry-After", "300")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				body, _ := json.Marshal(ErrorResponse{
					Error:     "认证失败次数过多，请 5 分钟后再试",
					ErrorType: "auth_rate_limit",
				})
				_, _ = w.Write(body)
				return
			}

			user, pass, ok := r.BasicAuth()
			if !ok {
				// 浏览器尚未缓存凭证（用户还没输入密码），不计入失败次数
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	body, err := json.Marshal(ErrorResponse{
		Error:     "未授权 (Unauthorized)",
		ErrorType: "auth",
	})
	if err != nil {
		_, _ = w.Write([]byte(`{"error":"未授权 (Unauthorized)","error_type":"auth"}`))
	} else {
		_, _ = w.Write(body)
	}
}

// maxTrackedAuthIPs caps the number of unique IPs tracked for auth failures,
// preventing unbounded memory growth from distributed brute-force attacks.
const maxTrackedAuthIPs = 10_000

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
		if len(a.states) >= maxTrackedAuthIPs {
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

func startAuthLimiterCleanup() {
	authCleanupMu.Lock()
	defer authCleanupMu.Unlock()
	if authCleanupStarted {
		return
	}
	authCleanupStarted = true
	authCleanupStop = make(chan struct{})
	stopCh := authCleanupStop // capture locally to avoid data race in select
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				authLimiter.Lock()
				now := time.Now()
				for ip, st := range authLimiter.states {
					if !st.blockedAt.IsZero() && now.Sub(st.blockedAt) >= authLimiter.blockWindow {
						delete(authLimiter.states, ip)
						continue
					}
					if st.blockedAt.IsZero() && now.Sub(st.lastAttempt) >= authLimiter.blockWindow {
						delete(authLimiter.states, ip)
					}
				}
				authLimiter.Unlock()
			}
		}
	}()
}
