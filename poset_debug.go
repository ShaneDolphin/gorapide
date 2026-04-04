package gorapide

import (
	"fmt"
	"sort"
	"strings"
)

// PosetStats holds aggregate statistics about a Poset.
type PosetStats struct {
	EventCount     int
	EdgeCount      int
	RootCount      int
	LeafCount      int
	MaxDepth       int     // longest causal chain
	AvgFanOut      float64 // average number of direct effects per event
	ComponentCount int     // number of distinct Source values
}

// DOT exports the poset as a Graphviz DOT format string.
func (p *Poset) DOT() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var b strings.Builder
	b.WriteString("digraph poset {\n")
	b.WriteString("  rankdir=TB;\n")
	b.WriteString("  node [shape=box, style=rounded];\n")

	// Collect and sort event IDs for deterministic output.
	ids := make([]EventID, 0, len(p.events))
	for id := range p.events {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return p.events[ids[i]].Clock.Lamport < p.events[ids[j]].Clock.Lamport
	})

	// Nodes.
	for _, id := range ids {
		e := p.events[id]
		label := fmt.Sprintf(`%s\n%s`, e.Name, e.ID.Short())
		b.WriteString(fmt.Sprintf("  %q [label=\"%s\"];\n", string(id), label))
	}

	// Edges.
	for _, fromID := range ids {
		succs := make([]EventID, 0, len(p.causalEdges[fromID]))
		for toID := range p.causalEdges[fromID] {
			succs = append(succs, toID)
		}
		sort.Slice(succs, func(i, j int) bool {
			return p.events[succs[i]].Clock.Lamport < p.events[succs[j]].Clock.Lamport
		})
		for _, toID := range succs {
			b.WriteString(fmt.Sprintf("  %q -> %q;\n", string(fromID), string(toID)))
		}
	}

	b.WriteString("}\n")
	return b.String()
}

// String returns a human-readable multi-line summary of the poset.
func (p *Poset) String() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var b strings.Builder

	edgeCount := 0
	for _, succs := range p.causalEdges {
		edgeCount += len(succs)
	}

	b.WriteString(fmt.Sprintf("Poset: %d events, %d causal edges\n", len(p.events), edgeCount))

	// Roots.
	var roots []string
	for id, e := range p.events {
		if len(p.reverseCausal[id]) == 0 {
			roots = append(roots, fmt.Sprintf("%s[%s]", e.Name, e.ID.Short()))
		}
	}
	sort.Strings(roots)
	b.WriteString(fmt.Sprintf("Roots: %s\n", strings.Join(roots, ", ")))

	// Leaves.
	var leaves []string
	for id, e := range p.events {
		if len(p.causalEdges[id]) == 0 {
			leaves = append(leaves, fmt.Sprintf("%s[%s]", e.Name, e.ID.Short()))
		}
	}
	sort.Strings(leaves)
	b.WriteString(fmt.Sprintf("Leaves: %s\n", strings.Join(leaves, ", ")))

	// Causal structure via topological order.
	b.WriteString("Causal structure:\n")
	sorted := p.topoSortLocked()
	for _, e := range sorted {
		preds := make([]string, 0, len(p.reverseCausal[e.ID]))
		for pred := range p.reverseCausal[e.ID] {
			preds = append(preds, p.events[pred].Name)
		}
		sort.Strings(preds)
		if len(preds) == 0 {
			b.WriteString(fmt.Sprintf("  %s[%s] @%s (root)\n", e.Name, e.ID.Short(), e.Source))
		} else {
			b.WriteString(fmt.Sprintf("  %s[%s] @%s <- %s\n", e.Name, e.ID.Short(), e.Source, strings.Join(preds, ", ")))
		}
	}

	return b.String()
}

// topoSortLocked is TopologicalSort without locking (caller must hold lock).
func (p *Poset) topoSortLocked() []*Event {
	inDegree := make(map[EventID]int, len(p.events))
	for id := range p.events {
		inDegree[id] = len(p.reverseCausal[id])
	}
	queue := make([]EventID, 0)
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	result := make([]*Event, 0, len(p.events))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		result = append(result, p.events[cur])
		for succ := range p.causalEdges[cur] {
			inDegree[succ]--
			if inDegree[succ] == 0 {
				queue = append(queue, succ)
			}
		}
	}
	return result
}

// Validate checks internal consistency of the poset and returns any errors found.
func (p *Poset) Validate() []error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var errs []error

	// Check all events are frozen.
	for _, e := range p.events {
		if !e.Immutable {
			errs = append(errs, fmt.Errorf("event %s (%s) is not frozen", e.Name, e.ID.Short()))
		}
	}

	// Check no dangling edge references.
	for fromID, succs := range p.causalEdges {
		if _, ok := p.events[fromID]; !ok {
			errs = append(errs, fmt.Errorf("causalEdges contains non-existent source event %s", fromID.Short()))
		}
		for toID := range succs {
			if _, ok := p.events[toID]; !ok {
				errs = append(errs, fmt.Errorf("causalEdges contains non-existent target event %s (from %s)", toID.Short(), fromID.Short()))
			}
		}
	}
	for toID, preds := range p.reverseCausal {
		if _, ok := p.events[toID]; !ok {
			errs = append(errs, fmt.Errorf("reverseCausal contains non-existent target event %s", toID.Short()))
		}
		for fromID := range preds {
			if _, ok := p.events[fromID]; !ok {
				errs = append(errs, fmt.Errorf("reverseCausal contains non-existent source event %s (to %s)", fromID.Short(), toID.Short()))
			}
		}
	}

	// Check edge symmetry: causalEdges[a][b] iff reverseCausal[b][a].
	for fromID, succs := range p.causalEdges {
		for toID := range succs {
			if !p.reverseCausal[toID][fromID] {
				errs = append(errs, fmt.Errorf("causalEdges[%s][%s] exists but reverseCausal[%s][%s] does not",
					fromID.Short(), toID.Short(), toID.Short(), fromID.Short()))
			}
		}
	}

	// Check Lamport consistency: for every causal edge from->to, from.Lamport < to.Lamport.
	for fromID, succs := range p.causalEdges {
		fromEvent := p.events[fromID]
		if fromEvent == nil {
			continue
		}
		for toID := range succs {
			toEvent := p.events[toID]
			if toEvent == nil {
				continue
			}
			if fromEvent.Clock.Lamport >= toEvent.Clock.Lamport {
				errs = append(errs, fmt.Errorf("Lamport violation: %s(%d) >= %s(%d)",
					fromEvent.Name, fromEvent.Clock.Lamport,
					toEvent.Name, toEvent.Clock.Lamport))
			}
		}
	}

	// Check for cycles using topological sort (if topo sort yields fewer
	// events than total, there is a cycle).
	sorted := p.topoSortLocked()
	if len(sorted) != len(p.events) {
		errs = append(errs, fmt.Errorf("cycle detected: topological sort yielded %d events, expected %d",
			len(sorted), len(p.events)))
	}

	return errs
}

// Stats returns aggregate statistics about the poset.
func (p *Poset) Stats() PosetStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	edgeCount := 0
	for _, succs := range p.causalEdges {
		edgeCount += len(succs)
	}

	rootCount := 0
	leafCount := 0
	sources := make(map[string]bool)
	for id, e := range p.events {
		if len(p.reverseCausal[id]) == 0 {
			rootCount++
		}
		if len(p.causalEdges[id]) == 0 {
			leafCount++
		}
		sources[e.Source] = true
	}

	var avgFanOut float64
	if len(p.events) > 0 {
		avgFanOut = float64(edgeCount) / float64(len(p.events))
	}

	// MaxDepth: longest path from any root to any leaf.
	maxDepth := p.maxDepthLocked()

	return PosetStats{
		EventCount:     len(p.events),
		EdgeCount:      edgeCount,
		RootCount:      rootCount,
		LeafCount:      leafCount,
		MaxDepth:       maxDepth,
		AvgFanOut:      avgFanOut,
		ComponentCount: len(sources),
	}
}

// maxDepthLocked computes the longest causal chain length. Uses dynamic
// programming on topological order. Caller must hold at least a read lock.
func (p *Poset) maxDepthLocked() int {
	if len(p.events) == 0 {
		return 0
	}
	sorted := p.topoSortLocked()
	depth := make(map[EventID]int, len(sorted))
	maxD := 1
	for _, e := range sorted {
		d := 1
		for pred := range p.reverseCausal[e.ID] {
			if depth[pred]+1 > d {
				d = depth[pred] + 1
			}
		}
		depth[e.ID] = d
		if d > maxD {
			maxD = d
		}
	}
	return maxD
}

// PosetBuilder provides a fluent API for constructing posets concisely.
type PosetBuilder struct {
	source string
	entries []builderEntry
	err     error
}

type builderEntry struct {
	name   string
	params map[string]any
	source string
	causes []string // names of causal predecessors
}

// Build creates a new PosetBuilder.
func Build() *PosetBuilder {
	return &PosetBuilder{source: "default"}
}

// Source sets the source component for subsequent events.
func (b *PosetBuilder) Source(component string) *PosetBuilder {
	b.source = component
	return b
}

// Event adds an event with the given name and optional key-value parameter pairs.
func (b *PosetBuilder) Event(name string, params ...any) *PosetBuilder {
	if b.err != nil {
		return b
	}
	if len(params)%2 != 0 {
		b.err = fmt.Errorf("Event(%q): params must be key-value pairs (got %d args)", name, len(params))
		return b
	}
	m := make(map[string]any, len(params)/2)
	for i := 0; i < len(params); i += 2 {
		key, ok := params[i].(string)
		if !ok {
			b.err = fmt.Errorf("Event(%q): param key at position %d must be a string, got %T", name, i, params[i])
			return b
		}
		m[key] = params[i+1]
	}
	b.entries = append(b.entries, builderEntry{
		name:   name,
		params: m,
		source: b.source,
	})
	return b
}

// CausedBy declares that the last added event was caused by the named events.
// Names refer to previously added events. If multiple events share a name,
// the most recently added one with that name is used.
func (b *PosetBuilder) CausedBy(names ...string) *PosetBuilder {
	if b.err != nil {
		return b
	}
	if len(b.entries) == 0 {
		b.err = fmt.Errorf("CausedBy called with no preceding Event")
		return b
	}
	b.entries[len(b.entries)-1].causes = names
	return b
}

// Done finalizes the builder and returns the constructed Poset.
func (b *PosetBuilder) Done() (*Poset, error) {
	if b.err != nil {
		return nil, b.err
	}
	p := NewPoset()
	// Map from event name to the most recently created Event with that name.
	byName := make(map[string]*Event)

	for _, entry := range b.entries {
		e := NewEvent(entry.name, entry.source, entry.params)

		// Resolve causes.
		causeIDs := make([]EventID, 0, len(entry.causes))
		for _, causeName := range entry.causes {
			causeEvent, ok := byName[causeName]
			if !ok {
				return nil, fmt.Errorf("CausedBy(%q): no event named %q has been added yet", entry.name, causeName)
			}
			causeIDs = append(causeIDs, causeEvent.ID)
		}

		if err := p.AddEventWithCause(e, causeIDs...); err != nil {
			return nil, fmt.Errorf("building event %q: %w", entry.name, err)
		}
		byName[entry.name] = e
	}

	return p, nil
}

// MustDone finalizes the builder and returns the Poset, panicking on error.
func (b *PosetBuilder) MustDone() *Poset {
	p, err := b.Done()
	if err != nil {
		panic(fmt.Sprintf("PosetBuilder.MustDone: %v", err))
	}
	return p
}
