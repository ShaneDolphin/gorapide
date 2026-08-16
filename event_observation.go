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

func eventView(e *Event, observation EventObservation) *Event {
	view := *cloneEvent(e)
	view.Name = observation.Name
	view.Source = observation.Source
	view.Params = copyParams(observation.Params)
	view.Observations = copyObservations(e.Observations)
	return &view
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
		p.events[id] = &updated
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
	p.events[id] = &updated
	return eventView(&updated, observation), nil
}
