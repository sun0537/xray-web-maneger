package sse

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// rawEvent wraps pre-marshaled JSON bytes for use in tests.
type rawEvent struct {
	JSON []byte
}

// degradedRawEvent is the degraded payload used in tests.
func degradedRawEvent() rawEvent {
	return rawEvent{JSON: []byte(`{"degraded":true}`)}
}

func TestBroadcasterLifecycle(t *testing.T) {
	var fetchCount atomic.Int32
	fetchFn := func() (rawEvent, error) {
		n := fetchCount.Add(1)
		b, _ := json.Marshal(map[string]int{"tick": int(n)})
		return rawEvent{JSON: b}, nil
	}

	b := NewBroadcaster(fetchFn, nil, degradedRawEvent, 50*time.Millisecond)
	defer b.Stop()

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	select {
	case payload := <-ch:
		if payload.JSON == nil {
			t.Fatal("expected non-nil data")
		}
		var m map[string]int
		if err := json.Unmarshal(payload.JSON, &m); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if m["tick"] != 1 {
			t.Fatalf("expected tick=1, got %d", m["tick"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first broadcast")
	}
}

func TestBroadcasterEnrichFn(t *testing.T) {
	enrichFn := func(data, prev rawEvent, tickAt, prevAt time.Time) rawEvent {
		return rawEvent{JSON: []byte(`{"enriched":true}`)}
	}
	b := NewBroadcaster(func() (rawEvent, error) {
		return rawEvent{JSON: []byte(`{"raw":true}`)}, nil
	}, enrichFn, degradedRawEvent, 50*time.Millisecond)
	defer b.Stop()

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	select {
	case payload := <-ch:
		var m map[string]bool
		json.Unmarshal(payload.JSON, &m)
		if !m["enriched"] {
			t.Fatal("expected enriched data from enrichFn")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

// dedupWaitDuration is how long we wait to confirm a duplicate broadcast was
// suppressed. It must be longer than the broadcaster's tick interval (50ms)
// so at least one tick fires during the wait.
const dedupWaitDuration = 200 * time.Millisecond

func TestBroadcasterDedup(t *testing.T) {
	b := NewBroadcaster(func() (rawEvent, error) {
		return rawEvent{JSON: []byte(`{"same":true}`)}, nil
	}, nil, degradedRawEvent, 50*time.Millisecond)
	defer b.Stop()

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first broadcast")
	}

	select {
	case <-ch:
		t.Fatal("duplicate data should have been deduped")
	case <-time.After(dedupWaitDuration):
	}
}

// TestBroadcasterDedupRawEvent verifies that identical rawEvent payloads
// are deduped via JSON marshaling comparison.
func TestBroadcasterDedupRawEvent(t *testing.T) {
	var callCount atomic.Int32
	b := NewBroadcaster(func() (rawEvent, error) {
		callCount.Add(1)
		return rawEvent{JSON: []byte(`{"v":1}`)}, nil
	}, nil, degradedRawEvent, 50*time.Millisecond)
	defer b.Stop()

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	// First call: must deliver.
	select {
	case payload := <-ch:
		if payload.JSON == nil {
			t.Fatalf("first payload must have JSON, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first broadcast")
	}

	// Second tick should dedup — no delivery.
	select {
	case payload := <-ch:
		t.Fatalf("unexpected broadcast %v", payload)
	case <-time.After(dedupWaitDuration):
	}

	// fetchFn must still have been called at least twice (once for the
	// delivered frame, at least once more that was deduped).
	if callCount.Load() < 2 {
		t.Fatalf("expected at least 2 fetch invocations, got %d", callCount.Load())
	}
}

func TestBroadcasterLastBroadcast(t *testing.T) {
	b := NewBroadcaster(func() (rawEvent, error) {
		return rawEvent{JSON: []byte(`{"v":1}`)}, nil
	}, nil, degradedRawEvent, 50*time.Millisecond)

	if _, ok := b.LastBroadcast(); ok {
		t.Fatal("LastBroadcast should return false before any broadcast")
	}

	ch := b.Subscribe()

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}

	cached, ok := b.LastBroadcast()
	if !ok {
		t.Fatal("LastBroadcast should return true after broadcast")
	}
	if string(cached.JSON) != `{"v":1}` {
		t.Fatalf("unexpected cached data: %s", cached.JSON)
	}

	time.Sleep(150 * time.Millisecond)

	cached2, ok := b.LastBroadcast()
	if !ok {
		t.Fatal("LastBroadcast should still return true")
	}
	if string(cached2.JSON) != `{"v":1}` {
		t.Fatal("LastBroadcast should return same data after dedup")
	}

	select {
	case <-ch:
		t.Fatal("no new broadcast expected after dedup")
	case <-time.After(100 * time.Millisecond):
	}

	b.Unsubscribe(ch)
	b.Stop()
}

func TestBroadcasterFetchErrorDegraded(t *testing.T) {
	var count atomic.Int32
	b := NewBroadcaster(func() (rawEvent, error) {
		count.Add(1)
		return rawEvent{}, &fetchError{}
	}, nil, degradedRawEvent, 50*time.Millisecond)
	defer b.Stop()

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	select {
	case payload := <-ch:
		var m map[string]bool
		if err := json.Unmarshal(payload.JSON, &m); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if !m["degraded"] {
			t.Fatalf("expected degraded event, got: %s", payload.JSON)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for degraded event")
	}
}

type fetchError struct{}

func (e *fetchError) Error() string { return "test fetch error" }

func TestBroadcasterSubscribeAfterStop(t *testing.T) {
	b := NewBroadcaster(func() (rawEvent, error) {
		return rawEvent{JSON: []byte(`{}`)}, nil
	}, nil, degradedRawEvent, time.Hour)

	b.Stop()
	ch := b.Subscribe()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout: expected closed channel")
	}
}

func TestBroadcasterUnsubscribeIdempotent(t *testing.T) {
	b := NewBroadcaster(func() (rawEvent, error) {
		return rawEvent{JSON: []byte(`{}`)}, nil
	}, nil, degradedRawEvent, time.Hour)
	defer b.Stop()

	ch := b.Subscribe()

	// First Unsubscribe removes the subscriber; must not panic.
	b.Unsubscribe(ch)

	// Second Unsubscribe on the same channel must be a safe no-op
	// (regression guard for the previously-introduced close(ch)).
	b.Unsubscribe(ch)

	// A third call, after Stop, must also be safe.
	b.Stop()
	b.Unsubscribe(ch)
}

// TestBroadcasterConcurrentFanOut exercises the "copy-under-lock, send-outside-
// lock" fan-out pattern under concurrent Subscribe/Unsubscribe pressure.
// Run with -race to verify there are no data races when the broadcaster loop
// copies the subscriber map while other goroutines modify it.
func TestBroadcasterConcurrentFanOut(t *testing.T) {
	var fetchCount atomic.Int32
	b := NewBroadcaster(func() (rawEvent, error) {
		n := fetchCount.Add(1)
		return rawEvent{JSON: []byte(fmt.Sprintf(`{"tick":%d}`, n))}, nil
	}, nil, degradedRawEvent, 20*time.Millisecond)
	defer b.Stop()

	const (
		numSubscribers = 20
		duration       = 500 * time.Millisecond
	)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Spawn many goroutines that rapidly subscribe, drain some events,
	// then unsubscribe — all while the broadcaster is actively fanning out.
	for i := 0; i < numSubscribers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}

				ch := b.Subscribe()
				// Drain between 1 and 3 events.
				drain := 1 + (int(time.Now().UnixNano()) % 3)
				for d := 0; d < drain; d++ {
					select {
					case <-ch:
					case <-time.After(100 * time.Millisecond):
					}
				}
				b.Unsubscribe(ch)
			}
		}()
	}

	time.Sleep(duration)
	close(stop)
	wg.Wait()
}
