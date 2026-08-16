package gorapide

import (
	"math"
	"strings"
	"testing"
)

func TestRapideFloat64AbsIsExactAndNormalizesZero(t *testing.T) {
	for _, test := range []struct {
		value float64
		want  float64
	}{
		{value: 0, want: 0},
		{value: math.Copysign(0, -1), want: 0},
		{value: -math.SmallestNonzeroFloat64, want: math.SmallestNonzeroFloat64},
		{value: -1.25, want: 1.25},
		{value: -math.MaxFloat64, want: math.MaxFloat64},
		{value: math.MaxFloat64, want: math.MaxFloat64},
	} {
		got, err := RapideFloat64Abs(test.value)
		if err != nil {
			t.Fatal(err)
		}
		if math.Float64bits(got) != math.Float64bits(test.want) {
			t.Errorf("Abs(%g) bits=%016x, want %016x", test.value, math.Float64bits(got), math.Float64bits(test.want))
		}
	}
}

func TestRapideFloat64AbsRejectsNonfiniteOperands(t *testing.T) {
	for _, value := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
		_, err := RapideFloat64Abs(value)
		if err == nil || !strings.Contains(err.Error(), "non-finite") {
			t.Fatalf("Abs(%g) error=%v, want non-finite failure", value, err)
		}
	}
}
