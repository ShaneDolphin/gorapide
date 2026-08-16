package pattern

import (
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestUniversalIntegerRangeRequiresOneOrderedInstancePerValue(t *testing.T) {
	poset := gorapide.NewPoset()
	first := addBindingTestEvent(t, poset, "WriteCall", "one", map[string]any{"value": 1})
	second := addBindingTestEvent(t, poset, "WriteCall", "two", map[string]any{"value": 2}, first.ID)
	addBindingTestEvent(t, poset, "WriteCall", "three", map[string]any{"value": 3}, second.ID)

	expression := ForAllIntegerRange(
		Var("D").WithType("Integer"), 1, 3, RelationFollows,
		MatchEvent("WriteCall").BindParam("value", Var("D").WithType("Integer")),
	)
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || len(matches[0].Events) != 3 {
		t.Fatalf("universal range matches = %#v, want one three-event computation", matches)
	}
	if _, exists := matches[0].Bindings.Lookup("D"); exists {
		t.Fatal("universal placeholder escaped its qualification as a scalar binding")
	}

	missing := gorapide.NewPoset()
	one := addBindingTestEvent(t, missing, "WriteCall", "one", map[string]any{"value": 1})
	addBindingTestEvent(t, missing, "WriteCall", "three", map[string]any{"value": 3}, one.ID)
	if got, err := MatchWithBindings(expression, missing); err != nil || len(got) != 0 {
		t.Fatalf("missing domain value matched: %#v, %v", got, err)
	}
}

func TestUniversalRangePreservesSharedExistentialBindings(t *testing.T) {
	poset := gorapide.NewPoset()
	alphaOne := addBindingTestEvent(t, poset, "Observed", "alpha-one", map[string]any{"value": 1, "group": "alpha"})
	addBindingTestEvent(t, poset, "Observed", "alpha-two", map[string]any{"value": 2, "group": "alpha"}, alphaOne.ID)
	betaOne := addBindingTestEvent(t, poset, "Observed", "beta-one", map[string]any{"value": 1, "group": "beta"})
	addBindingTestEvent(t, poset, "Observed", "gamma-two", map[string]any{"value": 2, "group": "gamma"}, betaOne.ID)

	inner := MatchEvent("Observed").
		BindParam("value", Var("D").WithType("Integer")).
		BindParam("group", Var("G").WithType("String"))
	expression := ForAllIntegerRange(Var("D"), 1, 2, RelationFollows, inner)
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %#v, want only the shared alpha existential binding", matches)
	}
	group, exists := matches[0].Bindings.Lookup("G")
	if !exists || group != "alpha" {
		t.Fatalf("G = %v, %t; want alpha", group, exists)
	}
	if names, err := BoundPlaceholders(expression); err != nil || len(names) != 1 || names[0] != "G" {
		t.Fatalf("result placeholders = %v, %v; want only G", names, err)
	}
}

func TestUniversalWholeMatchUsesItsFiniteDomainAssociation(t *testing.T) {
	poset := gorapide.NewPoset()
	first := addBindingTestEvent(t, poset, "WriteCall", "one", map[string]any{"value": 1})
	second := addBindingTestEvent(t, poset, "WriteCall", "two", map[string]any{"value": 2}, first.ID)
	addBindingTestEvent(t, poset, "WriteCall", "outside", map[string]any{"value": 99}, second.ID)

	expression := ForAllIntegerRange(
		Var("D"), 1, 2, RelationFollows,
		MatchEvent("WriteCall").BindParam("value", Var("D").WithType("Integer")),
	)
	matches, err := MatchWhole(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || len(matches[0].Events) != 2 {
		t.Fatalf("outside-domain event entered the associated computation: %#v", matches)
	}

	addBindingTestEvent(t, poset, "WriteCall", "duplicate-two", map[string]any{"value": 2}, first.ID)
	matches, err = MatchWhole(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatal("exact universal match ignored a duplicate in-domain associated event")
	}
}

func TestUniversalRangeHasCanonicalFiniteIdentity(t *testing.T) {
	makePattern := func() Pattern {
		return ForAllIntegerRange(
			Var("D"), -2, 2, RelationIndependent,
			MatchEvent("A").BindParam("n", Var("D").WithType("Integer")),
		)
	}
	first, err := DeterministicKey(makePattern())
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeterministicKey(makePattern())
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.Contains(first, `"kind":"universal-integer-range"`) {
		t.Fatalf("universal key is not canonical: %q / %q", first, second)
	}
}

func TestUniversalPlaceholderScopeDoesNotDeleteAnOuterBindingWithTheSameName(t *testing.T) {
	local := ForAllIntegerRange(
		Var("D"), 1, 1, RelationDisjoint,
		MatchEvent("Local").BindParam("n", Var("D").WithType("Integer")),
	)
	expression := Disjoint(
		MatchEvent("Outer").BindParam("name", Var("D").WithType("String")),
		local,
	)
	names, err := BoundPlaceholders(expression)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "D" {
		t.Fatalf("outer binding was lost through universal scope: %v", names)
	}
	types, err := BoundPlaceholderTypes(expression)
	if err != nil {
		t.Fatal(err)
	}
	if types["D"] != "String" {
		t.Fatalf("outer binding type=%q, want String", types["D"])
	}
}

func TestUniversalRangeRejectsAnImplicitResourceBound(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("oversized universal range did not fail explicitly")
		}
	}()
	ForAllIntegerRange(
		Var("D"), 0, int64(MaxUniversalRangeCardinality), RelationDisjoint,
		MatchEvent("A").BindParam("n", Var("D").WithType("Integer")),
	)
}
