package sse

import (
	"bytes"
	"encoding/json"
	"log"
	"sync/atomic"
	"testing"
	"time"
)

func TestBroadcasterLifecycle(t *testing.T) {
	var fetchCount atomic.Int32
	fetchFn := func() (any, error) {
		n := fetchCount.Add(1)
		b, _ := json.Marshal(map[string]int{"tick": int(n)})
		return RawEvent{JSON: b}, nil
	}

	b := NewBroadcaster(fetchFn, nil, 50*time.Millisecond)
	defer b.Stop()

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	select {
	case payload := <-ch:
		if payload == nil {
			t.Fatal("expected non-nil data")
		}
		raw, ok := payload.(RawEvent)
		if !ok {
			t.Fatalf("expected RawEvent, got %T", payload)
		}
		var m map[string]int
		if err := json.Unmarshal(raw.JSON, &m); err != nil {
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
	enrichFn := func(data, prev any, tickAt, prevAt time.Time) any {
		return RawEvent{JSON: []byte(`{"enriched":true}`)}
	}
	b := NewBroadcaster(func() (any, error) {
		return RawEvent{JSON: []byte(`{"raw":true}`)}, nil
	}, enrichFn, 50*time.Millisecond)
	defer b.Stop()

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	select {
	case payload := <-ch:
		raw, ok := payload.(RawEvent)
		if !ok {
			t.Fatalf("expected RawEvent, got %T", payload)
		}
		var m map[string]bool
		json.Unmarshal(raw.JSON, &m)
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
	b := NewBroadcaster(func() (any, error) {
		return RawEvent{JSON: []byte(`{"same":true}`)}, nil
	}, nil, 50*time.Millisecond)
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

func TestBroadcasterLastBroadcast(t *testing.T) {
	b := NewBroadcaster(func() (any, error) {
		return RawEvent{JSON: []byte(`{"v":1}`)}, nil
	}, nil, 50*time.Millisecond)

	if b.LastBroadcast() != nil {
		t.Fatal("LastBroadcast should be nil before any broadcast")
	}

	ch := b.Subscribe()

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}

	cached := b.LastBroadcastJSON()
	if cached == nil {
		t.Fatal("LastBroadcastJSON should be non-nil after broadcast")
	}
	if string(cached) != `{"v":1}` {
		t.Fatalf("unexpected cached data: %s", cached)
	}

	time.Sleep(150 * time.Millisecond)

	cached2 := b.LastBroadcastJSON()
	if string(cached2) != `{"v":1}` {
		t.Fatal("LastBroadcastJSON should return same data after dedup")
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
	b := NewBroadcaster(func() (any, error) {
		count.Add(1)
		return nil, &fetchError{}
	}, nil, 50*time.Millisecond)
	defer b.Stop()

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	select {
	case payload := <-ch:
		m, ok := payload.(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any, got %T", payload)
		}
		if m["degraded"] != true || m["error"] != "数据源不可用" {
			t.Fatalf("unexpected degraded event: %v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for degraded event")
	}
}

type fetchError struct{}

func (e *fetchError) Error() string { return "test fetch error" }

func TestBroadcasterSubscribeAfterStop(t *testing.T) {
	b := NewBroadcaster(func() (any, error) {
		return RawEvent{JSON: []byte(`{}`)}, nil
	}, nil, time.Hour)

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

func TestBroadcasterWarnOnceOnNonRawEvent(t *testing.T) {
	// Capture log output.
	var logBuf bytes.Buffer
	origFlags := log.Flags()
	origOutput := log.Writer()
	log.SetFlags(0)
	log.SetOutput(&logBuf)
	defer func() {
		log.SetFlags(origFlags)
		log.SetOutput(origOutput)
	}()

	var fetchCount atomic.Int32
	b := NewBroadcaster(func() (any, error) {
		fetchCount.Add(1)
		// Intentionally NOT a RawEvent to trigger the type warning.
		return map[string]int{"n": int(fetchCount.Load())}, nil
	}, nil, 40*time.Millisecond)
	defer b.Stop()

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	// First broadcast must arrive (non-RawEvent still broadcasts unconditionally).
	select {
	case payload := <-ch:
		if _, ok := payload.(map[string]int); !ok {
			t.Fatalf("expected map[string]int, got %T", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first broadcast")
	}

	// Wait for several more ticks so warnOnce has multiple chances to (not) fire.
	for i := 0; i < 3; i++ {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for subsequent broadcast")
		}
	}

	logStr := logBuf.String()
	if !bytes.Contains([]byte(logStr), []byte("Broadcaster fetchFn 返回非 RawEvent 类型")) {
		t.Fatalf("expected type warning in log, got: %q", logStr)
	}
	if count := bytes.Count([]byte(logStr), []byte("Broadcaster fetchFn 返回非 RawEvent 类型")); count != 1 {
		t.Fatalf("expected warn-once to fire exactly once, got %d occurrences in log:\n%s", count, logStr)
	}
}
