package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestParseStanfordObjectAndTypeNameDeclarationsByRegion(t *testing.T) {
	file, err := Parse([]byte(`
type Schema is interface
  type Any_Element;
  type Text_Element is String;
  Name, Occupation : String;
  Read, Show : function(value : Integer) return String;
requires
  External : Integer;
private
  type Representation <: String;
  Cache : String;
  Hidden : function(value : Integer);
end interface Schema;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Interfaces) != 1 {
		t.Fatalf("interfaces=%d", len(file.Interfaces))
	}
	declaration := file.Interfaces[0]
	if len(declaration.TypeNames) != 3 ||
		declaration.TypeNames[0].Region != InterfaceNameProvides ||
		declaration.TypeNames[0].Specification != InterfaceTypeNameAny ||
		declaration.TypeNames[1].Specification != InterfaceTypeNameExact ||
		declaration.TypeNames[2].Region != InterfaceNamePrivate ||
		declaration.TypeNames[2].Specification != InterfaceTypeNameSubtype {
		t.Fatalf("type-name AST=%#v", declaration.TypeNames)
	}
	if len(declaration.Objects) != 4 || declaration.Objects[0].Name != "Name" ||
		declaration.Objects[1].Name != "Occupation" ||
		declaration.Objects[2].Region != InterfaceNameRequires || declaration.Objects[2].Name != "External" ||
		declaration.Objects[3].Region != InterfaceNamePrivate || declaration.Objects[3].Name != "Cache" {
		t.Fatalf("object AST=%#v", declaration.Objects)
	}
	if len(declaration.Functions) != 3 || declaration.Functions[0].Name != "Read" ||
		declaration.Functions[1].Name != "Show" || declaration.Functions[2].Mode != FunctionPrivate {
		t.Fatalf("function-object AST=%#v", declaration.Functions)
	}
	if declaration.Objects[0].Position == declaration.Objects[1].Position {
		t.Fatal("normalized identifier-list objects lost their individual source positions")
	}
}

func TestCompileSourceObjectAndTypeNamesAttachExactStructuralDescriptor(t *testing.T) {
	source := []byte(`
type Schema is interface
  type Any_Element;
  type Text_Element is String;
  Name, Occupation : String;
  action out Changed(value : Integer);
  provides Read : function(value : Integer) return String;
requires
  External : Integer;
private
  type Representation <: String;
  Cache : String;
  Hidden : function(value : Integer);
end interface Schema;
architecture System() is schema : Schema; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	component, ok := model.Component("schema")
	if !ok {
		t.Fatal("compiled component is absent")
	}
	got, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("source structural type was not attached to the execution interface")
	}
	stringType := mustRootRapidePredefinedType(t, "String")
	integerType := mustRootRapidePredefinedType(t, "Integer")
	eventType := mustRootRapideEventType(t, gorapide.RapideEventParam("value", integerType))
	readType := mustRootRapideFunctionType(t,
		[]gorapide.RapideFunctionParameter{gorapide.RapideObjectParameter("value", integerType)}, stringType)
	hiddenType := mustRootRapideFunctionType(t,
		[]gorapide.RapideFunctionParameter{gorapide.RapideObjectParameter("value", integerType)}, gorapide.RapideType{})
	want := mustRootRapideInterfaceType(t,
		gorapide.UnboundedProvidedRapideTypeName("Any_Element"),
		gorapide.ExactProvidedRapideTypeName("Text_Element", stringType),
		gorapide.ProvidedRapideMember("Name", stringType),
		gorapide.ProvidedRapideMember("Occupation", stringType),
		gorapide.OutputRapideAction("Changed", eventType),
		gorapide.ProvidedRapideMember("Read", readType),
		gorapide.RequiredRapideMember("External", integerType),
		gorapide.BoundedPrivateRapideTypeName("Representation", stringType),
		gorapide.PrivateRapideMember("Cache", stringType),
		gorapide.PrivateRapideMember("Hidden", hiddenType),
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
		t.Fatalf("compiled structural type:\n%s\n%s", wantBytes, gotBytes)
	}
	if _, err := model.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}
}

func TestInterfaceNameDerivationCopiesRegionsAndRewritesReferences(t *testing.T) {
	declarations := mustParseInterfaceDeclarations(t, `
type Base is interface
  type Element;
  Item : Element;
  Public : function(value : Integer);
requires
  External : String;
private
  type Representation is String;
  Cache : Representation;
  Hidden : function();
end interface Base;
type Full is include Base replace (Element to Value, Representation to Storage); interface end interface Full;
type PublicOnly is interface include Base; end interface PublicOnly;
type RequiredOnly is interface requires include Base; end interface RequiredOnly;
type PrivateOnly is interface private include Base; end interface PrivateOnly;
`)
	normalized, err := normalizeInterfaceDeclarations(declarations)
	if err != nil {
		t.Fatal(err)
	}
	full := normalized["full"]
	if len(full.Objects) != 3 || len(full.TypeNames) != 2 || len(full.Functions) != 2 {
		t.Fatalf("full derivation objects=%#v types=%#v functions=%#v", full.Objects, full.TypeNames, full.Functions)
	}
	if got := findObjectType(full.Objects, "Item"); got != "Value" {
		t.Fatalf("replacement did not rewrite copied object type reference: %q", got)
	}
	if got := findObjectType(full.Objects, "Cache"); got != "Storage" {
		t.Fatalf("replacement did not rewrite copied private type reference: %q", got)
	}
	if findTypeName(full.TypeNames, "Value") == nil || findTypeName(full.TypeNames, "Storage") == nil {
		t.Fatalf("replacement did not rename copied type declarations: %#v", full.TypeNames)
	}
	publicOnly := normalized["publiconly"]
	if len(publicOnly.Objects) != 1 || len(publicOnly.TypeNames) != 1 ||
		len(publicOnly.Functions) != 1 || publicOnly.Functions[0].Mode != FunctionProvides {
		t.Fatalf("default/provides derivation=%#v", publicOnly)
	}
	requiredOnly := normalized["requiredonly"]
	if len(requiredOnly.Objects) != 1 || requiredOnly.Objects[0].Region != InterfaceNameRequires || len(requiredOnly.TypeNames) != 0 {
		t.Fatalf("requires derivation=%#v", requiredOnly)
	}
	privateOnly := normalized["privateonly"]
	if len(privateOnly.Objects) != 1 || privateOnly.Objects[0].Region != InterfaceNamePrivate ||
		len(privateOnly.TypeNames) != 1 || len(privateOnly.Functions) != 1 ||
		privateOnly.Functions[0].Mode != FunctionPrivate {
		t.Fatalf("private derivation=%#v", privateOnly)
	}
}

func TestInterfaceDerivationRecursivelyRewritesClosedTypeApplications(t *testing.T) {
	declarations := mustParseInterfaceDeclarations(t, `
type Base is interface
  type Element;
  Items : Iterator(Element);
  type Cursor is Ref(Element);
end interface Base;
type Derived is include Base replace (Element to Value); interface end interface Derived;
`)
	includePosition := declarations[1].Derivations[0].Position
	normalized, err := normalizeInterfaceDeclarations(declarations)
	if err != nil {
		t.Fatal(err)
	}
	derived := normalized["derived"]
	if len(derived.Objects) != 1 || derived.Objects[0].Type != "Iterator(Value)" {
		t.Fatalf("derived application object=%#v", derived.Objects)
	}
	objectExpression := derived.Objects[0].TypeExpression
	if objectExpression.Kind != TypeExpressionApplication || len(objectExpression.Arguments) != 1 ||
		objectExpression.Arguments[0].Name != "Value" {
		t.Fatalf("derived object type-expression=%#v", objectExpression)
	}
	if len(derived.TypeNames) != 2 {
		t.Fatalf("derived type names=%#v", derived.TypeNames)
	}
	var cursor *InterfaceTypeNameDecl
	for index := range derived.TypeNames {
		if folded(derived.TypeNames[index].Name) == "cursor" {
			cursor = &derived.TypeNames[index]
			break
		}
	}
	if cursor == nil || cursor.Type != "Ref(Value)" ||
		len(cursor.TypeExpression.Arguments) != 1 || cursor.TypeExpression.Arguments[0].Name != "Value" {
		t.Fatalf("derived exact type application=%#v", cursor)
	}
	if objectExpression.Position != includePosition ||
		objectExpression.Arguments[0].Position != includePosition {
		t.Fatal("derived type-expression diagnostics were not relocated to the include declaration")
	}
}

func TestDerivedObjectAndTypeNameInterfaceMatchesExplicitFlattening(t *testing.T) {
	derivedSource := []byte(`
type Base is interface
  type Element is String;
  Name : String;
requires External : Integer;
private Cache : String;
end interface Base;
type Derived is include Base replace (Element to Value, Name to Label); interface end interface Derived;
architecture System() is schema : Derived; end architecture System;
`)
	flatSource := []byte(`
type Derived is interface
  type Value is String;
  Label : String;
requires External : Integer;
private Cache : String;
end interface Derived;
architecture System() is schema : Derived; end architecture System;
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
		t.Fatalf("derived structural model %s != explicit flattening %s", left, right)
	}
}

func TestSourceStructuralNamesAreCanonicalAcrossOrderAndGOMAXPROCS(t *testing.T) {
	build := func(body string) []byte {
		return []byte("type Schema is interface " + body +
			" end interface Schema; architecture System() is schema : Schema; end architecture System;")
	}
	sources := [][]byte{
		build("type Text is String; Name : String; Count : Integer; requires External : String;"),
		build("Count : Integer; Name : String; type TEXT is String; requires External : String;"),
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 16; iteration++ {
		if iteration == 8 {
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
			t.Fatalf("source order/GOMAXPROCS changed structural model: %s != %s", digest, baseline)
		}
	}
}

func TestSourceObjectAndTypeNamesRejectUnsupportedOrConflictingForms(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{name: "type in requires", body: "requires type T;", want: "type-name declarations are not permitted"},
		{name: "anonymous bound", body: "type T <: interface end interface;", want: "rich type-expression form interface"},
		{name: "constructor object type", body: "Items : List(Integer);", want: `unknown closed predefined type constructor "List"`},
		{name: "unknown object type", body: "Item : Duration;", want: `type "Duration" is outside`},
		{name: "unknown exact type", body: "type Item is Duration;", want: `type "Duration" is outside`},
		{name: "type/object collision", body: "type Item; Item : String;", want: "type-name constituent"},
		{name: "type/function collision", body: "type Item; Item : function();", want: "type-name constituent"},
		{name: "duplicate type name", body: "type Item; type item is String;", want: "type-name constituent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type Schema is interface " + test.body +
				" end interface Schema; architecture System() is schema : Schema; end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func findObjectType(objects []InterfaceObjectDecl, name string) string {
	for _, object := range objects {
		if keyword(object.Name, name) {
			return object.Type
		}
	}
	return ""
}

func findTypeName(typeNames []InterfaceTypeNameDecl, name string) *InterfaceTypeNameDecl {
	for index := range typeNames {
		if keyword(typeNames[index].Name, name) {
			return &typeNames[index]
		}
	}
	return nil
}

func mustRootRapidePredefinedType(t *testing.T, name string) gorapide.RapideType {
	t.Helper()
	typ, err := gorapide.RapidePredefinedType(name)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

func mustRootRapideEventType(t *testing.T, parameters ...gorapide.RapideEventParameter) gorapide.RapideType {
	t.Helper()
	typ, err := gorapide.NewRapideEventType(parameters...)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

func mustRootRapideFunctionType(t *testing.T, parameters []gorapide.RapideFunctionParameter, result gorapide.RapideType) gorapide.RapideType {
	t.Helper()
	typ, err := gorapide.NewRapideFunctionType(parameters, result)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

func mustRootRapideInterfaceType(t *testing.T, members ...gorapide.RapideInterfaceMember) gorapide.RapideType {
	t.Helper()
	typ, err := gorapide.NewRapideInterfaceType(members...)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}
