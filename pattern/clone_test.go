package pattern

import (
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestCloneDeterministicOwnsEveryAdmittedPatternForm(t *testing.T) {
	value := Var("value").WithType("Integer")
	start := Var("start").WithType("Integer")
	duration := Var("duration").WithType("Integer")
	base := MatchEvent("A").WhereParam("n", int64(1)).BindParam("n", value)
	tests := []Pattern{
		base,
		Seq(base, MatchEvent("B")),
		ImmSeq(base, MatchEvent("B")),
		Independent(base, MatchEvent("B")),
		Disjoint(base, MatchEvent("B")),
		Union(base, MatchEvent("B")),
		Or(base, MatchEvent("B")),
		And(base, base),
		IterateRange(base, RelationDisjoint, 1, 2),
		IterateZeroOrMore(base, RelationDisjoint),
		IterateOneOrMore(base, RelationDisjoint),
		IterateIntegerRange(value, 1, 2, RelationDisjoint, base),
		ForAllIntegerRange(value, 1, 2, RelationDisjoint, base),
		Timing(base, start, duration, "Clock"),
		RapideAt(base, 1, "Clock"),
		RapideBefore(base, 1, "Clock"),
		RapideAfter(base, 1, "Clock"),
		RapideWithin(base, 1, "Clock"),
		RapideWithinRange(base, 1, 2, "Clock"),
		RapideTimeBefore(base, MatchEvent("B"), "Clock"),
		Where(base, BinaryCondition(ConditionEqual, BindingCondition(value), LiteralCondition(int64(1)))),
	}
	for _, original := range tests {
		before, err := DeterministicKey(original)
		if err != nil {
			t.Fatalf("original %T: %v", original, err)
		}
		clone, err := CloneDeterministic(original)
		if err != nil {
			t.Fatalf("clone %T: %v", original, err)
		}
		after, err := DeterministicKey(clone)
		if err != nil {
			t.Fatalf("clone key %T: %v", original, err)
		}
		if before != after {
			t.Fatalf("clone %T changed key\nbefore=%s\nafter=%s", original, before, after)
		}
	}
}

func TestCloneDeterministicIsIndependentFromSourceMutation(t *testing.T) {
	source := MatchEvent("A").WhereSource("left").WhereParam("n", int64(1))
	clone, err := CloneDeterministic(source)
	if err != nil {
		t.Fatal(err)
	}
	cloneKey, err := DeterministicKey(clone)
	if err != nil {
		t.Fatal(err)
	}
	source.WhereSource("right").WhereParam("n", int64(2)).BindParam("n", Var("n").WithType("Integer"))
	stableKey, err := DeterministicKey(clone)
	if err != nil {
		t.Fatal(err)
	}
	if stableKey != cloneKey {
		t.Fatalf("clone changed after source mutation\nbefore=%s\nafter=%s", cloneKey, stableKey)
	}
	if sourceKey, err := DeterministicKey(source); err != nil || sourceKey == cloneKey {
		t.Fatalf("source mutation was not observed: key=%s err=%v", sourceKey, err)
	}
}

func TestCloneDeterministicRejectsOpaquePatterns(t *testing.T) {
	_, err := CloneDeterministic(MatchEvent("A").Where(func(*gorapide.Event) bool { return true }))
	if !errors.Is(err, ErrOpaquePattern) {
		t.Fatalf("error=%v, want ErrOpaquePattern", err)
	}
	_, err = CloneDeterministic(Join(MatchEvent("A"), MatchEvent("B")))
	if !errors.Is(err, ErrOpaquePattern) {
		t.Fatalf("error=%v, want ErrOpaquePattern", err)
	}
}

func TestCloneDeterministicPreservesNilMatchAny(t *testing.T) {
	cloned, err := CloneDeterministic(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cloned != nil {
		t.Fatalf("clone = %T, want nil MatchAny spelling", cloned)
	}
}
