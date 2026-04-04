package otelexport

import (
	"testing"

	"github.com/ShaneDolphin/gorapide"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestSpanIDFromEventID(t *testing.T) {
	id := gorapide.NewEventID()
	sid := spanIDFromEventID(id)

	if !sid.IsValid() {
		t.Fatalf("spanIDFromEventID produced invalid SpanID for input %q", id)
	}

	// Deterministic: same input yields same output.
	sid2 := spanIDFromEventID(id)
	if sid != sid2 {
		t.Fatalf("spanIDFromEventID not deterministic: got %v then %v for same input", sid, sid2)
	}
}

func TestSpanIDFromDifferentEventIDs(t *testing.T) {
	id1 := gorapide.NewEventID()
	id2 := gorapide.NewEventID()

	sid1 := spanIDFromEventID(id1)
	sid2 := spanIDFromEventID(id2)

	if sid1 == sid2 {
		t.Fatalf("different EventIDs produced same SpanID: %v", sid1)
	}
}

func TestNewTraceID(t *testing.T) {
	tid := newTraceID()

	if !tid.IsValid() {
		t.Fatal("newTraceID produced invalid TraceID")
	}

	// Two calls should produce different IDs (with overwhelming probability).
	tid2 := newTraceID()
	if tid == tid2 {
		t.Fatal("newTraceID produced identical TraceIDs on consecutive calls")
	}
}

func TestFixedTraceID(t *testing.T) {
	id := gorapide.NewEventID()
	tid := traceIDFromEventID(id)

	if !tid.IsValid() {
		t.Fatalf("traceIDFromEventID produced invalid TraceID for input %q", id)
	}

	// Deterministic: same input yields same output.
	tid2 := traceIDFromEventID(id)
	if tid != tid2 {
		t.Fatalf("traceIDFromEventID not deterministic: got %v then %v for same input", tid, tid2)
	}

	// Should differ from zero.
	var zero oteltrace.TraceID
	if tid == zero {
		t.Fatal("traceIDFromEventID returned zero TraceID")
	}
}
