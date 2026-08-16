package arch

import (
	"errors"
	"reflect"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestStringConcatenationExpressionPreservesExactSequences(t *testing.T) {
	representable, typeName, err := EvaluateConstant(StringConcatenateValues(
		LiteralValue("\u03bb"), LiteralValue("A"),
	))
	if err != nil {
		t.Fatal(err)
	}
	if typeName != "String" || representable != "\u03bbA" {
		t.Fatalf("String concatenation result=%#v type %s, want \\u03bbA String", representable, typeName)
	}

	left := gorapide.RapideStringFromCodes(-1<<63, 65)
	right := gorapide.RapideStringFromCodes(66, 1<<63-1)
	combined, typeName, err := EvaluateConstant(StringConcatenateValues(LiteralValue(left), LiteralValue(right)))
	if err != nil {
		t.Fatal(err)
	}
	if typeName != "String" {
		t.Fatalf("String concatenation type=%s, want String", typeName)
	}
	codes, err := gorapide.CanonicalRapideStringCodes(combined)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{-1 << 63, 65, 66, 1<<63 - 1}; !reflect.DeepEqual(codes, want) {
		t.Fatalf("String concatenation codes=%v, want %v", codes, want)
	}
	if want := []int64{-1 << 63, 65}; !reflect.DeepEqual(left.Codes(), want) {
		t.Fatalf("String concatenation mutated left input: %v, want %v", left.Codes(), want)
	}
	if want := []int64{66, 1<<63 - 1}; !reflect.DeepEqual(right.Codes(), want) {
		t.Fatalf("String concatenation mutated right input: %v, want %v", right.Codes(), want)
	}
}

func TestStringConcatenationExpressionRejectsWrongOperandTypes(t *testing.T) {
	character := gorapide.RapideCharacterFromCode(65)
	for _, expression := range []RuleValue{
		StringConcatenateValues(LiteralValue(int64(1)), LiteralValue("A")),
		StringConcatenateValues(LiteralValue("A"), LiteralValue(character)),
		StringConcatenateValues(LiteralValue(character), LiteralValue("A")),
	} {
		if _, _, err := EvaluateConstant(expression); !errors.Is(err, ErrInvalidStateReference) {
			t.Fatalf("invalid String concatenation error=%v, want ErrInvalidStateReference", err)
		}
	}
}
