package sse

import (
	"hash/fnv"
	"sync"
	"time"
)

// Broadcaster periodically fetches data and fans it out to all subscribers.
// The background polling loop starts lazily on the first Subscribe() call
// and stops automatically when the last subscriber unsubscribes.
type Broadcaster struct {
	mu          sync.Mutex
	subscribers map[chan []byte]struct{}
	fetchFn     func() ([]byte, error)
	interval    time.Duration
	stopCh      chan struct{}
	stopped     bool
	lastData    []byte
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

// Start is a no-op kept for backward compatibility.
// The loop now starts lazily on first Subscribe().
func (b *Broadcaster) Start() {}

// Stop shuts down the background loop (if running) and closes all subscriber channels.
func (b *Broadcaster) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.stopped = true
	if b.stopCh != nil {
		close(b.stopCh)
		b.stopCh = nil
	}
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
		if b.lastData != nil {
			ch <- b.lastData
		}
		go b.loop(b.stopCh)
	} else if b.lastData != nil {
		ch <- b.lastData
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

func (b *Broadcaster) loop(stopCh chan struct{}) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	var lastHash uint64
	hasher := fnv.New64a()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			data, err := b.fetchFn()
			if err != nil {
				continue
			}

			// Skip broadcast if data unchanged since last tick
			hasher.Reset()
			hasher.Write(data)
			h := hasher.Sum64()
			if h == lastHash {
				continue
			}
			lastHash = h

			b.mu.Lock()
			b.lastData = data
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
