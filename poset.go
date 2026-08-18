package gorapide

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrEventExists               = errors.New("event already exists in poset")
	ErrEventNotFound             = errors.New("event not found in poset")
	ErrCyclicCausal              = errors.New("adding this edge would create a causal cycle")
	ErrNoPath                    = errors.New("no causal path exists between events")
	ErrSelfCausal                = errors.New("an event cannot causally precede itself")
	ErrCauseMismatch             = errors.New("event causes do not match deterministic provenance")
	ErrCausalEquivalenceConflict = errors.New("causal equivalence conflicts with strict causal order")
)

// Poset stores Rapide event occurrences and their causal and temporal ordering
// relationships. Causality is a preorder; causal-equivalence classes form the
// nodes of its quotient partial order. Poset is safe for concurrent use.
type Poset struct {
	events        map[EventID]*Event
	causalEdges   map[EventID]map[EventID]bool // from -> {to: true}
	reverseCausal map[EventID]map[EventID]bool // to -> {from: true}
	// causalClass maps every event to the lexicographically least member of
	// its causal-equivalence class. The quotient of these classes by
	// causalEdges is a strict DAG; together they represent Rapide's published
	// causal preorder without storing symmetric graph cycles.
	causalClass map[EventID]EventID
	// timedEvents counts stored occurrences carrying at least one Rapide
	// interval. Timing closure is defined on a clock two occurrences share, so
	// while this is zero no pair of stored occurrences can conflict and the
	// closure scan is provably vacuous. Every write to events goes through
	// storeEventLocked, which keeps this exact.
	timedEvents    int
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
		causalClass:   make(map[EventID]EventID),
	}
}

// AddEvent adds an event to the poset, freezes it, and assigns a Lamport
// timestamp. Returns an error if an event with the same ID already exists.
func (p *Poset) AddEvent(e *Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e == nil {
		return fmt.Errorf("%w: event is nil", ErrEventNotFound)
	}
	if len(e.expectedCauses) > 0 {
		return fmt.Errorf("%w: event %s requires %v, got no causes", ErrCauseMismatch, e.ID, e.expectedCauses)
	}
	return p.addEventLocked(e)
}

func (p *Poset) addEventLocked(e *Event) error {
	if e == nil {
		return fmt.Errorf("%w: event is nil", ErrEventNotFound)
	}
	if _, exists := p.events[e.ID]; exists {
		return fmt.Errorf("%w: %s", ErrEventExists, e.ID)
	}
	timings, err := CanonicalizeEventTimings(e.Timings)
	if err != nil {
		return err
	}
	e.Timings = timings
	p.lamportCounter++
	e.Clock.Lamport = p.lamportCounter
	e.Freeze()
	stored := e
	if e.deterministic {
		stored = cloneEvent(e)
	}
	p.storeEventLocked(stored)
	p.causalEdges[e.ID] = make(map[EventID]bool)
	p.reverseCausal[e.ID] = make(map[EventID]bool)
	p.ensureCausalClassesLocked()
	p.causalClass[e.ID] = e.ID
	return nil
}

func (p *Poset) mergeEventLocked(e *Event) error {
	if e == nil {
		return fmt.Errorf("%w: event is nil", ErrEventNotFound)
	}
	if _, exists := p.events[e.ID]; exists {
		return fmt.Errorf("%w: %s", ErrEventExists, e.ID)
	}
	timings, err := CanonicalizeEventTimings(e.Timings)
	if err != nil {
		return err
	}
	e.Timings = timings
	e.Freeze()
	p.storeEventLocked(e)
	p.causalEdges[e.ID] = make(map[EventID]bool)
	p.reverseCausal[e.ID] = make(map[EventID]bool)
	p.ensureCausalClassesLocked()
	p.causalClass[e.ID] = e.ID
	if e.Clock.Lamport > p.lamportCounter {
		p.lamportCounter = e.Clock.Lamport
	}
	return nil
}

// storeEventLocked writes an occurrence into the event store and keeps the
// timed-occurrence count exact, including when a replacement gains Rapide
// intervals the previous stored copy did not carry. Every write to p.events
// goes through here so the timing-closure fast path can never read a stale
// count. The caller must hold the write lock and must pass the copy that is
// actually being stored, not the caller's original.
func (p *Poset) storeEventLocked(e *Event) {
	if previous, exists := p.events[e.ID]; exists && len(previous.Timings) != 0 {
		p.timedEvents--
	}
	if len(e.Timings) != 0 {
		p.timedEvents++
	}
	p.events[e.ID] = e
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
	_, ok := p.events[from]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEventNotFound, from)
	}
	_, ok = p.events[to]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEventNotFound, to)
	}
	p.ensureCausalClassesLocked()
	originalFrom, originalTo := from, to
	from = p.causalRepresentativeLocked(from)
	to = p.causalRepresentativeLocked(to)
	if from == to {
		return fmt.Errorf("%w: equivalent events %s and %s cannot be strictly ordered", ErrSelfCausal, originalFrom, originalTo)
	}
	// Already exists — idempotent.
	if p.causalEdges[from][to] {
		return nil
	}
	// Cycle detection: if 'to' can already reach 'from', adding from->to creates a cycle.
	if p.canReachLocked(to, from) {
		return fmt.Errorf("%w: %s -> %s", ErrCyclicCausal, from, to)
	}
	if err := p.validateTimingClosureLocked(from, to); err != nil {
		return err
	}
	p.causalEdges[from][to] = true
	p.reverseCausal[to][from] = true
	// Update every member of the target equivalence class.
	if newLamport := p.maximumClassLamportLocked(from) + 1; newLamport > p.maximumClassLamportLocked(to) {
		p.setClassLamportLocked(to, newLamport)
		// Propagate Lamport updates to all descendants.
		p.propagateLamportLocked(to)
	}
	return nil
}

// propagateLamportLocked ensures all descendants of id have Lamport timestamps
// consistent with their causal predecessors. Uses BFS.
func (p *Poset) propagateLamportLocked(id EventID) {
	id = p.causalRepresentativeLocked(id)
	queue := []EventID{id}
	queued := map[EventID]bool{id: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		delete(queued, cur)
		currentLamport := p.maximumClassLamportLocked(cur)
		for _, succ := range p.causalClassSuccessorsLocked(cur) {
			if newLamport := currentLamport + 1; newLamport > p.maximumClassLamportLocked(succ) {
				p.setClassLamportLocked(succ, newLamport)
				if !queued[succ] {
					queue = append(queue, succ)
					queued[succ] = true
				}
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
	if e == nil {
		return fmt.Errorf("%w: event is nil", ErrEventNotFound)
	}
	timings, err := CanonicalizeEventTimings(e.Timings)
	if err != nil {
		return err
	}
	e.Timings = timings
	if len(e.expectedCauses) > 0 {
		normalized := append([]EventID(nil), causes...)
		sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
		write := 0
		for _, cause := range normalized {
			if write > 0 && normalized[write-1] == cause {
				continue
			}
			normalized[write] = cause
			write++
		}
		normalized = normalized[:write]
		if !eventIDSlicesEqual(normalized, e.expectedCauses) {
			return fmt.Errorf("%w: event %s requires %v, got %v",
				ErrCauseMismatch, e.ID, e.expectedCauses, normalized)
		}
	}
	// Validate all causes exist before adding the event.
	for _, cid := range causes {
		if _, ok := p.events[cid]; !ok {
			return fmt.Errorf("%w: cause %s", ErrEventNotFound, cid)
		}
	}
	// A conflict is a shared clock on which the earlier occurrence finishes
	// after the later one starts, so an occurrence with no intervals of its own
	// shares no clock with anything and can conflict with no predecessor.
	// Collecting the causal past would then be pure cost.
	if len(e.Timings) != 0 {
		checked := make(map[EventID]bool)
		for _, cause := range causes {
			for _, predecessor := range p.causalPredecessorIDsLocked(cause) {
				if checked[predecessor] {
					continue
				}
				checked[predecessor] = true
				if err := sharedTimingConflict(p.events[predecessor], e); err != nil {
					return err
				}
			}
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

func (p *Poset) validateTimingClosureLocked(from, to EventID) error {
	// See the timedEvents field comment: with no stored occurrence carrying a
	// Rapide interval, no cross-product pair below can ever share a clock, so
	// collecting the causal past and future is pure cost. A local check at
	// 'from'/'to' is not enough here (an edge between two untimed events can
	// still newly order a timed ancestor before a timed descendant), so this
	// is the poset-wide count, not len(e.Timings).
	if p.timedEvents == 0 {
		return nil
	}
	left := p.causalPredecessorIDsLocked(from)
	right := p.causalSuccessorIDsLocked(to)
	for _, before := range left {
		for _, after := range right {
			if err := sharedTimingConflict(p.events[before], p.events[after]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Poset) causalPredecessorIDsLocked(id EventID) []EventID {
	classes := p.reachableCausalClassesLocked(id, false)
	result := make([]EventID, 0)
	for candidate := range p.events {
		if classes[p.causalRepresentativeLocked(candidate)] {
			result = append(result, candidate)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (p *Poset) causalSuccessorIDsLocked(id EventID) []EventID {
	classes := p.reachableCausalClassesLocked(id, true)
	result := make([]EventID, 0)
	for candidate := range p.events {
		if classes[p.causalRepresentativeLocked(candidate)] {
			result = append(result, candidate)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func eventIDSlicesEqual(a, b []EventID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Event looks up an event by ID.
func (p *Poset) Event(id EventID) (*Event, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	e, ok := p.events[id]
	return snapshotEvent(e), ok
}

// Events returns a snapshot of all events in the poset.
func (p *Poset) Events() EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	set := make(EventSet, 0, len(p.events))
	for _, e := range p.events {
		set = append(set, snapshotEvent(e))
	}
	sortEventSet(set)
	return set
}

// EventsByName returns all events with the given name. Matching is per
// observation: an occurrence with multiple qualified roles named name
// appears once per matching role, as a view carrying that observation's
// Name/Source/Params but the occurrence's shared ID. The name test itself
// is non-allocating (see (*Event).observationsNamed); only actual matches
// pay for a deep-cloned view via eventView, and even then only when one is
// needed (see the aliasing note below). Final ordering is sorted below, so
// the scan order over p.events (map iteration) is not semantic.
//
// A legacy (non-deterministic) event matched via its own primary
// Name/Source role — including the synthesized fallback for an event with
// no explicit Observations — is returned as the shared, frozen stored
// pointer, exactly as Event() and Events() already return legacy events
// (via snapshotEvent). This is an aliasing norm the rest of the library's
// query surface already documents and relies on for legacy events:
// query results are race-free but frozen at query time, and callers must
// not mutate them. Every other match — a secondary/added observation role
// on any event, or any match at all on a deterministic event (which stays
// deep-cloned everywhere, matching Event()/Events()' own treatment of
// deterministic events) — still gets an isolated, defensively-copied view
// via eventView.
func (p *Poset) EventsByName(name string) EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	for _, e := range p.events {
		for _, observation := range e.observationsNamed(name) {
			if !e.deterministic && e.isPrimaryObservation(observation) {
				set = append(set, e)
				continue
			}
			set = append(set, eventView(e, observation))
		}
	}
	sortEventSet(set)
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
	if _, exists := p.events[a]; !exists {
		return false
	}
	if _, exists := p.events[b]; !exists {
		return false
	}
	return p.canReachLocked(a, b)
}

// canReachLocked performs BFS from 'start' following causal edges to see if
// 'target' is reachable. Caller must hold at least a read lock.
func (p *Poset) canReachLocked(start, target EventID) bool {
	return p.canReachClassLocked(start, target)
}

// IsCausallyIndependent reports whether neither a <c b nor b <c a.
func (p *Poset) IsCausallyIndependent(a, b EventID) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if a == b {
		return false
	}
	if _, exists := p.events[a]; !exists {
		return false
	}
	if _, exists := p.events[b]; !exists {
		return false
	}
	if p.causalRepresentativeLocked(a) == p.causalRepresentativeLocked(b) {
		return false
	}
	return !p.canReachLocked(a, b) && !p.canReachLocked(b, a)
}

// DirectCauses returns the immediate causal predecessors of the event (one hop back).
func (p *Poset) DirectCauses(id EventID) EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	if _, exists := p.events[id]; !exists {
		return set
	}
	for _, predecessor := range p.causalClassPredecessorsLocked(p.causalRepresentativeLocked(id)) {
		for _, member := range p.causalClassMembersLocked(predecessor) {
			set = append(set, snapshotEvent(p.events[member]))
		}
	}
	sortEventSet(set)
	return set
}

// DirectEffects returns the immediate causal successors of the event (one hop forward).
func (p *Poset) DirectEffects(id EventID) EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	if _, exists := p.events[id]; !exists {
		return set
	}
	for _, successor := range p.causalClassSuccessorsLocked(p.causalRepresentativeLocked(id)) {
		for _, member := range p.causalClassMembersLocked(successor) {
			set = append(set, snapshotEvent(p.events[member]))
		}
	}
	sortEventSet(set)
	return set
}

// CausalAncestors returns all transitive causal predecessors of the event.
func (p *Poset) CausalAncestors(id EventID) EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	if _, exists := p.events[id]; !exists {
		return set
	}
	classes := p.reachableCausalClassesLocked(id, false)
	delete(classes, p.causalRepresentativeLocked(id))
	for candidate, event := range p.events {
		if classes[p.causalRepresentativeLocked(candidate)] {
			set = append(set, snapshotEvent(event))
		}
	}
	sortEventSet(set)
	return set
}

// CausalDescendants returns all transitive causal successors of the event.
func (p *Poset) CausalDescendants(id EventID) EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	if _, exists := p.events[id]; !exists {
		return set
	}
	classes := p.reachableCausalClassesLocked(id, true)
	delete(classes, p.causalRepresentativeLocked(id))
	for candidate, event := range p.events {
		if classes[p.causalRepresentativeLocked(candidate)] {
			set = append(set, snapshotEvent(event))
		}
	}
	sortEventSet(set)
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
	forwardReachable := p.reachableCausalClassesLocked(from, true)
	// Then collect all nodes that can reach 'to' (reverse traversal).
	backwardReachable := p.reachableCausalClassesLocked(to, false)
	// Intersection is the set of events on any causal path.
	var chain EventSet
	for id, event := range p.events {
		representative := p.causalRepresentativeLocked(id)
		if forwardReachable[representative] && backwardReachable[representative] {
			chain = append(chain, snapshotEvent(event))
		}
	}
	sortEventSet(chain)
	return chain, nil
}

// collectReachableLocked does BFS from start, following edges forward or backward.
func (p *Poset) collectReachableLocked(start EventID, visited map[EventID]bool, forward bool) {
	classes := p.reachableCausalClassesLocked(start, forward)
	for id := range p.events {
		if classes[p.causalRepresentativeLocked(id)] {
			visited[id] = true
		}
	}
}

// Roots returns events with no causal predecessors.
func (p *Poset) Roots() EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	for id, e := range p.events {
		if len(p.causalClassPredecessorsLocked(p.causalRepresentativeLocked(id))) == 0 {
			set = append(set, snapshotEvent(e))
		}
	}
	sortEventSet(set)
	return set
}

// Leaves returns events with no causal successors.
func (p *Poset) Leaves() EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var set EventSet
	for id, e := range p.events {
		if len(p.causalClassSuccessorsLocked(p.causalRepresentativeLocked(id))) == 0 {
			set = append(set, snapshotEvent(e))
		}
	}
	sortEventSet(set)
	return set
}

// TopologicalSort returns events in a valid causal order where every event
// appears after all of its causal predecessors. Uses Kahn's algorithm.
func (p *Poset) TopologicalSort() []*Event {
	p.mu.RLock()
	defer p.mu.RUnlock()
	representatives := p.causalClassRepresentativesLocked()
	inDegree := make(map[EventID]int, len(representatives))
	for _, representative := range representatives {
		inDegree[representative] = len(p.causalClassPredecessorsLocked(representative))
	}
	queue := make([]EventID, 0)
	for _, id := range representatives {
		deg := inDegree[id]
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i] < queue[j] })

	result := make([]*Event, 0, len(p.events))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, member := range p.causalClassMembersLocked(cur) {
			result = append(result, snapshotEvent(p.events[member]))
		}
		for _, succ := range p.causalClassSuccessorsLocked(cur) {
			inDegree[succ]--
			if inDegree[succ] == 0 {
				queue = append(queue, succ)
			}
		}
		sort.Slice(queue, func(i, j int) bool { return queue[i] < queue[j] })
	}
	return result
}

func sortEventSet(events EventSet) {
	sort.Slice(events, func(i, j int) bool {
		if events[i].ID != events[j].ID {
			return events[i].ID < events[j].ID
		}
		if events[i].Source != events[j].Source {
			return events[i].Source < events[j].Source
		}
		return events[i].Name < events[j].Name
	})
}
