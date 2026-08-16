package gorapide

import (
	"errors"
	"math"
	"testing"
)

func TestRapideFloatArithmeticRoundsEveryExpressionNode(t *testing.T) {
	left := 1 + math.Ldexp(1, -27)
	right := 1 - math.Ldexp(1, -27)
	product, err := RapideFloat64Arithmetic("*", left, right)
	if err != nil {
		t.Fatal(err)
	}
	if product != 1 {
		t.Fatalf("rounded product=%g, want 1", product)
	}
	result, err := RapideFloat64Arithmetic("-", product, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result != 0 || math.Signbit(result) {
		t.Fatalf("node-rounded result=%g (bits %016x), want canonical +0", result, math.Float64bits(result))
	}
	if fused := math.FMA(left, right, -1); fused == result {
		t.Fatalf("test operands do not distinguish fused and per-node evaluation: fused=%g", fused)
	}
}

func TestRapideFloatArithmeticRoundsSubnormalResultsDeterministically(t *testing.T) {
	smallest := math.SmallestNonzeroFloat64
	unchanged, err := RapideFloat64Arithmetic("*", smallest, 1)
	if err != nil {
		t.Fatal(err)
	}
	if math.Float64bits(unchanged) != math.Float64bits(smallest) {
		t.Fatalf("subnormal product bits=%016x, want %016x", math.Float64bits(unchanged), math.Float64bits(smallest))
	}
	underflow, err := RapideFloat64Arithmetic("/", smallest, 2)
	if err != nil {
		t.Fatal(err)
	}
	if underflow != 0 || math.Signbit(underflow) {
		t.Fatalf("underflow=%g (bits %016x), want canonical +0", underflow, math.Float64bits(underflow))
	}
}

func TestRapideFloatArithmeticRejectsExceptionalResults(t *testing.T) {
	if _, err := RapideFloat64Arithmetic("/", 1, 0); err == nil || err.Error() != "division by zero" {
		t.Fatalf("division diagnostic=%v", err)
	}
	if _, err := RapideFloat64Arithmetic("*", math.MaxFloat64, 2); !errors.Is(err, ErrNonCanonicalValue) {
		t.Fatalf("overflow error=%v, want ErrNonCanonicalValue", err)
	}
	if _, err := RapideFloat64Arithmetic("+", math.Inf(1), 1); !errors.Is(err, ErrNonCanonicalValue) {
		t.Fatalf("nonfinite operand error=%v, want ErrNonCanonicalValue", err)
	}
}

func TestRapideFloatComparisonUsesExactFiniteValues(t *testing.T) {
	for _, test := range []struct {
		operator string
		left     float64
		right    float64
		want     bool
	}{
		{operator: "=", left: math.Copysign(0, -1), right: 0, want: true},
		{operator: "/=", left: 1, right: 2, want: true},
		{operator: "<", left: math.SmallestNonzeroFloat64, right: 1, want: true},
		{operator: "<=", left: 1, right: 1, want: true},
		{operator: ">", left: 2, right: 1, want: true},
		{operator: ">=", left: 1, right: 1, want: true},
	} {
		got, err := RapideFloat64Compare(test.operator, test.left, test.right)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Errorf("%g %s %g=%t, want %t", test.left, test.operator, test.right, got, test.want)
		}
	}
}
