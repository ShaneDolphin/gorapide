package arch

import (
	"errors"
	"strings"
	"testing"
)

func TestIfValueEvaluatesOnlySelectedBranch(t *testing.T) {
	failure := DivideValues(LiteralValue(int64(1)), LiteralValue(int64(0)))
	for _, test := range []struct {
		expression RuleValue
		want       int64
	}{
		{expression: IfValue(LiteralValue(true), LiteralValue(int64(7)), failure), want: 7},
		{expression: IfValue(LiteralValue(false), failure, LiteralValue(int64(9))), want: 9},
	} {
		value, typeName, err := EvaluateConstant(test.expression)
		if err != nil {
			t.Fatal(err)
		}
		if value != test.want || typeName != "Integer" {
			t.Fatalf("if-expression=%#v type=%s, want %d Integer", value, typeName, test.want)
		}
	}
}

func TestIfValueSurfacesSelectedFailure(t *testing.T) {
	failure := DivideValues(LiteralValue(int64(1)), LiteralValue(int64(0)))
	for _, expression := range []RuleValue{
		IfValue(LiteralValue(true), failure, LiteralValue(int64(9))),
		IfValue(LiteralValue(false), LiteralValue(int64(7)), failure),
	} {
		_, _, err := EvaluateConstant(expression)
		if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), "division by zero") {
			t.Fatalf("selected if branch error=%v, want deterministic division failure", err)
		}
	}
}

func TestIfValueStaticallyChecksConditionAndBothBranches(t *testing.T) {
	for _, expression := range []RuleValue{
		IfValue(LiteralValue(int64(1)), LiteralValue(true), LiteralValue(false)),
		IfValue(LiteralValue(true), LiteralValue(true), LiteralValue(int64(1))),
	} {
		_, _, err := EvaluateConstant(expression)
		if !errors.Is(err, ErrInvalidStateReference) {
			t.Fatalf("if-expression type error=%v, want ErrInvalidStateReference", err)
		}
	}
}
