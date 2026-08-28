package gorapide

import (
	"fmt"
	"sort"
)

// AddCausalEquivalent declares two event occurrences equivalent under
// Rapide's causal preorder. Equivalent occurrences remain distinct events but
// occupy one node in the quotient partial order.
func (p *Poset) AddCausalEquivalent(left, right EventID) error {
	return p.AddCausalEquivalenceClass(left, right)
}

// AddCausalEquivalenceClass merges the supplied occurrences into one causal-
// equivalence class. The operation is atomic: every event must exist and no
// pair of participating classes may already be strictly ordered.
func (p *Poset) AddCausalEquivalenceClass(ids ...EventID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.addCausalEquivalenceClassLocked(ids...)
}

func (p *Poset) addCausalEquivalenceClassLocked(ids ...EventID) error {
	if len(ids) == 0 {
		return fmt.Errorf("%w: equivalence class is empty", ErrEventNotFound)
	}
	p.ensureCausalClassesLocked()
	representatives := make(map[EventID]bool, len(ids))
	for _, id := range ids {
		if _, exists := p.events[id]; !exists {
			return fmt.Errorf("%w: %s", ErrEventNotFound, id)
		}
		representatives[p.causalRepresentativeLocked(id)] = true
	}
	ordered := make([]EventID, 0, len(representatives))
	for representative := range representatives {
		ordered = append(ordered, representative)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for leftIndex, left := range ordered {
		for _, right := range ordered[leftIndex+1:] {
			if p.canReachClassLocked(left, right) || p.canReachClassLocked(right, left) {
				return fmt.Errorf("%w: %s and %s", ErrCausalEquivalenceConflict, left, right)
			}
		}
	}
	if len(ordered) == 1 {
		return nil
	}
	if err := p.validateCausalEquivalenceTimingLocked(representatives); err != nil {
		return err
	}
	representative := ordered[0]
	maximumLamport := uint64(0)
	members := make([]EventID, 0, len(ordered))
	for id, event := range p.events {
		if representatives[p.causalRepresentativeLocked(id)] {
			p.causalClass[id] = representative
			members = append(members, id)
			if event.Clock.Lamport > maximumLamport {
				maximumLamport = event.Clock.Lamport
			}
		}
	}
	for id, event := range p.events {
		if p.causalRepresentativeLocked(id) == representative {
			event.Clock.Lamport = maximumLamport
		}
	}
	// members is exactly the merged class's complete new membership: every
	// id whose PRE-merge representative was one of the classes being
	// absorbed (representatives, built above from the supplied ids'
	// pre-merge representatives). Fold it into classMembers under the
	// surviving representative, and drop the index entries for every other
	// old representative — they no longer head any class.
	sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
	for _, old := range ordered {
		if old != representative {
			delete(p.classMembers, old)
		}
	}
	p.classMembers[representative] = members
	p.normalizeCausalEdgesLocked()
	p.propagateLamportLocked(representative)
	return nil
}

// validateCausalEquivalenceTimingLocked checks the strict relations that class
// substitution would add before mutating the preorder. Causal equivalence is
// distinct from temporal equivalence, so members are not compared with one
// another; only newly shared predecessors, successors, and their transitive
// cross-product must obey the existing strict-causality timing rule.
func (p *Poset) validateCausalEquivalenceTimingLocked(participating map[EventID]bool) error {
	merged := make(map[EventID]bool)
	predecessors := make(map[EventID]bool)
	successors := make(map[EventID]bool)
	for id := range p.events {
		representative := p.causalRepresentativeLocked(id)
		if participating[representative] {
			merged[id] = true
		}
	}
	for representative := range participating {
		for predecessor := range p.reachableCausalClassesLocked(representative, false) {
			if participating[predecessor] {
				continue
			}
			for _, member := range p.causalClassMembersLocked(predecessor) {
				predecessors[member] = true
			}
		}
		for successor := range p.reachableCausalClassesLocked(representative, true) {
			if participating[successor] {
				continue
			}
			for _, member := range p.causalClassMembersLocked(successor) {
				successors[member] = true
			}
		}
	}
	orderedPredecessors := sortedCausalEquivalenceIDs(predecessors)
	orderedMerged := sortedCausalEquivalenceIDs(merged)
	orderedSuccessors := sortedCausalEquivalenceIDs(successors)
	for _, predecessor := range orderedPredecessors {
		for _, member := range orderedMerged {
			if err := sharedTimingConflict(p.events[predecessor], p.events[member]); err != nil {
				return err
			}
		}
		for _, successor := range orderedSuccessors {
			if err := sharedTimingConflict(p.events[predecessor], p.events[successor]); err != nil {
				return err
			}
		}
	}
	for _, member := range orderedMerged {
		for _, successor := range orderedSuccessors {
			if err := sharedTimingConflict(p.events[member], p.events[successor]); err != nil {
				return err
			}
		}
	}
	return nil
}

func sortedCausalEquivalenceIDs(ids map[EventID]bool) []EventID {
	result := make([]EventID, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// IsCausallyEquivalent reports whether two distinct or identical occurrences
// belong to the same causal-equivalence class. Missing events are never
// equivalent.
func (p *Poset) IsCausallyEquivalent(left, right EventID) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, exists := p.events[left]; !exists {
		return false
	}
	if _, exists := p.events[right]; !exists {
		return false
	}
	return p.causalRepresentativeLocked(left) == p.causalRepresentativeLocked(right)
}

// CausalEquivalenceClass returns every occurrence causally equivalent to id in
// canonical EventID order. An unknown id returns an empty set.
func (p *Poset) CausalEquivalenceClass(id EventID) EventSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, exists := p.events[id]; !exists {
		return EventSet{}
	}
	result := make(EventSet, 0)
	for _, member := range p.causalClassMembersLocked(id) {
		result = append(result, snapshotEvent(p.events[member]))
	}
	return result
}

func (p *Poset) ensureCausalClassesLocked() {
	if p.causalClass == nil {
		p.causalClass = make(map[EventID]EventID, len(p.events))
	}
	if p.classMembers == nil {
		p.classMembers = make(map[EventID][]EventID, len(p.events))
	}
	// Defensive only: every current entry point that adds a new event id
	// (addEventLocked, mergeEventLocked, UnmarshalJSON's event-restore loop)
	// already calls registerTrivialClassLocked itself, so in normal
	// operation this loop finds nothing left to do. It stays as a fallback
	// — keeping causalClass and classMembers consistent with each other
	// even here — rather than being removed, since it is not the cost this
	// round targets (this function's own full p.events scan is a separate,
	// out-of-scope defect noted in the round's report, not touched here).
	// Fast path: causalClass holds exactly one entry per registered event
	// (registerTrivialClassLocked runs on every event-creation path, and a
	// class merge only re-points members, never deletes their keys), so equal
	// sizes mean there is nothing to repair. Without this guard every
	// AddCausal / AddCausalEquivalenceClass paid the O(|events|) scan below,
	// making a snapshot import O(|events| x |edges|).
	if len(p.causalClass) == len(p.events) {
		return
	}
	p.classRepairScans++
	for id := range p.events {
		if p.causalClass[id] == "" {
			p.registerTrivialClassLocked(id)
		}
	}
}

func (p *Poset) causalRepresentativeLocked(id EventID) EventID {
	if representative := p.causalClass[id]; representative != "" {
		return representative
	}
	return id
}

// causalClassMembersLocked returns id's complete causal-equivalence class,
// in canonical EventID order. Backed directly by classMembers, which is
// already maintained sorted (see registerTrivialClassLocked and
// addCausalEquivalenceClassLocked), so this is a lookup plus a defensive
// copy — no scan of p.events, no sort — instead of the O(poset size) scan
// the previous implementation paid on every call.
func (p *Poset) causalClassMembersLocked(id EventID) []EventID {
	representative := p.causalRepresentativeLocked(id)
	members := p.classMembers[representative]
	if len(members) == 0 {
		return []EventID{}
	}
	result := make([]EventID, len(members))
	copy(result, members)
	return result
}

// causalClassRepresentativesLocked returns every current representative, in
// canonical EventID order. classMembers' key set IS the current
// representative set by construction (registerTrivialClassLocked adds one
// key per new event; addCausalEquivalenceClassLocked deletes every
// absorbed representative's key in the same step it installs the merged
// one), so this only needs to list and sort those keys — no scan of
// p.events.
func (p *Poset) causalClassRepresentativesLocked() []EventID {
	result := make([]EventID, 0, len(p.classMembers))
	for representative := range p.classMembers {
		result = append(result, representative)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (p *Poset) nontrivialCausalClassesLocked() [][]EventID {
	result := make([][]EventID, 0)
	for _, representative := range p.causalClassRepresentativesLocked() {
		members := p.causalClassMembersLocked(representative)
		if len(members) > 1 {
			result = append(result, members)
		}
	}
	return result
}

// causalEquivalencePairsLocked returns a deterministic spanning representation
// of every nontrivial equivalence class.  A class with n occurrences needs
// only n-1 presentation edges because equivalence is transitive; the canonical
// semantic representation remains the complete member list.
func (p *Poset) causalEquivalencePairsLocked() [][2]EventID {
	result := make([][2]EventID, 0)
	for _, class := range p.nontrivialCausalClassesLocked() {
		representative := class[0]
		for _, member := range class[1:] {
			result = append(result, [2]EventID{representative, member})
		}
	}
	return result
}

// causalClassSuccessorsLocked returns the causal successors of
// representative's class, resolved to representatives, in canonical
// EventID order.
//
// Every raw event id gets a p.causalEdges entry at insert time
// (addEventLocked/mergeEventLocked/UnmarshalJSON), but the ONLY keys that
// ever hold real edge data are current representatives: addCausalLocked
// resolves both endpoints via causalRepresentativeLocked before writing
// p.causalEdges[from][to], and every causal-equivalence merge immediately
// calls normalizeCausalEdgesLocked, which rebuilds the whole map keyed by
// the representatives current AT THAT MOMENT. A non-representative
// member's p.causalEdges entry is therefore always empty. Rather than
// depend on that invariant alone, this unions p.causalEdges[member] over
// every member in the class (via classMembers — typically one member, the
// representative itself), which is correct even if that invariant were
// ever violated: it reproduces exactly the set the old O(poset size) full-
// map scan computed, just without paying for the scan.
func (p *Poset) causalClassSuccessorsLocked(representative EventID) []EventID {
	seen := make(map[EventID]bool)
	for _, member := range p.classMembers[representative] {
		for to := range p.causalEdges[member] {
			candidate := p.causalRepresentativeLocked(to)
			if candidate != representative {
				seen[candidate] = true
			}
		}
	}
	result := make([]EventID, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// causalClassPredecessorsLocked is causalClassSuccessorsLocked's mirror
// over p.reverseCausal. See that function's doc comment for why unioning
// over classMembers is correct and sufficient without scanning every
// p.reverseCausal key.
func (p *Poset) causalClassPredecessorsLocked(representative EventID) []EventID {
	seen := make(map[EventID]bool)
	for _, member := range p.classMembers[representative] {
		for from := range p.reverseCausal[member] {
			candidate := p.causalRepresentativeLocked(from)
			if candidate != representative {
				seen[candidate] = true
			}
		}
	}
	result := make([]EventID, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (p *Poset) canReachClassLocked(start, target EventID) bool {
	start = p.causalRepresentativeLocked(start)
	target = p.causalRepresentativeLocked(target)
	if start == target {
		return false
	}
	visited := make(map[EventID]bool)
	queue := []EventID{start}
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		if visited[current] {
			continue
		}
		visited[current] = true
		for _, successor := range p.causalClassSuccessorsLocked(current) {
			if successor == target {
				return true
			}
			if !visited[successor] {
				queue = append(queue, successor)
			}
		}
	}
	return false
}

func (p *Poset) reachableCausalClassesLocked(start EventID, forward bool) map[EventID]bool {
	start = p.causalRepresentativeLocked(start)
	result := map[EventID]bool{start: true}
	queue := []EventID{start}
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		neighbors := p.causalClassPredecessorsLocked(current)
		if forward {
			neighbors = p.causalClassSuccessorsLocked(current)
		}
		for _, neighbor := range neighbors {
			if !result[neighbor] {
				result[neighbor] = true
				queue = append(queue, neighbor)
			}
		}
	}
	return result
}

func (p *Poset) normalizeCausalEdgesLocked() {
	forward := make(map[EventID]map[EventID]bool, len(p.events))
	reverse := make(map[EventID]map[EventID]bool, len(p.events))
	for id := range p.events {
		forward[id] = make(map[EventID]bool)
		reverse[id] = make(map[EventID]bool)
	}
	for from, successors := range p.causalEdges {
		fromRepresentative := p.causalRepresentativeLocked(from)
		for to := range successors {
			toRepresentative := p.causalRepresentativeLocked(to)
			if fromRepresentative == toRepresentative {
				continue
			}
			forward[fromRepresentative][toRepresentative] = true
			reverse[toRepresentative][fromRepresentative] = true
		}
	}
	p.causalEdges = forward
	p.reverseCausal = reverse
}

func (p *Poset) maximumClassLamportLocked(representative EventID) uint64 {
	maximum := uint64(0)
	for _, member := range p.causalClassMembersLocked(representative) {
		if p.events[member].Clock.Lamport > maximum {
			maximum = p.events[member].Clock.Lamport
		}
	}
	return maximum
}

func (p *Poset) setClassLamportLocked(representative EventID, value uint64) {
	for _, member := range p.causalClassMembersLocked(representative) {
		p.events[member].Clock.Lamport = value
	}
}
