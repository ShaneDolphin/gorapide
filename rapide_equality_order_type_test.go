package gorapide

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestRapideEqualityAndOrderTypesMatchPublishedInterfaces(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	boolean := mustRapidePredefinedType(t, "Boolean")
	comparison := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("rhs", integer)}, boolean,
	)

	equality, err := RapideEqualityType(integer)
	if err != nil {
		t.Fatal(err)
	}
	wantEquality := mustRapideInterfaceType(t,
		ProvidedRapideMember("=", comparison),
		ProvidedRapideMember("/=", comparison),
	)
	if got, want := mustMarshalRapideType(t, equality), mustMarshalRapideType(t, wantEquality); !bytes.Equal(got, want) {
		t.Fatalf("Equality(Integer):\n%s\nwant:\n%s", got, want)
	}

	order, err := RapideOrderType(integer)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := mustRapideInterfaceType(t,
		ProvidedRapideMember("=", comparison),
		ProvidedRapideMember("/=", comparison),
		ProvidedRapideMember("<", comparison),
		ProvidedRapideMember("<=", comparison),
		ProvidedRapideMember(">", comparison),
		ProvidedRapideMember(">=", comparison),
	)
	if got, want := mustMarshalRapideType(t, order), mustMarshalRapideType(t, wantOrder); !bytes.Equal(got, want) {
		t.Fatalf("Order(Integer):\n%s\nwant:\n%s", got, want)
	}
	if subtype, err := IsRapideSubtype(order, equality); err != nil || !subtype {
		t.Fatalf("Order(Integer) <: Equality(Integer) = %v, %v", subtype, err)
	}
}

func TestRapideEqualityAndOrderDeriveContravariantOperandSubtyping(t *testing.T) {
	stringType := mustRapidePredefinedType(t, "String")
	employee := mustRapideInterfaceType(t, ProvidedRapideMember("Name", stringType))
	manager := mustRapideInterfaceType(t,
		ProvidedRapideMember("Name", stringType),
		ProvidedRapideMember("Department", stringType),
	)
	if !mustRapideSubtype(t, manager, employee) {
		t.Fatal("test prerequisite Manager <: Employee failed")
	}
	for _, constructor := range []struct {
		name string
		make func(RapideType) (RapideType, error)
	}{
		{name: "Equality", make: RapideEqualityType},
		{name: "Order", make: RapideOrderType},
	} {
		employeeOperations, err := constructor.make(employee)
		if err != nil {
			t.Fatal(err)
		}
		managerOperations, err := constructor.make(manager)
		if err != nil {
			t.Fatal(err)
		}
		if subtype, err := IsRapideSubtype(employeeOperations, managerOperations); err != nil || !subtype {
			t.Fatalf("%s(Employee) <: %s(Manager) = %v, %v", constructor.name, constructor.name, subtype, err)
		}
		if subtype, err := IsRapideSubtype(managerOperations, employeeOperations); err != nil || subtype {
			t.Fatalf("reversed %s operand variance = %v, %v", constructor.name, subtype, err)
		}
	}
}

func TestEqualityAndOrderApplicationsAreCanonicalAcrossCaseAndGOMAXPROCS(t *testing.T) {
	integer := mustRapidePredefinedType(t, "Integer")
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, constructor := range []string{"Equality", "Order"} {
		var baseline []byte
		for iteration := 0; iteration < 30; iteration++ {
			if iteration == 15 {
				runtime.GOMAXPROCS(8)
			}
			spelling := constructor
			if iteration%2 != 0 {
				spelling = strings.ToUpper(constructor)
			}
			typ, err := ApplyRapideTypeConstructor(spelling, integer)
			if err != nil {
				t.Fatal(err)
			}
			encoded := mustMarshalRapideType(t, typ)
			if baseline == nil {
				baseline = encoded
			} else if !bytes.Equal(encoded, baseline) {
				t.Fatalf("%s case/GOMAXPROCS changed descriptor:\n%s\n%s", constructor, baseline, encoded)
			}
		}
	}
}

func TestEqualityAndOrderConstructorsRejectInvalidArguments(t *testing.T) {
	if _, err := RapideEqualityType(RapideType{}); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), "Equality item type is invalid") {
		t.Fatalf("invalid Equality argument error = %v", err)
	}
	if _, err := RapideOrderType(RapideType{}); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), "Order item type is invalid") {
		t.Fatalf("invalid Order argument error = %v", err)
	}
	integer := mustRapidePredefinedType(t, "Integer")
	if _, err := ApplyRapideTypeConstructor("Equality"); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), "has 0 arguments, want 1") {
		t.Fatalf("Equality arity error = %v", err)
	}
	if _, err := ApplyRapideTypeConstructor("Order", integer, integer); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), "has 2 arguments, want 1") {
		t.Fatalf("Order arity error = %v", err)
	}
}
