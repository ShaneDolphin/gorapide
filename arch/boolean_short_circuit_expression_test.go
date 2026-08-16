package arch

import (
	"errors"
	"strings"
	"testing"
)

func booleanFailureExpression() RuleValue {
	return EqualValues(
		DivideValues(LiteralValue(int64(1)), LiteralValue(int64(0))),
		LiteralValue(int64(0)),
	)
}

func TestBooleanShortCircuitExpressionsImplementLazyIfEquivalences(t *testing.T) {
	for _, test := range []struct {
		left, right bool
	}{
		{left: true, right: true},
		{left: true, right: false},
		{left: false, right: true},
		{left: false, right: false},
	} {
		andThen, typeName, err := EvaluateConstant(AndThenValues(LiteralValue(test.left), LiteralValue(test.right)))
		if err != nil {
			t.Fatal(err)
		}
		if andThen != (test.left && test.right) || typeName != "Boolean" {
			t.Fatalf("%t andthen %t=%#v type=%s", test.left, test.right, andThen, typeName)
		}
		orElse, typeName, err := EvaluateConstant(OrElseValues(LiteralValue(test.left), LiteralValue(test.right)))
		if err != nil {
			t.Fatal(err)
		}
		if orElse != (test.left || test.right) || typeName != "Boolean" {
			t.Fatalf("%t orelse %t=%#v type=%s", test.left, test.right, orElse, typeName)
		}
	}
}

func TestBooleanShortCircuitExpressionsSkipAndSurfaceRightFailures(t *testing.T) {
	for _, expression := range []RuleValue{
		AndThenValues(LiteralValue(false), booleanFailureExpression()),
		OrElseValues(LiteralValue(true), booleanFailureExpression()),
	} {
		value, typeName, err := EvaluateConstant(expression)
		if err != nil {
			t.Fatal(err)
		}
		if value != (expression.operator == OperatorOrElse) || typeName != "Boolean" {
			t.Fatalf("short-circuit value=%#v type=%s operator=%s", value, typeName, expression.operator)
		}
	}
	for _, expression := range []RuleValue{
		AndThenValues(LiteralValue(true), booleanFailureExpression()),
		OrElseValues(LiteralValue(false), booleanFailureExpression()),
	} {
		_, _, err := EvaluateConstant(expression)
		if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), "division by zero") {
			t.Fatalf("evaluated RHS error=%v, want deterministic division failure", err)
		}
	}
}

func TestBooleanShortCircuitExpressionsRemainStaticallyTyped(t *testing.T) {
	for _, expression := range []RuleValue{
		AndThenValues(LiteralValue(false), LiteralValue(int64(1))),
		OrElseValues(LiteralValue(int64(1)), LiteralValue(true)),
	} {
		_, _, err := EvaluateConstant(expression)
		if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), "not defined") {
			t.Fatalf("short-circuit type error=%v, want typed ErrInvalidStateReference", err)
		}
	}
}
