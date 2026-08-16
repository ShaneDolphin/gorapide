package gorapide

import (
	"math"
	"strings"
	"testing"
)

func TestRapideFloat64FloorUsesExactMathematicalFloor(t *testing.T) {
	for _, test := range []struct {
		value float64
		want  int64
	}{
		{value: 0, want: 0},
		{value: math.Copysign(0, -1), want: 0},
		{value: math.SmallestNonzeroFloat64, want: 0},
		{value: -math.SmallestNonzeroFloat64, want: -1},
		{value: 1.9999999999999998, want: 1},
		{value: -1.0000000000000002, want: -2},
		{value: math.Ldexp(-1, 63), want: math.MinInt64},
		{value: math.Nextafter(math.Ldexp(1, 63), 0), want: 9223372036854774784},
	} {
		got, err := RapideFloat64Floor(test.value)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Errorf("Floor(%g)=%d, want %d", test.value, got, test.want)
		}
	}
}

func TestRapideFloat64FloorRejectsNonfiniteAndOutOfRangeResults(t *testing.T) {
	for _, value := range []float64{
		math.Inf(1), math.Inf(-1), math.NaN(),
		math.Ldexp(1, 63), math.Nextafter(math.Ldexp(-1, 63), math.Inf(-1)),
	} {
		_, err := RapideFloat64Floor(value)
		if err == nil || (!strings.Contains(err.Error(), "outside the signed 64-bit Integer range") &&
			!strings.Contains(err.Error(), "non-finite")) {
			t.Fatalf("Floor(%g) error=%v, want explicit nonfinite/range failure", value, err)
		}
	}
}
