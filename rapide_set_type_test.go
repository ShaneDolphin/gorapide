package gorapide

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestRapideSetTypeMatchesPublishedRecursiveInterface(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	got, err := RapideSetType(integer)
	if err != nil {
		t.Fatal(err)
	}
	want := mustPublishedRapideSetType(t, integer)
	gotBytes := mustMarshalRapideType(t, got)
	wantBytes := mustMarshalRapideType(t, want)
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("Set(Integer):\n%s\nwant published interface:\n%s", gotBytes, wantBytes)
	}
	parsed, err := ParseRapideType(gotBytes)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip := mustMarshalRapideType(t, parsed); !bytes.Equal(roundTrip, gotBytes) {
		t.Fatalf("Set recursive descriptor changed on round trip:\n%s\n%s", gotBytes, roundTrip)
	}
	equality, err := RapideEqualityType(got)
	if err != nil {
		t.Fatal(err)
	}
	if subtype, err := IsRapideSubtype(got, equality); err != nil || !subtype {
		t.Fatalf("Set(Integer) did not include Equality(Set(Integer)): %v, %v", subtype, err)
	}
}

func mustPublishedRapideSetType(t *testing.T, element RapideType) RapideType {
	t.Helper()
	boolean := mustRapidePredefinedType(t, "Boolean")
	natural := mustRapidePredefinedType(t, "Natural")
	result, err := NewSelfRecursiveRapideInterfaceType(func(self RapideType) (RapideType, error) {
		setComparison, err := NewRapideFunctionType(
			[]RapideFunctionParameter{RapideObjectParameter("rhs", self)}, boolean)
		if err != nil {
			return RapideType{}, err
		}
		membership, err := NewRapideFunctionType(
			[]RapideFunctionParameter{RapideObjectParameter("E", element)}, boolean)
		if err != nil {
			return RapideType{}, err
		}
		elementOperation, err := NewRapideFunctionType(
			[]RapideFunctionParameter{RapideObjectParameter("E", element)}, self)
		if err != nil {
			return RapideType{}, err
		}
		setOperation, err := NewRapideFunctionType(
			[]RapideFunctionParameter{RapideObjectParameter("S", self)}, self)
		if err != nil {
			return RapideType{}, err
		}
		cardinality, err := NewRapideFunctionType(nil, natural)
		if err != nil {
			return RapideType{}, err
		}
		return NewRapideInterfaceType(
			ProvidedRapideMember("=", setComparison),
			ProvidedRapideMember("/=", setComparison),
			ProvidedRapideMember("Is_Member", membership),
			ProvidedRapideMember("+", elementOperation),
			ProvidedRapideMember("&", setOperation),
			ProvidedRapideMember("-", elementOperation),
			ProvidedRapideMember("-", setOperation),
			ProvidedRapideMember("<", setComparison),
			ProvidedRapideMember("Cardinality", cardinality),
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRapideSetTypeEnforcesElementEqualityConstraint(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	nonEquatable := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
	)
	if _, err := RapideSetType(nonEquatable); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), "does not subtype Equality(element)") {
		t.Fatalf("non-equatable Set element error = %v", err)
	}

	equatable, err := NewSelfRecursiveRapideInterfaceType(func(self RapideType) (RapideType, error) {
		boolean := mustRapidePredefinedType(t, "Boolean")
		comparison, err := NewRapideFunctionType(
			[]RapideFunctionParameter{RapideObjectParameter("rhs", self)}, boolean)
		if err != nil {
			return RapideType{}, err
		}
		return NewRapideInterfaceType(
			ProvidedRapideMember("Name", stringType),
			ProvidedRapideMember("=", comparison),
			ProvidedRapideMember("/=", comparison),
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RapideSetType(equatable); err != nil {
		t.Fatalf("explicit Equality(element) implementation rejected: %v", err)
	}
	integer := mustRapidePredefinedType(t, "Integer")
	inner, err := RapideSetType(integer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RapideSetType(inner); err != nil {
		t.Fatalf("Set(Set(Integer)) rejected despite inherited set equality: %v", err)
	}
}

func TestRapideSetTypeUsesPublishedInvariantElementRule(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	stringType := mustRapidePredefinedType(t, "String")
	integers, err := RapideSetType(integer)
	if err != nil {
		t.Fatal(err)
	}
	stringsSet, err := RapideSetType(stringType)
	if err != nil {
		t.Fatal(err)
	}
	if subtype, err := IsRapideSubtype(integers, stringsSet); err != nil || subtype {
		t.Fatalf("Set(Integer) <: Set(String) = %v, %v", subtype, err)
	}
	if subtype, err := IsRapideSubtype(stringsSet, integers); err != nil || subtype {
		t.Fatalf("Set(String) <: Set(Integer) = %v, %v", subtype, err)
	}
	integersAgain, err := RapideSetType(integer)
	if err != nil {
		t.Fatal(err)
	}
	if subtype, err := IsRapideSubtype(integers, integersAgain); err != nil || !subtype {
		t.Fatalf("Set(Integer) structural equality subtype = %v, %v", subtype, err)
	}
}

func TestSetApplicationsAreCanonicalAcrossCaseAndGOMAXPROCS(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for iteration := 0; iteration < 30; iteration++ {
		if iteration == 15 {
			runtime.GOMAXPROCS(8)
		}
		spelling := "Set"
		if iteration%2 != 0 {
			spelling = "SET"
		}
		typ, err := ApplyRapideTypeConstructor(spelling, integer)
		if err != nil {
			t.Fatal(err)
		}
		encoded := mustMarshalRapideType(t, typ)
		if baseline == nil {
			baseline = encoded
		} else if !bytes.Equal(encoded, baseline) {
			t.Fatalf("Set case/GOMAXPROCS changed descriptor:\n%s\n%s", baseline, encoded)
		}
	}
}

func TestSetConstructorRejectsInvalidArguments(t *testing.T) {
	if _, err := RapideSetType(RapideType{}); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), "Set element type is invalid") {
		t.Fatalf("invalid Set element error = %v", err)
	}
	if _, err := ApplyRapideTypeConstructor("Set"); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), "has 0 arguments, want 1") {
		t.Fatalf("Set arity error = %v", err)
	}
}
