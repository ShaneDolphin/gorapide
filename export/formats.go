// Package export provides format utilities for working with exported
// poset data. The primary export methods live on *gorapide.Poset; this
// package offers standalone helpers for formatting and rendering.
package export

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SpanJSON is the JSON-serializable form of a trace span for OTLP-compatible
// export. It mirrors gorapide.TraceSpan but uses JSON-friendly field names.
type SpanJSON struct {
	TraceID    string            `json:"traceId"`
	SpanID     string            `json:"spanId"`
	ParentID   string            `json:"parentSpanId,omitempty"`
	Name       string            `json:"operationName"`
	StartTime  int64             `json:"startTime"` // microseconds since epoch
	Duration   int64             `json:"duration"`   // microseconds
	Attributes map[string]string `json:"tags,omitempty"`
}

// FormatSpansJSON converts trace span data into Jaeger-compatible JSON.
func FormatSpansJSON(spans []SpanJSON) ([]byte, error) {
	wrapper := struct {
		Data []struct {
			TraceID string     `json:"traceID"`
			Spans   []SpanJSON `json:"spans"`
		} `json:"data"`
	}{}

	if len(spans) == 0 {
		return json.Marshal(wrapper)
	}

	traceID := spans[0].TraceID
	wrapper.Data = append(wrapper.Data, struct {
		TraceID string     `json:"traceID"`
		Spans   []SpanJSON `json:"spans"`
	}{
		TraceID: traceID,
		Spans:   spans,
	})

	return json.MarshalIndent(wrapper, "", "  ")
}

// ToSpanJSON converts raw span fields into a SpanJSON.
func ToSpanJSON(traceID, spanID, parentID, name string, startTime time.Time, endTime time.Time, attrs map[string]string) SpanJSON {
	dur := endTime.Sub(startTime)
	if dur < 0 {
		dur = 0
	}
	return SpanJSON{
		TraceID:    traceID,
		SpanID:     spanID,
		ParentID:   parentID,
		Name:       name,
		StartTime:  startTime.UnixMicro(),
		Duration:   dur.Microseconds(),
		Attributes: attrs,
	}
}

// SourceColorPalette returns a mapping of source names to hex colors,
// cycling through a fixed palette.
func SourceColorPalette(sources []string) map[string]string {
	palette := []string{
		"#4285F4", "#EA4335", "#FBBC05", "#34A853",
		"#FF6D01", "#46BDC6", "#7B61FF", "#F538A0",
	}
	colors := make(map[string]string, len(sources))
	for i, src := range sources {
		colors[src] = palette[i%len(palette)]
	}
	return colors
}

// FormatMermaidNode formats a Mermaid node with optional styling.
func FormatMermaidNode(id, label, shape string) string {
	switch shape {
	case "round":
		return fmt.Sprintf("  %s(\"%s\")", id, label)
	case "diamond":
		return fmt.Sprintf("  %s{\"%s\"}", id, label)
	default:
		return fmt.Sprintf("  %s[\"%s\"]", id, label)
	}
}

// FormatDOTLabel escapes a string for use in DOT labels.
func FormatDOTLabel(s string) string {
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
