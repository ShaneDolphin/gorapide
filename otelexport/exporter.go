package otelexport

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beautiful-majestic-dolphin/gorapide"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Protocol selects the OTLP transport protocol.
type Protocol int

const (
	// GRPC selects the gRPC OTLP transport.
	GRPC Protocol = iota
	// HTTP selects the HTTP/protobuf OTLP transport.
	HTTP
)

// Config holds settings for a LiveExporter.
type Config struct {
	Endpoint     string
	Protocol     Protocol
	Headers      map[string]string
	ServiceName  string
	TraceID      oteltrace.TraceID // zero = auto-generate
	BatchSize    int               // default 512
	BatchTimeout time.Duration     // default 5s
	Insecure     bool
}

func (c *Config) defaults() {
	if c.BatchSize <= 0 {
		c.BatchSize = 512
	}
	if c.BatchTimeout <= 0 {
		c.BatchTimeout = 5 * time.Second
	}
	if c.ServiceName == "" {
		c.ServiceName = "gorapide"
	}
}

// LiveExporter converts gorapide poset events into OpenTelemetry spans and
// batches them for export. It implements an observer callback compatible with
// arch.WithObserver.
type LiveExporter struct {
	traceID  oteltrace.TraceID
	poset    *gorapide.Poset
	batcher  *batcher
	exported map[gorapide.EventID]bool
	count    atomic.Int64
	mu       sync.Mutex
}

// NewLiveExporter creates an exporter. If Config.Endpoint is empty, spans are
// batched but the flush function is a no-op (useful for testing without a collector).
func NewLiveExporter(cfg Config) (*LiveExporter, error) {
	cfg.defaults()
	tid := cfg.TraceID
	if !tid.IsValid() {
		tid = newTraceID()
	}
	le := &LiveExporter{
		traceID:  tid,
		exported: make(map[gorapide.EventID]bool),
	}
	le.batcher = newBatcher(cfg.BatchSize, cfg.BatchTimeout, le.exportBatch)
	le.batcher.start()
	return le, nil
}

// newTestExporter creates a LiveExporter with a custom flush function for testing.
func newTestExporter(poset *gorapide.Poset, flushFn func(ctx context.Context, batch []spanData)) *LiveExporter {
	le := &LiveExporter{
		traceID:  newTraceID(),
		poset:    poset,
		exported: make(map[gorapide.EventID]bool),
	}
	le.batcher = newBatcher(512, 50*time.Millisecond, flushFn)
	le.batcher.start()
	return le
}

// SetPoset sets the poset reference for causal parent lookups.
func (le *LiveExporter) SetPoset(p *gorapide.Poset) {
	le.mu.Lock()
	le.poset = p
	le.mu.Unlock()
}

// OnEvent is the observer callback compatible with arch.WithObserver.
func (le *LiveExporter) OnEvent(e *gorapide.Event) {
	le.mu.Lock()
	if le.exported[e.ID] {
		le.mu.Unlock()
		return
	}
	le.exported[e.ID] = true
	poset := le.poset
	le.mu.Unlock()

	le.count.Add(1)

	sd := spanData{
		name:      e.Name,
		traceID:   le.traceID,
		spanID:    spanIDFromEventID(e.ID),
		startTime: e.Clock.WallTime,
		source:    e.Source,
	}

	// Build attributes.
	attrs := make(map[string]string)
	if e.Source != "" {
		attrs["source"] = e.Source
	}
	for k, v := range e.Params {
		if s, ok := v.(string); ok {
			attrs[k] = s
		}
	}
	sd.attributes = attrs

	// Look up causal parents from poset.
	if poset != nil {
		causes := poset.DirectCauses(e.ID)
		if len(causes) > 0 {
			sort.Slice(causes, func(i, j int) bool {
				return causes[i].Clock.Lamport < causes[j].Clock.Lamport
			})
			sd.parentID = spanIDFromEventID(causes[0].ID)
			for _, c := range causes[1:] {
				sd.links = append(sd.links, spanIDFromEventID(c.ID))
			}
		}
	}

	le.batcher.add(sd)
}

// Shutdown flushes remaining spans and stops the batcher.
func (le *LiveExporter) Shutdown(ctx context.Context) error {
	le.batcher.stop(ctx)
	return nil
}

// ExportedCount returns the total number of events that have been observed
// (deduplicated).
func (le *LiveExporter) ExportedCount() int {
	return int(le.count.Load())
}

// Dropped returns the number of spans that were dropped because the
// internal queue was full.
func (le *LiveExporter) Dropped() int64 {
	return le.batcher.dropped()
}

// exportBatch is the default flush function (no-op unless wired to OTLP).
func (le *LiveExporter) exportBatch(ctx context.Context, batch []spanData) {
	// No-op. Task 10 will wire this to the OTel SDK.
}
