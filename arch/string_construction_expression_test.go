package arch

import (
	"errors"
	"reflect"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestStringConstructionExpressionsPreserveExactCharacterSequences(t *testing.T) {
	characterA := gorapide.RapideCharacterFromCode(65)
	appended, appendedType, err := EvaluateConstant(StringAppendValue(LiteralValue("\u03bb"), LiteralValue(characterA)))
	if err != nil {
		t.Fatal(err)
	}
	if appendedType != "String" || appended != "\u03bbA" {
		t.Fatalf("String.Append result=%#v type %s, want \\u03bbA String", appended, appendedType)
	}

	original := gorapide.RapideStringFromCodes(65, 1<<63-1)
	prepended, prependedType, err := EvaluateConstant(StringPrependValue(
		LiteralValue(original), LiteralValue(gorapide.RapideCharacterFromCode(-1<<63)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if prependedType != "String" {
		t.Fatalf("String.Prepend type=%s, want String", prependedType)
	}
	codes, err := gorapide.CanonicalRapideStringCodes(prepended)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{-1 << 63, 65, 1<<63 - 1}; !reflect.DeepEqual(codes, want) {
		t.Fatalf("String.Prepend codes=%v, want %v", codes, want)
	}
	if want := []int64{65, 1<<63 - 1}; !reflect.DeepEqual(original.Codes(), want) {
		t.Fatalf("String construction mutated its input: %v, want %v", original.Codes(), want)
	}
}

func TestStringConstructionExpressionsRejectWrongOperandTypes(t *testing.T) {
	character := gorapide.RapideCharacterFromCode(65)
	for _, expression := range []RuleValue{
		StringAppendValue(LiteralValue(int64(1)), LiteralValue(character)),
		StringAppendValue(LiteralValue("A"), LiteralValue(int64(66))),
		StringPrependValue(LiteralValue(character), LiteralValue(character)),
		StringPrependValue(LiteralValue("A"), LiteralValue("B")),
	} {
		if _, _, err := EvaluateConstant(expression); !errors.Is(err, ErrInvalidStateReference) {
			t.Fatalf("invalid String construction error=%v, want ErrInvalidStateReference", err)
		}
	}
}
