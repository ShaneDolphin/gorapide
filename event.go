package gorapide

import (
	"crypto/rand"
	"fmt"
	"sort"
	"strings"
	"time"
)

// EventID is a unique identifier for an event, represented as a UUID string.
type EventID string

// NewEventID generates a new random UUID-based EventID using crypto/rand.
//
// Deprecated: random identities are outside the deterministic trusted core.
// Use NewDeterministicEvent with explicit semantic provenance for replayable
// computations.
func NewEventID() EventID {
	var uuid [16]byte
	_, err := rand.Read(uuid[:])
	if err != nil {
		panic(fmt.Sprintf("gorapide: failed to generate EventID: %v", err))
	}
	// Set version 4 and variant bits per RFC 4122.
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return EventID(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16]))
}

// Short returns a truncated form of the EventID for display purposes.
func (id EventID) Short() string {
	s := string(id)
	if len(s) >= 8 {
		return s[:8]
	}
	return s
}

// ClockStamp holds both logical and physical time for an event.
type ClockStamp struct {
	Lamport  uint64      // Logical Lamport timestamp for causal ordering
	WallTime time.Time   // Wall clock time for temporal ordering
	Vector   VectorClock // Optional vector clock for distributed mode (nil = single-node)
}

// Before reports whether this ClockStamp is causally before other,
// using Lamport ordering.
func (c ClockStamp) Before(other ClockStamp) bool {
	return c.Lamport < other.Lamport
}

// Event is the atomic unit of the gorapide system.
// An Event is an immutable, uniquely identifiable tuple of values
// that exists within causal and temporal ordering relations.
type Event struct {
	ID     EventID
	Name   string
	Params map[string]any
	Clock  ClockStamp
	Source string
	// Timings are start/finish intervals on named Rapide simulation clocks.
	// They are distinct from legacy Clock.WallTime and Clock.Lamport metadata.
	Timings []EventTiming
	// Observations records every qualified action role through which this event
	// is visible. A Rapide basic connection can make one event occurrence visible
	// under another component/action name without creating a second occurrence.
	// Name, Source, and Params are the active observation for legacy callers.
	Observations []EventObservation
	Immutable    bool

	expectedCauses []EventID // deterministic provenance invariant
	deterministic  bool
}

// NewEvent creates a new Event with an auto-generated ID and WallTime set to now.
// Lamport starts at 0.
//
// Deprecated: this constructor belongs to the legacy/integration surface. Use
// NewDeterministicEvent for events that contribute to model execution, replay,
// canonical artifacts, or semantic digests.
func NewEvent(name string, source string, params map[string]any) *Event {
	p := copyParams(params)
	return &Event{
		ID:     NewEventID(),
		Name:   name,
		Source: source,
		Params: p,
		Clock: ClockStamp{
			Lamport:  0,
			WallTime: time.Now(),
		},
		Observations: []EventObservation{{Name: name, Source: source, Params: copyParams(p)}},
	}
}

// Param retrieves a parameter value by key. Returns the value and whether
// the key was present.
func (e *Event) Param(key string) (any, bool) {
	v, ok := e.Params[key]
	return v, ok
}

// ParamString returns the string value of a parameter, or "" if the key
// is missing or not a string.
func (e *Event) ParamString(key string) string {
	v, ok := e.Params[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// ParamInt returns the int value of a parameter, or 0 if the key
// is missing or not an int.
func (e *Event) ParamInt(key string) int {
	v, ok := e.Params[key]
	if !ok {
		return 0
	}
	switch i := v.(type) {
	case int:
		return i
	case int8:
		return int(i)
	case int16:
		return int(i)
	case int32:
		return int(i)
	case int64:
		return int(i)
	default:
		return 0
	}
}

// String returns a human-readable representation of the event.
// Format: "EventName(key=val, ...) @source [id:short]"
func (e *Event) String() string {
	var parts []string
	keys := make([]string, 0, len(e.Params))
	for k := range e.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, e.Params[k]))
	}
	paramStr := strings.Join(parts, ", ")
	return fmt.Sprintf("%s(%s) @%s [id:%s]", e.Name, paramStr, e.Source, e.ID.Short())
}

// Freeze marks the event as immutable. Once frozen, the Params map should not
// be modified. Freeze replaces the internal Params map with a defensive copy
// to discourage further mutation.
func (e *Event) Freeze() {
	e.Immutable = true
	e.Params = copyParams(e.Params)
	e.Observations = copyObservations(e.Observations)
	e.Timings = append([]EventTiming(nil), e.Timings...)
}

// EventSet is an ordered collection of events with helper methods.
type EventSet []*Event

// Contains reports whether the set contains an event with the given ID.
func (es EventSet) Contains(id EventID) bool {
	for _, e := range es {
		if e.ID == id {
			return true
		}
	}
	return false
}

// IDs returns the EventIDs of all events in the set.
func (es EventSet) IDs() []EventID {
	ids := make([]EventID, len(es))
	for i, e := range es {
		ids[i] = e.ID
	}
	return ids
}

// Filter returns a new EventSet containing only events for which fn returns true.
func (es EventSet) Filter(fn func(*Event) bool) EventSet {
	var result EventSet
	for _, e := range es {
		if fn(e) {
			result = append(result, e)
		}
	}
	return result
}

// Names returns the names of all events in the set.
func (es EventSet) Names() []string {
	names := make([]string, len(es))
	for i, e := range es {
		names[i] = e.Name
	}
	return names
}
