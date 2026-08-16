package arch

import (
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestStringIndexExpressionIsOneBasedAndExact(t *testing.T) {
	for _, test := range []struct {
		value    any
		position int64
		wantCode int64
	}{
		{value: "\u03bbA", position: 1, wantCode: 0x03bb},
		{value: "\u03bbA", position: 2, wantCode: 65},
		{value: gorapide.RapideStringFromCodes(-1<<63, 65, 1<<63-1), position: 1, wantCode: -1 << 63},
		{value: gorapide.RapideStringFromCodes(-1<<63, 65, 1<<63-1), position: 3, wantCode: 1<<63 - 1},
	} {
		value, typeName, err := EvaluateConstant(StringIndexValue(
			LiteralValue(test.value), LiteralValue(test.position),
		))
		if err != nil {
			t.Fatal(err)
		}
		character, ok := value.(gorapide.RapideCharacter)
		if !ok || typeName != "Character" || character.Code() != test.wantCode {
			t.Fatalf("String index %#v[%d]=%#v type %s, want Character(%d)", test.value, test.position, value, typeName, test.wantCode)
		}
	}
}

func TestStringIndexExpressionRejectsInvalidPositionsAndTypes(t *testing.T) {
	for _, test := range []struct {
		expression RuleValue
		want       string
	}{
		{expression: StringIndexValue(LiteralValue("A"), LiteralValue(int64(0))), want: "outside 1..1"},
		{expression: StringIndexValue(LiteralValue("A"), LiteralValue(int64(-1))), want: "outside 1..1"},
		{expression: StringIndexValue(LiteralValue("A"), LiteralValue(int64(2))), want: "outside 1..1"},
		{expression: StringIndexValue(LiteralValue(""), LiteralValue(int64(1))), want: "outside 1..0"},
		{expression: StringIndexValue(LiteralValue(int64(1)), LiteralValue(int64(1))), want: "not defined"},
		{expression: StringIndexValue(LiteralValue("A"), LiteralValue(true)), want: "not defined"},
	} {
		_, _, err := EvaluateConstant(test.expression)
		if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("invalid String index error=%v, want ErrInvalidStateReference containing %q", err, test.want)
		}
	}
}
