package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestParseClosedNamedTypeAlias(t *testing.T) {
	file, err := Parse([]byte(`type Count is Integer;`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.TypeAliases) != 1 || len(file.Interfaces) != 0 {
		t.Fatalf("parsed file=%#v", file)
	}
	alias := file.TypeAliases[0]
	if alias.Name != "Count" || alias.Target != "Integer" || alias.Position.Line != 1 || alias.Position.Column != 1 {
		t.Fatalf("type alias AST=%#v", alias)
	}
}

func TestParseClosedTypeConstructorApplicationAlias(t *testing.T) {
	file, err := Parse([]byte(`type Cursor is Ref(Iterator(Integer));`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.TypeAliases) != 1 {
		t.Fatalf("parsed file=%#v", file)
	}
	alias := file.TypeAliases[0]
	if alias.Name != "Cursor" || alias.Target != "Ref(Iterator(Integer))" ||
		alias.Expression.Kind != TypeExpressionApplication || alias.Expression.Name != "Ref" ||
		len(alias.Expression.Arguments) != 1 {
		t.Fatalf("constructor alias AST=%#v", alias)
	}
	iterator := alias.Expression.Arguments[0]
	if iterator.Kind != TypeExpressionApplication || iterator.Name != "Iterator" ||
		len(iterator.Arguments) != 1 || iterator.Arguments[0].Kind != TypeExpressionName ||
		iterator.Arguments[0].Name != "Integer" {
		t.Fatalf("nested constructor AST=%#v", iterator)
	}
}

func TestClosedPredefinedConstructorAliasesElaborateStructurally(t *testing.T) {
	source := []byte(`
type Employee is interface
  Name : String;
  Salary : Float;
end interface Employee;
type Employee_Iterator is Iterator(Employee);
type Employee_Iterable is Iterable(Employee);
type Employee_Cursor is Ref(Employee_Iterator);
type Employee_Order is Discrete(Employee);
type Schema is interface
  Items : Employee_Iterator;
  Collection : Employee_Iterable;
  Cursor : Employee_Cursor;
  Order : Employee_Order;
end interface Schema;
architecture System() is schema : Schema; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	component, ok := model.Component("schema")
	if !ok {
		t.Fatal("compiled schema component is absent")
	}
	got, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("schema structural descriptor is absent")
	}
	stringType := mustRootRapidePredefinedType(t, "String")
	floatType := mustRootRapidePredefinedType(t, "Float")
	employee := mustRootRapideInterfaceType(t,
		gorapide.ProvidedRapideMember("Name", stringType),
		gorapide.ProvidedRapideMember("Salary", floatType),
	)
	iterator, err := gorapide.ApplyRapideTypeConstructor("Iterator", employee)
	if err != nil {
		t.Fatal(err)
	}
	iterable, err := gorapide.ApplyRapideTypeConstructor("Iterable", employee)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := gorapide.ApplyRapideTypeConstructor("Ref", iterator)
	if err != nil {
		t.Fatal(err)
	}
	order, err := gorapide.ApplyRapideTypeConstructor("Discrete", employee)
	if err != nil {
		t.Fatal(err)
	}
	want := mustRootRapideInterfaceType(t,
		gorapide.ProvidedRapideMember("Items", iterator),
		gorapide.ProvidedRapideMember("Collection", iterable),
		gorapide.ProvidedRapideMember("Cursor", cursor),
		gorapide.ProvidedRapideMember("Order", order),
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
		t.Fatalf("constructor alias elaboration:\nwant %s\n got %s", wantBytes, gotBytes)
	}
}

func TestDirectClosedTypeApplicationsElaborateStructurally(t *testing.T) {
	directSource := []byte(`
type Schema is interface
  Items : Iterator(Integer);
  Collection : Iterable(Integer);
  type Cursor is Ref(Iterator(Integer));
  type Order <: Discrete(Integer);
end interface Schema;
architecture System() is schema : Schema; end architecture System;
`)
	aliasSource := []byte(`
type Items_Type is Iterator(Integer);
type Collection_Type is Iterable(Integer);
type Cursor_Type is Ref(Iterator(Integer));
type Order_Type is Discrete(Integer);
type Schema is interface
  Items : Items_Type;
  Collection : Collection_Type;
  type Cursor is Cursor_Type;
  type Order <: Order_Type;
end interface Schema;
architecture System() is schema : Schema; end architecture System;
`)
	direct, err := Compile(directSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	aliased, err := Compile(aliasSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	directDigest, err := direct.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	aliasDigest, err := aliased.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if directDigest != aliasDigest {
		t.Fatalf("direct type application retained source spelling identity: %s != %s", directDigest, aliasDigest)
	}

	file, err := Parse(directSource)
	if err != nil {
		t.Fatal(err)
	}
	declaration := file.Interfaces[0]
	if len(declaration.Objects) != 2 ||
		declaration.Objects[0].TypeExpression.Kind != TypeExpressionApplication ||
		declaration.Objects[1].TypeExpression.Kind != TypeExpressionApplication ||
		len(declaration.TypeNames) != 2 ||
		declaration.TypeNames[0].TypeExpression.Kind != TypeExpressionApplication ||
		declaration.TypeNames[1].TypeExpression.Kind != TypeExpressionApplication {
		t.Fatalf("direct structural type-expression AST=%#v", declaration)
	}
}

func TestDirectClosedTypeApplicationsInExecutableSlotsFailExplicitly(t *testing.T) {
	tests := []struct {
		name, declaration string
	}{
		{name: "action parameter", declaration: "action in Accept(value : Iterator(Integer));"},
		{name: "function parameter", declaration: "provides Read : function(value : Ref(Integer)) return Integer;"},
		{name: "function result", declaration: "provides Read : function() return Ref(Integer);"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type API is interface " + test.declaration +
				" end interface API; architecture System() is api : API; end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(),
				"cannot yet be used in an execution-facing predefined value slot") {
				t.Fatalf("got %v, want explicit execution-facing type-application boundary", err)
			}
		})
	}
}

func TestDirectClosedTypeApplicationsInStateAndObjectValuesFailExplicitly(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{
			name: "module object",
			body: "Value : Ref(Integer) is 1;",
			want: "cannot yet be used in an execution-facing predefined value slot",
		},
		{
			name: "module state",
			body: "Value : var Ref(Integer) := 1;",
			want: "cannot yet be used in the predefined-scalar state value kernel",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type API is interface end interface API; module Impl() return API is " +
				test.body + " end module Impl; architecture System() is api : API is Impl(); end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestClosedTypeAliasesHaveNoNominalExecutionIdentity(t *testing.T) {
	aliased := []byte(`
type Count is Natural;
type Number is Count;
type API is interface
  action in Accept(value : Number);
  provides Convert : function(value : Count) return Number;
end interface API;
architecture System() is api : API; end architecture System;
`)
	direct := []byte(`
type API is interface
  action in Accept(value : Natural);
  provides Convert : function(value : Natural) return Natural;
end interface API;
architecture System() is api : API; end architecture System;
`)
	aliasModel, err := Compile(aliased, "System")
	if err != nil {
		t.Fatal(err)
	}
	directModel, err := Compile(direct, "System")
	if err != nil {
		t.Fatal(err)
	}
	aliasDigest, err := aliasModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	directDigest, err := directModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if aliasDigest != directDigest {
		t.Fatalf("alias introduced nominal model identity: %s != %s", aliasDigest, directDigest)
	}
	component, ok := aliasModel.Component("api")
	if !ok {
		t.Fatal("compiled API component is absent")
	}
	if got := component.Interface.Actions[0].Params[0].Type; got != "Natural" {
		t.Fatalf("execution alias lost Natural membership constraint: %q", got)
	}
	if got := component.Interface.Functions[0]; got.Params[0].Type != "Natural" || got.ReturnType != "Natural" {
		t.Fatalf("function alias elaboration=%#v", got)
	}
}

func TestInterfaceAliasesResolveForComponentsAndModuleReturns(t *testing.T) {
	aliased := []byte(`
type API_Alias is API;
type Public_API is API_Alias;
type API is interface action out Ready(value : Integer); end interface API;
module Implementation() return API_Alias is
initial Ready(1);
end module Implementation;
architecture System() is api : Public_API is Implementation(); end architecture System;
`)
	direct := []byte(`
type API is interface action out Ready(value : Integer); end interface API;
module Implementation() return API is
initial Ready(1);
end module Implementation;
architecture System() is api : API is Implementation(); end architecture System;
`)
	aliasModel, err := Compile(aliased, "System")
	if err != nil {
		t.Fatal(err)
	}
	directModel, err := Compile(direct, "System")
	if err != nil {
		t.Fatal(err)
	}
	aliasDigest, err := aliasModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	directDigest, err := directModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if aliasDigest != directDigest {
		t.Fatalf("interface alias introduced nominal model identity: %s != %s", aliasDigest, directDigest)
	}
	journal := arch.NewExecutionJournal(aliasDigest, 10)
	aliasResult, err := aliasModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	directJournal := arch.NewExecutionJournal(directDigest, 10)
	directResult, err := directModel.ExecuteDeterministic(directJournal)
	if err != nil {
		t.Fatal(err)
	}
	aliasBytes, err := aliasResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	directBytes, err := directResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aliasBytes, directBytes) {
		t.Fatalf("interface aliases changed deterministic execution:\n%s\n%s", aliasBytes, directBytes)
	}
}

func TestAcyclicNamedInterfacesElaborateToClosedStructuralTypes(t *testing.T) {
	source := []byte(`
type Text is String;
type Label is Text;
type Person is interface
  Name : Label;
end interface Person;
type Schema is interface
  Employee : Person;
  type Person_Type <: Person;
end interface Schema;
architecture System() is schema : Schema; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	component, ok := model.Component("schema")
	if !ok {
		t.Fatal("compiled schema component is absent")
	}
	got, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("schema structural descriptor is absent")
	}
	stringType := mustRootRapidePredefinedType(t, "String")
	personType := mustRootRapideInterfaceType(t,
		gorapide.ProvidedRapideMember("Name", stringType),
	)
	want := mustRootRapideInterfaceType(t,
		gorapide.ProvidedRapideMember("Employee", personType),
		gorapide.BoundedProvidedRapideTypeName("Person_Type", personType),
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
		t.Fatalf("named-interface elaboration:\nwant %s\n got %s", wantBytes, gotBytes)
	}
}

func TestClosedTypeElaborationIsCanonicalAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Text is String;
type Label is Text;
type Person is interface Name : Label; Age : Integer; end interface Person;
type Schema is interface Employee : Person; type Person_Type <: Person; end interface Schema;
architecture System() is schema : Schema; end architecture System;
`),
		[]byte(`
type Schema is interface type person_type <: PERSON; Employee : person; end interface Schema;
type LABEL is text;
type PERSON is interface Age : integer; Name : label; end interface PERSON;
type text is string;
architecture System() is schema : schema; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 20; iteration++ {
		if iteration == 10 {
			runtime.GOMAXPROCS(4)
		}
		model, err := Compile(sources[iteration%len(sources)], "System")
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
			t.Fatalf("order/case/GOMAXPROCS changed type elaboration: %s != %s", digest, baseline)
		}
	}
}

func TestClosedTypeElaborationRejectsInvalidNamespacesAndGraphs(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{
			name: "duplicate alias",
			source: `type Count is Integer; type count is Integer;
type API is interface end interface API; architecture System() is api : API; end architecture System;`,
			want: `duplicate type alias "count"`,
		},
		{
			name: "alias predefined collision",
			source: `type integer is String;
type API is interface end interface API; architecture System() is api : API; end architecture System;`,
			want: `type alias "integer" collides with predefined type "Integer"`,
		},
		{
			name: "interface predefined collision",
			source: `type INTEGER is interface Name : String; end interface INTEGER;
architecture System() is value : INTEGER; end architecture System;`,
			want: `interface type "INTEGER" collides with predefined type "Integer"`,
		},
		{
			name: "alias interface collision",
			source: `type Value is String; type value is interface Name : String; end interface value;
architecture System() is item : value; end architecture System;`,
			want: `type alias "Value" collides with interface type "value"`,
		},
		{
			name: "unknown eager alias target",
			source: `type Count is Missing;
type API is interface end interface API; architecture System() is api : API; end architecture System;`,
			want: `type "Missing" is outside`,
		},
		{
			name: "alias cycle",
			source: `type A is B; type B is A;
type API is interface end interface API; architecture System() is api : API; end architecture System;`,
			want: `recursive type alias cycle A -> B -> A has no interface constructor to bind`,
		},
		{
			name: "interface in private action value slot",
			source: `type Value is interface Name : String; end interface Value;
type Alias is Value;
type API is interface private action Accept(value : Alias); end interface API;
architecture System() is api : API; end architecture System;`,
			want: `interface type "Value" cannot yet be adapted to an execution-facing predefined value slot`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestTypeAliasParserRejectsRichTypeExpressionsExplicitly(t *testing.T) {
	_, err := Parse([]byte(`type Callable is function();`))
	if err == nil || !strings.Contains(err.Error(), "rich type-expression form function is outside") {
		t.Fatalf("Parse(function type)=%v, want explicit rich-form boundary", err)
	}
}

func TestTypeConstructorApplicationsRejectUnknownArityCyclesAndObsoleteForms(t *testing.T) {
	tests := []struct {
		name, declaration, want string
	}{
		{name: "unknown", declaration: `type Value is List(Integer);`, want: `unknown closed predefined type constructor "List"`},
		{name: "missing argument", declaration: `type Value is Iterator();`, want: "has 0 arguments, want 1"},
		{name: "extra argument", declaration: `type Value is Ref(Integer,String);`, want: "has 2 arguments, want 1"},
		{name: "range obsolete", declaration: `type Value is Range(Integer);`, want: "withheld because the published draft denotation is obsolete"},
		{name: "application cycle", declaration: `type Value is Ref(Value);`, want: "recursive type alias cycle Value -> Value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(test.declaration + `
type API is interface end interface API;
architecture System() is api : API; end architecture System;`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}
