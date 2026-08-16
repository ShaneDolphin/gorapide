package rapide

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceInterfaceExceptionRaisesAndHandlesByExactDeclaration(t *testing.T) {
	source := []byte(`
type Worker is interface
  exception Failure(code : Integer);
  action in Trigger(code : Integer);
  action out Recovered(code : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(code : Integer); end interface Stimulus;

module WorkerModule() return Worker is
serial when (?Code : Integer) Trigger(?Code) do
  raise Failure(code is ?Code);
end when;
handler
  is Failure(code is ?Code) => Recovered(?Code);
end module WorkerModule;

architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect (?Code : Integer) stimulus.Trigger(?Code) => worker.Trigger(?Code);
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 40, arch.InputEvent{
		Key: "trigger", Source: "stimulus", Action: "Trigger", Params: map[string]any{"code": 9},
	})
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	failures := sourceNamedEvents(result.Poset, "worker", "Failure")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	if len(failures) != 1 || len(recovered) != 1 {
		t.Fatalf("Failure/Recovered=%d/%d", len(failures), len(recovered))
	}
	if code, _ := recovered[0].Param("code"); code != int64(9) {
		t.Fatalf("Recovered code=%#v", code)
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], failures[0])

	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed interface-exception execution")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("interface-exception replay changed canonical bytes")
	}
}

func TestSourceDerivedInterfaceExceptionReplaceGetsDerivedDeclarationIdentity(t *testing.T) {
	source := []byte(`
type Base is interface
  exception Failure(code : Integer);
  action in Trigger(code : Integer);
  action out Recovered(code : Integer);
end interface Base;
type Worker is include Base replace (Failure to Renamed); interface end interface Worker;
type Stimulus is interface action out Trigger(code : Integer); end interface Stimulus;

module WorkerModule() return Worker is
serial when (?Code : Integer) Trigger(?Code) do raise Renamed(?Code); end when;
handler is Renamed(?Code) => Recovered(?Code);
end module WorkerModule;

architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect (?Code : Integer) stimulus.Trigger(?Code) => worker.Trigger(?Code);
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 40, arch.InputEvent{
		Key: "trigger", Source: "stimulus", Action: "Trigger", Params: map[string]any{"code": 4},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sourceNamedEvents(result.Poset, "worker", "Renamed")); got != 1 {
		t.Fatalf("Renamed exceptions=%d", got)
	}
	if got := len(sourceNamedEvents(result.Poset, "worker", "Recovered")); got != 1 {
		t.Fatalf("Recovered events=%d", got)
	}
}

func TestSourceSelfSelectedInterfaceExceptionRaisesAndHandlesByExactDeclaration(t *testing.T) {
	source := []byte(`
type Worker is interface
  exception Failure(code : Integer);
  action out Trigger();
  action out Recovered(code : Integer);
  action out LocalRecovered(code : Integer);
end interface Worker;

module WorkerModule() return Worker is
exception Failure(code : Integer);
serial when Trigger() do
  raise Self.Failure(code is 9);
end when;
handler
  is Self.Failure(code is ?Code) => Recovered(?Code);
  is Failure(code is ?Code) => LocalRecovered(?Code);
end module WorkerModule;

architecture System() is worker : Worker is WorkerModule(); end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	process := file.Modules[0].Processes[0]
	if len(process.Statements) != 1 || process.Statements[0].Call.Name != "Self.Failure" {
		t.Fatalf("selected raise AST=%#v", process.Statements)
	}
	choice := file.Modules[0].Handler.Choices[0]
	if choice.Pattern.Event.Component != "Self" || choice.Pattern.Event.Name != "Failure" {
		t.Fatalf("selected handler AST=%#v", choice.Pattern.Event)
	}

	model, err := CompileFile(file, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 40, arch.InputEvent{
		Key: "trigger", Source: "worker", Action: "Trigger",
	})
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	failures := sourceNamedEvents(result.Poset, "worker", "Failure")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	localRecovered := sourceNamedEvents(result.Poset, "worker", "LocalRecovered")
	if len(failures) != 1 || len(recovered) != 1 || len(localRecovered) != 0 {
		t.Fatalf("Self.Failure/Recovered/LocalRecovered=%d/%d/%d",
			len(failures), len(recovered), len(localRecovered))
	}
	if code, _ := recovered[0].Param("code"); code != int64(9) {
		t.Fatalf("Recovered code=%#v", code)
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], failures[0])

	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed Self-selected exception execution")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("Self-selected exception replay changed canonical bytes")
	}
}

func TestSourceFunctionLocalModuleSelectsExactInterfaceException(t *testing.T) {
	source := []byte(`
type Provider is interface
  exception Failure(code : Integer);
  action out InterfaceRecovered(code : Integer);
  action out LocalRecovered(code : Integer);
  provides Test : function();
end interface Provider;
type Driver is interface
  action in Trigger();
  requires Test : function();
end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module ProviderModule() return Provider is
  exception Failure(code : Integer);
  Test : function() is
    Child : Provider is New();
  begin
    raise Child.Failure(code is 5);
  handler
    is Child.Failure(code is ?Code) => InterfaceRecovered(?Code);
    is Failure(code is ?Code) => LocalRecovered(?Code);
  end function Test;
end module ProviderModule;
module DriverModule() return Driver is
serial when Trigger() do Test(); end when;
end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  provider : Provider is ProviderModule();
  driver : Driver is DriverModule();
connect
  stimulus.Trigger to driver.Trigger;
  driver.Test to provider.Test;
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 50, arch.InputEvent{
		Key: "trigger", Source: "stimulus", Action: "Trigger",
	})
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	failures := sourceNamedEvents(result.Poset, "provider", "Failure")
	interfaceRecovered := sourceNamedEvents(result.Poset, "provider", "InterfaceRecovered")
	localRecovered := sourceNamedEvents(result.Poset, "provider", "LocalRecovered")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "driver", "Test'Return"))
	if len(failures) != 1 || len(interfaceRecovered) != 1 || len(localRecovered) != 0 || len(returns) != 1 {
		t.Fatalf("Child.Failure/InterfaceRecovered/LocalRecovered/Test'Return=%d/%d/%d/%d",
			len(failures), len(interfaceRecovered), len(localRecovered), len(returns))
	}
	if code, _ := interfaceRecovered[0].Param("code"); code != int64(5) {
		t.Fatalf("InterfaceRecovered code=%#v", code)
	}
	assertOnlyDirectCause(t, result.Poset, interfaceRecovered[0], failures[0])
	if !result.Poset.IsCausallyBefore(interfaceRecovered[0].ID, returns[0].ID) {
		t.Fatal("function-local selected exception did not recover before Return")
	}

	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed function-local selected exception execution")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("function-local selected exception replay changed canonical bytes")
	}
}

func TestSourceConcreteModuleHandlesServiceRewrittenExceptionDeterministically(t *testing.T) {
	source := []byte(`
type Faults is interface
  exception Failure(code : Integer);
end interface Faults;
type Wrapped is interface
  service Inner : Faults;
end interface Wrapped;
type Worker is interface
  action in Trigger();
  action out Recovered(code : Integer);
  service API : Wrapped;
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module WorkerModule() return Worker is
serial when Trigger() do
  raise API.Inner.Failure(code is 9);
end when;
handler
  is API.Inner.Failure(code is ?Code) => Recovered(?Code);
end module WorkerModule;

architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	module := file.Modules[0]
	if got := module.Processes[0].Statements[0].Call.Name; got != "API.Inner.Failure" {
		t.Fatalf("parsed raise name=%q", got)
	}
	if got := module.Handler.Choices[0].Pattern.Event; got.Component != "API" || got.Name != "Inner.Failure" {
		t.Fatalf("parsed handler event=%#v", got)
	}

	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 40, arch.InputEvent{
		Key: "trigger", Source: "stimulus", Action: "Trigger",
	})
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	failures := sourceNamedEvents(result.Poset, "worker", "api.inner.failure")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	if len(failures) != 1 || len(recovered) != 1 {
		t.Fatalf("API.Inner.Failure/Recovered=%d/%d", len(failures), len(recovered))
	}
	if code, _ := recovered[0].Param("code"); code != int64(9) {
		t.Fatalf("Recovered code=%#v", code)
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], failures[0])

	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed service-rewritten exception execution")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("service-rewritten exception replay changed canonical bytes")
	}
}

func TestSourceModuleScopeNameSelectsExactLocalException(t *testing.T) {
	source := []byte(`
type Worker is interface
  exception Failure(code : Integer);
  action in Trigger();
  action out ModuleRecovered(code : Integer);
  action out InterfaceRecovered(code : Integer);
  action out Wrong(code : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module WorkerModule() return Worker is
exception Failure(code : Integer);
serial when Trigger() do
  do
    raise WorkerModule::Failure(code is 4);
  handler
    is Self.Failure(code is ?Code) => Wrong(?Code);
    is WorkerModule::Failure(code is ?Code) => ModuleRecovered(?Code);
  end do;
  do
    raise Self.Failure(code is 8);
  handler
    is WorkerModule::Failure(code is ?Code) => Wrong(?Code);
    is Self.Failure(code is ?Code) => InterfaceRecovered(?Code);
  end do;
end when;
end module WorkerModule;

architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	process := file.Modules[0].Processes[0]
	if got := process.Statements[0].Body[0].Call.Name; got != "WorkerModule::Failure" {
		t.Fatalf("module-scoped raise AST=%q", got)
	}
	if got := process.Statements[0].Handler.Choices[1].Pattern.Event.Name; got != "WorkerModule::Failure" {
		t.Fatalf("module-scoped handler AST=%q", got)
	}

	model, err := CompileFile(file, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 60, arch.InputEvent{
		Key: "trigger", Source: "stimulus", Action: "Trigger",
	})
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	failures := sourceNamedEvents(result.Poset, "worker", "Failure")
	moduleRecovered := sourceNamedEvents(result.Poset, "worker", "ModuleRecovered")
	interfaceRecovered := sourceNamedEvents(result.Poset, "worker", "InterfaceRecovered")
	wrong := sourceNamedEvents(result.Poset, "worker", "Wrong")
	if len(failures) != 2 || len(moduleRecovered) != 1 || len(interfaceRecovered) != 1 || len(wrong) != 0 {
		t.Fatalf("Failure/ModuleRecovered/InterfaceRecovered/Wrong=%d/%d/%d/%d",
			len(failures), len(moduleRecovered), len(interfaceRecovered), len(wrong))
	}
	if code, _ := moduleRecovered[0].Param("code"); code != int64(4) {
		t.Fatalf("ModuleRecovered code=%#v", code)
	}
	if code, _ := interfaceRecovered[0].Param("code"); code != int64(8) {
		t.Fatalf("InterfaceRecovered code=%#v", code)
	}

	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed module-scope exception selection")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("module-scope exception replay changed canonical bytes")
	}
}

func TestSourceModuleScopeNameRejectsForeignOrNonOwnedException(t *testing.T) {
	tests := []struct {
		name       string
		local      string
		reference  string
		wantQuoted string
	}{
		{name: "interface constituent is not module-owned", reference: "WorkerModule::Failure", wantQuoted: "WorkerModule::Failure"},
		{name: "foreign scope", local: "exception Failure(code : Integer);", reference: "Other::Failure", wantQuoted: "Other::Failure"},
		{name: "unsupported nested region", local: "exception Failure(code : Integer);", reference: "WorkerModule::Inner::Failure", wantQuoted: "WorkerModule::Inner::Failure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(fmt.Sprintf(`
type Worker is interface
  exception Failure(code : Integer);
  action in Trigger();
end interface Worker;
module WorkerModule() return Worker is
%s
serial when Trigger() do raise %s(code is 1); end when;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`, test.local, test.reference))
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), `raise names undeclared exception "`+test.wantQuoted+`"`) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSourceLabeledDeclareDoScopesSelectExactNestedExceptions(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out InnerRecovered(code : Integer);
  action out OuterRecovered(code : Integer);
  action out ModuleRecovered(code : Integer);
  action out Wrong(code : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module WorkerModule() return Worker is
exception Failure(code : Integer);
serial Rule: when Trigger() do
  Outer: declare
    exception Failure(code : Integer);
  do
    Inner: declare
      exception Failure(code : Integer);
    do
      raise Inner::Failure(code is 1);
    handler
      is WorkerModule::Failure(code is ?Code) => Wrong(?Code);
      is Outer::Failure(code is ?Code) => Wrong(?Code);
      is WorkerModule::Rule::Outer::Inner::Failure(code is ?Code) => InnerRecovered(?Code);
    end do Inner;
    raise Outer::Failure(code is 2);
  handler
    is WorkerModule::Failure(code is ?Code) => Wrong(?Code);
    is Outer::Failure(code is ?Code) => OuterRecovered(?Code);
  end do Outer;
  raise WorkerModule::Failure(code is 3);
handler
  is WorkerModule::Failure(code is ?Code) => ModuleRecovered(?Code);
end when Rule;
end module WorkerModule;

architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	outer := file.Modules[0].Processes[0].Statements[0]
	inner := outer.Body[0]
	if outer.Label != "Outer" || len(outer.Exceptions) != 1 || outer.Exceptions[0].Name != "Failure" ||
		inner.Label != "Inner" || len(inner.Exceptions) != 1 || inner.Exceptions[0].Name != "Failure" {
		t.Fatalf("declare-do AST outer=%#v inner=%#v", outer, inner)
	}
	if got := inner.Body[0].Call.Name; got != "Inner::Failure" {
		t.Fatalf("nested scope raise AST=%q", got)
	}
	if got := inner.Handler.Choices[2].Pattern.Event.Name; got != "WorkerModule::Rule::Outer::Inner::Failure" {
		t.Fatalf("full nested handler scope AST=%q", got)
	}

	model, err := CompileFile(file, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 80, arch.InputEvent{
		Key: "trigger", Source: "stimulus", Action: "Trigger",
	})
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	failures := sourceNamedEvents(result.Poset, "worker", "Failure")
	innerRecovered := sourceNamedEvents(result.Poset, "worker", "InnerRecovered")
	outerRecovered := sourceNamedEvents(result.Poset, "worker", "OuterRecovered")
	moduleRecovered := sourceNamedEvents(result.Poset, "worker", "ModuleRecovered")
	wrong := sourceNamedEvents(result.Poset, "worker", "Wrong")
	if len(failures) != 3 || len(innerRecovered) != 1 || len(outerRecovered) != 1 ||
		len(moduleRecovered) != 1 || len(wrong) != 0 {
		t.Fatalf("Failure/Inner/Outer/Module/Wrong=%d/%d/%d/%d/%d",
			len(failures), len(innerRecovered), len(outerRecovered), len(moduleRecovered), len(wrong))
	}
	if code, _ := innerRecovered[0].Param("code"); code != int64(1) {
		t.Fatalf("InnerRecovered code=%#v", code)
	}
	if code, _ := outerRecovered[0].Param("code"); code != int64(2) {
		t.Fatalf("OuterRecovered code=%#v", code)
	}
	if code, _ := moduleRecovered[0].Param("code"); code != int64(3) {
		t.Fatalf("ModuleRecovered code=%#v", code)
	}

	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed nested scope-name exception selection")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("nested scope-name exception replay changed canonical bytes")
	}
}

func TestSourceLabeledDeclareDoExceptionScopeDoesNotLeak(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); end interface Worker;
module WorkerModule() return Worker is
serial when Trigger() do
  Inner: declare exception Failure(code : Integer); do
    null;
  end do Inner;
  raise Inner::Failure(code is 1);
end when;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`)
	_, err := Compile(source, "System")
	if err == nil || !strings.Contains(err.Error(), `raise names undeclared exception "Inner::Failure"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestSourceDeclarationBearingDoRejectsGeneralOrInterfaceBehaviorFunctionForms(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "general declaration",
			source: `type Worker is interface action in Trigger(); end interface Worker;
module WorkerModule() return Worker is
serial when Trigger() do Inner: declare Value : Integer is 1; do null; end do Inner; end when;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;`,
			want: "requires one or more exception declarations",
		},
		{
			name: "interface behavior function body",
			source: `type Worker is interface provides Work : function();
behavior
Work : function() is begin
  Inner: declare exception Failure(); do null; end do Inner;
end function Work;
begin
end interface Worker;
architecture System() is worker : Worker; end architecture System;`,
			want: "requires a concrete module generator",
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

func TestSourceModuleFunctionDeclarationBearingDoUsesExactLexicalExceptions(t *testing.T) {
	source := []byte(`
type Worker is interface
  exception InterfaceNoise;
  action in Trigger();
  action out NamedRecovered(code : Integer);
  action out UnnamedRecovered(code : Integer);
  action out FunctionContinued();
  action out FunctionHandlerRecovered(code : Integer);
  action out FunctionHandlerContinued();
  action out CallerContinued();
  action out Wrong();
  provides Work : function();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure(code : Integer);
  exception EnterHandler;
  Work : function() is begin
    Named: declare exception Failure(code : Integer); do
      raise Named::Failure(code is 2);
    handler
      is WorkerModule::Failure(code is ?Code) => Wrong();
      is WorkerModule::Named::Failure(code is ?Code) => NamedRecovered(?Code);
    end do Named;
    declare exception Failure(code : Integer); do
      raise Failure(code is 3);
    handler
      is WorkerModule::Failure(code is ?Code) => Wrong();
      is Failure(code is ?Code) => UnnamedRecovered(?Code);
    end do;
    FunctionContinued();
    raise EnterHandler;
  handler is EnterHandler =>
    declare exception HandlerFailure(code : Integer); do
      raise HandlerFailure(code is 4);
    handler is HandlerFailure(code is ?Code) => FunctionHandlerRecovered(?Code);
    end do;
    FunctionHandlerContinued();
  end function Work;
serial when Trigger do
  Work();
  CallerContinued();
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 80,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	failures := sourceNamedEvents(result.Poset, "worker", "Failure")
	named := sourceNamedEvents(result.Poset, "worker", "NamedRecovered")
	unnamed := sourceNamedEvents(result.Poset, "worker", "UnnamedRecovered")
	functionContinued := sourceNamedEvents(result.Poset, "worker", "FunctionContinued")
	handlerFailure := sourceNamedEvents(result.Poset, "worker", "HandlerFailure")
	handlerRecovered := sourceNamedEvents(result.Poset, "worker", "FunctionHandlerRecovered")
	handlerContinued := sourceNamedEvents(result.Poset, "worker", "FunctionHandlerContinued")
	callerContinued := sourceNamedEvents(result.Poset, "worker", "CallerContinued")
	if len(failures) != 2 || len(named) != 1 || len(unnamed) != 1 ||
		len(functionContinued) != 1 || len(handlerFailure) != 1 || len(handlerRecovered) != 1 ||
		len(handlerContinued) != 1 || len(callerContinued) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
		t.Fatalf("Failure/Named/Unnamed/Function/HandlerFailure/HandlerRecovered/HandlerContinued/Caller=%d/%d/%d/%d/%d/%d/%d/%d",
			len(failures), len(named), len(unnamed), len(functionContinued), len(handlerFailure),
			len(handlerRecovered), len(handlerContinued), len(callerContinued))
	}
	var namedFailure, unnamedFailure *gorapide.Event
	for _, failure := range failures {
		switch failure.ParamInt("code") {
		case 2:
			namedFailure = failure
		case 3:
			unnamedFailure = failure
		}
	}
	if namedFailure == nil || unnamedFailure == nil ||
		named[0].ParamInt("code") != 2 || unnamed[0].ParamInt("code") != 3 {
		t.Fatalf("function lexical exception events=%#v named=%#v unnamed=%#v", failures, named[0].Params, unnamed[0].Params)
	}
	assertOnlyDirectCause(t, result.Poset, named[0], namedFailure)
	assertOnlyDirectCause(t, result.Poset, unnamedFailure, named[0])
	assertOnlyDirectCause(t, result.Poset, unnamed[0], unnamedFailure)
	assertOnlyDirectCause(t, result.Poset, functionContinued[0], unnamed[0])
	if handlerFailure[0].ParamInt("code") != 4 || handlerRecovered[0].ParamInt("code") != 4 {
		t.Fatalf("function-handler local exception=%#v recovery=%#v", handlerFailure[0].Params, handlerRecovered[0].Params)
	}
	assertOnlyDirectCause(t, result.Poset, handlerRecovered[0], handlerFailure[0])
	assertOnlyDirectCause(t, result.Poset, handlerContinued[0], handlerRecovered[0])
	if !result.Poset.IsCausallyBefore(functionContinued[0].ID, handlerFailure[0].ID) ||
		!result.Poset.IsCausallyBefore(handlerContinued[0].ID, callerContinued[0].ID) {
		t.Fatal("caller continued before declaration-bearing function completed")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed declaration-bearing function execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("declaration-bearing function replay changed canonical bytes")
	}
}

func TestSourceOverloadedFunctionDoExceptionIdentitiesAreDistinctAndCanonical(t *testing.T) {
	integerBody := `Work : function(value : Integer) is begin
    Local: declare exception Failure(code : Integer); do
      raise Local::Failure(code is value);
    handler is WorkerModule::Local::Failure(code is ?Code) => IntegerRecovered(?Code);
    end do Local;
  end function Work;`
	booleanBody := `Work : function(flag : Boolean) is begin
    Local: declare exception Failure(flag : Boolean); do
      raise Local::Failure(flag is flag);
    handler is WorkerModule::Local::Failure(flag is ?Caught) => BooleanRecovered();
    end do Local;
  end function Work;`
	build := func(reverse bool) []byte {
		bodies := integerBody + booleanBody
		if reverse {
			bodies = booleanBody + integerBody
		}
		return []byte(`
type Worker is interface
  action in Trigger();
  action out IntegerRecovered(code : Integer);
  action out BooleanRecovered();
  action out Done();
  provides Work : function(value : Integer);
  provides Work : function(flag : Boolean);
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  ` + bodies + `
serial when Trigger do
  Work(7);
  Work(True);
  Done();
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`)
	}
	left, err := Compile(build(false), "System")
	if err != nil {
		t.Fatal(err)
	}
	right, err := Compile(build(true), "System")
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("function order changed exact local exception identity: %s != %s", leftDigest, rightDigest)
	}
	journal := arch.NewExecutionJournal(leftDigest, 100,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	leftResult, err := left.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	rightResult, err := right.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	integerRecovered := sourceNamedEvents(leftResult.Poset, "worker", "IntegerRecovered")
	if len(integerRecovered) != 1 || integerRecovered[0].ParamInt("code") != 7 ||
		len(sourceNamedEvents(leftResult.Poset, "worker", "BooleanRecovered")) != 1 ||
		len(sourceNamedEvents(leftResult.Poset, "worker", "Done")) != 1 {
		t.Fatalf("overloaded local exception recovery integer=%#v boolean=%d done=%d",
			integerRecovered,
			len(sourceNamedEvents(leftResult.Poset, "worker", "BooleanRecovered")),
			len(sourceNamedEvents(leftResult.Poset, "worker", "Done")))
	}
	leftArtifact, _ := leftResult.MarshalCanonical()
	rightArtifact, _ := rightResult.MarshalCanonical()
	if !bytes.Equal(leftArtifact, rightArtifact) {
		t.Fatal("function declaration order changed canonical execution artifact")
	}
}

func TestSourceModuleFunctionDeclarationBearingDoScopeDoesNotLeak(t *testing.T) {
	_, err := Compile([]byte(`
type Worker is interface provides Work : function(); end interface Worker;
module WorkerModule() return Worker is
  Work : function() is begin
    declare exception LocalOnly; do null; end do;
    raise LocalOnly;
  end function Work;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`), "System")
	if err == nil || !strings.Contains(err.Error(), `raise names undeclared exception "LocalOnly"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestSourceDeclarationBearingDoExceptionOrderIsCanonical(t *testing.T) {
	build := func(declarations, choices string) []byte {
		return []byte(fmt.Sprintf(`
type Worker is interface
  action in Trigger();
  action out Recovered(code : Integer);
  action out Wrong(code : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
serial when Trigger() do
  Inner: declare
    %s
  do
    raise Inner::First(code is 7);
  handler
    %s
  end do Inner;
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`, declarations, choices))
	}
	sources := [][]byte{
		build(
			"exception First(code : Integer); exception Second(code : Integer);",
			"is Inner::First(code is ?Code) => Recovered(?Code); is Inner::Second(code is ?Code) => Wrong(?Code);",
		),
		build(
			"exception Second(code : Integer); exception First(code : Integer);",
			"is Inner::Second(code is ?Code) => Wrong(?Code); is Inner::First(code is ?Code) => Recovered(?Code);",
		),
	}
	var baselineDigest string
	var baselineArtifact []byte
	for _, source := range sources {
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		journal := arch.NewExecutionJournal(digest, 40, arch.InputEvent{
			Key: "trigger", Source: "stimulus", Action: "Trigger",
		})
		result, err := model.ExecuteDeterministic(journal)
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := result.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if len(sourceNamedEvents(result.Poset, "worker", "Recovered")) != 1 ||
			len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
			t.Fatal("declaration/handler order changed exact local exception choice")
		}
		if baselineDigest == "" {
			baselineDigest, baselineArtifact = digest, artifact
			continue
		}
		if digest != baselineDigest || !bytes.Equal(artifact, baselineArtifact) {
			t.Fatalf("declare-do order changed model/artifact: %s/%s", baselineDigest, digest)
		}
	}
}

func TestSourceUnlabeledDeclarationBearingDoRestoresLexicalExceptionVisibility(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out LocalRecovered(code : Integer);
  action out ModuleRecovered(code : Integer);
  action out Wrong(code : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
exception Failure(code : Integer);
serial when Trigger() do
  declare exception Failure(code : Integer); do
    raise Failure(code is 1);
  handler
    is WorkerModule::Failure(code is ?Code) => Wrong(?Code);
    is Failure(code is ?Code) => LocalRecovered(?Code);
  end do;
  raise Failure(code is 2);
handler
  is WorkerModule::Failure(code is ?Code) => ModuleRecovered(?Code);
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	local := file.Modules[0].Processes[0].Statements[0]
	if local.Label != "" || len(local.Exceptions) != 1 {
		t.Fatalf("unlabeled declaration-bearing do AST=%#v", local)
	}
	model, err := CompileFile(file, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 80,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, err := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"LocalRecovered", "ModuleRecovered"} {
		if got := len(sourceNamedEvents(result.Poset, "worker", name)); got != 1 {
			t.Fatalf("%s events=%d", name, got)
		}
	}
	if got := len(sourceNamedEvents(result.Poset, "worker", "Wrong")); got != 0 {
		t.Fatalf("Wrong events=%d", got)
	}
	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed unlabeled declaration-bearing do execution")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("unlabeled declaration-bearing do replay changed canonical bytes")
	}
}

func TestSourceUnlabeledDeclarationBearingDoIdentityIsCanonicalAcrossProcessAndChoiceOrder(t *testing.T) {
	process := func(trigger, recovered, choices string) string {
		return fmt.Sprintf(`declare exception OuterWhen(); when %s() declare exception InnerWhen(); do
  declare exception Failure(); do
    raise Failure();
  handler
    %s
  end do;
end when;`, trigger, strings.ReplaceAll(choices, "$RECOVERED", recovered))
	}
	build := func(first, second string) []byte {
		return []byte(fmt.Sprintf(`
type Worker is interface
  action in TriggerA(); action in TriggerB();
  action out ARecovered(); action out BRecovered(); action out Wrong();
end interface Worker;
type Stimulus is interface action out TriggerA(); action out TriggerB(); end interface Stimulus;
module WorkerModule() return Worker is
exception Outer();
parallel %s || %s
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.TriggerA to worker.TriggerA;
stimulus.TriggerB to worker.TriggerB;
end architecture System;
`, first, second))
	}
	localFirst := "is Failure() => $RECOVERED(); is Outer() => Wrong();"
	outerFirst := "is Outer() => Wrong(); is Failure() => $RECOVERED();"
	sources := [][]byte{
		build(process("TriggerA", "ARecovered", localFirst), process("TriggerB", "BRecovered", localFirst)),
		build(process("TriggerB", "BRecovered", outerFirst), process("TriggerA", "ARecovered", outerFirst)),
	}
	var baselineDigest string
	var baselineArtifact []byte
	for _, source := range sources {
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 80,
			arch.InputEvent{Key: "a", Source: "stimulus", Action: "TriggerA"},
			arch.InputEvent{Key: "b", Source: "stimulus", Action: "TriggerB"},
		))
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := result.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"ARecovered", "BRecovered"} {
			if got := len(sourceNamedEvents(result.Poset, "worker", name)); got != 1 {
				t.Fatalf("%s events=%d", name, got)
			}
		}
		if baselineDigest == "" {
			baselineDigest, baselineArtifact = digest, artifact
			continue
		}
		if digest != baselineDigest || !bytes.Equal(artifact, baselineArtifact) {
			t.Fatalf("process/choice order changed unlabeled do identity: %s/%s", baselineDigest, digest)
		}
	}
}

func TestSourceOuterDeclarationBearingWhenSelectsExactExceptionScope(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger(code : Integer);
  action out Recovered(code : Integer);
  action out Wrong(code : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(code : Integer); end interface Stimulus;

module WorkerModule() return Worker is
exception Failure(code : Integer);
serial Rule: declare
  exception Failure(code : Integer);
when (?Code : Integer) Trigger(code is ?Code) do
  raise WorkerModule::Rule::Failure(code is ?Code);
handler
  is WorkerModule::Failure(code is ?Caught) => Wrong(?Caught);
  is Rule::Failure(code is ?Caught) => Recovered(?Caught);
end when Rule;
end module WorkerModule;

architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	process := file.Modules[0].Processes[0]
	if process.Label != "Rule" || len(process.OuterExceptions) != 1 ||
		process.OuterExceptions[0].Name != "Failure" {
		t.Fatalf("outer declaration-bearing when AST=%#v", process)
	}
	if got := process.Statements[0].Call.Name; got != "WorkerModule::Rule::Failure" {
		t.Fatalf("full outer-when scope raise AST=%q", got)
	}

	model, err := CompileFile(file, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 100,
		arch.InputEvent{Key: "first", Source: "stimulus", Action: "Trigger", Params: map[string]any{"code": 1}},
		arch.InputEvent{Key: "second", Source: "stimulus", Action: "Trigger", Params: map[string]any{"code": 2}},
	)
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, err := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sourceNamedEvents(result.Poset, "worker", "Recovered")); got != 2 {
		t.Fatalf("Recovered events=%d", got)
	}
	if got := len(sourceNamedEvents(result.Poset, "worker", "Wrong")); got != 0 {
		t.Fatalf("Wrong events=%d", got)
	}
	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed outer declaration-bearing when execution")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("outer declaration-bearing when replay changed canonical bytes")
	}
}

func TestSourceOuterDeclarationBearingWhenExceptionScopeDoesNotLeak(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); end interface Worker;
module WorkerModule() return Worker is
exception Failure(code : Integer);
serial Rule: declare exception Failure(code : Integer); when Trigger() do
  raise WorkerModule::Failure(code is 1);
end when Rule;
handler
  is WorkerModule::Failure(code is ?Code) => raise Rule::Failure(code is ?Code);
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`)
	_, err := Compile(source, "System")
	if err == nil || !strings.Contains(err.Error(), `raise names undeclared exception "Rule::Failure"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestSourceDeclarationBearingWhenRejectsGeneralForms(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "general outer declaration",
			source: `type Worker is interface action in Trigger(); end interface Worker;
module WorkerModule() return Worker is
serial Rule: declare Value : Integer is 1; when Trigger() do null; end when Rule;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;`,
			want: "requires one or more exception declarations",
		},
		{
			name: "general per-match declaration",
			source: `type Worker is interface action in Trigger(); end interface Worker;
module WorkerModule() return Worker is
serial Rule: when Trigger() declare Value : Integer is 1; do null; end when Rule;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;`,
			want: "requires one or more exception declarations",
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

func TestSourceUnlabeledDeclarationBearingWhenRestoresLexicalExceptionVisibility(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out OuterRecovered();
  action out InnerRecovered();
  action out ModuleRecovered();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
exception ModuleFailure();
serial declare exception OuterFailure(); when Trigger() declare exception InnerFailure(); do
  do raise OuterFailure(); handler is OuterFailure() => OuterRecovered(); end do;
  do raise InnerFailure(); handler is InnerFailure() => InnerRecovered(); end do;
  raise ModuleFailure();
handler
  is ModuleFailure() => ModuleRecovered();
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	process := file.Modules[0].Processes[0]
	if process.Label != "" || len(process.OuterExceptions) != 1 ||
		len(process.IterationExceptions) != 1 {
		t.Fatalf("unlabeled declaration-bearing when AST=%#v", process)
	}
	model, err := CompileFile(file, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 120,
		arch.InputEvent{Key: "first", Source: "stimulus", Action: "Trigger"},
		arch.InputEvent{Key: "second", Source: "stimulus", Action: "Trigger"},
	)
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, err := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"OuterRecovered", "InnerRecovered", "ModuleRecovered"} {
		if got := len(sourceNamedEvents(result.Poset, "worker", name)); got != 2 {
			t.Fatalf("%s events=%d", name, got)
		}
	}
	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed unlabeled declaration-bearing when execution")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("unlabeled declaration-bearing when replay changed canonical bytes")
	}
}

func TestSourceUnlabeledWhenExceptionScopesDoNotLeakOrGainScopeNames(t *testing.T) {
	tests := []struct {
		name       string
		moduleTail string
		want       string
	}{
		{
			name: "module handler leakage",
			moduleTail: `serial declare exception OuterFailure(); when Trigger() do raise OuterFailure(); end when;
handler is OuterFailure() => null;`,
			want: `handler choice names missing exception or visible action "OuterFailure"`,
		},
		{
			name: "invented module scope",
			moduleTail: `serial declare exception OuterFailure(); when Trigger() do
  raise WorkerModule::OuterFailure();
end when;`,
			want: `raise names undeclared exception "WorkerModule::OuterFailure"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(fmt.Sprintf(`
type Worker is interface action in Trigger(); end interface Worker;
module WorkerModule() return Worker is
%s
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`, test.moduleTail))
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestSourceOuterWhenLookaheadPreservesTopLevelDeclarationBearingDo(t *testing.T) {
	sources := [][]byte{[]byte(`
type Worker is interface action out Done(); end interface Worker;
module WorkerModule() return Worker is
serial Outer: declare exception Failure(); do Done(); end do Outer;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
	`), []byte(`
type Worker is interface action out Done(); end interface Worker;
module WorkerModule() return Worker is
serial declare exception Failure(); do Done(); end do;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
	`)}
	for _, source := range sources {
		file, err := Parse(source)
		if err != nil {
			t.Fatal(err)
		}
		process := file.Modules[0].Processes[0]
		if !process.Entry || len(process.OuterExceptions) != 0 || len(process.Statements) != 1 ||
			len(process.Statements[0].Exceptions) != 1 {
			t.Fatalf("top-level declaration-bearing do was reclassified as when: %#v", process)
		}
		if _, err := CompileFile(file, "System"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSourceOuterDeclarationBearingWhenExceptionOrderIsCanonical(t *testing.T) {
	build := func(declarations, choices string) []byte {
		return []byte(fmt.Sprintf(`
type Worker is interface
  action in Trigger();
  action out Recovered();
  action out Wrong();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
serial Rule: declare %s when Trigger() do
  raise Rule::First();
handler
  %s
end when Rule;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`, declarations, choices))
	}
	sources := [][]byte{
		build(
			"exception First(); exception Second();",
			"is Rule::First() => Recovered(); is Rule::Second() => Wrong();",
		),
		build(
			"exception Second(); exception First();",
			"is Rule::Second() => Wrong(); is Rule::First() => Recovered();",
		),
	}
	var baselineDigest string
	var baselineArtifact []byte
	for _, source := range sources {
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 40,
			arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
		))
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := result.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if len(sourceNamedEvents(result.Poset, "worker", "Recovered")) != 1 ||
			len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
			t.Fatal("outer when declaration/handler order changed exact exception choice")
		}
		if baselineDigest == "" {
			baselineDigest, baselineArtifact = digest, artifact
			continue
		}
		if digest != baselineDigest || !bytes.Equal(artifact, baselineArtifact) {
			t.Fatalf("outer when declaration order changed model/artifact: %s/%s", baselineDigest, digest)
		}
	}
}

func TestSourcePerMatchDeclarationBearingWhenSelectsExactExceptionScope(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger(code : Integer);
  action out OuterRecovered(code : Integer);
  action out InnerRecovered(code : Integer);
  action out Wrong(code : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(code : Integer); end interface Stimulus;

module WorkerModule() return Worker is
serial Rule: declare
  exception Failure(code : Integer);
when (?Code : Integer) Trigger(code is ?Code) declare
  exception Failure(code : Integer);
do
  do
    raise WorkerModule::Rule::Failure(code is ?Code);
  handler
    is Failure(code is ?Caught) => Wrong(?Caught);
    is Rule::Failure(code is ?Caught) => OuterRecovered(?Caught);
  end do;
  raise Failure(code is ?Code);
handler
  is Rule::Failure(code is ?Caught) => Wrong(?Caught);
  is Failure(code is ?Caught) => InnerRecovered(?Caught);
end when Rule;
end module WorkerModule;

architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	process := file.Modules[0].Processes[0]
	if process.Label != "Rule" || len(process.OuterExceptions) != 1 ||
		len(process.IterationExceptions) != 1 || process.IterationExceptions[0].Name != "Failure" {
		t.Fatalf("per-match declaration-bearing when AST=%#v", process)
	}

	model, err := CompileFile(file, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 140,
		arch.InputEvent{Key: "first", Source: "stimulus", Action: "Trigger", Params: map[string]any{"code": 1}},
		arch.InputEvent{Key: "second", Source: "stimulus", Action: "Trigger", Params: map[string]any{"code": 2}},
	)
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, err := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"OuterRecovered", "InnerRecovered"} {
		if got := len(sourceNamedEvents(result.Poset, "worker", name)); got != 2 {
			t.Fatalf("%s events=%d", name, got)
		}
	}
	if got := len(sourceNamedEvents(result.Poset, "worker", "Wrong")); got != 0 {
		t.Fatalf("Wrong events=%d", got)
	}
	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed per-match declaration-bearing when execution")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("per-match declaration-bearing when replay changed canonical bytes")
	}
}

func TestSourcePerMatchDeclarationBearingWhenExceptionScopeDoesNotLeak(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); end interface Worker;
module WorkerModule() return Worker is
serial Rule: when Trigger() declare exception Failure(); do
  raise Failure();
end when Rule;
handler
  is Failure() => null;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`)
	_, err := Compile(source, "System")
	if err == nil || !strings.Contains(err.Error(),
		`handler choice names missing exception or visible action "Failure"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestSourcePerMatchDeclarationBearingWhenExceptionOrderIsCanonical(t *testing.T) {
	build := func(declarations, choices string) []byte {
		return []byte(fmt.Sprintf(`
type Worker is interface
  action in Trigger();
  action out Recovered();
  action out Wrong();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
serial Rule: when Trigger() declare %s do
  raise First();
handler
  %s
end when Rule;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`, declarations, choices))
	}
	sources := [][]byte{
		build(
			"exception First(); exception Second();",
			"is First() => Recovered(); is Second() => Wrong();",
		),
		build(
			"exception Second(); exception First();",
			"is Second() => Wrong(); is First() => Recovered();",
		),
	}
	var baselineDigest string
	var baselineArtifact []byte
	for _, source := range sources {
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 40,
			arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
		))
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := result.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if len(sourceNamedEvents(result.Poset, "worker", "Recovered")) != 1 ||
			len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
			t.Fatal("per-match when declaration/handler order changed exact exception choice")
		}
		if baselineDigest == "" {
			baselineDigest, baselineArtifact = digest, artifact
			continue
		}
		if digest != baselineDigest || !bytes.Equal(artifact, baselineArtifact) {
			t.Fatalf("per-match when declaration order changed model/artifact: %s/%s", baselineDigest, digest)
		}
	}
}

func TestSourceInterfaceTypeScopeNameSelectsExactExceptionConstituent(t *testing.T) {
	source := []byte(`
type Worker is interface
  exception Failure(code : Integer);
  action in Trigger();
  action out TypeRecovered(code : Integer);
  action out ScopeRecovered(code : Integer);
  action out Wrong(code : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module WorkerModule() return Worker is
exception Failure(code : Integer);
serial when Trigger() do
  do
    raise Worker::Failure(code is 1);
  handler
    is WorkerModule::Failure(code is ?Code) => Wrong(?Code);
    is Self.Failure(code is ?Code) => TypeRecovered(?Code);
  end do;
  do
    raise Self.Failure(code is 2);
  handler
    is WorkerModule::Failure(code is ?Code) => Wrong(?Code);
    is Worker::Failure(code is ?Code) => ScopeRecovered(?Code);
  end do;
end when;
end module WorkerModule;

architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	process := file.Modules[0].Processes[0]
	if got := process.Statements[0].Body[0].Call.Name; got != "Worker::Failure" {
		t.Fatalf("interface type-scoped raise AST=%q", got)
	}

	model, err := CompileFile(file, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 80,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, err := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TypeRecovered", "ScopeRecovered"} {
		if got := len(sourceNamedEvents(result.Poset, "worker", name)); got != 1 {
			t.Fatalf("%s events=%d", name, got)
		}
	}
	if got := len(sourceNamedEvents(result.Poset, "worker", "Wrong")); got != 0 {
		t.Fatalf("Wrong events=%d", got)
	}
	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed interface type-scope exception execution")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("interface type-scope exception replay changed canonical bytes")
	}
}

func TestSourceVisibleForeignInterfaceScopeExceptionOrderIsCanonical(t *testing.T) {
	worker := `type Worker is interface
  action in Trigger();
  action out Recovered(code : Integer);
end interface Worker;`
	other := `type Other is interface exception Foreign(code : Integer); end interface Other;`
	build := func(first, second string) []byte {
		return []byte(fmt.Sprintf(`
%s
%s
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
serial when Trigger() do
  raise Other::Foreign(code is 7);
handler
  is Other::Foreign(code is ?Code) => Recovered(?Code);
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`, first, second))
	}
	var baselineDigest string
	var baselineArtifact []byte
	for _, source := range [][]byte{build(worker, other), build(other, worker)} {
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 40,
			arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
		))
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := result.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if got := len(sourceNamedEvents(result.Poset, "worker", "Recovered")); got != 1 {
			t.Fatalf("Recovered events=%d", got)
		}
		if baselineDigest == "" {
			baselineDigest, baselineArtifact = digest, artifact
			continue
		}
		if digest != baselineDigest || !bytes.Equal(artifact, baselineArtifact) {
			t.Fatalf("interface declaration order changed model/artifact: %s/%s", baselineDigest, digest)
		}
	}
}

func TestSourceInterfaceTypeScopeRejectsUnknownOrNonOwnedException(t *testing.T) {
	tests := []struct {
		name      string
		reference string
	}{
		{name: "missing member", reference: "Worker::Missing"},
		{name: "unknown scope", reference: "Unknown::Failure"},
		{name: "invented nesting", reference: "WorkerModule::Worker::Failure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(fmt.Sprintf(`
type Worker is interface exception Failure(); action in Trigger(); end interface Worker;
module WorkerModule() return Worker is
serial when Trigger() do raise %s(); end when;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`, test.reference))
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(),
				`raise names undeclared exception "`+test.reference+`"`) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSourceInterfaceExceptionIdentityAcrossGeneratedModules(t *testing.T) {
	tests := []struct {
		name                string
		source              string
		declarationOwner    string
		wantNamed, wantElse int
	}{
		{
			name: "one shared interface declaration",
			source: `
type Endpoint is interface
  exception Failure(code : Integer);
  action out Register(value : Endpoint);
  action in Accept(value : Endpoint);
  action out Ready();
  action in Go();
  action out NamedRecovery(code : Integer);
  action out ElseRecovery();
end interface Endpoint;
module ProducerModule() return Endpoint is
initial Register(Self);
serial when Go do raise Failure(7); end when;
end module ProducerModule;
module ConsumerModule() return Endpoint is
serial when (?Producer : Endpoint) Accept(?Producer) do Link(?Producer); Ready(); end when;
handler is Failure(?Code) => NamedRecovery(?Code); else ElseRecovery();
end module ConsumerModule;
architecture System() is
  plane : Endpoint is ProducerModule();
  sector : Endpoint is ConsumerModule();
connect
  (?Producer : Endpoint) plane.Register(?Producer) to sector.Accept(?Producer);
  sector.Ready to plane.Go;
end architecture System;
`,
			declarationOwner: "Endpoint", wantNamed: 1,
		},
		{
			name: "independent same-spelling interface declarations",
			source: `
type Aircraft is interface
  exception Failure(code : Integer);
  action out Register(value : Aircraft);
  action in Go();
end interface Aircraft;
type Sector is interface
  exception Failure(code : Integer);
  action in Accept(value : Aircraft);
  action out Ready();
  action out NamedRecovery(code : Integer);
  action out ElseRecovery();
end interface Sector;
module ProducerModule() return Aircraft is
initial Register(Self);
serial when Go do raise Failure(7); end when;
end module ProducerModule;
module ConsumerModule() return Sector is
serial when (?Producer : Aircraft) Accept(?Producer) do Link(?Producer); Ready(); end when;
handler is Failure(?Code) => NamedRecovery(?Code); else ElseRecovery();
end module ConsumerModule;
architecture System() is
  plane : Aircraft is ProducerModule();
  sector : Sector is ConsumerModule();
connect
  (?Producer : Aircraft) plane.Register(?Producer) to sector.Accept(?Producer);
  sector.Ready to plane.Go;
end architecture System;
`,
			declarationOwner: "Aircraft", wantElse: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model, err := Compile([]byte(test.source), "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			journal := arch.NewExecutionJournal(digest, 60)
			prior := runtime.GOMAXPROCS(1)
			result, err := model.ExecuteDeterministic(journal)
			if err != nil {
				runtime.GOMAXPROCS(prior)
				t.Fatal(err)
			}
			runtime.GOMAXPROCS(8)
			repeated, repeatedErr := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(prior)
			if repeatedErr != nil {
				t.Fatal(repeatedErr)
			}

			failures := sourceNamedEvents(result.Poset, "plane", "Failure")
			named := sourceNamedEvents(result.Poset, "sector", "NamedRecovery")
			elseRecovery := sourceNamedEvents(result.Poset, "sector", "ElseRecovery")
			if len(failures) != 1 || len(named) != test.wantNamed || len(elseRecovery) != test.wantElse {
				t.Fatalf("Failure/NamedRecovery/ElseRecovery=%d/%d/%d",
					len(failures), len(named), len(elseRecovery))
			}
			recovery := named
			if len(recovery) == 0 {
				recovery = elseRecovery
			}
			assertOnlyDirectCause(t, result.Poset, recovery[0], failures[0])

			plane := lifecycleModuleByOccurrence(t, result, "component:plane")
			sector := lifecycleModuleByOccurrence(t, result, "component:sector")
			propagation := exceptionPropagationBySource(t, result, plane.ModuleID)
			wantDeclaration := interfaceExceptionDeclarationIdentity(
				test.declarationOwner, InterfaceNameProvides, "Failure",
			)
			if propagation.ExceptionDeclaration != wantDeclaration {
				t.Fatalf("exception declaration=%q, want %q", propagation.ExceptionDeclaration, wantDeclaration)
			}
			sectorDisposition := ""
			for _, target := range propagation.Targets {
				if target.ModuleID == sector.ModuleID {
					sectorDisposition = target.Disposition + ":" + strings.Join(target.Relations, ",")
				}
			}
			if sectorDisposition != "handled:linked" {
				t.Fatalf("sector propagation disposition=%q", sectorDisposition)
			}

			encoded, _ := result.MarshalCanonical()
			repeatedEncoded, _ := repeated.MarshalCanonical()
			if !bytes.Equal(encoded, repeatedEncoded) {
				t.Fatal("GOMAXPROCS changed interface-exception propagation")
			}
		})
	}
}
