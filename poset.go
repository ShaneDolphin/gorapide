package gorapide

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrEventExists    = errors.New("event already exists in poset")
	ErrEventNotFound  = errors.New("event not found in poset")
	ErrCyclicCausal   = errors.New("adding this edge would create a causal cycle")
	ErrNoPath         = errors.New("no causal path exists between events")
	ErrSelfCausal     = errors.New("an event cannot causally precede itself")
)

// Poset is a Partially Ordered Event Set that stores events and their
// causal and temporal ordering relationships. It is safe for concurrent use.
type Poset struct {
	events        map[EventID]*Event
	causalEdges   map[EventID]map[EventID]bool // from -> {to: true}
	reverseCausal map[EventID]map[EventID]bool // to -> {from: true}
	mu             sync.RWMutex
	lamportCounter uint64
	pendingEdges   []PendingEdge
}

// NewPoset creates an empty Poset.
func NewPoset() *Poset {
	return &Poset{
		events:        make(map[EventID]*Event),
		causalEdges:   make(map[EventID]map[EventID]bool),
		reverseCausal: make(map[EventID]map[EventID]bool),
	}
}

// AddEvent adds an event to the poset, freezes it, and assigns a Lamport
// timestamp. Returns an error if an event with the same ID already exists.
func (p *Poset) AddEvent(e *Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.addEventLocked(e)
}

func (p *Poset) addEventLocked(e *Event) error {
	if _, exists := p.events[e.ID]; exists {
		return fmt.Errorf("%w: %s", ErrEventExists, e.ID)
	}
	p.lamportCounter++
	e.Clock.Lamport = p.lamportCounter
	e.Freeze()
	p.events[e.ID] = e
	p.causalEdges[e.ID] = make(map[EventID]bool)
	p.reverseCausal[e.ID] = make(map[EventID]bool)
	return nil
}

func (p *Poset) mergeEventLocked(e *Event) error {
	if _, exists := p.events[e.ID]; exists {
		return fmt.Errorf("%w: %s", ErrEventExists, e.ID)
	}
	e.Freeze()
	p.events[e.ID] = e
	p.causalEdges[e.ID] = make(map[EventID]bool)
	p.reverseCausal[e.ID] = make(map[EventID]bool)
	if e.Clock.Lamport > p.lamportCounter {
		p.lamportCounter = e.Clock.Lamport
	}
	return nil
}

// DrainPendingEdges attempts to resolve all buffered pending edges whose
// endpoints are now present in the poset. Returns the count of resolved
// edges and any errors encountered during resolution.
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

// PendingEdgeCount returns the number of buffered pending edges.
func (p *Poset) PendingEdgeCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.pendingEdges)
}

// AddCausal establishes that event 'from' causally precedes event 'to'.
// It validates both events exist, rejects self-edges and cycles, and updates
// the 'to' event's Lamport timestamp to max(to.Lamport, from.Lamport+1).
func (p *Poset) AddCausal(from, to EventID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.addCausalLocked(from, to)
}

func (p *Poset) addCausalLocked(from, to EventID) error {
	if from == to {
		return fmt.Errorf("%w: %s", ErrSelfCausal, from)
	}
	fromEvent, ok := p.events[from]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEventNotFound, from)
	}
	toEvent, ok := p.events[to]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEventNotFound, to)
	}
	// Already exists — idempotent.
	if p.causalEdges[from][to] {
		return nil
	}
	// Cycle detection: if 'to' can already reach 'from', adding from->to creates a cycle.
	if p.canReachLocked(to, from) {
		return fmt.Errorf("%w: %s -> %s", ErrCyclicCausal, from, to)
	}
	p.causalEdges[from][to] = true
	p.reverseCausal[to][from] = true
	// Update Lamport: to must be strictly after from.
	if newLamport := fromEvent.Clock.Lamport + 1; newLamport > toEvent.Clock.Lamport {
		toEvent.Clock.Lamport = newLamport
		// Propagate Lamport updates to all descendants.
		p.propagateLamportLocked(to)
	}
	return nil
}

// propagateLamportLocked ensures all descendants of id have Lamport timestamps
// consistent with their causal predecessors. Uses BFS.
func (p *Poset) propagateLamportLocked(id EventID) {
	queue := []EventID{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		curEvent := p.events[cur]
		for succ := range p.causalEdges[cur] {
			succEvent := p.events[succ]
			if newLamport := curEvent.Clock.Lamport + 1; newLamport > succEvent.Clock.Lamport {
				succEvent.Clock.Lamport = newLamport
				queue = append(queue, succ)
			}
		}
	}
}

// AddEventWithCause adds an event and establishes causal edges from all
// specified causes to the new event. This is the primary way events
// are added during execution.
func (p *Poset) AddEventWithCause(e *Event, causes ...EventID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Validate all causes exist before adding the event.
	for _, cid := range causes {
		if _, ok := p.events[cid]; !ok {
			return fmt.Errorf("%w: cause %s", ErrEventNotFound, cid)
		}
	}
	if err := p.addEventLocked(e); err != nil {
		return err
	}
	for _, cid := range causes {
		if err := p.addCausalLocked(cid, e.ID); err != nil {
			return err
		}
	}
	return nil
}

// Event looks up an event by ID.
func (p *Poset) Event(id EventID) (*Event, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	e, ok := p.events[id]
	return e, ok
}

// Events returns a snapshot of all events in the poset.
func (p *Poset) Events() EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	set := make(EventSet, 0, len(p.events))
	for _, e := range p.events {
		set = append(set, e)
	}
	return set
}

// EventsByName returns all events with the given name.
func (p *Poset) EventsByName(name string) EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	for _, e := range p.events {
		if e.Name == name {
			set = append(set, e)
		}
	}
	return set
}

// Len returns the number of events in the poset.
func (p *Poset) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.events)
}

// IsCausallyBefore reports whether event a causally precedes event b (transitive).
func (p *Poset) IsCausallyBefore(a, b EventID) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if a == b {
		return false // irreflexive
	}
	return p.canReachLocked(a, b)
}

// canReachLocked performs BFS from 'start' following causal edges to see if
// 'target' is reachable. Caller must hold at least a read lock.
func (p *Poset) canReachLocked(start, target EventID) bool {
	visited := make(map[EventID]bool)
	queue := []EventID{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == target {
			return true
		}
		if visited[cur] {
			continue
		}
		visited[cur] = true
		for succ := range p.causalEdges[cur] {
			if !visited[succ] {
				queue = append(queue, succ)
			}
		}
	}
	return false
}

// IsCausallyIndependent reports whether neither a <c b nor b <c a.
func (p *Poset) IsCausallyIndependent(a, b EventID) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if a == b {
		return false
	}
	return !p.canReachLocked(a, b) && !p.canReachLocked(b, a)
}

// DirectCauses returns the immediate causal predecessors of the event (one hop back).
func (p *Poset) DirectCauses(id EventID) EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	for pred := range p.reverseCausal[id] {
		if e, ok := p.events[pred]; ok {
			set = append(set, e)
		}
	}
	return set
}

// DirectEffects returns the immediate causal successors of the event (one hop forward).
func (p *Poset) DirectEffects(id EventID) EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	for succ := range p.causalEdges[id] {
		if e, ok := p.events[succ]; ok {
			set = append(set, e)
		}
	}
	return set
}

// CausalAncestors returns all transitive causal predecessors of the event.
func (p *Poset) CausalAncestors(id EventID) EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	visited := make(map[EventID]bool)
	queue := make([]EventID, 0)
	for pred := range p.reverseCausal[id] {
		queue = append(queue, pred)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		set = append(set, p.events[cur])
		for pred := range p.reverseCausal[cur] {
			if !visited[pred] {
				queue = append(queue, pred)
			}
		}
	}
	return set
}

// CausalDescendants returns all transitive causal successors of the event.
func (p *Poset) CausalDescendants(id EventID) EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	visited := make(map[EventID]bool)
	queue := make([]EventID, 0)
	for succ := range p.causalEdges[id] {
		queue = append(queue, succ)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		set = append(set, p.events[cur])
		for succ := range p.causalEdges[cur] {
			if !visited[succ] {
				queue = append(queue, succ)
			}
		}
	}
	return set
}

// CausalChain returns all events on any causal path from 'from' to 'to',
// including 'from' and 'to' themselves. Returns an error if no causal path exists.
func (p *Poset) CausalChain(from, to EventID) (EventSet, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, ok := p.events[from]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrEventNotFound, from)
	}
	if _, ok := p.events[to]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrEventNotFound, to)
	}
	if !p.canReachLocked(from, to) {
		return nil, fmt.Errorf("%w: %s to %s", ErrNoPath, from, to)
	}
	// Find all nodes on any path from 'from' to 'to'.
	// A node N is on some path if from can reach N and N can reach to.
	// First collect all nodes reachable from 'from'.
	forwardReachable := make(map[EventID]bool)
	p.collectReachableLocked(from, forwardReachable, true)
	forwardReachable[from] = true
	// Then collect all nodes that can reach 'to' (reverse traversal).
	backwardReachable := make(map[EventID]bool)
	p.collectReachableLocked(to, backwardReachable, false)
	backwardReachable[to] = true
	// Intersection is the set of events on any causal path.
	var chain EventSet
	for id := range forwardReachable {
		if backwardReachable[id] {
			chain = append(chain, p.events[id])
		}
	}
	return chain, nil
}

// collectReachableLocked does BFS from start, following edges forward or backward.
func (p *Poset) collectReachableLocked(start EventID, visited map[EventID]bool, forward bool) {
	queue := []EventID{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		var neighbors map[EventID]bool
		if forward {
			neighbors = p.causalEdges[cur]
		} else {
			neighbors = p.reverseCausal[cur]
		}
		for next := range neighbors {
			if !visited[next] {
				queue = append(queue, next)
			}
		}
	}
}

// Roots returns events with no causal predecessors.
func (p *Poset) Roots() EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	for id, e := range p.events {
		if len(p.reverseCausal[id]) == 0 {
			set = append(set, e)
		}
	}
	return set
}

// Leaves returns events with no causal successors.
func (p *Poset) Leaves() EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	for id, e := range p.events {
		if len(p.causalEdges[id]) == 0 {
			set = append(set, e)
		}
	}
	return set
}

// TopologicalSort returns events in a valid causal order where every event
// appears after all of its causal predecessors. Uses Kahn's algorithm.
func (p *Poset) TopologicalSort() []*Event {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Compute in-degrees.
	inDegree := make(map[EventID]int, len(p.events))
	for id := range p.events {
		inDegree[id] = len(p.reverseCausal[id])
	}

	// Seed queue with roots (in-degree 0).
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
