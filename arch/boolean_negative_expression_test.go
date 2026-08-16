package arch

import (
	"errors"
	"strings"
	"testing"
)

func TestBooleanNandNorExpressionsImplementTruthFunctions(t *testing.T) {
	for _, test := range []struct {
		left, right bool
		wantNand    bool
		wantNor     bool
	}{
		{left: true, right: true, wantNand: false, wantNor: false},
		{left: true, right: false, wantNand: true, wantNor: false},
		{left: false, right: true, wantNand: true, wantNor: false},
		{left: false, right: false, wantNand: true, wantNor: true},
	} {
		for _, operation := range []struct {
			name       string
			expression RuleValue
			want       bool
		}{
			{name: "nand", expression: NandValues(LiteralValue(test.left), LiteralValue(test.right)), want: test.wantNand},
			{name: "nor", expression: NorValues(LiteralValue(test.left), LiteralValue(test.right)), want: test.wantNor},
		} {
			value, typeName, err := EvaluateConstant(operation.expression)
			if err != nil {
				t.Fatal(err)
			}
			if value != operation.want || typeName != "Boolean" {
				t.Fatalf("%t %s %t=%#v type=%s, want %t Boolean", test.left, operation.name, test.right, value, typeName, operation.want)
			}
		}
	}
}

func TestBooleanNandNorExpressionsRejectNonbooleanOperands(t *testing.T) {
	for _, expression := range []RuleValue{
		NandValues(LiteralValue(true), LiteralValue(int64(1))),
		NorValues(LiteralValue("false"), LiteralValue(false)),
	} {
		_, _, err := EvaluateConstant(expression)
		if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), "not defined") {
			t.Fatalf("negative logical error=%v, want typed ErrInvalidStateReference", err)
		}
	}
}
