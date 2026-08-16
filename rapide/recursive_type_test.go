package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestSelfRecursiveSourceInterfaceHasNameFreeCanonicalGraph(t *testing.T) {
	model, err := Compile([]byte(`
type Node is interface Name : String; Child : Node; end interface Node;
architecture System() is node : Node; end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	component, ok := model.Component("node")
	if !ok {
		t.Fatal("compiled recursive component is absent")
	}
	typ, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("recursive structural descriptor is absent")
	}
	encoded, err := typ.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"format":"gorapide.rapide-type.v2","type":{"kind":"recursive_interface","members":[` +
		`{"region":"provides","name":"child","type":{"kind":"recursive_reference","depth":0}},` +
		`{"region":"provides","name":"name","type":{"kind":"predefined","name":"String"}}]}}`
	if string(encoded) != want {
		t.Fatalf("recursive source descriptor:\n%s\nwant:\n%s", encoded, want)
	}
	if strings.Contains(string(encoded), "Node") || strings.Contains(string(encoded), "node") {
		t.Fatalf("source declaration name leaked into recursive type identity: %s", encoded)
	}
	if _, err := model.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}
}

func TestRecursiveAliasInterfaceCycleErasesAliasIdentity(t *testing.T) {
	direct, err := Compile([]byte(`
type Node is interface Child : Node; end interface Node;
architecture System() is node : Node; end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	aliased, err := Compile([]byte(`
type Element is Node;
type Node is interface Child : Element; end interface Node;
architecture System() is node : Element; end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	left, err := direct.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	right, err := aliased.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("recursive alias introduced nominal model identity: %s != %s", left, right)
	}
}

func TestMutuallyRecursiveSourceInterfacesRoundTripCanonically(t *testing.T) {
	model, err := Compile([]byte(`
type Left is interface Right : Right; end interface Left;
type Right is interface Left : Left; end interface Right;
architecture System() is left : Left; end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	component, ok := model.Component("left")
	if !ok {
		t.Fatal("compiled mutually recursive component is absent")
	}
	typ, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("mutually recursive structural descriptor is absent")
	}
	encoded, err := typ.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"format":"gorapide.rapide-type.v2","type":{"kind":"recursive_interface","members":[` +
		`{"region":"provides","name":"right","type":{"kind":"interface","members":[` +
		`{"region":"provides","name":"left","type":{"kind":"recursive_reference","depth":0}}]}}]}}`
	if string(encoded) != want {
		t.Fatalf("mutually recursive source descriptor:\n%s\nwant:\n%s", encoded, want)
	}
	parsed, err := gorapide.ParseRapideType(encoded)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := parsed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(roundTrip, encoded) {
		t.Fatalf("mutually recursive source type round trip changed bytes:\n%s\n%s", encoded, roundTrip)
	}
	if _, err := model.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}
}

func TestRecursiveSourceTypesAreCanonicalAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Left is interface Label : String; Right : Right; end interface Left;
type Right is interface Left : Left; end interface Right;
architecture System() is left : Left; end architecture System;
`),
		[]byte(`
type RIGHT is interface LEFT : left; end interface RIGHT;
type Left is interface RIGHT : right; LABEL : string; end interface Left;
architecture System() is left : left; end architecture System;
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
			t.Fatalf("order/case/GOMAXPROCS changed recursive model: %s != %s", baseline, digest)
		}
	}
}

func TestRecursiveInterfaceObjectIsAcceptedInPublicActionTransport(t *testing.T) {
	model, err := Compile([]byte(`
type Node is interface Child : Node; end interface Node;
type API is interface action in Accept(value : Node); end interface API;
architecture System() is api : API; end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}
}

func TestRecursiveSourceDescriptorIgnoresUnrelatedDeclarationOrder(t *testing.T) {
	first, err := Compile([]byte(`
type Node is interface Child : Node; Name : String; end interface Node;
type Other is interface Value : Integer; end interface Other;
architecture System() is node : Node; end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile([]byte(`
type Other is interface Value : Integer; end interface Other;
type Node is interface Name : String; Child : Node; end interface Node;
architecture System() is node : Node; end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	left, _ := first.DeterministicModelDigest()
	right, _ := second.DeterministicModelDigest()
	if left != right {
		t.Fatalf("unrelated declaration order changed recursive model: %s != %s", left, right)
	}
}
