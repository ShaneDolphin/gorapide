package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

const namedIterationConstraintSource = `
type Worker is interface action out Write(value : Integer); end interface Worker;
architecture System() is worker : Worker;
constraint OrderedRange: match [D : 1..3 rel ->] worker.Write(D);
end architecture System;
`

func TestSourceNamedIntegerRangeIterationIsExactAndReplayable(t *testing.T) {
	model, err := Compile([]byte(namedIterationConstraintSource), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "one", Source: "worker", Action: "Write", Params: map[string]any{"value": 1}},
		arch.InputEvent{Key: "two", Source: "worker", Action: "Write", Params: map[string]any{"value": 2}, Causes: []string{"one"}},
		arch.InputEvent{Key: "three", Source: "worker", Action: "Write", Params: map[string]any{"value": 3}, Causes: []string{"two"}},
		arch.InputEvent{Key: "outside", Source: "worker", Action: "Write", Params: map[string]any{"value": 99}, Causes: []string{"three"}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || !result.Constraints.Passed {
		t.Fatalf("named iteration constraint report=%#v", result.Constraints)
	}
	encoded, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, replayBytes) {
		t.Fatal("named iteration replay was not byte-identical")
	}

	duplicate := journal
	duplicate.Inputs = append(append([]arch.InputEvent(nil), journal.Inputs...), arch.InputEvent{
		Key: "two-again", Source: "worker", Action: "Write", Params: map[string]any{"value": 2}, Causes: []string{"one"},
	})
	failed, err := model.ExecuteDeterministic(duplicate)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Constraints == nil || failed.Constraints.Passed {
		t.Fatalf("duplicate iterator value passed exact match: %#v", failed.Constraints)
	}
}

func TestSourceUnnamedIntegerRangeIterationUsesItsCardinality(t *testing.T) {
	model, err := Compile([]byte(`
type Worker is interface action out Write(); end interface Worker;
architecture System() is worker : Worker;
constraint Three: match [-1..1 rel ->] worker.Write;
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
		arch.InputEvent{Key: "one", Source: "worker", Action: "Write"},
		arch.InputEvent{Key: "two", Source: "worker", Action: "Write", Causes: []string{"one"}},
		arch.InputEvent{Key: "three", Source: "worker", Action: "Write", Causes: []string{"two"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || !result.Constraints.Passed {
		t.Fatalf("unnamed range iteration report=%#v", result.Constraints)
	}
}

func TestSourceEmptyNamedRangeUsesTheEmptyIteratorIdentity(t *testing.T) {
	model, err := Compile([]byte(`
type Worker is interface action out Write(value : Integer); end interface Worker;
architecture System() is worker : Worker;
constraint Empty: match [D : 2..1 rel ->] worker.Write(D);
end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10))
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || !result.Constraints.Passed {
		t.Fatalf("empty named range report=%#v", result.Constraints)
	}
}

func TestSourceNamedRangeIterationCanTriggerOneConnectionResult(t *testing.T) {
	model, err := Compile([]byte(`
type Source is interface action out Write(value : Integer; batch : String); end interface Source;
type Sink is interface action in Complete(batch : String); end interface Sink;
architecture System() is source : Source; sink : Sink;
connect (?B : String) [D : -1..1 rel ->] source.Write(D, ?B) ||> sink.Complete(?B);
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
		arch.InputEvent{Key: "minus", Source: "source", Action: "Write", Params: map[string]any{"value": -1, "batch": "A"}},
		arch.InputEvent{Key: "zero", Source: "source", Action: "Write", Params: map[string]any{"value": 0, "batch": "A"}, Causes: []string{"minus"}},
		arch.InputEvent{Key: "plus", Source: "source", Action: "Write", Params: map[string]any{"value": 1, "batch": "A"}, Causes: []string{"zero"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	complete := result.Poset.ByName("Complete")
	if len(complete) != 1 {
		t.Fatalf("named iteration connection generated %d Complete events, want 1", len(complete))
	}
	if batch, ok := complete[0].Param("batch"); !ok || batch != "A" {
		t.Fatalf("Complete batch=%v, %t; want A", batch, ok)
	}
}

func TestNamedIterationArtifactIgnoresInputOrderAndGOMAXPROCS(t *testing.T) {
	model, err := Compile([]byte(namedIterationConstraintSource), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	inputs := []arch.InputEvent{
		{Key: "one", Source: "worker", Action: "Write", Params: map[string]any{"value": 1}},
		{Key: "two", Source: "worker", Action: "Write", Params: map[string]any{"value": 2}, Causes: []string{"one"}},
		{Key: "three", Source: "worker", Action: "Write", Params: map[string]any{"value": 3}, Causes: []string{"two"}},
	}
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for _, processors := range []int{1, 4} {
		runtime.GOMAXPROCS(processors)
		for iteration := 0; iteration < 12; iteration++ {
			ordered := append([]arch.InputEvent(nil), inputs...)
			if iteration%2 != 0 {
				for left, right := 0, len(ordered)-1; left < right; left, right = left+1, right-1 {
					ordered[left], ordered[right] = ordered[right], ordered[left]
				}
			}
			result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10, ordered...))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := result.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			if baseline == nil {
				baseline = encoded
			} else if !bytes.Equal(baseline, encoded) {
				t.Fatalf("named iteration artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

func TestSourceNamedIterationRejectsUnclosedForms(t *testing.T) {
	template := func(pattern string, parameterType string) []byte {
		return []byte(`
type Worker is interface action out Write(value : ` + parameterType + `); end interface Worker;
architecture System() is worker : Worker; constraint match ` + pattern + `; end architecture System;
`)
	}
	tests := []struct {
		name          string
		pattern       string
		parameterType string
		needle        string
	}{
		{"missing-range", "[D : 1 rel ->] worker.Write(D)", "Integer", "first '.'"},
		{"wrong-parameter-type", "[D : 1..2 rel ->] worker.Write(D)", "Boolean", "has type Integer"},
		{"wrong-reference-form", "[D : 1..2 rel ->] worker.Write(?D)", "Integer", "not declared"},
		{"placeholder-conflict", "(?D : Integer) [D : 1..2 rel ->] worker.Write(D)", "Integer", "conflicts with a placeholder"},
		{"bounded-range", "[D : 0..256 rel ->] worker.Write(D)", "Integer", "at most 256"},
		{"bounded-unnamed-range", "[0..256 rel ->] worker.Write(1)", "Integer", "deterministic bound of 256"},
		{"bounded-exact-cardinality", "[257 rel ->] worker.Write(1)", "Integer", "deterministic bound of 256"},
		{"general-object-iterator", "[D : Domain rel ->] worker.Write(D)", "Integer", "range lower bound"},
		{"iterator-expression", "[D : 1..2 rel ->] worker.Write(D + 1)", "Integer", "unsupported object"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile(template(test.pattern, test.parameterType), "System")
			if err == nil || !strings.Contains(err.Error(), test.needle) {
				t.Fatalf("error=%v, want substring %q", err, test.needle)
			}
		})
	}
}
