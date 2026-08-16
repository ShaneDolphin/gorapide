package pattern

import (
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestZeroOrMoreFollowsEnumeratesAllOrderedSubcomputations(t *testing.T) {
	poset := gorapide.NewPoset()
	first := addBindingTestEvent(t, poset, "Balance", "first", nil)
	second := addBindingTestEvent(t, poset, "Balance", "second", nil, first.ID)
	addBindingTestEvent(t, poset, "Balance", "third", nil, second.ID)

	matches, err := MatchWithBindings(
		IterateZeroOrMore(MatchEvent("Balance"), RelationFollows), poset,
	)
	if err != nil {
		t.Fatal(err)
	}
	// The Pattern LRM example specifies 2^3 matches: empty, three
	// singletons, three ordered pairs, and the ordered triple.
	if len(matches) != 8 {
		t.Fatalf("got %d matches, want 8", len(matches))
	}
}

func TestZeroOrMoreDisjointEnumeratesEverySubset(t *testing.T) {
	poset := gorapide.NewPoset()
	for _, occurrence := range []string{"first", "second", "third"} {
		addBindingTestEvent(t, poset, "ReadReturn", occurrence, nil)
	}
	matches, err := MatchWithBindings(
		IterateZeroOrMore(MatchEvent("ReadReturn"), RelationDisjoint), poset,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 8 {
		t.Fatalf("three disjoint occurrences yielded %d subsets, want 8", len(matches))
	}
	oneOrMore, err := MatchWithBindings(
		IterateOneOrMore(MatchEvent("ReadReturn"), RelationDisjoint), poset,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(oneOrMore) != 7 {
		t.Fatalf("one-or-more yielded %d matches, want 7", len(oneOrMore))
	}
}

func TestDisjointRangeSupportsExactWholeComputationConstraint(t *testing.T) {
	three := gorapide.NewPoset()
	for _, occurrence := range []string{"first", "second", "third"} {
		addBindingTestEvent(t, three, "WriteReturn", occurrence, nil)
	}
	exactlyTwo := IterateRange(MatchEvent("WriteReturn"), RelationDisjoint, 2, 2)
	matches, err := MatchWhole(exactlyTwo, three)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatal("two-occurrence iteration exactly matched three associated events")
	}

	two := gorapide.NewPoset()
	for _, occurrence := range []string{"first", "second"} {
		addBindingTestEvent(t, two, "WriteReturn", occurrence, nil)
	}
	matches, err = MatchWhole(exactlyTwo, two)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || len(matches[0].Events) != 2 {
		t.Fatalf("two-occurrence computation yielded %#v", matches)
	}
}

func TestIterationSharesEnclosingPlaceholderBindings(t *testing.T) {
	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "Observed", "alpha", map[string]any{"subject": "alpha"})
	addBindingTestEvent(t, poset, "Observed", "beta", map[string]any{"subject": "beta"})
	expression := IterateZeroOrMore(
		MatchEvent("Observed").BindParam("subject", Var("S").WithType("String")),
		RelationDisjoint,
	)
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	// Empty plus two singleton matches. The two-event match is rejected because
	// the same ?S occurrence cannot bind both alpha and beta.
	if len(matches) != 3 {
		t.Fatalf("got %d matches, want empty plus two typed singletons", len(matches))
	}
}

func TestIterationStarMatchesEmptyComputation(t *testing.T) {
	poset := gorapide.NewPoset()
	matches, err := MatchWhole(
		IterateZeroOrMore(MatchEvent("Absent"), RelationDisjoint), poset,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || len(matches[0].Events) != 0 {
		t.Fatalf("star did not match empty computation: %#v", matches)
	}
}

func TestLargeIdempotentIterationReachesSemanticFixedPoint(t *testing.T) {
	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "A", "one", nil)
	matches, err := MatchWithBindings(IterateRange(MatchEvent("A"), RelationUnion, 10_000, 10_000), poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || len(matches[0].Events) != 1 {
		t.Fatalf("idempotent union did not reach fixed point: %#v", matches)
	}
}

func TestIterationDeterministicKeyIncludesRelationAndCardinality(t *testing.T) {
	star, err := DeterministicKey(IterateZeroOrMore(MatchEvent("A"), RelationDisjoint))
	if err != nil {
		t.Fatal(err)
	}
	plus, err := DeterministicKey(IterateOneOrMore(MatchEvent("A"), RelationDisjoint))
	if err != nil {
		t.Fatal(err)
	}
	if star == plus {
		t.Fatal("star and plus iteration produced the same model key")
	}
}
