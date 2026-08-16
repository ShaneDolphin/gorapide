package arch

import (
	"errors"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func moduleMembershipTarget(t *testing.T) (*InterfaceDecl, gorapide.RapideType, gorapide.RapideType) {
	t.Helper()
	stringType, err := gorapide.RapidePredefinedType("String")
	if err != nil {
		t.Fatal(err)
	}
	integerType, err := gorapide.RapidePredefinedType("Integer")
	if err != nil {
		t.Fatal(err)
	}
	structural, err := gorapide.NewRapideInterfaceType(
		gorapide.UnboundedProvidedRapideTypeName("Element"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return Interface("Schema").StructuralType(structural).Build(), stringType, integerType
}

func TestModuleMembershipIsCanonicalArchitectureContent(t *testing.T) {
	build := func(generator string, typ gorapide.RapideType) string {
		iface, _, _ := moduleMembershipTarget(t)
		denotation, err := gorapide.NewRapideTypeDenotation("Element", typ)
		if err != nil {
			t.Fatal(err)
		}
		component := NewComponent("schema", iface, nil)
		if err := component.SetModuleMembership(generator, denotation); err != nil {
			t.Fatal(err)
		}
		architecture := NewArchitecture("membership")
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	_, stringType, integerType := moduleMembershipTarget(t)
	stringDigest := build("Implementation", stringType)
	if got := build("IMPLEMENTATION", stringType); got != stringDigest {
		t.Fatalf("generator case changed membership identity: %s != %s", stringDigest, got)
	}
	if got := build("Implementation", integerType); got == stringDigest {
		t.Fatal("different exact type denotations produced the same architecture identity")
	}
	if got := build("Other_Implementation", stringType); got == stringDigest {
		t.Fatal("different module generators produced the same architecture identity")
	}
}

func TestModuleGeneratorArgumentsAreCanonicalMembershipContent(t *testing.T) {
	build := func(generator string, value any) string {
		component := NewComponent("worker", Interface("Empty").Build(), nil)
		if err := component.SetModuleMembershipWithArguments(
			generator,
			[]ModuleGeneratorArgument{ModuleArgument("Audit_Tag", "Integer", value)},
			nil,
			nil,
		); err != nil {
			t.Fatal(err)
		}
		architecture := NewArchitecture("membership")
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	baseline := build("Parameterized", int8(1))
	if got := build("PARAMETERIZED", int64(1)); got != baseline {
		t.Fatalf("generator case or Go integer width changed module-argument identity: %s != %s", baseline, got)
	}
	if got := build("Parameterized", int64(2)); got == baseline {
		t.Fatal("different unused module-generator actuals produced the same architecture identity")
	}
}

func TestModuleGeneratorArgumentsRejectDuplicateAndIllTypedValues(t *testing.T) {
	duplicate := NewComponent("duplicate", Interface("Empty").Build(), nil)
	err := duplicate.SetModuleMembershipWithArguments("M", []ModuleGeneratorArgument{
		ModuleArgument("Value", "Integer", int64(1)),
		ModuleArgument("value", "Integer", int64(1)),
	}, nil, nil)
	if !errors.Is(err, ErrInvalidModuleMembership) {
		t.Fatalf("duplicate module-generator argument error=%v", err)
	}
	wrong := NewComponent("wrong", Interface("Empty").Build(), nil)
	err = wrong.SetModuleMembershipWithArguments("M", []ModuleGeneratorArgument{
		ModuleArgument("Value", "Integer", true),
	}, nil, nil)
	if !errors.Is(err, ErrInvalidModuleMembership) {
		t.Fatalf("wrong module-generator argument type error=%v", err)
	}
}

func TestModuleMembershipRejectsMissingDuplicateAndRepeatedDeclaration(t *testing.T) {
	iface, stringType, _ := moduleMembershipTarget(t)
	component := NewComponent("schema", iface, nil)
	if err := component.SetModuleMembership("Impl"); !errors.Is(err, ErrInvalidModuleMembership) {
		t.Fatalf("missing denotation error=%v", err)
	}
	element, _ := gorapide.NewRapideTypeDenotation("Element", stringType)
	if err := component.SetModuleMembership("Impl", element, element); !errors.Is(err, ErrInvalidModuleMembership) {
		t.Fatalf("duplicate denotation error=%v", err)
	}
	if err := component.SetModuleMembership("Impl", element); err != nil {
		t.Fatal(err)
	}
	if err := component.SetModuleMembership("Impl", element); !errors.Is(err, ErrInvalidModuleMembership) {
		t.Fatalf("repeated membership declaration error=%v", err)
	}
	if err := (*Component)(nil).SetModuleMembership("Impl"); !errors.Is(err, ErrInvalidModuleMembership) {
		t.Fatalf("nil component membership error=%v", err)
	}
}

func TestModuleMembershipCanonicalizationIgnoresInputOrderAndGOMAXPROCS(t *testing.T) {
	stringType, _ := gorapide.RapidePredefinedType("String")
	structural, err := gorapide.NewRapideInterfaceType(
		gorapide.UnboundedProvidedRapideTypeName("A"),
		gorapide.UnboundedProvidedRapideTypeName("B"),
	)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := gorapide.NewRapideTypeDenotation("A", stringType)
	b, _ := gorapide.NewRapideTypeDenotation("B", stringType)
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 20; iteration++ {
		if iteration == 10 {
			runtime.GOMAXPROCS(4)
		}
		component := NewComponent("schema", Interface("Schema").StructuralType(structural).Build(), nil)
		denotations := []gorapide.RapideTypeDenotation{a, b}
		if iteration%2 != 0 {
			denotations = []gorapide.RapideTypeDenotation{b, a}
		}
		if err := component.SetModuleMembership("Impl", denotations...); err != nil {
			t.Fatal(err)
		}
		architecture := NewArchitecture("membership")
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if baseline == "" {
			baseline = digest
		} else if digest != baseline {
			t.Fatalf("order/GOMAXPROCS changed membership identity: %s != %s", baseline, digest)
		}
	}
}

func TestModuleObjectMembershipIsCanonicalArchitectureContent(t *testing.T) {
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	structural, err := gorapide.NewRapideInterfaceType(
		gorapide.ProvidedRapideMember("Limit", integerType),
	)
	if err != nil {
		t.Fatal(err)
	}
	build := func(value int64) string {
		object, err := gorapide.NewRapideObjectDenotation("Limit", integerType, value)
		if err != nil {
			t.Fatal(err)
		}
		component := NewComponent("schema", Interface("Schema").StructuralType(structural).Build(), nil)
		if err := component.SetModuleMembershipWithObjects("Impl", nil, []gorapide.RapideObjectDenotation{object}); err != nil {
			t.Fatal(err)
		}
		architecture := NewArchitecture("membership")
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	if build(1) == build(2) {
		t.Fatal("different concrete object values produced the same architecture identity")
	}
}

func TestModuleObjectMembershipRejectsMissingAndWrongType(t *testing.T) {
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	booleanType, _ := gorapide.RapidePredefinedType("Boolean")
	structural, _ := gorapide.NewRapideInterfaceType(gorapide.ProvidedRapideMember("Limit", integerType))
	component := NewComponent("schema", Interface("Schema").StructuralType(structural).Build(), nil)
	if err := component.SetModuleMembershipWithObjects("Impl", nil, nil); !errors.Is(err, ErrInvalidModuleMembership) {
		t.Fatalf("missing object error=%v", err)
	}
	wrong, _ := gorapide.NewRapideObjectDenotation("Limit", booleanType, true)
	if err := component.SetModuleMembershipWithObjects("Impl", nil, []gorapide.RapideObjectDenotation{wrong}); !errors.Is(err, ErrInvalidModuleMembership) {
		t.Fatalf("wrong object type error=%v", err)
	}
}

func TestModuleStructuralObjectMembershipIsCanonicalArchitectureContent(t *testing.T) {
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	iteratorType, err := gorapide.RapideIteratorType(integerType)
	if err != nil {
		t.Fatal(err)
	}
	structural, err := gorapide.NewRapideInterfaceType(
		gorapide.ProvidedRapideMember("Cursor", iteratorType),
	)
	if err != nil {
		t.Fatal(err)
	}
	build := func(occurrence string) string {
		module, err := gorapide.NewRapideModuleValue(gorapide.ModuleAllocationProvenance{
			Profile: CompatibilityProfile, Model: "structural-membership", Parent: "root",
			Generator: "FiniteIterator", Occurrence: occurrence,
		})
		if err != nil {
			t.Fatal(err)
		}
		cursor, err := gorapide.NewRapideObjectDenotation("Cursor", iteratorType, module)
		if err != nil {
			t.Fatal(err)
		}
		component := NewComponent("schema", Interface("Schema").StructuralType(structural).Build(), nil)
		if err := component.SetModuleMembershipWithObjects(
			"Impl", nil, []gorapide.RapideObjectDenotation{cursor},
		); err != nil {
			t.Fatal(err)
		}
		architecture := NewArchitecture("structural-membership")
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	if build("iterator-a") == build("iterator-b") {
		t.Fatal("structural module allocation identity did not affect canonical architecture")
	}
}

func TestModuleObjectMembershipCanonicalizationIgnoresOrderAndGOMAXPROCS(t *testing.T) {
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	structural, _ := gorapide.NewRapideInterfaceType(
		gorapide.ProvidedRapideMember("A", integerType),
		gorapide.ProvidedRapideMember("B", integerType),
	)
	a, _ := gorapide.NewRapideObjectDenotation("A", integerType, int64(1))
	b, _ := gorapide.NewRapideObjectDenotation("B", integerType, int64(2))
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 20; iteration++ {
		if iteration == 10 {
			runtime.GOMAXPROCS(4)
		}
		objects := []gorapide.RapideObjectDenotation{a, b}
		if iteration%2 != 0 {
			objects = []gorapide.RapideObjectDenotation{b, a}
		}
		component := NewComponent("schema", Interface("Schema").StructuralType(structural).Build(), nil)
		if err := component.SetModuleMembershipWithObjects("Impl", nil, objects); err != nil {
			t.Fatal(err)
		}
		architecture := NewArchitecture("membership")
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if baseline == "" {
			baseline = digest
		} else if digest != baseline {
			t.Fatalf("order/GOMAXPROCS changed object membership identity: %s != %s", baseline, digest)
		}
	}
}
