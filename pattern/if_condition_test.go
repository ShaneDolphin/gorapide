package pattern

import (
	"strings"
	"testing"
)

func TestIfConditionSkipsUnselectedFailureAndMissingState(t *testing.T) {
	failure := BinaryCondition(
		ConditionDivide, LiteralCondition(int64(1)), LiteralCondition(int64(0)),
	)
	for _, test := range []struct {
		condition Condition
		want      any
	}{
		{condition: TernaryCondition(ConditionIf, LiteralCondition(true), LiteralCondition(int64(7)), failure), want: int64(7)},
		{condition: TernaryCondition(ConditionIf, LiteralCondition(false), failure, LiteralCondition(int64(9))), want: int64(9)},
		{condition: TernaryCondition(ConditionIf, LiteralCondition(true), LiteralCondition("selected"), StateCondition("worker\x00missing", "String")), want: "selected"},
	} {
		evaluated, err := evaluateConditionWithState(test.condition, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if evaluated.value != test.want {
			t.Fatalf("if condition=%#v, want %#v", evaluated, test.want)
		}
	}
}

func TestIfConditionEvaluatesSelectedBranchAndChecksBothTypes(t *testing.T) {
	failure := BinaryCondition(
		ConditionDivide, LiteralCondition(int64(1)), LiteralCondition(int64(0)),
	)
	for _, condition := range []Condition{
		TernaryCondition(ConditionIf, LiteralCondition(true), failure, LiteralCondition(int64(9))),
		TernaryCondition(ConditionIf, LiteralCondition(false), LiteralCondition(int64(7)), failure),
	} {
		_, err := evaluateConditionWithState(condition, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "division by zero") {
			t.Fatalf("selected if condition error=%v", err)
		}
	}
	for _, condition := range []Condition{
		TernaryCondition(ConditionIf, LiteralCondition(int64(1)), LiteralCondition(true), LiteralCondition(false)),
		TernaryCondition(ConditionIf, LiteralCondition(true), LiteralCondition(true), LiteralCondition(int64(1))),
	} {
		_, err := validateConditionType(condition, nil)
		if err == nil || !strings.Contains(err.Error(), "not defined") {
			t.Fatalf("if condition type error=%v", err)
		}
	}
}
