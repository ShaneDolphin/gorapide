package arch

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestStringSliceExpressionIsInclusiveAndExact(t *testing.T) {
	representable, typeName, err := EvaluateConstant(StringSliceValue(
		LiteralValue("\u03bbABC"), LiteralValue(int64(2)), LiteralValue(int64(3)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if typeName != "String" || representable != "AB" {
		t.Fatalf("String Slice result=%#v type %s, want AB String", representable, typeName)
	}

	original := gorapide.RapideStringFromCodes(-1<<63, 65, 66, 1<<63-1)
	sliced, typeName, err := EvaluateConstant(StringSliceValue(
		LiteralValue(original), LiteralValue(int64(1)), LiteralValue(int64(4)),
	))
	if err != nil {
		t.Fatal(err)
	}
	codes, err := gorapide.CanonicalRapideStringCodes(sliced)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{-1 << 63, 65, 66, 1<<63 - 1}; typeName != "String" || !reflect.DeepEqual(codes, want) {
		t.Fatalf("String Slice codes=%v type %s, want %v String", codes, typeName, want)
	}
	if want := []int64{-1 << 63, 65, 66, 1<<63 - 1}; !reflect.DeepEqual(original.Codes(), want) {
		t.Fatalf("String Slice mutated its input: %v, want %v", original.Codes(), want)
	}
}

func TestStringSliceExpressionHasCanonicalEmptyRanges(t *testing.T) {
	for _, test := range []struct {
		value        string
		lower, upper int64
	}{
		{value: "", lower: 1, upper: 0},
		{value: "ABC", lower: 2, upper: 1},
		{value: "ABC", lower: 3, upper: 1},
		{value: "ABC", lower: 4, upper: 3},
	} {
		value, typeName, err := EvaluateConstant(StringSliceValue(
			LiteralValue(test.value), LiteralValue(test.lower), LiteralValue(test.upper),
		))
		if err != nil {
			t.Fatal(err)
		}
		if typeName != "String" || value != "" {
			t.Fatalf("%q[%d..%d]=%#v type %s, want empty String", test.value, test.lower, test.upper, value, typeName)
		}
	}
}

func TestStringSliceExpressionRejectsInvalidBoundsAndTypes(t *testing.T) {
	for _, test := range []struct {
		expression RuleValue
		want       string
	}{
		{expression: StringSliceValue(LiteralValue("ABC"), LiteralValue(int64(0)), LiteralValue(int64(1))), want: "outside length 3"},
		{expression: StringSliceValue(LiteralValue("ABC"), LiteralValue(int64(1)), LiteralValue(int64(4))), want: "outside length 3"},
		{expression: StringSliceValue(LiteralValue("ABC"), LiteralValue(int64(5)), LiteralValue(int64(3))), want: "outside length 3"},
		{expression: StringSliceValue(LiteralValue("ABC"), LiteralValue(int64(1)), LiteralValue(int64(-1))), want: "outside length 3"},
		{expression: StringSliceValue(LiteralValue(int64(1)), LiteralValue(int64(1)), LiteralValue(int64(1))), want: "not defined"},
		{expression: StringSliceValue(LiteralValue("ABC"), LiteralValue(true), LiteralValue(int64(1))), want: "not defined"},
		{expression: StringSliceValue(LiteralValue("ABC"), LiteralValue(int64(1)), LiteralValue(1.0)), want: "not defined"},
	} {
		_, _, err := EvaluateConstant(test.expression)
		if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("invalid String Slice error=%v, want ErrInvalidStateReference containing %q", err, test.want)
		}
	}
}
