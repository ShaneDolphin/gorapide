package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestGroupedFormalObjectParametersExpandAcrossSupportedConstructs(t *testing.T) {
	file, err := Parse([]byte(`
type Worker is interface
  action out Pair(Left, Right : Integer);
  provides Sum : function(Left, Right : Integer) return Integer;
end interface Worker;
type Factory is interface
  Build : module (Left, Right : Integer) return Worker;
end interface Factory;
module Pairer(Left, Right : Integer is 2) return Worker is
  Sum : function(Left, Right : Integer) return Integer is
  begin
    return Left + Right;
  end function Sum;
initial
  Pair(Left, Right);
end module Pairer;
architecture System(Left, Right : Integer is 2) is
  worker : Worker is Pairer(Left, Right);
end architecture System;
`))
	if err != nil {
		t.Fatal(err)
	}
	worker := file.Interfaces[0]
	if len(worker.Actions) != 1 || len(worker.Actions[0].Parameters) != 2 ||
		len(worker.Functions) != 1 || len(worker.Functions[0].Parameters) != 2 {
		t.Fatalf("grouped action/function formals=%#v/%#v", worker.Actions, worker.Functions)
	}
	factory := file.Interfaces[1]
	if len(factory.ModuleGenerators) != 1 || len(factory.ModuleGenerators[0].Parameters) != 2 {
		t.Fatalf("grouped interface module-generator formals=%#v", factory.ModuleGenerators)
	}
	module := file.Modules[0]
	if len(module.Parameters) != 2 || module.Parameters[0].Default == nil || module.Parameters[1].Default == nil ||
		len(module.Functions) != 1 || len(module.Functions[0].Parameters) != 2 {
		t.Fatalf("grouped concrete module/function formals=%#v/%#v", module.Parameters, module.Functions)
	}
	architecture := file.Architectures[0]
	if len(architecture.Parameters) != 2 || architecture.Parameters[0].Default == nil || architecture.Parameters[1].Default == nil {
		t.Fatalf("grouped architecture formals=%#v", architecture.Parameters)
	}
	for _, parameters := range [][]ParameterDecl{
		worker.Actions[0].Parameters,
		worker.Functions[0].Parameters,
		module.Parameters,
		module.Functions[0].Parameters,
		architecture.Parameters,
	} {
		if parameters[0].Name != "Left" || parameters[1].Name != "Right" ||
			parameters[0].Type != "Integer" || parameters[1].Type != "Integer" {
			t.Fatalf("identifier-list expansion lost order/type: %#v", parameters)
		}
	}
}

func TestGroupedFormalObjectParametersAreCanonicalWithExplicitExpansion(t *testing.T) {
	build := func(grouped bool) *arch.Architecture {
		interfaceParameters := "Left : Integer; Right : Integer"
		moduleParameters := "Base : Integer is 2; Offset : Integer is 2"
		architectureParameters := "Seed : Integer is 2; Delta : Integer is 2"
		if grouped {
			interfaceParameters = "Left, Right : Integer"
			moduleParameters = "Base, Offset : Integer is 2"
			architectureParameters = "Seed, Delta : Integer is 2"
		}
		source := []byte(`
type Worker is interface
  action out Pair(` + interfaceParameters + `);
  provides Sum : function(` + interfaceParameters + `) return Integer;
end interface Worker;
module Pairer(` + moduleParameters + `) return Worker is
  total : var Integer := 0;
  Sum : function(` + interfaceParameters + `) return Integer is
  begin
    return Left + Right;
  end function Sum;
initial
  total := Sum(Base, Offset);
  Pair(Base, $total);
end module Pairer;
architecture System(` + architectureParameters + `) is
  worker : Worker is Pairer(Seed, Delta);
end architecture System;
`)
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		return model
	}
	grouped := build(true)
	expanded := build(false)
	groupedDigest, err := grouped.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	expandedDigest, err := expanded.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if groupedDigest != expandedDigest {
		t.Fatalf("grouped declaration changed canonical model: %s != %s", groupedDigest, expandedDigest)
	}
	journal := arch.NewExecutionJournal(groupedDigest, 20)
	previous := runtime.GOMAXPROCS(1)
	groupedResult, groupedErr := grouped.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(8)
	expandedResult, expandedErr := expanded.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if groupedErr != nil {
		t.Fatal(groupedErr)
	}
	if expandedErr != nil {
		t.Fatal(expandedErr)
	}
	pair := groupedResult.Poset.ByName("Pair")
	if len(pair) != 1 || pair[0].ParamInt("Left") != 2 || pair[0].ParamInt("Right") != 4 {
		t.Fatalf("grouped parameter execution=%#v", pair)
	}
	groupedBytes, err := groupedResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	expandedBytes, err := expandedResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(groupedBytes, expandedBytes) {
		t.Fatal("grouped and explicitly expanded formals changed artifact bytes")
	}
	artifactDigest, err := groupedResult.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := grouped.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(groupedBytes, replayedBytes) {
		t.Fatal("grouped formal replay changed canonical artifact bytes")
	}
}

func TestGroupedFormalObjectParametersRetainDuplicateDiagnostics(t *testing.T) {
	_, err := Compile([]byte(`
type Worker is interface action out Pair(Value, value : Integer); end interface Worker;
architecture System() is worker : Worker; end architecture System;
`), "System")
	if err == nil || !strings.Contains(err.Error(), "duplicate parameter") {
		t.Fatalf("case-insensitive duplicate grouped formals diagnostic=%v", err)
	}
}
