package sse

import (
	"bytes"
	"log"
	"sync"
	"time"
)

// Broadcaster periodically fetches data and fans it out to all subscribers.
// The background polling loop starts lazily on the first Subscribe() call
// and stops automatically when the last subscriber unsubscribes.
//
// NOTE: fetchFn is called WITHOUT holding b.mu, so long-running fetches
// (e.g. gRPC calls with multi-second timeouts) will NOT block Subscribe()
// or Unsubscribe(). The mutex is only held during the fan-out phase.
type Broadcaster struct {
	mu          sync.Mutex
	subscribers map[chan []byte]struct{}
	fetchFn     func() ([]byte, error)
	interval    time.Duration
	stopCh      chan struct{}
	loopDone    chan struct{}
	stopped     bool
}

// NewBroadcaster creates a Broadcaster. fetchFn is called every interval;
// its result is broadcast to all subscribers. The loop starts on first
// Subscribe() — no need to call Start() manually.
func NewBroadcaster(fetchFn func() ([]byte, error), interval time.Duration) *Broadcaster {
	return &Broadcaster{
		subscribers: make(map[chan []byte]struct{}),
		fetchFn:     fetchFn,
		interval:    interval,
	}
}

// Stop shuts down the background loop (if running) and closes all subscriber channels.
func (b *Broadcaster) Stop() {
	b.mu.Lock()
	b.stopped = true
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
	b.subscribers = make(map[chan []byte]struct{})
}

// Subscribe returns a channel that receives broadcast payloads.
// If this is the first subscriber, the background polling loop starts.
// Call Unsubscribe when done to clean up.
func (b *Broadcaster) Subscribe() chan []byte {
	ch := make(chan []byte, 1)
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.stopped {
		close(ch)
		return ch
	}

	wasEmpty := len(b.subscribers) == 0
	b.subscribers[ch] = struct{}{}
	if wasEmpty {
		b.stopCh = make(chan struct{})
		b.loopDone = make(chan struct{})
		go b.loop(b.stopCh, b.loopDone)
	}
	return ch
}

// Unsubscribe removes the subscriber. If this was the last subscriber,
// the background polling loop is stopped to conserve resources.
func (b *Broadcaster) Unsubscribe(ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.subscribers, ch)
	if len(b.subscribers) == 0 && b.stopCh != nil {
		close(b.stopCh)
		b.stopCh = nil
	}
}

// maxConsecutiveErrors is the number of consecutive fetch failures before
// a degraded event is pushed to subscribers so they know data is stale.
const maxConsecutiveErrors = 3

func (b *Broadcaster) loop(stopCh chan struct{}, loopDone chan struct{}) {
	defer close(loopDone)

	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	var lastData []byte
	consecutiveErrors := 0

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			data, err := b.fetchFn()
			if err != nil {
				consecutiveErrors++
				log.Printf("警告: 统计数据获取失败 (连续第%d次): %v", consecutiveErrors, err)
				if consecutiveErrors == maxConsecutiveErrors {
					degradedEvent := []byte(`{"degraded":true,"error":"数据源不可用"}`)
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

			if bytes.Equal(lastData, data) {
				continue
			}
			lastData = data

			b.mu.Lock()
			for ch := range b.subscribers {
				select {
				case ch <- data:
				default:
				}
			}
			b.mu.Unlock()
		}
	}
}
