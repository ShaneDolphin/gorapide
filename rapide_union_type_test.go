package gorapide

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestRapideUnionTypeLowersToPublishedDependentFunction(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	boolean := mustRapidePredefinedType(t, "Boolean")
	union, err := NewRapideUnionType(
		RapideUnionTag("Int", integer),
		RapideUnionTag("Bool", boolean),
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded := mustMarshalRapideType(t, union)
	want := `{"format":"gorapide.rapide-type.v2","type":{"kind":"function","parameters":[` +
		`{"name":"t","kind":"type"},` +
		`{"name":"p","type":{"kind":"interface","members":[` +
		`{"region":"provides","name":"bool","type":{"kind":"function","parameters":[` +
		`{"name":"o","type":{"kind":"predefined","name":"Boolean"}}],` +
		`"result":{"kind":"type_reference","name":"t"}}},` +
		`{"region":"provides","name":"int","type":{"kind":"function","parameters":[` +
		`{"name":"o","type":{"kind":"predefined","name":"Integer"}}],` +
		`"result":{"kind":"type_reference","name":"t"}}}]}}],` +
		`"result":{"kind":"type_reference","name":"t"}}}`
	if string(encoded) != want {
		t.Fatalf("Union function reduction:\n%s\nwant:\n%s", encoded, want)
	}
	parsed, err := ParseRapideType(encoded)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip := mustMarshalRapideType(t, parsed)
	if !bytes.Equal(roundTrip, encoded) {
		t.Fatalf("dependent Union descriptor changed on round trip:\n%s\n%s", encoded, roundTrip)
	}
}

func TestRapideUnionSubtypeUsesTagSubsetAndMemberCovariance(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	booleanType := mustRapidePredefinedType(t, "Boolean")
	employee := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
	)
	manager := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Department", stringType),
	)
	general, err := NewRapideUnionType(
		RapideUnionTag("Person", employee),
		RapideUnionTag("Enabled", booleanType),
	)
	if err != nil {
		t.Fatal(err)
	}
	special, err := NewRapideUnionType(
		RapideUnionTag("Person", manager),
	)
	if err != nil {
		t.Fatal(err)
	}
	if subtype, err := IsRapideSubtype(special, general); err != nil || !subtype {
		t.Fatalf("tag subset plus covariant member subtype = %v, %v", subtype, err)
	}
	if subtype, err := IsRapideSubtype(general, special); err != nil || subtype {
		t.Fatalf("reversed Union width subtype = %v, %v", subtype, err)
	}

	wrongTag, err := NewRapideUnionType(RapideUnionTag("Worker", manager))
	if err != nil {
		t.Fatal(err)
	}
	if subtype, err := IsRapideSubtype(wrongTag, general); err != nil || subtype {
		t.Fatalf("different Union tag subtype = %v, %v", subtype, err)
	}
}

func TestRapideUnionTypeIsCanonicalAcrossTagOrderCaseAndGOMAXPROCS(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	boolean := mustRapidePredefinedType(t, "Boolean")
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for iteration := 0; iteration < 30; iteration++ {
		if iteration == 15 {
			runtime.GOMAXPROCS(8)
		}
		members := []RapideUnionMember{
			RapideUnionTag("Int", integer),
			RapideUnionTag("Bool", boolean),
		}
		if iteration%2 != 0 {
			members = []RapideUnionMember{
				RapideUnionTag("BOOL", boolean),
				RapideUnionTag("INT", integer),
			}
		}
		union, err := NewRapideUnionType(members...)
		if err != nil {
			t.Fatal(err)
		}
		encoded := mustMarshalRapideType(t, union)
		if baseline == nil {
			baseline = encoded
		} else if !bytes.Equal(encoded, baseline) {
			t.Fatalf("tag order/case/GOMAXPROCS changed Union descriptor:\n%s\n%s", baseline, encoded)
		}
	}
}

func TestRapideUnionTypeRejectsDuplicateInvalidAndUnscopedReferences(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	boolean := mustRapidePredefinedType(t, "Boolean")
	if _, err := NewRapideUnionType(
		RapideUnionTag("Value", integer),
		RapideUnionTag("value", boolean),
	); !errors.Is(err, ErrInvalidRapideType) || !strings.Contains(err.Error(), `duplicate Union tag "value"`) {
		t.Fatalf("duplicate Union tag error = %v", err)
	}
	if _, err := NewRapideUnionType(RapideUnionTag("Value", RapideType{})); !errors.Is(err, ErrInvalidRapideType) || !strings.Contains(err.Error(), "invalid member type") {
		t.Fatalf("invalid Union member error = %v", err)
	}
	reference, err := NewRapideTypeParameterReference("T")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reference.MarshalCanonical(); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), `unscoped type-name reference "t"`) {
		t.Fatalf("standalone function type reference error = %v", err)
	}
}

func TestRapideFunctionTypeParameterReferencesHaveSequentialScope(t *testing.T) {
	reference, err := NewRapideTypeParameterReference("T")
	if err != nil {
		t.Fatal(err)
	}
	forward, err := NewRapideFunctionType([]RapideFunctionParameter{
		RapideObjectParameter("Value", reference),
		RapideTypeParameter("T"),
	}, reference)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := forward.MarshalCanonical(); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), `function parameter "value"`) ||
		!strings.Contains(err.Error(), `unscoped type-name reference "t"`) {
		t.Fatalf("forward function type-parameter reference error = %v", err)
	}

	canonicalForward := []byte(`{"format":"gorapide.rapide-type.v2","type":{"kind":"function","parameters":[{"name":"value","type":{"kind":"type_reference","name":"t"}},{"name":"t","kind":"type"}],"result":{"kind":"type_reference","name":"t"}}}`)
	if _, err := ParseRapideType(canonicalForward); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), `unscoped type-name reference "t"`) {
		t.Fatalf("canonical forward function type-parameter reference error = %v", err)
	}
}
