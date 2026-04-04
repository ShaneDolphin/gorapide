package otelexport

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// spanIDFromEventID generates an 8-byte SpanID from an EventID.
// Uses first 8 bytes of the UUID hex (stripped of dashes).
func spanIDFromEventID(id gorapide.EventID) oteltrace.SpanID {
	raw := strings.ReplaceAll(string(id), "-", "")
	var sid oteltrace.SpanID
	if len(raw) >= 16 {
		b, err := hex.DecodeString(raw[:16])
		if err == nil {
			copy(sid[:], b)
		}
	}
	return sid
}

// traceIDFromEventID generates a 16-byte TraceID from an EventID.
func traceIDFromEventID(id gorapide.EventID) oteltrace.TraceID {
	raw := strings.ReplaceAll(string(id), "-", "")
	var tid oteltrace.TraceID
	if len(raw) >= 32 {
		b, err := hex.DecodeString(raw[:32])
		if err == nil {
			copy(tid[:], b)
		}
	}
	return tid
}

// newTraceID generates a random 16-byte TraceID.
func newTraceID() oteltrace.TraceID {
	var tid oteltrace.TraceID
	_, _ = rand.Read(tid[:])
	return tid
}
