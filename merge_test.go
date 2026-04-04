package gorapide

import (
	"testing"
	"time"
)

func TestMergeDisjointPosets(t *testing.T) {
	// Local poset: A -> B
	local := NewPoset()
	a := &Event{ID: "A", Name: "a", Source: "local", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	b := &Event{ID: "B", Name: "b", Source: "local", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := local.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	if err := local.AddEvent(b); err != nil {
		t.Fatal(err)
	}
	if err := local.AddCausal("A", "B"); err != nil {
		t.Fatal(err)
	}

	// Remote poset: C -> D
	remote := NewPoset()
	c := &Event{ID: "C", Name: "c", Source: "remote", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	d := &Event{ID: "D", Name: "d", Source: "remote", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := remote.AddEvent(c); err != nil {
		t.Fatal(err)
	}
	if err := remote.AddEvent(d); err != nil {
		t.Fatal(err)
	}
	if err := remote.AddCausal("C", "D"); err != nil {
		t.Fatal(err)
	}

	snap := remote.CreateSnapshot("remote-node")
	result, err := local.MergeSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}

	if result.EventsAdded != 2 {
		t.Errorf("expected 2 events added, got %d", result.EventsAdded)
	}
	if result.EdgesAdded != 1 {
		t.Errorf("expected 1 edge added, got %d", result.EdgesAdded)
	}
	if local.Len() != 4 {
		t.Errorf("expected 4 events total, got %d", local.Len())
	}

	// Verify C < D is preserved.
	if !local.IsCausallyBefore("C", "D") {
		t.Error("expected C causally before D")
	}
}

func TestMergeOverlapping(t *testing.T) {
	// Local: has event A
	local := NewPoset()
	a := &Event{ID: "A", Name: "a", Source: "local", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := local.AddEvent(a); err != nil {
		t.Fatal(err)
	}

	// Remote: has A -> B
	remote := NewPoset()
	a2 := &Event{ID: "A", Name: "a", Source: "remote", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	b := &Event{ID: "B", Name: "b", Source: "remote", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := remote.AddEvent(a2); err != nil {
		t.Fatal(err)
	}
	if err := remote.AddEvent(b); err != nil {
		t.Fatal(err)
	}
	if err := remote.AddCausal("A", "B"); err != nil {
		t.Fatal(err)
	}

	snap := remote.CreateSnapshot("remote-node")
	result, err := local.MergeSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}

	if result.EventsAdded != 1 {
		t.Errorf("expected 1 event added (B only), got %d", result.EventsAdded)
	}
	if result.EventsSkipped != 1 {
		t.Errorf("expected 1 event skipped (A), got %d", result.EventsSkipped)
	}
	if local.Len() != 2 {
		t.Errorf("expected 2 events total, got %d", local.Len())
	}
	// Edge A->B should have been added.
	if !local.IsCausallyBefore("A", "B") {
		t.Error("expected A causally before B after merge")
	}
}

func TestMergeIdempotent(t *testing.T) {
	local := NewPoset()
	a := &Event{ID: "A", Name: "a", Source: "local", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := local.AddEvent(a); err != nil {
		t.Fatal(err)
	}

	remote := NewPoset()
	b := &Event{ID: "B", Name: "b", Source: "remote", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := remote.AddEvent(b); err != nil {
		t.Fatal(err)
	}

	snap := remote.CreateSnapshot("remote-node")

	// First merge.
	r1, err := local.MergeSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	if r1.EventsAdded != 1 {
		t.Errorf("first merge: expected 1 added, got %d", r1.EventsAdded)
	}

	// Second merge — should be no-op.
	r2, err := local.MergeSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	if r2.EventsAdded != 0 {
		t.Errorf("second merge: expected 0 added, got %d", r2.EventsAdded)
	}
	if r2.EventsSkipped != 1 {
		t.Errorf("second merge: expected 1 skipped, got %d", r2.EventsSkipped)
	}
	if local.Len() != 2 {
		t.Errorf("expected 2 events total, got %d", local.Len())
	}
}

func TestMergeLamportReconciliation(t *testing.T) {
	local := NewPoset()
	a := &Event{ID: "A", Name: "a", Source: "local", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := local.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	// local lamportCounter is now 1

	// Remote has high Lamport values.
	snap := &Snapshot{
		NodeID: "remote-node",
		Events: []EventExport{
			{ID: "R1", Name: "r1", Source: "remote", Lamport: 100, WallTime: time.Now().Format(time.RFC3339Nano)},
		},
		HighWater: 100,
	}

	if _, err := local.MergeSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	// Now add a new local event; its Lamport should be > 100.
	c := &Event{ID: "C", Name: "c", Source: "local", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := local.AddEvent(c); err != nil {
		t.Fatal(err)
	}

	ev, ok := local.Event("C")
	if !ok {
		t.Fatal("event C not found")
	}
	if ev.Clock.Lamport <= 100 {
		t.Errorf("expected Lamport > 100, got %d", ev.Clock.Lamport)
	}
}

func TestMergePendingEdges(t *testing.T) {
	local := NewPoset()
	a := &Event{ID: "A", Name: "a", Source: "local", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := local.AddEvent(a); err != nil {
		t.Fatal(err)
	}

	// Snapshot with edge A -> X, but X doesn't exist locally or in snapshot.
	snap := &Snapshot{
		NodeID:      "remote-node",
		Events:      []EventExport{},
		CausalEdges: [][]string{{"A", "X"}},
		HighWater:   1,
	}

	result, err := local.MergeSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}

	if result.EdgesPending != 1 {
		t.Errorf("expected 1 pending edge, got %d", result.EdgesPending)
	}
	if local.PendingEdgeCount() != 1 {
		t.Errorf("expected PendingEdgeCount 1, got %d", local.PendingEdgeCount())
	}
}

func TestCreateSnapshot(t *testing.T) {
	p := NewPoset()
	a := &Event{ID: "A", Name: "a", Source: "s", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	b := &Event{ID: "B", Name: "b", Source: "s", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := p.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	if err := p.AddEvent(b); err != nil {
		t.Fatal(err)
	}
	if err := p.AddCausal("A", "B"); err != nil {
		t.Fatal(err)
	}

	snap := p.CreateSnapshot("node-1")

	if snap.NodeID != "node-1" {
		t.Errorf("expected NodeID 'node-1', got %q", snap.NodeID)
	}
	if len(snap.Events) != 2 {
		t.Errorf("expected 2 events, got %d", len(snap.Events))
	}
	if len(snap.CausalEdges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(snap.CausalEdges))
	}
	if snap.HighWater != 2 {
		t.Errorf("expected HighWater 2, got %d", snap.HighWater)
	}
}

func TestCreateIncrementalSnapshot(t *testing.T) {
	p := NewPoset()
	a := &Event{ID: "A", Name: "a", Source: "s", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	b := &Event{ID: "B", Name: "b", Source: "s", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	c := &Event{ID: "C", Name: "c", Source: "s", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := p.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	if err := p.AddEvent(b); err != nil {
		t.Fatal(err)
	}
	if err := p.AddEvent(c); err != nil {
		t.Fatal(err)
	}
	if err := p.AddCausal("A", "B"); err != nil {
		t.Fatal(err)
	}
	if err := p.AddCausal("B", "C"); err != nil {
		t.Fatal(err)
	}

	// A has Lamport 1, B has Lamport 2 (bumped by edge from A), C has Lamport 3.
	// Only events with Lamport >= 2 should be included.
	snap := p.CreateIncrementalSnapshot("node-1", 2)

	if len(snap.Events) != 2 {
		t.Errorf("expected 2 events (B, C), got %d", len(snap.Events))
	}
	// Verify the events are B and C.
	ids := map[string]bool{}
	for _, ee := range snap.Events {
		ids[ee.ID] = true
	}
	if !ids["B"] || !ids["C"] {
		t.Errorf("expected events B and C, got %v", ids)
	}
	// Edge B->C should be included.
	if len(snap.CausalEdges) != 1 {
		t.Errorf("expected 1 edge (B->C), got %d", len(snap.CausalEdges))
	}
}

func TestDrainPendingEdges(t *testing.T) {
	local := NewPoset()
	a := &Event{ID: "A", Name: "a", Source: "s", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := local.AddEvent(a); err != nil {
		t.Fatal(err)
	}

	// Create a snapshot with edge A->X where X is missing.
	snap := &Snapshot{
		NodeID:      "remote",
		Events:      []EventExport{},
		CausalEdges: [][]string{{"A", "X"}},
		HighWater:   1,
	}
	if _, err := local.MergeSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	if local.PendingEdgeCount() != 1 {
		t.Fatalf("expected 1 pending edge, got %d", local.PendingEdgeCount())
	}

	// Drain before X exists — nothing should resolve.
	resolved, errs := local.DrainPendingEdges()
	if resolved != 0 {
		t.Errorf("expected 0 resolved, got %d", resolved)
	}
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
	if local.PendingEdgeCount() != 1 {
		t.Errorf("expected 1 pending edge still, got %d", local.PendingEdgeCount())
	}

	// Now add event X.
	x := &Event{ID: "X", Name: "x", Source: "s", Params: map[string]any{}, Clock: ClockStamp{WallTime: time.Now()}}
	if err := local.AddEvent(x); err != nil {
		t.Fatal(err)
	}

	// Drain again — should resolve.
	resolved, errs = local.DrainPendingEdges()
	if resolved != 1 {
		t.Errorf("expected 1 resolved, got %d", resolved)
	}
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
	if local.PendingEdgeCount() != 0 {
		t.Errorf("expected 0 pending edges, got %d", local.PendingEdgeCount())
	}

	// Verify edge A->X now exists.
	if !local.IsCausallyBefore("A", "X") {
		t.Error("expected A causally before X after drain")
	}
}
