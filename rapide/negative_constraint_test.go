package rapide

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
	"github.com/ShaneDolphin/gorapide/constraint"
)

func TestSourceNotMatchRejectsOnlyAnExactAssociatedComputation(t *testing.T) {
	compile := func() *arch.Architecture {
		t.Helper()
		model, err := Compile([]byte(`
type Worker is interface action out X(); end interface Worker;
architecture System() is worker : Worker;
constraint Policy: not match worker.X;
end architecture System;
`), "System")
		if err != nil {
			t.Fatal(err)
		}
		return model
	}

	one := compile()
	oneDigest, err := one.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	oneJournal := arch.NewExecutionJournal(oneDigest, 10,
		arch.InputEvent{Key: "x-1", Source: "worker", Action: "X"},
	)
	oneResult, err := one.ExecuteDeterministic(oneJournal)
	if err != nil {
		t.Fatal(err)
	}
	if oneResult.Constraints == nil || oneResult.Constraints.Passed || len(oneResult.Constraints.Reports) != 1 ||
		len(oneResult.Constraints.Reports[0].Violations) != 1 {
		t.Fatalf("one-X negative match report=%#v", oneResult.Constraints)
	}
	violation := oneResult.Constraints.Reports[0].Violations[0]
	if violation.Kind != constraint.MustNotMatch.String() || violation.Clause != "source" ||
		!strings.Contains(violation.Constraint, "label:policy") {
		t.Fatalf("negative source violation=%#v", violation)
	}
	encoded, err := oneResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := oneResult.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := one.ReplayDeterministic(oneJournal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, replayBytes) {
		t.Fatal("negative source constraint replay was not byte-identical")
	}

	two := compile()
	twoDigest, err := two.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	twoResult, err := two.ExecuteDeterministic(arch.NewExecutionJournal(twoDigest, 10,
		arch.InputEvent{Key: "x-1", Source: "worker", Action: "X"},
		arch.InputEvent{Key: "x-2", Source: "worker", Action: "X"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if twoResult.Constraints == nil || !twoResult.Constraints.Passed {
		t.Fatalf("two-X non-exact computation failed negative match: %#v", twoResult.Constraints)
	}
}

func TestGroupedNotMatchAndNeverRemainSemanticallyDistinct(t *testing.T) {
	model, err := Compile([]byte(`
type Worker is interface action out X(); end interface Worker;
architecture System() is worker : Worker;
constraint Policy: observe from worker.X
  NotExactlyOne: not match worker.X;
  NoOccurrence: never worker.X;
end observe;
end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "x-1", Source: "worker", Action: "X"},
		arch.InputEvent{Key: "x-2", Source: "worker", Action: "X"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || result.Constraints.Passed || len(result.Constraints.Reports) != 1 {
		t.Fatalf("grouped negative/never report=%#v", result.Constraints)
	}
	violations := result.Constraints.Reports[0].Violations
	if len(violations) != 2 {
		t.Fatalf("never should reject both X occurrences: %#v", violations)
	}
	for _, violation := range violations {
		if violation.Kind != constraint.MustNever.String() || violation.Clause != "label:nooccurrence" {
			t.Fatalf("not-match was conflated with never: %#v", violations)
		}
	}
}

func TestSourceRejectsMalformedNegativeMatchConstraints(t *testing.T) {
	for _, declaration := range []string{
		"not never worker.X;",
		"Policy: not never worker.X;",
		"not worker.X;",
	} {
		source := []byte(`
type Worker is interface action out X(); end interface Worker;
architecture System() is worker : Worker; constraint ` + declaration + ` end architecture System;
`)
		_, err := Compile(source, "System")
		if err == nil {
			t.Fatalf("malformed negative constraint %q was accepted", declaration)
		}
	}
}
