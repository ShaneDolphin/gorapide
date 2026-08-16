package pattern

import (
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestDeterministicKeyBasicPattern(t *testing.T) {
	first, err := DeterministicKey(MatchEvent("Receive"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeterministicKey(MatchEvent("Receive"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first == "" {
		t.Fatalf("unstable key: %q != %q", first, second)
	}
}

func TestIsPlainBasicEventPatternRejectsFiltersBindingsAndCompounds(t *testing.T) {
	if !IsPlainBasicEventPattern(MatchEvent("A")) {
		t.Fatal("plain named event pattern was not recognized")
	}
	for name, expression := range map[string]Pattern{
		"any":       MatchAny(),
		"parameter": MatchEvent("A").WhereParam("n", 1),
		"source":    MatchEvent("A").WhereSource("source"),
		"binding":   MatchEvent("A").BindParam("n", Var("N").WithType("Integer")),
		"module":    MatchEvent("A").BindModuleSource(Var("M").WithType("I")),
		"opaque":    MatchEvent("A").Where(func(*gorapide.Event) bool { return true }),
		"compound":  Seq(MatchEvent("A"), MatchEvent("B")),
	} {
		if IsPlainBasicEventPattern(expression) {
			t.Fatalf("%s pattern was incorrectly accepted as plain", name)
		}
	}
}

func TestIsUnqualifiedSingleEventPatternAcceptsBindingsAndClosedGuardOnly(t *testing.T) {
	bound := MatchEvent("A").BindParam("n", Var("N").WithType("Integer"))
	guarded := Where(bound, BinaryCondition(
		ConditionGreater,
		BindingCondition(Var("N").WithType("Integer")),
		LiteralCondition(int64(0)),
	))
	if !IsUnqualifiedSingleEventPattern(MatchEvent("A")) ||
		!IsUnqualifiedSingleEventPattern(MatchEvent("A").WhereParam("n", int64(1))) ||
		!IsUnqualifiedSingleEventPattern(bound) || !IsUnqualifiedSingleEventPattern(guarded) {
		t.Fatal("deterministic unqualified single-event forms were not recognized")
	}
	for name, expression := range map[string]Pattern{
		"source":   MatchEvent("A").WhereSource("source"),
		"module":   MatchEvent("A").BindModuleSource(Var("M").WithType("I")),
		"opaque":   MatchEvent("A").Where(func(*gorapide.Event) bool { return true }),
		"compound": Seq(MatchEvent("A"), MatchEvent("B")),
	} {
		if IsUnqualifiedSingleEventPattern(expression) {
			t.Fatalf("%s pattern was incorrectly accepted as unqualified single-event", name)
		}
	}
}

func TestIsModuleQualifiedSingleEventPatternAcceptsOneClosedBoundSourceOnly(t *testing.T) {
	qualified := MatchEvent("Radio").
		BindModuleSource(Var("Peer").WithType("Aircraft")).
		BindParam("message", Var("Message").WithType("Integer"))
	guarded := Where(qualified, BinaryCondition(
		ConditionGreater,
		BindingCondition(Var("Message").WithType("Integer")),
		LiteralCondition(int64(0)),
	))
	if !IsModuleQualifiedSingleEventPattern(qualified) || !IsModuleQualifiedSingleEventPattern(guarded) {
		t.Fatal("direct deterministic module-qualified source forms were not recognized")
	}
	for name, expression := range map[string]Pattern{
		"unqualified": MatchEvent("Radio"),
		"fixed":       MatchEvent("Radio").WhereSource("plane"),
		"opaque": MatchEvent("Radio").
			BindModuleSource(Var("Peer").WithType("Aircraft")).
			Where(func(*gorapide.Event) bool { return true }),
		"compound": Union(qualified, MatchEvent("Other")),
	} {
		if IsModuleQualifiedSingleEventPattern(expression) {
			t.Fatalf("%s pattern was incorrectly accepted as one module-qualified event", name)
		}
	}
}

func TestIsDeterministicSingleEventPatternAcceptsQualifiedClosedFormsOnly(t *testing.T) {
	bound := MatchEvent("A").WhereSource("source").
		BindParam("n", Var("N").WithType("Integer"))
	guarded := Where(bound, BinaryCondition(
		ConditionGreater,
		BindingCondition(Var("N").WithType("Integer")),
		LiteralCondition(int64(0)),
	))
	for name, expression := range map[string]Pattern{
		"plain":        MatchEvent("A"),
		"fixed":        bound,
		"module":       MatchEvent("A").BindModuleSource(Var("M").WithType("I")),
		"closed-where": guarded,
	} {
		if !IsDeterministicSingleEventPattern(expression) {
			t.Fatalf("%s pattern was not recognized as one deterministic event", name)
		}
	}
	for name, expression := range map[string]Pattern{
		"any":       MatchAny(),
		"opaque":    MatchEvent("A").Where(func(*gorapide.Event) bool { return true }),
		"compound":  Seq(MatchEvent("A"), MatchEvent("B")),
		"iteration": IterateRange(MatchEvent("A"), RelationDisjoint, 1, 1),
		"timed":     RapideAt(MatchEvent("A"), 1, "clock"),
	} {
		if IsDeterministicSingleEventPattern(expression) {
			t.Fatalf("%s pattern was incorrectly accepted as one deterministic event", name)
		}
	}
}

func TestIsUnqualifiedNonemptyEventPatternAcceptsSourceCompoundAlgebraOnly(t *testing.T) {
	bound := Union(
		MatchEvent("A").BindParam("n", Var("N").WithType("Integer")),
		MatchEvent("B").BindParam("n", Var("N").WithType("Integer")),
	)
	guarded := Where(bound, BinaryCondition(
		ConditionGreater,
		BindingCondition(Var("N").WithType("Integer")),
		LiteralCondition(int64(0)),
	))
	for name, expression := range map[string]Pattern{
		"basic":       MatchEvent("A"),
		"compound":    guarded,
		"causal":      Seq(MatchEvent("A"), MatchEvent("B")),
		"independent": Independent(MatchEvent("A"), MatchEvent("B")),
		"finite":      IterateRange(MatchEvent("A"), RelationDisjoint, 1, 2),
	} {
		if !IsUnqualifiedNonemptyEventPattern(expression) {
			t.Fatalf("%s pattern was not recognized as unqualified and nonempty", name)
		}
	}
	for name, expression := range map[string]Pattern{
		"any":       MatchAny(),
		"source":    Union(MatchEvent("A").WhereSource("source"), MatchEvent("B")),
		"module":    Union(MatchEvent("A").BindModuleSource(Var("M").WithType("I")), MatchEvent("B")),
		"opaque":    MatchEvent("A").Where(func(*gorapide.Event) bool { return true }),
		"empty":     IterateZeroOrMore(MatchEvent("A"), RelationDisjoint),
		"timed":     RapideAt(MatchEvent("A"), 1, "clock"),
		"universal": ForAllIntegerRange(Var("I").WithType("Integer"), 1, 2, RelationDisjoint, MatchEvent("A")),
		"state":     Where(MatchEvent("A"), StateCondition("worker\x00enabled", "Boolean")),
	} {
		if IsUnqualifiedNonemptyEventPattern(expression) {
			t.Fatalf("%s pattern was incorrectly accepted as the source compound subset", name)
		}
	}
}

func TestDeterministicKeyRejectsOpaquePredicate(t *testing.T) {
	_, err := DeterministicKey(MatchEvent("Receive").Where(func(*gorapide.Event) bool { return true }))
	if !errors.Is(err, ErrOpaquePattern) {
		t.Fatalf("expected ErrOpaquePattern, got %v", err)
	}
}

func TestDeterministicKeySupportsDeclarativeFilters(t *testing.T) {
	_, err := DeterministicKey(MatchEvent("Receive").WhereSource("sender").WhereParam("sequence", 1))
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeterministicKeySupportsCompoundPattern(t *testing.T) {
	first, err := DeterministicKey(Disjoint(MatchEvent("A"), MatchEvent("B")))
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeterministicKey(Disjoint(MatchEvent("B"), MatchEvent("A")))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("commutative pattern key depends on operand order:\n%s\n%s", first, second)
	}
}

func TestDeterministicSingleEventKeyRejectsComposite(t *testing.T) {
	_, err := DeterministicSingleEventKey(Seq(MatchEvent("A"), MatchEvent("B")))
	if !errors.Is(err, ErrOpaquePattern) {
		t.Fatalf("expected ErrOpaquePattern, got %v", err)
	}
}

func TestDeterministicSingleEventKeyAcceptsClosedWhereGuard(t *testing.T) {
	guarded := Where(MatchEvent("A").BindParam("n", Var("N").WithType("Integer")),
		BinaryCondition(ConditionGreater, BindingCondition(Var("N").WithType("Integer")), LiteralCondition(0)))
	key, err := DeterministicSingleEventKey(guarded)
	if err != nil {
		t.Fatal(err)
	}
	deterministic, err := DeterministicKey(guarded)
	if err != nil {
		t.Fatal(err)
	}
	if key != deterministic {
		t.Fatalf("single-event guarded key=%q, deterministic key=%q", key, deterministic)
	}
}

func TestDeterministicKeyRejectsNonRapideJoin(t *testing.T) {
	_, err := DeterministicKey(Join(MatchEvent("A"), MatchEvent("B")))
	if !errors.Is(err, ErrOpaquePattern) {
		t.Fatalf("expected ErrOpaquePattern, got %v", err)
	}
}

func TestCanMatchEmptyFollowsDeterministicPatternStructure(t *testing.T) {
	empty := IterateZeroOrMore(MatchEvent("A"), RelationDisjoint)
	tests := []struct {
		name    string
		pattern Pattern
		want    bool
	}{
		{name: "default trigger", pattern: nil, want: false},
		{name: "basic", pattern: MatchEvent("A"), want: false},
		{name: "zero iteration", pattern: empty, want: true},
		{name: "one empty iteration", pattern: IterateOneOrMore(empty, RelationDisjoint), want: true},
		{name: "or empty branch", pattern: Or(MatchEvent("A"), empty), want: true},
		{name: "and requires nonempty branch", pattern: And(MatchEvent("A"), empty), want: false},
		{name: "all empty conjunction", pattern: And(empty, empty), want: true},
		{name: "sequence requires nonempty branch", pattern: Seq(empty, MatchEvent("A")), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := CanMatchEmpty(test.pattern)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("CanMatchEmpty=%t, want %t", got, test.want)
			}
		})
	}

	_, err := CanMatchEmpty(Guard(MatchEvent("A"), func() bool { return true }))
	if !errors.Is(err, ErrOpaquePattern) {
		t.Fatalf("expected ErrOpaquePattern for opaque guard, got %v", err)
	}
}
