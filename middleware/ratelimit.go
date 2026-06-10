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

// maxTrackedRateIPs caps the number of unique IPs tracked for rate limiting,
// preventing unbounded memory growth from distributed attacks.
const maxTrackedRateIPs = 10_000

type rateLimiter struct {
	sync.Mutex
	requests map[string]*slidingWindow
	limit    int
	window   time.Duration
}

var limiter = rateLimiter{
	requests: make(map[string]*slidingWindow),
	limit:    100,
	window:   time.Minute,
}

var limiterMu sync.Mutex
var limiterStarted bool
var limiterStop chan struct{}

func InitRateLimiter() {
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
				limiter.cleanupStaleKeys(time.Now())
				limiter.Unlock()
			}
		}
	}()
}

// StopCleanup stops both the rate limiter and auth limiter cleanup goroutines.
// It also resets the "started" flags so a subsequent InitRateLimiter /
// startAuthLimiterCleanup call (e.g. in tests) can restart the goroutines
// cleanly instead of being a no-op.
func StopCleanup() {
	limiterMu.Lock()
	if limiterStop != nil {
		close(limiterStop)
		limiterStop = nil
	}
	limiterStarted = false
	limiterMu.Unlock()

	authCleanupMu.Lock()
	if authCleanupStop != nil {
		close(authCleanupStop)
		authCleanupStop = nil
	}
	authCleanupStarted = false
	authCleanupMu.Unlock()
}

func RateLimit(trustProxy bool, next http.Handler) http.Handler {
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
		if len(rl.requests) >= maxTrackedRateIPs {
			rl.evictOldest()
		}
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
	return true
}

func (rl *rateLimiter) cleanupStaleKeys(now time.Time) {
	for ip, sw := range rl.requests {
		if now.Sub(sw.windowStart) > rl.window*2 {
			delete(rl.requests, ip)
		}
	}
}

// evictOldest removes the entry with the oldest windowStart to make room.
// Uses a linear scan which is acceptable given the infrequent call path
// (only when the map is at capacity) and the bounded map size.
func (rl *rateLimiter) evictOldest() {
	var oldestIP string
	var oldestTime time.Time
	for ip, sw := range rl.requests {
		if oldestIP == "" || sw.windowStart.Before(oldestTime) {
			oldestIP = ip
			oldestTime = sw.windowStart
		}
	}
	if oldestIP != "" {
		delete(rl.requests, oldestIP)
	}
}
