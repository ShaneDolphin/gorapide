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
	for id, event := range p.events {
		if representatives[p.causalRepresentativeLocked(id)] {
			p.causalClass[id] = representative
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
	for id := range p.events {
		if p.causalClass[id] == "" {
			p.causalClass[id] = id
		}
	}
}

func (p *Poset) causalRepresentativeLocked(id EventID) EventID {
	if representative := p.causalClass[id]; representative != "" {
		return representative
	}
	return id
}

func (p *Poset) causalClassMembersLocked(id EventID) []EventID {
	representative := p.causalRepresentativeLocked(id)
	result := make([]EventID, 0)
	for candidate := range p.events {
		if p.causalRepresentativeLocked(candidate) == representative {
			result = append(result, candidate)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (p *Poset) causalClassRepresentativesLocked() []EventID {
	seen := make(map[EventID]bool, len(p.events))
	for id := range p.events {
		seen[p.causalRepresentativeLocked(id)] = true
	}
	result := make([]EventID, 0, len(seen))
	for representative := range seen {
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

func (p *Poset) causalClassSuccessorsLocked(representative EventID) []EventID {
	seen := make(map[EventID]bool)
	for from, successors := range p.causalEdges {
		if p.causalRepresentativeLocked(from) != representative {
			continue
		}
		for to := range successors {
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

func (p *Poset) causalClassPredecessorsLocked(representative EventID) []EventID {
	seen := make(map[EventID]bool)
	for to, predecessors := range p.reverseCausal {
		if p.causalRepresentativeLocked(to) != representative {
			continue
		}
		for from := range predecessors {
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
