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
//  4. a match via a SECONDARY observation role (added via AddObservation),
//     or any match at all on a DETERMINISTIC event, is still a defensive
//     copy: mutating it must not affect a later query;
//  5. a match on a LEGACY (non-deterministic) event via its own PRIMARY
//     Name/Source role — including the synthesized fallback for an event
//     with no explicit Observations — returns the exact shared, frozen
//     stored pointer, not a copy.
//
// Round-2 baseline: this test was originally written against, and passed
// unmodified on, gorapide@v0.2.2 (see the perf-investigation-report.md
// contract-snapshot run in gorapide-fix2-report.md), pinning a single rule:
// "mutating a returned event's Params must never affect a later query" for
// EVERY match, with no exception. Round 3's controller ruling deliberately
// narrows that rule: EventsByName's legacy-primary-observation matches (via
// isPrimaryObservation in poset.go/EventsByName) are aligned with the
// aliasing norm Event()/Events() already use for legacy events
// (snapshotEvent returns the shared stored pointer for non-deterministic
// events, frozen-at-query-time, caller-must-not-mutate) instead of being
// cloned. Assertion 5 replaces the old "Bare" and "other" defensive-copy
// checks with pointer-identity checks reflecting that ruling; assertion 4
// keeps the original defensive-copy pin for every case the ruling does NOT
// touch (secondary roles, deterministic events).
func TestEventsByNameContract(t *testing.T) {
	p := NewPoset()

	// --- Multi-observation event: one occurrence, two SECONDARY roles
	// (multi's own Name is "Trigger", not "Match") both named "Match",
	// each with distinct Source/Params. Secondary-role matches are always
	// cloned views, regardless of the round-3 ruling.
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
	// to {e.Name, e.Source, e.Params}. That fallback is always the primary
	// role, so under the round-3 ruling a legacy event matching this way
	// is returned as the shared stored pointer.
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
	if bare.deterministic {
		t.Fatalf("test setup invariant broken: bare event must be a legacy (non-deterministic) event")
	}

	// --- A second "Match"-named legacy event whose OWN Name/Source is
	// "Match"/"zzz-source" (self-observation == primary role), to exercise
	// both sort ordering and the primary-match aliasing rule alongside
	// multi's secondary roles.
	other := NewEvent("Match", "zzz-source", map[string]any{"k": "z"})
	if err := p.AddEvent(other); err != nil {
		t.Fatalf("AddEvent(other): %v", err)
	}

	// --- A deterministic event matched via its own primary role. The
	// round-3 ruling explicitly excludes deterministic events from the
	// aliasing shortcut ("stay deep-cloned everywhere"), so this must
	// remain a defensive copy same as before.
	detEvent, err := NewDeterministicEvent(EventProvenance{
		Profile: "contract-test", Model: "contract-model",
		Instance: "component", Action: "DetMatch", Occurrence: "1",
	}, map[string]any{"seed": "det"})
	if err != nil {
		t.Fatalf("NewDeterministicEvent: %v", err)
	}
	if err := p.AddEvent(detEvent); err != nil {
		t.Fatalf("AddEvent(detEvent): %v", err)
	}

	// --- Assertion 1 + 3: EventsByName("Match") returns exactly the two
	// secondary roles of `multi` plus `other`'s primary self-observation,
	// sorted by (ID, Source, Name).
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
			if m == multi {
				t.Errorf("secondary-role match (Source=%q) must be a cloned view, not the shared multi pointer", m.Source)
			}
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
	// The third match is `other`'s primary self-observation: assertion 5
	// requires the exact shared stored pointer, not a clone with equal
	// values.
	foundOther := false
	for _, m := range matches {
		if m.ID == other.ID {
			foundOther = true
			if m != other {
				t.Errorf("primary-observation match for a legacy event must be the shared stored pointer: got a distinct *Event")
			}
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

	// --- Assertion 2 + 5: the observation-less "Bare" event matches via
	// its synthesized self-observation (always primary), and — being a
	// legacy event — that match is the exact shared stored pointer.
	bareMatches := p.EventsByName("Bare")
	if len(bareMatches) != 1 {
		t.Fatalf("EventsByName(Bare): got %d results, want 1", len(bareMatches))
	}
	if bareMatches[0] != bare {
		t.Fatalf("EventsByName(Bare)[0] must be the shared stored `bare` pointer (primary-observation aliasing), got a distinct *Event")
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
	// Same pointer identity across repeated queries too (both calls alias
	// the same underlying stored event).
	if second := p.EventsByName("Bare"); second[0] != bareMatches[0] {
		t.Errorf("EventsByName(Bare) primary-observation match should return the same pointer on every call")
	}

	// --- Assertion 4 (secondary-role case): defensive-copy pin for the
	// multi-observation path. Mutating a returned SECONDARY-role event's
	// Params must not affect a subsequent query's results.
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
				t.Fatalf("defensive-copy violated on multi-observation (secondary role) path: got %#v, want {k:a}", m.Params)
			}
		}
	}

	// --- Assertion 4 (deterministic-event case): a deterministic event's
	// primary-observation match must still be a defensive copy — the
	// round-3 aliasing ruling applies only to legacy events.
	detQuery1 := p.EventsByName("DetMatch")
	if len(detQuery1) != 1 {
		t.Fatalf("EventsByName(DetMatch): got %d results, want 1", len(detQuery1))
	}
	if detQuery1[0] == detEvent {
		t.Errorf("deterministic-event match must not alias the caller's original *Event")
	}
	detQuery1[0].Params["seed"] = "MUTATED"
	detQuery2 := p.EventsByName("DetMatch")
	if !reflect.DeepEqual(detQuery2[0].Params, map[string]any{"seed": "det"}) {
		t.Fatalf("defensive-copy violated for deterministic event's primary-observation match: got %#v, want {seed:det}", detQuery2[0].Params)
	}
	if detQuery1[0] == detQuery2[0] {
		t.Errorf("deterministic-event matches across separate EventsByName calls must not alias the same *Event")
	}
}
