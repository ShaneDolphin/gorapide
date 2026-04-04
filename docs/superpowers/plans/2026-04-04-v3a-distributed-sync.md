# V3-A: Distributed Poset Synchronization

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable multiple GoRapide instances to synchronize their posets across nodes while preserving causality, using vector clocks and a grow-only CRDT merge algorithm.

**Architecture:** Extend `ClockStamp` with an optional `VectorClock` field (nil = single-node, backward compatible). Add a `mergeEventLocked` path on Poset that preserves remote Lamport values instead of assigning new ones. A new `dsync/` package provides a `Transport` interface and `Coordinator` for background push/pull sync, with `MemTransport` for testing. Zero external dependencies.

**Tech Stack:** Go 1.22, zero new dependencies

---

## File Structure

### New files

| File | Responsibility |
|------|---------------|
| `event_vector.go` | `NodeID` type, `VectorClock` type with Merge/Increment/Before/Concurrent/Clone methods |
| `event_vector_test.go` | VectorClock operation tests |
| `merge.go` | `Snapshot` wire type, `MergeResult`, `PendingEdge`, `Poset.MergeSnapshot()`, `Poset.CreateSnapshot()` |
| `merge_test.go` | Merge algorithm tests |
| `dsync/transport.go` | `Transport` interface |
| `dsync/mem_transport.go` | `MemTransport` for testing |
| `dsync/mem_transport_test.go` | MemTransport tests |
| `dsync/coordinator.go` | `Coordinator` for background push/pull sync |
| `dsync/coordinator_test.go` | Coordinator tests including two-node convergence |

### Modified files

| File | Change |
|------|--------|
| `event.go` | Add `Vector VectorClock` field to `ClockStamp` |
| `poset.go` | Add `mergeEventLocked()`, `pendingEdges` field, `DrainPendingEdges()` |
| `export.go` | Add `VectorClock` to `EventExport`, update Marshal/Unmarshal |

---

## Task 1: VectorClock type

**Files:**
- Create: `event_vector.go`
- Create: `event_vector_test.go`
- Modify: `event.go`

- [ ] **Step 1: Write failing tests for VectorClock**

```go
// event_vector_test.go
package gorapide

import (
	"testing"
)

func TestVectorClockIncrement(t *testing.T) {
	vc := VectorClock{"node1": 1, "node2": 3}
	next := vc.Increment("node1")
	if next["node1"] != 2 {
		t.Errorf("node1: want 2, got %d", next["node1"])
	}
	if next["node2"] != 3 {
		t.Errorf("node2: want 3, got %d", next["node2"])
	}
	// Original unchanged.
	if vc["node1"] != 1 {
		t.Error("Increment should not mutate original")
	}
}

func TestVectorClockIncrementNew(t *testing.T) {
	vc := VectorClock{}
	next := vc.Increment("node1")
	if next["node1"] != 1 {
		t.Errorf("node1: want 1, got %d", next["node1"])
	}
}

func TestVectorClockMerge(t *testing.T) {
	a := VectorClock{"node1": 3, "node2": 1}
	b := VectorClock{"node1": 1, "node2": 5, "node3": 2}
	merged := a.Merge(b)
	if merged["node1"] != 3 {
		t.Errorf("node1: want 3, got %d", merged["node1"])
	}
	if merged["node2"] != 5 {
		t.Errorf("node2: want 5, got %d", merged["node2"])
	}
	if merged["node3"] != 2 {
		t.Errorf("node3: want 2, got %d", merged["node3"])
	}
}

func TestVectorClockBefore(t *testing.T) {
	a := VectorClock{"node1": 1, "node2": 2}
	b := VectorClock{"node1": 2, "node2": 3}
	if !a.Before(b) {
		t.Error("a should be before b")
	}
	if b.Before(a) {
		t.Error("b should not be before a")
	}
}

func TestVectorClockBeforeEqual(t *testing.T) {
	a := VectorClock{"node1": 2, "node2": 3}
	b := VectorClock{"node1": 2, "node2": 3}
	if a.Before(b) {
		t.Error("equal vectors are not before each other")
	}
}

func TestVectorClockConcurrent(t *testing.T) {
	a := VectorClock{"node1": 3, "node2": 1}
	b := VectorClock{"node1": 1, "node2": 3}
	if !a.Concurrent(b) {
		t.Error("a and b should be concurrent")
	}
	if a.Before(b) {
		t.Error("concurrent vectors: a should not be before b")
	}
	if b.Before(a) {
		t.Error("concurrent vectors: b should not be before a")
	}
}

func TestVectorClockClone(t *testing.T) {
	vc := VectorClock{"node1": 5}
	clone := vc.Clone()
	clone["node1"] = 99
	if vc["node1"] != 5 {
		t.Error("Clone should produce independent copy")
	}
}

func TestNilVectorClockBefore(t *testing.T) {
	var a VectorClock // nil
	b := VectorClock{"node1": 1}
	// nil is "before" any non-nil (all components are 0, which is <= and at least one <)
	if !a.Before(b) {
		t.Error("nil vector should be before non-nil")
	}
}

func TestNilVectorClockConcurrent(t *testing.T) {
	var a VectorClock // nil
	var b VectorClock // nil
	// Two nil vectors: equal, so not concurrent (not before each other, but not concurrent either)
	// Actually: Before returns false for equal, Concurrent checks !a.Before(b) && !b.Before(a)
	// For two nil: Before(nil, nil) => no entries with at-least-one-less => false
	// Concurrent(nil, nil) => !false && !false => true? But two empty vectors are equal...
	// This is an edge case. Let's say two nil are NOT concurrent (they're equal).
	if a.Concurrent(b) {
		t.Error("two nil vectors should not be concurrent (they are equal)")
	}
}

func TestClockStampBackwardCompat(t *testing.T) {
	// Existing code that only uses Lamport/WallTime should still work.
	cs := ClockStamp{Lamport: 5}
	if cs.Vector != nil {
		t.Error("Vector should default to nil")
	}
	other := ClockStamp{Lamport: 10}
	if !cs.Before(other) {
		t.Error("Before should still work with Lamport only")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -run "TestVectorClock|TestNilVectorClock|TestClockStampBackward" -v`
Expected: FAIL — `VectorClock` undefined

- [ ] **Step 3: Write VectorClock type**

```go
// event_vector.go
package gorapide

// NodeID identifies a node in a distributed GoRapide cluster.
type NodeID string

// VectorClock tracks logical time across multiple nodes.
// A nil VectorClock indicates single-node mode (backward compatible).
type VectorClock map[NodeID]uint64

// Increment returns a new VectorClock with the given node's counter incremented.
func (vc VectorClock) Increment(node NodeID) VectorClock {
	next := vc.Clone()
	if next == nil {
		next = make(VectorClock)
	}
	next[node]++
	return next
}

// Merge returns a new VectorClock that is the pointwise max of vc and other.
func (vc VectorClock) Merge(other VectorClock) VectorClock {
	merged := make(VectorClock, len(vc)+len(other))
	for k, v := range vc {
		merged[k] = v
	}
	for k, v := range other {
		if v > merged[k] {
			merged[k] = v
		}
	}
	return merged
}

// Before reports whether vc is causally before other.
// vc < other iff all(vc[k] <= other[k]) AND exists(vc[k] < other[k]).
func (vc VectorClock) Before(other VectorClock) bool {
	atLeastOneLess := false
	for k, v := range vc {
		ov := other[k]
		if v > ov {
			return false
		}
		if v < ov {
			atLeastOneLess = true
		}
	}
	for k, ov := range other {
		if _, ok := vc[k]; !ok && ov > 0 {
			atLeastOneLess = true
		}
	}
	return atLeastOneLess
}

// Concurrent reports whether neither vc < other nor other < vc.
// Two nil/empty vectors are considered equal (not concurrent).
func (vc VectorClock) Concurrent(other VectorClock) bool {
	if len(vc) == 0 && len(other) == 0 {
		return false
	}
	return !vc.Before(other) && !other.Before(vc)
}

// Clone returns a deep copy of the VectorClock.
func (vc VectorClock) Clone() VectorClock {
	if vc == nil {
		return nil
	}
	clone := make(VectorClock, len(vc))
	for k, v := range vc {
		clone[k] = v
	}
	return clone
}
```

- [ ] **Step 4: Add Vector field to ClockStamp**

In `event.go`, change the `ClockStamp` struct (lines 38-41) to:

```go
type ClockStamp struct {
	Lamport  uint64       // Logical Lamport timestamp for causal ordering
	WallTime time.Time    // Wall clock time for temporal ordering
	Vector   VectorClock  // Optional vector clock for distributed mode (nil = single-node)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -run "TestVectorClock|TestNilVectorClock|TestClockStampBackward" -v`
Expected: PASS

- [ ] **Step 6: Run full suite to confirm no regressions**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add event.go event_vector.go event_vector_test.go
git commit -m "feat: add VectorClock type and extend ClockStamp for distributed mode"
```

---

## Task 2: mergeEventLocked and MergeSnapshot

**Files:**
- Modify: `poset.go`
- Create: `merge.go`
- Create: `merge_test.go`

- [ ] **Step 1: Write failing tests for merge**

```go
// merge_test.go
package gorapide

import (
	"testing"
)

func TestMergeDisjointPosets(t *testing.T) {
	// Local poset: A -> B
	local := NewPoset()
	a := NewEvent("A", "node1", nil)
	b := NewEvent("B", "node1", nil)
	local.AddEvent(a)
	local.AddEventWithCause(b, a.ID)

	// Remote poset: C -> D (completely disjoint events)
	remote := NewPoset()
	c := NewEvent("C", "node2", nil)
	d := NewEvent("D", "node2", nil)
	remote.AddEvent(c)
	remote.AddEventWithCause(d, c.ID)

	// Create snapshot from remote.
	snap := remote.CreateSnapshot("node2")

	// Merge into local.
	result, err := local.MergeSnapshot(snap)
	if err != nil {
		t.Fatalf("MergeSnapshot: %v", err)
	}
	if result.EventsAdded != 2 {
		t.Errorf("EventsAdded: want 2, got %d", result.EventsAdded)
	}
	if result.EdgesAdded != 1 {
		t.Errorf("EdgesAdded: want 1, got %d", result.EdgesAdded)
	}
	if local.Len() != 4 {
		t.Errorf("Len: want 4, got %d", local.Len())
	}

	// Verify causal relationship preserved.
	if !local.IsCausallyBefore(c.ID, d.ID) {
		t.Error("C should causally precede D after merge")
	}
}

func TestMergeOverlapping(t *testing.T) {
	// Both posets have event A.
	local := NewPoset()
	a := NewEvent("A", "node1", nil)
	local.AddEvent(a)

	remote := NewPoset()
	// Add the SAME event (same ID) to remote.
	remote.mu.Lock()
	remote.events[a.ID] = &Event{
		ID: a.ID, Name: a.Name, Source: a.Source,
		Clock: ClockStamp{Lamport: 1}, Immutable: true,
		Params: make(map[string]any),
	}
	remote.causalEdges[a.ID] = make(map[EventID]bool)
	remote.reverseCausal[a.ID] = make(map[EventID]bool)
	remote.lamportCounter = 1
	remote.mu.Unlock()

	// Also add a new event B on remote.
	b := NewEvent("B", "node2", nil)
	remote.AddEventWithCause(b, a.ID)

	snap := remote.CreateSnapshot("node2")
	result, err := local.MergeSnapshot(snap)
	if err != nil {
		t.Fatalf("MergeSnapshot: %v", err)
	}
	if result.EventsSkipped != 1 {
		t.Errorf("EventsSkipped: want 1 (duplicate A), got %d", result.EventsSkipped)
	}
	if result.EventsAdded != 1 {
		t.Errorf("EventsAdded: want 1 (B), got %d", result.EventsAdded)
	}
	if local.Len() != 2 {
		t.Errorf("Len: want 2, got %d", local.Len())
	}
}

func TestMergeIdempotent(t *testing.T) {
	local := NewPoset()
	a := NewEvent("A", "src", nil)
	local.AddEvent(a)

	remote := NewPoset()
	b := NewEvent("B", "src", nil)
	remote.AddEvent(b)
	snap := remote.CreateSnapshot("node2")

	// Merge twice.
	result1, _ := local.MergeSnapshot(snap)
	result2, _ := local.MergeSnapshot(snap)

	if result1.EventsAdded != 1 {
		t.Errorf("first merge EventsAdded: want 1, got %d", result1.EventsAdded)
	}
	if result2.EventsAdded != 0 {
		t.Errorf("second merge EventsAdded: want 0 (idempotent), got %d", result2.EventsAdded)
	}
	if local.Len() != 2 {
		t.Errorf("Len: want 2, got %d", local.Len())
	}
}

func TestMergeLamportReconciliation(t *testing.T) {
	local := NewPoset()
	a := NewEvent("A", "src", nil)
	local.AddEvent(a) // Lamport = 1

	// Remote has events with high Lamport values.
	remote := NewPoset()
	for i := 0; i < 10; i++ {
		remote.AddEvent(NewEvent("R", "node2", nil))
	}
	// Remote's lamportCounter is now 10.
	snap := remote.CreateSnapshot("node2")

	local.MergeSnapshot(snap)

	// New local event should get Lamport > 10.
	newEvent := NewEvent("New", "node1", nil)
	local.AddEvent(newEvent)
	if newEvent.Clock.Lamport <= 10 {
		t.Errorf("new event Lamport should be > 10, got %d", newEvent.Clock.Lamport)
	}
}

func TestMergePendingEdges(t *testing.T) {
	local := NewPoset()
	a := NewEvent("A", "src", nil)
	local.AddEvent(a)

	// Create a snapshot that references an event not in local.
	missingID := NewEventID()
	snap := &Snapshot{
		NodeID: "node2",
		Events: []EventExport{},
		CausalEdges: [][]string{
			{string(a.ID), string(missingID)}, // edge to missing event
		},
	}

	result, err := local.MergeSnapshot(snap)
	if err != nil {
		t.Fatalf("MergeSnapshot: %v", err)
	}
	if result.EdgesPending != 1 {
		t.Errorf("EdgesPending: want 1, got %d", result.EdgesPending)
	}
}

func TestCreateSnapshot(t *testing.T) {
	p := NewPoset()
	a := NewEvent("A", "src", nil)
	b := NewEvent("B", "src", nil)
	p.AddEvent(a)
	p.AddEventWithCause(b, a.ID)

	snap := p.CreateSnapshot("node1")
	if snap.NodeID != "node1" {
		t.Errorf("NodeID: want node1, got %s", snap.NodeID)
	}
	if len(snap.Events) != 2 {
		t.Errorf("Events: want 2, got %d", len(snap.Events))
	}
	if len(snap.CausalEdges) != 1 {
		t.Errorf("CausalEdges: want 1, got %d", len(snap.CausalEdges))
	}
	if snap.HighWater != b.Clock.Lamport {
		t.Errorf("HighWater: want %d, got %d", b.Clock.Lamport, snap.HighWater)
	}
}

func TestCreateIncrementalSnapshot(t *testing.T) {
	p := NewPoset()
	a := NewEvent("A", "src", nil)
	b := NewEvent("B", "src", nil)
	c := NewEvent("C", "src", nil)
	p.AddEvent(a) // Lamport 1
	p.AddEvent(b) // Lamport 2
	p.AddEvent(c) // Lamport 3

	// Only events with Lamport >= 2.
	snap := p.CreateIncrementalSnapshot("node1", 2)
	if len(snap.Events) != 2 {
		t.Errorf("Events: want 2 (B and C), got %d", len(snap.Events))
	}
}

func TestDrainPendingEdges(t *testing.T) {
	local := NewPoset()
	a := NewEvent("A", "src", nil)
	local.AddEvent(a)

	b := NewEvent("B", "src", nil)
	// Don't add B yet — create snapshot with edge A->B
	snap := &Snapshot{
		NodeID:      "node2",
		Events:      []EventExport{},
		CausalEdges: [][]string{{string(a.ID), string(b.ID)}},
	}
	local.MergeSnapshot(snap) // Edge is pending (B missing)

	// Now add B.
	local.AddEvent(b)

	// Drain should resolve the pending edge.
	resolved, errs := local.DrainPendingEdges()
	if len(errs) != 0 {
		t.Errorf("DrainPendingEdges errors: %v", errs)
	}
	if resolved != 1 {
		t.Errorf("resolved: want 1, got %d", resolved)
	}
	if !local.IsCausallyBefore(a.ID, b.ID) {
		t.Error("A should causally precede B after drain")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -run "TestMerge|TestCreate|TestDrain" -v`
Expected: FAIL — `MergeSnapshot`, `CreateSnapshot`, etc. undefined

- [ ] **Step 3: Add mergeEventLocked to poset.go**

In `poset.go`, add `pendingEdges` field to `Poset` struct (after `lamportCounter` on line 24):

```go
	pendingEdges []PendingEdge
```

Add `mergeEventLocked` method after `addEventLocked` (after line 55):

```go
// mergeEventLocked adds a remote event preserving its original Lamport value.
// Unlike addEventLocked, it does NOT increment lamportCounter or assign a new Lamport.
// It only reconciles lamportCounter to max(local, remote).
func (p *Poset) mergeEventLocked(e *Event) error {
	if _, exists := p.events[e.ID]; exists {
		return fmt.Errorf("%w: %s", ErrEventExists, e.ID)
	}
	e.Freeze()
	p.events[e.ID] = e
	p.causalEdges[e.ID] = make(map[EventID]bool)
	p.reverseCausal[e.ID] = make(map[EventID]bool)
	// Reconcile: ensure future local events get higher Lamport values.
	if e.Clock.Lamport > p.lamportCounter {
		p.lamportCounter = e.Clock.Lamport
	}
	return nil
}
```

Add `DrainPendingEdges` public method (at the end of poset.go):

```go
// DrainPendingEdges retries buffered edges whose endpoints may now exist.
// Returns the number of edges resolved and any errors encountered.
func (p *Poset) DrainPendingEdges() (int, []error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var remaining []PendingEdge
	var errs []error
	resolved := 0

	for _, pe := range p.pendingEdges {
		_, fromOK := p.events[pe.From]
		_, toOK := p.events[pe.To]
		if !fromOK || !toOK {
			remaining = append(remaining, pe)
			continue
		}
		if err := p.addCausalLocked(pe.From, pe.To); err != nil {
			errs = append(errs, err)
		} else {
			resolved++
		}
	}
	p.pendingEdges = remaining
	return resolved, errs
}
```

- [ ] **Step 4: Write merge.go**

```go
// merge.go
package gorapide

import (
	"fmt"
	"sort"
	"time"
)

// Snapshot is the wire format for poset state exchange between nodes.
type Snapshot struct {
	NodeID      NodeID        `json:"node_id"`
	Events      []EventExport `json:"events"`
	CausalEdges [][]string    `json:"causal_edges"`
	HighWater   uint64        `json:"high_water"` // max Lamport seen by sender
}

// MergeResult reports what changed during a merge operation.
type MergeResult struct {
	EventsAdded   int
	EventsSkipped int // duplicates
	EdgesAdded    int
	EdgesSkipped  int
	EdgesPending  int // buffered (reference missing events)
}

// PendingEdge represents a causal edge where one or both endpoints haven't arrived yet.
type PendingEdge struct {
	From EventID
	To   EventID
}

// MergeSnapshot merges a remote node's snapshot into this poset.
// New events and edges are added; duplicates are silently skipped.
func (p *Poset) MergeSnapshot(snap *Snapshot) (*MergeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := &MergeResult{}

	// Sort events by Lamport for consistent insertion order.
	events := make([]EventExport, len(snap.Events))
	copy(events, snap.Events)
	sort.Slice(events, func(i, j int) bool {
		return events[i].Lamport < events[j].Lamport
	})

	// Phase 1: Merge events.
	for _, ee := range events {
		wallTime, err := time.Parse(time.RFC3339Nano, ee.WallTime)
		if err != nil {
			wallTime = time.Now()
		}

		e := &Event{
			ID:     EventID(ee.ID),
			Name:   ee.Name,
			Params: ee.Params,
			Source: ee.Source,
			Clock: ClockStamp{
				Lamport:  ee.Lamport,
				WallTime: wallTime,
			},
		}
		if e.Params == nil {
			e.Params = make(map[string]any)
		}
		// Restore vector clock if present.
		if ee.VectorClock != nil {
			vc := make(VectorClock, len(ee.VectorClock))
			for k, v := range ee.VectorClock {
				vc[NodeID(k)] = v
			}
			e.Clock.Vector = vc
		}

		if err := p.mergeEventLocked(e); err != nil {
			result.EventsSkipped++
		} else {
			result.EventsAdded++
		}
	}

	// Phase 2: Merge causal edges.
	for _, edge := range snap.CausalEdges {
		if len(edge) != 2 {
			continue
		}
		from := EventID(edge[0])
		to := EventID(edge[1])

		// Check if both endpoints exist.
		_, fromOK := p.events[from]
		_, toOK := p.events[to]

		if !fromOK || !toOK {
			p.pendingEdges = append(p.pendingEdges, PendingEdge{From: from, To: to})
			result.EdgesPending++
			continue
		}

		if err := p.addCausalLocked(from, to); err != nil {
			result.EdgesSkipped++
		} else {
			result.EdgesAdded++
		}
	}

	// Reconcile Lamport counter.
	if snap.HighWater > p.lamportCounter {
		p.lamportCounter = snap.HighWater
	}

	return result, nil
}

// CreateSnapshot creates a full snapshot of the current poset state.
func (p *Poset) CreateSnapshot(nodeID NodeID) *Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.createSnapshotLocked(nodeID, 0)
}

// CreateIncrementalSnapshot creates a snapshot containing only events
// with Lamport timestamps >= sinceHighWater.
func (p *Poset) CreateIncrementalSnapshot(nodeID NodeID, sinceHighWater uint64) *Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.createSnapshotLocked(nodeID, sinceHighWater)
}

func (p *Poset) createSnapshotLocked(nodeID NodeID, sinceHighWater uint64) *Snapshot {
	snap := &Snapshot{
		NodeID: nodeID,
	}

	// Collect events.
	ids := make([]EventID, 0, len(p.events))
	for id, e := range p.events {
		if e.Clock.Lamport >= sinceHighWater {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		return p.events[ids[i]].Clock.Lamport < p.events[ids[j]].Clock.Lamport
	})

	eventIDSet := make(map[EventID]bool, len(ids))
	for _, id := range ids {
		eventIDSet[id] = true
	}

	for _, id := range ids {
		e := p.events[id]
		ee := EventExport{
			ID:       string(e.ID),
			Name:     e.Name,
			Params:   e.Params,
			Source:   e.Source,
			Lamport:  e.Clock.Lamport,
			WallTime: e.Clock.WallTime.Format(time.RFC3339Nano),
		}
		if e.Clock.Vector != nil {
			ee.VectorClock = make(map[string]uint64, len(e.Clock.Vector))
			for k, v := range e.Clock.Vector {
				ee.VectorClock[string(k)] = v
			}
		}
		snap.Events = append(snap.Events, ee)

		if e.Clock.Lamport > snap.HighWater {
			snap.HighWater = e.Clock.Lamport
		}
	}

	// Collect edges (only between events in the snapshot).
	for _, fromID := range ids {
		for toID := range p.causalEdges[fromID] {
			if eventIDSet[toID] {
				snap.CausalEdges = append(snap.CausalEdges, []string{
					string(fromID), string(toID),
				})
			}
		}
	}

	return snap
}

// PendingEdgeCount returns the number of buffered pending edges.
func (p *Poset) PendingEdgeCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.pendingEdges)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -run "TestMerge|TestCreate|TestDrain" -v`
Expected: PASS

- [ ] **Step 6: Run full suite**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add poset.go merge.go merge_test.go
git commit -m "feat: add Poset merge algorithm with snapshot creation and pending edge buffer"
```

---

## Task 3: JSON serialization update for vector clocks

**Files:**
- Modify: `export.go`
- Modify: `export_test.go` (append)

- [ ] **Step 1: Write failing test**

Append to `export_test.go`:

```go
func TestJSONRoundTripVectorClock(t *testing.T) {
	p := NewPoset()
	e := NewEvent("X", "src", nil)
	e.Clock.Vector = VectorClock{"node1": 3, "node2": 5}
	p.AddEvent(e)

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Verify vector_clock appears in JSON.
	if !strings.Contains(string(data), "vector_clock") {
		t.Error("JSON should contain vector_clock field")
	}

	p2 := NewPoset()
	if err := json.Unmarshal(data, p2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	events := p2.Events()
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	vc := events[0].Clock.Vector
	if vc == nil {
		t.Fatal("VectorClock should not be nil after round-trip")
	}
	if vc["node1"] != 3 || vc["node2"] != 5 {
		t.Errorf("VectorClock: want {node1:3, node2:5}, got %v", vc)
	}
}

func TestJSONBackwardCompatNoVectorClock(t *testing.T) {
	// Old JSON without vector_clock field should still parse.
	oldJSON := `{"events":[{"id":"test-id","name":"X","params":{},"source":"src","lamport":1,"wall_time":"2024-01-01T00:00:00Z"}],"causal_edges":[],"metadata":{}}`
	p := NewPoset()
	if err := json.Unmarshal([]byte(oldJSON), p); err != nil {
		t.Fatalf("Unmarshal old JSON: %v", err)
	}
	e := p.Events()
	if len(e) != 1 {
		t.Fatalf("want 1 event, got %d", len(e))
	}
	if e[0].Clock.Vector != nil {
		t.Error("VectorClock should be nil for old JSON without field")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -run "TestJSONRoundTripVectorClock|TestJSONBackwardCompat" -v`
Expected: FAIL — vector_clock not in JSON output / VectorClock not restored

- [ ] **Step 3: Update EventExport and MarshalJSON/UnmarshalJSON**

In `export.go`, add field to `EventExport` (after line 20, `WallTime`):

```go
	VectorClock map[string]uint64 `json:"vector_clock,omitempty"`
```

In `MarshalJSON`, update the event export loop (around line 47-54) to include vector clock:

```go
		ee := EventExport{
			ID:       string(e.ID),
			Name:     e.Name,
			Params:   e.Params,
			Source:   e.Source,
			Lamport:  e.Clock.Lamport,
			WallTime: e.Clock.WallTime.Format(time.RFC3339Nano),
		}
		if e.Clock.Vector != nil {
			ee.VectorClock = make(map[string]uint64, len(e.Clock.Vector))
			for k, v := range e.Clock.Vector {
				ee.VectorClock[string(k)] = v
			}
		}
		events = append(events, ee)
```

In `UnmarshalJSON`, update event restoration (around line 104-120) to restore vector clock:

```go
		e := &Event{
			ID:        EventID(ee.ID),
			Name:      ee.Name,
			Params:    ee.Params,
			Source:    ee.Source,
			Clock:     ClockStamp{Lamport: ee.Lamport, WallTime: wallTime},
			Immutable: true,
		}
		if ee.VectorClock != nil {
			vc := make(VectorClock, len(ee.VectorClock))
			for k, v := range ee.VectorClock {
				vc[NodeID(k)] = v
			}
			e.Clock.Vector = vc
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -run "TestJSON" -v`
Expected: All JSON tests PASS (new + existing)

- [ ] **Step 5: Run full suite**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add export.go export_test.go
git commit -m "feat: add VectorClock to JSON serialization with backward compatibility"
```

---

## Task 4: Transport interface and MemTransport

**Files:**
- Create: `dsync/transport.go`
- Create: `dsync/mem_transport.go`
- Create: `dsync/mem_transport_test.go`

- [ ] **Step 1: Write failing tests**

```go
// dsync/mem_transport_test.go
package dsync

import (
	"context"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

func TestMemTransportSendReceive(t *testing.T) {
	net := NewMemNetwork()
	t1 := net.Transport("node1")
	t2 := net.Transport("node2")

	snap := &gorapide.Snapshot{
		NodeID:    "node1",
		HighWater: 5,
	}

	err := t1.Send(context.Background(), "node2", snap)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case received := <-t2.Receive():
		if received.NodeID != "node1" {
			t.Errorf("NodeID: want node1, got %s", received.NodeID)
		}
		if received.HighWater != 5 {
			t.Errorf("HighWater: want 5, got %d", received.HighWater)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for snapshot")
	}
}

func TestMemTransportMultiplePeers(t *testing.T) {
	net := NewMemNetwork()
	t1 := net.Transport("node1")
	t2 := net.Transport("node2")
	t3 := net.Transport("node3")

	snap := &gorapide.Snapshot{NodeID: "node1", HighWater: 1}
	t1.Send(context.Background(), "node2", snap)
	t1.Send(context.Background(), "node3", snap)

	select {
	case <-t2.Receive():
	case <-time.After(time.Second):
		t.Fatal("node2 should receive")
	}
	select {
	case <-t3.Receive():
	case <-time.After(time.Second):
		t.Fatal("node3 should receive")
	}
}

func TestMemTransportClose(t *testing.T) {
	net := NewMemNetwork()
	t1 := net.Transport("node1")
	t1.Close()

	// Receive channel should be closed.
	_, ok := <-t1.Receive()
	if ok {
		t.Error("Receive should return closed channel after Close")
	}
}

func TestMemTransportSendToUnknown(t *testing.T) {
	net := NewMemNetwork()
	t1 := net.Transport("node1")

	err := t1.Send(context.Background(), "unknown", &gorapide.Snapshot{})
	if err == nil {
		t.Error("Send to unknown peer should fail")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./dsync/ -run "TestMemTransport" -v`
Expected: FAIL — package dsync doesn't exist

- [ ] **Step 3: Write Transport interface**

```go
// dsync/transport.go
package dsync

import (
	"context"

	"github.com/ShaneDolphin/gorapide"
)

// Transport abstracts the network layer for poset synchronization.
type Transport interface {
	Send(ctx context.Context, target gorapide.NodeID, snap *gorapide.Snapshot) error
	Receive() <-chan *gorapide.Snapshot
	Close() error
}
```

- [ ] **Step 4: Write MemTransport**

```go
// dsync/mem_transport.go
package dsync

import (
	"context"
	"fmt"
	"sync"

	"github.com/ShaneDolphin/gorapide"
)

// MemNetwork is an in-memory network that connects MemTransport instances.
type MemNetwork struct {
	transports map[gorapide.NodeID]*MemTransport
	mu         sync.RWMutex
}

// NewMemNetwork creates a new in-memory network.
func NewMemNetwork() *MemNetwork {
	return &MemNetwork{
		transports: make(map[gorapide.NodeID]*MemTransport),
	}
}

// Transport creates or retrieves a MemTransport for the given node.
func (n *MemNetwork) Transport(nodeID gorapide.NodeID) *MemTransport {
	n.mu.Lock()
	defer n.mu.Unlock()
	if t, ok := n.transports[nodeID]; ok {
		return t
	}
	t := &MemTransport{
		nodeID:  nodeID,
		network: n,
		inbox:   make(chan *gorapide.Snapshot, 256),
	}
	n.transports[nodeID] = t
	return t
}

// MemTransport implements Transport using in-memory channels.
type MemTransport struct {
	nodeID  gorapide.NodeID
	network *MemNetwork
	inbox   chan *gorapide.Snapshot
	closed  bool
	mu      sync.Mutex
}

// Send transmits a snapshot to the target node.
func (t *MemTransport) Send(ctx context.Context, target gorapide.NodeID, snap *gorapide.Snapshot) error {
	t.network.mu.RLock()
	peer, ok := t.network.transports[target]
	t.network.mu.RUnlock()
	if !ok {
		return fmt.Errorf("dsync: peer %q not found", target)
	}
	select {
	case peer.inbox <- snap:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Receive returns the channel for incoming snapshots.
func (t *MemTransport) Receive() <-chan *gorapide.Snapshot {
	return t.inbox
}

// Close shuts down the transport.
func (t *MemTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		close(t.inbox)
	}
	return nil
}
```

- [ ] **Step 5: Run tests**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./dsync/ -run "TestMemTransport" -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add dsync/
git commit -m "feat(dsync): add Transport interface and MemTransport for testing"
```

---

## Task 5: Coordinator

**Files:**
- Create: `dsync/coordinator.go`
- Create: `dsync/coordinator_test.go`

- [ ] **Step 1: Write failing tests**

```go
// dsync/coordinator_test.go
package dsync

import (
	"context"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

func TestCoordinatorTwoNodeSync(t *testing.T) {
	net := NewMemNetwork()

	// Node 1 with event A.
	poset1 := gorapide.NewPoset()
	a := gorapide.NewEvent("A", "node1", nil)
	poset1.AddEvent(a)

	// Node 2 with event B.
	poset2 := gorapide.NewPoset()
	b := gorapide.NewEvent("B", "node2", nil)
	poset2.AddEvent(b)

	c1 := NewCoordinator("node1", poset1, net.Transport("node1"), WithInterval(50*time.Millisecond))
	c1.AddPeer("node2")

	c2 := NewCoordinator("node2", poset2, net.Transport("node2"), WithInterval(50*time.Millisecond))
	c2.AddPeer("node1")

	ctx := context.Background()
	c1.Start(ctx)
	c2.Start(ctx)

	// Wait for sync.
	time.Sleep(300 * time.Millisecond)

	c1.Stop()
	c2.Stop()
	c1.Wait()
	c2.Wait()

	// Both posets should have both events.
	if poset1.Len() != 2 {
		t.Errorf("poset1: want 2 events, got %d", poset1.Len())
	}
	if poset2.Len() != 2 {
		t.Errorf("poset2: want 2 events, got %d", poset2.Len())
	}
}

func TestCoordinatorThreeNodeConvergence(t *testing.T) {
	net := NewMemNetwork()

	poset1 := gorapide.NewPoset()
	poset1.AddEvent(gorapide.NewEvent("A", "node1", nil))

	poset2 := gorapide.NewPoset()
	poset2.AddEvent(gorapide.NewEvent("B", "node2", nil))

	poset3 := gorapide.NewPoset()
	poset3.AddEvent(gorapide.NewEvent("C", "node3", nil))

	c1 := NewCoordinator("node1", poset1, net.Transport("node1"), WithInterval(50*time.Millisecond))
	c1.AddPeer("node2")
	c1.AddPeer("node3")

	c2 := NewCoordinator("node2", poset2, net.Transport("node2"), WithInterval(50*time.Millisecond))
	c2.AddPeer("node1")
	c2.AddPeer("node3")

	c3 := NewCoordinator("node3", poset3, net.Transport("node3"), WithInterval(50*time.Millisecond))
	c3.AddPeer("node1")
	c3.AddPeer("node2")

	ctx := context.Background()
	c1.Start(ctx)
	c2.Start(ctx)
	c3.Start(ctx)

	time.Sleep(500 * time.Millisecond)

	c1.Stop()
	c2.Stop()
	c3.Stop()
	c1.Wait()
	c2.Wait()
	c3.Wait()

	// All three should have all three events.
	if poset1.Len() != 3 {
		t.Errorf("poset1: want 3, got %d", poset1.Len())
	}
	if poset2.Len() != 3 {
		t.Errorf("poset2: want 3, got %d", poset2.Len())
	}
	if poset3.Len() != 3 {
		t.Errorf("poset3: want 3, got %d", poset3.Len())
	}
}

func TestCoordinatorPushPull(t *testing.T) {
	net := NewMemNetwork()

	poset1 := gorapide.NewPoset()
	a := gorapide.NewEvent("A", "node1", nil)
	poset1.AddEvent(a)

	poset2 := gorapide.NewPoset()

	c1 := NewCoordinator("node1", poset1, net.Transport("node1"), WithInterval(50*time.Millisecond))
	c1.AddPeer("node2")

	c2 := NewCoordinator("node2", poset2, net.Transport("node2"), WithInterval(50*time.Millisecond))
	c2.AddPeer("node1")

	ctx := context.Background()
	c1.Start(ctx)
	c2.Start(ctx)

	time.Sleep(200 * time.Millisecond)

	c1.Stop()
	c2.Stop()
	c1.Wait()
	c2.Wait()

	if poset2.Len() != 1 {
		t.Errorf("poset2 should have received A, got len=%d", poset2.Len())
	}
}

func TestCoordinatorStopIdempotent(t *testing.T) {
	net := NewMemNetwork()
	poset := gorapide.NewPoset()
	c := NewCoordinator("node1", poset, net.Transport("node1"))
	c.Start(context.Background())
	c.Stop()
	c.Stop() // Should not panic.
	c.Wait()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./dsync/ -run "TestCoordinator" -v`
Expected: FAIL — `NewCoordinator` undefined

- [ ] **Step 3: Write Coordinator**

```go
// dsync/coordinator.go
package dsync

import (
	"context"
	"sync"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

// CoordOption configures a Coordinator.
type CoordOption func(*Coordinator)

// WithInterval sets the sync interval.
func WithInterval(d time.Duration) CoordOption {
	return func(c *Coordinator) {
		c.interval = d
	}
}

// Coordinator manages periodic poset synchronization with peer nodes.
type Coordinator struct {
	nodeID    gorapide.NodeID
	poset     *gorapide.Poset
	transport Transport
	peers     []gorapide.NodeID
	interval  time.Duration
	highWater map[gorapide.NodeID]uint64 // last sent high water per peer

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewCoordinator creates a sync coordinator for the given poset and transport.
func NewCoordinator(nodeID gorapide.NodeID, poset *gorapide.Poset, transport Transport, opts ...CoordOption) *Coordinator {
	c := &Coordinator{
		nodeID:    nodeID,
		poset:     poset,
		transport: transport,
		interval:  5 * time.Second,
		highWater: make(map[gorapide.NodeID]uint64),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// AddPeer registers a peer node for synchronization.
func (c *Coordinator) AddPeer(id gorapide.NodeID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.peers = append(c.peers, id)
}

// RemovePeer unregisters a peer node.
func (c *Coordinator) RemovePeer(id gorapide.NodeID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, p := range c.peers {
		if p == id {
			c.peers = append(c.peers[:i], c.peers[i+1:]...)
			break
		}
	}
}

// Start begins background synchronization.
func (c *Coordinator) Start(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done != nil {
		return
	}
	c.done = make(chan struct{})
	var coordCtx context.Context
	coordCtx, c.cancel = context.WithCancel(ctx)
	go c.runPush(coordCtx)
	go c.runReceive(coordCtx)
}

// Stop signals the coordinator to shut down.
func (c *Coordinator) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

// Wait blocks until the coordinator has stopped.
func (c *Coordinator) Wait() {
	c.mu.Lock()
	d := c.done
	c.mu.Unlock()
	if d != nil {
		<-d
	}
}

// runPush periodically sends snapshots to all peers.
func (c *Coordinator) runPush(ctx context.Context) {
	defer close(c.done)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.pushToAll(ctx)
		}
	}
}

func (c *Coordinator) pushToAll(ctx context.Context) {
	c.mu.Lock()
	peers := make([]gorapide.NodeID, len(c.peers))
	copy(peers, c.peers)
	c.mu.Unlock()

	for _, peer := range peers {
		snap := c.poset.CreateSnapshot(c.nodeID)
		c.transport.Send(ctx, peer, snap)
	}
}

// runReceive processes incoming snapshots from peers.
func (c *Coordinator) runReceive(ctx context.Context) {
	ch := c.transport.Receive()
	for {
		select {
		case <-ctx.Done():
			return
		case snap, ok := <-ch:
			if !ok {
				return
			}
			c.poset.MergeSnapshot(snap)
			c.poset.DrainPendingEdges()
		}
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./dsync/ -run "TestCoordinator" -v -race`
Expected: PASS

- [ ] **Step 5: Run full suite**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add dsync/coordinator.go dsync/coordinator_test.go
git commit -m "feat(dsync): add Coordinator for periodic push/pull poset synchronization"
```

---

## Task 6: Final verification

- [ ] **Step 1: Run full test suite fresh**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race -count=1 ./...`
Expected: All PASS

- [ ] **Step 2: Run otelexport tests**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/otelexport && go test -race -count=1 ./...`
Expected: All PASS

- [ ] **Step 3: Run go vet**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go vet ./...`
Expected: No issues

---

## Verification

1. `go test -race ./... -run TestVectorClock` — VectorClock operations
2. `go test -race ./... -run TestMerge` — merge algorithm (disjoint, overlapping, idempotent, Lamport reconciliation, pending edges)
3. `go test -race ./... -run TestJSON` — vector clock JSON round-trip + backward compat
4. `go test -race ./dsync/` — transport + coordinator + two/three-node convergence
5. `go test -race ./...` — no regressions across all packages
