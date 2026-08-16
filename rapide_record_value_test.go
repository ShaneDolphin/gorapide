package gorapide

import (
	"bytes"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestRapideRecordValuePreservesAllocationAndCanonicalFields(t *testing.T) {
	provenance := moduleValueTestProvenance()
	left, err := NewRapideRecordValue(provenance,
		RapideRecordObjectField("Name", "Ada"),
		RapideRecordObjectField("Level", int32(3)),
	)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewRapideRecordValue(provenance,
		RapideRecordObjectField("LEVEL", int64(3)),
		RapideRecordObjectField("name", "Ada"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !SameRapideRecord(left, right) {
		t.Fatal("same record-literal allocation provenance changed identity")
	}
	leftCanonical := mustCanonicalValueJSON(t, left)
	rightCanonical := mustCanonicalValueJSON(t, right)
	if !bytes.Equal(leftCanonical, rightCanonical) {
		t.Fatalf("field order/case changed Record value:\n%s\n%s", leftCanonical, rightCanonical)
	}
	if names := left.FieldNames(); len(names) != 2 || names[0] != "level" || names[1] != "name" {
		t.Fatalf("canonical Record field names = %#v", names)
	}
	name, ok, err := left.Field("NAME")
	if err != nil || !ok || name != "Ada" {
		t.Fatalf("Record selection = %#v, %v, %v", name, ok, err)
	}
	if _, ok, err := left.Field("Missing"); err != nil || ok {
		t.Fatalf("missing Record selection = %v, %v", ok, err)
	}
}

func TestRapideRecordValueIdentityIsNotStructuralEquality(t *testing.T) {
	firstProvenance := moduleValueTestProvenance()
	secondProvenance := firstProvenance
	secondProvenance.Occurrence = "record-literal:2"
	first, err := NewRapideRecordValue(firstProvenance, RapideRecordObjectField("Value", 1))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRapideRecordValue(secondProvenance, RapideRecordObjectField("Value", 1))
	if err != nil {
		t.Fatal(err)
	}
	if SameRapideRecord(first, second) {
		t.Fatal("distinct Record literal evaluations share allocation identity")
	}
	if first.Identity() == second.Identity() {
		t.Fatal("distinct Record identity strings are equal")
	}
	if equal, err := CanonicalValuesEqual(first, second); err != nil || equal {
		t.Fatalf("tool-level canonical Record equality = %v, %v", equal, err)
	}
}

func TestRapideRecordValueCanonicalRoundTripAndDefensiveSelection(t *testing.T) {
	nested := map[string]any{"items": []any{int32(1), "two"}}
	record, err := NewRapideRecordValue(moduleValueTestProvenance(),
		RapideRecordObjectField("Nested", nested),
	)
	if err != nil {
		t.Fatal(err)
	}
	nested["items"].([]any)[0] = int64(99)
	selected, ok, err := record.Field("nested")
	if err != nil || !ok {
		t.Fatal(err)
	}
	selectedMap := selected.(map[string]any)
	selectedMap["items"].([]any)[0] = int64(77)
	again, _, err := record.Field("nested")
	if err != nil {
		t.Fatal(err)
	}
	if got := again.(map[string]any)["items"].([]any)[0]; got != int64(1) {
		t.Fatalf("Record field mutated through input/selection alias: %#v", got)
	}

	encoded, err := EncodeCanonicalValue(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCanonicalValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	replayed, ok := decoded.(RapideRecordValue)
	if !ok || !SameRapideRecord(record, replayed) {
		t.Fatalf("decoded Record = %#v", decoded)
	}
	if got, ok, err := replayed.Field("Nested"); err != nil || !ok || got == nil {
		t.Fatalf("decoded Record field = %#v, %v, %v", got, ok, err)
	}
}

func TestRapideRecordValueRejectsMalformedFields(t *testing.T) {
	tests := []struct {
		name   string
		fields []RapideRecordField
		want   string
	}{
		{name: "bad name", fields: []RapideRecordField{RapideRecordObjectField("9bad", 1)}, want: "field 0"},
		{name: "duplicate", fields: []RapideRecordField{
			RapideRecordObjectField("Name", "Ada"), RapideRecordObjectField("name", "Grace"),
		}, want: `duplicate field "name"`},
		{name: "noncanonical value", fields: []RapideRecordField{
			RapideRecordObjectField("Channel", make(chan int)),
		}, want: "deterministic value algebra"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRapideRecordValue(moduleValueTestProvenance(), test.fields...)
			if !errors.Is(err, ErrInvalidRapideRecordValue) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want ErrInvalidRapideRecordValue containing %q", err, test.want)
			}
		})
	}
}

func TestRapideRecordValueSatisfiesStructuralObjectMembership(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	integerType := mustRapidePredefinedType(t, "Integer")
	recordType := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Level", integerType),
	)
	valid, err := NewRapideRecordValue(moduleValueTestProvenance(),
		RapideRecordObjectField("Extra", true),
		RapideRecordObjectField("LEVEL", int32(3)),
		RapideRecordObjectField("name", "Ada"),
	)
	if err != nil {
		t.Fatal(err)
	}
	denotation, err := NewRapideObjectDenotation("Employee", recordType, valid)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := denotation.Value()
	if err != nil {
		t.Fatal(err)
	}
	if replayed, ok := decoded.(RapideRecordValue); !ok || !SameRapideRecord(valid, replayed) {
		t.Fatalf("Record membership value = %#v", decoded)
	}

	for _, test := range []struct {
		name   string
		fields []RapideRecordField
		want   string
	}{
		{name: "missing", fields: []RapideRecordField{RapideRecordObjectField("Name", "Ada")}, want: `does not supply field "level"`},
		{name: "wrong type", fields: []RapideRecordField{
			RapideRecordObjectField("Name", "Ada"), RapideRecordObjectField("Level", false),
		}, want: `field "level" is not a member of Integer`},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := NewRapideRecordValue(moduleValueTestProvenance(), test.fields...)
			if err != nil {
				t.Fatal(err)
			}
			_, err = NewRapideObjectDenotation("Employee", recordType, value)
			if !errors.Is(err, ErrInvalidRapideMembership) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("membership error = %v", err)
			}
		})
	}
}

func TestRapideRecordValueCanonicalBytesIgnoreFieldOrderAndGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for iteration := 0; iteration < 30; iteration++ {
		if iteration == 15 {
			runtime.GOMAXPROCS(8)
		}
		fields := []RapideRecordField{
			RapideRecordObjectField("A", false),
			RapideRecordObjectField("B", int32(2)),
		}
		if iteration%2 != 0 {
			fields = []RapideRecordField{
				RapideRecordObjectField("b", int64(2)),
				RapideRecordObjectField("a", false),
			}
		}
		record, err := NewRapideRecordValue(moduleValueTestProvenance(), fields...)
		if err != nil {
			t.Fatal(err)
		}
		encoded := mustCanonicalValueJSON(t, record)
		if baseline == nil {
			baseline = encoded
		} else if !bytes.Equal(encoded, baseline) {
			t.Fatalf("field order/case/GOMAXPROCS changed Record bytes:\n%s\n%s", baseline, encoded)
		}
	}
}

func TestRapideRecordValueEventIdentityUsesAllocationAndCanonicalFields(t *testing.T) {
	recordProvenance := moduleValueTestProvenance()
	left, err := NewRapideRecordValue(recordProvenance,
		RapideRecordObjectField("Name", "Ada"),
		RapideRecordObjectField("Level", int32(3)),
	)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewRapideRecordValue(recordProvenance,
		RapideRecordObjectField("level", int64(3)),
		RapideRecordObjectField("NAME", "Ada"),
	)
	if err != nil {
		t.Fatal(err)
	}
	eventProvenance := EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "records", Instance: "worker",
		Action: "Observed", Occurrence: "1",
	}
	first, err := NewDeterministicEvent(eventProvenance, map[string]any{"record": left})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewDeterministicEvent(eventProvenance, map[string]any{"record": right})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("canonical Record spelling changed event identity: %s != %s", first.ID, second.ID)
	}
	recordProvenance.Occurrence = "record-literal:other"
	other, err := NewRapideRecordValue(recordProvenance,
		RapideRecordObjectField("Name", "Ada"),
		RapideRecordObjectField("Level", 3),
	)
	if err != nil {
		t.Fatal(err)
	}
	third, err := NewDeterministicEvent(eventProvenance, map[string]any{"record": other})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == third.ID {
		t.Fatal("distinct Record allocation identity did not affect event identity")
	}
}

func TestRapideRecordValueCanonicalDecoderRejectsNoncanonicalFields(t *testing.T) {
	record, err := NewRapideRecordValue(moduleValueTestProvenance(),
		RapideRecordObjectField("A", 1), RapideRecordObjectField("B", 2),
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeCanonicalValue(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*CanonicalValue){
		func(value *CanonicalValue) { value.Fields[0], value.Fields[1] = value.Fields[1], value.Fields[0] },
		func(value *CanonicalValue) { value.Fields[1].Name = value.Fields[0].Name },
		func(value *CanonicalValue) { value.Fields[0].Name = "A" },
		func(value *CanonicalValue) { value.Text = "not-a-module" },
	} {
		candidate := copyCanonicalMembershipValue(encoded)
		mutate(&candidate)
		if _, err := DecodeCanonicalValue(candidate); err == nil {
			t.Fatalf("accepted malformed canonical Record: %#v", candidate)
		}
	}
}

func mustCanonicalValueJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := EncodeCanonicalValue(value)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
