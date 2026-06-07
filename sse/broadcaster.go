package sse

import (
	"bytes"
	"log"
	"sync"
	"time"
)

// RawEvent wraps pre-marshaled JSON bytes. fetchFn returns this so that
// enrichFn can work with the raw JSON without re-parsing, and dedup can
// compare the underlying bytes. Subscribers receive either a RawEvent
// (normal data) or a map (degraded event).
type RawEvent struct {
	JSON []byte
}

// Broadcaster periodically fetches data and fans it out to all subscribers.
// The background polling loop starts lazily on the first Subscribe() call.
// When the last subscriber unsubscribes, the loop continues running for
// idleTimeout before stopping, to avoid rapid start/stop cycles when SSE
// clients briefly disconnect and reconnect.
//
// NOTE: fetchFn is called WITHOUT holding b.mu, so long-running fetches
// (e.g. gRPC calls with multi-second timeouts) will NOT block Subscribe()
// or Unsubscribe(). The mutex is only held during the fan-out phase.
type Broadcaster struct {
	mu          sync.Mutex
	subscribers map[chan any]struct{}
	fetchFn     func() (any, error)
	enrichFn    func(data, prev any, tickAt, prevAt time.Time) any
	interval    time.Duration
	stopCh      chan struct{}
	loopDone    chan struct{}
	stopped     bool
	idleTimer   *time.Timer
	warnOnce    sync.Once

	lastMu        sync.RWMutex
	lastBroadcast any
}

// broadcasterIdleTimeout is how long the polling loop stays alive after the
// last subscriber unsubscribes, allowing quick reconnection without restarting.
const broadcasterIdleTimeout = 10 * time.Second

// NewBroadcaster creates a Broadcaster. fetchFn is called every interval;
// its result is broadcast to all subscribers. If enrichFn is non-nil, it is
// invoked with the current and previous raw payloads plus their tick
// timestamps (captured before the fetch), so it can derive additional
// fields (e.g. BPS from cumulative counters) using the real elapsed time.
// The loop starts on first Subscribe() — no need to call Start() manually.
func NewBroadcaster(fetchFn func() (any, error), enrichFn func(data, prev any, tickAt, prevAt time.Time) any, interval time.Duration) *Broadcaster {
	return &Broadcaster{
		subscribers: make(map[chan any]struct{}),
		fetchFn:     fetchFn,
		enrichFn:    enrichFn,
		interval:    interval,
	}
}

// LastBroadcast returns the most recent enriched broadcast payload.
// Returns nil if no successful broadcast has occurred yet.
// Safe for concurrent use.
func (b *Broadcaster) LastBroadcast() any {
	b.lastMu.RLock()
	defer b.lastMu.RUnlock()
	return b.lastBroadcast
}

// LastBroadcastJSON returns the raw JSON bytes from the most recent broadcast.
// Returns nil if no broadcast has occurred or the payload has no raw JSON.
func (b *Broadcaster) LastBroadcastJSON() []byte {
	b.lastMu.RLock()
	defer b.lastMu.RUnlock()
	switch v := b.lastBroadcast.(type) {
	case RawEvent:
		return v.JSON
	case []byte:
		return v
	default:
		return nil
	}
}

// Stop shuts down the background loop (if running) and closes all subscriber channels.
func (b *Broadcaster) Stop() {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return
	}
	b.stopped = true
	if b.idleTimer != nil {
		b.idleTimer.Stop()
		b.idleTimer = nil
	}
	if b.stopCh != nil {
		close(b.stopCh)
		b.stopCh = nil
	}
	loopDone := b.loopDone
	b.mu.Unlock()

	// Wait for the loop goroutine to fully exit before closing channels,
	// preventing a send-on-closed-channel panic.
	if loopDone != nil {
		<-loopDone
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subscribers {
		close(ch)
	}
	b.subscribers = make(map[chan any]struct{})
}

// Subscribe returns a channel that receives broadcast payloads.
// If this is the first subscriber and the loop is not running, the background
// polling loop starts. Call Unsubscribe when done to clean up.
func (b *Broadcaster) Subscribe() chan any {
	ch := make(chan any, 1)
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.stopped {
		close(ch)
		return ch
	}

	// Cancel any pending idle shutdown — a subscriber is back.
	if b.idleTimer != nil {
		b.idleTimer.Stop()
		b.idleTimer = nil
	}

	wasEmpty := len(b.subscribers) == 0 && b.stopCh == nil
	b.subscribers[ch] = struct{}{}
	if wasEmpty {
		b.stopCh = make(chan struct{})
		b.loopDone = make(chan struct{})
		go b.loop(b.stopCh, b.loopDone)
	}
	return ch
}

// Unsubscribe removes the subscriber. If this was the last subscriber,
// the background polling loop is scheduled to stop after broadcasterIdleTimeout
// to allow quick reconnection without restarting the polling goroutine.
func (b *Broadcaster) Unsubscribe(ch chan any) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.subscribers, ch)
	if len(b.subscribers) == 0 && b.stopCh != nil && b.idleTimer == nil {
		b.idleTimer = time.AfterFunc(broadcasterIdleTimeout, func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if len(b.subscribers) == 0 && b.stopCh != nil {
				close(b.stopCh)
				b.stopCh = nil
			}
			b.idleTimer = nil
		})
	}
}

// maxConsecutiveErrors is the number of consecutive fetch failures before
// a degraded event is pushed to subscribers so they know data is stale.
const maxConsecutiveErrors = 3

func (b *Broadcaster) loop(stopCh chan struct{}, loopDone chan struct{}) {
	defer close(loopDone)

	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	var lastSnapshot any
	var lastAt time.Time
	consecutiveErrors := 0

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			tickAt := time.Now()
			data, err := b.fetchFn()
			if err != nil {
				consecutiveErrors++
				log.Printf("警告: 统计数据获取失败 (连续第%d次): %v", consecutiveErrors, err)
				if consecutiveErrors == maxConsecutiveErrors {
					degradedEvent := map[string]any{"degraded": true, "error": "数据源不可用"}
					b.mu.Lock()
					for ch := range b.subscribers {
						select {
						case ch <- degradedEvent:
						default:
						}
					}
					b.mu.Unlock()
				}
				continue
			}

			consecutiveErrors = 0

			// Defensive type assertion at the fetch entry point.
			// The stats publisher emits RawEvent; any other type bypasses
			// dedup silently. Warn once per Broadcaster so developers
			// notice the mismatch without being flooded in logs.
			if _, ok := data.(RawEvent); !ok {
				b.warnOnce.Do(func() {
					log.Printf("警告: Broadcaster fetchFn 返回非 RawEvent 类型 %T，去重将被跳过", data)
				})
			}

			// Dedup uses the raw snapshot so cumulative counters decide equality;
			// derived fields (e.g. BPS) would otherwise defeat dedup.
			//
			// Dedup is scoped to RawEvent (byte-level JSON comparison) because
			// that is what the stats publisher emits. Non-RawEvent values bypass
			// dedup and are broadcast unconditionally — if a new payload type
			// is introduced and dedup is desired, it must be handled here.
			if lastSnapshot != nil {
				if curr, ok := data.(RawEvent); ok {
					if prev, ok := lastSnapshot.(RawEvent); ok {
						if bytes.Equal(curr.JSON, prev.JSON) {
							continue
						}
					}
				}
			}

			broadcast := data
			if b.enrichFn != nil {
				broadcast = b.enrichFn(data, lastSnapshot, tickAt, lastAt)
			}
			lastSnapshot = data
			lastAt = tickAt

			b.lastMu.Lock()
			b.lastBroadcast = broadcast
			b.lastMu.Unlock()

			b.mu.Lock()
			for ch := range b.subscribers {
				select {
				case ch <- broadcast:
				default:
				}
			}
			b.mu.Unlock()
		}
	}
}
