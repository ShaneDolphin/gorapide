package arch

import (
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestQualifyValuePreservesCanonicalScalarValues(t *testing.T) {
	character := gorapide.RapideCharacterFromCode(65)
	for _, test := range []struct {
		typeName string
		value    any
	}{
		{typeName: "boolean", value: true},
		{typeName: "INTEGER", value: int64(7)},
		{typeName: "Float", value: 1.25},
		{typeName: "Character", value: character},
		{typeName: "String", value: "value"},
	} {
		value, typeName, err := EvaluateConstant(QualifyValue(test.typeName, LiteralValue(test.value)))
		if err != nil {
			t.Fatalf("%s qualification: %v", test.typeName, err)
		}
		equal, err := gorapide.CanonicalValuesEqual(value, test.value)
		if err != nil || !equal {
			t.Fatalf("%s qualification value=%#v equal=%t err=%v", test.typeName, value, equal, err)
		}
		canonical, _ := canonicalQualificationType(test.typeName)
		if typeName != canonical {
			t.Fatalf("%s qualification type=%s, want %s", test.typeName, typeName, canonical)
		}
	}
}

func TestQualifyValueAcceptsOnlyPublishedIntegerWidening(t *testing.T) {
	for _, test := range []struct {
		source string
		target string
	}{
		{source: "Positive", target: "Natural"},
		{source: "Positive", target: "Integer"},
		{source: "Natural", target: "Integer"},
	} {
		_, canonical, typeName, err := canonicalizeClosedRuleValue(
			"qualified expression", QualifyValue(test.target, BoundValue("value")), nil,
			map[string]string{"value": test.source},
		)
		if err != nil {
			t.Fatalf("%s to %s: %v", test.source, test.target, err)
		}
		if typeName != test.target || canonical.Operator != RuleValueOperator("qualify:"+test.target) {
			t.Fatalf("%s to %s canonical=%#v type=%s", test.source, test.target, canonical, typeName)
		}
	}

	for _, expression := range []RuleValue{
		QualifyValue("Float", LiteralValue(int64(1))),
		QualifyValue("Clock", LiteralValue(true)),
	} {
		_, _, err := EvaluateConstant(expression)
		if !errors.Is(err, ErrInvalidStateReference) {
			t.Fatalf("invalid qualification error=%v, want ErrInvalidStateReference", err)
		}
	}
	value, typeName, err := EvaluateConstant(QualifyValue("Positive", LiteralValue(int64(1))))
	if err != nil || value != int64(1) || typeName != "Positive" {
		t.Fatalf("closed constrained membership proof value=%#v type=%s err=%v", value, typeName, err)
	}
}

func TestQualifyValueCanonicalizesTargetSpelling(t *testing.T) {
	_, lower, _, err := canonicalizeClosedRuleValue(
		"qualified expression", QualifyValue("integer", LiteralValue(int64(3))), nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, upper, _, err := canonicalizeClosedRuleValue(
		"qualified expression", QualifyValue("INTEGER", LiteralValue(int64(3))), nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lower.Operator != upper.Operator || !strings.EqualFold(string(lower.Operator), "qualify:Integer") {
		t.Fatalf("qualification operators differ: %q != %q", lower.Operator, upper.Operator)
	}
}
