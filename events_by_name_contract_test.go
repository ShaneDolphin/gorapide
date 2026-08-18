package gorapide

import (
	"reflect"
	"testing"
)

// TestEventsByNameContract pins the observable behavior of EventsByName that
// must survive the perf/events-by-name-hot-path fix untouched:
//
//  1. an event with multiple qualified observations appears once per
//     matching role, each view carrying that observation's Name/Source/
//     Params but the occurrence's shared ID;
//  2. an event with no explicit Observations still matches via its
//     synthesized {Name, Source, Params} self-observation;
//  3. results come back sorted exactly as sortEventSet orders them (ID,
//     then Source, then Name);
//  4. mutating a returned event's Params does not affect a later query
//     (defensive-copy pin — returned data must not alias stored state).
//
// This test is written against, and passes unmodified on, gorapide@v0.2.2
// (see the perf-investigation-report.md contract-snapshot run in the
// gorapide-fix2-report.md deliverable). It must also pass after the
// eventView/cloneEvent/EventsByName rewrite.
func TestEventsByNameContract(t *testing.T) {
	p := NewPoset()

	// --- Multi-observation event: one occurrence, two roles both named
	// "Match", each with distinct Source/Params.
	multi := NewEvent("Trigger", "origin", map[string]any{"seed": "trigger"})
	if err := p.AddEvent(multi); err != nil {
		t.Fatalf("AddEvent(multi): %v", err)
	}
	if _, err := p.AddObservation(multi.ID, EventObservation{
		Name: "Match", Source: "roleA", Params: map[string]any{"k": "a"},
	}); err != nil {
		t.Fatalf("AddObservation(roleA): %v", err)
	}
	if _, err := p.AddObservation(multi.ID, EventObservation{
		Name: "Match", Source: "roleB", Params: map[string]any{"k": "b"},
	}); err != nil {
		t.Fatalf("AddObservation(roleB): %v", err)
	}

	// --- Observation-less event: added by hand (bypassing NewEvent's
	// self-observation seeding) so e.Observations is empty and the "Bare"
	// match can only come from the EventObservations/EventsByName fallback
	// to {e.Name, e.Source, e.Params}.
	bare := &Event{
		ID:     NewEventID(),
		Name:   "Bare",
		Source: "bare-source",
		Params: map[string]any{"note": "fallback"},
	}
	if err := p.AddEvent(bare); err != nil {
		t.Fatalf("AddEvent(bare): %v", err)
	}
	if len(bare.Observations) != 0 {
		t.Fatalf("test setup invariant broken: bare event has Observations = %#v, want empty", bare.Observations)
	}

	// --- A second "Match"-named bare-self-observation event, to exercise
	// sort ordering (its ID relative to multi's determines the expected
	// order in the "Match" query below).
	other := NewEvent("Match", "zzz-source", map[string]any{"k": "z"})
	if err := p.AddEvent(other); err != nil {
		t.Fatalf("AddEvent(other): %v", err)
	}

	// --- Assertion 1 + 3: EventsByName("Match") returns exactly the two
	// roles of `multi` plus `other`'s self-observation, sorted by
	// (ID, Source, Name).
	matches := p.EventsByName("Match")
	if len(matches) != 3 {
		t.Fatalf("EventsByName(Match): got %d results, want 3: %#v", len(matches), matches)
	}
	for _, m := range matches {
		if m.Name != "Match" {
			t.Errorf("EventsByName(Match) returned event with Name=%q", m.Name)
		}
	}
	// Every "Match" view of `multi` carries multi's shared ID.
	multiViews := 0
	seenSources := map[string]map[string]any{}
	for _, m := range matches {
		if m.ID == multi.ID {
			multiViews++
			seenSources[m.Source] = m.Params
		}
	}
	if multiViews != 2 {
		t.Fatalf("expected 2 views of multi's ID in EventsByName(Match), got %d", multiViews)
	}
	if !reflect.DeepEqual(seenSources["roleA"], map[string]any{"k": "a"}) {
		t.Errorf("roleA view Params = %#v, want {k:a}", seenSources["roleA"])
	}
	if !reflect.DeepEqual(seenSources["roleB"], map[string]any{"k": "b"}) {
		t.Errorf("roleB view Params = %#v, want {k:b}", seenSources["roleB"])
	}
	// The third match is `other`'s self-observation.
	foundOther := false
	for _, m := range matches {
		if m.ID == other.ID {
			foundOther = true
			if m.Source != "zzz-source" {
				t.Errorf("other view Source = %q, want zzz-source", m.Source)
			}
			if !reflect.DeepEqual(m.Params, map[string]any{"k": "z"}) {
				t.Errorf("other view Params = %#v, want {k:z}", m.Params)
			}
		}
	}
	if !foundOther {
		t.Fatalf("EventsByName(Match) missing other's self-observation view")
	}
	// Exact sort order per sortEventSet: (ID, Source, Name).
	for i := 1; i < len(matches); i++ {
		prev, cur := matches[i-1], matches[i]
		less := prev.ID < cur.ID ||
			(prev.ID == cur.ID && prev.Source < cur.Source) ||
			(prev.ID == cur.ID && prev.Source == cur.Source && prev.Name <= cur.Name)
		if !less {
			t.Errorf("EventsByName(Match) not sorted at index %d: %+v then %+v", i, prev, cur)
		}
	}

	// --- Assertion 2: the observation-less "Bare" event matches via its
	// synthesized self-observation.
	bareMatches := p.EventsByName("Bare")
	if len(bareMatches) != 1 {
		t.Fatalf("EventsByName(Bare): got %d results, want 1", len(bareMatches))
	}
	if bareMatches[0].ID != bare.ID {
		t.Errorf("EventsByName(Bare)[0].ID = %s, want %s", bareMatches[0].ID, bare.ID)
	}
	if bareMatches[0].Source != "bare-source" {
		t.Errorf("EventsByName(Bare)[0].Source = %q, want bare-source", bareMatches[0].Source)
	}
	if !reflect.DeepEqual(bareMatches[0].Params, map[string]any{"note": "fallback"}) {
		t.Errorf("EventsByName(Bare)[0].Params = %#v, want {note:fallback}", bareMatches[0].Params)
	}

	// --- Assertion 4: defensive-copy pin. Mutating a returned event's
	// Params must not affect a subsequent query's results.
	firstQuery := p.EventsByName("Bare")
	firstQuery[0].Params["note"] = "MUTATED"
	firstQuery[0].Params["injected"] = "also-mutated"
	secondQuery := p.EventsByName("Bare")
	if !reflect.DeepEqual(secondQuery[0].Params, map[string]any{"note": "fallback"}) {
		t.Fatalf("defensive-copy violated: mutating a returned Params leaked into a later query, got %#v", secondQuery[0].Params)
	}

	// Same pin for the multi-observation match path.
	matchQuery1 := p.EventsByName("Match")
	for _, m := range matchQuery1 {
		if m.ID == multi.ID && m.Source == "roleA" {
			m.Params["k"] = "MUTATED"
		}
	}
	matchQuery2 := p.EventsByName("Match")
	for _, m := range matchQuery2 {
		if m.ID == multi.ID && m.Source == "roleA" {
			if !reflect.DeepEqual(m.Params, map[string]any{"k": "a"}) {
				t.Fatalf("defensive-copy violated on multi-observation path: got %#v, want {k:a}", m.Params)
			}
		}
	}
}
