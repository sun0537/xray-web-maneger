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

// Broadcaster periodically fetches data and fans it out to all subscribers.
// The background polling loop starts lazily on the first Subscribe() call.
// When the last subscriber unsubscribes, the loop continues running for
// idleTimeout before stopping, to avoid rapid start/stop cycles when SSE
// clients briefly disconnect and reconnect.
//
// NOTE: fetchFn is called WITHOUT holding b.mu, so long-running fetches
// (e.g. gRPC calls with multi-second timeouts) will NOT block Subscribe()
// or Unsubscribe(). The mutex is only held during the fan-out phase.
type Broadcaster[T any] struct {
	mu          sync.Mutex
	subscribers map[chan T]struct{}
	fetchFn     func() (T, error)
	enrichFn    func(data, prev T, tickAt, prevAt time.Time) T
	degradedFn  func() T
	interval    time.Duration
	stopCh      chan struct{}
	loopDone    chan struct{}
	loopGen     uint64
	stopped     bool
	idleTimer   *time.Timer
	stopOnce    sync.Once

	lastMu        sync.RWMutex
	lastBroadcast T
	hasLast       bool

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
// degradedFn constructs a degraded payload of type T pushed to subscribers
// after maxConsecutiveErrors consecutive fetch failures.
// The loop starts on first Subscribe() — no need to call Start() manually.
func NewBroadcaster[T any](fetchFn func() (T, error), enrichFn func(data, prev T, tickAt, prevAt time.Time) T, degradedFn func() T, interval time.Duration) *Broadcaster[T] {
	return &Broadcaster[T]{
		subscribers: make(map[chan T]struct{}),
		fetchFn:     fetchFn,
		enrichFn:    enrichFn,
		degradedFn:  degradedFn,
		interval:    interval,
	}
}

// LastBroadcast returns the most recent enriched broadcast payload.
// Returns false if no successful broadcast has occurred yet.
// Safe for concurrent use.
func (b *Broadcaster[T]) LastBroadcast() (T, bool) {
	b.lastMu.RLock()
	defer b.lastMu.RUnlock()
	return b.lastBroadcast, b.hasLast
}

// Stop shuts down the background loop (if running) and closes all subscriber channels.
func (b *Broadcaster[T]) Stop() {
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
		b.subscribers = make(map[chan T]struct{})
		b.mu.Unlock()
	})
}

// Subscribe returns a channel that receives broadcast payloads.
// If this is the first subscriber and the loop is not running, the background
// polling loop starts. Call Unsubscribe when done to clean up.
func (b *Broadcaster[T]) Subscribe() chan T {
	ch := make(chan T, 5)
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
func (b *Broadcaster[T]) Unsubscribe(ch chan T) {
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

// snapshotSubscribers returns a copy of the current subscriber channel list.
// Callers can send to these channels without holding b.mu.
func (b *Broadcaster[T]) snapshotSubscribers() []chan T {
	b.mu.Lock()
	defer b.mu.Unlock()
	chans := make([]chan T, 0, len(b.subscribers))
	for ch := range b.subscribers {
		chans = append(chans, ch)
	}
	return chans
}

func (b *Broadcaster[T]) loop(stopCh chan struct{}, loopDone chan struct{}) {
	defer close(loopDone)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("严重: 广播器循环 panic: %v", r)
		}
	}()

	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	var lastSnapshot T
	var lastAt time.Time
	var prevJSON []byte // cached marshaled bytes of previous tick for dedup
	consecutiveErrors := 0

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			tickAt := time.Now()
			data, err := func() (result T, err error) {
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
					degradedEvent, ok := func() (degraded T, ok bool) {
						defer func() {
							if r := recover(); r != nil {
								log.Printf("严重: degradedFn panic: %v", r)
							}
						}()
						return b.degradedFn(), true
					}()
					if !ok {
						continue
					}
					chans := b.snapshotSubscribers()
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

			// Dedup via JSON marshaling: marshal only the current payload and
			// compare against the cached prevJSON (from the previous tick).
			// This avoids re-marshaling lastSnapshot every tick.
			currJSON, marshalErr := json.Marshal(data)
			if marshalErr != nil {
				prevJSON = nil
			} else if prevJSON != nil && bytes.Equal(currJSON, prevJSON) {
				continue
			} else {
				prevJSON = currJSON
			}

			broadcast := data
			if b.enrichFn != nil {
				broadcast = b.enrichFn(data, lastSnapshot, tickAt, lastAt)
			}
			lastSnapshot = data
			lastAt = tickAt

			b.lastMu.Lock()
			b.lastBroadcast = broadcast
			b.hasLast = true
			b.lastMu.Unlock()

			chans := b.snapshotSubscribers()

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
