package arch

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ShaneDolphin/gorapide"
)

var (
	ErrInvalidAugmentedComputation = errors.New("invalid augmented Rapide computation")
	ErrConsistentCutLimit          = errors.New("consistent-cut exploration limit exceeded")
)

const ConsistentCutWitnessFormat = "gorapide.consistent-cut-state-witness.v1"

type semanticOccurrenceKind string

const (
	semanticEventOccurrence semanticOccurrenceKind = "event"
	semanticRefOperation    semanticOccurrenceKind = "ref-operation"
)

type semanticOccurrence struct {
	id        string
	kind      semanticOccurrenceKind
	initial   bool
	operation *StateOperationRecord
}

// AugmentedComputation is the event/Ref-operation causal graph used to recover
// states at event-pattern matches. Construction order has no semantic meaning.
type AugmentedComputation struct {
	nodes map[string]semanticOccurrence
	edges map[string]map[string]bool
}

func NewAugmentedComputation() *AugmentedComputation {
	return &AugmentedComputation{
		nodes: make(map[string]semanticOccurrence), edges: make(map[string]map[string]bool),
	}
}

// AugmentedComputation reconstructs the currently audited event/Ref-operation
// graph from an execution result. It fails if any operation dependency names an
// occurrence absent from the artifact.
func (result *ExecutionResult) AugmentedComputation() (*AugmentedComputation, error) {
	if result == nil || result.Poset == nil {
		return nil, fmt.Errorf("%w: execution result or poset is nil", ErrInvalidAugmentedComputation)
	}
	canonicalPoset, err := result.Poset.Canonical()
	if err != nil {
		return nil, err
	}
	computation := NewAugmentedComputation()
	for _, event := range canonicalPoset.Events {
		if err := computation.AddEventOccurrence(event.ID); err != nil {
			return nil, err
		}
	}
	for _, operation := range result.StateOperations {
		if err := computation.AddRefOperation(operation, operation.Kind == StateOperationCreate); err != nil {
			return nil, err
		}
	}
	for _, edge := range canonicalPoset.Edges {
		if err := computation.AddCausalDependency(edge.From, edge.To); err != nil {
			return nil, err
		}
	}
	if _, err := computation.validate(); err != nil {
		return nil, err
	}
	return computation, nil
}

// AddEventOccurrence registers one event occurrence identity.
func (computation *AugmentedComputation) AddEventOccurrence(id string) error {
	return computation.addOccurrence(semanticOccurrence{id: id, kind: semanticEventOccurrence})
}

// AddRefOperation registers one operation from a canonical per-Ref history.
// Initial is true for elaboration-time reference creation in the current module
// subset; such nodes belong to every state cut.
func (computation *AugmentedComputation) AddRefOperation(operation StateOperationRecord, initial bool) error {
	copy := operation
	copy.Causes = append([]string(nil), operation.Causes...)
	return computation.addOccurrence(semanticOccurrence{
		id: operation.ID, kind: semanticRefOperation, initial: initial, operation: &copy,
	})
}

func (computation *AugmentedComputation) addOccurrence(occurrence semanticOccurrence) error {
	if computation == nil {
		return fmt.Errorf("%w: computation is nil", ErrInvalidAugmentedComputation)
	}
	if occurrence.id == "" || strings.ContainsRune(occurrence.id, '\x00') {
		return fmt.Errorf("%w: occurrence identity is empty or contains NUL", ErrInvalidAugmentedComputation)
	}
	if computation.nodes == nil {
		computation.nodes = make(map[string]semanticOccurrence)
	}
	if _, duplicate := computation.nodes[occurrence.id]; duplicate {
		return fmt.Errorf("%w: duplicate occurrence %q", ErrInvalidAugmentedComputation, occurrence.id)
	}
	if occurrence.kind == semanticRefOperation {
		operation := occurrence.operation
		if operation == nil || operation.ID != occurrence.id || operation.ComponentID == "" || operation.Name == "" ||
			operation.Owner == "" || (operation.Kind != StateOperationCreate && operation.Kind != StateOperationDereference && operation.Kind != StateOperationAssign) {
			return fmt.Errorf("%w: malformed Ref operation %q", ErrInvalidAugmentedComputation, occurrence.id)
		}
		if operation.Kind == StateOperationCreate && operation.Version != 0 {
			return fmt.Errorf("%w: Ref creation %q has version %d", ErrInvalidAugmentedComputation, occurrence.id, operation.Version)
		}
		if operation.Kind != StateOperationCreate && occurrence.initial {
			return fmt.Errorf("%w: only Ref creation may be initial", ErrInvalidAugmentedComputation)
		}
	}
	computation.nodes[occurrence.id] = occurrence
	return nil
}

// AddCausalDependency records before -> after in the augmented computation.
func (computation *AugmentedComputation) AddCausalDependency(before, after string) error {
	if computation == nil || computation.nodes[before].id == "" || computation.nodes[after].id == "" {
		return fmt.Errorf("%w: causal dependency %q -> %q references a missing occurrence", ErrInvalidAugmentedComputation, before, after)
	}
	if before == after {
		return fmt.Errorf("%w: occurrence %q depends on itself", ErrInvalidAugmentedComputation, before)
	}
	if computation.edges == nil {
		computation.edges = make(map[string]map[string]bool)
	}
	if computation.edges[before] == nil {
		computation.edges[before] = make(map[string]bool)
	}
	computation.edges[before][after] = true
	return nil
}

// ConsistentCutLimits makes state-space cost explicit and reproducible.
type ConsistentCutLimits struct {
	MaxCuts                uint64
	MaxOptionalOccurrences uint64
}

// CutStateRecord is the value of one Ref in a consistent-cut witness.
type CutStateRecord struct {
	ComponentID string                  `json:"component_id"`
	Name        string                  `json:"name"`
	OperationID string                  `json:"operation_id"`
	Version     uint64                  `json:"version"`
	Value       gorapide.CanonicalValue `json:"value"`
}

// ConsistentCutStateWitness binds one prefix-closed occurrence set to every
// maximum matched event at which it is a valid cut and to the resulting Ref state.
type ConsistentCutStateWitness struct {
	Digest      string           `json:"digest"`
	Anchors     []string         `json:"anchors"`
	Occurrences []string         `json:"occurrences"`
	State       []CutStateRecord `json:"state"`
}

type canonicalCutWitness struct {
	Format      string           `json:"format"`
	Anchors     []string         `json:"anchors"`
	Occurrences []string         `json:"occurrences"`
	State       []CutStateRecord `json:"state"`
}

type validatedAugmentedComputation struct {
	nodes        map[string]semanticOccurrence
	predecessors map[string][]string
	successors   map[string][]string
	topological  []string
}

// ConsistentCutStateWitnesses canonically enumerates every bounded consistent
// cut containing the complete match and anchored at a maximum event of it.
func (computation *AugmentedComputation) ConsistentCutStateWitnesses(
	matchEventIDs []string,
	limits ConsistentCutLimits,
) ([]ConsistentCutStateWitness, error) {
	if limits.MaxCuts == 0 || limits.MaxOptionalOccurrences == 0 {
		return nil, fmt.Errorf("%w: cut limits must be positive", ErrInvalidAugmentedComputation)
	}
	validated, err := computation.validate()
	if err != nil {
		return nil, err
	}
	match := canonicalStrings(matchEventIDs)
	if len(match) == 0 {
		return nil, fmt.Errorf("%w: matched event set is empty", ErrInvalidAugmentedComputation)
	}
	for _, id := range match {
		node, exists := validated.nodes[id]
		if !exists || node.kind != semanticEventOccurrence {
			return nil, fmt.Errorf("%w: match occurrence %q is not an event", ErrInvalidAugmentedComputation, id)
		}
	}
	anchors := make([]string, 0, len(match))
	for _, candidate := range match {
		maximum := true
		for _, other := range match {
			if candidate != other && validated.reachable(candidate, other) {
				maximum = false
				break
			}
		}
		if maximum {
			anchors = append(anchors, candidate)
		}
	}
	base := make(map[string]bool)
	for _, id := range match {
		validated.addPredecessorClosure(id, base)
	}
	for id, node := range validated.nodes {
		if node.initial {
			validated.addPredecessorClosure(id, base)
		}
	}

	type accumulatedCut struct {
		occurrences []string
		anchors     map[string]bool
	}
	byKey := make(map[string]*accumulatedCut)
	for _, anchor := range anchors {
		forbidden := make(map[string]bool)
		validated.addSuccessorClosure(anchor, forbidden)
		delete(forbidden, anchor)
		validAnchor := true
		for id := range base {
			if forbidden[id] {
				validAnchor = false
				break
			}
		}
		if !validAnchor {
			continue
		}
		optional := uint64(0)
		for _, id := range validated.topological {
			if !base[id] && !forbidden[id] {
				optional++
			}
		}
		if optional > limits.MaxOptionalOccurrences {
			return nil, fmt.Errorf("%w: anchor %q has %d optional occurrences, max %d", ErrConsistentCutLimit, anchor, optional, limits.MaxOptionalOccurrences)
		}
		selected := copyStringSet(base)
		var enumerate func(int) error
		enumerate = func(index int) error {
			if index == len(validated.topological) {
				occurrences := selectedStrings(selected)
				key := strings.Join(occurrences, "\x00")
				cut := byKey[key]
				if cut == nil {
					if uint64(len(byKey)) >= limits.MaxCuts {
						return fmt.Errorf("%w: more than %d unique cuts", ErrConsistentCutLimit, limits.MaxCuts)
					}
					cut = &accumulatedCut{occurrences: occurrences, anchors: make(map[string]bool)}
					byKey[key] = cut
				}
				cut.anchors[anchor] = true
				return nil
			}
			id := validated.topological[index]
			if base[id] || forbidden[id] {
				return enumerate(index + 1)
			}
			if err := enumerate(index + 1); err != nil {
				return err
			}
			for _, predecessor := range validated.predecessors[id] {
				if !selected[predecessor] {
					return nil
				}
			}
			selected[id] = true
			err := enumerate(index + 1)
			delete(selected, id)
			return err
		}
		if err := enumerate(0); err != nil {
			return nil, err
		}
	}

	result := make([]ConsistentCutStateWitness, 0, len(byKey))
	for _, cut := range byKey {
		state, err := validated.stateAt(cut.occurrences)
		if err != nil {
			return nil, err
		}
		witness := ConsistentCutStateWitness{
			Anchors: selectedStrings(cut.anchors), Occurrences: cut.occurrences, State: state,
		}
		encoded, err := json.Marshal(canonicalCutWitness{
			Format: ConsistentCutWitnessFormat, Anchors: witness.Anchors,
			Occurrences: witness.Occurrences, State: witness.State,
		})
		if err != nil {
			return nil, err
		}
		witness.Digest = digestBytes(encoded)
		result = append(result, witness)
	}
	sort.Slice(result, func(i, j int) bool {
		left, _ := json.Marshal(result[i])
		right, _ := json.Marshal(result[j])
		return string(left) < string(right)
	})
	return result, nil
}

func (computation *AugmentedComputation) validate() (*validatedAugmentedComputation, error) {
	if computation == nil || len(computation.nodes) == 0 {
		return nil, fmt.Errorf("%w: computation is nil or empty", ErrInvalidAugmentedComputation)
	}
	predecessors := make(map[string][]string, len(computation.nodes))
	successors := make(map[string][]string, len(computation.nodes))
	edges := make(map[string]bool)
	addEdge := func(before, after string) error {
		if computation.nodes[before].id == "" || computation.nodes[after].id == "" || before == after {
			return fmt.Errorf("%w: invalid causal dependency %q -> %q", ErrInvalidAugmentedComputation, before, after)
		}
		key := before + "\x00" + after
		if edges[key] {
			return nil
		}
		edges[key] = true
		predecessors[after] = append(predecessors[after], before)
		successors[before] = append(successors[before], after)
		return nil
	}
	for before, targets := range computation.edges {
		for after := range targets {
			if err := addEdge(before, after); err != nil {
				return nil, err
			}
		}
	}
	for _, node := range computation.nodes {
		if node.operation == nil {
			continue
		}
		operation := node.operation
		beforeValues := append(append(append([]string(nil), operation.Causes...), operation.Dependencies...), operation.Predecessor, operation.ValueSource)
		for _, before := range beforeValues {
			if before == "" {
				continue
			}
			if err := addEdge(before, operation.ID); err != nil {
				return nil, err
			}
		}
		for _, after := range operation.Successors {
			if err := addEdge(operation.ID, after); err != nil {
				return nil, err
			}
		}
	}
	for id := range computation.nodes {
		predecessors[id] = canonicalStrings(predecessors[id])
		successors[id] = canonicalStrings(successors[id])
	}
	indegree := make(map[string]int, len(computation.nodes))
	ready := make([]string, 0)
	for id := range computation.nodes {
		indegree[id] = len(predecessors[id])
		if indegree[id] == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	topological := make([]string, 0, len(computation.nodes))
	for len(ready) != 0 {
		id := ready[0]
		ready = ready[1:]
		topological = append(topological, id)
		for _, next := range successors[id] {
			indegree[next]--
			if indegree[next] == 0 {
				ready = append(ready, next)
				sort.Strings(ready)
			}
		}
	}
	if len(topological) != len(computation.nodes) {
		return nil, fmt.Errorf("%w: causal graph contains a cycle", ErrInvalidAugmentedComputation)
	}
	return &validatedAugmentedComputation{
		nodes: computation.nodes, predecessors: predecessors, successors: successors, topological: topological,
	}, nil
}

func (computation *validatedAugmentedComputation) reachable(before, after string) bool {
	if before == after {
		return true
	}
	seen := map[string]bool{before: true}
	queue := []string{before}
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range computation.successors[current] {
			if next == after {
				return true
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

func (computation *validatedAugmentedComputation) addPredecessorClosure(id string, result map[string]bool) {
	if result[id] {
		return
	}
	result[id] = true
	for _, predecessor := range computation.predecessors[id] {
		computation.addPredecessorClosure(predecessor, result)
	}
}

func (computation *validatedAugmentedComputation) addSuccessorClosure(id string, result map[string]bool) {
	if result[id] {
		return
	}
	result[id] = true
	for _, successor := range computation.successors[id] {
		computation.addSuccessorClosure(successor, result)
	}
}

func (computation *validatedAugmentedComputation) stateAt(occurrences []string) ([]CutStateRecord, error) {
	inCut := make(map[string]bool, len(occurrences))
	for _, id := range occurrences {
		inCut[id] = true
	}
	latest := make(map[string]StateOperationRecord)
	for _, id := range computation.topological {
		node := computation.nodes[id]
		if !inCut[id] || node.operation == nil ||
			(node.operation.Kind != StateOperationCreate && node.operation.Kind != StateOperationAssign) {
			continue
		}
		operation := *node.operation
		key := operation.ComponentID + "\x00" + operation.Name
		prior, exists := latest[key]
		if exists && operation.Version <= prior.Version {
			return nil, fmt.Errorf("%w: Ref %s.%s has non-increasing versions in cut", ErrInvalidAugmentedComputation, operation.ComponentID, operation.Name)
		}
		if exists && operation.Version != prior.Version+1 {
			return nil, fmt.Errorf("%w: Ref %s.%s skips version %d in cut", ErrInvalidAugmentedComputation, operation.ComponentID, operation.Name, prior.Version+1)
		}
		latest[key] = operation
	}
	result := make([]CutStateRecord, 0, len(latest))
	for _, operation := range latest {
		result = append(result, CutStateRecord{
			ComponentID: operation.ComponentID, Name: operation.Name,
			OperationID: operation.ID, Version: operation.Version, Value: operation.Value,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ComponentID != result[j].ComponentID {
			return result[i].ComponentID < result[j].ComponentID
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func canonicalStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	write := 0
	for _, value := range result {
		if value == "" || write > 0 && result[write-1] == value {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

func copyStringSet(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for value := range source {
		result[value] = true
	}
	return result
}

func selectedStrings(selected map[string]bool) []string {
	result := make([]string, 0, len(selected))
	for value, include := range selected {
		if include {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
