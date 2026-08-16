package arch

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func cutTestValue(t *testing.T, value any) gorapide.CanonicalValue {
	t.Helper()
	encoded, err := gorapide.CanonicalizeParameters(map[string]any{"value": value})
	if err != nil {
		t.Fatal(err)
	}
	return encoded[0].Value
}

func ambiguousStateCutComputation(t *testing.T, reverse bool) *AugmentedComputation {
	t.Helper()
	computation := NewAugmentedComputation()
	create := StateOperationRecord{
		ID: "create", ComponentID: "worker", Name: "version", Kind: StateOperationCreate,
		Version: 0, Value: cutTestValue(t, int64(0)), Owner: "elaboration", Causes: []string{},
	}
	assign := StateOperationRecord{
		ID: "assign-1", ComponentID: "worker", Name: "version", Kind: StateOperationAssign,
		Version: 1, Value: cutTestValue(t, int64(1)), Predecessor: "create",
		Owner: "writer", Causes: []string{"left"},
	}
	add := []func() error{
		func() error { return computation.AddEventOccurrence("left") },
		func() error { return computation.AddEventOccurrence("right") },
		func() error { return computation.AddRefOperation(create, true) },
		func() error { return computation.AddRefOperation(assign, false) },
	}
	if reverse {
		for left, right := 0, len(add)-1; left < right; left, right = left+1, right-1 {
			add[left], add[right] = add[right], add[left]
		}
	}
	for _, operation := range add {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	return computation
}

func TestConsistentCutStateWitnessesEnumerateAmbiguousMatchState(t *testing.T) {
	computation := ambiguousStateCutComputation(t, false)
	witnesses, err := computation.ConsistentCutStateWitnesses(
		[]string{"right", "left"},
		ConsistentCutLimits{MaxCuts: 10, MaxOptionalOccurrences: 10},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(witnesses) != 2 {
		t.Fatalf("witnesses=%#v, want two valid match-relative states", witnesses)
	}
	byVersion := make(map[uint64]ConsistentCutStateWitness)
	for _, witness := range witnesses {
		if witness.Digest == "" || len(witness.State) != 1 {
			t.Fatalf("invalid cut witness=%#v", witness)
		}
		byVersion[witness.State[0].Version] = witness
	}
	before := byVersion[0]
	after := byVersion[1]
	if len(before.Anchors) != 2 || before.Anchors[0] != "left" || before.Anchors[1] != "right" ||
		len(before.Occurrences) != 3 || before.State[0].OperationID != "create" {
		t.Fatalf("pre-assignment cut=%#v", before)
	}
	if len(after.Anchors) != 1 || after.Anchors[0] != "right" || len(after.Occurrences) != 4 ||
		after.State[0].OperationID != "assign-1" || after.State[0].Value.Text != "1" {
		t.Fatalf("post-assignment cut=%#v", after)
	}
}

func TestConsistentCutStateWitnessesIgnoreConstructionOrder(t *testing.T) {
	encode := func(reverse bool, match []string) []byte {
		t.Helper()
		witnesses, err := ambiguousStateCutComputation(t, reverse).ConsistentCutStateWitnesses(
			match, ConsistentCutLimits{MaxCuts: 10, MaxOptionalOccurrences: 10},
		)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(witnesses)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	left := encode(false, []string{"left", "right"})
	right := encode(true, []string{"right", "left", "left"})
	if !bytes.Equal(left, right) {
		t.Fatalf("consistent-cut witnesses changed with construction order:\n%s\n%s", left, right)
	}
}

func TestConsistentCutStateWitnessesEnforceExplicitBoundsAndValidateGraph(t *testing.T) {
	computation := ambiguousStateCutComputation(t, false)
	if _, err := computation.ConsistentCutStateWitnesses(
		[]string{"left", "right"}, ConsistentCutLimits{MaxCuts: 1, MaxOptionalOccurrences: 10},
	); !errors.Is(err, ErrConsistentCutLimit) {
		t.Fatalf("cut limit error=%v", err)
	}
	if _, err := computation.ConsistentCutStateWitnesses(
		[]string{"left", "right"}, ConsistentCutLimits{MaxCuts: 10},
	); !errors.Is(err, ErrInvalidAugmentedComputation) {
		t.Fatalf("zero bound error=%v", err)
	}

	cyclic := NewAugmentedComputation()
	if err := cyclic.AddEventOccurrence("a"); err != nil {
		t.Fatal(err)
	}
	if err := cyclic.AddEventOccurrence("b"); err != nil {
		t.Fatal(err)
	}
	if err := cyclic.AddCausalDependency("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := cyclic.AddCausalDependency("b", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cyclic.ConsistentCutStateWitnesses(
		[]string{"a"}, ConsistentCutLimits{MaxCuts: 10, MaxOptionalOccurrences: 10},
	); !errors.Is(err, ErrInvalidAugmentedComputation) {
		t.Fatalf("cycle error=%v", err)
	}
}
