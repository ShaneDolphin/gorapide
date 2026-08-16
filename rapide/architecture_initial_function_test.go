package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestArchitectureInitialCallsInwardProvidedFunctionAfterProviderStartup(t *testing.T) {
	source := []byte(`
type Boundary is interface
  action out After();
  provides Lookup : function(value : Integer) return Integer;
end interface Boundary;
type Provider is interface
  action out Ready();
  action out Inside(value : Integer);
  provides Fetch : function(operand : Integer) return Integer;
end interface Provider;

module ProviderModule() return Provider is
  Fetch : function(operand : Integer) return Integer is
    begin
      Inside(operand + 1);
      return operand + 2;
    end function Fetch;
initial
  Ready();
end module ProviderModule;

architecture Root() return Boundary is
  provider : Provider is ProviderModule();
connect
  Lookup to provider.Fetch;
initial
  Lookup(5);
  After();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 100)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	providerStart := sourceNamedEvents(result.Poset, "$module/provider", arch.ArchitectureStartAction)
	ready := sourceNamedEvents(result.Poset, "provider", "Ready")
	call := functionBoundaryEvents(result.Poset, arch.ArchitectureInterfaceID, "Lookup'Call")
	providerCall := functionBoundaryEvents(result.Poset, "provider", "Fetch'Call")
	inside := sourceNamedEvents(result.Poset, "provider", "Inside")
	returned := functionBoundaryEvents(result.Poset, "provider", "Fetch'Return")
	boundaryReturn := functionBoundaryEvents(result.Poset, arch.ArchitectureInterfaceID, "Lookup'Return")
	after := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "After")
	if len(providerStart) != 1 || len(ready) != 1 || len(call) != 1 ||
		len(providerCall) != 1 || len(inside) != 1 || len(returned) != 1 ||
		len(boundaryReturn) != 1 || len(after) != 1 {
		t.Fatalf("startup/call/body/return/after=%d/%d/%d/%d/%d/%d/%d/%d",
			len(providerStart), len(ready), len(call), len(providerCall),
			len(inside), len(returned), len(boundaryReturn), len(after))
	}
	if call[0].ID != providerCall[0].ID || returned[0].ID != boundaryReturn[0].ID {
		t.Fatal("architecture/provider aliases duplicated synchronous call or return occurrences")
	}
	if inside[0].ParamInt("value") != 6 || returned[0].ParamInt("Return") != 7 {
		t.Fatalf("function values inside/return=%#v/%#v", inside[0].Params, returned[0].Params)
	}
	chain := []*gorapide.Event{providerStart[0], ready[0], call[0], inside[0], returned[0], after[0]}
	for index := 0; index+1 < len(chain); index++ {
		if !result.Poset.IsCausallyBefore(chain[index].ID, chain[index+1].ID) {
			t.Fatalf("startup function chain lost edge %d: %s !< %s", index, chain[index].ID, chain[index+1].ID)
		}
	}
	if direct := result.Poset.DirectCauses(call[0].ID); len(direct) != 1 || direct[0].ID != ready[0].ID {
		t.Fatalf("architecture initial call direct causes=%#v, want provider startup frontier %s", direct, ready[0].ID)
	}
	if len(result.Firings) != 2 || result.Firings[0].Transition != "initial" ||
		result.Firings[0].Target != "provider" ||
		result.Firings[1].Transition != "architecture-initial" ||
		result.Firings[1].Target != arch.ArchitectureInterfaceID {
		t.Fatalf("startup firing audit=%#v", result.Firings)
	}

	canonical, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(canonical, replayedBytes) {
		t.Fatal("architecture initial function replay changed canonical bytes")
	}
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for iteration := 0; iteration < 2; iteration++ {
			repeated, err := Compile(source, "Root")
			if err != nil {
				t.Fatal(err)
			}
			repeatedDigest, err := repeated.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			if repeatedDigest != digest {
				t.Fatalf("model changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
			repeatedResult, err := repeated.ExecuteDeterministic(arch.NewExecutionJournal(repeatedDigest, 100))
			if err != nil {
				t.Fatal(err)
			}
			repeatedBytes, _ := repeatedResult.MarshalCanonical()
			if !bytes.Equal(canonical, repeatedBytes) {
				t.Fatalf("artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

func TestArchitectureInitialFunctionCannotEscapeChildElaborationSubtree(t *testing.T) {
	source := []byte(`
type Boundary is interface
  provides Down : function() return Integer;
  requires Up : function() return Integer;
end interface Boundary;
type Worker is interface
  provides Internal : function() return Integer;
  requires External : function() return Integer;
end interface Worker;
type Server is interface provides Fetch : function() return Integer; end interface Server;

module WorkerModule() return Worker is
  temporary : var Integer := 0;
  Internal : function() return Integer is
    begin
      temporary := External();
      return $temporary;
    end function Internal;
end module WorkerModule;
module ServerModule() return Server is
  Fetch : function() return Integer is begin return 1; end function Fetch;
end module ServerModule;

architecture Child() return Boundary is
  worker : Worker is WorkerModule();
connect
  Down to worker.Internal;
  worker.External to Up;
initial
  Down();
end architecture Child;

architecture Root() is
  child : Boundary is Child();
  server : Server is ServerModule();
connect
  child.Up to server.Fetch;
end architecture Root;
`)
	_, err := Compile(source, "Root")
	if err == nil || !strings.Contains(err.Error(), "outside its elaborated subtree") {
		t.Fatalf("escaping child initializer route error=%v", err)
	}
}

func TestArchitectureInitialFunctionMayReachAnotherProviderInSameSubtree(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Begin : function() return Integer; end interface Boundary;
type Worker is interface
  provides Entry : function() return Integer;
  requires Next : function() return Integer;
end interface Worker;
type Server is interface provides Finish : function() return Integer; end interface Server;
module WorkerModule() return Worker is
  result : var Integer := 0;
  Entry : function() return Integer is
    begin result := Next(); return $result; end function Entry;
end module WorkerModule;
module ServerModule() return Server is
  Finish : function() return Integer is begin return 9; end function Finish;
end module ServerModule;
architecture Root() return Boundary is
  worker : Worker is WorkerModule();
  server : Server is ServerModule();
connect
  Begin to worker.Entry;
  worker.Next to server.Finish;
initial
  Begin();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 100))
	if err != nil {
		t.Fatal(err)
	}
	outerCall := functionBoundaryEvents(result.Poset, arch.ArchitectureInterfaceID, "Begin'Call")
	innerCall := functionBoundaryEvents(result.Poset, "worker", "Next'Call")
	serverStart := sourceNamedEvents(result.Poset, "$module/server", arch.ArchitectureStartAction)
	serverReturn := functionBoundaryEvents(result.Poset, "server", "Finish'Return")
	if len(outerCall) != 1 || len(innerCall) != 1 || len(serverStart) != 1 || len(serverReturn) != 1 {
		t.Fatalf("same-subtree nested call events=%d/%d/%d/%d",
			len(outerCall), len(innerCall), len(serverStart), len(serverReturn))
	}
	causes := result.Poset.DirectCauses(innerCall[0].ID)
	if len(causes) != 2 ||
		!eventSetContainsID(causes, outerCall[0].ID) ||
		!eventSetContainsID(causes, serverStart[0].ID) {
		t.Fatalf("nested initializer call direct causes=%#v", causes)
	}
	if serverReturn[0].ParamInt("Return") != 9 {
		t.Fatalf("same-subtree nested return=%#v", serverReturn[0].Params)
	}
}

func eventSetContainsID(events gorapide.EventSet, id gorapide.EventID) bool {
	for _, event := range events {
		if event != nil && event.ID == id {
			return true
		}
	}
	return false
}

func TestChildArchitectureInitialCallsItsOwnInwardFunctionAlias(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Warm : function(value : Integer) return Integer; end interface Boundary;
type Worker is interface
  action out Seen(value : Integer);
  provides Heat : function(operand : Integer) return Integer;
end interface Worker;
module WorkerModule() return Worker is
  Heat : function(operand : Integer) return Integer is
    begin Seen(operand); return operand; end function Heat;
end module WorkerModule;
architecture Child() return Boundary is
  worker : Worker is WorkerModule();
connect
  Warm to worker.Heat;
initial
  Warm(3);
end architecture Child;
architecture Root() is
  child : Boundary is Child();
connect
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 100))
	if err != nil {
		t.Fatal(err)
	}
	workerID := arch.DeterministicArchitectureComponentID("child", "worker")
	workerStart := sourceNamedEvents(result.Poset, "$module/"+workerID, arch.ArchitectureStartAction)
	call := functionBoundaryEvents(result.Poset, "child", "Warm'Call")
	providerCall := functionBoundaryEvents(result.Poset, workerID, "Heat'Call")
	seen := sourceNamedEvents(result.Poset, workerID, "Seen")
	returned := functionBoundaryEvents(result.Poset, workerID, "Heat'Return")
	boundaryReturn := functionBoundaryEvents(result.Poset, "child", "Warm'Return")
	if len(workerStart) != 1 || len(call) != 1 || len(providerCall) != 1 ||
		len(seen) != 1 || len(returned) != 1 || len(boundaryReturn) != 1 {
		t.Fatalf("child initial function events=%d/%d/%d/%d/%d/%d",
			len(workerStart), len(call), len(providerCall), len(seen), len(returned), len(boundaryReturn))
	}
	if call[0].ID != providerCall[0].ID || returned[0].ID != boundaryReturn[0].ID ||
		seen[0].ParamInt("value") != 3 {
		t.Fatalf("child function alias values/identity=%#v/%#v/%#v", call, returned, seen)
	}
	if direct := result.Poset.DirectCauses(call[0].ID); len(direct) != 1 || direct[0].ID != workerStart[0].ID {
		t.Fatalf("child initializer call direct causes=%#v", direct)
	}
}

func TestArchitectureInitialRequiresFunctionFailsExplicitly(t *testing.T) {
	source := []byte(`
type Boundary is interface requires Imported : function() return Integer; end interface Boundary;
architecture Root() return Boundary is
connect
initial
  Imported();
end architecture Root;
`)
	_, err := Compile(source, "Root")
	if err == nil || !strings.Contains(err.Error(), "not a returned-interface provides function connected inward") {
		t.Fatalf("architecture initial requirement error=%v", err)
	}
}

func TestArchitectureInitialFailedCreationFinalizesRootScopeExactly(t *testing.T) {
	template := `
type Boundary is interface
  action in Trigger();
  action out After();
  provides Spawn : function();
end interface Boundary;
type Factory is interface
  action out Allocated(value : Factory);
  action out Recovered();
  action out Closing();
  provides Spawn : function();
end interface Factory;
module FactoryModule() return Factory is
  exception Failure;
  exception Replacement;
  Spawn : function() is begin Allocated(New(Seed is 1)); end function Spawn;
initial (Seed : Integer is 0)
  if Seed = 1 then raise Failure; end if;
MODULE_HANDLER
final
  Closing();
end module FactoryModule;
architecture Root() return Boundary is
  factory : Factory is FactoryModule();
connect
  Spawn to factory.Spawn;
initial
  Spawn();
  After();
end architecture Root;
`
	for _, test := range []struct {
		name              string
		handler           string
		wantRecovery      bool
		wantFactoryRaised bool
		wantReplacement   bool
	}{
		{name: "propagated", wantFactoryRaised: true},
		{name: "provider module handler", handler: "handler is Failure => Recovered();", wantRecovery: true},
		{name: "provider handler replacement", handler: "handler is Failure => raise Replacement;", wantFactoryRaised: true, wantReplacement: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(strings.Replace(template, "MODULE_HANDLER", test.handler, 1))
			model, err := Compile(source, "Root")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			journal := arch.NewExecutionJournalWithLimits(
				digest, arch.ExecutionLimits{MaxFirings: 100, MaxStatements: 120},
				arch.InputEvent{Key: "after-failed-architecture-initial", Source: arch.ArchitectureInterfaceID, Action: "Trigger"},
			)
			previous := runtime.GOMAXPROCS(1)
			first, firstErr := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(8)
			second, secondErr := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(previous)
			if firstErr != nil || secondErr != nil {
				t.Fatalf("architecture-initial failed creation=%v/%v", firstErr, secondErr)
			}
			factory := lifecycleModuleByOccurrence(t, first, "component:factory")
			root := lifecycleModuleByOccurrence(t, first, "architecture:root")
			var child *arch.ModuleLifecycleRecord
			for index := range first.Modules {
				candidate := &first.Modules[index]
				if candidate.Kind == "allocator-module" && candidate.Parent == factory.ModuleID {
					child = candidate
					break
				}
			}
			if child == nil {
				t.Fatalf("architecture-initial failed child lifecycle=%#v", first.Modules)
			}
			failures := sourceNamedEvents(first.Poset, child.ModuleID, "Failure")
			boundaryCalls := functionBoundaryEvents(first.Poset, arch.ArchitectureInterfaceID, "Spawn'Call")
			providerCalls := functionBoundaryEvents(first.Poset, "factory", "Spawn'Call")
			boundaryReturns := functionBoundaryEvents(first.Poset, arch.ArchitectureInterfaceID, "Spawn'Return")
			providerReturns := functionBoundaryEvents(first.Poset, "factory", "Spawn'Return")
			recovered := sourceNamedEvents(first.Poset, "factory", "Recovered")
			replacements := sourceNamedEvents(first.Poset, "factory", "Replacement")
			closing := sourceNamedEvents(first.Poset, factory.ModuleID, "Closing")
			if len(failures) != 1 || len(boundaryCalls) != 1 || len(providerCalls) != 1 ||
				boundaryCalls[0].ID != providerCalls[0].ID || len(boundaryReturns) != 0 || len(providerReturns) != 0 ||
				len(closing) != 1 ||
				(len(recovered) == 1) != test.wantRecovery ||
				(len(replacements) == 1) != test.wantReplacement ||
				len(first.Poset.ByName("Allocated")) != 0 || len(first.Poset.ByName("After")) != 0 ||
				len(first.Poset.ByName("Trigger")) != 0 {
				t.Fatalf("architecture-initial failure/recovery/replacement/closing/allocated/after/input=%d/%d/%d/%d/%d/%d/%d",
					len(failures), len(recovered), len(replacements), len(closing), len(first.Poset.ByName("Allocated")),
					len(first.Poset.ByName("After")), len(first.Poset.ByName("Trigger")))
			}
			if !first.Poset.IsCausallyBefore(boundaryCalls[0].ID, failures[0].ID) {
				t.Fatal("architecture-initial failed allocation lost its connected function Call prefix")
			}
			if child.State != arch.ModuleFinalizedState || child.Namable ||
				child.TerminationEventID != string(failures[0].ID) || child.FinishEventID == "" ||
				factory.State != arch.ModuleFinalizedState || factory.Namable ||
				(factory.TerminationEventID != "") != test.wantFactoryRaised || factory.FinishEventID == "" ||
				root.State != arch.ModuleFinalizedState || root.Namable ||
				(root.TerminationEventID != "") != test.wantFactoryRaised || root.FinishEventID == "" {
				t.Fatalf("architecture-initial child/factory/root lifecycles=%#v/%#v/%#v", child, factory, root)
			}
			active := failures[0]
			if test.wantReplacement {
				active = replacements[0]
				assertOnlyDirectCause(t, first.Poset, active, failures[0])
			}
			if test.wantFactoryRaised &&
				(factory.TerminationEventID != string(active.ID) || root.TerminationEventID != string(active.ID)) {
				t.Fatalf("architecture-initial propagated occurrence=%#v/%#v/%s", factory, root, active.ID)
			}
			frontier := active
			if test.wantRecovery {
				frontier = recovered[0]
				assertOnlyDirectCause(t, first.Poset, recovered[0], failures[0])
			}
			for _, name := range child.Names {
				if name.Live || len(name.LostAfter) != 1 || name.LostAfter[0] != string(failures[0].ID) {
					t.Fatalf("architecture-initial failed child name=%#v", name)
				}
			}
			for _, name := range root.Names {
				if name.Live || len(name.LostAfter) != 1 || name.LostAfter[0] != string(frontier.ID) {
					t.Fatalf("architecture-initial root name=%#v", name)
				}
			}
			var factoryConstituent arch.ModuleNameRecord
			for _, name := range factory.Names {
				if name.Kind == "architecture-constituent" {
					factoryConstituent = name
				}
			}
			if factoryConstituent.NameID == "" || factoryConstituent.Live ||
				len(factoryConstituent.LostAfter) != 1 || factoryConstituent.LostAfter[0] != string(frontier.ID) {
				t.Fatalf("architecture-initial provider scope loss=%#v", factoryConstituent)
			}
			childFinish, childFinishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
			factoryFinish, factoryFinishExists := first.Poset.Get(gorapide.EventID(factory.FinishEventID))
			rootFinish, rootFinishExists := first.Poset.Get(gorapide.EventID(root.FinishEventID))
			if !childFinishExists || childFinish == nil || !factoryFinishExists || factoryFinish == nil ||
				!rootFinishExists || rootFinish == nil {
				t.Fatalf("architecture-initial Finish events=%#v/%#v/%#v", childFinish, factoryFinish, rootFinish)
			}
			assertOnlyDirectCause(t, first.Poset, childFinish, failures[0])
			assertOnlyDirectCause(t, first.Poset, closing[0], frontier)
			assertOnlyDirectCause(t, first.Poset, factoryFinish, closing[0])
			assertOnlyDirectCause(t, first.Poset, rootFinish, frontier)
			if !first.Poset.IsCausallyIndependent(closing[0].ID, rootFinish.ID) ||
				!first.Poset.IsCausallyIndependent(factoryFinish.ID, rootFinish.ID) {
				t.Fatal("architecture-initial scope cleanup ordered independent factory/root branches")
			}
			architectureFiringFound := false
			constituentFinalizationFound := false
			for _, firing := range first.Firings {
				if firing.Transition == "architecture-initial" && firing.Completion == "exception" &&
					firing.ExceptionEventID == string(failures[0].ID) {
					architectureFiringFound = true
				}
				if firing.Transition == "architecture-constituent-finalization" && firing.Target == "factory" {
					constituentFinalizationFound = true
				}
			}
			if !architectureFiringFound || !constituentFinalizationFound {
				t.Fatalf("architecture-initial failure audit=%#v", first.Firings)
			}
			firstBytes, err := first.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			secondBytes, err := second.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(firstBytes, secondBytes) {
				t.Fatal("GOMAXPROCS changed architecture-initial failed-creation bytes")
			}
			expected, err := first.ArtifactDigest()
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := model.ReplayDeterministic(journal, expected)
			if err != nil {
				t.Fatal(err)
			}
			replayedBytes, err := replayed.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(firstBytes, replayedBytes) {
				t.Fatal("architecture-initial failed-creation replay changed bytes")
			}
		})
	}
}

func TestArchitectureInitialFailedCreationTerminatesRunningStructuralParentExactly(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Spawn : function(); end interface Boundary;
type Factory is interface
  action in Ping();
  action out Allocated(value : Factory);
  action out Closing();
  provides Spawn : function();
end interface Factory;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New(Seed is 1)); end function Spawn;
initial (Seed : Integer is 0)
  if Seed = 1 then raise Failure; end if;
serial when Ping() do null; end when;
final Closing();
end module FactoryModule;
architecture Root() return Boundary is
  factory : Factory is FactoryModule();
connect Spawn to factory.Spawn;
initial Spawn();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 100, MaxStatements: 120},
	)
	previous := runtime.GOMAXPROCS(1)
	first, firstErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if firstErr != nil || secondErr != nil {
		t.Fatalf("running structural-parent shutdown=%v/%v", firstErr, secondErr)
	}
	factory := lifecycleModuleByOccurrence(t, first, "component:factory")
	root := lifecycleModuleByOccurrence(t, first, "architecture:root")
	var child *arch.ModuleLifecycleRecord
	for index := range first.Modules {
		candidate := &first.Modules[index]
		if candidate.Kind == "allocator-module" && candidate.Parent == factory.ModuleID {
			child = candidate
			break
		}
	}
	failures := first.Poset.ByName("Failure")
	closing := sourceNamedEvents(first.Poset, factory.ModuleID, "Closing")
	if child == nil || len(failures) != 1 || len(closing) != 1 ||
		len(first.Poset.ByName("Allocated")) != 0 || len(first.Poset.ByName("Spawn'Return")) != 0 {
		t.Fatalf("running structural-parent child/failure/closing/result=%#v/%d/%d/%d/%d",
			child, len(failures), len(closing), len(first.Poset.ByName("Allocated")),
			len(first.Poset.ByName("Spawn'Return")))
	}
	failure := failures[0]
	if child.State != arch.ModuleFinalizedState || child.TerminationEventID != string(failure.ID) ||
		child.FinishEventID == "" || child.Namable ||
		factory.State != arch.ModuleFinalizedState || factory.TerminationEventID != string(failure.ID) ||
		factory.FinishEventID == "" || factory.Namable ||
		root.State != arch.ModuleFinalizedState || root.TerminationEventID != string(failure.ID) ||
		root.FinishEventID == "" || root.Namable {
		t.Fatalf("running structural-parent lifecycles=%#v/%#v/%#v", child, factory, root)
	}
	foundShutdown := false
	for _, process := range first.Processes {
		if process.ComponentID != "factory" {
			continue
		}
		if !process.Terminated || process.Completion != "module-termination" ||
			process.ExceptionEventID != string(failure.ID) {
			t.Fatalf("running structural-parent process shutdown=%#v", process)
		}
		foundShutdown = true
	}
	if !foundShutdown {
		t.Fatalf("running structural-parent process audit=%#v", first.Processes)
	}
	assertOnlyDirectCause(t, first.Poset, closing[0], failure)
	factoryFinish, factoryFinishExists := first.Poset.Get(gorapide.EventID(factory.FinishEventID))
	rootFinish, rootFinishExists := first.Poset.Get(gorapide.EventID(root.FinishEventID))
	if !factoryFinishExists || !rootFinishExists {
		t.Fatalf("running structural-parent Finish events=%#v/%#v", factoryFinish, rootFinish)
	}
	assertOnlyDirectCause(t, first.Poset, factoryFinish, closing[0])
	assertOnlyDirectCause(t, first.Poset, rootFinish, failure)
	if !first.Poset.IsCausallyIndependent(factoryFinish.ID, rootFinish.ID) {
		t.Fatal("running structural-parent cleanup ordered independent factory/root branches")
	}
	assertCanonicalExecutionReplayAndProcessorEquality(t, model, journal, first, second)
}

func TestNestedArchitectureInitialFailedCreationAbandonsGeneratorChainExactly(t *testing.T) {
	template := `
type Boundary is interface provides Spawn : function(); end interface Boundary;
type Shell is interface end interface Shell;
type Factory is interface
  action out Allocated(value : Factory);
  action out Recovered();
  action out Closing();
  provides Spawn : function();
end interface Factory;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New(Seed is 1)); end function Spawn;
initial (Seed : Integer is 0)
  if Seed = 1 then raise Failure; end if;
MODULE_HANDLER
final Closing();
end module FactoryModule;
architecture Child() return Boundary is
  factory : Factory is FactoryModule();
connect Spawn to factory.Spawn;
initial Spawn();
end architecture Child;
architecture Middle() return Shell is
  child : Boundary is Child();
end architecture Middle;
architecture Root() is
  middle : Shell is Middle();
end architecture Root;
`
	for _, test := range []struct {
		name, handler string
		handled       bool
	}{
		{name: "propagated"},
		{name: "provider recovery", handler: "handler is Failure => Recovered();", handled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(strings.Replace(template, "MODULE_HANDLER", test.handler, 1))
			model, err := Compile(source, "Root")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			journal := arch.NewExecutionJournalWithLimits(
				digest, arch.ExecutionLimits{MaxFirings: 120, MaxStatements: 140},
			)
			previous := runtime.GOMAXPROCS(1)
			first, firstErr := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(8)
			second, secondErr := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(previous)
			if firstErr != nil || secondErr != nil {
				t.Fatalf("nested architecture abandonment=%v/%v", firstErr, secondErr)
			}
			middleArchitecture := lifecycleModuleByOccurrence(t, first, "architecture-instance:middle")
			childInstanceID := arch.DeterministicArchitectureInstanceID("middle", "child")
			childArchitecture := lifecycleModuleByOccurrence(t, first, "architecture-instance:"+childInstanceID)
			root := lifecycleModuleByOccurrence(t, first, "architecture:root")
			var factory, leaf *arch.ModuleLifecycleRecord
			for index := range first.Modules {
				candidate := &first.Modules[index]
				switch {
				case candidate.Kind == "module-generator-result" &&
					candidate.Parent == childArchitecture.ModuleID:
					factory = candidate
				case candidate.Kind == "allocator-module":
					leaf = candidate
				}
			}
			failures := first.Poset.ByName("Failure")
			recovered := first.Poset.ByName("Recovered")
			closing := first.Poset.ByName("Closing")
			if factory == nil || leaf == nil || leaf.Parent != factory.ModuleID ||
				len(failures) != 1 || len(closing) != 1 ||
				(len(recovered) == 1) != test.handled ||
				len(first.Poset.ByName("Allocated")) != 0 ||
				len(first.Poset.ByName("Spawn'Return")) != 0 {
				t.Fatalf("nested architecture failure/recovery/closing/result=%#v/%#v/%d/%d/%d/%d/%d",
					factory, leaf, len(failures), len(recovered), len(closing),
					len(first.Poset.ByName("Allocated")), len(first.Poset.ByName("Spawn'Return")))
			}
			failure := failures[0]
			frontier := failure
			if test.handled {
				frontier = recovered[0]
				assertOnlyDirectCause(t, first.Poset, frontier, failure)
			}
			for _, lifecycle := range []*arch.ModuleLifecycleRecord{
				leaf, factory, childArchitecture, middleArchitecture, root,
			} {
				if lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
					lifecycle.FinishEventID == "" {
					t.Fatalf("nested architecture lifecycle=%#v", lifecycle)
				}
			}
			if leaf.TerminationEventID != string(failure.ID) {
				t.Fatalf("nested failed leaf occurrence=%#v", leaf)
			}
			for _, lifecycle := range []*arch.ModuleLifecycleRecord{
				factory, childArchitecture, middleArchitecture, root,
			} {
				if (lifecycle.TerminationEventID == "") != test.handled {
					t.Fatalf("nested architecture propagated/handled lifecycle=%#v", lifecycle)
				}
				if !test.handled && lifecycle.TerminationEventID != string(failure.ID) {
					t.Fatalf("nested architecture active occurrence=%#v", lifecycle)
				}
			}
			for _, lifecycle := range []*arch.ModuleLifecycleRecord{
				leaf, factory, childArchitecture, middleArchitecture, root,
			} {
				for _, name := range lifecycle.Names {
					if name.Kind != "implicit-self" && name.Live {
						t.Fatalf("nested architecture retained live name=%#v in %#v", name, lifecycle)
					}
				}
			}
			leafFinish, leafFinishExists := first.Poset.Get(gorapide.EventID(leaf.FinishEventID))
			factoryFinish, factoryFinishExists := first.Poset.Get(gorapide.EventID(factory.FinishEventID))
			childFinish, childFinishExists := first.Poset.Get(gorapide.EventID(childArchitecture.FinishEventID))
			middleFinish, middleFinishExists := first.Poset.Get(gorapide.EventID(middleArchitecture.FinishEventID))
			rootFinish, rootFinishExists := first.Poset.Get(gorapide.EventID(root.FinishEventID))
			if !leafFinishExists || !factoryFinishExists || !childFinishExists ||
				!middleFinishExists || !rootFinishExists {
				t.Fatalf("nested architecture Finish events=%#v/%#v/%#v/%#v/%#v",
					leafFinish, factoryFinish, childFinish, middleFinish, rootFinish)
			}
			assertOnlyDirectCause(t, first.Poset, leafFinish, failure)
			assertOnlyDirectCause(t, first.Poset, closing[0], frontier)
			assertOnlyDirectCause(t, first.Poset, factoryFinish, closing[0])
			assertOnlyDirectCause(t, first.Poset, childFinish, frontier)
			assertOnlyDirectCause(t, first.Poset, middleFinish, frontier)
			assertOnlyDirectCause(t, first.Poset, rootFinish, frontier)
			if !first.Poset.IsCausallyIndependent(childFinish.ID, middleFinish.ID) ||
				!first.Poset.IsCausallyIndependent(middleFinish.ID, rootFinish.ID) ||
				!first.Poset.IsCausallyIndependent(factoryFinish.ID, rootFinish.ID) {
				t.Fatal("nested architecture cleanup ordered independent generator branches")
			}
			childFinalization := false
			rootFinalization := false
			for _, firing := range first.Firings {
				for _, generated := range firing.Generated {
					if firing.Target == childInstanceID && generated.EventID == string(childFinish.ID) {
						childFinalization = true
					}
					if firing.Target == arch.ArchitectureInterfaceID && generated.EventID == string(rootFinish.ID) {
						rootFinalization = true
					}
				}
			}
			if !childFinalization || !rootFinalization {
				t.Fatalf("nested architecture finalization audit=%#v", first.Firings)
			}
			assertCanonicalExecutionReplayAndProcessorEquality(t, model, journal, first, second)
		})
	}
}

func TestNestedArchitectureStaticInitialFailedCreationAbandonsGeneratorChainExactly(t *testing.T) {
	template := `
type Boundary is interface end interface Boundary;
type Shell is interface end interface Shell;
type Factory is interface
  action out Allocated(value : Factory);
  action out Wrong();
  action out Recovered();
  action out Closing();
end interface Factory;
module FactoryModule() return Factory is
  exception Failure;
initial (Seed : Integer is 0)
  if Seed = 1 then raise Failure; end if;
  Allocated(New(Seed is 1));
  Wrong();
MODULE_HANDLER
final Closing();
end module FactoryModule;
architecture Child() return Boundary is
  factory : Factory is FactoryModule();
end architecture Child;
architecture Middle() return Shell is
  child : Boundary is Child();
end architecture Middle;
architecture Root() is
  middle : Shell is Middle();
end architecture Root;
`
	for _, test := range []struct {
		name, handler string
		handled       bool
	}{
		{name: "propagated"},
		{name: "static parent recovery", handler: "handler is Failure => Recovered();", handled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(strings.Replace(template, "MODULE_HANDLER", test.handler, 1))
			model, err := Compile(source, "Root")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			journal := arch.NewExecutionJournalWithLimits(
				digest, arch.ExecutionLimits{MaxFirings: 120, MaxStatements: 140},
			)
			previous := runtime.GOMAXPROCS(1)
			first, firstErr := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(8)
			second, secondErr := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(previous)
			if firstErr != nil || secondErr != nil {
				t.Fatalf("nested static-initial architecture abandonment=%v/%v", firstErr, secondErr)
			}
			middleArchitecture := lifecycleModuleByOccurrence(t, first, "architecture-instance:middle")
			childInstanceID := arch.DeterministicArchitectureInstanceID("middle", "child")
			childArchitecture := lifecycleModuleByOccurrence(t, first, "architecture-instance:"+childInstanceID)
			root := lifecycleModuleByOccurrence(t, first, "architecture:root")
			var factory, leaf *arch.ModuleLifecycleRecord
			for index := range first.Modules {
				candidate := &first.Modules[index]
				switch {
				case candidate.Kind == "module-generator-result" &&
					candidate.Parent == childArchitecture.ModuleID:
					factory = candidate
				case candidate.Kind == "allocator-module":
					leaf = candidate
				}
			}
			failures := first.Poset.ByName("Failure")
			recovered := first.Poset.ByName("Recovered")
			closing := first.Poset.ByName("Closing")
			if factory == nil || leaf == nil || leaf.Parent != factory.ModuleID ||
				len(failures) != 1 || (len(recovered) == 1) != test.handled ||
				(len(closing) == 1) != test.handled || len(first.Poset.ByName("Allocated")) != 0 ||
				len(first.Poset.ByName("Wrong")) != 0 {
				t.Fatalf("nested static-initial failure/recovery/closing/result=%#v/%#v/%d/%d/%d/%d/%d",
					factory, leaf, len(failures), len(recovered), len(closing),
					len(first.Poset.ByName("Allocated")), len(first.Poset.ByName("Wrong")))
			}
			failure := failures[0]
			frontier := failure
			if test.handled {
				frontier = recovered[0]
				assertOnlyDirectCause(t, first.Poset, frontier, failure)
			}
			for _, lifecycle := range []*arch.ModuleLifecycleRecord{
				leaf, factory, childArchitecture, middleArchitecture, root,
			} {
				if lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
					lifecycle.FinishEventID == "" {
					t.Fatalf("nested static-initial lifecycle=%#v", lifecycle)
				}
			}
			if leaf.TerminationEventID != string(failure.ID) {
				t.Fatalf("nested static-initial failed leaf=%#v", leaf)
			}
			for _, lifecycle := range []*arch.ModuleLifecycleRecord{
				factory, childArchitecture, middleArchitecture, root,
			} {
				if (lifecycle.TerminationEventID == "") != test.handled {
					t.Fatalf("nested static-initial propagated/handled lifecycle=%#v", lifecycle)
				}
				if !test.handled && lifecycle.TerminationEventID != string(failure.ID) {
					t.Fatalf("nested static-initial active occurrence=%#v", lifecycle)
				}
			}
			leafFinish, leafFinishExists := first.Poset.Get(gorapide.EventID(leaf.FinishEventID))
			factoryFinish, factoryFinishExists := first.Poset.Get(gorapide.EventID(factory.FinishEventID))
			childFinish, childFinishExists := first.Poset.Get(gorapide.EventID(childArchitecture.FinishEventID))
			middleFinish, middleFinishExists := first.Poset.Get(gorapide.EventID(middleArchitecture.FinishEventID))
			rootFinish, rootFinishExists := first.Poset.Get(gorapide.EventID(root.FinishEventID))
			if !leafFinishExists || !factoryFinishExists || !childFinishExists ||
				!middleFinishExists || !rootFinishExists {
				t.Fatalf("nested static-initial Finish events=%#v/%#v/%#v/%#v/%#v",
					leafFinish, factoryFinish, childFinish, middleFinish, rootFinish)
			}
			assertOnlyDirectCause(t, first.Poset, leafFinish, failure)
			if test.handled {
				assertOnlyDirectCause(t, first.Poset, closing[0], frontier)
				assertOnlyDirectCause(t, first.Poset, factoryFinish, closing[0])
			} else {
				assertOnlyDirectCause(t, first.Poset, factoryFinish, failure)
			}
			assertOnlyDirectCause(t, first.Poset, childFinish, frontier)
			assertOnlyDirectCause(t, first.Poset, middleFinish, frontier)
			assertOnlyDirectCause(t, first.Poset, rootFinish, frontier)
			if !first.Poset.IsCausallyIndependent(factoryFinish.ID, childFinish.ID) ||
				!first.Poset.IsCausallyIndependent(childFinish.ID, middleFinish.ID) ||
				!first.Poset.IsCausallyIndependent(childFinish.ID, rootFinish.ID) {
				t.Fatal("nested static-initial cleanup ordered independent generator branches")
			}
			assertCanonicalExecutionReplayAndProcessorEquality(t, model, journal, first, second)
		})
	}
}

func TestArchitectureInitialFailedCreationRetainsIndependentRunningConstituentExactly(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Spawn : function(); end interface Boundary;
type Factory is interface action out Allocated(value : Factory); provides Spawn : function(); end interface Factory;
type Worker is interface action in Ping(); end interface Worker;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New(Seed is 1)); end function Spawn;
initial (Seed : Integer is 0) if Seed = 1 then raise Failure; end if;
end module FactoryModule;
module WorkerModule() return Worker is
serial when Ping() do null; end when;
end module WorkerModule;
architecture Root() return Boundary is
  factory : Factory is FactoryModule();
  worker : Worker is WorkerModule();
connect Spawn to factory.Spawn;
initial Spawn();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 100, MaxStatements: 120},
		arch.InputEvent{Key: "suppressed", Source: "worker", Action: "Ping"},
	)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	worker := lifecycleModuleByOccurrence(t, first, "component:worker")
	root := lifecycleModuleByOccurrence(t, first, "architecture:root")
	if worker.State != arch.ModuleRunningState || worker.Namable ||
		worker.FinishEventID != "" || worker.TerminationEventID != "" ||
		root.State != arch.ModuleFinalizedState || root.FinishEventID == "" {
		t.Fatalf("post-scope idle worker/root lifecycle=%#v/%#v", worker, root)
	}
	componentNameLost := false
	for _, name := range worker.Names {
		if name.Name == "worker" {
			componentNameLost = !name.Live && len(name.LostAfter) != 0
		}
	}
	if !componentNameLost {
		t.Fatalf("post-scope worker retained its architecture name: %#v", worker.Names)
	}
	if len(sourceNamedEvents(first.Poset, "worker", "Ping")) != 0 {
		t.Fatal("journal input entered an architecture generator that returned no value")
	}
	waiting := false
	for _, process := range first.Processes {
		if process.ComponentID == "worker" {
			waiting = !process.Terminated && process.Completion == ""
		}
	}
	if !waiting {
		t.Fatalf("post-scope idle process=%#v", first.Processes)
	}
	assertCanonicalExecutionReplayAndProcessorEquality(t, model, journal, first, second)
}

func TestArchitectureInitialFailedCreationCompletesTimedPostScopeProcessExactly(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Spawn : function(); end interface Boundary;
type Factory is interface action out Allocated(value : Factory); provides Spawn : function(); end interface Factory;
type Worker is interface action out Started(); action out Finished(); action out Closing(); end interface Worker;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New(Seed is 1)); end function Spawn;
initial (Seed : Integer is 0) if Seed = 1 then raise Failure; end if;
end module FactoryModule;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
parallel
  Started();
  Finished() pause C.Ticks(2);
final Closing();
end module WorkerModule;
architecture Root() return Boundary is
  factory : Factory is FactoryModule();
  worker : Worker is WorkerModule();
connect Spawn to factory.Spawn;
initial Spawn();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 120, MaxStatements: 160},
	)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	started := sourceNamedEvents(first.Poset, "worker", "Started")
	finished := sourceNamedEvents(first.Poset, "worker", "Finished")
	worker := lifecycleModuleByOccurrence(t, first, "component:worker")
	closing := sourceNamedEvents(first.Poset, worker.ModuleID, "Closing")
	if len(started) != 1 || len(finished) != 1 || len(closing) != 1 {
		t.Fatalf("post-scope events Started=%d Finished=%d Closing=%d lifecycle=%#v", len(started), len(finished), len(closing), worker)
	}
	if !first.Poset.IsCausallyBefore(started[0].ID, finished[0].ID) {
		t.Fatal("post-scope timed process lost its sequential causal order")
	}
	timing, related := finished[0].Timing(arch.ClockID("worker", "C"))
	if !related || timing.Start != 0 || timing.Finish != 2 {
		t.Fatalf("post-scope timed output=%#v related=%t, want [0,2]", timing, related)
	}
	root := lifecycleModuleByOccurrence(t, first, "architecture:root")
	if worker.State != arch.ModuleFinalizedState || worker.Namable ||
		worker.FinishEventID == "" || worker.TerminationEventID != "" ||
		root.State != arch.ModuleFinalizedState || root.FinishEventID == "" {
		t.Fatalf("post-scope completed worker/root lifecycle=%#v/%#v", worker, root)
	}
	var nameLoss gorapide.EventID
	for _, name := range worker.Names {
		if name.Name == "worker" && !name.Live && len(name.LostAfter) == 1 {
			nameLoss = gorapide.EventID(name.LostAfter[0])
		}
	}
	if nameLoss == "" {
		t.Fatalf("post-scope worker name loss=%#v", worker.Names)
	}
	if !first.Poset.IsCausallyIndependent(nameLoss, finished[0].ID) {
		t.Fatal("scheduler traversal ordered independent name-loss and process-completion branches")
	}
	direct := first.Poset.DirectCauses(closing[0].ID)
	directIDs := make(map[gorapide.EventID]bool, len(direct))
	for _, event := range direct {
		directIDs[event.ID] = true
	}
	if len(directIDs) != 2 || !directIDs[nameLoss] || !directIDs[finished[0].ID] {
		t.Fatalf("post-scope finalization causes=%#v, want name loss plus process completion", direct)
	}
	finish, exists := first.Poset.Event(gorapide.EventID(worker.FinishEventID))
	if !exists {
		t.Fatalf("post-scope worker Finish %q is absent", worker.FinishEventID)
	}
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	rootFinish, exists := first.Poset.Event(gorapide.EventID(root.FinishEventID))
	if !exists {
		t.Fatalf("root Finish %q is absent", root.FinishEventID)
	}
	if !first.Poset.IsCausallyIndependent(finish.ID, rootFinish.ID) {
		t.Fatal("post-scope worker/root finalization branches were falsely ordered")
	}
	terminated := false
	for _, process := range first.Processes {
		if process.ComponentID == "worker" {
			terminated = process.Terminated && process.Completion == "normal"
		}
	}
	if !terminated {
		t.Fatalf("post-scope timed process=%#v", first.Processes)
	}
	assertCanonicalExecutionReplayAndProcessorEquality(t, model, journal, first, second)
}

func TestArchitectureInitialFailedCreationCompletesParallelPostScopeProcessesExactly(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Spawn : function(); end interface Boundary;
type Factory is interface action out Allocated(value : Factory); provides Spawn : function(); end interface Factory;
type Worker is interface action out Left(); action out Right(); action out Closing(); end interface Worker;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New(Seed is 1)); end function Spawn;
initial (Seed : Integer is 0) if Seed = 1 then raise Failure; end if;
end module FactoryModule;
module WorkerModule() return Worker is
parallel
  Left();
||
  Right();
final Closing();
end module WorkerModule;
architecture Root() return Boundary is
  factory : Factory is FactoryModule();
  worker : Worker is WorkerModule();
connect Spawn to factory.Spawn;
initial Spawn();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 120, MaxStatements: 160},
	)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	left := sourceNamedEvents(first.Poset, "worker", "Left")
	right := sourceNamedEvents(first.Poset, "worker", "Right")
	worker := lifecycleModuleByOccurrence(t, first, "component:worker")
	closing := sourceNamedEvents(first.Poset, worker.ModuleID, "Closing")
	if len(left) != 1 || len(right) != 1 || len(closing) != 1 ||
		!first.Poset.IsCausallyIndependent(left[0].ID, right[0].ID) {
		t.Fatalf("parallel post-scope events/lifecycle=%d/%d/%d %#v", len(left), len(right), len(closing), worker)
	}
	if worker.State != arch.ModuleFinalizedState || worker.Namable || worker.FinishEventID == "" {
		t.Fatalf("parallel post-scope worker lifecycle=%#v", worker)
	}
	var nameLoss gorapide.EventID
	for _, name := range worker.Names {
		if name.Name == "worker" && !name.Live && len(name.LostAfter) == 1 {
			nameLoss = gorapide.EventID(name.LostAfter[0])
		}
	}
	direct := first.Poset.DirectCauses(closing[0].ID)
	if nameLoss == "" || len(direct) != 3 ||
		!eventSetContainsID(direct, nameLoss) ||
		!eventSetContainsID(direct, left[0].ID) ||
		!eventSetContainsID(direct, right[0].ID) {
		t.Fatalf("parallel post-scope finalization causes=%#v name-loss=%q", direct, nameLoss)
	}
	terminated := 0
	for _, process := range first.Processes {
		if process.ComponentID == "worker" && process.Terminated && process.Completion == "normal" {
			terminated++
		}
	}
	processChoices := 0
	explorationJournal := journal
	for _, choice := range first.Choices {
		if strings.HasPrefix(choice.Domain, "process-schedule:parallel:worker") {
			processChoices++
			continue
		}
		explorationJournal.Choices = append(explorationJournal.Choices, choice.Decision())
	}
	if terminated != 2 || processChoices != 1 {
		t.Fatalf("parallel post-scope process/choice audit=%#v/%#v", first.Processes, first.Choices)
	}
	assertCanonicalExecutionReplayAndProcessorEquality(t, model, journal, first, second)
	explored, err := model.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 4, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || explored.Executions < 2 || len(explored.Computations) != 1 {
		t.Fatalf("parallel post-scope exploration complete=%v executions=%d computations=%d",
			explored.Complete, explored.Executions, len(explored.Computations))
	}
}

func TestArchitectureInitialFailedCreationContinuesPostScopeModuleConnectionExactly(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Spawn : function(); end interface Boundary;
type Factory is interface action out Allocated(value : Factory); provides Spawn : function(); end interface Factory;
type Worker is interface action out Started(); action out Echo(); action out Seen(); action out Closing(); end interface Worker;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New(Seed is 1)); end function Spawn;
initial (Seed : Integer is 0) if Seed = 1 then raise Failure; end if;
end module FactoryModule;
module WorkerModule() return Worker is
connect Started => Echo;
parallel
  Started();
||
  await Echo => Seen(); end await;
final Closing();
end module WorkerModule;
architecture Root() return Boundary is
  factory : Factory is FactoryModule();
  worker : Worker is WorkerModule();
connect Spawn to factory.Spawn;
initial Spawn();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 100, MaxStatements: 120},
	)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	started := sourceNamedEvents(first.Poset, "worker", "Started")
	echo := sourceNamedEvents(first.Poset, "worker", "Echo")
	seen := sourceNamedEvents(first.Poset, "worker", "Seen")
	worker := lifecycleModuleByOccurrence(t, first, "component:worker")
	closing := sourceNamedEvents(first.Poset, worker.ModuleID, "Closing")
	if len(started) != 1 || len(echo) != 1 || len(seen) != 1 || len(closing) != 1 ||
		!first.Poset.IsCausallyBefore(started[0].ID, echo[0].ID) ||
		!first.Poset.IsCausallyBefore(echo[0].ID, seen[0].ID) {
		t.Fatalf("post-scope module connection events=%d/%d/%d/%d", len(started), len(echo), len(seen), len(closing))
	}
	if worker.State != arch.ModuleFinalizedState || worker.Namable || worker.FinishEventID == "" {
		t.Fatalf("post-scope module-connection lifecycle=%#v", worker)
	}
	var nameLoss gorapide.EventID
	for _, name := range worker.Names {
		if name.Name == "worker" && !name.Live && len(name.LostAfter) == 1 {
			nameLoss = gorapide.EventID(name.LostAfter[0])
		}
	}
	direct := first.Poset.DirectCauses(closing[0].ID)
	if nameLoss == "" || len(direct) != 2 ||
		!eventSetContainsID(direct, nameLoss) || !eventSetContainsID(direct, seen[0].ID) {
		t.Fatalf("post-scope module-controller finalization causes=%#v name-loss=%q", direct, nameLoss)
	}
	moduleFiring := false
	for _, firing := range first.Firings {
		if firing.Transition == "connection" && firing.ConnectionScope == arch.ModuleConnectionScope.String() &&
			firing.TriggerID == string(started[0].ID) && firing.ResultID == string(echo[0].ID) {
			moduleFiring = true
		}
	}
	if !moduleFiring {
		t.Fatalf("post-scope module connection firing=%#v", first.Firings)
	}
	assertCanonicalExecutionReplayAndProcessorEquality(t, model, journal, first, second)
}

func TestArchitectureInitialFailedCreationRoutesPostScopeFinalActionsExactly(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Spawn : function(); end interface Boundary;
type Factory is interface action out Allocated(value : Factory); provides Spawn : function(); end interface Factory;
type Worker is interface
  action out Started(); action out Echo(); action out Seen();
  action out Closing(step : Integer);
  action out Piped(step : Integer);
  action out Agented(step : Integer);
end interface Worker;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New(Seed is 1)); end function Spawn;
initial (Seed : Integer is 0) if Seed = 1 then raise Failure; end if;
end module FactoryModule;
module WorkerModule() return Worker is
connect
  Started => Echo;
  (?N : Integer) Closing(?N) => Piped(?N);
  (?M : Integer) Closing(?M) ||> Agented(?M);
parallel
  Started();
||
  await Echo => Seen(); end await;
final
  Closing(1);
  Closing(2);
end module WorkerModule;
architecture Root() return Boundary is
  factory : Factory is FactoryModule();
  worker : Worker is WorkerModule();
connect Spawn to factory.Spawn;
initial Spawn();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 140, MaxStatements: 180},
	)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}

	worker := lifecycleModuleByOccurrence(t, first, "component:worker")
	seen := sourceNamedEvents(first.Poset, "worker", "Seen")
	byStep := func(name string) map[int]*gorapide.Event {
		indexed := make(map[int]*gorapide.Event, 2)
		for _, event := range sourceNamedEvents(first.Poset, worker.ModuleID, name) {
			indexed[event.ParamInt("step")] = event
		}
		return indexed
	}
	closing := byStep("Closing")
	piped := byStep("Piped")
	agented := byStep("Agented")
	if len(seen) != 1 || len(closing) != 2 || len(piped) != 2 || len(agented) != 2 {
		t.Fatalf(
			"post-scope final route Seen/Closing/Piped/Agented=%d/%d/%d/%d firings=%#v",
			len(seen), len(closing), len(piped), len(agented), first.Firings,
		)
	}
	if worker.State != arch.ModuleFinalizedState || worker.Namable || worker.FinishEventID == "" {
		t.Fatalf("post-scope final-route lifecycle=%#v", worker)
	}
	finish, exists := first.Poset.Get(gorapide.EventID(worker.FinishEventID))
	if !exists || finish == nil {
		t.Fatalf("post-scope final-route Finish %q is absent", worker.FinishEventID)
	}
	var nameLoss gorapide.EventID
	for _, name := range worker.Names {
		if name.Name == "worker" && !name.Live && len(name.LostAfter) == 1 {
			nameLoss = gorapide.EventID(name.LostAfter[0])
		}
	}
	closingOneCauses := first.Poset.DirectCauses(closing[1].ID)
	if nameLoss == "" || len(closingOneCauses) != 2 ||
		!eventSetContainsID(closingOneCauses, nameLoss) ||
		!eventSetContainsID(closingOneCauses, seen[0].ID) {
		t.Fatalf("first final action causes=%#v name-loss=%q", closingOneCauses, nameLoss)
	}
	assertOnlyDirectCause(t, first.Poset, closing[2], closing[1])
	assertOnlyDirectCause(t, first.Poset, piped[1], closing[1])
	assertOnlyDirectCause(t, first.Poset, agented[1], closing[1])
	assertOnlyDirectCause(t, first.Poset, agented[2], closing[2])
	assertOnlyDirectCause(t, first.Poset, finish, closing[2])
	pipedTwoCauses := first.Poset.DirectCauses(piped[2].ID)
	if len(pipedTwoCauses) != 2 ||
		!eventSetContainsID(pipedTwoCauses, closing[2].ID) ||
		!eventSetContainsID(pipedTwoCauses, piped[1].ID) {
		t.Fatalf("second final pipe output causes=%#v", pipedTwoCauses)
	}
	if !first.Poset.IsCausallyBefore(piped[1].ID, piped[2].ID) ||
		!first.Poset.IsCausallyIndependent(agented[1].ID, agented[2].ID) ||
		!first.Poset.IsCausallyIndependent(piped[2].ID, finish.ID) ||
		!first.Poset.IsCausallyIndependent(agented[2].ID, finish.ID) {
		t.Fatal("post-scope final connection routing changed pipe/agent/Finish causality")
	}

	kindCounts := map[string]int{}
	for _, firing := range first.Firings {
		if firing.Transition != "connection" ||
			firing.ConnectionScope != arch.ModuleConnectionScope.String() ||
			firing.TriggerSource != worker.ModuleID || firing.TriggerAction != "Closing" {
			continue
		}
		if firing.Target != worker.ModuleID {
			t.Fatalf("post-scope final route target=%#v", firing)
		}
		kindCounts[firing.ConnectionKind]++
	}
	if kindCounts[arch.PipeConnection.String()] != 2 ||
		kindCounts[arch.AgentConnection.String()] != 2 {
		t.Fatalf("post-scope final route firing kinds=%v", kindCounts)
	}
	assertCanonicalExecutionReplayAndProcessorEquality(t, model, journal, first, second)

	explorationJournal := journal
	for _, choice := range first.Choices {
		if strings.HasPrefix(choice.Domain, "process-schedule:parallel:worker") {
			continue
		}
		explorationJournal.Choices = append(explorationJournal.Choices, choice.Decision())
	}
	limits := arch.ExplorationLimits{MaxExecutions: 4, MaxChoiceDepth: 1}
	explored, err := model.ExploreDeterministic(explorationJournal, limits)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(explorationJournal, limits)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || explored.Executions != 1 ||
		len(explored.Computations) != 1 || !bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf(
			"post-scope final-route exploration complete=%v executions=%d computations=%d",
			explored.Complete, explored.Executions, len(explored.Computations),
		)
	}
}

func TestArchitectureInitialFailedCreationRoutesCompletedSiblingFinalActionsExactly(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Spawn : function(); end interface Boundary;
type Factory is interface action out Allocated(value : Factory); provides Spawn : function(); end interface Factory;
type Worker is interface
  action out Closing(); action out BasicRouted(); action out Routed();
end interface Worker;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New(Seed is 1)); end function Spawn;
initial (Seed : Integer is 0) if Seed = 1 then raise Failure; end if;
end module FactoryModule;
module WorkerModule() return Worker is
connect Closing to BasicRouted; Closing => Routed;
final Closing();
end module WorkerModule;
architecture Root() return Boundary is
  factory : Factory is FactoryModule();
  worker : Worker is WorkerModule();
connect Spawn to factory.Spawn;
initial Spawn();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 100, MaxStatements: 120},
	)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	worker := lifecycleModuleByOccurrence(t, first, "component:worker")
	closing := sourceNamedEvents(first.Poset, worker.ModuleID, "Closing")
	basicRouted := sourceNamedEvents(first.Poset, worker.ModuleID, "BasicRouted")
	routed := sourceNamedEvents(first.Poset, worker.ModuleID, "Routed")
	if len(closing) != 1 || len(basicRouted) != 1 || len(routed) != 1 ||
		basicRouted[0].ID != closing[0].ID ||
		!first.Poset.IsCausallyBefore(closing[0].ID, routed[0].ID) {
		t.Fatalf("completed sibling final Closing/Basic/Routed=%d/%d/%d firings=%#v",
			len(closing), len(basicRouted), len(routed), first.Firings)
	}
	if worker.State != arch.ModuleFinalizedState || worker.Namable || worker.FinishEventID == "" {
		t.Fatalf("completed sibling final-route lifecycle=%#v", worker)
	}
	finish, exists := first.Poset.Get(gorapide.EventID(worker.FinishEventID))
	if !exists || finish == nil {
		t.Fatalf("completed sibling Finish %q is absent", worker.FinishEventID)
	}
	assertOnlyDirectCause(t, first.Poset, routed[0], closing[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	if !first.Poset.IsCausallyIndependent(routed[0].ID, finish.ID) {
		t.Fatal("asynchronous completed-sibling final route was falsely ordered with Finish")
	}
	basicFiring := false
	routedFiring := false
	for _, firing := range first.Firings {
		if firing.Transition == "connection" &&
			firing.ConnectionScope == arch.ModuleConnectionScope.String() &&
			firing.ConnectionKind == arch.BasicConnection.String() &&
			firing.TriggerSource == worker.ModuleID && firing.TriggerAction == "Closing" &&
			firing.Target == worker.ModuleID && firing.ResultID == string(closing[0].ID) {
			basicFiring = true
		}
		if firing.Transition == "connection" &&
			firing.ConnectionScope == arch.ModuleConnectionScope.String() &&
			firing.ConnectionKind == arch.PipeConnection.String() &&
			firing.TriggerSource == worker.ModuleID && firing.TriggerAction == "Closing" &&
			firing.Target == worker.ModuleID && firing.ResultID == string(routed[0].ID) {
			routedFiring = true
		}
	}
	if !basicFiring || !routedFiring {
		t.Fatalf("completed sibling final route firings basic=%v pipe=%v audit=%#v",
			basicFiring, routedFiring, first.Firings)
	}
	assertCanonicalExecutionReplayAndProcessorEquality(t, model, journal, first, second)
}

func TestArchitectureInitialFailedCreationRejectsPostScopeArchitectureFunctionRouteExactly(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Spawn : function(); end interface Boundary;
type Factory is interface action out Allocated(value : Factory); provides Spawn : function(); end interface Factory;
type Worker is interface action out Started(); requires Lookup : function(); end interface Worker;
type Server is interface provides Fetch : function(); end interface Server;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New(Seed is 1)); end function Spawn;
initial (Seed : Integer is 0) if Seed = 1 then raise Failure; end if;
end module FactoryModule;
module WorkerModule() return Worker is
serial
  Started();
  Lookup();
end module WorkerModule;
module ServerModule() return Server is
  Fetch : function() is begin null; end function Fetch;
end module ServerModule;
architecture Root() return Boundary is
  factory : Factory is FactoryModule();
  worker : Worker is WorkerModule();
  server : Server is ServerModule();
connect
  Spawn to factory.Spawn;
  worker.Lookup to server.Fetch;
initial Spawn();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	_, err = model.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 100, MaxStatements: 120},
	))
	if err == nil || !strings.Contains(
		err.Error(),
		`requires an architecture route after its owning architecture scope closed`,
	) {
		t.Fatalf("post-scope architecture function route error=%v", err)
	}
}

func TestArchitectureInitialFailedCreationRunsPostScopeModuleHandlerExactly(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Spawn : function(); end interface Boundary;
type Factory is interface action out Allocated(value : Factory); provides Spawn : function(); end interface Factory;
type Worker is interface action out Started(); action out Recovered(); action out Continued(); action out Closing(); end interface Worker;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New(Seed is 1)); end function Spawn;
initial (Seed : Integer is 0) if Seed = 1 then raise Failure; end if;
end module FactoryModule;
module WorkerModule() return Worker is
  exception Failure;
  C : Clock is Make_Clock();
parallel
  Started();
  pause C.Ticks(1);
  raise Failure;
||
  Continued() pause C.Ticks(2);
handler is Failure => Recovered();
final Closing();
end module WorkerModule;
architecture Root() return Boundary is
  factory : Factory is FactoryModule();
  worker : Worker is WorkerModule();
connect Spawn to factory.Spawn;
initial Spawn();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 120, MaxStatements: 160},
	)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	started := sourceNamedEvents(first.Poset, "worker", "Started")
	failure := sourceNamedEvents(first.Poset, "worker", "Failure")
	recovered := sourceNamedEvents(first.Poset, "worker", "Recovered")
	continued := sourceNamedEvents(first.Poset, "worker", "Continued")
	worker := lifecycleModuleByOccurrence(t, first, "component:worker")
	closing := sourceNamedEvents(first.Poset, worker.ModuleID, "Closing")
	if len(started) != 1 || len(failure) != 1 || len(recovered) != 1 ||
		len(continued) != 1 || len(closing) != 1 {
		t.Fatalf("post-scope handler events Started=%d Failure=%d Recovered=%d Continued=%d Closing=%d lifecycle=%#v",
			len(started), len(failure), len(recovered), len(continued), len(closing), worker)
	}
	if !first.Poset.IsCausallyBefore(started[0].ID, failure[0].ID) {
		t.Fatal("post-scope process exception lost its sequential process frontier")
	}
	assertOnlyDirectCause(t, first.Poset, recovered[0], failure[0])
	if worker.State != arch.ModuleFinalizedState || worker.Namable ||
		worker.TerminationEventID != "" || worker.FinishEventID == "" {
		t.Fatalf("post-scope handled module lifecycle=%#v", worker)
	}
	var nameLoss gorapide.EventID
	for _, name := range worker.Names {
		if name.Name == "worker" && !name.Live && len(name.LostAfter) == 1 {
			nameLoss = gorapide.EventID(name.LostAfter[0])
		}
	}
	direct := first.Poset.DirectCauses(closing[0].ID)
	if nameLoss == "" || len(direct) != 3 ||
		!eventSetContainsID(direct, nameLoss) || !eventSetContainsID(direct, recovered[0].ID) ||
		!eventSetContainsID(direct, continued[0].ID) {
		t.Fatalf("post-scope handled finalization causes=%#v name-loss=%q", direct, nameLoss)
	}
	finish, exists := first.Poset.Event(gorapide.EventID(worker.FinishEventID))
	if !exists {
		t.Fatalf("post-scope handled Finish %q is absent", worker.FinishEventID)
	}
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	for _, propagation := range first.ExceptionPropagations {
		if propagation.ExceptionEventID == string(failure[0].ID) {
			t.Fatalf("handled post-scope exception propagated=%#v", propagation)
		}
	}
	handlerFiring := false
	for _, firing := range first.Firings {
		if firing.Transition == "module-handler" && firing.Target == "worker" &&
			len(firing.MatchedEvents) == 1 && firing.MatchedEvents[0] == string(failure[0].ID) &&
			len(firing.Generated) == 1 && firing.Generated[0].EventID == string(recovered[0].ID) {
			handlerFiring = true
		}
	}
	if !handlerFiring {
		t.Fatalf("post-scope module-handler firing=%#v", first.Firings)
	}
	exceptionCompleted := 0
	normalCompleted := 0
	for _, process := range first.Processes {
		if process.ComponentID == "worker" && process.Terminated && process.Completion == "exception" &&
			process.ExceptionEventID == string(failure[0].ID) {
			exceptionCompleted++
		}
		if process.ComponentID == "worker" && process.Terminated && process.Completion == "normal" {
			normalCompleted++
		}
	}
	if exceptionCompleted != 1 || normalCompleted != 1 {
		t.Fatalf("post-scope handled process audit=%#v", first.Processes)
	}
	assertCanonicalExecutionReplayAndProcessorEquality(t, model, journal, first, second)
}

func TestArchitectureInitialFailedCreationTerminatesOnPostScopeModuleHandlerRaiseExactly(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Spawn : function(); end interface Boundary;
type Factory is interface action out Allocated(value : Factory); provides Spawn : function(); end interface Factory;
type Worker is interface action out Started(); action out Recovered(); action out Closing(); end interface Worker;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New(Seed is 1)); end function Spawn;
initial (Seed : Integer is 0) if Seed = 1 then raise Failure; end if;
end module FactoryModule;
module WorkerModule() return Worker is
  exception Failure;
  exception Escalated;
  C : Clock is Make_Clock();
serial
  Started();
  pause C.Ticks(1);
  raise Failure;
handler
  is Failure => raise Escalated;
  is Escalated => Recovered();
final Closing();
end module WorkerModule;
architecture Root() return Boundary is
  factory : Factory is FactoryModule();
  worker : Worker is WorkerModule();
connect Spawn to factory.Spawn;
initial Spawn();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 120, MaxStatements: 160},
	)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	started := sourceNamedEvents(first.Poset, "worker", "Started")
	failure := sourceNamedEvents(first.Poset, "worker", "Failure")
	escalated := sourceNamedEvents(first.Poset, "worker", "Escalated")
	recovered := sourceNamedEvents(first.Poset, "worker", "Recovered")
	worker := lifecycleModuleByOccurrence(t, first, "component:worker")
	closing := sourceNamedEvents(first.Poset, worker.ModuleID, "Closing")
	if len(started) != 1 || len(failure) != 1 || len(escalated) != 1 ||
		len(recovered) != 0 || len(closing) != 1 {
		t.Fatalf("post-scope handler escape events Started=%d Failure=%d Escalated=%d Recovered=%d Closing=%d",
			len(started), len(failure), len(escalated), len(recovered), len(closing))
	}
	if !first.Poset.IsCausallyBefore(started[0].ID, failure[0].ID) {
		t.Fatal("post-scope handler escape lost its sequential process frontier")
	}
	assertOnlyDirectCause(t, first.Poset, escalated[0], failure[0])
	root := lifecycleModuleByOccurrence(t, first, "architecture:root")
	if worker.State != arch.ModuleFinalizedState || worker.Namable ||
		worker.TerminationEventID != string(escalated[0].ID) || worker.FinishEventID == "" ||
		root.State != arch.ModuleFinalizedState || root.FinishEventID == "" {
		t.Fatalf("post-scope handler escape lifecycle worker=%#v root=%#v", worker, root)
	}
	var nameLoss gorapide.EventID
	for _, name := range worker.Names {
		if name.Name == "worker" && !name.Live && len(name.LostAfter) == 1 {
			nameLoss = gorapide.EventID(name.LostAfter[0])
		}
	}
	direct := first.Poset.DirectCauses(closing[0].ID)
	if nameLoss == "" || len(direct) != 2 ||
		!eventSetContainsID(direct, nameLoss) || !eventSetContainsID(direct, escalated[0].ID) {
		t.Fatalf("post-scope exceptional finalization causes=%#v name-loss=%q", direct, nameLoss)
	}
	finish, exists := first.Poset.Event(gorapide.EventID(worker.FinishEventID))
	if !exists {
		t.Fatalf("post-scope exceptional Finish %q is absent", worker.FinishEventID)
	}
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	rootFinish, exists := first.Poset.Event(gorapide.EventID(root.FinishEventID))
	if !exists || !first.Poset.IsCausallyIndependent(rootFinish.ID, escalated[0].ID) {
		t.Fatalf("finalized parent reacted to later child escape root-finish=%#v escalated=%#v", rootFinish, escalated[0])
	}
	handlerFirings := 0
	for _, firing := range first.Firings {
		if firing.Transition != "module-handler" || firing.Target != "worker" {
			continue
		}
		handlerFirings++
		if len(firing.MatchedEvents) != 1 || firing.MatchedEvents[0] != string(failure[0].ID) ||
			len(firing.Generated) != 1 || firing.Generated[0].EventID != string(escalated[0].ID) ||
			!firing.Generated[0].Exception {
			t.Fatalf("post-scope escaping module-handler firing=%#v", firing)
		}
	}
	if handlerFirings != 1 {
		t.Fatalf("post-scope escaping module-handler firings=%d", handlerFirings)
	}
	foundPropagation := false
	for _, propagation := range first.ExceptionPropagations {
		if propagation.ExceptionEventID != string(escalated[0].ID) ||
			propagation.SourceModuleID != worker.ModuleID {
			continue
		}
		if propagation.SourceComponentID != "worker" || len(propagation.Targets) != 1 ||
			propagation.Targets[0].ModuleID != root.ModuleID ||
			propagation.Targets[0].Disposition != "ignored-finalized" ||
			len(propagation.Targets[0].Relations) != 1 ||
			propagation.Targets[0].Relations[0] != "parent" {
			t.Fatalf("post-scope handler escape propagation=%#v", propagation)
		}
		foundPropagation = true
	}
	if !foundPropagation {
		t.Fatalf("post-scope handler escape propagation is absent: %#v", first.ExceptionPropagations)
	}
	assertCanonicalExecutionReplayAndProcessorEquality(t, model, journal, first, second)
}

func assertCanonicalExecutionReplayAndProcessorEquality(
	t *testing.T,
	model *arch.Architecture,
	journal arch.ExecutionJournal,
	first, second *arch.ExecutionResult,
) {
	t.Helper()
	firstBytes, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed canonical execution bytes")
	}
	expected, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("replay changed canonical execution bytes")
	}
}
