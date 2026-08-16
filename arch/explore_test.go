package arch

import (
	"bytes"
	"errors"
	"testing"
)

func TestExploreDeterministicEnumeratesAndDeduplicatesPermittedChoices(t *testing.T) {
	architecture := ambiguousRuleArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "component", Action: "Input"},
	)
	limits := ExplorationLimits{MaxExecutions: 10, MaxChoiceDepth: 2}
	first, err := architecture.ExploreDeterministic(journal, limits)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Complete {
		t.Fatal("bounded exploration unexpectedly truncated")
	}
	if first.Executions < 3 {
		t.Fatalf("executions=%d, want at least discovery plus two scheduled branches", first.Executions)
	}
	if len(first.Computations) != 1 {
		t.Fatalf("equivalent rule orders should collapse to one semantic poset, got %d", len(first.Computations))
	}
	if len(first.Computations[0].Schedule) == 0 || first.Computations[0].Result == nil {
		t.Fatalf("missing replay witness or execution result: %#v", first.Computations[0])
	}
	second, err := architecture.ExploreDeterministic(journal, limits)
	if err != nil {
		t.Fatal(err)
	}
	left, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	right, err := second.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatalf("repeated exploration differs:\nfirst=%s\nsecond=%s", left, right)
	}
}

func TestExploreDeterministicReportsExplicitBoundTruncation(t *testing.T) {
	architecture := ambiguousRuleArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "component", Action: "Input"},
	)
	result, err := architecture.ExploreDeterministic(journal,
		ExplorationLimits{MaxExecutions: 1, MaxChoiceDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Complete || result.Executions != 1 {
		t.Fatalf("execution bound was not reported: %#v", result)
	}
}

func TestExploreDeterministicRejectsMissingBounds(t *testing.T) {
	architecture := ambiguousRuleArchitecture(t)
	if _, err := architecture.ExploreDeterministic(ExecutionJournal{}, ExplorationLimits{}); !errors.Is(err, ErrInvalidExplorationLimits) {
		t.Fatalf("expected ErrInvalidExplorationLimits, got %v", err)
	}
}
