package gorapide

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDeterministicTimedEventIdentityCanonicalizesLocalClocks(t *testing.T) {
	provenance := EventProvenance{
		Profile: "rapide-1.0", Model: "model", Instance: "component",
		Action: "A", Occurrence: "one",
		Timings: []EventTiming{{Clock: "slow", Start: 2, Finish: 4}, {Clock: "fast", Start: 10, Finish: 10}},
	}
	left, err := NewDeterministicEvent(provenance, map[string]any{"value": 1})
	if err != nil {
		t.Fatal(err)
	}
	provenance.Timings[0], provenance.Timings[1] = provenance.Timings[1], provenance.Timings[0]
	right, err := NewDeterministicEvent(provenance, map[string]any{"value": int64(1)})
	if err != nil {
		t.Fatal(err)
	}
	if left.ID != right.ID || !strings.HasPrefix(string(left.ID), "evt2-") {
		t.Fatalf("timing declaration order changed identity: %s != %s", left.ID, right.ID)
	}
	if !left.Clock.WallTime.IsZero() {
		t.Fatal("deterministic Rapide timing consulted wall time")
	}
	changed := provenance
	changed.Timings = append([]EventTiming(nil), provenance.Timings...)
	changed.Timings[0].Finish++
	different, err := NewDeterministicEvent(changed, map[string]any{"value": 1})
	if err != nil {
		t.Fatal(err)
	}
	if different.ID == left.ID {
		t.Fatal("different Rapide timing produced the same event identity")
	}
	untimed := provenance
	untimed.Timings = nil
	legacy, err := NewDeterministicEvent(untimed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(legacy.ID), "evt1-") {
		t.Fatalf("untimed identity version changed: %s", legacy.ID)
	}
}

func TestCanonicalTimedPosetRoundTripsAndKeepsUntimedV1Stable(t *testing.T) {
	poset := NewPoset()
	first := timedTestEvent(t, "A", "a", []EventTiming{{Clock: "z", Start: 1, Finish: 2}, {Clock: "a", Start: ^uint64(0), Finish: ^uint64(0)}})
	second := timedTestEvent(t, "B", "b", []EventTiming{{Clock: "z", Start: 2, Finish: 5}})
	if err := poset.AddEvent(first); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(second); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddCausal(first.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	encoded, err := poset.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"format":"`+CanonicalTimedPosetFormat+`"`)) {
		t.Fatalf("timed poset did not select v2: %s", encoded)
	}
	parsed, err := ParseCanonicalPoset(encoded)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := parsed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, roundTrip) {
		t.Fatal("timed canonical round trip changed bytes")
	}
	parsedFirst := parsed.ByName("A")[0]
	if parsedFirst.Timings[0].Clock != "a" || parsedFirst.Timings[1].Clock != "z" {
		t.Fatalf("timings are not canonical: %#v", parsedFirst.Timings)
	}
	start, err := parsedFirst.StartTime("z")
	if err != nil || start != 1 {
		t.Fatalf("Start(z) = %d, %v", start, err)
	}
	finish, err := parsedFirst.FinishTime("z")
	if err != nil || finish != 2 {
		t.Fatalf("Finish(z) = %d, %v", finish, err)
	}
	length, err := parsedFirst.Length("z")
	if err != nil || length != 1 {
		t.Fatalf("Length(z) = %d, %v", length, err)
	}
	if _, err := parsedFirst.StartTime("missing"); !errors.Is(err, ErrTimingNotRelated) {
		t.Fatalf("got %v, want Timing_Error equivalent", err)
	}

	untimed := NewPoset()
	if err := untimed.AddEvent(timedTestEvent(t, "U", "u", nil)); err != nil {
		t.Fatal(err)
	}
	untimedBytes, err := untimed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(untimedBytes, []byte(`"format":"`+CanonicalPosetFormat+`"`)) || bytes.Contains(untimedBytes, []byte(`"timings"`)) {
		t.Fatalf("untimed v1 encoding changed: %s", untimedBytes)
	}
}

func TestCausalOrderRequiresFinishBeforeStartOnEverySharedClock(t *testing.T) {
	t.Run("valid boundary", func(t *testing.T) {
		poset := NewPoset()
		before := timedTestEvent(t, "Before", "before", []EventTiming{{Clock: "C", Start: 1, Finish: 3}})
		after := timedTestEvent(t, "After", "after", []EventTiming{{Clock: "C", Start: 3, Finish: 4}})
		if err := poset.AddEvent(before); err != nil {
			t.Fatal(err)
		}
		if err := poset.AddEvent(after); err != nil {
			t.Fatal(err)
		}
		if err := poset.AddCausal(before.ID, after.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("direct conflict", func(t *testing.T) {
		poset := NewPoset()
		before := timedTestEvent(t, "Before", "before", []EventTiming{{Clock: "C", Start: 1, Finish: 5}})
		after := timedTestEvent(t, "After", "after", []EventTiming{{Clock: "C", Start: 4, Finish: 6}})
		if err := poset.AddEvent(before); err != nil {
			t.Fatal(err)
		}
		if err := poset.AddEvent(after); err != nil {
			t.Fatal(err)
		}
		if err := poset.AddCausal(before.ID, after.ID); !errors.Is(err, ErrTimingCausality) {
			t.Fatalf("got %v, want timing causality failure", err)
		}
		if poset.IsCausallyBefore(before.ID, after.ID) {
			t.Fatal("invalid timing edge partially mutated the poset")
		}
	})

	t.Run("transitive hidden clock gap", func(t *testing.T) {
		poset := NewPoset()
		before := timedTestEvent(t, "Before", "before", []EventTiming{{Clock: "C", Start: 0, Finish: 10}})
		hidden := timedTestEvent(t, "Hidden", "hidden", nil)
		after := timedTestEvent(t, "After", "after", []EventTiming{{Clock: "C", Start: 5, Finish: 5}})
		for _, event := range []*Event{before, hidden, after} {
			if err := poset.AddEvent(event); err != nil {
				t.Fatal(err)
			}
		}
		if err := poset.AddCausal(before.ID, hidden.ID); err != nil {
			t.Fatal(err)
		}
		if err := poset.AddCausal(hidden.ID, after.ID); !errors.Is(err, ErrTimingCausality) {
			t.Fatalf("got %v, want transitive timing causality failure", err)
		}
		if poset.IsCausallyBefore(hidden.ID, after.ID) {
			t.Fatal("invalid transitive timing edge partially mutated the poset")
		}
	})

	t.Run("transitive conflict before insertion", func(t *testing.T) {
		poset := NewPoset()
		before := timedTestEvent(t, "Before", "before", []EventTiming{{Clock: "C", Start: 0, Finish: 10}})
		hidden := timedTestEvent(t, "Hidden", "hidden", nil)
		after := timedTestEvent(t, "After", "after", []EventTiming{{Clock: "C", Start: 5, Finish: 5}})
		if err := poset.AddEvent(before); err != nil {
			t.Fatal(err)
		}
		if err := poset.AddEventWithCause(hidden, before.ID); err != nil {
			t.Fatal(err)
		}
		if err := poset.AddEventWithCause(after, hidden.ID); !errors.Is(err, ErrTimingCausality) {
			t.Fatalf("got %v, want transitive timing causality failure", err)
		}
		if _, exists := poset.Event(after.ID); exists {
			t.Fatal("invalid timed event was inserted before its cause was rejected")
		}
	})

	t.Run("independent clocks", func(t *testing.T) {
		poset := NewPoset()
		before := timedTestEvent(t, "Before", "before", []EventTiming{{Clock: "C1", Start: 0, Finish: 100}})
		after := timedTestEvent(t, "After", "after", []EventTiming{{Clock: "C2", Start: 0, Finish: 0}})
		if err := poset.AddEvent(before); err != nil {
			t.Fatal(err)
		}
		if err := poset.AddEvent(after); err != nil {
			t.Fatal(err)
		}
		if err := poset.AddCausal(before.ID, after.ID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTimedEventAndCanonicalParserRejectMalformedTiming(t *testing.T) {
	invalid := []EventTiming{{Clock: "C", Start: 3, Finish: 2}}
	if _, err := NewDeterministicEvent(EventProvenance{
		Profile: "profile", Instance: "component", Action: "A", Occurrence: "one", Timings: invalid,
	}, nil); !errors.Is(err, ErrInvalidEventTiming) {
		t.Fatalf("got %v, want invalid interval", err)
	}
	duplicate := &Event{ID: "duplicate", Name: "A", Source: "component", Timings: []EventTiming{{Clock: "C"}, {Clock: "C"}}}
	if err := NewPoset().AddEvent(duplicate); !errors.Is(err, ErrInvalidEventTiming) {
		t.Fatalf("got %v, want duplicate clock rejection", err)
	}

	poset := NewPoset()
	if err := poset.AddEvent(timedTestEvent(t, "A", "a", []EventTiming{{Clock: "C", Start: 1, Finish: 1}})); err != nil {
		t.Fatal(err)
	}
	encoded, err := poset.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	v1WithTiming := bytes.Replace(encoded, []byte(CanonicalTimedPosetFormat), []byte(CanonicalPosetFormat), 1)
	if _, err := ParseCanonicalPoset(v1WithTiming); !errors.Is(err, ErrInvalidCanonicalPoset) {
		t.Fatalf("got %v, want v1 timing rejection", err)
	}
	untimed := NewPoset()
	if err := untimed.AddEvent(timedTestEvent(t, "U", "u", nil)); err != nil {
		t.Fatal(err)
	}
	untimedBytes, err := untimed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	v2WithoutTiming := bytes.Replace(untimedBytes, []byte(CanonicalPosetFormat), []byte(CanonicalTimedPosetFormat), 1)
	if _, err := ParseCanonicalPoset(v2WithoutTiming); !errors.Is(err, ErrInvalidCanonicalPoset) {
		t.Fatalf("got %v, want empty v2 timing rejection", err)
	}
	nonCanonicalTick := bytes.Replace(encoded, []byte(`"start":"1"`), []byte(`"start":"01"`), 1)
	if _, err := ParseCanonicalPoset(nonCanonicalTick); !errors.Is(err, ErrInvalidCanonicalPoset) {
		t.Fatalf("got %v, want noncanonical tick rejection", err)
	}
	canonical, err := poset.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	canonical.Events[0].Timings = []CanonicalEventTiming{{Clock: "z", Start: "0", Finish: "0"}, {Clock: "a", Start: "0", Finish: "0"}}
	unsorted, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCanonicalPoset(unsorted); !errors.Is(err, ErrInvalidCanonicalPoset) {
		t.Fatalf("got %v, want unsorted timing rejection", err)
	}

	conflicting := CanonicalPoset{
		Format: CanonicalTimedPosetFormat,
		Events: []CanonicalEvent{
			{ID: "a", CausalDepth: 1, Observations: []CanonicalObservation{{Name: "A", Source: "component", Params: []CanonicalParameter{}}}, Timings: []CanonicalEventTiming{{Clock: "C", Start: "0", Finish: "10"}}},
			{ID: "b", CausalDepth: 2, Observations: []CanonicalObservation{{Name: "B", Source: "component", Params: []CanonicalParameter{}}}, Timings: []CanonicalEventTiming{{Clock: "C", Start: "5", Finish: "5"}}},
		},
		Edges: []CanonicalEdge{{From: "a", To: "b"}},
	}
	conflictingBytes, err := json.Marshal(conflicting)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCanonicalPoset(conflictingBytes); !errors.Is(err, ErrInvalidCanonicalPoset) {
		t.Fatalf("got %v, want timing/causal conflict rejection", err)
	}
}

func TestLegacyJSONRoundTripPreservesRapideTiming(t *testing.T) {
	poset := NewPoset()
	event := timedTestEvent(t, "A", "a", []EventTiming{{Clock: "C", Start: 7, Finish: 9}})
	if err := poset.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(poset)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Poset
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	got := decoded.ByName("A")
	if len(got) != 1 || len(got[0].Timings) != 1 || got[0].Timings[0] != event.Timings[0] {
		t.Fatalf("legacy JSON lost Rapide timing: %#v", got)
	}
}

func TestAddObservationWithTimingsValidatesCausalClosureAtomically(t *testing.T) {
	poset := NewPoset()
	before := timedTestEvent(t, "Before", "before", nil)
	if err := poset.AddEvent(before); err != nil {
		t.Fatal(err)
	}
	after, err := NewDeterministicEvent(EventProvenance{
		Profile: "test-profile", Model: "test-model", Instance: "component",
		Action: "After", Occurrence: "after", Causes: []EventID{before.ID},
		Timings: []EventTiming{{Clock: "target.C", Start: 5, Finish: 5}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(after, before.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := poset.AddObservationWithTimings(before.ID, EventObservation{
		Name: "Observed", Source: "target", Params: map[string]any{},
	}, []EventTiming{{Clock: "target.C", Start: 10, Finish: 10}}); !errors.Is(err, ErrTimingCausality) {
		t.Fatalf("got %v, want causal timing rejection", err)
	}
	stored, ok := poset.Event(before.ID)
	if !ok {
		t.Fatal("before event disappeared")
	}
	if _, related := stored.Timing("target.C"); related || stored.HasObservation("target", "Observed") {
		t.Fatalf("rejected observation partially mutated occurrence: %#v", stored)
	}
}

// --- Timing-closure fast-path counter staleness ---
//
// AddEventWithCause's local check and addCausalLocked's poset-wide
// timedEvents count both let an untimed-only poset skip timing-closure
// validation entirely. These tests build a poset while it is fully untimed
// (both fast paths active, timedEvents == 0), then introduce a timing
// through one specific entry path, then force a slow-path scan and confirm
// a genuine conflict is still caught. A stale counter that failed to track
// an entry path would let the conflicting edge through undetected.

// TestFastPathDeactivatesWhenTimedEventIsInserted covers the "event
// inserted with Timings set" entry path: AddEventWithCause storing a new
// occurrence that already carries an interval.
func TestFastPathDeactivatesWhenTimedEventIsInserted(t *testing.T) {
	poset := NewPoset()
	root := timedTestEvent(t, "Root", "root", nil)
	leaf := timedTestEvent(t, "Leaf", "leaf", nil)
	if err := poset.AddEvent(root); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(leaf, root.ID); err != nil {
		t.Fatal(err)
	}
	// Poset is still fully untimed here: timedEvents == 0, fast paths active.

	early := timedTestEvent(t, "Early", "early", []EventTiming{{Clock: "K", Start: 0, Finish: 10}})
	if err := poset.AddEventWithCause(early, root.ID); err != nil {
		t.Fatal(err)
	}
	late := timedTestEvent(t, "Late", "late", []EventTiming{{Clock: "K", Start: 5, Finish: 5}})
	if err := poset.AddEventWithCause(late, leaf.ID); err != nil {
		t.Fatal(err)
	}

	// early and late are not yet causally related; the edge below is what
	// must run the slow closure scan (early is a predecessor of "early"
	// itself and, once linked, of leaf's successors) to catch the conflict.
	if err := poset.AddCausal(early.ID, leaf.ID); !errors.Is(err, ErrTimingCausality) {
		t.Fatalf("got %v, want timing conflict caught after a timed insert left the fast path (stale timedEvents would wrongly skip validation)", err)
	}
}

// TestFastPathDeactivatesWhenObservationAddsTiming covers the
// AddObservationWithTimings entry path: a stored occurrence gaining its
// first interval without ever being re-inserted.
func TestFastPathDeactivatesWhenObservationAddsTiming(t *testing.T) {
	poset := NewPoset()
	a := timedTestEvent(t, "A", "a", nil)
	b := timedTestEvent(t, "B", "b", nil)
	c := timedTestEvent(t, "C", "c", nil)
	if err := poset.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(b, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(c); err != nil {
		t.Fatal(err)
	}
	// Poset is still fully untimed here: timedEvents == 0, fast paths active.

	if _, err := poset.AddObservationWithTimings(a.ID, EventObservation{
		Name: "ObservedA", Source: "component",
	}, []EventTiming{{Clock: "K", Start: 0, Finish: 10}}); err != nil {
		t.Fatal(err)
	}
	if _, err := poset.AddObservationWithTimings(c.ID, EventObservation{
		Name: "ObservedC", Source: "component",
	}, []EventTiming{{Clock: "K", Start: 5, Finish: 5}}); err != nil {
		t.Fatal(err)
	}

	// b -> c is the edge that must run the slow closure scan: neither b nor
	// c carries a timing directly, but b's predecessor a now conflicts with
	// c on clock K.
	if err := poset.AddCausal(b.ID, c.ID); !errors.Is(err, ErrTimingCausality) {
		t.Fatalf("got %v, want timing conflict caught after AddObservationWithTimings left the fast path (stale timedEvents would wrongly skip validation)", err)
	}
}

// TestUnmarshalJSONRecomputesTimedEventCounterOnStateReplace covers the
// UnmarshalJSON entry path. UnmarshalJSON replaces the whole event store, so
// the timedEvents count must be recomputed from the new state, not carried
// over (or left stale) from whatever the receiver held before.
func TestUnmarshalJSONRecomputesTimedEventCounterOnStateReplace(t *testing.T) {
	source := NewPoset()
	a := timedTestEvent(t, "A", "a", []EventTiming{{Clock: "K", Start: 0, Finish: 10}})
	b := timedTestEvent(t, "B", "b", []EventTiming{{Clock: "K", Start: 5, Finish: 5}})
	if err := source.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	if err := source.AddEvent(b); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}

	target := NewPoset()
	if err := target.AddEvent(timedTestEvent(t, "U", "u", nil)); err != nil {
		t.Fatal(err)
	}
	// target held only untimed events before this replaces its entire state.
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatal(err)
	}

	if err := target.AddCausal(a.ID, b.ID); !errors.Is(err, ErrTimingCausality) {
		t.Fatalf("got %v, want timing conflict detected after state replace (stale timedEvents would wrongly skip validation)", err)
	}
}

func timedTestEvent(t *testing.T, action, occurrence string, timings []EventTiming) *Event {
	t.Helper()
	event, err := NewDeterministicEvent(EventProvenance{
		Profile: "test-profile", Model: "test-model", Instance: "component",
		Action: action, Occurrence: occurrence, Timings: timings,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
