package gorapide

import (
	"reflect"
	"testing"
)

func TestRapideStringNormalizesUnicodeSequencesCompatibly(t *testing.T) {
	codes := []int64{65, 10, 0x03bb}
	value := RapideStringFromCodes(codes...)
	codes[0] = 0
	if got := value.Codes(); !reflect.DeepEqual(got, []int64{65, 10, 0x03bb}) {
		t.Fatalf("constructor did not copy codes: %#v", got)
	}
	returned := value.Codes()
	returned[0] = 0
	if value.Codes()[0] != 65 {
		t.Fatal("Codes exposed mutable String storage")
	}

	canonical, err := CanonicalizeParams(map[string]any{"value": value})
	if err != nil {
		t.Fatal(err)
	}
	want := "A\nλ"
	if canonical["value"] != want {
		t.Fatalf("Unicode-representable sequence normalized as %#v, want %q", canonical["value"], want)
	}
	encoded, err := EncodeCanonicalValue(value)
	if err != nil {
		t.Fatal(err)
	}
	if encoded.Kind != "string" || encoded.Text != want {
		t.Fatalf("representable String encoding=%#v", encoded)
	}
	equal, err := CanonicalValuesEqual(value, want)
	if err != nil || !equal {
		t.Fatalf("equivalent RapideString/Go string equality=%t error=%v", equal, err)
	}
	if !CanonicalValueMatchesPredefinedType(value, "String") {
		t.Fatal("representable RapideString did not satisfy String membership")
	}
}

func TestRapideStringPreservesArbitraryCharacterCodes(t *testing.T) {
	want := []int64{-1, 65, 0xd800, 0x110000, 1<<63 - 1}
	value := RapideStringFromCodes(want...)
	canonical, err := CanonicalizeParams(map[string]any{"value": value})
	if err != nil {
		t.Fatal(err)
	}
	normalized, ok := canonical["value"].(RapideString)
	if !ok || !reflect.DeepEqual(normalized.Codes(), want) {
		t.Fatalf("arbitrary String normalized as %#v", canonical["value"])
	}
	encoded, err := EncodeCanonicalValue(value)
	if err != nil {
		t.Fatal(err)
	}
	if encoded.Kind != "string-codes" || len(encoded.Items) != len(want) {
		t.Fatalf("arbitrary String encoding=%#v", encoded)
	}
	decoded, err := DecodeCanonicalValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	decodedString, ok := decoded.(RapideString)
	if !ok || !reflect.DeepEqual(decodedString.Codes(), want) {
		t.Fatalf("arbitrary String round trip=%#v", decoded)
	}
	if !CanonicalValueMatchesPredefinedType(decoded, "String") {
		t.Fatal("arbitrary Character sequence did not satisfy String membership")
	}
}

func TestRapideStringEquivalentFormsShareEventIdentity(t *testing.T) {
	provenance := EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "string-sequence", Instance: "source",
		Action: "Value", Occurrence: "one",
	}
	direct, err := NewDeterministicEvent(provenance, map[string]any{"value": "Alpha"})
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := NewDeterministicEvent(provenance, map[string]any{
		"value": RapideStringFromCodes(65, 108, 112, 104, 97),
	})
	if err != nil {
		t.Fatal(err)
	}
	if direct.ID != sequence.ID {
		t.Fatalf("equivalent direct/code String values changed event identity: %s != %s", direct.ID, sequence.ID)
	}
}

func TestCanonicalStringCodesRejectNoncanonicalRepresentableForm(t *testing.T) {
	for _, value := range []CanonicalValue{
		{Kind: "string-codes"},
		{Kind: "string-codes", Items: []CanonicalValue{{Kind: "character", Text: "65"}}},
		{Kind: "string-codes", Items: []CanonicalValue{{Kind: "integer", Text: "-1"}}},
	} {
		if _, err := DecodeCanonicalValue(value); err == nil {
			t.Fatalf("accepted noncanonical String-code encoding %#v", value)
		}
	}
}
