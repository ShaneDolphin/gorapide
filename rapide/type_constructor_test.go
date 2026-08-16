package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestParseStanfordTypeConstructorNameDeclarations(t *testing.T) {
	file, err := Parse([]byte(`
type Collections is interface
  type Deque(type Element; Size, Capacity : Integer);
  type Named(type T <: Person) <: Person;
private
  type Exact(type T) is Person;
end interface Collections;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Interfaces) != 1 || len(file.Interfaces[0].TypeConstructors) != 3 {
		t.Fatalf("type-constructor AST=%#v", file.Interfaces)
	}
	constructors := file.Interfaces[0].TypeConstructors
	if constructors[0].Name != "Deque" || constructors[0].Region != InterfaceNameProvides ||
		constructors[0].Specification != InterfaceTypeNameAny || len(constructors[0].Parameters) != 3 {
		t.Fatalf("unbounded constructor=%#v", constructors[0])
	}
	if constructors[0].Parameters[0].Kind != InterfaceFormalTypeParameter ||
		constructors[0].Parameters[1].Kind != InterfaceFormalObjectParameter ||
		constructors[0].Parameters[2].Name != "Capacity" || constructors[0].Parameters[2].Type != "Integer" {
		t.Fatalf("normalized formal parameters=%#v", constructors[0].Parameters)
	}
	if constructors[1].Specification != InterfaceTypeNameSubtype || constructors[1].Type != "Person" ||
		constructors[1].Parameters[0].Type != "Person" {
		t.Fatalf("bounded constructor=%#v", constructors[1])
	}
	if constructors[2].Region != InterfaceNamePrivate || constructors[2].Specification != InterfaceTypeNameExact {
		t.Fatalf("private exact constructor=%#v", constructors[2])
	}
	if constructors[0].Parameters[1].Position == constructors[0].Parameters[2].Position {
		t.Fatal("formal object identifier-list normalization lost source positions")
	}
}

func TestCompileSourceTypeConstructorsAttachExactStructuralDescriptor(t *testing.T) {
	source := []byte(`
type Person is interface Name : String; Salary : Float; end interface Person;
type Salary_Info is interface Salary : Float; end interface Salary_Info;
type Collections is interface
  type General(type Element; Size : Integer);
  type Employees(type Element <: Person) <: Salary_Info;
private
  type Exact(type Element) is Person;
end interface Collections;
architecture System() is collections : Collections; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	component, ok := model.Component("collections")
	if !ok {
		t.Fatal("compiled collections component is absent")
	}
	got, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("type-constructor structural descriptor is absent")
	}
	stringType := mustRootRapidePredefinedType(t, "String")
	floatType := mustRootRapidePredefinedType(t, "Float")
	integerType := mustRootRapidePredefinedType(t, "Integer")
	personType := mustRootRapideInterfaceType(t,
		gorapide.ProvidedRapideMember("Name", stringType),
		gorapide.ProvidedRapideMember("Salary", floatType),
	)
	salaryType := mustRootRapideInterfaceType(t,
		gorapide.ProvidedRapideMember("Salary", floatType),
	)
	want := mustRootRapideInterfaceType(t,
		gorapide.UnboundedProvidedRapideTypeConstructor("General",
			gorapide.RapideTypeParameter("Element"),
			gorapide.RapideObjectParameter("Size", integerType)),
		gorapide.BoundedProvidedRapideTypeConstructor("Employees", salaryType,
			gorapide.BoundedRapideTypeParameter("Element", personType)),
		gorapide.ExactPrivateRapideTypeConstructor("Exact", personType,
			gorapide.RapideTypeParameter("Element")),
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
		t.Fatalf("compiled type constructors:\nwant %s\n got %s", wantBytes, gotBytes)
	}
}

func TestTypeConstructorClosedApplicationsElaborateInFormalsAndResult(t *testing.T) {
	source := []byte(`
type Collections is interface
  type Closed(type T <: Discrete(Integer); Cursor : Ref(Integer)) <: Iterator(Integer);
end interface Collections;
architecture System() is collections : Collections; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	component, ok := model.Component("collections")
	if !ok {
		t.Fatal("compiled collections component is absent")
	}
	got, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("type-constructor structural descriptor is absent")
	}
	integerType := mustRootRapidePredefinedType(t, "Integer")
	discrete, err := gorapide.ApplyRapideTypeConstructor("Discrete", integerType)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := gorapide.ApplyRapideTypeConstructor("Ref", integerType)
	if err != nil {
		t.Fatal(err)
	}
	iterator, err := gorapide.ApplyRapideTypeConstructor("Iterator", integerType)
	if err != nil {
		t.Fatal(err)
	}
	want := mustRootRapideInterfaceType(t,
		gorapide.BoundedProvidedRapideTypeConstructor("Closed", iterator,
			gorapide.BoundedRapideTypeParameter("T", discrete),
			gorapide.RapideObjectParameter("Cursor", reference)),
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
		t.Fatalf("closed constructor applications:\nwant %s\n got %s", wantBytes, gotBytes)
	}
}

func TestTypeConstructorDerivationCopiesAndRewritesClosedSignatures(t *testing.T) {
	declarations := mustParseInterfaceDeclarations(t, `
type Base is interface
  type Element;
  type Collection(type T <: Element; Size : Integer) <: Element;
end interface Base;
type Derived is include Base replace (Element to Value, Collection to Sequence); interface end interface Derived;
`)
	normalized, err := normalizeInterfaceDeclarations(declarations)
	if err != nil {
		t.Fatal(err)
	}
	derived := normalized["derived"]
	if len(derived.TypeConstructors) != 1 {
		t.Fatalf("derived constructors=%#v", derived.TypeConstructors)
	}
	constructor := derived.TypeConstructors[0]
	if constructor.Name != "Sequence" || constructor.Type != "Value" ||
		constructor.Parameters[0].Type != "Value" {
		t.Fatalf("constructor replacement=%#v", constructor)
	}
}

func TestDerivedTypeConstructorMatchesExplicitFlattening(t *testing.T) {
	derivedSource := []byte(`
type Salary_Info is interface Salary : Float; end interface Salary_Info;
type Base is interface type Collection(type Element; Size : Integer) <: Salary_Info; end interface Base;
type Derived is include Base replace (Collection to Sequence); interface end interface Derived;
architecture System() is collections : Derived; end architecture System;
`)
	flatSource := []byte(`
type Salary_Info is interface Salary : Float; end interface Salary_Info;
type Derived is interface type Sequence(type Element; Size : Integer) <: Salary_Info; end interface Derived;
architecture System() is collections : Derived; end architecture System;
`)
	derived, err := Compile(derivedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	flat, err := Compile(flatSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	left, err := derived.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	right, err := flat.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("derived type-constructor model %s != explicit flattening %s", left, right)
	}
}

func TestSourceTypeConstructorsAreCanonicalAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Salary_Info is interface Salary : Float; end interface Salary_Info;
type Collections is interface
  type General(type Element; Size : Integer);
  type Employees(type Element) <: Salary_Info;
end interface Collections;
architecture System() is collections : Collections; end architecture System;
`),
		[]byte(`
type Collections is interface
  type EMPLOYEES(type ELEMENT) <: salary_info;
  type GENERAL(type ELEMENT; SIZE : integer);
end interface Collections;
type Salary_Info is interface Salary : float; end interface Salary_Info;
architecture System() is collections : collections; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 20; iteration++ {
		if iteration == 10 {
			runtime.GOMAXPROCS(4)
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
			t.Fatalf("order/case/GOMAXPROCS changed type-constructor model: %s != %s", digest, baseline)
		}
	}
}

func TestSourceTypeConstructorsRejectUnsupportedOrConflictingForms(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{name: "requires", body: "requires type Collection(type T);", want: "type-constructor declarations are not permitted"},
		{name: "default object", body: "type Collection(Size : Integer is 1);", want: "formal object-parameter defaults require canonical object denotations"},
		{name: "duplicate formal", body: "type Collection(type T; T : Integer);", want: `duplicate formal parameter "T"`},
		{name: "formal object symbolic type", body: "type Collection(type T; Item : T);", want: `formal object-parameter type "T"`},
		{name: "formal bound symbolic type", body: "type Collection(type T <: U; type U);", want: `formal type-parameter bound "U"`},
		{name: "result symbolic type", body: "type Collection(type T) <: T;", want: `result expression "T" requires symbolic`},
		{name: "unknown closed type", body: "type Collection(type T) <: Missing;", want: `type "Missing" is outside`},
		{name: "constructor object collision", body: "type Collection(type T); Collection : String;", want: `type-constructor constituent "collection" collides`},
		{name: "constructor type collision", body: "type Collection(type T); type collection;", want: `constituent "collection" collides`},
		{name: "rich result", body: "type Collection(type T) <: interface end interface;", want: "rich type-expression form interface"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type Collections is interface " + test.body +
				" end interface Collections; architecture System() is collections : Collections; end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}
