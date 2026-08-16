package gorapide

import (
	"math"
	"testing"
)

func TestRapideIntegerToFloatRoundsExactIntegersToNearestEven(t *testing.T) {
	for _, test := range []struct {
		name  string
		value int64
		want  float64
	}{
		{name: "zero", value: 0, want: 0},
		{name: "positive exact", value: 1 << 53, want: math.Ldexp(1, 53)},
		{name: "positive tie down to even", value: 1<<53 + 1, want: math.Ldexp(1, 53)},
		{name: "positive tie up to even", value: 1<<53 + 3, want: math.Ldexp(1, 53) + 4},
		{name: "negative tie toward even", value: -(1<<53 + 1), want: -math.Ldexp(1, 53)},
		{name: "negative tie away to even", value: -(1<<53 + 3), want: -(math.Ldexp(1, 53) + 4)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := RapideIntegerToFloat(test.value)
			if err != nil {
				t.Fatal(err)
			}
			if math.Float64bits(got) != math.Float64bits(test.want) {
				t.Fatalf("Float(%d)=%g bits=%016x, want %g bits=%016x", test.value, got, math.Float64bits(got), test.want, math.Float64bits(test.want))
			}
		})
	}
}

func TestRapideIntegerToFloatHandlesBothSignedIntegerBoundaries(t *testing.T) {
	for _, test := range []struct {
		value int64
		want  float64
	}{
		{value: math.MinInt64, want: -math.Ldexp(1, 63)},
		{value: math.MaxInt64, want: math.Ldexp(1, 63)},
	} {
		got, err := RapideIntegerToFloat(test.value)
		if err != nil {
			t.Fatal(err)
		}
		if math.Float64bits(got) != math.Float64bits(test.want) {
			t.Fatalf("Float(%d)=%g bits=%016x, want %g bits=%016x", test.value, got, math.Float64bits(got), test.want, math.Float64bits(test.want))
		}
	}
}
