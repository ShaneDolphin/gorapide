package gorapide

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestRapideModuleTypeDenotationsFollowTargetSpecifications(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	floatType := mustRapidePredefinedType(t, "Float")
	salary := mustRapideInterfaceType(t, ProvidedRapideMember("Salary", floatType))
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Salary", floatType),
	)
	target := mustRapideInterfaceType(t,
		UnboundedProvidedRapideTypeName("Element"),
		BoundedProvidedRapideTypeName("Worker", salary),
		ExactPrivateRapideTypeName("Label", stringType),
	)
	element, err := NewRapideTypeDenotation("ELEMENT", employee)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewRapideTypeDenotation("worker", employee)
	if err != nil {
		t.Fatal(err)
	}
	label, err := NewRapideTypeDenotation("Label", stringType)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := ValidateRapideInterfaceTypeDenotations(target, worker, label, element)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 3 || normalized[0].Name() != "element" ||
		normalized[1].Name() != "label" || normalized[2].Name() != "worker" {
		t.Fatalf("canonical module type denotations=%#v", normalized)
	}
	if normalized[0].Type().node != employee.node {
		t.Fatal("normalization changed the immutable exact type denotation")
	}
}

func TestRapideModuleTypeDenotationsRejectMissingDuplicateAndIncompatible(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	integerType := mustRapidePredefinedType(t, "Integer")
	target := mustRapideInterfaceType(t,
		ExactProvidedRapideTypeName("Element", stringType),
	)
	stringElement, _ := NewRapideTypeDenotation("Element", stringType)
	integerElement, _ := NewRapideTypeDenotation("Element", integerType)
	tests := []struct {
		name string
		got  []RapideTypeDenotation
		want string
	}{
		{name: "missing", want: `does not supply type-name "element"`},
		{name: "duplicate", got: []RapideTypeDenotation{stringElement, stringElement}, want: `duplicate module type denotation "element"`},
		{name: "incompatible", got: []RapideTypeDenotation{integerElement}, want: `does not satisfy target exact type`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateRapideInterfaceTypeDenotations(target, test.got...)
			if !errors.Is(err, ErrInvalidRapideMembership) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want ErrInvalidRapideMembership containing %q", err, test.want)
			}
		})
	}
	if _, err := NewRapideTypeDenotation("9bad", stringType); !errors.Is(err, ErrInvalidRapideMembership) {
		t.Fatalf("invalid denotation name error=%v", err)
	}
	if _, err := NewRapideTypeDenotation("Element", RapideType{}); !errors.Is(err, ErrInvalidRapideMembership) {
		t.Fatalf("zero denotation type error=%v", err)
	}
}

func TestRapideModuleTypeDenotationsPreserveExtraPrivateTypes(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	target := mustRapideInterfaceType(t, UnboundedProvidedRapideTypeName("Element"))
	element, _ := NewRapideTypeDenotation("Element", stringType)
	helper, _ := NewRapideTypeDenotation("Helper", stringType)
	normalized, err := ValidateRapideInterfaceTypeDenotations(target, helper, element)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 2 || normalized[0].Name() != "element" || normalized[1].Name() != "helper" {
		t.Fatalf("private module type was not retained canonically: %#v", normalized)
	}
}

func TestRapideModuleTypeDenotationSubstitutesDirectLocalBound(t *testing.T) {
	baseReference := mustRapideTypeNameReference(t, "Base")
	target := mustRapideInterfaceType(t,
		UnboundedProvidedRapideTypeName("Base"),
		BoundedProvidedRapideTypeName("Derived", baseReference),
	)
	stringType := mustRapidePredefinedType(t, "String")
	base, _ := NewRapideTypeDenotation("Base", stringType)
	derived, _ := NewRapideTypeDenotation("Derived", stringType)
	if _, err := ValidateRapideInterfaceTypeDenotations(target, derived, base); err != nil {
		t.Fatal(err)
	}
	integerType := mustRapidePredefinedType(t, "Integer")
	incompatible, _ := NewRapideTypeDenotation("Derived", integerType)
	_, err := ValidateRapideInterfaceTypeDenotations(target, incompatible, base)
	if !errors.Is(err, ErrInvalidRapideMembership) || !strings.Contains(err.Error(), "does not satisfy target bound") {
		t.Fatalf("substituted bound incompatibility error=%v", err)
	}
}

func TestRapideModuleTypeDenotationsSupportRecursiveInterfaceTargets(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	target, err := NewSelfRecursiveRapideInterfaceType(func(self RapideType) (RapideType, error) {
		return NewRapideInterfaceType(
			ProvidedRapideMember("Next", self),
			UnboundedProvidedRapideTypeName("Element"),
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	element, _ := NewRapideTypeDenotation("Element", stringType)
	if _, err := ValidateRapideInterfaceTypeDenotations(target, element); err != nil {
		t.Fatal(err)
	}
}

func TestRapideModuleTypeDenotationNormalizationIgnoresGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	stringType := mustRapidePredefinedType(t, "String")
	target := mustRapideInterfaceType(t,
		UnboundedProvidedRapideTypeName("A"),
		UnboundedProvidedRapideTypeName("B"),
	)
	a, _ := NewRapideTypeDenotation("A", stringType)
	b, _ := NewRapideTypeDenotation("B", stringType)
	for iteration := 0; iteration < 20; iteration++ {
		if iteration == 10 {
			runtime.GOMAXPROCS(4)
		}
		input := []RapideTypeDenotation{a, b}
		if iteration%2 != 0 {
			input = []RapideTypeDenotation{b, a}
		}
		normalized, err := ValidateRapideInterfaceTypeDenotations(target, input...)
		if err != nil {
			t.Fatal(err)
		}
		if normalized[0].Name() != "a" || normalized[1].Name() != "b" {
			t.Fatalf("iteration %d normalization=%#v", iteration, normalized)
		}
	}
}

func TestRapideModuleObjectDenotationsSatisfyProvidedAndPrivateObjects(t *testing.T) {
	integerType := mustRapidePredefinedType(t, "Integer")
	booleanType := mustRapidePredefinedType(t, "Boolean")
	target := mustRapideInterfaceType(t,
		ProvidedRapideMember("Limit", integerType),
		PrivateRapideMember("Enabled", booleanType),
		RequiredRapideMember("External", integerType),
	)
	limit, err := NewRapideObjectDenotation("LIMIT", integerType, int32(7))
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := NewRapideObjectDenotation("enabled", booleanType, true)
	if err != nil {
		t.Fatal(err)
	}
	helper, err := NewRapideObjectDenotation("Helper", integerType, int64(9))
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := ValidateRapideInterfaceObjectDenotations(target, nil, helper, enabled, limit)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 3 || normalized[0].Name() != "enabled" ||
		normalized[1].Name() != "helper" || normalized[2].Name() != "limit" {
		t.Fatalf("canonical module object denotations=%#v", normalized)
	}
	value, err := normalized[2].Value()
	if err != nil || value != int64(7) {
		t.Fatalf("normalized object value=%#v, err=%v", value, err)
	}
}

func TestRapideModuleObjectDenotationsSubstituteLocalTypes(t *testing.T) {
	itemReference := mustRapideTypeNameReference(t, "Item")
	target := mustRapideInterfaceType(t,
		UnboundedProvidedRapideTypeName("Item"),
		ProvidedRapideMember("Default", itemReference),
	)
	integerType := mustRapidePredefinedType(t, "Integer")
	item, _ := NewRapideTypeDenotation("Item", integerType)
	value, _ := NewRapideObjectDenotation("Default", integerType, int64(3))
	if _, err := ValidateRapideInterfaceObjectDenotations(target, []RapideTypeDenotation{item}, value); err != nil {
		t.Fatal(err)
	}
}

func TestRapideModuleObjectDenotationAcceptsAllocatedStructuralModule(t *testing.T) {
	integerType := mustRapidePredefinedType(t, "Integer")
	iteratorType, err := RapideIteratorType(integerType)
	if err != nil {
		t.Fatal(err)
	}
	module, err := NewRapideModuleValue(moduleValueTestProvenance())
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := NewRapideObjectDenotation("Cursor", iteratorType, module)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := cursor.Value()
	if err != nil {
		t.Fatal(err)
	}
	decodedModule, ok := decoded.(RapideModuleValue)
	if !ok || !SameRapideModule(module, decodedModule) {
		t.Fatalf("structural module value=%#v, want allocation %s", decoded, module.Identity())
	}
	target := mustRapideInterfaceType(t, ProvidedRapideMember("Cursor", iteratorType))
	validated, err := ValidateRapideInterfaceObjectDenotations(target, nil, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(validated) != 1 || validated[0].EncodedValue().Kind != "module" {
		t.Fatalf("canonical structural module denotation=%#v", validated)
	}
}

func TestRapideModuleObjectDenotationRejectsUntypedStructuralValues(t *testing.T) {
	integerType := mustRapidePredefinedType(t, "Integer")
	iteratorType, err := RapideIteratorType(integerType)
	if err != nil {
		t.Fatal(err)
	}
	module, err := NewRapideModuleValue(moduleValueTestProvenance())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		typ   RapideType
		value any
		want  string
	}{
		{name: "scalar as interface", typ: iteratorType, value: int64(1), want: "not an allocation-identified module"},
		{name: "module as scalar", typ: integerType, value: module, want: "not a member of Integer"},
		{name: "module as function", typ: mustRapideFunctionType(t, nil, integerType), value: module, want: "unsupported structural value-membership kernel"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRapideObjectDenotation("Value", test.typ, test.value)
			if !errors.Is(err, ErrInvalidRapideMembership) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want ErrInvalidRapideMembership containing %q", err, test.want)
			}
		})
	}
}

func TestRapideModuleObjectDenotationsRejectMalformedMembership(t *testing.T) {
	integerType := mustRapidePredefinedType(t, "Integer")
	booleanType := mustRapidePredefinedType(t, "Boolean")
	target := mustRapideInterfaceType(t, ProvidedRapideMember("Limit", integerType))
	wrong, _ := NewRapideObjectDenotation("Limit", booleanType, true)
	valid, _ := NewRapideObjectDenotation("Limit", integerType, int64(1))
	tests := []struct {
		name string
		got  []RapideObjectDenotation
		want string
	}{
		{name: "missing", want: `does not supply object "limit"`},
		{name: "duplicate", got: []RapideObjectDenotation{valid, valid}, want: `duplicate module object denotation "limit"`},
		{name: "wrong type", got: []RapideObjectDenotation{wrong}, want: `does not subtype its interface object type`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateRapideInterfaceObjectDenotations(target, nil, test.got...)
			if !errors.Is(err, ErrInvalidRapideMembership) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want ErrInvalidRapideMembership containing %q", err, test.want)
			}
		})
	}
	if _, err := NewRapideObjectDenotation("9bad", integerType, int64(1)); !errors.Is(err, ErrInvalidRapideMembership) {
		t.Fatalf("invalid object name error=%v", err)
	}
	if _, err := NewRapideObjectDenotation("Limit", RapideType{}, int64(1)); !errors.Is(err, ErrInvalidRapideMembership) {
		t.Fatalf("zero object type error=%v", err)
	}
	if _, err := NewRapideObjectDenotation("Limit", integerType, true); !errors.Is(err, ErrInvalidRapideMembership) {
		t.Fatalf("wrong object value error=%v", err)
	}
}

func TestRapideModuleObjectDenotationsRejectOverloadedTargetExplicitly(t *testing.T) {
	integerType := mustRapidePredefinedType(t, "Integer")
	booleanType := mustRapidePredefinedType(t, "Boolean")
	target := mustRapideInterfaceType(t,
		ProvidedRapideMember("Value", integerType),
		ProvidedRapideMember("Value", booleanType),
	)
	value, _ := NewRapideObjectDenotation("Value", integerType, int64(1))
	_, err := ValidateRapideInterfaceObjectDenotations(target, nil, value)
	if !errors.Is(err, ErrInvalidRapideMembership) ||
		!strings.Contains(err.Error(), `overloaded concrete interface object "value"`) {
		t.Fatalf("overloaded target error=%v", err)
	}
}

func TestRapideModuleObjectDenotationsIgnoreOrderAndGOMAXPROCS(t *testing.T) {
	integerType := mustRapidePredefinedType(t, "Integer")
	target := mustRapideInterfaceType(t,
		ProvidedRapideMember("A", integerType),
		ProvidedRapideMember("B", integerType),
	)
	a, _ := NewRapideObjectDenotation("A", integerType, int64(1))
	b, _ := NewRapideObjectDenotation("B", integerType, int64(2))
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for iteration := 0; iteration < 20; iteration++ {
		if iteration == 10 {
			runtime.GOMAXPROCS(4)
		}
		input := []RapideObjectDenotation{a, b}
		if iteration%2 != 0 {
			input = []RapideObjectDenotation{b, a}
		}
		normalized, err := ValidateRapideInterfaceObjectDenotations(target, nil, input...)
		if err != nil {
			t.Fatal(err)
		}
		if normalized[0].Name() != "a" || normalized[1].Name() != "b" {
			t.Fatalf("iteration %d normalization=%#v", iteration, normalized)
		}
	}
}
