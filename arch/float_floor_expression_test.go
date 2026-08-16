package arch

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestFloatFloorExpressionIsExactAndTyped(t *testing.T) {
	for _, test := range []struct {
		value float64
		want  int64
	}{
		{value: 1.75, want: 1},
		{value: -1.25, want: -2},
		{value: -math.SmallestNonzeroFloat64, want: -1},
	} {
		value, typeName, err := EvaluateConstant(FloatFloorValue(LiteralValue(test.value)))
		if err != nil {
			t.Fatal(err)
		}
		if typeName != "Integer" || value != test.want {
			t.Fatalf("Float.Floor(%g)=%#v type %s, want %d Integer", test.value, value, typeName, test.want)
		}
	}
}

func TestFloatFloorExpressionRejectsWrongTypeAndOverflow(t *testing.T) {
	for _, test := range []struct {
		expression RuleValue
		want       string
	}{
		{expression: FloatFloorValue(LiteralValue(int64(1))), want: "not defined"},
		{expression: FloatFloorValue(LiteralValue(math.Ldexp(1, 63))), want: "outside the signed 64-bit Integer range"},
	} {
		_, _, err := EvaluateConstant(test.expression)
		if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("invalid Float.Floor error=%v, want ErrInvalidStateReference containing %q", err, test.want)
		}
	}
}
