package pattern

import (
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestMatchWholeRejectsExistentialSubset(t *testing.T) {
	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "Write", "first", nil)
	addBindingTestEvent(t, poset, "Write", "second", nil)

	if matches, err := MatchWithBindings(MatchEvent("Write"), poset); err != nil || len(matches) != 2 {
		t.Fatalf("ordinary matching = %d, %v; want two existential matches", len(matches), err)
	}
	whole, err := MatchWhole(MatchEvent("Write"), poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 0 {
		t.Fatalf("single-event pattern exactly matched a two-event associated computation")
	}
}

func TestMatchWholeRequiresExactAssociatedSubcomputation(t *testing.T) {
	poset := gorapide.NewPoset()
	start := addBindingTestEvent(t, poset, "Start", "start", nil)
	addBindingTestEvent(t, poset, "End", "end", nil, start.ID)
	addBindingTestEvent(t, poset, "Unrelated", "unrelated", nil)

	matches, err := MatchWhole(Seq(MatchEvent("Start"), MatchEvent("End")), poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d exact matches, want 1", len(matches))
	}
	if len(matches[0].Events) != 2 {
		t.Fatalf("unrelated event entered constrained subcomputation: %v", matches[0].Events.IDs())
	}

	addBindingTestEvent(t, poset, "End", "extra-end", nil, start.ID)
	matches, err = MatchWhole(Seq(MatchEvent("Start"), MatchEvent("End")), poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatal("existential Start/End pair passed despite an unmatched associated End event")
	}
}

func TestMatchWholeIncludesBindingMismatchesInAssociatedEvents(t *testing.T) {
	poset := gorapide.NewPoset()
	start := addBindingTestEvent(t, poset, "Start", "start", map[string]any{"subject": "alpha"})
	addBindingTestEvent(t, poset, "End", "alpha", map[string]any{"subject": "alpha"}, start.ID)
	addBindingTestEvent(t, poset, "End", "beta", map[string]any{"subject": "beta"}, start.ID)
	expression := Seq(
		MatchEvent("Start").BindParam("subject", Var("S")),
		MatchEvent("End").BindParam("subject", Var("S")),
	)
	matches, err := MatchWhole(expression, poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatal("cross-placeholder mismatch was omitted from the associated subcomputation")
	}
}
