package gorapide

import (
	"fmt"
	"sort"
	"testing"
)

func depthTestEvent(t testing.TB, action, occurrence string, causes ...EventID) *Event {
	t.Helper()
	event, err := NewDeterministicEvent(EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "depth", Instance: "i", Action: action, Occurrence: occurrence, Causes: causes,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

// referenceCausalDepths recomputes longest-path depths with the sort-after-
// every-pop traversal the canonical encoder used through v0.2.5, so the
// heap-backed implementation can be checked against it on arbitrary shapes.
func referenceCausalDepths(p *Poset) map[EventID]uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	representatives := p.causalClassRepresentativesLocked()
	inDegree := make(map[EventID]int, len(representatives))
	ready := make([]EventID, 0)
	for _, id := range representatives {
		inDegree[id] = len(p.causalClassPredecessorsLocked(id))
		if inDegree[id] == 0 {
			ready = append(ready, id)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	classDepths := make(map[EventID]uint64, len(representatives))
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		depth := uint64(1)
		for _, predecessor := range p.causalClassPredecessorsLocked(current) {
			if candidate := classDepths[predecessor] + 1; candidate > depth {
				depth = candidate
			}
		}
		classDepths[current] = depth
		for _, successor := range p.causalClassSuccessorsLocked(current) {
			inDegree[successor]--
			if inDegree[successor] == 0 {
				ready = append(ready, successor)
			}
		}
		sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	}
	depths := make(map[EventID]uint64, len(p.events))
	for id := range p.events {
		depths[id] = classDepths[p.causalRepresentativeLocked(id)]
	}
	return depths
}

func assertDepthsMatchReference(t *testing.T, p *Poset) {
	t.Helper()
	p.mu.RLock()
	got, err := p.causalDepthsLocked()
	p.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	want := referenceCausalDepths(p)
	if len(got) != len(want) {
		t.Fatalf("depth map size %d, want %d", len(got), len(want))
	}
	for id, depth := range want {
		if got[id] != depth {
			t.Fatalf("event %s depth %d, want %d", id, got[id], depth)
		}
	}
}

func TestCausalDepthsHeapMatchesSortedTraversalOnWideFanOut(t *testing.T) {
	p := NewPoset()
	root := depthTestEvent(t, "root", "1")
	if err := p.AddEvent(root); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2000; i++ {
		child := depthTestEvent(t, "child", fmt.Sprint(i), root.ID)
		if err := p.AddEventWithCause(child, root.ID); err != nil {
			t.Fatal(err)
		}
	}
	assertDepthsMatchReference(t, p)
}

func TestCausalDepthsHeapMatchesSortedTraversalOnDiamondsAndClasses(t *testing.T) {
	p := NewPoset()
	a := depthTestEvent(t, "A", "1")
	b := depthTestEvent(t, "B", "1", a.ID)
	c := depthTestEvent(t, "C", "1", a.ID)
	d := depthTestEvent(t, "D", "1", b.ID, c.ID)
	e := depthTestEvent(t, "E", "1", d.ID)
	f := depthTestEvent(t, "F", "1", a.ID)
	for _, step := range []struct {
		event  *Event
		causes []EventID
	}{{a, nil}, {b, []EventID{a.ID}}, {c, []EventID{a.ID}}, {d, []EventID{b.ID, c.ID}}, {e, []EventID{d.ID}}, {f, []EventID{a.ID}}} {
		var err error
		if len(step.causes) == 0 {
			err = p.AddEvent(step.event)
		} else {
			err = p.AddEventWithCause(step.event, step.causes...)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	// A long path through F must win over the short direct edge.
	if err := p.AddCausal(f.ID, e.ID); err != nil {
		t.Fatal(err)
	}
	// Equivalence class collapses B and C into one representative.
	if err := p.AddCausalEquivalenceClass(b.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	// Several independent roots interleave with the main graph in ID order.
	for i := 0; i < 25; i++ {
		r := depthTestEvent(t, "R", fmt.Sprint(i))
		if err := p.AddEvent(r); err != nil {
			t.Fatal(err)
		}
		s := depthTestEvent(t, "S", fmt.Sprint(i), r.ID)
		if err := p.AddEventWithCause(s, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	assertDepthsMatchReference(t, p)

	p.mu.RLock()
	depths, err := p.causalDepthsLocked()
	p.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if depths[a.ID] != 1 || depths[b.ID] != 2 || depths[c.ID] != 2 || depths[d.ID] != 3 || depths[e.ID] != 4 || depths[f.ID] != 2 {
		t.Fatalf("unexpected depths: A=%d B=%d C=%d D=%d E=%d F=%d",
			depths[a.ID], depths[b.ID], depths[c.ID], depths[d.ID], depths[e.ID], depths[f.ID])
	}
}

func BenchmarkSemanticDigestWideFanOut(b *testing.B) {
	p := NewPoset()
	root := depthTestEvent(b, "root", "1")
	if err := p.AddEvent(root); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 16000; i++ {
		child := depthTestEvent(b, "child", fmt.Sprint(i), root.ID)
		if err := p.AddEventWithCause(child, root.ID); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.SemanticDigest(); err != nil {
			b.Fatal(err)
		}
	}
}
