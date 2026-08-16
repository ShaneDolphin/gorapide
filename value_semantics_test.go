package gorapide

import (
	"errors"
	"testing"
)

func TestCanonicalValuesEqualNormalizesIntegerWidths(t *testing.T) {
	equal, err := CanonicalValuesEqual(int32(7), int64(7))
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatal("expected signed integer widths to normalize")
	}
	equal, err = CanonicalValuesEqual(int64(7), float64(7))
	if err != nil {
		t.Fatal(err)
	}
	if equal {
		t.Fatal("Integer and Float values must remain type-distinct")
	}
}

func TestCanonicalValueMatchesPredefinedType(t *testing.T) {
	tests := []struct {
		value any
		typ   string
		want  bool
	}{
		{true, "Boolean", true},
		{int32(-1), "Integer", true},
		{int32(-1), "Natural", false},
		{int32(0), "Natural", true},
		{int32(0), "Positive", false},
		{int32(1), "Positive", true},
		{float32(1.5), "Float", true},
		{"subject", "String", true},
		{"subject", "Unknown", false},
	}
	for _, test := range tests {
		if got := CanonicalValueMatchesPredefinedType(test.value, test.typ); got != test.want {
			t.Errorf("CanonicalValueMatchesPredefinedType(%v, %q) = %t, want %t", test.value, test.typ, got, test.want)
		}
	}
}

func TestCanonicalStringsRejectInvalidUTF8WithoutJSONReplacement(t *testing.T) {
	invalid := string([]byte{0xff})
	if _, err := CanonicalizeParams(map[string]any{"value": invalid}); !errors.Is(err, ErrNonCanonicalValue) {
		t.Fatalf("invalid UTF-8 value error=%v, want ErrNonCanonicalValue", err)
	}
	if _, err := CanonicalizeParams(map[string]any{invalid: "value"}); !errors.Is(err, ErrNonCanonicalValue) {
		t.Fatalf("invalid UTF-8 key error=%v, want ErrNonCanonicalValue", err)
	}
	if CanonicalValueMatchesPredefinedType(invalid, "String") {
		t.Fatal("invalid UTF-8 host string matched Rapide String")
	}
}
