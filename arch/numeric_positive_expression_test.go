package arch

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestNumericPositiveExpressionIsExactAndTyped(t *testing.T) {
	for _, test := range []struct {
		value    any
		want     any
		wantType string
	}{
		{value: int64(math.MinInt64), want: int64(math.MinInt64), wantType: "Integer"},
		{value: int64(math.MaxInt64), want: int64(math.MaxInt64), wantType: "Integer"},
		{value: -1.25, want: -1.25, wantType: "Float"},
		{value: -math.SmallestNonzeroFloat64, want: -math.SmallestNonzeroFloat64, wantType: "Float"},
	} {
		value, typeName, err := EvaluateConstant(PositiveValue(LiteralValue(test.value)))
		if err != nil {
			t.Fatal(err)
		}
		if typeName != test.wantType || value != test.want {
			t.Fatalf("+(%#v)=%#v type %s, want %#v %s", test.value, value, typeName, test.want, test.wantType)
		}
	}
	zero, _, err := EvaluateConstant(PositiveValue(LiteralValue(math.Copysign(0, -1))))
	if err != nil {
		t.Fatal(err)
	}
	if zero != float64(0) || math.Signbit(zero.(float64)) {
		t.Fatalf("unary plus zero=%v bits=%016x, want canonical +0", zero, math.Float64bits(zero.(float64)))
	}
}

func TestNumericPositiveExpressionRejectsWrongType(t *testing.T) {
	_, _, err := EvaluateConstant(PositiveValue(LiteralValue(true)))
	if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("unary plus error=%v, want typed ErrInvalidStateReference", err)
	}
}
