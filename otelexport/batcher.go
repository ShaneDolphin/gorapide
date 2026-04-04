package otelexport

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	oteltrace "go.opentelemetry.io/otel/trace"
)

const batcherQueueSize = 2048

// spanData holds the data needed to export a single span.
type spanData struct {
	name       string
	traceID    oteltrace.TraceID
	spanID     oteltrace.SpanID
	parentID   oteltrace.SpanID
	startTime  time.Time
	source     string
	attributes map[string]string
	links      []oteltrace.SpanID
}

// batcher accumulates spanData and flushes in batches.
type batcher struct {
	maxSize int
	timeout time.Duration
	flushFn func(ctx context.Context, batch []spanData)
	queue   chan spanData
	done    chan struct{}
	once    sync.Once
	dropCnt atomic.Int64
}

func newBatcher(maxSize int, timeout time.Duration, flushFn func(ctx context.Context, batch []spanData)) *batcher {
	return &batcher{
		maxSize: maxSize,
		timeout: timeout,
		flushFn: flushFn,
		queue:   make(chan spanData, batcherQueueSize),
		done:    make(chan struct{}),
	}
}

func (b *batcher) start() {
	go b.run()
}

func (b *batcher) run() {
	defer close(b.done)
	batch := make([]spanData, 0, b.maxSize)
	timer := time.NewTimer(b.timeout)
	defer timer.Stop()

	for {
		select {
		case sd, ok := <-b.queue:
			if !ok {
				if len(batch) > 0 {
					b.flushFn(context.Background(), batch)
				}
				return
			}
			batch = append(batch, sd)
			if len(batch) >= b.maxSize {
				b.flushFn(context.Background(), batch)
				batch = make([]spanData, 0, b.maxSize)
				timer.Reset(b.timeout)
			}
		case <-timer.C:
			if len(batch) > 0 {
				b.flushFn(context.Background(), batch)
				batch = make([]spanData, 0, b.maxSize)
			}
			timer.Reset(b.timeout)
		}
	}
}

func (b *batcher) add(sd spanData) {
	select {
	case b.queue <- sd:
	default:
		b.dropCnt.Add(1)
	}
}

func (b *batcher) stop(ctx context.Context) {
	b.once.Do(func() {
		close(b.queue)
	})
	<-b.done
}

func (b *batcher) dropped() int64 {
	return b.dropCnt.Load()
}
