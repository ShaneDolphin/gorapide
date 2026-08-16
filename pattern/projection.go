package pattern

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/ShaneDolphin/gorapide"
)

var ErrInvalidProjection = errors.New("invalid visible-poset projection")

// Projection is Rapide's visible-poset view. Only selected event occurrences
// and qualified views are exposed, while causality between visible endpoints is
// inherited transitively from the source computation. Hidden intermediates
// therefore preserve causality without becoming matchable events.
type Projection struct {
	source  PosetReader
	all     gorapide.EventSet
	views   gorapide.EventSet
	visible map[gorapide.EventID]bool
}

// NewProjection constructs a stable, defensive visible-poset projection.
func NewProjection(source PosetReader, visible gorapide.EventSet) (*Projection, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: source is nil", ErrInvalidProjection)
	}
	sourceIDs := make(map[gorapide.EventID]bool)
	for _, event := range source.All() {
		if event != nil && event.ID != "" {
			sourceIDs[event.ID] = true
		}
	}

	views := make(gorapide.EventSet, 0, len(visible))
	seenViews := make(map[string]*gorapide.Event)
	for index, event := range visible {
		if event == nil || event.ID == "" {
			return nil, fmt.Errorf("%w: visible event %d is nil or unidentified", ErrInvalidProjection, index)
		}
		if !sourceIDs[event.ID] {
			return nil, fmt.Errorf("%w: event %s is absent from source", ErrInvalidProjection, event.ID)
		}
		key := projectionViewKey(event)
		if previous, exists := seenViews[key]; exists {
			if !reflect.DeepEqual(previous.Params, event.Params) {
				return nil, fmt.Errorf("%w: conflicting view %s.%s for event %s", ErrInvalidProjection, event.Source, event.Name, event.ID)
			}
			continue
		}
		copy := event.Snapshot()
		seenViews[key] = copy
		views = append(views, copy)
	}
	sort.Slice(views, func(i, j int) bool { return projectionViewKey(views[i]) < projectionViewKey(views[j]) })

	projection := &Projection{
		source: source, views: views, visible: make(map[gorapide.EventID]bool),
	}
	for _, event := range views {
		if projection.visible[event.ID] {
			continue
		}
		projection.visible[event.ID] = true
		projection.all = append(projection.all, event.Snapshot())
	}
	sort.Slice(projection.all, func(i, j int) bool { return projection.all[i].ID < projection.all[j].ID })
	return projection, nil
}

func projectionViewKey(event *gorapide.Event) string {
	return fmt.Sprintf("%d:%s%d:%s%d:%s", len(event.ID), event.ID, len(event.Source), event.Source, len(event.Name), event.Name)
}

func snapshotProjectionEvents(events gorapide.EventSet) gorapide.EventSet {
	result := make(gorapide.EventSet, len(events))
	for i, event := range events {
		result[i] = event.Snapshot()
	}
	return result
}

// All returns each visible event occurrence once in canonical EventID order.
func (projection *Projection) All() gorapide.EventSet {
	return snapshotProjectionEvents(projection.all)
}

// ByName returns the visible qualified event views with the requested action.
func (projection *Projection) ByName(name string) gorapide.EventSet {
	result := make(gorapide.EventSet, 0)
	for _, event := range projection.views {
		if event.Name == name {
			result = append(result, event.Snapshot())
		}
	}
	return result
}

// Len returns the number of visible event occurrences, not the number of
// qualified views.
func (projection *Projection) Len() int {
	return len(projection.all)
}

func (projection *Projection) IsCausallyBefore(a, b gorapide.EventID) bool {
	return projection.visible[a] && projection.visible[b] && projection.source.IsCausallyBefore(a, b)
}

func (projection *Projection) IsCausallyEquivalent(a, b gorapide.EventID) bool {
	return projection.visible[a] && projection.visible[b] && sourceCausallyEquivalent(projection.source, a, b)
}

type causalEquivalenceReader interface {
	IsCausallyEquivalent(a, b gorapide.EventID) bool
}

func sourceCausallyEquivalent(source PosetReader, a, b gorapide.EventID) bool {
	if reader, ok := source.(causalEquivalenceReader); ok {
		return reader.IsCausallyEquivalent(a, b)
	}
	// A historical strict-poset reader has only singleton equivalence classes.
	return a == b
}

func (projection *Projection) IsCausallyIndependent(a, b gorapide.EventID) bool {
	return projection.visible[a] && projection.visible[b] && projection.source.IsCausallyIndependent(a, b)
}

func (projection *Projection) CausalAncestors(id gorapide.EventID) gorapide.EventSet {
	if !projection.visible[id] {
		return gorapide.EventSet{}
	}
	result := make(gorapide.EventSet, 0)
	for _, event := range projection.all {
		if projection.source.IsCausallyBefore(event.ID, id) {
			result = append(result, event.Snapshot())
		}
	}
	return result
}

func (projection *Projection) CausalDescendants(id gorapide.EventID) gorapide.EventSet {
	if !projection.visible[id] {
		return gorapide.EventSet{}
	}
	result := make(gorapide.EventSet, 0)
	for _, event := range projection.all {
		if projection.source.IsCausallyBefore(id, event.ID) {
			result = append(result, event.Snapshot())
		}
	}
	return result
}

func (projection *Projection) CausalChain(from, to gorapide.EventID) (gorapide.EventSet, error) {
	if !projection.visible[from] {
		return nil, fmt.Errorf("%w: %s", gorapide.ErrEventNotFound, from)
	}
	if !projection.visible[to] {
		return nil, fmt.Errorf("%w: %s", gorapide.ErrEventNotFound, to)
	}
	if !projection.source.IsCausallyBefore(from, to) {
		return nil, fmt.Errorf("%w: %s to %s", gorapide.ErrNoPath, from, to)
	}
	result := make(gorapide.EventSet, 0)
	for _, event := range projection.all {
		if event.ID == from || event.ID == to ||
			(projection.source.IsCausallyBefore(from, event.ID) && projection.source.IsCausallyBefore(event.ID, to)) {
			result = append(result, event.Snapshot())
		}
	}
	return result, nil
}

func (projection *Projection) Roots() gorapide.EventSet {
	result := make(gorapide.EventSet, 0)
	for _, event := range projection.all {
		if len(projection.CausalAncestors(event.ID)) == 0 {
			result = append(result, event.Snapshot())
		}
	}
	return result
}

func (projection *Projection) Leaves() gorapide.EventSet {
	result := make(gorapide.EventSet, 0)
	for _, event := range projection.all {
		if len(projection.CausalDescendants(event.ID)) == 0 {
			result = append(result, event.Snapshot())
		}
	}
	return result
}

// TopologicalSort uses ancestor count and EventID only as a stable
// representation order. It does not add causal order between independent
// visible events.
func (projection *Projection) TopologicalSort() []*gorapide.Event {
	result := projection.All()
	sort.Slice(result, func(i, j int) bool {
		leftDepth := len(projection.CausalAncestors(result[i].ID))
		rightDepth := len(projection.CausalAncestors(result[j].ID))
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return result[i].ID < result[j].ID
	})
	return []*gorapide.Event(result)
}

// MarshalCanonical returns a deterministic canonical poset containing only
// visible observations and the transitive reduction of causality between
// visible occurrences. Hidden intermediates affect the causal relation but do
// not appear as events or observations.
func (projection *Projection) MarshalCanonical() ([]byte, error) {
	ids := make([]gorapide.EventID, 0, len(projection.all))
	events := make(map[gorapide.EventID]*gorapide.Event, len(projection.all))
	for _, event := range projection.all {
		ids = append(ids, event.ID)
		events[event.ID] = event
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	representative := make(map[gorapide.EventID]gorapide.EventID, len(ids))
	classes := make(map[gorapide.EventID][]gorapide.EventID)
	for _, id := range ids {
		least := id
		for _, candidate := range ids {
			if candidate < least && sourceCausallyEquivalent(projection.source, id, candidate) {
				least = candidate
			}
		}
		representative[id] = least
		classes[least] = append(classes[least], id)
	}
	representatives := make([]gorapide.EventID, 0, len(classes))
	for id := range classes {
		representatives = append(representatives, id)
	}
	sort.Slice(representatives, func(i, j int) bool { return representatives[i] < representatives[j] })

	direct := make(map[gorapide.EventID]map[gorapide.EventID]bool, len(representatives))
	for _, from := range representatives {
		direct[from] = make(map[gorapide.EventID]bool)
		for _, to := range representatives {
			if from == to || !projection.source.IsCausallyBefore(from, to) {
				continue
			}
			immediate := true
			for _, middle := range representatives {
				if middle != from && middle != to &&
					projection.source.IsCausallyBefore(from, middle) &&
					projection.source.IsCausallyBefore(middle, to) {
					immediate = false
					break
				}
			}
			if immediate {
				direct[from][to] = true
			}
		}
	}

	depths := make(map[gorapide.EventID]uint64, len(representatives))
	computed := make(map[gorapide.EventID]bool, len(ids))
	var depth func(gorapide.EventID) uint64
	depth = func(id gorapide.EventID) uint64 {
		if computed[id] {
			return depths[id]
		}
		result := uint64(1)
		for _, predecessor := range representatives {
			if !direct[predecessor][id] {
				continue
			}
			candidate := depth(predecessor) + 1
			if candidate > result {
				result = candidate
			}
		}
		computed[id] = true
		depths[id] = result
		return result
	}

	viewsByID := make(map[gorapide.EventID]gorapide.EventSet, len(ids))
	for _, view := range projection.views {
		viewsByID[view.ID] = append(viewsByID[view.ID], view)
	}
	canonical := gorapide.CanonicalPoset{
		Format: gorapide.CanonicalPosetFormat,
		Events: make([]gorapide.CanonicalEvent, 0, len(ids)),
	}
	for _, classRepresentative := range representatives {
		members := classes[classRepresentative]
		if len(members) < 2 {
			continue
		}
		class := gorapide.CanonicalCausalEquivalenceClass{Members: make([]string, len(members))}
		for index, member := range members {
			class.Members[index] = string(member)
		}
		canonical.CausalEquivalences = append(canonical.CausalEquivalences, class)
	}
	timed := false
	for _, id := range ids {
		observations := make([]gorapide.CanonicalObservation, 0, len(viewsByID[id]))
		for _, view := range viewsByID[id] {
			params, err := gorapide.CanonicalizeParameters(view.Params)
			if err != nil {
				return nil, fmt.Errorf("projection event %s observation %s.%s: %w", id, view.Source, view.Name, err)
			}
			observations = append(observations, gorapide.CanonicalObservation{
				Name: view.Name, Source: view.Source, Params: params,
			})
		}
		timings, err := gorapide.EncodeCanonicalEventTimings(events[id].Timings)
		if err != nil {
			return nil, fmt.Errorf("projection event %s timing: %w", id, err)
		}
		if len(timings) != 0 {
			timed = true
		}
		canonical.Events = append(canonical.Events, gorapide.CanonicalEvent{
			ID: string(id), CausalDepth: depth(representative[id]), Observations: observations, Timings: timings,
		})
	}
	if len(canonical.CausalEquivalences) != 0 && timed {
		canonical.Format = gorapide.CanonicalTimedCausalPreorderFormat
	} else if len(canonical.CausalEquivalences) != 0 {
		canonical.Format = gorapide.CanonicalCausalPreorderFormat
	} else if timed {
		canonical.Format = gorapide.CanonicalTimedPosetFormat
	}
	for _, from := range representatives {
		for _, to := range representatives {
			if direct[from][to] {
				canonical.Edges = append(canonical.Edges, gorapide.CanonicalEdge{From: string(from), To: string(to)})
			}
		}
	}
	return json.Marshal(canonical)
}

// SemanticDigest identifies the exact visible computation represented by the
// projection, including inherited causality through hidden intermediates.
func (projection *Projection) SemanticDigest() (string, error) {
	encoded, err := projection.MarshalCanonical()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

var _ PosetReader = (*Projection)(nil)
