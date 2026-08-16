package gorapide

import (
	"bytes"
	"encoding/json"
	"errors"
	"runtime"
	"testing"
)

func moduleValueTestProvenance() ModuleAllocationProvenance {
	return ModuleAllocationProvenance{
		Profile: "stanford-rapide-1.0", Model: "module-values", Parent: "root",
		Generator: "Airplane", Occurrence: "launch-1",
		Causes: []EventID{"evt-cause-b", "evt-cause-a"},
	}
}

func TestRapideModuleValueIdentityIsCanonicalAllocationProvenance(t *testing.T) {
	provenance := moduleValueTestProvenance()
	first, err := NewRapideModuleValue(provenance)
	if err != nil {
		t.Fatal(err)
	}
	provenance.Causes = []EventID{"evt-cause-a", "evt-cause-b", "evt-cause-a"}
	second, err := NewRapideModuleValue(provenance)
	if err != nil {
		t.Fatal(err)
	}
	if !SameRapideModule(first, second) || first.Identity() != second.Identity() {
		t.Fatalf("cause order or duplication changed allocation identity: %q != %q", first.Identity(), second.Identity())
	}
	if len(first.Identity()) != len(rapideModuleIdentityPrefix)+64 {
		t.Fatalf("module identity %q does not contain one SHA-256 digest", first.Identity())
	}

	provenance.Occurrence = "launch-2"
	third, err := NewRapideModuleValue(provenance)
	if err != nil {
		t.Fatal(err)
	}
	if SameRapideModule(first, third) {
		t.Fatal("distinct module-generator evaluations have the same identity")
	}
	if SameRapideModule(RapideModuleValue{}, RapideModuleValue{}) {
		t.Fatal("invalid zero values must not compare as the same Rapide module")
	}
}

func TestRapideModuleValueRejectsIncompleteOrNoncanonicalIdentity(t *testing.T) {
	base := moduleValueTestProvenance()
	tests := []struct {
		name   string
		mutate func(*ModuleAllocationProvenance)
	}{
		{"profile", func(value *ModuleAllocationProvenance) { value.Profile = "" }},
		{"model", func(value *ModuleAllocationProvenance) { value.Model = "" }},
		{"parent", func(value *ModuleAllocationProvenance) { value.Parent = "" }},
		{"generator", func(value *ModuleAllocationProvenance) { value.Generator = "" }},
		{"occurrence", func(value *ModuleAllocationProvenance) { value.Occurrence = "" }},
		{"cause", func(value *ModuleAllocationProvenance) { value.Causes = []EventID{""} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provenance := base
			test.mutate(&provenance)
			_, err := NewRapideModuleValue(provenance)
			if !errors.Is(err, ErrInvalidRapideModuleValue) {
				t.Fatalf("expected ErrInvalidRapideModuleValue, got %v", err)
			}
		})
	}

	valid, err := NewRapideModuleValue(base)
	if err != nil {
		t.Fatal(err)
	}
	invalid := []string{
		"", "module-" + valid.Identity(), rapideModuleIdentityPrefix + "00",
		rapideModuleIdentityPrefix + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
	for _, identity := range invalid {
		if _, err := ParseRapideModuleValue(identity); !errors.Is(err, ErrInvalidRapideModuleValue) {
			t.Errorf("ParseRapideModuleValue(%q) error = %v, want ErrInvalidRapideModuleValue", identity, err)
		}
	}
}

func TestRapideModuleValueCanonicalEventRoundTrip(t *testing.T) {
	module, err := NewRapideModuleValue(moduleValueTestProvenance())
	if err != nil {
		t.Fatal(err)
	}
	event, err := NewDeterministicEvent(EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "module-values", Instance: "tower",
		Action: "Acknowledge", Occurrence: "1",
	}, map[string]any{
		"airplane": module,
		"nested":   []any{map[string]any{"owner": module}},
	})
	if err != nil {
		t.Fatal(err)
	}
	poset := NewPoset()
	if err := poset.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	before, err := poset.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(before, []byte(`"kind":"module","text":"`+module.Identity()+`"`)) {
		t.Fatalf("canonical event does not carry typed module identity: %s", before)
	}
	restored, err := ParseCanonicalPoset(before)
	if err != nil {
		t.Fatal(err)
	}
	after, err := restored.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("module-valued event changed across replay:\nbefore=%s\nafter=%s", before, after)
	}
	value, ok := restored.All()[0].Param("airplane")
	if !ok {
		t.Fatal("restored event lost module-valued parameter")
	}
	replayed, ok := value.(RapideModuleValue)
	if !ok || !SameRapideModule(module, replayed) {
		t.Fatalf("restored value = %#v, want module %s", value, module.Identity())
	}
}

func TestRapideModuleValueIdentityAndEventBytesIgnoreHostScheduling(t *testing.T) {
	original := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(original)
	var baseline []byte
	for _, processors := range []int{1, 4} {
		runtime.GOMAXPROCS(processors)
		module, err := NewRapideModuleValue(moduleValueTestProvenance())
		if err != nil {
			t.Fatal(err)
		}
		event, err := NewDeterministicEvent(EventProvenance{
			Profile: "stanford-rapide-1.0", Model: "module-values", Instance: "tower",
			Action: "Observe", Occurrence: "stable",
		}, map[string]any{"module": module, "sequence": int32(1)})
		if err != nil {
			t.Fatal(err)
		}
		poset := NewPoset()
		if err := poset.AddEvent(event); err != nil {
			t.Fatal(err)
		}
		encoded, err := poset.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if baseline == nil {
			baseline = encoded
		} else if !bytes.Equal(baseline, encoded) {
			t.Fatalf("GOMAXPROCS=%d changed module-valued event bytes:\n%s\n%s", processors, baseline, encoded)
		}
	}
}

func TestDecodeCanonicalParametersRejectsMalformedModuleIdentity(t *testing.T) {
	params := []CanonicalParameter{{
		Name: "module", Value: CanonicalValue{Kind: "module", Text: rapideModuleIdentityPrefix + "00"},
	}}
	_, err := DecodeCanonicalParameters(params)
	if !errors.Is(err, ErrInvalidRapideModuleValue) {
		t.Fatalf("expected ErrInvalidRapideModuleValue, got %v", err)
	}

	encoded, err := json.Marshal(params)
	if err != nil || len(encoded) == 0 {
		t.Fatalf("test fixture did not encode: %s, %v", encoded, err)
	}
}
