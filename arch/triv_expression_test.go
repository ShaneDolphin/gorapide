package arch

import (
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestTrivUnitExpressionsRemainTypedAndUnique(t *testing.T) {
	unit := gorapide.RapideUnit()
	for _, expression := range []RuleValue{
		LiteralValue(unit),
		QualifyValue("triv", LiteralValue(unit)),
		IfValue(LiteralValue(true), LiteralValue(unit), QualifyValue("TRIV", LiteralValue(unit))),
	} {
		value, typeName, err := EvaluateConstant(expression)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := value.(gorapide.RapideTriv); !ok || typeName != "Triv" {
			t.Fatalf("Triv expression value=%#v type=%s", value, typeName)
		}
	}

	equal, typeName, err := EvaluateConstant(EqualValues(LiteralValue(unit), LiteralValue(gorapide.RapideUnit())))
	if err != nil || equal != true || typeName != "Boolean" {
		t.Fatalf("Unit equality value=%#v type=%s err=%v", equal, typeName, err)
	}
	if _, _, err := EvaluateConstant(QualifyValue("Triv", LiteralValue(nil))); err == nil {
		t.Fatal("null was accepted as Triv")
	}
}
