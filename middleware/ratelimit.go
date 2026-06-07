package middleware

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type slidingWindow struct {
	prevCount   int
	currCount   int
	windowStart time.Time
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

var limiterMu sync.Mutex
var limiterStarted bool
var limiterStop chan struct{}

func startRateLimiterCleanup() {
	limiterMu.Lock()
	defer limiterMu.Unlock()
	if limiterStarted {
		return
	}
	limiterStarted = true
	limiterStop = make(chan struct{})
	stopCh := limiterStop // capture locally to avoid data race in select
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
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
}

// StopCleanup stops both the rate limiter and auth limiter cleanup goroutines.
func StopCleanup() {
	limiterMu.Lock()
	if limiterStop != nil {
		close(limiterStop)
		limiterStop = nil
	}
	limiterMu.Unlock()

	authCleanupMu.Lock()
	if authCleanupStop != nil {
		close(authCleanupStop)
		authCleanupStop = nil
	}
	authCleanupMu.Unlock()
}

func RateLimit(trustProxy bool, next http.Handler) http.Handler {
	startRateLimiterCleanup()
	return limiter.middleware(trustProxy, next)
}

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
			_, _ = w.Write(body)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *rateLimiter) checkAndRecord(ip string) bool {
	rl.Lock()
	defer rl.Unlock()

	now := time.Now()
	sw, exists := rl.requests[ip]

	if !exists {
		rl.requests[ip] = &slidingWindow{currCount: 1, windowStart: now}
		return true
	}

	elapsed := now.Sub(sw.windowStart)

	if elapsed > rl.window*2 {
		sw.currCount = 1
		sw.prevCount = 0
		sw.windowStart = now
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
		return false
	}

	sw.currCount++

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
