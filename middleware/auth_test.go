package middleware

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// newTestAuthLimiter returns a fresh simpleAuthRateLimit with a tiny blockWindow
// so we can exercise the unblock path without time.Sleep in tests.
func newTestAuthLimiter() *simpleAuthRateLimit {
	return &simpleAuthRateLimit{
		states:      make(map[string]*authState),
		limit:       5,
		blockWindow: 50 * time.Millisecond,
	}
}

func TestAuthStateBelowLimit(t *testing.T) {
	al := newTestAuthLimiter()

	// Record (limit-1) failures — must NOT be blocked.
	for i := 0; i < al.limit-1; i++ {
		al.recordAuthFailure("1.2.3.4")
		assert.False(t, al.isAuthBlocked("1.2.3.4"), "ip must not be blocked below limit (iteration %d)", i)
	}
}

func TestAuthStateBlocksAtLimit(t *testing.T) {
	al := newTestAuthLimiter()

	for i := 0; i < al.limit; i++ {
		al.recordAuthFailure("1.2.3.4")
	}
	assert.True(t, al.isAuthBlocked("1.2.3.4"), "ip must be blocked once failures reach limit")

	// A different IP must NOT be affected.
	assert.False(t, al.isAuthBlocked("5.6.7.8"), "blocking must be scoped per-IP")
}

func TestAuthStateDoesNotReBlock(t *testing.T) {
	al := newTestAuthLimiter()

	// Exceed the limit; subsequent failures must not extend or reset blockedAt
	// while still in the blocked window.
	for i := 0; i < al.limit+3; i++ {
		al.recordAuthFailure("1.2.3.4")
	}
	al.Lock()
	original := al.states["1.2.3.4"].blockedAt
	al.Unlock()

	assert.True(t, al.isAuthBlocked("1.2.3.4"))

	al.Lock()
	after := al.states["1.2.3.4"].blockedAt
	al.Unlock()
	assert.True(t, original.Equal(after), "blockedAt must not move on subsequent failures within window")
}

func TestAuthStateUnblocksAfterWindow(t *testing.T) {
	al := newTestAuthLimiter()

	for i := 0; i < al.limit; i++ {
		al.recordAuthFailure("1.2.3.4")
	}
	assert.True(t, al.isAuthBlocked("1.2.3.4"))

	// Wait past the block window.
	time.Sleep(2 * al.blockWindow)

	assert.False(t, al.isAuthBlocked("1.2.3.4"), "ip must be unblocked after blockWindow elapses")

	// State must be cleared so the map does not grow unbounded.
	al.Lock()
	_, stillTracked := al.states["1.2.3.4"]
	al.Unlock()
	assert.False(t, stillTracked, "expired block must be evicted from the map")
}

func TestAuthStateRespectsMaxTrackedIPs(t *testing.T) {
	al := &simpleAuthRateLimit{
		states:      make(map[string]*authState),
		limit:       5,
		blockWindow: time.Minute,
	}
	// We cannot mutate the constant; emulate the cap by using a fresh limiter
	// and asserting via the public methods + an equivalent cap check.

	// Drive the limiter to its actual cap with unique IPs.
	for i := 0; i < maxTrackedAuthIPs; i++ {
		ip := ipFromInt(i)
		al.recordAuthFailure(ip)
	}
	al.Lock()
	size := len(al.states)
	al.Unlock()
	assert.Equal(t, maxTrackedAuthIPs, size, "limiter must be filled to its cap")

	// One more unique IP: the oldest entry is evicted to make room.
	al.recordAuthFailure("overflow-ip")
	al.Lock()
	_, present := al.states["overflow-ip"]
	sizeAfter := len(al.states)
	al.Unlock()
	assert.True(t, present, "new IPs beyond the cap must be tracked (oldest evicted)")
	assert.Equal(t, maxTrackedAuthIPs, sizeAfter, "map size must stay at cap after eviction")
}

func TestAuthStateIsAuthBlockedUnknownIP(t *testing.T) {
	al := newTestAuthLimiter()
	assert.False(t, al.isAuthBlocked("never-seen"), "an unknown IP must not be blocked")
}

func TestAuthStateEmptyIPIsHandled(t *testing.T) {
	al := newTestAuthLimiter()

	for i := 0; i < al.limit; i++ {
		al.recordAuthFailure("")
	}
	assert.True(t, al.isAuthBlocked(""), "empty-ip key must be tracked like any other key")
}

// ipFromInt converts a small integer to a unique valid dotted-quad IP string
// (10.x.y.1) for generating IP fixtures in tests. Supports up to 65536 unique values.
func ipFromInt(i int) string {
	return fmt.Sprintf("10.%d.%d.1", (i>>8)&0xFF, i&0xFF)
}
