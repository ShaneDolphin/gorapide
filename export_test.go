package gorapide

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// --- JSON round-trip ---

func TestJSONRoundTrip(t *testing.T) {
	p := Build().
		Source("scanner").
		Event("ScanStart").
		Event("VulnFound", "severity", "HIGH").CausedBy("ScanStart").
		Source("aggregator").
		Event("Finding").CausedBy("VulnFound").
		MustDone()

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	p2 := NewPoset()
	if err := json.Unmarshal(data, p2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if p2.Len() != p.Len() {
		t.Errorf("Len: want %d, got %d", p.Len(), p2.Len())
	}

	// Verify all events preserved.
	for _, e := range p.Events() {
		e2, ok := p2.Event(e.ID)
		if !ok {
			t.Errorf("event %s not found after round-trip", e.ID.Short())
			continue
		}
		if e2.Name != e.Name {
			t.Errorf("event %s: Name want %s, got %s", e.ID.Short(), e.Name, e2.Name)
		}
		if e2.Source != e.Source {
			t.Errorf("event %s: Source want %s, got %s", e.ID.Short(), e.Source, e2.Source)
		}
		if e2.Clock.Lamport != e.Clock.Lamport {
			t.Errorf("event %s: Lamport want %d, got %d", e.ID.Short(), e.Clock.Lamport, e2.Clock.Lamport)
		}
	}

	// Verify causal structure preserved.
	for _, e := range p.Events() {
		origSucc := p.DirectSuccessors(e.ID)
		newSucc := p2.DirectSuccessors(e.ID)
		if len(origSucc) != len(newSucc) {
			t.Errorf("event %s: successor count want %d, got %d",
				e.ID.Short(), len(origSucc), len(newSucc))
		}
	}

	// Verify specific causal relationships.
	scanStart := p.EventsByName("ScanStart")[0]
	vulnFound := p.EventsByName("VulnFound")[0]
	finding := p.EventsByName("Finding")[0]

	if !p2.IsCausallyBefore(scanStart.ID, vulnFound.ID) {
		t.Error("ScanStart should be causally before VulnFound after round-trip")
	}
	if !p2.IsCausallyBefore(vulnFound.ID, finding.ID) {
		t.Error("VulnFound should be causally before Finding after round-trip")
	}
	if !p2.IsCausallyBefore(scanStart.ID, finding.ID) {
		t.Error("ScanStart should be transitively before Finding after round-trip")
	}
}

func TestJSONRoundTripParams(t *testing.T) {
	p := Build().
		Event("X", "key", "value", "count", "42").
		MustDone()

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	p2 := NewPoset()
	if err := json.Unmarshal(data, p2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	e2 := p2.EventsByName("X")[0]
	if e2.ParamString("key") != "value" {
		t.Errorf("param key: want 'value', got %q", e2.ParamString("key"))
	}
}

func TestJSONRoundTripEmpty(t *testing.T) {
	p := NewPoset()

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal empty: %v", err)
	}

	p2 := NewPoset()
	if err := json.Unmarshal(data, p2); err != nil {
		t.Fatalf("Unmarshal empty: %v", err)
	}
	if p2.Len() != 0 {
		t.Errorf("empty: want 0 events, got %d", p2.Len())
	}
}

func TestJSONMetadata(t *testing.T) {
	p := Build().
		Event("X").
		MustDone()

	data, err := p.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var export PosetExport
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatalf("Unmarshal to PosetExport: %v", err)
	}

	if export.Metadata == nil {
		t.Fatal("Metadata should not be nil")
	}
	if export.Metadata["event_count"] != "1" {
		t.Errorf("metadata event_count: want '1', got %q", export.Metadata["event_count"])
	}
}

func TestJSONValidOutput(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		MustDone()

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if !json.Valid(data) {
		t.Error("output should be valid JSON")
	}

	// Verify it's human-readable (has indentation when using MarshalIndent).
	pretty, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if !strings.Contains(string(pretty), "\n") {
		t.Error("MarshalIndent should produce multi-line output")
	}
}

func TestJSONCausalEdgesPreserved(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		Event("D").CausedBy("B").CausedBy("C"). // fan-in: B and C both cause D
		MustDone()

	data, _ := json.Marshal(p)
	p2 := NewPoset()
	json.Unmarshal(data, p2)

	a := p.EventsByName("A")[0]
	b := p.EventsByName("B")[0]
	c := p.EventsByName("C")[0]

	if !p2.IsCausallyBefore(a.ID, b.ID) {
		t.Error("A -> B edge not preserved")
	}
	if !p2.IsCausallyBefore(a.ID, c.ID) {
		t.Error("A -> C edge not preserved")
	}
}

func TestJSONWallTimePreserved(t *testing.T) {
	p := Build().Event("X").MustDone()
	e := p.EventsByName("X")[0]
	origWall := e.Clock.WallTime

	data, _ := json.Marshal(p)
	p2 := NewPoset()
	json.Unmarshal(data, p2)

	e2 := p2.EventsByName("X")[0]
	diff := origWall.Sub(e2.Clock.WallTime)
	if diff < -time.Millisecond || diff > time.Millisecond {
		t.Errorf("WallTime not preserved: diff=%v", diff)
	}
}

// --- DOTWithOptions ---

func TestDOTWithOptionsColorBySource(t *testing.T) {
	p := Build().
		Source("scanner").Event("ScanStart").
		Source("renderer").Event("Render").CausedBy("ScanStart").
		MustDone()

	dot := p.DOTWithOptions(DOTOptions{ColorBySource: true})
	if !strings.Contains(dot, "color") || !strings.Contains(dot, "fillcolor") {
		t.Error("ColorBySource should produce color attributes")
	}
	if !strings.Contains(dot, "digraph") {
		t.Error("should be a valid digraph")
	}
}

func TestDOTWithOptionsShowParams(t *testing.T) {
	p := Build().
		Event("X", "severity", "HIGH").
		MustDone()

	dot := p.DOTWithOptions(DOTOptions{ShowParams: true})
	if !strings.Contains(dot, "severity") {
		t.Error("ShowParams should include param names in output")
	}
	if !strings.Contains(dot, "HIGH") {
		t.Error("ShowParams should include param values in output")
	}
}

func TestDOTWithOptionsShowTimestamps(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		MustDone()

	dot := p.DOTWithOptions(DOTOptions{ShowTimestamps: true})
	if !strings.Contains(dot, "L=") || !strings.Contains(dot, "1") {
		t.Error("ShowTimestamps should include Lamport timestamps")
	}
}

func TestDOTWithOptionsHighlightPath(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		MustDone()

	a := p.EventsByName("A")[0]
	b := p.EventsByName("B")[0]

	dot := p.DOTWithOptions(DOTOptions{
		HighlightPath: []EventID{a.ID, b.ID},
	})
	if !strings.Contains(dot, "red") || !strings.Contains(dot, "penwidth") {
		t.Error("HighlightPath should highlight specified events")
	}
}

func TestDOTWithOptionsClusterBySource(t *testing.T) {
	p := Build().
		Source("scanner").Event("ScanStart").
		Source("renderer").Event("Render").CausedBy("ScanStart").
		MustDone()

	dot := p.DOTWithOptions(DOTOptions{ClusterBySource: true})
	if !strings.Contains(dot, "subgraph") {
		t.Error("ClusterBySource should produce subgraphs")
	}
	if !strings.Contains(dot, "scanner") {
		t.Error("ClusterBySource should mention source names")
	}
}

func TestDOTWithOptionsEmpty(t *testing.T) {
	p := NewPoset()
	dot := p.DOTWithOptions(DOTOptions{})
	if !strings.Contains(dot, "digraph") {
		t.Error("empty poset should still produce valid digraph")
	}
}

// --- Mermaid ---

func TestMermaidBasic(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		MustDone()

	m := p.Mermaid()
	if !strings.Contains(m, "graph TD") {
		t.Error("Mermaid should start with 'graph TD'")
	}
	if !strings.Contains(m, "-->") {
		t.Error("Mermaid should contain edge arrows")
	}
	if !strings.Contains(m, "A") {
		t.Error("Mermaid should contain event names")
	}
}

func TestMermaidMultipleEdges(t *testing.T) {
	p := Build().
		Event("Start").
		Event("Middle").CausedBy("Start").
		Event("End").CausedBy("Middle").
		MustDone()

	m := p.Mermaid()
	arrowCount := strings.Count(m, "-->")
	if arrowCount != 2 {
		t.Errorf("expected 2 arrows, got %d", arrowCount)
	}
}

func TestMermaidEmpty(t *testing.T) {
	p := NewPoset()
	m := p.Mermaid()
	if !strings.Contains(m, "graph TD") {
		t.Error("empty poset Mermaid should still have header")
	}
}

func TestMermaidWithSource(t *testing.T) {
	p := Build().
		Source("scanner").Event("Scan").
		Source("renderer").Event("Render").CausedBy("Scan").
		MustDone()

	m := p.Mermaid()
	if !strings.Contains(m, "scanner") || !strings.Contains(m, "renderer") {
		t.Error("Mermaid should include source in labels")
	}
}

// --- TraceSpan ---

func TestToTraceSpansBasic(t *testing.T) {
	p := Build().
		Source("scanner").
		Event("ScanStart").
		Event("VulnFound").CausedBy("ScanStart").
		MustDone()

	spans := p.ToTraceSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
}

func TestToTraceSpansSameTraceID(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		MustDone()

	spans := p.ToTraceSpans()
	traceID := spans[0].TraceID
	if traceID == "" {
		t.Fatal("TraceID should not be empty")
	}
	for _, s := range spans {
		if s.TraceID != traceID {
			t.Errorf("all spans should share TraceID: want %s, got %s", traceID, s.TraceID)
		}
	}
}

func TestToTraceSpansParentChild(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		MustDone()

	a := p.EventsByName("A")[0]
	b := p.EventsByName("B")[0]

	spans := p.ToTraceSpans()
	spanByID := make(map[string]TraceSpan)
	for _, s := range spans {
		spanByID[s.SpanID] = s
	}

	spanA := spanByID[string(a.ID)]
	spanB := spanByID[string(b.ID)]

	if spanA.ParentID != "" {
		t.Error("root span should have empty ParentID")
	}
	if spanB.ParentID != string(a.ID) {
		t.Errorf("B's ParentID should be A's ID: want %s, got %s", a.ID, spanB.ParentID)
	}
}

func TestToTraceSpansAttributes(t *testing.T) {
	p := Build().
		Source("scanner").
		Event("VulnFound", "severity", "HIGH").
		MustDone()

	spans := p.ToTraceSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name != "VulnFound" {
		t.Errorf("span name: want VulnFound, got %s", s.Name)
	}
	if s.Attributes["source"] != "scanner" {
		t.Errorf("source attr: want scanner, got %s", s.Attributes["source"])
	}
	if s.Attributes["severity"] != "HIGH" {
		t.Errorf("severity attr: want HIGH, got %s", s.Attributes["severity"])
	}
}

func TestToTraceSpansMultipleParents(t *testing.T) {
	// Fan-in: A and B both cause C. TraceSpan only has one ParentID.
	p := Build().
		Event("A").
		Event("B").
		Event("C").CausedBy("A").CausedBy("B").
		MustDone()

	spans := p.ToTraceSpans()
	cEvent := p.EventsByName("C")[0]

	spanByID := make(map[string]TraceSpan)
	for _, s := range spans {
		spanByID[s.SpanID] = s
	}

	spanC := spanByID[string(cEvent.ID)]
	// ParentID should be one of the direct predecessors.
	if spanC.ParentID == "" {
		t.Error("C should have a parent")
	}
	aID := string(p.EventsByName("A")[0].ID)
	bID := string(p.EventsByName("B")[0].ID)
	if spanC.ParentID != aID && spanC.ParentID != bID {
		t.Error("C's parent should be A or B")
	}
}

func TestToTraceSpansEmpty(t *testing.T) {
	p := NewPoset()
	spans := p.ToTraceSpans()
	if len(spans) != 0 {
		t.Errorf("empty poset: want 0 spans, got %d", len(spans))
	}
}

func TestToTraceSpansStartTime(t *testing.T) {
	p := Build().Event("X").MustDone()
	spans := p.ToTraceSpans()
	if spans[0].StartTime.IsZero() {
		t.Error("StartTime should not be zero")
	}
}
