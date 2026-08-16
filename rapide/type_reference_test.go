package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestCompileSourceLocalTypeNameReferencesAttachStructuralDescriptor(t *testing.T) {
	source := []byte(`
type Schema is interface
  type Element;
  Item : Element;
  type Collection(type T <: Element; Size : Integer) <: Element;
private
  type Representation <: Element;
  Cache : Representation;
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
		t.Fatal("local type-name structural descriptor is absent")
	}
	element := mustRootRapideTypeNameReference(t, "Element")
	representation := mustRootRapideTypeNameReference(t, "Representation")
	integerType := mustRootRapidePredefinedType(t, "Integer")
	want := mustRootRapideInterfaceType(t,
		gorapide.UnboundedProvidedRapideTypeName("Element"),
		gorapide.ProvidedRapideMember("Item", element),
		gorapide.BoundedProvidedRapideTypeConstructor("Collection", element,
			gorapide.BoundedRapideTypeParameter("T", element),
			gorapide.RapideObjectParameter("Size", integerType)),
		gorapide.BoundedPrivateRapideTypeName("Representation", element),
		gorapide.PrivateRapideMember("Cache", representation),
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
		t.Fatalf("compiled local type-name references:\nwant %s\n got %s", wantBytes, gotBytes)
	}
	if _, err := model.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}
}

func TestDerivedLocalTypeNameReferencesMatchExplicitFlattening(t *testing.T) {
	derivedSource := []byte(`
type Base is interface
  type Element;
  Item : Element;
  type Collection(type T <: Element; Size : Integer) <: Element;
end interface Base;
type Derived is include Base replace (Element to Value, Collection to Sequence); interface end interface Derived;
architecture System() is schema : Derived; end architecture System;
`)
	flatSource := []byte(`
type Derived is interface
  type Value;
  Item : Value;
  type Sequence(type T <: Value; Size : Integer) <: Value;
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
		t.Fatalf("derived local type-name references differ from explicit flattening: %s != %s", left, right)
	}
}

func TestSourceLocalTypeNameReferencesAreCanonicalAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Schema is interface
  type Element;
  Item : Element;
  type Collection(type T <: Element; Size : Integer) <: Element;
end interface Schema;
architecture System() is schema : Schema; end architecture System;
`),
		[]byte(`
type Schema is interface
  type COLLECTION(type T <: ELEMENT; SIZE : integer) <: element;
  ITEM : element;
  type ELEMENT;
end interface Schema;
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
			t.Fatalf("order/case/GOMAXPROCS changed local type-name model: %s != %s", baseline, digest)
		}
	}
}

func TestSourceLocalTypeNameReferencesRemainExplicitOutsideStructuralSlots(t *testing.T) {
	tests := []struct {
		name, member string
	}{
		{name: "action parameter", member: "action in Accept(value : Element);"},
		{name: "function parameter", member: "provides Read : function(value : Element) return Integer;"},
		{name: "function result", member: "provides Read : function() return Element;"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type Schema is interface type Element; " + test.member +
				" end interface Schema; architecture System() is schema : Schema; end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), `type "Element" is outside the current deterministic Rapide type subset`) {
				t.Fatalf("got %v, want explicit execution-slot boundary", err)
			}
		})
	}
}

func TestSourceLocalTypeNameReferenceMustBeDeclaredInSameInterface(t *testing.T) {
	source := []byte(`
type Schema is interface Item : Missing; end interface Schema;
architecture System() is schema : Schema; end architecture System;
`)
	_, err := Compile(source, "System")
	if err == nil || !strings.Contains(err.Error(), `type "Missing" is outside the current deterministic Rapide`) {
		t.Fatalf("got %v, want unknown structural type error", err)
	}
}

func mustRootRapideTypeNameReference(t *testing.T, name string) gorapide.RapideType {
	t.Helper()
	typ, err := gorapide.NewRapideTypeNameReference(name)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}
