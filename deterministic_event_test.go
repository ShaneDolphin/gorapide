package gorapide

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"
)

func TestDeterministicEventSameSemanticInputSameID(t *testing.T) {
	provenanceA := EventProvenance{
		Profile:    "stanford-rapide-1.0",
		Model:      "connection-conformance",
		Instance:   "sender",
		Action:     "Send",
		Occurrence: "input/1",
		Causes:     []EventID{"cause-b", "cause-a"},
	}
	provenanceB := provenanceA
	provenanceB.Causes = []EventID{"cause-a", "cause-b"}

	eventA, err := NewDeterministicEvent(provenanceA, map[string]any{
		"z": []any{true, int64(7)},
		"a": map[string]any{"payload": "value"},
	})
	if err != nil {
		t.Fatalf("NewDeterministicEvent A: %v", err)
	}
	eventB, err := NewDeterministicEvent(provenanceB, map[string]any{
		"a": map[string]any{"payload": "value"},
		"z": []any{true, int(7)},
	})
	if err != nil {
		t.Fatalf("NewDeterministicEvent B: %v", err)
	}

	if eventA.ID != eventB.ID {
		t.Fatalf("same semantic input produced different IDs:\nA=%s\nB=%s", eventA.ID, eventB.ID)
	}
	if !eventA.Clock.WallTime.IsZero() || !eventB.Clock.WallTime.IsZero() {
		t.Fatal("deterministic events must not read the wall clock")
	}
}

func TestDeterministicEventCauseSetIsCanonical(t *testing.T) {
	base := EventProvenance{
		Profile: "stanford-rapide-1.0", Instance: "i", Action: "A", Occurrence: "1",
	}
	base.Causes = []EventID{"b", "a", "a"}
	withDuplicate, err := NewDeterministicEvent(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.Causes = []EventID{"a", "b"}
	withoutDuplicate, err := NewDeterministicEvent(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if withDuplicate.ID != withoutDuplicate.ID {
		t.Fatal("direct causes are a set; order and duplicate entries must not change identity")
	}
}

func TestDeterministicEventSeparatesAllocationIdentityFromInitialObservation(t *testing.T) {
	provenance := EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "m", Instance: "mod1-allocation",
		Action: "Start", Occurrence: "module:start",
	}
	semantic, err := NewDeterministicEvent(provenance, nil)
	if err != nil {
		t.Fatal(err)
	}
	provenance.ObservationSource = "$module/worker"
	observed, err := NewDeterministicEvent(provenance, nil)
	if err != nil {
		t.Fatal(err)
	}
	if semantic.ID != observed.ID {
		t.Fatal("a qualified observation name created a second occurrence identity")
	}
	if observed.Source != "$module/worker" || !observed.HasObservation("$module/worker", "Start") ||
		observed.HasObservation("mod1-allocation", "Start") {
		t.Fatalf("initial observation=%#v", observed.EventObservations())
	}
}

func TestDeterministicEventRejectsCauseMismatchOnInsertion(t *testing.T) {
	event, err := NewDeterministicEvent(EventProvenance{
		Profile: "stanford-rapide-1.0", Instance: "i", Action: "B", Occurrence: "1",
		Causes: []EventID{"cause"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPoset()
	if err := p.AddEvent(event); !errors.Is(err, ErrCauseMismatch) {
		t.Fatalf("expected ErrCauseMismatch, got %v", err)
	}
}

func TestDeterministicEventOccurrenceDistinguishesEvents(t *testing.T) {
	base := EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "m", Instance: "i", Action: "A",
		Occurrence: "1",
	}
	first, err := NewDeterministicEvent(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.Occurrence = "2"
	second, err := NewDeterministicEvent(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("distinct semantic occurrences must have distinct IDs")
	}
}

func TestDeterministicEventRejectsValuesOutsideAlgebra(t *testing.T) {
	_, err := NewDeterministicEvent(EventProvenance{
		Profile: "stanford-rapide-1.0", Instance: "i", Action: "A", Occurrence: "1",
	}, map[string]any{"host_time": time.Now()})
	if !errors.Is(err, ErrNonCanonicalValue) {
		t.Fatalf("expected ErrNonCanonicalValue, got %v", err)
	}

	_, err = NewDeterministicEvent(EventProvenance{
		Profile: "stanford-rapide-1.0", Instance: "i", Action: "A", Occurrence: "1",
	}, map[string]any{"not_a_number": math.NaN()})
	if !errors.Is(err, ErrNonCanonicalValue) {
		t.Fatalf("expected ErrNonCanonicalValue for NaN, got %v", err)
	}
}

func TestDeterministicEventCanonicalizesNegativeZero(t *testing.T) {
	provenance := EventProvenance{
		Profile: "stanford-rapide-1.0", Instance: "i", Action: "A", Occurrence: "1",
	}
	positive, err := NewDeterministicEvent(provenance, map[string]any{"n": float64(0)})
	if err != nil {
		t.Fatal(err)
	}
	negative, err := NewDeterministicEvent(provenance, map[string]any{"n": math.Copysign(0, -1)})
	if err != nil {
		t.Fatal(err)
	}
	if positive.ID != negative.ID {
		t.Fatal("positive and negative zero should have one canonical identity")
	}
}

func TestDeterministicPosetStorageIsDeeplyImmutable(t *testing.T) {
	original := map[string]any{
		"object": map[string]any{"value": "original"},
		"list":   []any{map[string]any{"value": "original"}},
		"bytes":  []byte{1, 2, 3},
	}
	event, err := NewDeterministicEvent(EventProvenance{
		Profile: "stanford-rapide-1.0", Instance: "component", Action: "Action", Occurrence: "immutable/1",
	}, original)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPoset()
	if err := p.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	baseline, err := p.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}

	original["object"].(map[string]any)["value"] = "mutated input"
	event.Params["object"].(map[string]any)["value"] = "mutated caller event"
	event.Params["list"].([]any)[0].(map[string]any)["value"] = "mutated caller list"
	event.Params["bytes"].([]byte)[0] = 9
	snapshot, ok := p.Event(event.ID)
	if !ok {
		t.Fatal("stored event missing")
	}
	snapshot.Params["object"].(map[string]any)["value"] = "mutated read snapshot"
	snapshot.Observations[0].Params["object"].(map[string]any)["value"] = "mutated observation snapshot"

	after, err := p.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(baseline, after) {
		t.Fatalf("external mutation changed deterministic poset:\nbefore=%s\nafter=%s", baseline, after)
	}
}

func TestObservationViewsShareIdentityAndAreCanonicalIsolatedSnapshots(t *testing.T) {
	event, err := NewDeterministicEvent(EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "observations", Instance: "z-caller",
		Action: "Required'Call", Occurrence: "call/1",
	}, map[string]any{"key": "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	event.Observations = append(event.Observations, EventObservation{
		Name: "Provided'Call", Source: "a-provider", Params: map[string]any{"key": "alpha"},
	})
	views := event.ObservationViews()
	if len(views) != 2 {
		t.Fatalf("observation views=%d, want 2", len(views))
	}
	if views[0].Source != "a-provider" || views[0].Name != "Provided'Call" ||
		views[1].Source != "z-caller" || views[1].Name != "Required'Call" {
		t.Fatalf("views are not canonically ordered: %#v", views)
	}
	if views[0].ID != event.ID || views[1].ID != event.ID {
		t.Fatal("qualified views changed occurrence identity")
	}
	views[0].Params["key"] = "mutated"
	views[0].Observations[0].Params["key"] = "also-mutated"
	if event.ParamString("key") != "alpha" || event.EventObservations()[0].Params["key"] != "alpha" {
		t.Fatal("mutating an observation view changed the source occurrence")
	}
}
