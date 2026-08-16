package arch

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestIntegerFloatExpressionUsesExactSoftwareRounding(t *testing.T) {
	for _, test := range []struct {
		value int64
		want  float64
	}{
		{value: 0, want: 0},
		{value: 1<<53 + 1, want: math.Ldexp(1, 53)},
		{value: math.MaxInt64, want: math.Ldexp(1, 63)},
		{value: math.MinInt64, want: -math.Ldexp(1, 63)},
	} {
		value, typeName, err := EvaluateConstant(IntegerFloatValue(LiteralValue(test.value)))
		if err != nil {
			t.Fatal(err)
		}
		if typeName != "Float" || math.Float64bits(value.(float64)) != math.Float64bits(test.want) {
			t.Fatalf("Integer.Float(%d)=%#v type=%s, want %g Float", test.value, value, typeName, test.want)
		}
	}
}

func TestIntegerFloatExpressionRejectsNonintegerOperand(t *testing.T) {
	for _, operand := range []any{1.0, true, "1"} {
		_, _, err := EvaluateConstant(IntegerFloatValue(LiteralValue(operand)))
		if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), "not defined") {
			t.Fatalf("Integer.Float(%#v) error=%v, want typed ErrInvalidStateReference", operand, err)
		}
	}
}
