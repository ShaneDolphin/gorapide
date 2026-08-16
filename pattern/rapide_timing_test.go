package pattern

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestRapideTimingBindsEarliestStartAndDuration(t *testing.T) {
	poset := gorapide.NewPoset()
	start := rapideTimedEvent(t, "Start", "start", map[string]any{"expected": int64(10)}, []gorapide.EventTiming{
		{Clock: "mission", Start: 10, Finish: 12}, {Clock: "local", Start: 1, Finish: 2},
	})
	finish := rapideTimedEvent(t, "Finish", "finish", nil, []gorapide.EventTiming{
		{Clock: "mission", Start: 20, Finish: 25}, {Clock: "local", Start: 3, Finish: 4},
	})
	if err := poset.AddEvent(start); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(finish, start.ID); err != nil {
		t.Fatal(err)
	}
	timing := Timing(
		Seq(MatchEvent("Start").BindParam("expected", Var("T").WithType("Integer")), MatchEvent("Finish")),
		Var("T"), Var("D"), "mission",
	)
	matches, err := MatchWithBindings(timing, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1", len(matches))
	}
	assertTimingBinding(t, matches[0].Bindings, "T", int64(10))
	assertTimingBinding(t, matches[0].Bindings, "D", int64(15))
	types, err := BoundPlaceholderTypes(timing)
	if err != nil {
		t.Fatal(err)
	}
	if types["T"] != "Integer" || types["D"] != "Integer" {
		t.Fatalf("unexpected timing binding types: %#v", types)
	}
}

func TestRapideTimingRequiresEveryEventRelatedToClock(t *testing.T) {
	poset := gorapide.NewPoset()
	left := rapideTimedEvent(t, "A", "a", nil, []gorapide.EventTiming{{Clock: "C", Start: 1, Finish: 2}})
	right := rapideTimedEvent(t, "B", "b", nil, []gorapide.EventTiming{{Clock: "Other", Start: 3, Finish: 4}})
	if err := poset.AddEvent(left); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(right); err != nil {
		t.Fatal(err)
	}
	expression := Timing(Union(MatchEvent("A"), MatchEvent("B")), Var("T"), Var("D"), "C")
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("event unrelated to C participated in timing match: %#v", matches)
	}
}

func TestRapideTimingParticipatesInWholeMatchingAndReferences(t *testing.T) {
	poset := gorapide.NewPoset()
	event := rapideTimedEvent(t, "A", "a", nil, []gorapide.EventTiming{{Clock: "C", Start: 4, Finish: 9}})
	if err := poset.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	expression := Timing(MatchEvent("A").WhereSource("component"), Var("T"), Var("D"), "C")
	matches, err := MatchWhole(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("whole timing matches = %d, want 1", len(matches))
	}
	references, err := BasicEventReferences(expression)
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 1 || references[0].Action != "A" || len(references[0].Sources) != 1 || references[0].Sources[0] != "component" {
		t.Fatalf("timing wrapper hid its basic event references: %#v", references)
	}
	empty, err := CanMatchEmpty(expression)
	if err != nil || empty {
		t.Fatalf("Timing unexpectedly matches an empty computation: %t, %v", empty, err)
	}
}

func TestRapideTimingIsCanonicalAndIsolatesPlaceholderMutation(t *testing.T) {
	start := Var("T")
	duration := Var("D")
	left := Timing(MatchEvent("A"), start, duration, "C")
	start.name = "changed"
	duration.typ = "String"
	right := Timing(MatchEvent("A"), Var("T").WithType("Integer"), Var("D").WithType("Integer"), "C")
	leftKey, err := DeterministicKey(left)
	if err != nil {
		t.Fatal(err)
	}
	rightKey, err := DeterministicKey(right)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(leftKey), []byte(rightKey)) {
		t.Fatalf("placeholder mutation changed a closed timing pattern:\n%s\n%s", leftKey, rightKey)
	}
	wrongType := Timing(MatchEvent("A"), Var("T").WithType("String"), Var("D"), "C")
	if _, err := DeterministicKey(wrongType); !errors.Is(err, ErrInvalidRapideTimingPattern) {
		t.Fatalf("got %v, want invalid timing placeholder type", err)
	}
}

func TestRapideTimingMatchBytesIgnoreEventInsertionOrder(t *testing.T) {
	first := rapideTimedEvent(t, "A", "first", nil, []gorapide.EventTiming{{Clock: "C", Start: 1, Finish: 2}})
	second := rapideTimedEvent(t, "A", "second", nil, []gorapide.EventTiming{{Clock: "C", Start: 5, Finish: 8}})
	left, right := gorapide.NewPoset(), gorapide.NewPoset()
	if err := left.AddEvent(first.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := left.AddEvent(second.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := right.AddEvent(second.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := right.AddEvent(first.Snapshot()); err != nil {
		t.Fatal(err)
	}
	expression := Timing(MatchEvent("A"), Var("T"), Var("D"), "C")
	leftMatches, err := MatchWithBindings(expression, left)
	if err != nil {
		t.Fatal(err)
	}
	rightMatches, err := MatchWithBindings(expression, right)
	if err != nil {
		t.Fatal(err)
	}
	leftBytes, err := MarshalCanonicalMatches(leftMatches)
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := MarshalCanonicalMatches(rightMatches)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatalf("insertion order changed timing matches:\n%s\n%s", leftBytes, rightBytes)
	}
}

func TestRapideTimingFailsExplicitlyOutsideInitialIntegerRange(t *testing.T) {
	poset := gorapide.NewPoset()
	event := rapideTimedEvent(t, "A", "a", nil, []gorapide.EventTiming{{Clock: "C", Start: uint64(math.MaxInt64) + 1, Finish: uint64(math.MaxInt64) + 1}})
	if err := poset.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	_, err := MatchWithBindings(Timing(MatchEvent("A"), Var("T"), Var("D"), "C"), poset)
	if !errors.Is(err, ErrTimingBindingRange) {
		t.Fatalf("got %v, want explicit timing binding range failure", err)
	}
}

func assertTimingBinding(t *testing.T, bindings Bindings, name string, want int64) {
	t.Helper()
	value, ok := bindings.Lookup(name)
	if !ok || value != want {
		t.Fatalf("binding %s = %#v, %t; want %d", name, value, ok, want)
	}
}

func rapideTimedEvent(t *testing.T, action, occurrence string, params map[string]any, timings []gorapide.EventTiming) *gorapide.Event {
	t.Helper()
	event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: "test", Model: "timing", Instance: "component", Action: action,
		Occurrence: occurrence, Timings: timings,
	}, params)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
