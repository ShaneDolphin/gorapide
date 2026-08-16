package pattern

import (
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestTrivConditionsBindQualifyAndCompareUnit(t *testing.T) {
	unit := gorapide.RapideUnit()
	condition := BinaryCondition(
		ConditionEqual,
		BindingCondition(Var("Value").WithType("Triv")),
		QualifiedCondition("triv", LiteralCondition(unit)),
	)
	if typeName, err := validateConditionType(condition, map[string]string{"Value": "Triv"}); err != nil || typeName != "Boolean" {
		t.Fatalf("Triv condition type=%s err=%v", typeName, err)
	}
	bindings := Bindings{{Placeholder: "Value", Value: unit}}
	evaluated, err := evaluateCondition(condition, bindings)
	if err != nil || evaluated.value != true || evaluated.typeName != "Boolean" {
		t.Fatalf("Triv condition=%#v err=%v", evaluated, err)
	}
	key, err := deterministicConditionKey(condition)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(key, `"kind":"triv"`) || !strings.Contains(key, `"operator":"qualify:Triv"`) {
		t.Fatalf("Triv condition key omits canonical semantics: %s", key)
	}
}
