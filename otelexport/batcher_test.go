package otelexport

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBatcherFlushOnSize(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]spanData

	b := newBatcher(3, 5*time.Second, func(_ context.Context, batch []spanData) {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]spanData, len(batch))
		copy(cp, batch)
		flushed = append(flushed, cp)
	})
	b.start()

	b.add(spanData{name: "a"})
	b.add(spanData{name: "b"})
	b.add(spanData{name: "c"})

	// Give the batcher goroutine time to process.
	time.Sleep(100 * time.Millisecond)

	b.stop(context.Background())

	mu.Lock()
	defer mu.Unlock()

	if len(flushed) != 1 {
		t.Fatalf("expected 1 flush, got %d", len(flushed))
	}
	if len(flushed[0]) != 3 {
		t.Fatalf("expected 3 items in flush, got %d", len(flushed[0]))
	}
}

func TestBatcherFlushOnTimeout(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]spanData

	b := newBatcher(100, 50*time.Millisecond, func(_ context.Context, batch []spanData) {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]spanData, len(batch))
		copy(cp, batch)
		flushed = append(flushed, cp)
	})
	b.start()

	b.add(spanData{name: "x"})

	// Wait long enough for the timeout to trigger.
	time.Sleep(150 * time.Millisecond)

	b.stop(context.Background())

	mu.Lock()
	defer mu.Unlock()

	if len(flushed) < 1 {
		t.Fatal("expected at least 1 flush from timeout, got 0")
	}
	if flushed[0][0].name != "x" {
		t.Fatalf("expected span name 'x', got %q", flushed[0][0].name)
	}
}

func TestBatcherStopFlushesRemaining(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]spanData

	b := newBatcher(10, 5*time.Second, func(_ context.Context, batch []spanData) {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]spanData, len(batch))
		copy(cp, batch)
		flushed = append(flushed, cp)
	})
	b.start()

	b.add(spanData{name: "one"})
	b.add(spanData{name: "two"})

	// Give the goroutine time to receive items but not hit timeout.
	time.Sleep(50 * time.Millisecond)

	b.stop(context.Background())

	mu.Lock()
	defer mu.Unlock()

	if len(flushed) != 1 {
		t.Fatalf("expected 1 flush on stop, got %d", len(flushed))
	}
	if len(flushed[0]) != 2 {
		t.Fatalf("expected 2 items flushed on stop, got %d", len(flushed[0]))
	}
}

func TestBatcherDropsOnFullQueue(t *testing.T) {
	// Use a flush function that never returns quickly, but we won't
	// actually block — the point is to overflow the channel buffer.
	b := newBatcher(batcherQueueSize+200, 5*time.Second, func(_ context.Context, batch []spanData) {})
	// Intentionally do NOT start the batcher so nothing drains the queue.

	for i := 0; i < batcherQueueSize+100; i++ {
		b.add(spanData{name: "overflow"})
	}

	dropped := b.dropped()
	if dropped < 1 {
		t.Fatalf("expected drops when queue overflows, got %d", dropped)
	}

	// Now start and stop so the goroutine cleans up.
	b.start()
	b.stop(context.Background())
}
