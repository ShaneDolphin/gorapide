package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

const universalConstraintSource = `
type Worker is interface action out Write(value : Integer); end interface Worker;
architecture System() is worker : Worker;
constraint OrderedDomain: match
  (!D : Integer range 1..3 by ->) worker.Write(!D);
end architecture System;
`

func TestSourceUniversalIntegerRangeConstraintIsExactAndReplayable(t *testing.T) {
	model, err := Compile([]byte(universalConstraintSource), "System")
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
	if result.Constraints == nil || !result.Constraints.Passed || len(result.Constraints.Reports) != 1 {
		t.Fatalf("universal constraint report=%#v", result.Constraints)
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
		t.Fatal("universal constraint replay was not byte-identical")
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
		t.Fatalf("duplicate in-domain value passed exact universal match: %#v", failed.Constraints)
	}
}

func TestSourceUniversalRangeCanTriggerOneClosedConnectionResult(t *testing.T) {
	model, err := Compile([]byte(`
type Source is interface action out Write(value : Integer; batch : String); end interface Source;
type Sink is interface action in Complete(batch : String); end interface Sink;
architecture System() is source : Source; sink : Sink;
connect (!D : Integer range -1..1 by ->; ?B : String)
  source.Write(!D, ?B) ||> sink.Complete(?B);
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
		t.Fatalf("universal connection generated %d Complete events, want 1", len(complete))
	}
	if batch, ok := complete[0].Param("batch"); !ok || batch != "A" {
		t.Fatalf("Complete batch=%v, %t; want A", batch, ok)
	}
	for _, write := range result.Poset.ByName("Write") {
		if !result.Poset.IsCausallyBefore(write.ID, complete[0].ID) {
			t.Fatalf("Complete does not depend on universal match member %s", write.ID)
		}
	}
}

func TestSourceUniversalRangeCanTriggerAClosedBehaviorRule(t *testing.T) {
	model, err := Compile([]byte(`
type Worker is interface
  action out Write(value : Integer; batch : String);
  action out Complete(batch : String);
  behavior begin
    (!D : Integer range 1..2 by ->; ?B : String)
      Write(!D, ?B) ||> Complete(?B);;
end interface Worker;
architecture System() is worker : Worker; end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "one", Source: "worker", Action: "Write", Params: map[string]any{"value": 1, "batch": "B"}},
		arch.InputEvent{Key: "two", Source: "worker", Action: "Write", Params: map[string]any{"value": 2, "batch": "B"}, Causes: []string{"one"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	complete := result.Poset.ByName("Complete")
	if len(complete) != 1 {
		t.Fatalf("universal rule generated %d Complete events, want 1", len(complete))
	}
	if batch, ok := complete[0].Param("batch"); !ok || batch != "B" {
		t.Fatalf("Complete batch=%v, %t; want B", batch, ok)
	}
}

func TestUniversalSourceArtifactIgnoresInputOrderAndGOMAXPROCS(t *testing.T) {
	model, err := Compile([]byte(universalConstraintSource), "System")
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
		for iteration := 0; iteration < 20; iteration++ {
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
				t.Fatalf("universal artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

func TestSourceUniversalQualificationRejectsAmbiguousOrUnclosedForms(t *testing.T) {
	template := func(constraint string) []byte {
		return []byte(`
type Worker is interface action out Write(value : Integer); end interface Worker;
architecture System() is worker : Worker; constraint ` + constraint + ` end architecture System;
`)
	}
	tests := []struct {
		name   string
		body   string
		needle string
	}{
		{"missing-by", "match (!D : Integer range 1..3) worker.Write(!D);", "by"},
		{"wrong-domain-type", "match (!D : Boolean range 1..3 by ->) worker.Write(!D);", "requires Integer"},
		{"empty-domain", "match (!D : Integer range 3..1 by ->) worker.Write(!D);", "must be nonempty"},
		{"bounded-domain", "match (!D : Integer range 0..256 by ->) worker.Write(!D);", "at most 256"},
		{"multiple-universal", "match (!D : Integer range 1..2 by ->; !E : Integer range 1..2 by ~) worker.Write(!D);", "one universal"},
		{"wrong-reference-marker", "match (!D : Integer range 1..2 by ->) worker.Write(?D);", "referenced as !"},
		{"unbound-universal", "match (!D : Integer range 1..2 by ->) worker.Write;", "does not occur"},
		{"nested-qualification", "match ((!D : Integer range 1..2 by ->) worker.Write(!D));", "nested placeholder"},
		{"universal-guard", "match (!D : Integer range 1..2 by ->) worker.Write(!D) where !D = 1;", "unavailable to a whole-match guard"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile(template(test.body), "System")
			if err == nil || !strings.Contains(err.Error(), test.needle) {
				t.Fatalf("error=%v, want substring %q", err, test.needle)
			}
		})
	}
}
