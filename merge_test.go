package gorapide

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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
	a2 := &Event{ID: "A", Name: "a", Source: "local", Params: map[string]any{}, Clock: ClockStamp{WallTime: a.Clock.WallTime}}
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

func TestMergeSnapshotRejectsMalformedWallTimeWithoutAmbientRepair(t *testing.T) {
	stamp := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	snapshot := &Snapshot{
		NodeID: "remote",
		Events: []EventExport{
			{ID: "valid", Name: "Valid", Source: "remote", Lamport: 1, WallTime: stamp.Format(time.RFC3339Nano)},
			{ID: "invalid", Name: "Invalid", Source: "remote", Lamport: 2, WallTime: "not-a-time"},
		},
		HighWater: 2,
	}

	local := NewPoset()
	result, err := local.MergeSnapshot(snapshot)
	if !errors.Is(err, ErrInvalidSnapshotMerge) {
		t.Fatalf("MergeSnapshot error = %v, want ErrInvalidSnapshotMerge", err)
	}
	if result.EventsAdded != 1 || result.EventsSkipped != 1 {
		t.Fatalf("MergeResult = %+v, want one added and one skipped event", result)
	}
	if _, ok := local.Event("valid"); !ok {
		t.Fatal("valid event was not retained in the explicit partial result")
	}
	if _, ok := local.Event("invalid"); ok {
		t.Fatal("invalid wall time was repaired and admitted")
	}
	if !strings.Contains(err.Error(), `event[1] "invalid": invalid wall_time "not-a-time"`) {
		t.Fatalf("diagnostic lacks stable event/timestamp context: %v", err)
	}

	second := NewPoset()
	_, secondErr := second.MergeSnapshot(snapshot)
	if secondErr == nil || secondErr.Error() != err.Error() {
		t.Fatalf("repeated malformed merge diagnostic changed:\nfirst:  %v\nsecond: %v", err, secondErr)
	}
}

func TestMergeSnapshotRejectsConflictingExistingEvent(t *testing.T) {
	stamp := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	local := NewPoset()
	existing := &Event{ID: "same", Name: "Original", Source: "source", Params: map[string]any{"value": 1}, Clock: ClockStamp{WallTime: stamp}}
	if err := local.AddEvent(existing); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	snapshot := &Snapshot{
		NodeID: "remote",
		Events: []EventExport{{
			ID: "same", Name: "Conflicting", Source: "source", Params: map[string]any{"value": 1},
			Lamport: 1, WallTime: stamp.Format(time.RFC3339Nano),
		}},
		HighWater: 1,
	}

	result, err := local.MergeSnapshot(snapshot)
	if !errors.Is(err, ErrInvalidSnapshotMerge) {
		t.Fatalf("MergeSnapshot error = %v, want ErrInvalidSnapshotMerge", err)
	}
	if result.EventsAdded != 0 || result.EventsSkipped != 1 {
		t.Fatalf("MergeResult = %+v, want conflicting occurrence skipped", result)
	}
	got, ok := local.Event("same")
	if !ok || got.Name != "Original" {
		t.Fatalf("conflicting first-arrival content changed local event: %+v", got)
	}
	if !strings.Contains(err.Error(), `existing event name "Original" conflicts with snapshot name "Conflicting"`) {
		t.Fatalf("conflict diagnostic lacks exact field values: %v", err)
	}
}

func TestMergeSnapshotRejectsDuplicateIDsWithinSnapshot(t *testing.T) {
	stamp := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	snapshot := &Snapshot{
		NodeID: "remote",
		Events: []EventExport{
			{ID: "duplicate", Name: "A", Source: "remote", Lamport: 1, WallTime: stamp},
			{ID: "duplicate", Name: "B", Source: "remote", Lamport: 2, WallTime: stamp},
		},
		HighWater: 2,
	}

	local := NewPoset()
	result, err := local.MergeSnapshot(snapshot)
	if !errors.Is(err, ErrInvalidSnapshotMerge) {
		t.Fatalf("MergeSnapshot error = %v, want ErrInvalidSnapshotMerge", err)
	}
	if result.EventsSkipped != 2 || local.Len() != 0 {
		t.Fatalf("duplicate snapshot IDs were partially admitted: result=%+v len=%d", result, local.Len())
	}
	if !strings.Contains(err.Error(), `event ID occurs 2 times in one snapshot`) {
		t.Fatalf("duplicate diagnostic missing: %v", err)
	}
}

func TestMergeSnapshotAggregatesMalformedAndCyclicEdges(t *testing.T) {
	stamp := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	snapshot := &Snapshot{
		NodeID: "remote",
		Events: []EventExport{
			{ID: "A", Name: "A", Source: "remote", Lamport: 1, WallTime: stamp},
			{ID: "B", Name: "B", Source: "remote", Lamport: 1, WallTime: stamp},
		},
		CausalEdges: [][]string{{"A"}, {"A", "A"}, {"A", "B"}, {"A", "B"}},
		HighWater:   1,
	}

	local := NewPoset()
	result, err := local.MergeSnapshot(snapshot)
	if !errors.Is(err, ErrInvalidSnapshotMerge) || !errors.Is(err, ErrSelfCausal) {
		t.Fatalf("MergeSnapshot error = %v, want aggregate invalid-snapshot and self-causal errors", err)
	}
	if result.EventsAdded != 2 || result.EdgesAdded != 1 || result.EdgesSkipped != 3 {
		t.Fatalf("MergeResult = %+v, want two events, one edge, and three skipped edges", result)
	}
	if !local.IsCausallyBefore("A", "B") {
		t.Fatal("valid edge was not retained in the explicit partial result")
	}
	mergeErr, ok := err.(*SnapshotMergeError)
	if !ok || len(mergeErr.Issues) != 2 {
		t.Fatalf("aggregate issues = %#v, want two stable issues", mergeErr)
	}
}

func TestSnapshotAndPresentationOrderUseEventIDTieBreak(t *testing.T) {
	stamp := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	local := NewPoset()
	if _, err := local.MergeSnapshot(&Snapshot{
		NodeID: "remote",
		Events: []EventExport{
			{ID: "B", Name: "B", Source: "remote", Lamport: 1, WallTime: stamp},
			{ID: "A", Name: "A", Source: "remote", Lamport: 1, WallTime: stamp},
		},
		HighWater: 1,
	}); err != nil {
		t.Fatalf("MergeSnapshot: %v", err)
	}

	full := local.CreateSnapshot("local")
	if got := []string{full.Events[0].ID, full.Events[1].ID}; got[0] != "A" || got[1] != "B" {
		t.Fatalf("CreateSnapshot event order = %v, want [A B]", got)
	}
	incremental := local.CreateIncrementalSnapshot("local", 1)
	if got := []string{incremental.Events[0].ID, incremental.Events[1].ID}; got[0] != "A" || got[1] != "B" {
		t.Fatalf("CreateIncrementalSnapshot event order = %v, want [A B]", got)
	}
	encoded, err := json.Marshal(local)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var exported PosetExport
	if err := json.Unmarshal(encoded, &exported); err != nil {
		t.Fatalf("decode PosetExport: %v", err)
	}
	if got := []string{exported.Events[0].ID, exported.Events[1].ID}; got[0] != "A" || got[1] != "B" {
		t.Fatalf("MarshalJSON event order = %v, want [A B]", got)
	}
	if dot := local.DOT(); strings.Index(dot, `"A" [`) > strings.Index(dot, `"B" [`) {
		t.Fatalf("DOT event order lacks EventID tie-break:\n%s", dot)
	}
}

func TestCreateSnapshotOwnsExportedParameters(t *testing.T) {
	local := NewPoset()
	event := &Event{
		ID: "owned", Name: "Owned", Source: "local",
		Params: map[string]any{"value": int64(1), "nested": map[string]any{"key": "original"}},
		Clock:  ClockStamp{WallTime: time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)},
	}
	if err := local.AddEvent(event); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	snapshot := local.CreateSnapshot("local")
	snapshot.Events[0].Params["value"] = int64(2)
	snapshot.Events[0].Params["nested"].(map[string]any)["key"] = "mutated"

	stored, ok := local.Event("owned")
	if !ok {
		t.Fatal("stored event is missing")
	}
	if stored.Params["value"] != int64(1) || stored.Params["nested"].(map[string]any)["key"] != "original" {
		t.Fatalf("snapshot parameter mutation escaped into the poset: %#v", stored.Params)
	}
}

func TestMergeSnapshotDiagnosticsAndPartialResultIgnoreInputOrder(t *testing.T) {
	stamp := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	events := []EventExport{
		{ID: "C", Name: "C", Source: "remote", Lamport: 1, WallTime: stamp},
		{ID: "invalid", Name: "Invalid", Source: "remote", Lamport: 2, WallTime: "bad-time"},
		{ID: "A", Name: "A", Source: "remote", Lamport: 1, WallTime: stamp},
	}
	edges := [][]string{{"A", "C"}, {"A"}, {"A", "C"}}
	leftSnapshot := &Snapshot{NodeID: "remote", Events: events, CausalEdges: edges, HighWater: 2}
	rightSnapshot := &Snapshot{
		NodeID:      "remote",
		Events:      []EventExport{events[2], events[0], events[1]},
		CausalEdges: [][]string{edges[2], edges[1], edges[0]},
		HighWater:   2,
	}

	left := NewPoset()
	leftResult, leftErr := left.MergeSnapshot(leftSnapshot)
	right := NewPoset()
	rightResult, rightErr := right.MergeSnapshot(rightSnapshot)
	if leftErr == nil || rightErr == nil || leftErr.Error() != rightErr.Error() {
		t.Fatalf("shuffled diagnostics differ:\nleft:  %v\nright: %v", leftErr, rightErr)
	}
	if !reflect.DeepEqual(leftResult, rightResult) {
		t.Fatalf("shuffled partial results differ: left=%+v right=%+v", leftResult, rightResult)
	}
	leftCanonical, err := left.MarshalCanonical()
	if err != nil {
		t.Fatalf("left MarshalCanonical: %v", err)
	}
	rightCanonical, err := right.MarshalCanonical()
	if err != nil {
		t.Fatalf("right MarshalCanonical: %v", err)
	}
	if !bytes.Equal(leftCanonical, rightCanonical) {
		t.Fatalf("shuffled partial computations differ:\nleft:  %s\nright: %s", leftCanonical, rightCanonical)
	}
}
