package pattern

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestRapideTimingMacrosUseDurationConsistentBoundaries(t *testing.T) {
	poset := gorapide.NewPoset()
	instant := rapideTimedEvent(t, "Instant", "instant", nil, []gorapide.EventTiming{{Clock: "C", Start: 10, Finish: 10}})
	span := rapideTimedEvent(t, "Span", "span", nil, []gorapide.EventTiming{{Clock: "C", Start: 20, Finish: 25}})
	if err := poset.AddEvent(instant); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(span); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		expression Pattern
		want       int
	}{
		{"at exact zero duration", RapideAt(MatchEvent("Instant"), 10, "C"), 1},
		{"at wrong tick", RapideAt(MatchEvent("Instant"), 11, "C"), 0},
		{"at rejects nonzero duration", RapideAt(MatchEvent("Span"), 20, "C"), 0},
		{"before inclusive finish", RapideBefore(MatchEvent("Span"), 25, "C"), 1},
		{"before below finish", RapideBefore(MatchEvent("Span"), 24, "C"), 0},
		{"after inclusive start", RapideAfter(MatchEvent("Span"), 20, "C"), 1},
		{"after above start", RapideAfter(MatchEvent("Span"), 21, "C"), 0},
		{"within inclusive duration", RapideWithin(MatchEvent("Span"), 5, "C"), 1},
		{"within below duration", RapideWithin(MatchEvent("Span"), 4, "C"), 0},
		{"within inclusive range", RapideWithinRange(MatchEvent("Span"), 5, 5, "C"), 1},
		{"outside duration range", RapideWithinRange(MatchEvent("Span"), 6, 9, "C"), 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matches, err := MatchWithBindings(test.expression, poset)
			if err != nil {
				t.Fatal(err)
			}
			if len(matches) != test.want {
				t.Fatalf("matches = %d, want %d", len(matches), test.want)
			}
		})
	}
}

func TestRapideTimeBeforeAddsNoCausalOrder(t *testing.T) {
	poset := gorapide.NewPoset()
	left := rapideTimedEvent(t, "A", "a", map[string]any{"subject": "same"}, []gorapide.EventTiming{{Clock: "C", Start: 1, Finish: 3}})
	right := rapideTimedEvent(t, "B", "b", map[string]any{"subject": "same"}, []gorapide.EventTiming{{Clock: "C", Start: 4, Finish: 5}})
	if err := poset.AddEvent(left); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(right); err != nil {
		t.Fatal(err)
	}
	subject := Var("S").WithType("String")
	expression := RapideTimeBefore(
		MatchEvent("A").BindParam("subject", subject),
		MatchEvent("B").BindParam("subject", Var("S").WithType("String")), "C",
	)
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || len(matches[0].Events) != 2 {
		t.Fatalf("temporal order matches = %#v", matches)
	}
	value, ok := matches[0].Bindings.Lookup("S")
	if !ok || value != "same" {
		t.Fatalf("shared binding = %#v, %t", value, ok)
	}
	if !poset.IsCausallyIndependent(left.ID, right.ID) {
		t.Fatal("temporal pattern evaluation invented a causal edge")
	}
	reverse, err := MatchWithBindings(RapideTimeBefore(MatchEvent("B"), MatchEvent("A"), "C"), poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(reverse) != 0 {
		t.Fatal("reversed temporal order matched")
	}
}

func TestRapideTimingMacrosRequireEveryEventRelatedToClock(t *testing.T) {
	poset := gorapide.NewPoset()
	left := rapideTimedEvent(t, "A", "a", nil, []gorapide.EventTiming{{Clock: "C", Start: 1, Finish: 1}})
	right := rapideTimedEvent(t, "B", "b", nil, []gorapide.EventTiming{{Clock: "Other", Start: 1, Finish: 1}})
	if err := poset.AddEvent(left); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(right); err != nil {
		t.Fatal(err)
	}
	matches, err := MatchWithBindings(RapideWithin(Union(MatchEvent("A"), MatchEvent("B")), 100, "C"), poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatal("timing macro accepted an event unrelated to its clock")
	}
}

func TestRapideTimingMacroIdentityIsLosslessAndRejectsInvalidRange(t *testing.T) {
	key, err := DeterministicKey(RapideAt(MatchEvent("A"), ^uint64(0), "C"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(key, `"18446744073709551615"`) {
		t.Fatalf("maximum tick was not represented losslessly: %s", key)
	}
	if _, err := DeterministicKey(RapideWithinRange(MatchEvent("A"), 9, 3, "C")); !errors.Is(err, ErrInvalidRapideTimingPattern) {
		t.Fatalf("got %v, want reversed range rejection", err)
	}
	if _, err := DeterministicKey(RapideBefore(MatchEvent("A"), 1, "")); !errors.Is(err, ErrInvalidRapideTimingPattern) {
		t.Fatalf("got %v, want empty clock rejection", err)
	}
}

func TestRapideTimingMacroMatchBytesIgnoreInsertionOrder(t *testing.T) {
	first := rapideTimedEvent(t, "A", "first", nil, []gorapide.EventTiming{{Clock: "C", Start: 1, Finish: 2}})
	second := rapideTimedEvent(t, "A", "second", nil, []gorapide.EventTiming{{Clock: "C", Start: 3, Finish: 4}})
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
	expression := RapideBefore(MatchEvent("A"), 10, "C")
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
		t.Fatalf("insertion order changed macro matches:\n%s\n%s", leftBytes, rightBytes)
	}
}
