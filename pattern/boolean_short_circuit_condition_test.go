package pattern

import (
	"strings"
	"testing"
)

func conditionFailureExpression() Condition {
	return BinaryCondition(
		ConditionEqual,
		BinaryCondition(ConditionDivide, LiteralCondition(int64(1)), LiteralCondition(int64(0))),
		LiteralCondition(int64(0)),
	)
}

func TestBooleanShortCircuitConditionsSkipFailuresAndMissingState(t *testing.T) {
	for _, test := range []struct {
		condition Condition
		want      bool
	}{
		{condition: BinaryCondition(ConditionAndThen, LiteralCondition(false), conditionFailureExpression()), want: false},
		{condition: BinaryCondition(ConditionOrElse, LiteralCondition(true), conditionFailureExpression()), want: true},
		{condition: BinaryCondition(ConditionAndThen, LiteralCondition(false), StateCondition("worker\x00missing", "Boolean")), want: false},
		{condition: BinaryCondition(ConditionOrElse, LiteralCondition(true), StateCondition("worker\x00missing", "Boolean")), want: true},
	} {
		evaluated, err := evaluateConditionWithState(test.condition, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if evaluated.value != test.want || evaluated.typeName != "Boolean" {
			t.Fatalf("lazy condition=%#v, want %t Boolean", evaluated, test.want)
		}
	}
}

func TestBooleanShortCircuitConditionsEvaluateRequiredRightOperand(t *testing.T) {
	for _, condition := range []Condition{
		BinaryCondition(ConditionAndThen, LiteralCondition(true), conditionFailureExpression()),
		BinaryCondition(ConditionOrElse, LiteralCondition(false), conditionFailureExpression()),
	} {
		_, err := evaluateConditionWithState(condition, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "division by zero") {
			t.Fatalf("required RHS error=%v, want division by zero", err)
		}
	}
	for _, condition := range []Condition{
		BinaryCondition(ConditionAndThen, LiteralCondition(true), StateCondition("worker\x00missing", "Boolean")),
		BinaryCondition(ConditionOrElse, LiteralCondition(false), StateCondition("worker\x00missing", "Boolean")),
	} {
		_, err := evaluateConditionWithState(condition, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "requires a consistent-cut witness") {
			t.Fatalf("required RHS witness error=%v", err)
		}
	}
}

func TestBooleanShortCircuitConditionsRemainStaticallyTyped(t *testing.T) {
	for _, condition := range []Condition{
		BinaryCondition(ConditionAndThen, LiteralCondition(false), LiteralCondition(int64(1))),
		BinaryCondition(ConditionOrElse, LiteralCondition(int64(1)), LiteralCondition(true)),
	} {
		_, err := validateConditionType(condition, nil)
		if err == nil || !strings.Contains(err.Error(), "not defined") {
			t.Fatalf("short-circuit condition type error=%v", err)
		}
	}
}
