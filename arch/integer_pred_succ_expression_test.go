package arch

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestIntegerPredSuccExpressionsAreExactAndTyped(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression RuleValue
		want       int64
	}{
		{name: "predecessor of zero", expression: IntegerPredValue(LiteralValue(int64(0))), want: -1},
		{name: "successor of negative one", expression: IntegerSuccValue(LiteralValue(int64(-1))), want: 0},
		{name: "minimum in range", expression: IntegerPredValue(LiteralValue(int64(math.MinInt64 + 1))), want: math.MinInt64},
		{name: "maximum in range", expression: IntegerSuccValue(LiteralValue(int64(math.MaxInt64 - 1))), want: math.MaxInt64},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, typeName, err := EvaluateConstant(test.expression)
			if err != nil {
				t.Fatal(err)
			}
			if value != test.want || typeName != "Integer" {
				t.Fatalf("value=%#v type=%s, want %d Integer", value, typeName, test.want)
			}
		})
	}
}

func TestIntegerPredSuccExpressionsRejectWrongTypesAndOverflow(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression RuleValue
		want       string
	}{
		{name: "predecessor Float", expression: IntegerPredValue(LiteralValue(1.0)), want: "not defined"},
		{name: "successor Boolean", expression: IntegerSuccValue(LiteralValue(true)), want: "not defined"},
		{name: "predecessor overflow", expression: IntegerPredValue(LiteralValue(int64(math.MinInt64))), want: "integer overflow"},
		{name: "successor overflow", expression: IntegerSuccValue(LiteralValue(int64(math.MaxInt64))), want: "integer overflow"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := EvaluateConstant(test.expression)
			if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want ErrInvalidStateReference containing %q", err, test.want)
			}
		})
	}
}
