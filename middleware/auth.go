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
			ip := GetClientIP(r)

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
					Error:     "未授权 (Unauthorized)",
					ErrorType: "auth",
				})
				if err != nil {
					w.Write([]byte(`{"error":"未授权 (Unauthorized)","error_type":"auth"}`))
				} else {
					w.Write(body)
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

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

func (a *simpleAuthRateLimit) isAuthBlocked(ip string) bool {
	a.Lock()
	defer a.Unlock()

	if blocked, ok := a.blockedAt[ip]; ok {
		if time.Since(blocked) < a.blockWindow {
			return true
		}
		delete(a.blockedAt, ip)
		a.attempts[ip] = 0
	}
	return false
}

func (a *simpleAuthRateLimit) recordAuthFailure(ip string) {
	a.Lock()
	defer a.Unlock()

	a.attempts[ip]++
	if a.attempts[ip] >= a.limit {
		a.blockedAt[ip] = time.Now()
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
				for ip, t := range authLimiter.blockedAt {
					if now.Sub(t) >= authLimiter.blockWindow {
						delete(authLimiter.blockedAt, ip)
						delete(authLimiter.attempts, ip)
					}
				}
				for ip, count := range authLimiter.attempts {
					if _, blocked := authLimiter.blockedAt[ip]; !blocked {
						if count == 0 {
							delete(authLimiter.attempts, ip)
						}
					}
				}
				authLimiter.Unlock()
			}
		}
	}()
}
