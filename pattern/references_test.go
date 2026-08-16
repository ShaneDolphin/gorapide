package pattern

import (
	"reflect"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestBasicEventReferencesAreCanonicalAndTyped(t *testing.T) {
	placeholder := Var("object").WithType("String")
	left := And(
		MatchEvent("Read").WhereSource("db").BindParam("id", placeholder),
		MatchEvent("Commit").WhereParam("ok", true),
	)
	right := And(
		MatchEvent("Commit").WhereParam("ok", true),
		MatchEvent("Read").WhereSource("db").BindParam("id", Var("object").WithType("String")),
	)
	leftReferences, err := BasicEventReferences(left)
	if err != nil {
		t.Fatal(err)
	}
	rightReferences, err := BasicEventReferences(right)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leftReferences, rightReferences) {
		t.Fatalf("commutative reference order changed:\n%#v\n%#v", leftReferences, rightReferences)
	}
	if len(leftReferences) != 2 || leftReferences[1].Action != "Read" || len(leftReferences[1].Bindings) != 1 {
		t.Fatalf("unexpected references: %#v", leftReferences)
	}
	if leftReferences[0].Action != "Commit" || len(leftReferences[0].Filters) != 1 || leftReferences[0].Filters[0].Parameter != "ok" {
		t.Fatalf("unexpected literal filter reference: %#v", leftReferences[0])
	}
	binding := leftReferences[1].Bindings[0]
	if binding.Parameter != "id" || binding.Placeholder != "object" || binding.Type != "String" {
		t.Fatalf("unexpected binding: %#v", binding)
	}
}

func TestBasicEventReferencesRejectOpaquePatterns(t *testing.T) {
	if _, err := BasicEventReferences(MatchEvent("Read").Where(func(*gorapide.Event) bool { return true })); err == nil {
		t.Fatal("expected opaque callback rejection")
	}
}
