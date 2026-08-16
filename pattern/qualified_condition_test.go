package pattern

import (
	"strings"
	"testing"
)

func TestQualifiedConditionPreservesValueTypeAndCanonicalSpelling(t *testing.T) {
	lower := QualifiedCondition("boolean", LiteralCondition(true))
	upper := QualifiedCondition("BOOLEAN", LiteralCondition(true))
	for _, condition := range []Condition{lower, upper} {
		typeName, err := validateConditionType(condition, nil)
		if err != nil {
			t.Fatal(err)
		}
		evaluated, err := evaluateCondition(condition, nil)
		if err != nil {
			t.Fatal(err)
		}
		if typeName != "Boolean" || evaluated.typeName != "Boolean" || evaluated.value != true {
			t.Fatalf("qualified condition type=%s evaluated=%#v", typeName, evaluated)
		}
	}
	lowerKey, err := deterministicConditionKey(lower)
	if err != nil {
		t.Fatal(err)
	}
	upperKey, err := deterministicConditionKey(upper)
	if err != nil {
		t.Fatal(err)
	}
	if lowerKey != upperKey || !strings.Contains(lowerKey, `"operator":"qualify:Boolean"`) {
		t.Fatalf("qualification keys differ or omit canonical target:\n%s\n%s", lowerKey, upperKey)
	}
}

func TestQualifiedConditionRejectsNarrowingAndUnknownTypes(t *testing.T) {
	for _, condition := range []Condition{
		QualifiedCondition("Natural", LiteralCondition(int64(1))),
		QualifiedCondition("Float", LiteralCondition(int64(1))),
		QualifiedCondition("Clock", LiteralCondition(true)),
	} {
		if _, err := validateConditionType(condition, nil); err == nil {
			t.Fatalf("qualification %#v unexpectedly validated", condition)
		}
	}
}
