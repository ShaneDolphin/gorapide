package gorapide

import (
	"sort"
	"time"
)

// Snapshot is a serializable representation of a subset of a Poset,
// used for shipping events between nodes.
type Snapshot struct {
	NodeID      NodeID        `json:"node_id"`
	Events      []EventExport `json:"events"`
	CausalEdges [][]string    `json:"causal_edges"`
	HighWater   uint64        `json:"high_water"`
}

// MergeResult summarizes the outcome of merging a Snapshot into a Poset.
type MergeResult struct {
	EventsAdded   int
	EventsSkipped int
	EdgesAdded    int
	EdgesSkipped  int
	EdgesPending  int
}

// PendingEdge represents a causal edge whose endpoints may not yet be present
// in the local poset.
type PendingEdge struct {
	From EventID
	To   EventID
}

// MergeSnapshot integrates a remote Snapshot into the local Poset.
// Events are sorted by Lamport before insertion so that causal ordering
// is preserved. Duplicate events are skipped. Edges whose endpoints are
// missing are buffered as pending edges for later resolution.
func (p *Poset) MergeSnapshot(snap *Snapshot) (*MergeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := &MergeResult{}

	// Sort events by Lamport to merge in causal order.
	sorted := make([]EventExport, len(snap.Events))
	copy(sorted, snap.Events)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Lamport < sorted[j].Lamport
	})

	// Merge events.
	for _, ee := range sorted {
		id := EventID(ee.ID)
		if _, exists := p.events[id]; exists {
			result.EventsSkipped++
			continue
		}

		wallTime, err := time.Parse(time.RFC3339Nano, ee.WallTime)
		if err != nil {
			wallTime = time.Now()
		}

		e := &Event{
			ID:     id,
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
		if ee.VectorClock != nil {
			e.Clock.Vector = make(VectorClock, len(ee.VectorClock))
			for k, v := range ee.VectorClock {
				e.Clock.Vector[NodeID(k)] = v
			}
		}

		if err := p.mergeEventLocked(e); err != nil {
			result.EventsSkipped++
			continue
		}
		result.EventsAdded++
	}

	// Merge edges.
	for _, edge := range snap.CausalEdges {
		if len(edge) != 2 {
			continue
		}
		from := EventID(edge[0])
		to := EventID(edge[1])

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

	// Reconcile lamport counter from HighWater.
	if snap.HighWater > p.lamportCounter {
		p.lamportCounter = snap.HighWater
	}

	return result, nil
}

// CreateSnapshot builds a full Snapshot of the current Poset state
// containing all events and edges.
func (p *Poset) CreateSnapshot(nodeID NodeID) *Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()

	snap := &Snapshot{
		NodeID:  nodeID,
		Events:  make([]EventExport, 0, len(p.events)),
		HighWater: p.lamportCounter,
	}

	// Sort events by Lamport for deterministic output.
	ids := make([]EventID, 0, len(p.events))
	for id := range p.events {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return p.events[ids[i]].Clock.Lamport < p.events[ids[j]].Clock.Lamport
	})

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
	}

	// Collect edges in deterministic order.
	for _, fromID := range ids {
		succs := sortedSuccessors(p.causalEdges[fromID], p.events)
		for _, toID := range succs {
			snap.CausalEdges = append(snap.CausalEdges, []string{string(fromID), string(toID)})
		}
	}

	return snap
}

// CreateIncrementalSnapshot builds a Snapshot containing only events with
// Lamport timestamps >= sinceHighWater, along with edges between those events.
func (p *Poset) CreateIncrementalSnapshot(nodeID NodeID, sinceHighWater uint64) *Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()

	snap := &Snapshot{
		NodeID:    nodeID,
		Events:    make([]EventExport, 0),
		HighWater: p.lamportCounter,
	}

	// Collect events with Lamport >= sinceHighWater.
	included := make(map[EventID]bool)
	ids := make([]EventID, 0)
	for id, e := range p.events {
		if e.Clock.Lamport >= sinceHighWater {
			included[id] = true
			ids = append(ids, id)
		}
	}

	sort.Slice(ids, func(i, j int) bool {
		return p.events[ids[i]].Clock.Lamport < p.events[ids[j]].Clock.Lamport
	})

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
	}

	// Only include edges where both endpoints are in the snapshot.
	for _, fromID := range ids {
		succs := sortedSuccessors(p.causalEdges[fromID], p.events)
		for _, toID := range succs {
			if included[toID] {
				snap.CausalEdges = append(snap.CausalEdges, []string{string(fromID), string(toID)})
			}
		}
	}

	return snap
}
