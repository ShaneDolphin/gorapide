package arch

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestNumericAbsExpressionSupportsIntegerAndFloat(t *testing.T) {
	for _, test := range []struct {
		value    any
		want     any
		wantType string
	}{
		{value: int64(-7), want: int64(7), wantType: "Integer"},
		{value: int64(0), want: int64(0), wantType: "Integer"},
		{value: -1.25, want: 1.25, wantType: "Float"},
		{value: -math.SmallestNonzeroFloat64, want: math.SmallestNonzeroFloat64, wantType: "Float"},
	} {
		value, typeName, err := EvaluateConstant(AbsValue(LiteralValue(test.value)))
		if err != nil {
			t.Fatal(err)
		}
		if typeName != test.wantType || value != test.want {
			t.Fatalf("Abs(%#v)=%#v type %s, want %#v %s", test.value, value, typeName, test.want, test.wantType)
		}
	}
}

func TestNumericAbsExpressionRejectsWrongTypeAndMinInteger(t *testing.T) {
	for _, test := range []struct {
		expression RuleValue
		want       string
	}{
		{expression: AbsValue(LiteralValue(true)), want: "not defined"},
		{expression: AbsValue(LiteralValue(int64(math.MinInt64))), want: "integer overflow"},
	} {
		_, _, err := EvaluateConstant(test.expression)
		if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("invalid Numeric.Abs error=%v, want ErrInvalidStateReference containing %q", err, test.want)
		}
	}
}
