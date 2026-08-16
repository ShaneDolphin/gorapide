package arch

import (
	"bytes"
	"testing"

	"github.com/ShaneDolphin/gorapide/constraint"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func constrainedArchitecture(t *testing.T, reverse bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("constrained")
	component := NewComponent("component", Interface("Component").OutAction("Input").OutAction("Error").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	constraints := []*constraint.Constraint{
		constraint.NewConstraint("input-required").Must("input", pattern.MatchEvent("Input"), "Input must occur").Build(),
		constraint.NewConstraint("error-forbidden").MustNever("error", pattern.MatchEvent("Error"), "Error must not occur").Build(),
	}
	if reverse {
		constraints[0], constraints[1] = constraints[1], constraints[0]
	}
	set := constraint.NewConstraintSet("architecture-policy")
	for _, declaration := range constraints {
		set.Add(declaration)
	}
	architecture.WithConstraints(set, constraint.CheckOnEvent)
	return architecture
}

func TestDeterministicExecutionIncludesCanonicalConstraintDecision(t *testing.T) {
	architecture := constrainedArchitecture(t, false)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 5,
		InputEvent{Key: "input", Source: "component", Action: "Input"},
		InputEvent{Key: "error", Source: "component", Action: "Error"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || result.Constraints.Passed || len(result.Constraints.Reports) != 2 {
		t.Fatalf("missing or incorrect constraint decision: %#v", result.Constraints)
	}
	violations := 0
	for _, report := range result.Constraints.Reports {
		violations += len(report.Violations)
	}
	if violations != 1 {
		t.Fatalf("violations=%d, want 1", violations)
	}
	encoded, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"constraint_report"`)) || !bytes.Contains(encoded, []byte(`"passed":false`)) {
		t.Fatalf("constraint audit is absent from execution artifact: %s", encoded)
	}
}

func TestConstraintDeclarationOrderDoesNotChangeModelOrArtifact(t *testing.T) {
	forward := constrainedArchitecture(t, false)
	reverse := constrainedArchitecture(t, true)
	leftDigest, err := forward.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := reverse.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("constraint declaration order changed model identity: %s != %s", leftDigest, rightDigest)
	}
	journal := NewExecutionJournal(leftDigest, 5,
		InputEvent{Key: "input", Source: "component", Action: "Input"},
	)
	left, err := forward.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	right, err := reverse.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	leftBytes, err := left.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := right.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatalf("constraint declaration order changed artifact:\nleft=%s\nright=%s", leftBytes, rightBytes)
	}
}
