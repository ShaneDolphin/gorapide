package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestSourceEqualityAndOrderApplicationsAttachPublishedInterfaces(t *testing.T) {
	source := []byte(`
type Eq_Int is Equality(Integer);
type Ordered_Int is Order(Integer);
type Schema is interface
  type Equal_Operations is Eq_Int;
  type Order_Operations is Ordered_Int;
end interface Schema;
architecture System() is schema : Schema; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	got := mustCompiledComponentRapideType(t, model, "schema")
	integer := mustRootRapidePredefinedType(t, "Integer")
	equality, err := gorapide.RapideEqualityType(integer)
	if err != nil {
		t.Fatal(err)
	}
	order, err := gorapide.RapideOrderType(integer)
	if err != nil {
		t.Fatal(err)
	}
	want := mustRootRapideInterfaceType(t,
		gorapide.ExactProvidedRapideTypeName("Equal_Operations", equality),
		gorapide.ExactProvidedRapideTypeName("Order_Operations", order),
	)
	gotBytes, err := got.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := want.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("source Equality/Order applications:\n%s\nwant:\n%s", gotBytes, wantBytes)
	}
}

func TestSourceEqualityAndOrderDeriveStructuralVariance(t *testing.T) {
	file, err := Parse([]byte(`
type Employee is interface Name : String; end interface Employee;
type Manager is interface Name : String; Department : String; end interface Manager;
type Employee_Equality is Equality(Employee);
type Manager_Equality is Equality(Manager);
type Employee_Order is Order(Employee);
type Manager_Order is Order(Manager);
`))
	if err != nil {
		t.Fatal(err)
	}
	interfaces, err := normalizeInterfaceDeclarationsWithAliases(file.Interfaces, file.TypeAliases)
	if err != nil {
		t.Fatal(err)
	}
	types, err := newSourceTypeElaborator(interfaces, file.TypeAliases)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{"Employee_Equality", "Manager_Equality"},
		{"Employee_Order", "Manager_Order"},
	} {
		left, err := types.resolveNamed(Position{Line: 1, Column: 1}, pair[0], nil)
		if err != nil {
			t.Fatal(err)
		}
		right, err := types.resolveNamed(Position{Line: 1, Column: 1}, pair[1], nil)
		if err != nil {
			t.Fatal(err)
		}
		if subtype, err := gorapide.IsRapideSubtype(left, right); err != nil || !subtype {
			t.Fatalf("%s <: %s = %v, %v", pair[0], pair[1], subtype, err)
		}
		if subtype, err := gorapide.IsRapideSubtype(right, left); err != nil || subtype {
			t.Fatalf("reversed %s/%s variance = %v, %v", pair[0], pair[1], subtype, err)
		}
	}
}

func TestSourceEqualityAndOrderAreCanonicalAcrossAliasesCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Eq_Int is Equality(Integer);
type Ord_Int is Order(Integer);
type Schema is interface type E is Eq_Int; type O is Ord_Int; end interface Schema;
architecture System() is schema : Schema; end architecture System;
`),
		[]byte(`
type Ord_Alias is order(integer);
type Schema is interface type o is ORD_ALIAS; type e is eq_alias; end interface Schema;
type Eq_Alias is EQUALITY(INTEGER);
architecture System() is schema : schema; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 30; iteration++ {
		if iteration == 15 {
			runtime.GOMAXPROCS(8)
		}
		model, err := Compile(sources[iteration%2], "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if baseline == "" {
			baseline = digest
		} else if digest != baseline {
			t.Fatalf("Equality/Order alias/case/GOMAXPROCS changed model: %s != %s", baseline, digest)
		}
	}
}

func TestSourceEqualityAndOrderRejectWrongArity(t *testing.T) {
	for _, test := range []struct {
		source string
		want   string
	}{
		{source: `type Bad is Equality(); architecture System() is end architecture System;`, want: "has 0 arguments, want 1"},
		{source: `type Bad is Order(Integer, String); architecture System() is end architecture System;`, want: "has 2 arguments, want 1"},
	} {
		_, err := Compile([]byte(test.source), "System")
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("got %v, want error containing %q", err, test.want)
		}
	}
}
