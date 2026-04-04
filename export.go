package gorapide

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// --- JSON serialization types ---

// EventExport is the JSON-serializable representation of an Event.
type EventExport struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Params   map[string]any `json:"params"`
	Source   string         `json:"source"`
	Lamport  uint64         `json:"lamport"`
	WallTime string         `json:"wall_time"`
}

// PosetExport is the JSON-serializable representation of a Poset.
type PosetExport struct {
	Events      []EventExport     `json:"events"`
	CausalEdges [][]string        `json:"causal_edges"`
	Metadata    map[string]string `json:"metadata"`
}

// MarshalJSON implements json.Marshaler for Poset.
func (p *Poset) MarshalJSON() ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Sort events by Lamport for deterministic output.
	ids := make([]EventID, 0, len(p.events))
	for id := range p.events {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return p.events[ids[i]].Clock.Lamport < p.events[ids[j]].Clock.Lamport
	})

	events := make([]EventExport, 0, len(ids))
	for _, id := range ids {
		e := p.events[id]
		events = append(events, EventExport{
			ID:       string(e.ID),
			Name:     e.Name,
			Params:   e.Params,
			Source:   e.Source,
			Lamport:  e.Clock.Lamport,
			WallTime: e.Clock.WallTime.Format(time.RFC3339Nano),
		})
	}

	var edges [][]string
	for _, fromID := range ids {
		succs := sortedSuccessors(p.causalEdges[fromID], p.events)
		for _, toID := range succs {
			edges = append(edges, []string{string(fromID), string(toID)})
		}
	}

	edgeCount := 0
	for _, succs := range p.causalEdges {
		edgeCount += len(succs)
	}

	export := PosetExport{
		Events:      events,
		CausalEdges: edges,
		Metadata: map[string]string{
			"event_count": fmt.Sprintf("%d", len(p.events)),
			"edge_count":  fmt.Sprintf("%d", edgeCount),
			"exported_at": time.Now().Format(time.RFC3339),
		},
	}
	return json.Marshal(export)
}

// UnmarshalJSON implements json.Unmarshaler for Poset.
func (p *Poset) UnmarshalJSON(data []byte) error {
	var export PosetExport
	if err := json.Unmarshal(data, &export); err != nil {
		return fmt.Errorf("gorapide.Poset.UnmarshalJSON: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Reset internal state.
	p.events = make(map[EventID]*Event, len(export.Events))
	p.causalEdges = make(map[EventID]map[EventID]bool, len(export.Events))
	p.reverseCausal = make(map[EventID]map[EventID]bool, len(export.Events))
	p.lamportCounter = 0

	// Restore events.
	for _, ee := range export.Events {
		wallTime, err := time.Parse(time.RFC3339Nano, ee.WallTime)
		if err != nil {
			return fmt.Errorf("gorapide.Poset.UnmarshalJSON: parsing wall_time for %s: %w", ee.ID, err)
		}
		e := &Event{
			ID:        EventID(ee.ID),
			Name:      ee.Name,
			Params:    ee.Params,
			Source:    ee.Source,
			Clock:     ClockStamp{Lamport: ee.Lamport, WallTime: wallTime},
			Immutable: true,
		}
		if e.Params == nil {
			e.Params = make(map[string]any)
		}
		p.events[e.ID] = e
		p.causalEdges[e.ID] = make(map[EventID]bool)
		p.reverseCausal[e.ID] = make(map[EventID]bool)
		if ee.Lamport > p.lamportCounter {
			p.lamportCounter = ee.Lamport
		}
	}

	// Restore causal edges.
	for _, edge := range export.CausalEdges {
		if len(edge) != 2 {
			return fmt.Errorf("gorapide.Poset.UnmarshalJSON: invalid edge %v", edge)
		}
		from := EventID(edge[0])
		to := EventID(edge[1])
		if _, ok := p.events[from]; !ok {
			return fmt.Errorf("gorapide.Poset.UnmarshalJSON: edge references unknown event %s", from)
		}
		if _, ok := p.events[to]; !ok {
			return fmt.Errorf("gorapide.Poset.UnmarshalJSON: edge references unknown event %s", to)
		}
		p.causalEdges[from][to] = true
		p.reverseCausal[to][from] = true
	}

	return nil
}

// sortedSuccessors returns successor IDs sorted by Lamport timestamp.
func sortedSuccessors(succs map[EventID]bool, events map[EventID]*Event) []EventID {
	ids := make([]EventID, 0, len(succs))
	for id := range succs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return events[ids[i]].Clock.Lamport < events[ids[j]].Clock.Lamport
	})
	return ids
}

// --- DOT with options ---

// DOTOptions configures the DOT export.
type DOTOptions struct {
	ColorBySource   bool      // different colors per component
	ShowParams      bool      // include param values in labels
	ShowTimestamps  bool      // include Lamport timestamps
	HighlightPath   []EventID // highlight a specific causal path
	ClusterBySource bool      // group nodes by source component in subgraphs
}

// DOTWithOptions exports the poset as Graphviz DOT with configurable options.
func (p *Poset) DOTWithOptions(opts DOTOptions) string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	ids := sortedEventIDs(p.events)
	highlightSet := make(map[EventID]bool, len(opts.HighlightPath))
	for _, id := range opts.HighlightPath {
		highlightSet[id] = true
	}

	// Build source → color map.
	sourceColors := make(map[string]string)
	if opts.ColorBySource {
		palette := []string{
			"#4285F4", "#EA4335", "#FBBC05", "#34A853",
			"#FF6D01", "#46BDC6", "#7B61FF", "#F538A0",
		}
		sources := sortedSources(p.events)
		for i, src := range sources {
			sourceColors[src] = palette[i%len(palette)]
		}
	}

	var b strings.Builder
	b.WriteString("digraph poset {\n")
	b.WriteString("  rankdir=TB;\n")
	b.WriteString("  node [shape=box, style=\"rounded,filled\", fillcolor=\"#FFFFFF\"];\n")

	if opts.ClusterBySource {
		p.writeDOTClustered(&b, ids, opts, sourceColors, highlightSet)
	} else {
		p.writeDOTFlat(&b, ids, opts, sourceColors, highlightSet)
	}

	// Edges.
	for _, fromID := range ids {
		succs := sortedSuccessors(p.causalEdges[fromID], p.events)
		for _, toID := range succs {
			attrs := ""
			if highlightSet[fromID] && highlightSet[toID] {
				attrs = ` [color="red", penwidth=2]`
			}
			b.WriteString(fmt.Sprintf("  %q -> %q%s;\n", string(fromID), string(toID), attrs))
		}
	}

	b.WriteString("}\n")
	return b.String()
}

func (p *Poset) writeDOTFlat(b *strings.Builder, ids []EventID, opts DOTOptions, sourceColors map[string]string, highlightSet map[EventID]bool) {
	for _, id := range ids {
		e := p.events[id]
		label := p.dotLabel(e, opts)
		attrs := p.dotNodeAttrs(e, opts, sourceColors, highlightSet)
		b.WriteString(fmt.Sprintf("  %q [label=\"%s\"%s];\n", string(id), label, attrs))
	}
}

func (p *Poset) writeDOTClustered(b *strings.Builder, ids []EventID, opts DOTOptions, sourceColors map[string]string, highlightSet map[EventID]bool) {
	bySource := make(map[string][]EventID)
	for _, id := range ids {
		src := p.events[id].Source
		bySource[src] = append(bySource[src], id)
	}
	sources := sortedSources(p.events)
	for _, src := range sources {
		clusterName := src
		if clusterName == "" {
			clusterName = "unknown"
		}
		b.WriteString(fmt.Sprintf("  subgraph \"cluster_%s\" {\n", clusterName))
		b.WriteString(fmt.Sprintf("    label=\"%s\";\n", clusterName))
		if color, ok := sourceColors[src]; ok {
			b.WriteString(fmt.Sprintf("    color=\"%s\";\n", color))
		}
		for _, id := range bySource[src] {
			e := p.events[id]
			label := p.dotLabel(e, opts)
			attrs := p.dotNodeAttrs(e, opts, sourceColors, highlightSet)
			b.WriteString(fmt.Sprintf("    %q [label=\"%s\"%s];\n", string(id), label, attrs))
		}
		b.WriteString("  }\n")
	}
}

func (p *Poset) dotLabel(e *Event, opts DOTOptions) string {
	parts := []string{e.Name}
	if opts.ShowTimestamps {
		parts = append(parts, fmt.Sprintf("L=%d", e.Clock.Lamport))
	}
	if opts.ShowParams && len(e.Params) > 0 {
		keys := make([]string, 0, len(e.Params))
		for k := range e.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", k, e.Params[k]))
		}
	}
	return strings.Join(parts, "\\n")
}

func (p *Poset) dotNodeAttrs(e *Event, opts DOTOptions, sourceColors map[string]string, highlightSet map[EventID]bool) string {
	var attrs []string
	if color, ok := sourceColors[e.Source]; ok && opts.ColorBySource {
		attrs = append(attrs, fmt.Sprintf("fillcolor=\"%s\"", color))
		attrs = append(attrs, `fontcolor="white"`)
	}
	if highlightSet[e.ID] {
		attrs = append(attrs, `color="red"`, `penwidth=2`)
	}
	if len(attrs) == 0 {
		return ""
	}
	return ", " + strings.Join(attrs, ", ")
}

func sortedEventIDs(events map[EventID]*Event) []EventID {
	ids := make([]EventID, 0, len(events))
	for id := range events {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return events[ids[i]].Clock.Lamport < events[ids[j]].Clock.Lamport
	})
	return ids
}

func sortedSources(events map[EventID]*Event) []string {
	seen := make(map[string]bool)
	for _, e := range events {
		seen[e.Source] = true
	}
	sources := make([]string, 0, len(seen))
	for s := range seen {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	return sources
}

// --- Mermaid ---

// Mermaid exports the poset as a Mermaid flowchart for embedding in markdown.
func (p *Poset) Mermaid() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	ids := sortedEventIDs(p.events)

	var b strings.Builder
	b.WriteString("graph TD\n")

	for _, id := range ids {
		e := p.events[id]
		nodeID := mermaidNodeID(id)
		label := e.Name
		if e.Source != "" {
			label = fmt.Sprintf("%s\\n@%s", e.Name, e.Source)
		}
		b.WriteString(fmt.Sprintf("  %s[\"%s\"]\n", nodeID, label))
	}

	for _, fromID := range ids {
		succs := sortedSuccessors(p.causalEdges[fromID], p.events)
		for _, toID := range succs {
			b.WriteString(fmt.Sprintf("  %s --> %s\n", mermaidNodeID(fromID), mermaidNodeID(toID)))
		}
	}

	return b.String()
}

// mermaidNodeID creates a Mermaid-safe node identifier from an EventID.
func mermaidNodeID(id EventID) string {
	s := string(id)
	return "n" + strings.ReplaceAll(s, "-", "")
}

// --- TraceSpan ---

// TraceSpan represents an OpenTelemetry-compatible trace span derived from
// a poset event. Causal edges become parent-child span relationships.
type TraceSpan struct {
	TraceID    string
	SpanID     string
	ParentID   string
	Name       string
	StartTime  time.Time
	EndTime    time.Time
	Attributes map[string]string
}

// ToTraceSpans converts poset events to OpenTelemetry-compatible trace spans.
// All spans share the same TraceID. SpanID maps to EventID. ParentID maps to
// the first direct causal predecessor (lowest Lamport).
func (p *Poset) ToTraceSpans() []TraceSpan {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.events) == 0 {
		return nil
	}

	// Generate a stable TraceID from the first root's ID.
	traceID := generateTraceID(p.events)

	ids := sortedEventIDs(p.events)
	spans := make([]TraceSpan, 0, len(ids))

	for _, id := range ids {
		e := p.events[id]

		// Find parent: first direct predecessor by Lamport order.
		parentID := ""
		preds := sortedPredecessors(p.reverseCausal[id], p.events)
		if len(preds) > 0 {
			parentID = string(preds[0])
		}

		// Build attributes from params + source.
		attrs := make(map[string]string)
		if e.Source != "" {
			attrs["source"] = e.Source
		}
		for k, v := range e.Params {
			attrs[k] = fmt.Sprintf("%v", v)
		}

		spans = append(spans, TraceSpan{
			TraceID:    traceID,
			SpanID:     string(e.ID),
			ParentID:   parentID,
			Name:       e.Name,
			StartTime:  e.Clock.WallTime,
			EndTime:    e.Clock.WallTime,
			Attributes: attrs,
		})
	}

	return spans
}

func generateTraceID(events map[EventID]*Event) string {
	// Use a deterministic trace ID based on sorted event IDs.
	ids := make([]string, 0, len(events))
	for id := range events {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	if len(ids) > 0 {
		// Use first 32 hex chars of the first event ID (stripped of dashes).
		raw := strings.ReplaceAll(ids[0], "-", "")
		if len(raw) >= 32 {
			return raw[:32]
		}
		return raw
	}
	return ""
}

func sortedPredecessors(preds map[EventID]bool, events map[EventID]*Event) []EventID {
	ids := make([]EventID, 0, len(preds))
	for id := range preds {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return events[ids[i]].Clock.Lamport < events[ids[j]].Clock.Lamport
	})
	return ids
}
