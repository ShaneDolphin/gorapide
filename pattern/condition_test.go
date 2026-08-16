package pattern

import (
	"errors"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestWhereEvaluatesConditionForEachCompleteMatch(t *testing.T) {
	poset := gorapide.NewPoset()
	firstLow := addBindingTestEvent(t, poset, "Write", "first-low", map[string]any{"version": 1})
	addBindingTestEvent(t, poset, "Write", "first-high", map[string]any{"version": 2}, firstLow.ID)
	secondHigh := addBindingTestEvent(t, poset, "Write", "second-high", map[string]any{"version": 4})
	secondLow := addBindingTestEvent(t, poset, "Write", "second-low", map[string]any{"version": 3}, secondHigh.ID)

	left := Var("V1").WithType("Integer")
	right := Var("V2").WithType("Integer")
	expression := Where(
		Seq(
			MatchEvent("Write").BindParam("version", left),
			MatchEvent("Write").BindParam("version", right),
		),
		BinaryCondition(ConditionGreaterEqual, BindingCondition(left), BindingCondition(right)),
	)
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || !matches[0].Events.Contains(secondHigh.ID) || !matches[0].Events.Contains(secondLow.ID) {
		t.Fatalf("guarded matches=%#v, want only the decreasing-version chain", matches)
	}
	if value, ok := matches[0].Bindings.Lookup("V1"); !ok || value != int64(4) {
		t.Fatalf("V1=%v, %t; want 4", value, ok)
	}
	if got := len(expression.Match(poset)); got != 1 {
		t.Fatalf("legacy projection returned %d guarded matches, want 1", got)
	}
}

func TestWhereSupportsClosedBooleanAndIntegerAlgebra(t *testing.T) {
	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "Measure", "inside", map[string]any{"value": 15})
	addBindingTestEvent(t, poset, "Measure", "outside", map[string]any{"value": 25})
	value := Var("Value").WithType("Integer")
	condition := BinaryCondition(ConditionAnd,
		BinaryCondition(ConditionGreaterEqual, BindingCondition(value), LiteralCondition(int64(10))),
		BinaryCondition(ConditionLess,
			BinaryCondition(ConditionAdd, BindingCondition(value), LiteralCondition(int64(1))),
			LiteralCondition(int64(20))),
	)
	matches, err := MatchWithBindings(Where(
		MatchEvent("Measure").BindParam("value", value), condition,
	), poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("algebraic guard matches=%d, want 1", len(matches))
	}
}

func TestWhereKeyAndResultsAreHostIndependent(t *testing.T) {
	build := func() Pattern {
		value := Var("Value").WithType("Integer")
		return Where(
			MatchEvent("Measure").BindParam("value", value),
			BinaryCondition(ConditionGreater, BindingCondition(value), LiteralCondition(int32(10))),
		)
	}
	left, err := DeterministicKey(build())
	if err != nil {
		t.Fatal(err)
	}
	right, err := DeterministicKey(build())
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("guard keys differ:\n%s\n%s", left, right)
	}

	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "Measure", "one", map[string]any{"value": 11})
	var baseline string
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 2, 4} {
		runtime.GOMAXPROCS(processors)
		matches, err := MatchWithBindings(build(), poset)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := SemanticDigestMatches(matches)
		if err != nil {
			t.Fatal(err)
		}
		if baseline == "" {
			baseline = digest
		} else if digest != baseline {
			t.Fatalf("guarded match digest changed at GOMAXPROCS=%d", processors)
		}
	}
}

func TestWherePreservesBoundPlaceholderMetadata(t *testing.T) {
	value := Var("Value").WithType("Integer")
	expression := Where(
		MatchEvent("Measure").BindParam("value", value),
		BinaryCondition(ConditionGreater, BindingCondition(value), LiteralCondition(int64(10))),
	)
	names, err := BoundPlaceholders(expression)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "Value" {
		t.Fatalf("bound placeholders=%v, want [Value]", names)
	}
	types, err := BoundPlaceholderTypes(expression)
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 || types["Value"] != "Integer" {
		t.Fatalf("bound placeholder types=%v", types)
	}
}

func TestWhereDeterministicKeyRejectsUnboundOrConflictingGuardBinding(t *testing.T) {
	bound := Var("Value").WithType("Integer")
	base := MatchEvent("Measure").BindParam("value", bound)
	tests := []struct {
		name      string
		condition Condition
	}{
		{name: "unbound", condition: BindingCondition(Var("Missing").WithType("Integer"))},
		{name: "conflicting type", condition: BindingCondition(Var("Value").WithType("Boolean"))},
		{name: "non Boolean", condition: LiteralCondition(int64(1))},
		{name: "ill typed", condition: BinaryCondition(ConditionAnd, BindingCondition(bound), LiteralCondition(true))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DeterministicKey(Where(base, test.condition)); !errors.Is(err, ErrInvalidCondition) {
				t.Fatalf("error=%v, want ErrInvalidCondition", err)
			}
		})
	}
}

func TestWhereRejectsMalformedOrIllTypedConditions(t *testing.T) {
	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "A", "one", nil)
	tests := []struct {
		name      string
		condition Condition
	}{
		{name: "zero value", condition: Condition{}},
		{name: "missing binding", condition: BindingCondition(Var("Missing").WithType("Boolean"))},
		{name: "non Boolean result", condition: LiteralCondition(int64(1))},
		{name: "wrong operand type", condition: BinaryCondition(ConditionAnd, LiteralCondition(true), LiteralCondition(int64(1)))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := MatchWithBindings(Where(MatchEvent("A"), test.condition), poset)
			if !errors.Is(err, ErrInvalidCondition) {
				t.Fatalf("error=%v, want ErrInvalidCondition", err)
			}
		})
	}
}
