package export

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSourceColorPalette(t *testing.T) {
	colors := SourceColorPalette([]string{"scanner", "renderer", "aggregator"})
	if len(colors) != 3 {
		t.Fatalf("expected 3 colors, got %d", len(colors))
	}
	if colors["scanner"] == colors["renderer"] {
		t.Error("different sources should get different colors")
	}
}

func TestSourceColorPaletteCycles(t *testing.T) {
	sources := make([]string, 12)
	for i := range sources {
		sources[i] = string(rune('A' + i))
	}
	colors := SourceColorPalette(sources)
	if len(colors) != 12 {
		t.Fatalf("expected 12, got %d", len(colors))
	}
	// Colors cycle after palette length.
	if colors["A"] != colors["I"] {
		t.Error("colors should cycle after palette exhaustion")
	}
}

func TestToSpanJSON(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(100 * time.Millisecond)
	s := ToSpanJSON("trace1", "span1", "parent1", "op", start, end, map[string]string{"k": "v"})

	if s.TraceID != "trace1" {
		t.Errorf("TraceID: got %s", s.TraceID)
	}
	if s.Duration != 100000 { // 100ms = 100000µs
		t.Errorf("Duration: want 100000, got %d", s.Duration)
	}
	if s.Attributes["k"] != "v" {
		t.Errorf("Attributes: got %v", s.Attributes)
	}
}

func TestFormatSpansJSON(t *testing.T) {
	spans := []SpanJSON{
		{TraceID: "t1", SpanID: "s1", Name: "root"},
		{TraceID: "t1", SpanID: "s2", ParentID: "s1", Name: "child"},
	}
	data, err := FormatSpansJSON(spans)
	if err != nil {
		t.Fatalf("FormatSpansJSON: %v", err)
	}
	if !json.Valid(data) {
		t.Error("output should be valid JSON")
	}
	if !strings.Contains(string(data), "root") {
		t.Error("output should contain span names")
	}
}

func TestFormatSpansJSONEmpty(t *testing.T) {
	data, err := FormatSpansJSON(nil)
	if err != nil {
		t.Fatalf("FormatSpansJSON empty: %v", err)
	}
	if !json.Valid(data) {
		t.Error("empty output should be valid JSON")
	}
}

func TestFormatDOTLabel(t *testing.T) {
	got := FormatDOTLabel(`hello "world"`)
	if !strings.Contains(got, `\"`) {
		t.Errorf("should escape quotes: got %s", got)
	}
}

func TestFormatMermaidNode(t *testing.T) {
	got := FormatMermaidNode("n1", "Hello", "round")
	if !strings.Contains(got, "(") {
		t.Errorf("round should use parens: got %s", got)
	}
	got2 := FormatMermaidNode("n2", "World", "box")
	if !strings.Contains(got2, "[") {
		t.Errorf("box should use brackets: got %s", got2)
	}
}
