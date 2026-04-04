package gorapide

import (
	"testing"
)

// Compile-time interface satisfaction checks.
var (
	_ EventStore     = (*Poset)(nil)
	_ CausalStore    = (*Poset)(nil)
	_ PosetQuerier   = (*Poset)(nil)
	_ PosetReadWriter = (*Poset)(nil)
)

// All tests in this file use the interface types rather than *Poset,
// proving that the interfaces are correctly implemented.

func newPosetViaInterface() PosetReadWriter {
	return NewPoset()
}

func TestEventStoreInterface(t *testing.T) {
	var store EventStore = NewPoset()

	e1 := NewEvent("Alpha", "src", map[string]any{"x": 1})
	e2 := NewEvent("Beta", "src", nil)
	e3 := NewEvent("Alpha", "src", nil)

	if err := store.Add(e1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Add(e2); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Add(e3); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if store.Len() != 3 {
		t.Errorf("Len: want 3, got %d", store.Len())
	}

	got, ok := store.Get(e1.ID)
	if !ok || got.ID != e1.ID {
		t.Error("Get should find e1")
	}
	_, ok = store.Get(EventID("nonexistent"))
	if ok {
		t.Error("Get should return false for missing ID")
	}

	all := store.All()
	if len(all) != 3 {
		t.Errorf("All: want 3, got %d", len(all))
	}

	alphas := store.ByName("Alpha")
	if len(alphas) != 2 {
		t.Errorf("ByName(Alpha): want 2, got %d", len(alphas))
	}
	betas := store.ByName("Beta")
	if len(betas) != 1 {
		t.Errorf("ByName(Beta): want 1, got %d", len(betas))
	}
}

func TestEventStoreDuplicate(t *testing.T) {
	var store EventStore = NewPoset()
	e := NewEvent("X", "src", nil)
	if err := store.Add(e); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(e); err == nil {
		t.Error("Add duplicate should return error")
	}
}

func TestCausalStoreInterface(t *testing.T) {
	p := NewPoset()
	var cs CausalStore = p

	a := NewEvent("A", "src", nil)
	b := NewEvent("B", "src", nil)
	c := NewEvent("C", "src", nil)
	for _, e := range []*Event{a, b, c} {
		if err := p.AddEvent(e); err != nil {
			t.Fatal(err)
		}
	}

	// A -> B -> C
	if err := cs.AddEdge(a.ID, b.ID); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := cs.AddEdge(b.ID, c.ID); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	preds := cs.DirectPredecessors(c.ID)
	if len(preds) != 1 || preds[0] != b.ID {
		t.Errorf("DirectPredecessors(C): want [B], got %v", preds)
	}

	succs := cs.DirectSuccessors(a.ID)
	if len(succs) != 1 || succs[0] != b.ID {
		t.Errorf("DirectSuccessors(A): want [B], got %v", succs)
	}

	if !cs.HasPath(a.ID, c.ID) {
		t.Error("HasPath(A, C) should be true (transitive)")
	}
	if cs.HasPath(c.ID, a.ID) {
		t.Error("HasPath(C, A) should be false")
	}
	if cs.HasPath(a.ID, a.ID) {
		t.Error("HasPath(A, A) should be false (irreflexive)")
	}
}

func TestCausalStoreEdgeErrors(t *testing.T) {
	p := NewPoset()
	var cs CausalStore = p

	a := NewEvent("A", "src", nil)
	if err := p.AddEvent(a); err != nil {
		t.Fatal(err)
	}

	if err := cs.AddEdge(a.ID, EventID("missing")); err == nil {
		t.Error("AddEdge to missing event should error")
	}
	if err := cs.AddEdge(EventID("missing"), a.ID); err == nil {
		t.Error("AddEdge from missing event should error")
	}
}

func TestPosetQuerierInterface(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		Event("D").CausedBy("B", "C").
		MustDone()

	var q PosetQuerier = p

	aID := p.EventsByName("A")[0].ID
	bID := p.EventsByName("B")[0].ID
	cID := p.EventsByName("C")[0].ID
	dID := p.EventsByName("D")[0].ID

	// IsCausallyBefore
	if !q.IsCausallyBefore(aID, dID) {
		t.Error("A should be causally before D")
	}
	if q.IsCausallyBefore(dID, aID) {
		t.Error("D should not be causally before A")
	}

	// IsCausallyIndependent
	if !q.IsCausallyIndependent(bID, cID) {
		t.Error("B and C should be causally independent")
	}

	// CausalAncestors
	anc := q.CausalAncestors(dID)
	if len(anc) != 3 {
		t.Errorf("CausalAncestors(D): want 3, got %d", len(anc))
	}

	// CausalDescendants
	desc := q.CausalDescendants(aID)
	if len(desc) != 3 {
		t.Errorf("CausalDescendants(A): want 3, got %d", len(desc))
	}

	// CausalChain
	chain, err := q.CausalChain(aID, dID)
	if err != nil {
		t.Fatalf("CausalChain: %v", err)
	}
	if len(chain) != 4 {
		t.Errorf("CausalChain(A,D): want 4, got %d", len(chain))
	}

	// Roots
	roots := q.Roots()
	if len(roots) != 1 || roots[0].Name != "A" {
		t.Error("Roots should be [A]")
	}

	// Leaves
	leaves := q.Leaves()
	if len(leaves) != 1 || leaves[0].Name != "D" {
		t.Error("Leaves should be [D]")
	}

	// TopologicalSort
	sorted := q.TopologicalSort()
	if len(sorted) != 4 {
		t.Fatalf("TopologicalSort: want 4, got %d", len(sorted))
	}
	pos := make(map[EventID]int)
	for i, e := range sorted {
		pos[e.ID] = i
	}
	if pos[aID] >= pos[dID] {
		t.Error("A must come before D in topological order")
	}
}

func TestPosetReadWriterInterface(t *testing.T) {
	var rw PosetReadWriter = newPosetViaInterface()

	// Use the full interface to build a poset.
	a := NewEvent("Start", "engine", nil)
	if err := rw.Add(a); err != nil {
		t.Fatal(err)
	}

	b := NewEvent("Process", "engine", map[string]any{"step": 1})
	if err := rw.AddEventWithCause(b, a.ID); err != nil {
		t.Fatal(err)
	}

	c := NewEvent("End", "engine", nil)
	if err := rw.AddEventWithCause(c, b.ID); err != nil {
		t.Fatal(err)
	}

	// EventStore methods
	if rw.Len() != 3 {
		t.Errorf("Len: want 3, got %d", rw.Len())
	}
	got, ok := rw.Get(b.ID)
	if !ok || got.Name != "Process" {
		t.Error("Get should find Process event")
	}

	// CausalStore methods
	if !rw.HasPath(a.ID, c.ID) {
		t.Error("HasPath(Start, End) should be true")
	}
	preds := rw.DirectPredecessors(c.ID)
	if len(preds) != 1 || preds[0] != b.ID {
		t.Error("DirectPredecessors(End) should be [Process]")
	}

	// PosetQuerier methods
	if !rw.IsCausallyBefore(a.ID, c.ID) {
		t.Error("Start should be causally before End")
	}
	roots := rw.Roots()
	if len(roots) != 1 || roots[0].Name != "Start" {
		t.Error("root should be Start")
	}

	// Debug methods
	errs := rw.Validate()
	if len(errs) != 0 {
		t.Errorf("Validate: %v", errs)
	}

	stats := rw.Stats()
	if stats.EventCount != 3 || stats.EdgeCount != 2 {
		t.Errorf("Stats: want 3 events/2 edges, got %d/%d", stats.EventCount, stats.EdgeCount)
	}

	dot := rw.DOT()
	if len(dot) == 0 {
		t.Error("DOT should return non-empty string")
	}
}

func TestPosetReadWriterWithBuilder(t *testing.T) {
	// Verify that builder-created posets also satisfy the interface.
	var rw PosetReadWriter = Build().
		Source("test").
		Event("X").
		Event("Y").CausedBy("X").
		Event("Z").CausedBy("Y").
		MustDone()

	if rw.Len() != 3 {
		t.Errorf("Len: want 3, got %d", rw.Len())
	}

	xID := rw.ByName("X")[0].ID
	zID := rw.ByName("Z")[0].ID

	if !rw.HasPath(xID, zID) {
		t.Error("X should have path to Z")
	}

	sorted := rw.TopologicalSort()
	if len(sorted) != 3 {
		t.Fatalf("TopologicalSort: want 3, got %d", len(sorted))
	}
	if sorted[0].Name != "X" {
		t.Error("first in topo sort should be X")
	}
}

// Verify MapTarget and BindingTarget interfaces exist and are documentable.
func TestPlaceholderInterfacesExist(t *testing.T) {
	// These are compile-time checks. The interfaces exist but have no
	// implementations yet. We verify they are properly defined by
	// attempting type assertions on nil values.
	var m MapTarget
	if m != nil {
		t.Error("nil MapTarget should be nil")
	}

	var b BindingTarget
	if b != nil {
		t.Error("nil BindingTarget should be nil")
	}
}
