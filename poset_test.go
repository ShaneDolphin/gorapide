package gorapide

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// helper to create a named event and add it to the poset.
func addNamedEvent(t *testing.T, p *Poset, name string) *Event {
	t.Helper()
	e := NewEvent(name, "test", nil)
	if err := p.AddEvent(e); err != nil {
		t.Fatalf("AddEvent(%s): %v", name, err)
	}
	return e
}

// buildDiamond creates:  A -> B, A -> C, B -> D, C -> D
func buildDiamond(t *testing.T) (*Poset, *Event, *Event, *Event, *Event) {
	t.Helper()
	p := NewPoset()
	a := addNamedEvent(t, p, "A")
	b := addNamedEvent(t, p, "B")
	c := addNamedEvent(t, p, "C")
	d := addNamedEvent(t, p, "D")
	for _, edge := range [][2]EventID{{a.ID, b.ID}, {a.ID, c.ID}, {b.ID, d.ID}, {c.ID, d.ID}} {
		if err := p.AddCausal(edge[0], edge[1]); err != nil {
			t.Fatalf("AddCausal(%s->%s): %v", edge[0].Short(), edge[1].Short(), err)
		}
	}
	return p, a, b, c, d
}

// buildLinearChain creates: A -> B -> C -> D
func buildLinearChain(t *testing.T) (*Poset, []*Event) {
	t.Helper()
	p := NewPoset()
	names := []string{"A", "B", "C", "D"}
	events := make([]*Event, len(names))
	for i, name := range names {
		events[i] = addNamedEvent(t, p, name)
	}
	for i := 0; i < len(events)-1; i++ {
		if err := p.AddCausal(events[i].ID, events[i+1].ID); err != nil {
			t.Fatalf("AddCausal: %v", err)
		}
	}
	return p, events
}

func TestPosetNewPoset(t *testing.T) {
	p := NewPoset()
	if p.Len() != 0 {
		t.Errorf("new poset should be empty, got Len()=%d", p.Len())
	}
	if len(p.Events()) != 0 {
		t.Error("new poset Events() should be empty")
	}
	if len(p.Roots()) != 0 {
		t.Error("new poset Roots() should be empty")
	}
	if len(p.Leaves()) != 0 {
		t.Error("new poset Leaves() should be empty")
	}
}

func TestPosetAddEvent(t *testing.T) {
	p := NewPoset()
	e := NewEvent("Test", "src", map[string]any{"x": 1})
	if err := p.AddEvent(e); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	if p.Len() != 1 {
		t.Errorf("expected Len()=1, got %d", p.Len())
	}
	if !e.Immutable {
		t.Error("event should be frozen after AddEvent")
	}
	if e.Clock.Lamport == 0 {
		t.Error("Lamport timestamp should be assigned (>0) after AddEvent")
	}
}

func TestPosetAddEventDuplicate(t *testing.T) {
	p := NewPoset()
	e := NewEvent("Test", "src", nil)
	if err := p.AddEvent(e); err != nil {
		t.Fatalf("first AddEvent: %v", err)
	}
	err := p.AddEvent(e)
	if err == nil {
		t.Fatal("adding duplicate event should return error")
	}
	if !errors.Is(err, ErrEventExists) {
		t.Errorf("expected ErrEventExists, got: %v", err)
	}
}

func TestPosetEventLookup(t *testing.T) {
	p := NewPoset()
	e := addNamedEvent(t, p, "Lookup")
	found, ok := p.Event(e.ID)
	if !ok {
		t.Fatal("Event() should find the event")
	}
	if found.ID != e.ID {
		t.Error("Event() returned wrong event")
	}
	_, ok = p.Event(EventID("nonexistent"))
	if ok {
		t.Error("Event() should return false for missing ID")
	}
}

func TestPosetEventsByName(t *testing.T) {
	p := NewPoset()
	addNamedEvent(t, p, "Alpha")
	addNamedEvent(t, p, "Beta")
	addNamedEvent(t, p, "Alpha")

	alphas := p.EventsByName("Alpha")
	if len(alphas) != 2 {
		t.Errorf("expected 2 Alpha events, got %d", len(alphas))
	}
	betas := p.EventsByName("Beta")
	if len(betas) != 1 {
		t.Errorf("expected 1 Beta event, got %d", len(betas))
	}
	gammas := p.EventsByName("Gamma")
	if len(gammas) != 0 {
		t.Errorf("expected 0 Gamma events, got %d", len(gammas))
	}
}

// --- Diamond poset tests ---

func TestDiamondCausallyBefore(t *testing.T) {
	p, a, b, c, d := buildDiamond(t)

	// Direct edges.
	if !p.IsCausallyBefore(a.ID, b.ID) {
		t.Error("A should be causally before B")
	}
	if !p.IsCausallyBefore(a.ID, c.ID) {
		t.Error("A should be causally before C")
	}
	if !p.IsCausallyBefore(b.ID, d.ID) {
		t.Error("B should be causally before D")
	}
	if !p.IsCausallyBefore(c.ID, d.ID) {
		t.Error("C should be causally before D")
	}
	// Transitive.
	if !p.IsCausallyBefore(a.ID, d.ID) {
		t.Error("A should be transitively causally before D")
	}
	// Not reverse.
	if p.IsCausallyBefore(d.ID, a.ID) {
		t.Error("D should not be causally before A")
	}
	// Irreflexive.
	if p.IsCausallyBefore(a.ID, a.ID) {
		t.Error("A should not be causally before itself")
	}
}

func TestDiamondCausallyIndependent(t *testing.T) {
	p, _, b, c, _ := buildDiamond(t)

	if !p.IsCausallyIndependent(b.ID, c.ID) {
		t.Error("B and C should be causally independent")
	}
	if !p.IsCausallyIndependent(c.ID, b.ID) {
		t.Error("C and B should be causally independent (symmetric)")
	}
}

func TestDiamondDirectCauses(t *testing.T) {
	p, a, b, c, d := buildDiamond(t)

	// D has two direct causes: B and C.
	causes := p.DirectCauses(d.ID)
	if len(causes) != 2 {
		t.Fatalf("D should have 2 direct causes, got %d", len(causes))
	}
	if !causes.Contains(b.ID) || !causes.Contains(c.ID) {
		t.Error("D's direct causes should be B and C")
	}

	// A has no direct causes.
	causes = p.DirectCauses(a.ID)
	if len(causes) != 0 {
		t.Errorf("A should have 0 direct causes, got %d", len(causes))
	}
}

func TestDiamondDirectEffects(t *testing.T) {
	p, a, b, _, d := buildDiamond(t)

	// A has two direct effects: B and C.
	effects := p.DirectEffects(a.ID)
	if len(effects) != 2 {
		t.Fatalf("A should have 2 direct effects, got %d", len(effects))
	}
	if !effects.Contains(b.ID) {
		t.Error("A's direct effects should include B")
	}

	// D has no direct effects.
	effects = p.DirectEffects(d.ID)
	if len(effects) != 0 {
		t.Errorf("D should have 0 direct effects, got %d", len(effects))
	}
}

func TestDiamondCausalAncestors(t *testing.T) {
	p, a, b, c, d := buildDiamond(t)

	anc := p.CausalAncestors(d.ID)
	if len(anc) != 3 {
		t.Fatalf("D should have 3 ancestors (A,B,C), got %d", len(anc))
	}
	if !anc.Contains(a.ID) || !anc.Contains(b.ID) || !anc.Contains(c.ID) {
		t.Error("D's ancestors should include A, B, and C")
	}

	anc = p.CausalAncestors(a.ID)
	if len(anc) != 0 {
		t.Errorf("A should have 0 ancestors, got %d", len(anc))
	}
}

func TestDiamondCausalDescendants(t *testing.T) {
	p, a, b, c, d := buildDiamond(t)

	desc := p.CausalDescendants(a.ID)
	if len(desc) != 3 {
		t.Fatalf("A should have 3 descendants (B,C,D), got %d", len(desc))
	}
	if !desc.Contains(b.ID) || !desc.Contains(c.ID) || !desc.Contains(d.ID) {
		t.Error("A's descendants should include B, C, and D")
	}

	desc = p.CausalDescendants(d.ID)
	if len(desc) != 0 {
		t.Errorf("D should have 0 descendants, got %d", len(desc))
	}
}

func TestDiamondRoots(t *testing.T) {
	p, a, _, _, _ := buildDiamond(t)

	roots := p.Roots()
	if len(roots) != 1 {
		t.Fatalf("diamond should have 1 root, got %d", len(roots))
	}
	if roots[0].ID != a.ID {
		t.Error("the only root should be A")
	}
}

func TestDiamondLeaves(t *testing.T) {
	p, _, _, _, d := buildDiamond(t)

	leaves := p.Leaves()
	if len(leaves) != 1 {
		t.Fatalf("diamond should have 1 leaf, got %d", len(leaves))
	}
	if leaves[0].ID != d.ID {
		t.Error("the only leaf should be D")
	}
}

func TestDiamondCausalChain(t *testing.T) {
	p, a, b, c, d := buildDiamond(t)

	chain, err := p.CausalChain(a.ID, d.ID)
	if err != nil {
		t.Fatalf("CausalChain(A, D): %v", err)
	}
	// Should include A, B, C, D (all on some path).
	if len(chain) != 4 {
		t.Fatalf("chain A->D should include 4 events, got %d", len(chain))
	}
	if !chain.Contains(a.ID) || !chain.Contains(b.ID) || !chain.Contains(c.ID) || !chain.Contains(d.ID) {
		t.Error("chain should include all events in diamond")
	}
}

func TestDiamondTopologicalSort(t *testing.T) {
	p, a, b, c, d := buildDiamond(t)

	sorted := p.TopologicalSort()
	if len(sorted) != 4 {
		t.Fatalf("expected 4 events in topo sort, got %d", len(sorted))
	}

	// Build position map.
	pos := make(map[EventID]int)
	for i, e := range sorted {
		pos[e.ID] = i
	}

	// A must come before B, C, D.
	if pos[a.ID] >= pos[b.ID] {
		t.Error("A must come before B in topological order")
	}
	if pos[a.ID] >= pos[c.ID] {
		t.Error("A must come before C in topological order")
	}
	if pos[a.ID] >= pos[d.ID] {
		t.Error("A must come before D in topological order")
	}
	// B and C must come before D.
	if pos[b.ID] >= pos[d.ID] {
		t.Error("B must come before D in topological order")
	}
	if pos[c.ID] >= pos[d.ID] {
		t.Error("C must come before D in topological order")
	}
}

// --- Linear chain tests ---

func TestLinearChainTransitiveCausality(t *testing.T) {
	p, events := buildLinearChain(t)
	a, b, c, d := events[0], events[1], events[2], events[3]

	// All forward pairs should be causal.
	for i := 0; i < len(events); i++ {
		for j := i + 1; j < len(events); j++ {
			if !p.IsCausallyBefore(events[i].ID, events[j].ID) {
				t.Errorf("%s should be causally before %s", events[i].Name, events[j].Name)
			}
		}
	}
	// No reverse causality.
	if p.IsCausallyBefore(d.ID, a.ID) {
		t.Error("D should not be causally before A")
	}
	if p.IsCausallyBefore(c.ID, b.ID) {
		t.Error("C should not be causally before B")
	}
}

func TestLinearChainLamportMonotonic(t *testing.T) {
	_, events := buildLinearChain(t)

	for i := 0; i < len(events)-1; i++ {
		if events[i].Clock.Lamport >= events[i+1].Clock.Lamport {
			t.Errorf("Lamport should be monotonically increasing: %s(%d) >= %s(%d)",
				events[i].Name, events[i].Clock.Lamport,
				events[i+1].Name, events[i+1].Clock.Lamport)
		}
	}
}

func TestLinearChainRootsAndLeaves(t *testing.T) {
	p, events := buildLinearChain(t)

	roots := p.Roots()
	if len(roots) != 1 || roots[0].ID != events[0].ID {
		t.Error("linear chain should have exactly one root (A)")
	}
	leaves := p.Leaves()
	if len(leaves) != 1 || leaves[0].ID != events[3].ID {
		t.Error("linear chain should have exactly one leaf (D)")
	}
}

func TestLinearChainCausalChain(t *testing.T) {
	p, events := buildLinearChain(t)
	a, d := events[0], events[3]

	chain, err := p.CausalChain(a.ID, d.ID)
	if err != nil {
		t.Fatalf("CausalChain: %v", err)
	}
	if len(chain) != 4 {
		t.Errorf("expected all 4 events in chain, got %d", len(chain))
	}
}

// --- Cycle detection tests ---

func TestCycleDetection(t *testing.T) {
	p := NewPoset()
	a := addNamedEvent(t, p, "A")
	b := addNamedEvent(t, p, "B")
	c := addNamedEvent(t, p, "C")

	// A -> B -> C
	if err := p.AddCausal(a.ID, b.ID); err != nil {
		t.Fatalf("AddCausal(A,B): %v", err)
	}
	if err := p.AddCausal(b.ID, c.ID); err != nil {
		t.Fatalf("AddCausal(B,C): %v", err)
	}

	// Try C -> A: should fail with cycle error.
	err := p.AddCausal(c.ID, a.ID)
	if err == nil {
		t.Fatal("adding C->A should fail (creates cycle)")
	}
	if !errors.Is(err, ErrCyclicCausal) {
		t.Errorf("expected ErrCyclicCausal, got: %v", err)
	}
}

func TestSelfCausalRejected(t *testing.T) {
	p := NewPoset()
	a := addNamedEvent(t, p, "A")

	err := p.AddCausal(a.ID, a.ID)
	if err == nil {
		t.Fatal("self-causal edge should be rejected")
	}
	if !errors.Is(err, ErrSelfCausal) {
		t.Errorf("expected ErrSelfCausal, got: %v", err)
	}
}

func TestAddCausalMissingEvent(t *testing.T) {
	p := NewPoset()
	a := addNamedEvent(t, p, "A")

	err := p.AddCausal(a.ID, EventID("missing"))
	if err == nil {
		t.Fatal("AddCausal with missing 'to' should fail")
	}
	if !errors.Is(err, ErrEventNotFound) {
		t.Errorf("expected ErrEventNotFound, got: %v", err)
	}

	err = p.AddCausal(EventID("missing"), a.ID)
	if err == nil {
		t.Fatal("AddCausal with missing 'from' should fail")
	}
}

func TestAddCausalIdempotent(t *testing.T) {
	p := NewPoset()
	a := addNamedEvent(t, p, "A")
	b := addNamedEvent(t, p, "B")

	if err := p.AddCausal(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	// Adding same edge again should not error.
	if err := p.AddCausal(a.ID, b.ID); err != nil {
		t.Errorf("duplicate AddCausal should be idempotent, got: %v", err)
	}
}

// --- CausalChain error cases ---

func TestCausalChainNoPath(t *testing.T) {
	p := NewPoset()
	a := addNamedEvent(t, p, "A")
	b := addNamedEvent(t, p, "B")

	_, err := p.CausalChain(a.ID, b.ID)
	if err == nil {
		t.Fatal("CausalChain should fail when no path exists")
	}
	if !errors.Is(err, ErrNoPath) {
		t.Errorf("expected ErrNoPath, got: %v", err)
	}
}

func TestCausalChainMissingEvent(t *testing.T) {
	p := NewPoset()
	a := addNamedEvent(t, p, "A")

	_, err := p.CausalChain(a.ID, EventID("missing"))
	if err == nil {
		t.Fatal("CausalChain with missing event should fail")
	}
	if !errors.Is(err, ErrEventNotFound) {
		t.Errorf("expected ErrEventNotFound, got: %v", err)
	}
}

// --- AddEventWithCause tests ---

func TestAddEventWithCause(t *testing.T) {
	p := NewPoset()
	a := addNamedEvent(t, p, "A")
	b := addNamedEvent(t, p, "B")

	c := NewEvent("C", "test", nil)
	if err := p.AddEventWithCause(c, a.ID, b.ID); err != nil {
		t.Fatalf("AddEventWithCause: %v", err)
	}
	if p.Len() != 3 {
		t.Errorf("expected 3 events, got %d", p.Len())
	}
	if !p.IsCausallyBefore(a.ID, c.ID) {
		t.Error("A should be causally before C")
	}
	if !p.IsCausallyBefore(b.ID, c.ID) {
		t.Error("B should be causally before C")
	}
	causes := p.DirectCauses(c.ID)
	if len(causes) != 2 {
		t.Errorf("C should have 2 direct causes, got %d", len(causes))
	}
}

func TestAddEventWithCauseLamport(t *testing.T) {
	p := NewPoset()
	a := addNamedEvent(t, p, "A")
	b := addNamedEvent(t, p, "B")

	c := NewEvent("C", "test", nil)
	if err := p.AddEventWithCause(c, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}

	// C's Lamport should be greater than both A and B.
	if c.Clock.Lamport <= a.Clock.Lamport {
		t.Errorf("C.Lamport(%d) should be > A.Lamport(%d)", c.Clock.Lamport, a.Clock.Lamport)
	}
	if c.Clock.Lamport <= b.Clock.Lamport {
		t.Errorf("C.Lamport(%d) should be > B.Lamport(%d)", c.Clock.Lamport, b.Clock.Lamport)
	}
}

func TestAddEventWithCauseMissingCause(t *testing.T) {
	p := NewPoset()
	a := addNamedEvent(t, p, "A")

	e := NewEvent("E", "test", nil)
	err := p.AddEventWithCause(e, a.ID, EventID("missing"))
	if err == nil {
		t.Fatal("should fail when a cause event is missing")
	}
	if !errors.Is(err, ErrEventNotFound) {
		t.Errorf("expected ErrEventNotFound, got: %v", err)
	}
	// Event should NOT have been added.
	if p.Len() != 1 {
		t.Errorf("failed AddEventWithCause should not add event, got Len()=%d", p.Len())
	}
}

func TestAddEventWithCauseNoCauses(t *testing.T) {
	p := NewPoset()
	e := NewEvent("Root", "test", nil)
	if err := p.AddEventWithCause(e); err != nil {
		t.Fatalf("AddEventWithCause with no causes: %v", err)
	}
	if p.Len() != 1 {
		t.Errorf("expected 1 event, got %d", p.Len())
	}
	roots := p.Roots()
	if len(roots) != 1 {
		t.Errorf("expected 1 root, got %d", len(roots))
	}
}

// --- TopologicalSort valid ordering ---

func TestTopologicalSortValidOrdering(t *testing.T) {
	p, a, b, c, d := buildDiamond(t)
	// Add extra events to make it more interesting.
	e := NewEvent("E", "test", nil)
	if err := p.AddEventWithCause(e, d.ID); err != nil {
		t.Fatal(err)
	}

	sorted := p.TopologicalSort()
	if len(sorted) != 5 {
		t.Fatalf("expected 5 events, got %d", len(sorted))
	}

	pos := make(map[EventID]int)
	for i, ev := range sorted {
		pos[ev.ID] = i
	}

	// Every causal edge (from, to) must have pos[from] < pos[to].
	edges := [][2]EventID{
		{a.ID, b.ID}, {a.ID, c.ID}, {b.ID, d.ID}, {c.ID, d.ID}, {d.ID, e.ID},
	}
	for _, edge := range edges {
		if pos[edge[0]] >= pos[edge[1]] {
			t.Errorf("topological order violated: %s (pos %d) should come before %s (pos %d)",
				p.events[edge[0]].Name, pos[edge[0]], p.events[edge[1]].Name, pos[edge[1]])
		}
	}
}

// --- Concurrent access test ---

func TestConcurrentAccess(t *testing.T) {
	p := NewPoset()
	root := addNamedEvent(t, p, "Root")

	const goroutines = 100
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			e := NewEvent(fmt.Sprintf("Event-%d", idx), "worker", map[string]any{"idx": idx})
			if err := p.AddEventWithCause(e, root.ID); err != nil {
				errs <- fmt.Errorf("goroutine %d: %w", idx, err)
				return
			}
			// Read operations while others write.
			_ = p.Events()
			_ = p.Len()
			_ = p.Roots()
			_ = p.Leaves()
			_ = p.IsCausallyBefore(root.ID, e.ID)
			_ = p.DirectCauses(e.ID)
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	if p.Len() != goroutines+1 {
		t.Errorf("expected %d events, got %d", goroutines+1, p.Len())
	}
}

// --- Lamport consistency ---

func TestLamportConsistencyWithCausalOrder(t *testing.T) {
	p := NewPoset()
	a := addNamedEvent(t, p, "A")
	b := addNamedEvent(t, p, "B")
	c := addNamedEvent(t, p, "C")
	d := addNamedEvent(t, p, "D")

	// Build: A -> C, B -> C, C -> D
	if err := p.AddCausal(a.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.AddCausal(b.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.AddCausal(c.ID, d.ID); err != nil {
		t.Fatal(err)
	}

	// For every causal pair, the Lamport timestamp must be strictly less.
	pairs := [][2]*Event{{a, c}, {b, c}, {c, d}, {a, d}, {b, d}}
	for _, pair := range pairs {
		if pair[0].Clock.Lamport >= pair[1].Clock.Lamport {
			t.Errorf("Lamport violated: %s(%d) should be < %s(%d)",
				pair[0].Name, pair[0].Clock.Lamport,
				pair[1].Name, pair[1].Clock.Lamport)
		}
	}
}

func TestLamportPropagation(t *testing.T) {
	// Build a chain where we add causal edges in a way that requires propagation.
	p := NewPoset()
	a := addNamedEvent(t, p, "A")
	b := addNamedEvent(t, p, "B")
	c := addNamedEvent(t, p, "C")

	// First: B -> C
	if err := p.AddCausal(b.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	cLamportBefore := c.Clock.Lamport

	// Now: A -> B (this should propagate to update C if needed)
	if err := p.AddCausal(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}

	if a.Clock.Lamport >= b.Clock.Lamport {
		t.Errorf("A.Lamport(%d) should be < B.Lamport(%d)", a.Clock.Lamport, b.Clock.Lamport)
	}
	if b.Clock.Lamport >= c.Clock.Lamport {
		t.Errorf("B.Lamport(%d) should be < C.Lamport(%d)", b.Clock.Lamport, c.Clock.Lamport)
	}
	_ = cLamportBefore // C's lamport may or may not have changed depending on values.
}

// --- Multiple roots and leaves ---

func TestMultipleRootsAndLeaves(t *testing.T) {
	p := NewPoset()
	r1 := addNamedEvent(t, p, "R1")
	r2 := addNamedEvent(t, p, "R2")
	mid := addNamedEvent(t, p, "Mid")
	l1 := addNamedEvent(t, p, "L1")
	l2 := addNamedEvent(t, p, "L2")

	// R1 -> Mid, R2 -> Mid, Mid -> L1, Mid -> L2
	for _, e := range [][2]EventID{{r1.ID, mid.ID}, {r2.ID, mid.ID}, {mid.ID, l1.ID}, {mid.ID, l2.ID}} {
		if err := p.AddCausal(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}

	roots := p.Roots()
	if len(roots) != 2 {
		t.Errorf("expected 2 roots, got %d", len(roots))
	}
	if !roots.Contains(r1.ID) || !roots.Contains(r2.ID) {
		t.Error("roots should be R1 and R2")
	}

	leaves := p.Leaves()
	if len(leaves) != 2 {
		t.Errorf("expected 2 leaves, got %d", len(leaves))
	}
	if !leaves.Contains(l1.ID) || !leaves.Contains(l2.ID) {
		t.Error("leaves should be L1 and L2")
	}
}

// --- Isolated events ---

func TestIsolatedEventsAreRootsAndLeaves(t *testing.T) {
	p := NewPoset()
	a := addNamedEvent(t, p, "A")
	b := addNamedEvent(t, p, "B")

	roots := p.Roots()
	leaves := p.Leaves()

	if len(roots) != 2 {
		t.Errorf("expected 2 roots, got %d", len(roots))
	}
	if len(leaves) != 2 {
		t.Errorf("expected 2 leaves, got %d", len(leaves))
	}

	if !p.IsCausallyIndependent(a.ID, b.ID) {
		t.Error("isolated events should be causally independent")
	}
}
