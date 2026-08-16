package rapide

import (
	"runtime"
	"strings"
	"testing"
)

func TestSupportedModuleMembershipRequiresEveryProvidedFunctionBody(t *testing.T) {
	source := []byte(`
type API is interface
  action out Ready(value : Integer);
  provides Compute : function(value : Integer) return Integer;
requires
  External : String;
end interface API;
module Impl() return API is
  Compute : function(value : Integer) return Integer is
  begin return value + 1; end function Compute;
initial Ready(2);
end module Impl;
architecture System() is api : API is Impl(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}
}

func TestParseModuleExactTypeDenotations(t *testing.T) {
	file, err := Parse([]byte(`
type Schema is interface type Element; end interface Schema;
module Impl() return Schema is
  type Element is String;
end module Impl;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Modules) != 1 || len(file.Modules[0].Types) != 1 ||
		file.Modules[0].Types[0].Name != "Element" || file.Modules[0].Types[0].Target != "String" {
		t.Fatalf("module type denotation AST=%#v", file.Modules)
	}
}

func TestModuleExactTypeDenotationSatisfiesAndIdentifiesMembership(t *testing.T) {
	build := func(denotation string) string {
		source := []byte(`
type Schema is interface type Element; end interface Schema;
module Impl() return Schema is type Element is ` + denotation + `; end module Impl;
architecture System() is schema : Schema is Impl(); end architecture System;
`)
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	stringDigest := build("String")
	if integerDigest := build("Integer"); integerDigest == stringDigest {
		t.Fatal("different module type denotations produced the same model identity")
	}
}

func TestModulePrivateTypeDenotationIsRetainedInMembershipIdentity(t *testing.T) {
	build := func(denotation string) string {
		source := []byte(`
type API is interface action out Ready(); end interface API;
module Impl() return API is
  type Helper is ` + denotation + `;
initial Ready();
end module Impl;
architecture System() is api : API is Impl(); end architecture System;
`)
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	if build("String") == build("Integer") {
		t.Fatal("private module type denotation was dropped from model identity")
	}
}

func TestModuleTypeDenotationUsesStructuralBoundAndExactRules(t *testing.T) {
	tests := []struct {
		name, specification, denotation string
		ok                              bool
	}{
		{name: "bounded subtype", specification: "<: Salary_Info", denotation: "Employee", ok: true},
		{name: "bounded failure", specification: "<: Employee", denotation: "Salary_Info"},
		{name: "exact equality", specification: "is Salary_Info", denotation: "Salary_Info", ok: true},
		{name: "exact width mismatch", specification: "is Salary_Info", denotation: "Employee"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Salary_Info is interface Salary : Float; end interface Salary_Info;
type Employee is interface Name : String; Salary : Float; end interface Employee;
type Schema is interface type Element ` + test.specification + `; end interface Schema;
module Impl() return Schema is type Element is ` + test.denotation + `; end module Impl;
architecture System() is schema : Schema is Impl(); end architecture System;
`)
			model, err := Compile(source, "System")
			if test.ok {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := model.DeterministicModelDigest(); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "does not satisfy target") {
				t.Fatalf("got %v, want incompatible module type denotation", err)
			}
		})
	}
}

func TestModuleTypeDenotationSubstitutesAnotherLocalTypeName(t *testing.T) {
	source := []byte(`
type Salary_Info is interface Salary : Float; end interface Salary_Info;
type Employee is interface Name : String; Salary : Float; end interface Employee;
type Schema is interface type Base; type Element <: Base; end interface Schema;
module Impl() return Schema is
  type Base is Salary_Info;
  type Element is Employee;
end module Impl;
architecture System() is schema : Schema is Impl(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}
}

func TestModuleTypeDenotationElaboratesClosedConstructorApplications(t *testing.T) {
	build := func(denotation string) string {
		t.Helper()
		source := []byte(`
type Schema is interface type Element; end interface Schema;
module Impl() return Schema is
  type Element is ` + denotation + `;
end module Impl;
architecture System() is schema : Schema is Impl(); end architecture System;
`)
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	baseline := build("Ref(Iterator(Integer))")
	if variant := build("rEf(iTeRaToR(iNtEgEr))"); variant != baseline {
		t.Fatalf("constructor or predefined spelling changed structural module identity: %s != %s", baseline, variant)
	}
	if iterator := build("Iterator(Integer)"); iterator == baseline {
		t.Fatal("distinct closed structural module denotations produced the same model identity")
	}
}

func TestModuleTypeDenotationConstructorApplicationResolvesLocalTypes(t *testing.T) {
	source := []byte(`
type Schema is interface type Element; end interface Schema;
module Impl() return Schema is
  type Base is Iterator(Integer);
  type Element is Ref(Base);
end module Impl;
architecture System() is schema : Schema is Impl(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}
}

func TestModuleTypeDenotationRejectsCycleThroughConstructorApplication(t *testing.T) {
	source := []byte(`
type Schema is interface type Element; end interface Schema;
module Impl() return Schema is
  type Element is Ref(Element);
end module Impl;
architecture System() is schema : Schema is Impl(); end architecture System;
`)
	_, err := Compile(source, "System")
	want := "recursive module type denotation Element -> Element"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want error containing %q", err, want)
	}
}

func TestModuleTypeDenotationRejectsMissingDuplicateCyclesAndUnknownTargets(t *testing.T) {
	tests := []struct {
		name, interfaceBody, moduleBody, want string
	}{
		{name: "missing", interfaceBody: "type Element;", want: `does not supply type-name "element"`},
		{name: "duplicate", interfaceBody: "type Element;", moduleBody: "type Element is String; type element is Integer;", want: `duplicate module type denotation "element"`},
		{name: "cycle", interfaceBody: "type Element;", moduleBody: "type Element is Other; type Other is Element;", want: `recursive module type denotation Element -> Other -> Element`},
		{name: "unknown", interfaceBody: "type Element;", moduleBody: "type Element is Missing;", want: `type "Missing" is outside`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type Schema is interface " + test.interfaceBody +
				" end interface Schema; module Impl() return Schema is " + test.moduleBody +
				" end module Impl; architecture System() is schema : Schema is Impl(); end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestModuleTypeDenotationsAreCanonicalAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Schema is interface type A; type B; end interface Schema;
module Impl() return Schema is type A is String; type B is Integer; end module Impl;
architecture System() is schema : Schema is Impl(); end architecture System;
`),
		[]byte(`
type Schema is interface type B; type A; end interface Schema;
module IMPL() return schema is type b is integer; type a is string; end module impl;
architecture System() is schema : schema is impl(); end architecture System;
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
			t.Fatalf("order/case/GOMAXPROCS changed module membership: %s != %s", baseline, digest)
		}
	}
}

func TestModuleMembershipRejectsUnrepresentedConcreteInterfaceDeclarations(t *testing.T) {
	source := []byte(`
type Schema is interface
  type Element;
  type Collection(type T);
private
  Hidden : function();
end interface Schema;
module Impl() return Schema is type Element is String; end module Impl;
architecture System() is schema : Schema is Impl(); end architecture System;
`)
	_, err := Compile(source, "System")
	want := "module \"Impl\" membership in interface \"Schema\" requires unsupported concrete declarations: " +
		"private function hidden, provides type-constructor collection"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want membership error containing %q", err, want)
	}
}

func TestModuleMembershipDiagnosticOrderIsCanonical(t *testing.T) {
	build := func(declarations string) string {
		source := []byte("type Schema is interface " + declarations +
			" end interface Schema; module Impl() return Schema is end module Impl;" +
			" architecture System() is schema : Schema is Impl(); end architecture System;")
		_, err := Compile(source, "System")
		if err == nil {
			t.Fatal("unsupported membership unexpectedly compiled")
		}
		return err.Error()
	}
	first := build("Item : String; Other : Float; private Cache : Integer;")
	second := build("Other : Float; Item : String; private Cache : Integer;")
	if first != second {
		t.Fatalf("declaration order changed membership diagnostic:\n%s\n%s", first, second)
	}
}

func TestModuleMembershipGateIgnoresHostProcessorCount(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	source := []byte(`
type API is interface action out Ready(); end interface API;
module Impl() return API is initial Ready(); end module Impl;
architecture System() is api : API is Impl(); end architecture System;
`)
	var baseline string
	for iteration := 0; iteration < 20; iteration++ {
		if iteration == 10 {
			runtime.GOMAXPROCS(4)
		}
		model, err := Compile(source, "System")
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
			t.Fatalf("GOMAXPROCS changed supported membership model: %s != %s", baseline, digest)
		}
	}
}
