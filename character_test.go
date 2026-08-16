package gorapide

import (
	"testing"
)

func TestRapideCharacterIsAFirstClassCanonicalValue(t *testing.T) {
	for _, code := range []int64{-1, 0, 10, 39, 65, 92, 1<<63 - 1, -1 << 63} {
		character := RapideCharacterFromCode(code)
		if character.Code() != code {
			t.Fatalf("Character code=%d, want %d", character.Code(), code)
		}
		encoded, err := EncodeCanonicalValue(character)
		if err != nil {
			t.Fatal(err)
		}
		if encoded.Kind != "character" {
			t.Fatalf("code %d encoded kind=%q, want character", code, encoded.Kind)
		}
		decoded, err := DecodeCanonicalValue(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if decoded != character {
			t.Fatalf("code %d round trip=%#v", code, decoded)
		}
	}

	character := RapideCharacterFromCode(65)
	if !CanonicalValueMatchesPredefinedType(character, "Character") {
		t.Fatal("first-class Character did not satisfy Character membership")
	}
	if CanonicalValueMatchesPredefinedType(int64(65), "Character") ||
		CanonicalValueMatchesPredefinedType("A", "Character") ||
		CanonicalValueMatchesPredefinedType(character, "Integer") ||
		CanonicalValueMatchesPredefinedType(character, "String") {
		t.Fatal("Character collapsed into Integer or String membership")
	}
	for _, other := range []any{int64(65), "A"} {
		equal, err := CanonicalValuesEqual(character, other)
		if err != nil {
			t.Fatal(err)
		}
		if equal {
			t.Fatalf("Character unexpectedly equals %T", other)
		}
	}
}

func TestRapideCharacterHasDistinctEventIdentity(t *testing.T) {
	provenance := EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "character", Instance: "source",
		Action: "Value", Occurrence: "one",
	}
	ids := map[EventID]bool{}
	for _, value := range []any{RapideCharacterFromCode(65), int64(65), "A"} {
		event, err := NewDeterministicEvent(provenance, map[string]any{"value": value})
		if err != nil {
			t.Fatal(err)
		}
		ids[event.ID] = true
	}
	if len(ids) != 3 {
		t.Fatalf("Character, Integer, and String produced only %d event identities", len(ids))
	}
}

func TestCanonicalCharacterRejectsNoncanonicalCodes(t *testing.T) {
	for _, value := range []CanonicalValue{
		{Kind: "character", Text: ""},
		{Kind: "character", Text: "+65"},
		{Kind: "character", Text: "065"},
		{Kind: "character", Text: "9223372036854775808"},
	} {
		if _, err := DecodeCanonicalValue(value); err == nil {
			t.Fatalf("accepted noncanonical Character encoding %#v", value)
		}
	}
}
