package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestParseAndCompileInterfaceModuleGeneratorNames(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Done();
end interface Worker;
type Factory is interface
  Build, Spare : module () return Worker;
  Configured : module (Initial_Value : Integer; Label : String) return Worker;
  Generic : module (type Item; type Order <: Discrete(Integer)) return Worker;
requires
  External : module () return Worker;
private
  Hidden : module () return Worker;
end interface Factory;
architecture System() is factory : Factory; end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Interfaces) != 2 || len(file.Interfaces[1].ModuleGenerators) != 6 {
		t.Fatalf("module-generator name AST=%#v", file.Interfaces)
	}
	generators := file.Interfaces[1].ModuleGenerators
	if generators[0].Name != "Build" || generators[0].Region != InterfaceNameProvides ||
		generators[1].Name != "Spare" || generators[1].Region != InterfaceNameProvides ||
		generators[2].Name != "Configured" || generators[2].Region != InterfaceNameProvides ||
		generators[3].Name != "Generic" || generators[3].Region != InterfaceNameProvides ||
		generators[4].Name != "External" || generators[4].Region != InterfaceNameRequires ||
		generators[5].Name != "Hidden" || generators[5].Region != InterfaceNamePrivate {
		t.Fatalf("module-generator name regions=%#v", generators)
	}
	for index, generator := range generators {
		wantParameters := 0
		if index == 2 || index == 3 {
			wantParameters = 2
		}
		if len(generator.Parameters) != wantParameters || generator.ReturnType != "Worker" ||
			generator.ReturnTypeExpression.Kind != TypeExpressionName {
			t.Fatalf("module-generator signature=%#v", generator)
		}
	}
	if generators[2].Parameters[0].Name != "Initial_Value" || generators[2].Parameters[0].Type != "Integer" ||
		generators[2].Parameters[1].Name != "Label" || generators[2].Parameters[1].Type != "String" {
		t.Fatalf("configured module-generator parameters=%#v", generators[2].Parameters)
	}
	if generators[3].Parameters[0].Kind != InterfaceFormalTypeParameter || generators[3].Parameters[0].Type != "" ||
		generators[3].Parameters[1].Kind != InterfaceFormalTypeParameter ||
		generators[3].Parameters[1].Type != "Discrete(Integer)" {
		t.Fatalf("generic module-generator parameters=%#v", generators[3].Parameters)
	}

	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	component, ok := model.Component("factory")
	if !ok {
		t.Fatal("compiled factory component is absent")
	}
	got, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("factory structural descriptor is absent")
	}
	eventType := mustRootRapideEventType(t)
	worker := mustRootRapideInterfaceType(t, gorapide.OutputRapideAction("Done", eventType))
	signature := mustRootRapideFunctionType(t, nil, worker)
	configuredSignature := mustRootRapideFunctionType(t, []gorapide.RapideFunctionParameter{
		gorapide.RapideObjectParameter("Initial_Value", mustRootRapidePredefinedType(t, "Integer")),
		gorapide.RapideObjectParameter("Label", mustRootRapidePredefinedType(t, "String")),
	}, worker)
	discreteInteger, err := gorapide.ApplyRapideTypeConstructor(
		"Discrete", mustRootRapidePredefinedType(t, "Integer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	genericSignature := mustRootRapideFunctionType(t, []gorapide.RapideFunctionParameter{
		gorapide.RapideTypeParameter("Item"),
		gorapide.BoundedRapideTypeParameter("Order", discreteInteger),
	}, worker)
	want := mustRootRapideInterfaceType(t,
		gorapide.ProvidedRapideModuleGenerator("Build", signature),
		gorapide.ProvidedRapideModuleGenerator("Spare", signature),
		gorapide.ProvidedRapideModuleGenerator("Configured", configuredSignature),
		gorapide.ProvidedRapideModuleGenerator("Generic", genericSignature),
		gorapide.RequiredRapideModuleGenerator("External", signature),
		gorapide.PrivateRapideModuleGenerator("Hidden", signature),
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
		t.Fatalf("source module-generator structural type:\nwant %s\n got %s", wantBytes, gotBytes)
	}
}

func TestInterfaceModuleGeneratorDerivationRewritesNestedReturnType(t *testing.T) {
	derivedSource := []byte(`
type Product is interface Name : String; end interface Product;
type Base is interface
  type Item is Product;
  Build : module (Seed : Item) return Iterator(Item);
end interface Base;
type Derived is
  include Base replace (Item to Element, Build to Create);
interface end interface Derived;
`)
	declarations := mustParseInterfaceDeclarations(t, string(derivedSource))
	normalized, err := normalizeInterfaceDeclarations(declarations)
	if err != nil {
		t.Fatal(err)
	}
	derived := normalized["derived"]
	if len(derived.ModuleGenerators) != 1 || len(derived.TypeNames) != 1 {
		t.Fatalf("derived names=%#v", derived)
	}
	generator := derived.ModuleGenerators[0]
	if generator.Name != "Create" || generator.ReturnType != "Iterator(Element)" ||
		len(generator.Parameters) != 1 || generator.Parameters[0].Type != "Element" ||
		generator.ReturnTypeExpression.Kind != TypeExpressionApplication ||
		len(generator.ReturnTypeExpression.Arguments) != 1 ||
		generator.ReturnTypeExpression.Arguments[0].Name != "Element" {
		t.Fatalf("derived module generator=%#v", generator)
	}

	compiledDerivedSource := []byte(`
type Worker is interface Name : String; end interface Worker;
type Base is interface
  type Item is Integer;
  Build : module (Initial : Item) return Worker;
end interface Base;
type Derived is include Base replace (Item to Value, Build to Create); interface end interface Derived;
architecture System() is factory : Derived; end architecture System;
`)
	flatSource := []byte(`
type Worker is interface Name : String; end interface Worker;
type Derived is interface
  type Value is Integer;
  Create : module (Initial : Value) return Worker;
end interface Derived;
architecture System() is factory : Derived; end architecture System;
`)
	derivedModel, err := Compile(compiledDerivedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	flatModel, err := Compile(flatSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	derivedDigest, err := derivedModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	flatDigest, err := flatModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if derivedDigest != flatDigest {
		t.Fatalf("derived module-generator model %s != flat model %s", derivedDigest, flatDigest)
	}
}

func TestSourceModuleGeneratorNamesAreCanonicalAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Worker is interface end interface Worker;
type Factory is interface
  Alpha : module (Initial : Integer) return Worker;
  Beta : module () return Worker;
end interface Factory;
architecture System() is factory : Factory; end architecture System;
`),
		[]byte(`
type Worker is interface end interface Worker;
type Factory is interface
  beta : module () return worker;
  ALPHA : module (INITIAL : integer) return WORKER;
end interface Factory;
architecture System() is factory : Factory; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 12; iteration++ {
		if iteration == 6 {
			runtime.GOMAXPROCS(4)
		} else if iteration == 0 {
			runtime.GOMAXPROCS(1)
		}
		model, err := Compile(sources[iteration%len(sources)], "system")
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
			t.Fatalf("source order/case/GOMAXPROCS changed digest: %s != %s", baseline, digest)
		}
	}
}

func TestSourceModuleGeneratorNamesRejectUnsupportedOrUnsatisfiedForms(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "defaulted type parameter",
			source: `type Worker is interface end interface Worker;
type Factory is interface Build : module (type Item is Integer) return Worker; end interface Factory;`,
			want: "defaulted interface module-generator type parameters are outside the current source subset",
		},
		{
			name: "defaulted parameters",
			source: `type Worker is interface end interface Worker;
type Factory is interface Build : module (size : Integer := 1) return Worker; end interface Factory;`,
			want: "defaulted interface module-generator parameters are outside the current source subset",
		},
		{
			name: "duplicate parameters",
			source: `type Worker is interface end interface Worker;
type Factory is interface Build : module (size : Integer; SIZE : Integer) return Worker; end interface Factory;
architecture System() is factory : Factory; end architecture System;`,
			want: `duplicate formal parameter "SIZE" on module-generator name "Build"`,
		},
		{
			name: "dependent object parameter",
			source: `type Worker is interface end interface Worker;
type Factory is interface Build : module (type Item; value : Item) return Worker; end interface Factory;
architecture System() is factory : Factory; end architecture System;`,
			want: `formal object-parameter type "Item" on module-generator name "Build" requires symbolic parameter references`,
		},
		{
			name: "dependent return",
			source: `type Factory is interface Build : module (type Item) return Iterator(Item); end interface Factory;
architecture System() is factory : Factory; end architecture System;`,
			want: `module-generator return expression "Iterator(Item)" requires symbolic parameter substitution`,
		},
		{
			name:   "missing return",
			source: `type Factory is interface Build : module (); end interface Factory;`,
			want:   "interface module-generator name requires 'return Interface_Expression'",
		},
		{
			name: "noninterface result",
			source: `type Factory is interface Build : module () return Integer; end interface Factory;
architecture System() is factory : Factory; end architecture System;`,
			want: "does not have a function signature returning an interface",
		},
		{
			name: "concrete membership",
			source: `type Worker is interface end interface Worker;
type Factory is interface Build : module () return Worker; end interface Factory;
module Impl() return Factory is end module Impl;
architecture System() is factory : Factory is Impl(); end architecture System;`,
			want: "requires unsupported concrete declarations: provides module-generator build",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}
