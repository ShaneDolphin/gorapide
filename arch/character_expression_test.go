package arch

import (
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestCharacterCodeExpressionsAreExactClosedOperations(t *testing.T) {
	for _, code := range []int64{-1 << 63, -1, 0, 65, 1<<63 - 1} {
		character := gorapide.RapideCharacterFromCode(code)
		decoded, decodedType, err := EvaluateConstant(CharacterCodeValue(LiteralValue(character)))
		if err != nil {
			t.Fatal(err)
		}
		if decodedType != "Integer" || decoded != code {
			t.Fatalf("Character(%d).Code()=%#v type %s", code, decoded, decodedType)
		}
		roundTrip, roundTripType, err := EvaluateConstant(CodeToCharacterValue(LiteralValue(code)))
		if err != nil {
			t.Fatal(err)
		}
		if roundTripType != "Character" || roundTrip != character {
			t.Fatalf("Code_To_Char(%d)=%#v type %s", code, roundTrip, roundTripType)
		}
	}
}

func TestCharacterCodeExpressionsRejectWrongOperandTypes(t *testing.T) {
	for _, expression := range []RuleValue{
		CharacterCodeValue(LiteralValue(int64(65))),
		CodeToCharacterValue(LiteralValue(gorapide.RapideCharacterFromCode(65))),
		CodeToCharacterValue(LiteralValue(65.0)),
	} {
		if _, _, err := EvaluateConstant(expression); !errors.Is(err, ErrInvalidStateReference) {
			t.Fatalf("invalid Character conversion error=%v, want ErrInvalidStateReference", err)
		}
	}
}
