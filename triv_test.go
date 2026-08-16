package gorapide

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRapideUnitHasDistinctStrictCanonicalIdentity(t *testing.T) {
	unit := RapideUnit()
	encoded, err := EncodeCanonicalValue(unit)
	if err != nil {
		t.Fatal(err)
	}
	if encoded.Kind != "triv" || encoded.Text != "" || encoded.Bool || len(encoded.Items) != 0 || len(encoded.Fields) != 0 {
		t.Fatalf("Unit canonical value=%#v, want kind triv only", encoded)
	}
	decoded, err := DecodeCanonicalValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded.(RapideTriv); !ok {
		t.Fatalf("decoded Unit has host type %T, want RapideTriv", decoded)
	}

	malformed := encoded
	malformed.Text = "Unit"
	if _, err := DecodeCanonicalValue(malformed); err == nil {
		t.Fatal("noncanonical Triv text field was accepted")
	}
	bytes, err := json.Marshal(encoded)
	if err != nil || string(bytes) != `{"kind":"triv"}` {
		t.Fatalf("Unit canonical JSON=%s err=%v", bytes, err)
	}

	for _, other := range []any{nil, false, true, "Unit", int64(0)} {
		equal, err := CanonicalValuesEqual(unit, other)
		if err != nil || equal {
			t.Fatalf("Unit unexpectedly equals %#v: equal=%t err=%v", other, equal, err)
		}
	}
}

func TestRapideUnitMembershipTypeAndEventIdentity(t *testing.T) {
	unit := RapideUnit()
	if !IsSupportedPredefinedType("Triv") || !CanonicalValueMatchesPredefinedType(unit, "Triv") {
		t.Fatal("Unit does not satisfy supported Triv membership")
	}
	for _, wrong := range []string{"Root", "Boolean", "Integer", "String"} {
		if CanonicalValueMatchesPredefinedType(unit, wrong) {
			t.Fatalf("Unit unexpectedly satisfies %s", wrong)
		}
	}
	typ, err := RapidePredefinedType("tRiV")
	if err != nil {
		t.Fatal(err)
	}
	if name, ok := typ.PredefinedName(); !ok || name != "Triv" {
		t.Fatalf("Triv descriptor name=%q ok=%t", name, ok)
	}

	provenance := EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "triv", Instance: "worker",
		Action: "Observed", Occurrence: "1",
	}
	withUnit, err := NewDeterministicEvent(provenance, map[string]any{"value": unit})
	if err != nil {
		t.Fatal(err)
	}
	withNull, err := NewDeterministicEvent(provenance, map[string]any{"value": nil})
	if err != nil {
		t.Fatal(err)
	}
	if withUnit.ID == withNull.ID {
		t.Fatal("Unit and null collapsed to one deterministic event identity")
	}
	poset := NewPoset()
	if err := poset.AddEvent(withUnit); err != nil {
		t.Fatal(err)
	}
	before, err := poset.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ParseCanonicalPoset(before)
	if err != nil {
		t.Fatal(err)
	}
	after, err := restored.MarshalCanonical()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("Unit poset round trip changed bytes: err=%v\nbefore=%s\nafter=%s", err, before, after)
	}
}
