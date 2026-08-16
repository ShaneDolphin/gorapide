package pattern

import (
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestScopeUnqualifiedEventSourcesScopesMixedPatternLeavesWithoutMutation(t *testing.T) {
	number := Var("N").WithType("Integer")
	peer := Var("Peer").WithType("Factory")
	original := Union(
		MatchEvent("Local").BindParam("step", number),
		MatchEvent("Closing").BindModuleSource(peer).BindParam("step", number),
	)
	originalKey, err := DeterministicKey(original)
	if err != nil {
		t.Fatal(err)
	}
	if !IsContextualNonemptyEventPattern(original) {
		t.Fatal("mixed local/module-qualified compound pattern was not recognized")
	}

	scoped, err := ScopeUnqualifiedEventSources(original, "factory#child")
	if err != nil {
		t.Fatal(err)
	}
	references, err := BasicEventReferences(scoped)
	if err != nil {
		t.Fatal(err)
	}
	sources := make(map[string][]string, len(references))
	for _, reference := range references {
		sources[reference.Action] = reference.Sources
	}
	if got := sources["Local"]; len(got) != 1 || got[0] != "factory#child" {
		t.Fatalf("scoped Local sources=%v", got)
	}
	if got := sources["Closing"]; len(got) != 0 {
		t.Fatalf("module-qualified Closing acquired fixed sources=%v", got)
	}
	afterKey, err := DeterministicKey(original)
	if err != nil || afterKey != originalKey {
		t.Fatalf("source specialization mutated canonical input before=%q after=%q err=%v", originalKey, afterKey, err)
	}

	fixed := Union(
		MatchEvent("Fixed").WhereSource("named"),
		MatchEvent("Closing").BindModuleSource(peer),
	)
	fixedScoped, err := ScopeUnqualifiedEventSources(fixed, "owner")
	if err != nil {
		t.Fatal(err)
	}
	fixedReferences, err := BasicEventReferences(fixedScoped)
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range fixedReferences {
		if reference.Action == "Fixed" && (len(reference.Sources) != 1 || reference.Sources[0] != "named") {
			t.Fatalf("fixed source was replaced: %#v", reference)
		}
	}

	_, err = ScopeUnqualifiedEventSources(MatchEvent("Opaque").Where(func(*gorapide.Event) bool { return true }), "owner")
	if !errors.Is(err, ErrOpaquePattern) {
		t.Fatalf("opaque source specialization error=%v", err)
	}
}

func TestIsContextualNonemptyEventPatternRejectsOpenOrNoncontextualForms(t *testing.T) {
	peer := Var("Peer").WithType("Factory")
	qualified := MatchEvent("Closing").BindModuleSource(peer)
	tests := []struct {
		name       string
		expression Pattern
	}{
		{name: "unqualified", expression: MatchEvent("Local")},
		{name: "match-any", expression: Union(MatchAny(), qualified)},
		{name: "fixed", expression: Union(MatchEvent("Local").WhereSource("fixed"), qualified)},
		{name: "empty", expression: IterateRange(qualified, RelationUnion, 0, 0)},
		{name: "universal", expression: ForAllIntegerRange(
			Var("N").WithType("Integer"), 1, 1, RelationUnion,
			MatchEvent("Closing").BindModuleSource(peer).BindParam("step", Var("N").WithType("Integer")),
		)},
	}
	for _, test := range tests {
		if IsContextualNonemptyEventPattern(test.expression) {
			t.Fatalf("%s unexpectedly entered contextual dynamic subset", test.name)
		}
	}
}
