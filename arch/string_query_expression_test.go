package arch

import (
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestStringQueryExpressionsCountCharactersNotUTF8Bytes(t *testing.T) {
	for _, test := range []struct {
		value  any
		length int64
		null   bool
	}{
		{value: "", length: 0, null: true},
		{value: "λA", length: 2, null: false},
		{value: gorapide.RapideStringFromCodes(-1, 65, 1<<63-1), length: 3, null: false},
	} {
		length, lengthType, err := EvaluateConstant(StringLengthValue(LiteralValue(test.value)))
		if err != nil {
			t.Fatal(err)
		}
		if lengthType != "Integer" || length != test.length {
			t.Fatalf("Length(%#v)=%#v type %s, want %d", test.value, length, lengthType, test.length)
		}
		null, nullType, err := EvaluateConstant(StringIsNullValue(LiteralValue(test.value)))
		if err != nil {
			t.Fatal(err)
		}
		if nullType != "Boolean" || null != test.null {
			t.Fatalf("Is_Null(%#v)=%#v type %s, want %t", test.value, null, nullType, test.null)
		}
	}
}

func TestStringQueryExpressionsRejectNonStringOperands(t *testing.T) {
	for _, expression := range []RuleValue{
		StringLengthValue(LiteralValue(int64(1))),
		StringIsNullValue(LiteralValue(gorapide.RapideCharacterFromCode(65))),
	} {
		if _, _, err := EvaluateConstant(expression); !errors.Is(err, ErrInvalidStateReference) {
			t.Fatalf("invalid String query error=%v, want ErrInvalidStateReference", err)
		}
	}
}
