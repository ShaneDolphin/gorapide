package gorapide

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Every event-creation path registers its trivial class up front, so the
// fallback repair scan in ensureCausalClassesLocked must never run during
// ordinary inserts or a snapshot import. Before the size guard was added the
// scan ran on EVERY AddCausal, making edge insertion O(|events|) and a
// snapshot import O(|events| x |edges|) — a 90s import for a ~19k-event /
// ~231k-edge poset that took under a second on v0.1.0.
func TestEnsureCausalClassesDoesNotScanOnOrdinaryInserts(t *testing.T) {
	const n = 300
	p := NewPoset()
	ids := make([]EventID, n)
	for i := 0; i < n; i++ {
		e := NewEvent("E", "S", map[string]any{"i": i})
		if err := p.AddEvent(e); err != nil {
			t.Fatal(err)
		}
		ids[i] = e.ID
	}
	for i := 1; i < n; i++ {
		if err := p.AddCausal(ids[i-1], ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.AddCausalEquivalenceClass(ids[0], ids[0]); err != nil {
		t.Fatalf("trivial equivalence class: %v", err)
	}
	if got := p.classRepairScans; got != 0 {
		t.Fatalf("repair scan ran %d times during ordinary inserts; want 0", got)
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	q := NewPoset()
	if err := json.Unmarshal(data, q); err != nil {
		t.Fatal(err)
	}
	if got := q.classRepairScans; got != 0 {
		t.Fatalf("repair scan ran %d times during snapshot import; want 0", got)
	}
	if q.Len() != n || q.Stats().EdgeCount != n-1 {
		t.Fatalf("import lost content: %d events, %d edges", q.Len(), q.Stats().EdgeCount)
	}
}

// The repair scan must still run — and still repair — when the invariant is
// actually broken, so the guard is a fast path, not a removal.
func TestEnsureCausalClassesRepairsMissingRegistration(t *testing.T) {
	p := NewPoset()
	a := NewEvent("A", "S", nil)
	b := NewEvent("B", "S", nil)
	for _, e := range []*Event{a, b} {
		if err := p.AddEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	p.mu.Lock()
	delete(p.causalClass, b.ID)
	delete(p.classMembers, b.ID)
	p.mu.Unlock()

	if err := p.AddCausal(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.classRepairScans != 1 {
		t.Fatalf("repair scan count = %d; want exactly 1", p.classRepairScans)
	}
	if got := p.causalClass[b.ID]; got != b.ID {
		t.Fatalf("b not re-registered as its own class: %q", got)
	}
	if fmt.Sprint(p.classMembers[b.ID]) != fmt.Sprint([]EventID{b.ID}) {
		t.Fatalf("classMembers[b] = %v", p.classMembers[b.ID])
	}
}
