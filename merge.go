package gorapide

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Snapshot is a serializable representation of a subset of a Poset,
// used for shipping events between nodes.
type Snapshot struct {
	NodeID             NodeID        `json:"node_id"`
	Events             []EventExport `json:"events"`
	CausalEquivalences [][]string    `json:"causal_equivalences,omitempty"`
	CausalEdges        [][]string    `json:"causal_edges"`
	HighWater          uint64        `json:"high_water"`
}

// CloneSnapshot returns a deeply owned integration snapshot. It preserves
// malformed wall-time and edge text for the receiver to diagnose, while
// rejecting parameter or Rapide-timing values that cannot be copied into the
// deterministic value algebra.
func CloneSnapshot(snap *Snapshot) (*Snapshot, error) {
	if snap == nil {
		return nil, fmt.Errorf("gorapide.CloneSnapshot: snapshot is nil")
	}
	result := &Snapshot{
		NodeID:             snap.NodeID,
		Events:             make([]EventExport, len(snap.Events)),
		CausalEquivalences: make([][]string, len(snap.CausalEquivalences)),
		CausalEdges:        make([][]string, len(snap.CausalEdges)),
		HighWater:          snap.HighWater,
	}
	for index, exported := range snap.Events {
		params, err := CanonicalizeParams(exported.Params)
		if err != nil {
			return nil, fmt.Errorf("gorapide.CloneSnapshot: event %d %q parameters: %w", index, exported.ID, err)
		}
		timings, err := CanonicalizeEventTimings(exported.Timings)
		if err != nil {
			return nil, fmt.Errorf("gorapide.CloneSnapshot: event %d %q timings: %w", index, exported.ID, err)
		}
		cloned := exported
		cloned.Params = params
		cloned.Timings = timings
		if exported.VectorClock != nil {
			cloned.VectorClock = make(map[string]uint64, len(exported.VectorClock))
			for key, value := range exported.VectorClock {
				cloned.VectorClock[key] = value
			}
		}
		result.Events[index] = cloned
	}
	for index, edge := range snap.CausalEdges {
		result.CausalEdges[index] = append([]string(nil), edge...)
	}
	for index, class := range snap.CausalEquivalences {
		result.CausalEquivalences[index] = append([]string(nil), class...)
	}
	return result, nil
}

// MergeResult summarizes the outcome of merging a Snapshot into a Poset.
type MergeResult struct {
	EventsAdded         int
	EventsSkipped       int
	EquivalencesAdded   int
	EquivalencesSkipped int
	EdgesAdded          int
	EdgesSkipped        int
	EdgesPending        int
}

// PendingEdge represents a causal edge whose endpoints may not yet be present
// in the local poset.
type PendingEdge struct {
	From EventID
	To   EventID
}

// ErrInvalidSnapshotMerge identifies malformed or conflicting content in the
// permissive legacy Snapshot integration format.
var ErrInvalidSnapshotMerge = errors.New("snapshot merge contains invalid or conflicting content")

// SnapshotMergeIssue is one stable validation or application failure observed
// while integrating a legacy Snapshot. A merge may return a partial result and
// a *SnapshotMergeError containing all issues.
type SnapshotMergeIssue struct {
	Section string
	Index   int
	Object  string
	Cause   error
}

func (issue SnapshotMergeIssue) Error() string {
	location := issue.Section
	if issue.Index >= 0 {
		location = fmt.Sprintf("%s[%d]", location, issue.Index)
	}
	if issue.Object != "" {
		location += " " + fmt.Sprintf("%q", issue.Object)
	}
	return fmt.Sprintf("%s: %v", location, issue.Cause)
}

func (issue SnapshotMergeIssue) Unwrap() error {
	return issue.Cause
}

// SnapshotMergeError reports every malformed or conflicting object in stable
// section/object/index order. Successfully integrated objects remain described
// by the accompanying MergeResult.
type SnapshotMergeError struct {
	Issues []SnapshotMergeIssue
}

func (e *SnapshotMergeError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return ErrInvalidSnapshotMerge.Error()
	}
	parts := make([]string, len(e.Issues))
	for index, issue := range e.Issues {
		parts[index] = issue.Error()
	}
	return ErrInvalidSnapshotMerge.Error() + ": " + strings.Join(parts, "; ")
}

func (e *SnapshotMergeError) Unwrap() []error {
	if e == nil {
		return []error{ErrInvalidSnapshotMerge}
	}
	result := make([]error, 1, len(e.Issues)+1)
	result[0] = ErrInvalidSnapshotMerge
	for _, issue := range e.Issues {
		if issue.Cause != nil {
			result = append(result, issue.Cause)
		}
	}
	return result
}

func newSnapshotMergeError(issues []SnapshotMergeIssue) error {
	if len(issues) == 0 {
		return nil
	}
	result := append([]SnapshotMergeIssue(nil), issues...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Section != result[j].Section {
			return result[i].Section < result[j].Section
		}
		if result[i].Object != result[j].Object {
			return result[i].Object < result[j].Object
		}
		if result[i].Index != result[j].Index {
			return result[i].Index < result[j].Index
		}
		return result[i].Cause.Error() < result[j].Cause.Error()
	})
	return &SnapshotMergeError{Issues: result}
}

func sortEventIDsByLamport(ids []EventID, events map[EventID]*Event) {
	sort.Slice(ids, func(i, j int) bool {
		left := events[ids[i]].Clock.Lamport
		right := events[ids[j]].Clock.Lamport
		if left != right {
			return left < right
		}
		return ids[i] < ids[j]
	})
}

// MergeSnapshot integrates a remote Snapshot into the local Poset.
// Events are sorted by Lamport and EventID before insertion. Exact duplicate
// occurrences are skipped, while conflicting reuse of one EventID is reported.
// Edges whose endpoints are missing are buffered for later resolution.
//
// MergeSnapshot is a permissive legacy/integration operation, not a canonical
// replay import. It returns a partial MergeResult together with a stable
// *SnapshotMergeError when any object is invalid; it never repairs malformed
// wall-clock metadata from ambient time. Use ParseCanonicalPoset for trusted
// semantic input.
func (p *Poset) MergeSnapshot(snap *Snapshot) (*MergeResult, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: local poset is nil", ErrInvalidSnapshotMerge)
	}
	if snap == nil {
		return nil, fmt.Errorf("%w: snapshot is nil", ErrInvalidSnapshotMerge)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.events == nil || p.causalEdges == nil || p.reverseCausal == nil {
		return nil, fmt.Errorf("%w: local poset is not initialized", ErrInvalidSnapshotMerge)
	}

	result := &MergeResult{}
	var issues []SnapshotMergeIssue
	addIssue := func(section string, index int, object string, cause error) {
		issues = append(issues, SnapshotMergeIssue{Section: section, Index: index, Object: object, Cause: cause})
	}

	type indexedEvent struct {
		export EventExport
		index  int
	}
	sortedEvents := make([]indexedEvent, len(snap.Events))
	counts := make(map[string]int, len(snap.Events))
	maxLamport := uint64(0)
	for index, exported := range snap.Events {
		sortedEvents[index] = indexedEvent{export: exported, index: index}
		counts[exported.ID]++
		if exported.Lamport > maxLamport {
			maxLamport = exported.Lamport
		}
	}
	sort.Slice(sortedEvents, func(i, j int) bool {
		left, right := sortedEvents[i].export, sortedEvents[j].export
		if left.Lamport != right.Lamport {
			return left.Lamport < right.Lamport
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return sortedEvents[i].index < sortedEvents[j].index
	})
	if snap.HighWater < maxLamport {
		addIssue("snapshot", -1, "high_water", fmt.Errorf("value %d is below maximum event Lamport %d", snap.HighWater, maxLamport))
	}

	// Merge events.
	reportedDuplicate := make(map[string]bool)
	for position, item := range sortedEvents {
		ee := item.export
		if ee.ID == "" {
			result.EventsSkipped++
			addIssue("event", position, "", fmt.Errorf("event ID is empty"))
			continue
		}
		if counts[ee.ID] > 1 {
			result.EventsSkipped++
			if !reportedDuplicate[ee.ID] {
				reportedDuplicate[ee.ID] = true
				addIssue("event", position, ee.ID, fmt.Errorf("event ID occurs %d times in one snapshot", counts[ee.ID]))
			}
			continue
		}

		event, err := eventFromSnapshotExport(ee)
		if err != nil {
			result.EventsSkipped++
			addIssue("event", position, ee.ID, err)
			continue
		}
		id := EventID(ee.ID)
		if existing, exists := p.events[id]; exists {
			result.EventsSkipped++
			if err := snapshotEventConflict(existing, event); err != nil {
				addIssue("event", position, ee.ID, err)
			}
			continue
		}
		if err := p.mergeEventLocked(event); err != nil {
			result.EventsSkipped++
			addIssue("event", position, ee.ID, fmt.Errorf("poset insertion: %w", err))
			continue
		}
		result.EventsAdded++
	}

	// Merge causal-equivalence classes before strict edges. Class and member
	// order in the permissive snapshot format is nonsemantic, but malformed,
	// overlapping, missing, or order-conflicting content fails explicitly.
	type indexedEquivalence struct {
		members []string
		index   int
	}
	sortedEquivalences := make([]indexedEquivalence, len(snap.CausalEquivalences))
	for index, class := range snap.CausalEquivalences {
		members := append([]string(nil), class...)
		sort.Strings(members)
		sortedEquivalences[index] = indexedEquivalence{members: members, index: index}
	}
	sort.Slice(sortedEquivalences, func(i, j int) bool {
		left := strings.Join(sortedEquivalences[i].members, "\x00")
		right := strings.Join(sortedEquivalences[j].members, "\x00")
		if left != right {
			return left < right
		}
		return sortedEquivalences[i].index < sortedEquivalences[j].index
	})
	seenEquivalentMembers := make(map[string]bool)
	seenEquivalences := make(map[string]bool)
	for position, item := range sortedEquivalences {
		members := item.members
		object := strings.Join(members, " =c ")
		if len(members) < 2 {
			result.EquivalencesSkipped++
			addIssue("causal-equivalence", position, object, fmt.Errorf("class must contain at least two event IDs"))
			continue
		}
		valid := true
		for index, member := range members {
			if member == "" || !utf8.ValidString(member) {
				addIssue("causal-equivalence", position, object, fmt.Errorf("member %d is empty or not valid UTF-8", index))
				valid = false
				break
			}
			if index > 0 && member == members[index-1] {
				addIssue("causal-equivalence", position, object, fmt.Errorf("event %q occurs more than once in one class", member))
				valid = false
				break
			}
			if seenEquivalentMembers[member] {
				addIssue("causal-equivalence", position, object, fmt.Errorf("event %q occurs in multiple classes", member))
				valid = false
				break
			}
			if _, exists := p.events[EventID(member)]; !exists {
				addIssue("causal-equivalence", position, object, fmt.Errorf("event %q is absent", member))
				valid = false
				break
			}
		}
		key := strings.Join(members, "\x00")
		if seenEquivalences[key] {
			result.EquivalencesSkipped++
			continue
		}
		seenEquivalences[key] = true
		if !valid {
			result.EquivalencesSkipped++
			continue
		}
		for _, member := range members {
			seenEquivalentMembers[member] = true
		}
		ids := make([]EventID, len(members))
		alreadyEquivalent := true
		for index, member := range members {
			ids[index] = EventID(member)
			if index > 0 && p.causalRepresentativeLocked(ids[index]) != p.causalRepresentativeLocked(ids[0]) {
				alreadyEquivalent = false
			}
		}
		if alreadyEquivalent {
			result.EquivalencesSkipped++
			continue
		}
		if err := p.addCausalEquivalenceClassLocked(ids...); err != nil {
			result.EquivalencesSkipped++
			addIssue("causal-equivalence", position, object, err)
		} else {
			result.EquivalencesAdded++
		}
	}

	// Merge edges.
	type indexedEdge struct {
		edge  []string
		index int
	}
	sortedEdges := make([]indexedEdge, len(snap.CausalEdges))
	for index, edge := range snap.CausalEdges {
		sortedEdges[index] = indexedEdge{edge: append([]string(nil), edge...), index: index}
	}
	sort.Slice(sortedEdges, func(i, j int) bool {
		left := snapshotEdgeKey(sortedEdges[i].edge)
		right := snapshotEdgeKey(sortedEdges[j].edge)
		if left != right {
			return left < right
		}
		return sortedEdges[i].index < sortedEdges[j].index
	})
	seenEdges := make(map[string]bool, len(sortedEdges))
	for position, item := range sortedEdges {
		edge := item.edge
		if len(edge) != 2 {
			result.EdgesSkipped++
			addIssue("edge", position, strings.Join(edge, " -> "), fmt.Errorf("causal edge must contain exactly two event IDs"))
			continue
		}
		from := EventID(edge[0])
		to := EventID(edge[1])
		object := edge[0] + " -> " + edge[1]
		if from == "" || to == "" {
			result.EdgesSkipped++
			addIssue("edge", position, object, fmt.Errorf("causal edge endpoint is empty"))
			continue
		}
		if !utf8.ValidString(edge[0]) || !utf8.ValidString(edge[1]) {
			result.EdgesSkipped++
			addIssue("edge", position, object, fmt.Errorf("causal edge endpoint is not valid UTF-8"))
			continue
		}
		if from == to {
			result.EdgesSkipped++
			addIssue("edge", position, object, fmt.Errorf("%w: %s", ErrSelfCausal, from))
			continue
		}
		key := snapshotEdgeKey(edge)
		if seenEdges[key] {
			result.EdgesSkipped++
			continue
		}
		seenEdges[key] = true

		_, fromOK := p.events[from]
		_, toOK := p.events[to]
		if !fromOK || !toOK {
			if pendingEdgeExists(p.pendingEdges, from, to) {
				result.EdgesSkipped++
			} else {
				p.pendingEdges = append(p.pendingEdges, PendingEdge{From: from, To: to})
				result.EdgesPending++
			}
			continue
		}
		fromRepresentative := p.causalRepresentativeLocked(from)
		toRepresentative := p.causalRepresentativeLocked(to)
		if fromRepresentative == toRepresentative {
			result.EdgesSkipped++
			addIssue("edge", position, object, fmt.Errorf("%w: equivalent endpoints", ErrSelfCausal))
			continue
		}
		if p.causalEdges[fromRepresentative][toRepresentative] {
			result.EdgesSkipped++
			continue
		}
		if err := p.addCausalLocked(from, to); err != nil {
			result.EdgesSkipped++
			addIssue("edge", position, object, err)
		} else {
			result.EdgesAdded++
		}
	}

	// Reconcile lamport counter from HighWater.
	if snap.HighWater > p.lamportCounter {
		p.lamportCounter = snap.HighWater
	}

	return result, newSnapshotMergeError(issues)
}

func eventFromSnapshotExport(exported EventExport) (*Event, error) {
	if !utf8.ValidString(exported.ID) {
		return nil, fmt.Errorf("event ID is not valid UTF-8")
	}
	if exported.Name == "" {
		return nil, fmt.Errorf("event name is empty")
	}
	if !utf8.ValidString(exported.Name) {
		return nil, fmt.Errorf("event name is not valid UTF-8")
	}
	if !utf8.ValidString(exported.Source) {
		return nil, fmt.Errorf("event source is not valid UTF-8")
	}
	wallTime, err := time.Parse(time.RFC3339Nano, exported.WallTime)
	if err != nil {
		return nil, fmt.Errorf("invalid wall_time %q: %w", exported.WallTime, err)
	}
	params, err := CanonicalizeParams(exported.Params)
	if err != nil {
		return nil, fmt.Errorf("parameters: %w", err)
	}
	timings, err := CanonicalizeEventTimings(exported.Timings)
	if err != nil {
		return nil, fmt.Errorf("Rapide timings: %w", err)
	}
	event := &Event{
		ID:     EventID(exported.ID),
		Name:   exported.Name,
		Params: params,
		Source: exported.Source,
		Clock: ClockStamp{
			Lamport:  exported.Lamport,
			WallTime: wallTime,
		},
		Timings: timings,
		Observations: []EventObservation{{
			Name: exported.Name, Source: exported.Source, Params: copyParams(params),
		}},
	}
	if exported.VectorClock != nil {
		event.Clock.Vector = make(VectorClock, len(exported.VectorClock))
		for key, value := range exported.VectorClock {
			if key == "" || !utf8.ValidString(key) {
				return nil, fmt.Errorf("vector clock node ID %q is empty or invalid UTF-8", key)
			}
			event.Clock.Vector[NodeID(key)] = value
		}
	}
	return event, nil
}

func snapshotEventConflict(existing, candidate *Event) error {
	if existing.Name != candidate.Name {
		return fmt.Errorf("existing event name %q conflicts with snapshot name %q", existing.Name, candidate.Name)
	}
	if existing.Source != candidate.Source {
		return fmt.Errorf("existing event source %q conflicts with snapshot source %q", existing.Source, candidate.Source)
	}
	existingParams, err := CanonicalizeParams(existing.Params)
	if err != nil {
		return fmt.Errorf("existing event parameters are invalid: %w", err)
	}
	if !reflect.DeepEqual(existingParams, candidate.Params) {
		return fmt.Errorf("existing event parameters conflict with snapshot parameters")
	}
	existingTimings, err := CanonicalizeEventTimings(existing.Timings)
	if err != nil {
		return fmt.Errorf("existing event Rapide timings are invalid: %w", err)
	}
	if !reflect.DeepEqual(existingTimings, candidate.Timings) {
		return fmt.Errorf("existing event Rapide timings conflict with snapshot timings")
	}
	if !existing.Clock.WallTime.Equal(candidate.Clock.WallTime) {
		return fmt.Errorf("existing event wall_time %q conflicts with snapshot wall_time %q",
			existing.Clock.WallTime.Format(time.RFC3339Nano), candidate.Clock.WallTime.Format(time.RFC3339Nano))
	}
	return nil
}

func pendingEdgeExists(edges []PendingEdge, from, to EventID) bool {
	for _, edge := range edges {
		if edge.From == from && edge.To == to {
			return true
		}
	}
	return false
}

func snapshotEdgeKey(edge []string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%d:", len(edge))
	for _, endpoint := range edge {
		fmt.Fprintf(&builder, "%d:%s", len(endpoint), endpoint)
	}
	return builder.String()
}

// CreateSnapshot builds a full Snapshot of the current Poset state
// containing all events and edges.
func (p *Poset) CreateSnapshot(nodeID NodeID) *Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()

	snap := &Snapshot{
		NodeID:    nodeID,
		Events:    make([]EventExport, 0, len(p.events)),
		HighWater: p.lamportCounter,
	}

	// Sort events by Lamport for deterministic output.
	ids := make([]EventID, 0, len(p.events))
	for id := range p.events {
		ids = append(ids, id)
	}
	sortEventIDsByLamport(ids, p.events)

	for _, id := range ids {
		e := p.events[id]
		ee := EventExport{
			ID:       string(e.ID),
			Name:     e.Name,
			Params:   copyParams(e.Params),
			Source:   e.Source,
			Lamport:  e.Clock.Lamport,
			WallTime: e.Clock.WallTime.Format(time.RFC3339Nano),
			Timings:  append([]EventTiming(nil), e.Timings...),
		}
		if e.Clock.Vector != nil {
			ee.VectorClock = make(map[string]uint64, len(e.Clock.Vector))
			for k, v := range e.Clock.Vector {
				ee.VectorClock[string(k)] = v
			}
		}
		snap.Events = append(snap.Events, ee)
	}
	for _, class := range p.nontrivialCausalClassesLocked() {
		members := make([]string, len(class))
		for index, member := range class {
			members[index] = string(member)
		}
		snap.CausalEquivalences = append(snap.CausalEquivalences, members)
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

	sortEventIDsByLamport(ids, p.events)

	for _, id := range ids {
		e := p.events[id]
		ee := EventExport{
			ID:       string(e.ID),
			Name:     e.Name,
			Params:   copyParams(e.Params),
			Source:   e.Source,
			Lamport:  e.Clock.Lamport,
			WallTime: e.Clock.WallTime.Format(time.RFC3339Nano),
			Timings:  append([]EventTiming(nil), e.Timings...),
		}
		if e.Clock.Vector != nil {
			ee.VectorClock = make(map[string]uint64, len(e.Clock.Vector))
			for k, v := range e.Clock.Vector {
				ee.VectorClock[string(k)] = v
			}
		}
		snap.Events = append(snap.Events, ee)
	}
	for _, class := range p.nontrivialCausalClassesLocked() {
		include := true
		for _, member := range class {
			if !included[member] {
				include = false
				break
			}
		}
		if !include {
			continue
		}
		members := make([]string, len(class))
		for index, member := range class {
			members[index] = string(member)
		}
		snap.CausalEquivalences = append(snap.CausalEquivalences, members)
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
