package pattern

import (
	"errors"
	"testing"
)

func TestBoundPlaceholderTypesRejectsConflictingDeclarations(t *testing.T) {
	expression := And(
		MatchEvent("One").BindParam("value", Var("V").WithType("Integer")),
		MatchEvent("Two").BindParam("value", Var("V").WithType("String")),
	)
	if _, err := BoundPlaceholderTypes(expression); !errors.Is(err, ErrOpaquePattern) {
		t.Fatalf("expected conflicting placeholder type error, got %v", err)
	}

	types, err := BoundPlaceholderTypes(And(
		MatchEvent("One").BindParam("value", Var("V")),
		MatchEvent("Two").BindParam("value", Var("V").WithType("Integer")),
	))
	if err != nil {
		t.Fatal(err)
	}
	if types["V"] != "Integer" {
		t.Fatalf("placeholder type=%q, want Integer", types["V"])
	}
}
