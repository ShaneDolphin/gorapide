package pattern

import (
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestNamedIntegerRangeIterationSubstitutesEachValueInOrder(t *testing.T) {
	poset := gorapide.NewPoset()
	first := addBindingTestEvent(t, poset, "IssueCheck", "one", map[string]any{"number": 1})
	second := addBindingTestEvent(t, poset, "IssueCheck", "two", map[string]any{"number": 2}, first.ID)
	addBindingTestEvent(t, poset, "IssueCheck", "three", map[string]any{"number": 3}, second.ID)

	expression := IterateIntegerRange(
		Var("I").WithType("Integer"), 1, 3, RelationFollows,
		MatchEvent("IssueCheck").BindParam("number", Var("I").WithType("Integer")),
	)
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || len(matches[0].Events) != 3 {
		t.Fatalf("named iteration matches=%#v, want one three-event computation", matches)
	}
	if _, exists := matches[0].Bindings.Lookup("I"); exists {
		t.Fatal("named iterator escaped its lexical scope")
	}
	if names, err := BoundPlaceholders(expression); err != nil || len(names) != 0 {
		t.Fatalf("named iteration exposed placeholders %v, %v", names, err)
	}
}

func TestNamedRangeIterationUsesItsFiniteDomainForWholeMatch(t *testing.T) {
	poset := gorapide.NewPoset()
	first := addBindingTestEvent(t, poset, "Write", "one", map[string]any{"value": 1})
	addBindingTestEvent(t, poset, "Write", "two", map[string]any{"value": 2}, first.ID)
	addBindingTestEvent(t, poset, "Write", "outside", map[string]any{"value": 99}, first.ID)
	expression := IterateIntegerRange(
		Var("I"), 1, 2, RelationFollows,
		MatchEvent("Write").BindParam("value", Var("I").WithType("Integer")),
	)
	matches, err := MatchWhole(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || len(matches[0].Events) != 2 {
		t.Fatalf("outside iterator domain changed exact association: %#v", matches)
	}

	addBindingTestEvent(t, poset, "Write", "duplicate-two", map[string]any{"value": 2}, first.ID)
	if matches, err = MatchWhole(expression, poset); err != nil || len(matches) != 0 {
		t.Fatalf("duplicate in-domain event survived exact named iteration: %#v, %v", matches, err)
	}
}

func TestEmptyNamedRangeIterationMatchesOnlyTheEmptyComputation(t *testing.T) {
	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "A", "present", nil)
	expression := IterateIntegerRange(Var("I"), 3, 1, RelationDisjoint, MatchEvent("A"))
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || len(matches[0].Events) != 0 {
		t.Fatalf("empty named range matches=%#v, want one empty computation", matches)
	}
	if empty, err := CanMatchEmpty(expression); err != nil || !empty {
		t.Fatalf("CanMatchEmpty=%t, %v", empty, err)
	}
}

func TestNamedRangeIterationHasDistinctCanonicalIdentity(t *testing.T) {
	makePattern := func() Pattern {
		return IterateIntegerRange(
			Var("I"), -2, 2, RelationIndependent,
			MatchEvent("A").BindParam("n", Var("I").WithType("Integer")),
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
	if first != second || !strings.Contains(first, `"kind":"named-integer-range-iteration"`) {
		t.Fatalf("named iteration key is not canonical: %q / %q", first, second)
	}
}

func TestNamedRangeIterationRejectsAnImplicitResourceBound(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("oversized named range did not fail explicitly")
		}
	}()
	IterateIntegerRange(
		Var("I"), 0, int64(MaxNamedRangeIterationCardinality), RelationDisjoint,
		MatchEvent("A").BindParam("n", Var("I").WithType("Integer")),
	)
}
