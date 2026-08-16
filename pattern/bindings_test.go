package pattern

import (
	"bytes"
	"errors"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func addBindingTestEvent(t *testing.T, poset *gorapide.Poset, action, occurrence string, params map[string]any, causes ...gorapide.EventID) *gorapide.Event {
	t.Helper()
	event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "pattern-bindings", Instance: "component",
		Action: action, Occurrence: occurrence, Causes: causes,
	}, params)
	if err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(event, causes...); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestTypedPlaceholderRequiresCrossEventEquality(t *testing.T) {
	poset := gorapide.NewPoset()
	take := addBindingTestEvent(t, poset, "Take_In", "take-alpha", map[string]any{"subject": "alpha"})
	addBindingTestEvent(t, poset, "Deliver", "deliver-beta", map[string]any{"subject": "beta"}, take.ID)
	deliverAlpha := addBindingTestEvent(t, poset, "Deliver", "deliver-alpha", map[string]any{"subject": "alpha"}, take.ID)

	subject := Var("S").WithType("String")
	expression := Seq(
		MatchEvent("Take_In").BindParam("subject", subject),
		MatchEvent("Deliver").BindParam("subject", subject),
	)
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1", len(matches))
	}
	if !matches[0].Events.Contains(take.ID) || !matches[0].Events.Contains(deliverAlpha.ID) {
		t.Fatalf("match has events %v, want Take_In(alpha) and Deliver(alpha)", matches[0].Events.IDs())
	}
	value, ok := matches[0].Bindings.Lookup("S")
	if !ok || value != "alpha" {
		t.Fatalf("binding S = %v, %t; want alpha, true", value, ok)
	}
	if got := len(expression.Match(poset)); got != 1 {
		t.Fatalf("legacy Match projection returned %d matches, want 1", got)
	}
}

func TestPlaceholderSubstitutionUsesRapideModuleAllocationIdentity(t *testing.T) {
	module := func(occurrence string) gorapide.RapideModuleValue {
		value, err := gorapide.NewRapideModuleValue(gorapide.ModuleAllocationProvenance{
			Profile: "stanford-rapide-1.0", Model: "module-placeholder", Parent: "root",
			Generator: "Airplane", Occurrence: occurrence,
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	airplaneA := module("airplane-a")
	airplaneB := module("airplane-b")
	poset := gorapide.NewPoset()
	transmit := addBindingTestEvent(t, poset, "Transmit_Position", "a", map[string]any{"airplane": airplaneA})
	acknowledgeA := addBindingTestEvent(t, poset, "Acknowledge_Position", "a", map[string]any{"airplane": airplaneA}, transmit.ID)
	addBindingTestEvent(t, poset, "Acknowledge_Position", "b", map[string]any{"airplane": airplaneB}, transmit.ID)

	identifier := Var("A")
	expression := Seq(
		MatchEvent("Transmit_Position").BindParam("airplane", identifier),
		MatchEvent("Acknowledge_Position").BindParam("airplane", identifier),
	)
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want only the same allocated Airplane module", len(matches))
	}
	if !matches[0].Events.Contains(transmit.ID) || !matches[0].Events.Contains(acknowledgeA.ID) {
		t.Fatalf("identity match has events %v", matches[0].Events.IDs())
	}
	bound, ok := matches[0].Bindings.Lookup("A")
	boundModule, typed := bound.(gorapide.RapideModuleValue)
	if !ok || !typed || !gorapide.SameRapideModule(boundModule, airplaneA) {
		t.Fatalf("binding A = %#v, want Airplane module %s", bound, airplaneA.Identity())
	}
}

func TestTypedPlaceholderRejectsWrongRapideType(t *testing.T) {
	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "Take_In", "string", map[string]any{"subject": "alpha"})
	addBindingTestEvent(t, poset, "Take_In", "integer", map[string]any{"subject": 7})

	matches, err := MatchWithBindings(
		MatchEvent("Take_In").BindParam("subject", Var("S").WithType("String")), poset,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want only the String-valued event", len(matches))
	}
}

func TestDeclarativeBasicPatternKeyIsCanonical(t *testing.T) {
	first := MatchEvent("Observe").
		WhereSource("sensor").
		WhereParam("metadata", map[string]any{"zone": "A", "priority": 1}).
		BindParam("subject", Var("S").WithType("String"))
	second := MatchEvent("Observe").
		BindParam("subject", Var("S").WithType("String")).
		WhereParam("metadata", map[string]any{"priority": int32(1), "zone": "A"}).
		WhereSource("sensor")

	firstKey, err := DeterministicKey(first)
	if err != nil {
		t.Fatal(err)
	}
	secondKey, err := DeterministicKey(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstKey != secondKey {
		t.Fatalf("semantic keys differ:\n%s\n%s", firstKey, secondKey)
	}
}

func TestWhereParamSupportsCanonicalStructuralValues(t *testing.T) {
	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "Observe", "one", map[string]any{
		"metadata": map[string]any{"zone": "A", "levels": []any{1, 2}},
	})
	pattern := MatchEvent("Observe").WhereParam("metadata", map[string]any{
		"levels": []any{int64(1), int32(2)}, "zone": "A",
	})
	if got := len(pattern.Match(poset)); got != 1 {
		t.Fatalf("got %d matches, want 1", got)
	}
}

func TestMatchWithBindingsRejectsOpaquePredicate(t *testing.T) {
	poset := gorapide.NewPoset()
	_, err := MatchWithBindings(MatchAny().Where(func(*gorapide.Event) bool { return true }), poset)
	if !errors.Is(err, ErrOpaquePattern) {
		t.Fatalf("expected ErrOpaquePattern, got %v", err)
	}
}

func TestBindParamCopiesPlaceholderDeclaration(t *testing.T) {
	placeholder := Var("S").WithType("String")
	pattern := MatchEvent("Observe").BindParam("subject", placeholder)
	placeholder.WithType("Unsupported")
	if _, err := DeterministicKey(pattern); err != nil {
		t.Fatalf("pattern changed after external placeholder mutation: %v", err)
	}
}

func TestBindModuleSourceCopiesPlaceholderDeclaration(t *testing.T) {
	placeholder := Var("Module").WithType("Aircraft")
	pattern := MatchEvent("Radio").BindModuleSource(placeholder)
	before, err := DeterministicKey(pattern)
	if err != nil {
		t.Fatal(err)
	}
	placeholder.WithType("Unsupported")
	after, err := DeterministicKey(pattern)
	if err != nil {
		t.Fatalf("module-source pattern changed after external placeholder mutation: %v", err)
	}
	if before != after {
		t.Fatalf("module-source key changed after external placeholder mutation:\n%s\n%s", before, after)
	}
}

func TestDisjointRequiresDifferentEventOccurrences(t *testing.T) {
	poset := gorapide.NewPoset()
	first := addBindingTestEvent(t, poset, "Audit", "first", map[string]any{"subject": "alpha"})

	expression := Disjoint(MatchEvent("Audit"), MatchEvent("Audit"))
	if got := len(expression.Match(poset)); got != 0 {
		t.Fatalf("one event was reused by both sides of disjoint pattern: %d matches", got)
	}

	second := addBindingTestEvent(t, poset, "Audit", "second", map[string]any{"subject": "alpha"})
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d canonical matches, want 1", len(matches))
	}
	if !matches[0].Events.Contains(first.ID) || !matches[0].Events.Contains(second.ID) {
		t.Fatalf("disjoint match has events %v", matches[0].Events.IDs())
	}
	if got := len(expression.Match(poset)); got != 1 {
		t.Fatalf("legacy match projection returned %d duplicate matches, want 1", got)
	}
}

func TestDisjointPropagatesPlaceholderEquality(t *testing.T) {
	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "Observed", "alpha", map[string]any{"subject": "alpha"})
	addBindingTestEvent(t, poset, "Approved", "alpha", map[string]any{"subject": "alpha"})
	addBindingTestEvent(t, poset, "Approved", "beta", map[string]any{"subject": "beta"})

	expression := Disjoint(
		MatchEvent("Observed").BindParam("subject", Var("S")),
		MatchEvent("Approved").BindParam("subject", Var("S")),
	)
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want only equal-valued disjoint occurrences", len(matches))
	}
}

func TestCanonicalMatchesIgnoreResultEventAndBindingOrder(t *testing.T) {
	firstEvent := &gorapide.Event{ID: "event-a"}
	secondEvent := &gorapide.Event{ID: "event-b"}
	first := []MatchResult{
		{Events: gorapide.EventSet{secondEvent, firstEvent}, Bindings: Bindings{
			{Placeholder: "Z", Value: map[string]any{"n": 1}},
			{Placeholder: "A", Value: "alpha"},
		}},
	}
	second := []MatchResult{
		{Events: gorapide.EventSet{firstEvent, secondEvent}, Bindings: Bindings{
			{Placeholder: "A", Value: "alpha"},
			{Placeholder: "Z", Value: map[string]any{"n": int32(1)}},
		}},
	}
	firstBytes, err := MarshalCanonicalMatches(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := MarshalCanonicalMatches(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("canonical match bytes differ:\n%s\n%s", firstBytes, secondBytes)
	}
}

func TestCanonicalMatchesPreserveNumericKinds(t *testing.T) {
	integer, err := MarshalCanonicalMatches([]MatchResult{{Bindings: Bindings{{Placeholder: "N", Value: 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	floating, err := MarshalCanonicalMatches([]MatchResult{{Bindings: Bindings{{Placeholder: "N", Value: 1.0}}}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(integer, floating) {
		t.Fatal("Integer and Float bindings produced identical canonical bytes")
	}
}

func TestBindingMatchesIgnoreInsertionOrderAndGOMAXPROCS(t *testing.T) {
	originalProcs := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(originalProcs)

	var expected []byte
	for run := 0; run < 100; run++ {
		if run%2 == 0 {
			runtime.GOMAXPROCS(1)
		} else {
			runtime.GOMAXPROCS(8)
		}
		poset := gorapide.NewPoset()
		alpha, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: "stanford-rapide-1.0", Model: "binding-stress", Instance: "component",
			Action: "Observed", Occurrence: "alpha",
		}, map[string]any{"subject": "alpha"})
		if err != nil {
			t.Fatal(err)
		}
		beta, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: "stanford-rapide-1.0", Model: "binding-stress", Instance: "component",
			Action: "Observed", Occurrence: "beta",
		}, map[string]any{"subject": "beta"})
		if err != nil {
			t.Fatal(err)
		}
		ordered := []*gorapide.Event{alpha, beta}
		if run%2 == 1 {
			ordered[0], ordered[1] = ordered[1], ordered[0]
		}
		for _, event := range ordered {
			if err := poset.AddEvent(event); err != nil {
				t.Fatal(err)
			}
		}
		matches, err := MatchWithBindings(
			MatchEvent("Observed").BindParam("subject", Var("S").WithType("String")), poset,
		)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := MarshalCanonicalMatches(matches)
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			expected = encoded
		} else if !bytes.Equal(encoded, expected) {
			t.Fatalf("run %d changed canonical matches", run)
		}
	}
}

func TestBindingsPropagateAcrossSupportedPatternOperators(t *testing.T) {
	poset := gorapide.NewPoset()
	a := addBindingTestEvent(t, poset, "A", "a", map[string]any{"subject": "alpha"})
	addBindingTestEvent(t, poset, "B", "b", map[string]any{"subject": "alpha"}, a.ID)
	addBindingTestEvent(t, poset, "C", "c", map[string]any{"subject": "alpha"})

	bind := func(name string) Pattern {
		return MatchEvent(name).BindParam("subject", Var("S").WithType("String"))
	}
	tests := []struct {
		name       string
		expression Pattern
		want       int
	}{
		{"immediate follows", ImmSeq(bind("A"), bind("B")), 1},
		{"independent", Independent(bind("A"), bind("C")), 1},
		{"disjoint", Disjoint(bind("A"), bind("C")), 1},
		{"union", Union(bind("A"), bind("C")), 1},
		{"or", Or(bind("A"), bind("C")), 2},
		{"and", And(bind("A"), MatchAny().WhereSource("component").BindParam("subject", Var("S"))), 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matches, err := MatchWithBindings(test.expression, poset)
			if err != nil {
				t.Fatal(err)
			}
			if len(matches) != test.want {
				t.Fatalf("got %d matches, want %d", len(matches), test.want)
			}
			for _, match := range matches {
				value, ok := match.Bindings.Lookup("S")
				if !ok || value != "alpha" {
					t.Fatalf("binding S = %v, %t", value, ok)
				}
			}
		})
	}
}
