package arch

import (
	"errors"
	"strings"
	"testing"
)

func TestBooleanXorExpressionImplementsPublishedTruthTable(t *testing.T) {
	for _, test := range []struct {
		left, right bool
		want        bool
	}{
		{left: true, right: true, want: false},
		{left: true, right: false, want: true},
		{left: false, right: true, want: true},
		{left: false, right: false, want: false},
	} {
		value, typeName, err := EvaluateConstant(XorValues(LiteralValue(test.left), LiteralValue(test.right)))
		if err != nil {
			t.Fatal(err)
		}
		if value != test.want || typeName != "Boolean" {
			t.Fatalf("%t xor %t=%#v type=%s, want %t Boolean", test.left, test.right, value, typeName, test.want)
		}
	}
}

func TestBooleanXorExpressionRejectsNonbooleanOperands(t *testing.T) {
	for _, expression := range []RuleValue{
		XorValues(LiteralValue(true), LiteralValue(int64(1))),
		XorValues(LiteralValue("true"), LiteralValue(false)),
	} {
		_, _, err := EvaluateConstant(expression)
		if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), "not defined") {
			t.Fatalf("Boolean.Xor error=%v, want typed ErrInvalidStateReference", err)
		}
	}
}
