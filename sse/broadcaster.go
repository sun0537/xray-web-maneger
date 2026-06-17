package sse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
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
	loopGen     uint64
	stopped     bool
	idleTimer   *time.Timer
	stopOnce    sync.Once

	lastMu        sync.RWMutex
	lastBroadcast any

	droppedMsgs atomic.Int64
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

// Stop shuts down the background loop (if running) and closes all subscriber channels.
func (b *Broadcaster) Stop() {
	b.stopOnce.Do(func() {
		b.mu.Lock()
		b.stopped = true
		b.loopGen++
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
		for ch := range b.subscribers {
			close(ch)
		}
		b.subscribers = make(map[chan any]struct{})
		b.mu.Unlock()
	})
}

// Subscribe returns a channel that receives broadcast payloads.
// If this is the first subscriber and the loop is not running, the background
// polling loop starts. Call Unsubscribe when done to clean up.
func (b *Broadcaster) Subscribe() chan any {
	ch := make(chan any, 5)
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
		// Wait for any previous loop to fully exit before starting a new
		// one. Without this, the idle-timer path (Unsubscribe → timer →
		// close(stopCh); stopCh=nil) can race with Subscribe: the old
		// loop hasn't exited yet but stopCh is already nil, so Subscribe
		// starts a second loop → duplicate fetches and broadcasts.
		//
		// The generation counter disambiguates what happened while we
		// waited without the lock: if it changed, another Subscribe
		// already started a new loop, or Stop() was called — either way,
		// we must not start a second loop.
		if b.loopDone != nil {
			gen := b.loopGen
			done := b.loopDone
			b.mu.Unlock()
			<-done
			b.mu.Lock()
			if gen != b.loopGen {
				if !b.stopped {
					return ch
				}
				delete(b.subscribers, ch)
				close(ch)
				return ch
			}
		}
		b.stopCh = make(chan struct{})
		b.loopDone = make(chan struct{})
		go b.loop(b.stopCh, b.loopDone)
	}
	return ch
}

// Unsubscribe removes the subscriber. Idempotent: calling it multiple times
// for the same channel is safe. Channel closing is handled exclusively by
// Stop() to keep lifecycle management in one place; consumers should rely on
// their own context (e.g. request context) for cancellation.
func (b *Broadcaster) Unsubscribe(ch chan any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subscribers, ch)
	if len(b.subscribers) == 0 && b.stopCh != nil && b.idleTimer == nil {
		b.idleTimer = time.AfterFunc(broadcasterIdleTimeout, func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			// Guard against the race where Stop() runs between the timer
			// firing and this callback acquiring the lock. Stop() already
			// handles cleanup, so we must not touch stopCh.
			if b.stopped {
				b.idleTimer = nil
				return
			}
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
	defer func() {
		if r := recover(); r != nil {
			log.Printf("严重: 广播器循环 panic: %v", r)
		}
	}()

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
			data, err := func() (result any, err error) {
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("fetchFn panic: %v", r)
					}
				}()
				return b.fetchFn()
			}()
			if err != nil {
				consecutiveErrors++
				log.Printf("警告: 统计数据获取失败 (连续第%d次): %v", consecutiveErrors, err)
				if consecutiveErrors == maxConsecutiveErrors {
					degradedEvent := map[string]any{"degraded": true, "error": "数据源不可用"}
					b.mu.Lock()
					chans := make([]chan any, 0, len(b.subscribers))
					for ch := range b.subscribers {
						chans = append(chans, ch)
					}
					b.mu.Unlock()
					for _, ch := range chans {
						select {
						case ch <- degradedEvent:
						default:
						}
					}
				}
				continue
			}

			consecutiveErrors = 0

			// Dedup is scoped to RawEvent (byte-level JSON comparison) for the
			// hot stats path, and falls back to json.Marshal for arbitrary
			// payload types so dedup remains correct for non-RawEvent sources.
			// We also fall back when only one side is RawEvent, because Go's
			// type system allows fetchFn to switch payload types across ticks
			// (defensive: the assumption is that fetchFn returns a stable
			// type, but we don't want dedup to silently disappear on a switch).
			if lastSnapshot != nil {
				currRaw, currIsRaw := data.(RawEvent)
				prevRaw, prevIsRaw := lastSnapshot.(RawEvent)
				if currIsRaw && prevIsRaw {
					if bytes.Equal(currRaw.JSON, prevRaw.JSON) {
						continue
					}
				} else {
					currJSON, err1 := json.Marshal(data)
					prevJSON, err2 := json.Marshal(lastSnapshot)
					if err1 == nil && err2 == nil && bytes.Equal(currJSON, prevJSON) {
						continue
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

			// Copy subscribers under lock, send outside lock to avoid
			// blocking Subscribe/Unsubscribe during channel sends.
			b.mu.Lock()
			chans := make([]chan any, 0, len(b.subscribers))
			for ch := range b.subscribers {
				chans = append(chans, ch)
			}
			b.mu.Unlock()

			for _, ch := range chans {
				select {
				case ch <- broadcast:
				default:
					b.droppedMsgs.Add(1)
				}
			}

			if dropped := b.droppedMsgs.Swap(0); dropped > 0 {
				log.Printf("警告: SSE 广播丢弃了 %d 条消息 (订阅者 channel 已满)", dropped)
			}
		}
	}
}
