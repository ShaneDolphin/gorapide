package gorapide

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
)

var (
	// ErrObservationConflict means the same qualified action role was assigned
	// incompatible parameter values for one event occurrence.
	ErrObservationConflict = errors.New("event observation conflicts with existing role")
)

// EventObservation is one qualified interface/action view of an event
// occurrence. Basic Rapide connections add observations to an occurrence;
// they do not add new occurrences to the poset.
type EventObservation struct {
	Name   string         `json:"name"`
	Source string         `json:"source"`
	Params map[string]any `json:"params"`
}

func copyParams(params map[string]any) map[string]any {
	if params == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(params))
	for key, value := range params {
		result[key] = copyParamValue(value)
	}
	return result
}

func copyParamValue(value any) any {
	switch value := value.(type) {
	case []byte:
		return append([]byte(nil), value...)
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = copyParamValue(item)
		}
		return result
	case map[string]any:
		return copyParams(value)
	default:
		return value
	}
}

func copyObservations(observations []EventObservation) []EventObservation {
	result := make([]EventObservation, len(observations))
	for i, observation := range observations {
		result[i] = EventObservation{
			Name:   observation.Name,
			Source: observation.Source,
			Params: copyParams(observation.Params),
		}
	}
	return result
}

func observationLess(a, b EventObservation) bool {
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	return a.Name < b.Name
}

// EventObservations returns a defensive, canonically ordered copy of all
// qualified action roles known for this occurrence.
func (e *Event) EventObservations() []EventObservation {
	observations := copyObservations(e.Observations)
	if len(observations) == 0 {
		observations = []EventObservation{{
			Name: e.Name, Source: e.Source, Params: copyParams(e.Params),
		}}
	}
	sort.Slice(observations, func(i, j int) bool {
		return observationLess(observations[i], observations[j])
	})
	return observations
}

// observationsNamed reports the qualified observations of e whose Name
// equals name, applying the exact same membership test as
// EventObservations (including the synthesized {e.Name, e.Source, e.Params}
// self-observation fallback for events with no explicit Observations) but
// without copying any Params map or sorting. The returned entries alias e's
// stored Params/Observations maps and are for read-only membership testing
// only: callers that need an isolated, caller-owned result (e.g. to return
// to code outside the poset) must still build one explicitly, such as via
// eventView. This exists solely to let EventsByName test names cheaply
// without paying for a copy-and-sort of every scanned event, match or not.
func (e *Event) observationsNamed(name string) []EventObservation {
	if len(e.Observations) == 0 {
		if e.Name != name {
			return nil
		}
		return []EventObservation{{Name: e.Name, Source: e.Source, Params: e.Params}}
	}
	var matches []EventObservation
	for _, observation := range e.Observations {
		if observation.Name == name {
			matches = append(matches, observation)
		}
	}
	return matches
}

// ObservationViews returns one isolated active event view for every qualified
// role of this occurrence, in canonical order. All views retain the same event
// identity and causal relationships.
func (e *Event) ObservationViews() EventSet {
	if e == nil {
		return nil
	}
	observations := e.EventObservations()
	views := make(EventSet, len(observations))
	for index, observation := range observations {
		views[index] = eventView(e, observation)
	}
	return views
}

// HasObservation reports whether this occurrence is visible through the
// specified qualified action role.
func (e *Event) HasObservation(source, name string) bool {
	for _, observation := range e.EventObservations() {
		if observation.Source == source && observation.Name == name {
			return true
		}
	}
	return false
}

// eventView builds an isolated, defensively-copied view of e with the given
// observation active. It intentionally does not call cloneEvent: cloneEvent
// unconditionally deep-copies e.Params and e.Observations, both of which
// eventView immediately overwrites, so routing through it would compute and
// discard a full Params/Observations copy on every call. Every field set
// here mirrors what cloneEvent would have produced, minus that dead work.
//
// observation.Params is deep-copied once, directly, into the returned
// view's Params. When e.Observations is non-empty, observation is one of
// its entries by value (observationsNamed/EventObservations both alias the
// stored Observations' Params maps rather than copying them), so
// copyObservationsSkipping reuses that same copy for the matching entry in
// view.Observations instead of deep-copying the identical map a second
// time.
//
// Disclosed aliasing: as a direct consequence, view.Params and
// view.Observations[i].Params — for whichever i is the matched role — are
// the SAME map object within one returned *Event, not two independent
// copies of equal content (which is what v0.2.2 always allocated, and
// what round 3 of this branch originally, incorrectly, claimed was
// unchanged). Mutating one through the returned pointer mutates the
// other. This aliasing is confined to the one returned view: it never
// reaches back into the poset's stored state, another view's fields, or
// any other event, so it does not violate the defensive-copy guarantee
// query results give the caller relative to the poset — but a caller
// that mutates a returned view (already outside the query contract,
// which promises frozen-at-query-time snapshots, not mutable objects)
// must be aware Params and the matching Observations entry are not
// independent inside that one object. Pinned by
// TestEventsByNameContract's intra-view aliasing assertion.
func eventView(e *Event, observation EventObservation) *Event {
	view := *e
	view.Name = observation.Name
	view.Source = observation.Source
	viewParams := copyParams(observation.Params)
	view.Params = viewParams
	view.Observations = copyObservationsSkipping(e.Observations, observation.Params, viewParams)
	view.Timings = append([]EventTiming(nil), e.Timings...)
	view.expectedCauses = append([]EventID(nil), e.expectedCauses...)
	if e.Clock.Vector != nil {
		view.Clock.Vector = e.Clock.Vector.Clone()
	}
	return &view
}

// copyObservationsSkipping copies observations exactly like copyObservations,
// except that an entry whose Params map is literally the same map (by
// identity, not just content) as alreadyCopiedFrom reuses alreadyCopiedTo
// instead of computing a second, redundant deep copy of it. Called only
// from eventView, with alreadyCopiedTo set to the view's own already-copied
// Params map — so the reused entry's Params and the view's own Params end
// up being the same map object, not independent copies. See eventView's
// "Disclosed aliasing" note for the full explanation and its bound.
func copyObservationsSkipping(observations []EventObservation, alreadyCopiedFrom, alreadyCopiedTo map[string]any) []EventObservation {
	result := make([]EventObservation, len(observations))
	for i, observation := range observations {
		if sameParamsMap(observation.Params, alreadyCopiedFrom) {
			result[i] = EventObservation{
				Name:   observation.Name,
				Source: observation.Source,
				Params: alreadyCopiedTo,
			}
			continue
		}
		result[i] = EventObservation{
			Name:   observation.Name,
			Source: observation.Source,
			Params: copyParams(observation.Params),
		}
	}
	return result
}

// sameParamsMap reports whether a and b are literally the same map value
// (identity), not merely equal in content. Two nil maps are never treated
// as the same: copyParams(nil) allocates a fresh empty map on every call,
// and conflating separate nils here would alias two logically distinct
// empty maps into the same object in the returned view — exactly the kind
// of aliasing hazard this identity check exists to avoid introducing.
func sameParamsMap(a, b map[string]any) bool {
	if a == nil || b == nil {
		return false
	}
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// isPrimaryObservation reports whether observation is e's own active
// Name/Source role, i.e. the role EventsByName's fallback synthesizes for
// an event with no explicit Observations, or the entry NewEvent /
// NewDeterministicEvent seed at construction otherwise. e.Name/e.Source
// never change after AddEvent (AddObservationWithTimings only appends to
// Observations and updates Timings), and AddObservationWithTimings rejects
// any second stored observation sharing a (Name, Source) pair with
// different Params (ErrObservationConflict), so at most one stored
// observation can ever carry e's own (Name, Source) — this equality test
// is therefore a stable, unambiguous identification of the primary role
// for the entire life of the event.
func (e *Event) isPrimaryObservation(observation EventObservation) bool {
	return observation.Name == e.Name && observation.Source == e.Source
}

func cloneEvent(e *Event) *Event {
	if e == nil {
		return nil
	}
	clone := *e
	clone.Params = copyParams(e.Params)
	clone.Observations = copyObservations(e.Observations)
	clone.Timings = append([]EventTiming(nil), e.Timings...)
	clone.expectedCauses = append([]EventID(nil), e.expectedCauses...)
	if e.Clock.Vector != nil {
		clone.Clock.Vector = e.Clock.Vector.Clone()
	}
	return &clone
}

func snapshotEvent(e *Event) *Event {
	if e != nil && e.deterministic {
		return cloneEvent(e)
	}
	return e
}

// Snapshot returns a deep defensive copy of the event and its parameter,
// observation, cause, and vector-clock state.
func (e *Event) Snapshot() *Event {
	return cloneEvent(e)
}

// AddObservation makes an existing event occurrence visible through another
// qualified action role and returns a view with that role active. The poset's
// event count and causal relation are unchanged.
func (p *Poset) AddObservation(id EventID, observation EventObservation) (*Event, error) {
	return p.AddObservationWithTimings(id, observation, nil)
}

// AddObservationWithTimings adds a basic-connection observation and any
// newly discovered local-clock relationships atomically. Existing timing
// relationships must agree. Adding a relationship is validated against the
// occurrence's complete causal past and future before the frozen stored event
// is replaced.
func (p *Poset) AddObservationWithTimings(id EventID, observation EventObservation, timings []EventTiming) (*Event, error) {
	if observation.Name == "" {
		return nil, fmt.Errorf("gorapide.Poset.AddObservationWithTimings: observation name is empty")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	event, ok := p.events[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrEventNotFound, id)
	}

	mergedTimings := append([]EventTiming(nil), event.Timings...)
	for _, timing := range timings {
		existing, found := event.Timing(timing.Clock)
		if found {
			if existing != timing {
				return nil, fmt.Errorf("%w: event %s has conflicting interval on clock %q", ErrInvalidEventTiming, id, timing.Clock)
			}
			continue
		}
		mergedTimings = append(mergedTimings, timing)
	}
	mergedTimings, err := CanonicalizeEventTimings(mergedTimings)
	if err != nil {
		return nil, err
	}
	candidate := cloneEvent(event)
	candidate.Timings = mergedTimings
	for _, predecessor := range p.causalPredecessorIDsLocked(id) {
		if predecessor != id {
			if err := sharedTimingConflict(p.events[predecessor], candidate); err != nil {
				return nil, err
			}
		}
	}
	for _, successor := range p.causalSuccessorIDsLocked(id) {
		if successor != id {
			if err := sharedTimingConflict(candidate, p.events[successor]); err != nil {
				return nil, err
			}
		}
	}

	observation.Params = copyParams(observation.Params)
	observations := event.EventObservations()
	for _, existing := range observations {
		if existing.Source != observation.Source || existing.Name != observation.Name {
			continue
		}
		if !reflect.DeepEqual(existing.Params, observation.Params) {
			return nil, fmt.Errorf("%w: %s.%s on %s", ErrObservationConflict,
				observation.Source, observation.Name, id)
		}
		updated := *event
		updated.Timings = mergedTimings
		updated.Freeze()
		p.storeEventLocked(&updated)
		return eventView(&updated, existing), nil
	}

	observations = append(observations, observation)
	sort.Slice(observations, func(i, j int) bool {
		return observationLess(observations[i], observations[j])
	})

	updated := *event
	updated.Observations = observations
	updated.Timings = mergedTimings
	updated.Freeze()
	p.storeEventLocked(&updated)
	return eventView(&updated, observation), nil
}
