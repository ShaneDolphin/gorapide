package rapide

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceAllocatedModuleInitialRecursivelyAllocatesExactSpecialization(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory; depth : Integer);
  action out Initialized(depth : Integer);
  action out Closing();
  provides Spawn : function(Depth : Integer);
end interface Factory;
type Driver is interface
  action in Trigger(depth : Integer);
  requires Spawn : function(Depth : Integer);
end interface Driver;
type Stimulus is interface action out Trigger(depth : Integer); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function(Depth : Integer) is
  begin
    Allocated(New(Depth is Depth), Depth);
  end function Spawn;
initial (Depth : Integer is 0)
  if Depth > 0 then
    Allocated(New(Depth is Depth - 1), Depth - 1);
  end if;
  Initialized(Depth);
final
  Closing();
end module FactoryModule;

module DriverModule() return Driver is
serial when (?Depth : Integer) Trigger(?Depth) do Spawn(?Depth); end when;
end module DriverModule;

architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  (?Depth : Integer) stimulus.Trigger(?Depth) => driver.Trigger(?Depth);
  driver.Spawn to factory.Spawn;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 160, MaxStatements: 220},
		arch.InputEvent{
			Key: "trigger", Source: "stimulus", Action: "Trigger",
			Params: map[string]any{"depth": 2},
		},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}

	modules := make([]gorapide.RapideModuleValue, 0, 3)
	owner := "factory"
	for depth := 2; depth >= 0; depth-- {
		allocated := sourceNamedEvents(first.Poset, owner, "Allocated")
		if len(allocated) != 1 || allocated[0].ParamInt("depth") != depth {
			t.Fatalf("depth %d allocation from %q=%#v", depth, owner, allocated)
		}
		value, present := allocated[0].Param("value")
		module, ok := value.(gorapide.RapideModuleValue)
		if !present || !ok || module.Identity() == "" {
			t.Fatalf("depth %d allocated module=%#v", depth, value)
		}
		modules = append(modules, module)
		initialized := sourceNamedEvents(first.Poset, module.Identity(), "Initialized")
		if len(initialized) != 1 || initialized[0].ParamInt("depth") != depth {
			t.Fatalf("depth %d initialized=%#v", depth, initialized)
		}
		assertOnlyDirectCause(t, first.Poset, allocated[0], initialized[0])
		owner = module.Identity()
	}
	if modules[0].Identity() == modules[1].Identity() ||
		modules[0].Identity() == modules[2].Identity() ||
		modules[1].Identity() == modules[2].Identity() {
		t.Fatalf("recursive allocator reused identity: %#v", modules)
	}

	lifecycles := make(map[string]arch.ModuleLifecycleRecord)
	for _, lifecycle := range first.Modules {
		lifecycles[lifecycle.ModuleID] = lifecycle
	}
	for index, module := range modules {
		depth := 2 - index
		lifecycle, found := lifecycles[module.Identity()]
		if !found || lifecycle.Kind != "allocator-module" || lifecycle.FinishEventID == "" {
			t.Fatalf("depth %d lifecycle=%#v found=%v", depth, lifecycle, found)
		}
		closing := sourceNamedEvents(first.Poset, module.Identity(), "Closing")
		if len(closing) != 1 {
			t.Fatalf("depth %d closing=%#v", depth, closing)
		}
		finish, ok := first.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
		if !ok {
			t.Fatalf("depth %d missing Finish %q", depth, lifecycle.FinishEventID)
		}
		assertOnlyDirectCause(t, first.Poset, finish, closing[0])
		if index > 0 && lifecycle.Parent != modules[index-1].Identity() {
			t.Fatalf("depth %d parent=%q, want %q", depth, lifecycle.Parent, modules[index-1].Identity())
		}
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
		t.Fatal("GOMAXPROCS changed recursive allocator artifact bytes")
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
		t.Fatal("recursive allocator replay changed artifact bytes")
	}
	explorationJournal := journal
	explorationJournal.Choices = make([]arch.ChoiceDecision, len(first.Choices))
	for index, choice := range first.Choices {
		explorationJournal.Choices[index] = choice.Decision()
	}
	explored, err := model.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || len(explored.Computations) != 1 ||
		!bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("recursive allocator exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourceAllocatedModuleInitialRecursiveAllocationUsesStatementBudget(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
end interface Factory;
module FactoryModule() return Factory is
initial
  Allocated(New());
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 20, MaxStatements: 12},
	)
	previous := runtime.GOMAXPROCS(1)
	_, first := model.ExecuteDeterministic(journal)
	if first == nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal("recursive allocator unexpectedly escaped the explicit statement limit")
	}
	runtime.GOMAXPROCS(8)
	_, second := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if second == nil || !errors.Is(first, arch.ErrExecutionLimit) ||
		first.Error() != second.Error() || !strings.Contains(first.Error(), "max_statements=12") {
		t.Fatalf("recursive allocator limits first=%v second=%v", first, second)
	}
}

func TestSourceAllocatedModuleInitialHandlerInterruptsNestedAllocation(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Born();
  action out Relay();
  action out Deferred();
  action out Recovered();
  action out Done();
  action out Wrong();
  action out Closing();
end interface Factory;
module FactoryModule() return Factory is
  C : Clock is MakeClock();
  Two : C.Ticks is 2;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  if Child then Deferred() in Two; Born(); end if;
  do
    Allocated(New(Child is True));
    Wrong();
  handler
    is Relay => Recovered();
  end do;
  Done();
final
  Closing();
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 80, MaxStatements: 120},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}

	var child arch.ModuleLifecycleRecord
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind != "allocator-module" {
			continue
		}
		if child.ModuleID != "" {
			t.Fatalf("initializer interrupt created multiple children: %#v", first.Modules)
		}
		child = lifecycle
	}
	if child.ModuleID == "" {
		t.Fatalf("initializer interrupt created no child: %#v", first.Modules)
	}
	start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
	relay := sourceNamedEvents(first.Poset, "factory", "Relay")
	closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
	recovered := sourceNamedEvents(first.Poset, "factory", "Recovered")
	done := sourceNamedEvents(first.Poset, "factory", "Done")
	if !startExists || !finishExists || len(born) != 1 || len(relay) != 1 ||
		len(closing) != 1 || len(recovered) != 1 || len(done) != 1 {
		t.Fatalf("initializer interrupt Start/Finish/Born/Relay/Closing/Recovered/Done=%v/%v/%d/%d/%d/%d/%d lifecycle=%#v",
			startExists, finishExists, len(born), len(relay), len(closing), len(recovered), len(done), child)
	}
	if len(sourceNamedEvents(first.Poset, "factory", "Allocated")) != 0 ||
		len(sourceNamedEvents(first.Poset, "factory", "Wrong")) != 0 ||
		len(sourceNamedEvents(first.Poset, child.ModuleID, "Deferred")) != 0 {
		t.Fatal("initializer interrupt returned a module value or executed the abandoned protected remainder")
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) ||
		!first.Poset.IsCausallyIndependent(closing[0].ID, recovered[0].ID) ||
		!first.Poset.IsCausallyIndependent(finish.ID, done[0].ID) {
		t.Fatal("initializer interrupt cleanup acquired a false procedural edge")
	}
	if child.State != arch.ModuleFinalizedState || child.Namable || child.TerminationEventID != "" {
		t.Fatalf("initializer-interrupted child lifecycle=%#v", child)
	}
	selfLoss, allocatorNames := false, 0
	for _, name := range child.Names {
		switch name.Kind {
		case "implicit-self":
			selfLoss = !name.Live && len(name.LostAfter) == 1 && name.LostAfter[0] == string(relay[0].ID)
		case "allocator-result":
			allocatorNames++
		}
	}
	if !selfLoss || allocatorNames != 0 {
		t.Fatalf("initializer-interrupted child name graph self=%t allocator=%d names=%#v",
			selfLoss, allocatorNames, child.Names)
	}
	for _, process := range first.Processes {
		if process.ComponentID == child.ModuleID {
			t.Fatalf("initializer interrupt elaborated child process %#v", process)
		}
	}
	canceled := make([]string, 0)
	for _, firing := range first.Firings {
		canceled = append(canceled, firing.CanceledSchedules...)
	}
	if len(canceled) != 1 || canceled[0] == "" {
		t.Fatalf("initializer-owned interrupt canceled schedules=%#v", canceled)
	}

	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed initializer-owned nested-allocation interrupt bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("initializer-owned nested-allocation interrupt replay changed canonical bytes")
	}
	explorationJournal := journal
	explorationJournal.Choices = make([]arch.ChoiceDecision, len(first.Choices))
	for index, choice := range first.Choices {
		explorationJournal.Choices[index] = choice.Decision()
	}
	explored, err := model.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || len(explored.Computations) != 1 ||
		!bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("initializer-owned nested-allocation exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourceDynamicInitializerHandlerInterruptsGrandchildAllocation(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory; depth : Integer);
  action out Born();
  action out Relay();
  action out Recovered();
  action out Done();
  action out Wrong();
  action out Closing();
  provides Spawn : function(Depth : Integer);
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(Depth : Integer); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  Spawn : function(Depth : Integer) is begin Allocated(New(Depth is Depth), Depth); end function Spawn;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Depth : Integer is -1)
  if Depth = 1 then
    do
      Allocated(New(Depth is 0), 0);
      Wrong();
    handler
      is Relay => Recovered();
    end do;
    Done();
  elsif Depth = 0 then
    Born();
  end if;
final
  Closing();
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(1); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 120, MaxStatements: 160},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}

	allocated := sourceNamedEvents(first.Poset, "factory", "Allocated")
	if len(allocated) != 1 || allocated[0].ParamInt("depth") != 1 {
		t.Fatalf("dynamic handler owner allocation=%#v", allocated)
	}
	outerValue, present := allocated[0].Param("value")
	outer, ok := outerValue.(gorapide.RapideModuleValue)
	if !present || !ok || outer.Identity() == "" {
		t.Fatalf("dynamic handler owner=%#v", outerValue)
	}
	var child arch.ModuleLifecycleRecord
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind == "allocator-module" && lifecycle.Parent == outer.Identity() {
			if child.ModuleID != "" {
				t.Fatalf("dynamic initializer created multiple grandchildren: %#v", first.Modules)
			}
			child = lifecycle
		}
	}
	if child.ModuleID == "" {
		t.Fatalf("dynamic initializer created no grandchild: %#v", first.Modules)
	}
	start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
	relay := sourceNamedEvents(first.Poset, outer.Identity(), "Relay")
	recovered := sourceNamedEvents(first.Poset, outer.Identity(), "Recovered")
	done := sourceNamedEvents(first.Poset, outer.Identity(), "Done")
	closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
	if !startExists || !finishExists || len(born) != 1 || len(relay) != 1 ||
		len(recovered) != 1 || len(done) != 1 || len(closing) != 1 {
		t.Fatalf("dynamic initializer interrupt Start/Finish/Born/Relay/Recovered/Done/Closing=%v/%v/%d/%d/%d/%d/%d child=%#v",
			startExists, finishExists, len(born), len(relay), len(recovered), len(done), len(closing), child)
	}
	if len(sourceNamedEvents(first.Poset, outer.Identity(), "Allocated")) != 0 ||
		len(sourceNamedEvents(first.Poset, outer.Identity(), "Wrong")) != 0 {
		t.Fatal("dynamic initializer handler returned the grandchild or executed its protected remainder")
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) ||
		!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
		t.Fatal("dynamic initializer recovery and abandoned-child cleanup gained a false edge")
	}
	if child.State != arch.ModuleFinalizedState || child.Namable || child.TerminationEventID != "" {
		t.Fatalf("dynamic initializer interrupted grandchild lifecycle=%#v", child)
	}
	for _, process := range first.Processes {
		if process.ComponentID == child.ModuleID {
			t.Fatalf("dynamic initializer interrupted grandchild elaborated process %#v", process)
		}
	}

	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed dynamic-initializer grandchild-interrupt bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("dynamic-initializer grandchild-interrupt replay changed canonical bytes")
	}
}

func TestSourceAllocatedModuleInitialHandlerFunctionAllocationInterruptsExactly(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Born();
  action out Relay();
  action out Recovered();
  action out Done();
  action out CallerWrong();
  action out FunctionWrong();
  action out Closing();
  provides Initialize : function();
end interface Factory;
module FactoryModule() return Factory is
  Initialize : function() is
  begin
    Allocated(New(Child is True));
    FunctionWrong();
  end function Initialize;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  if Child then Born(); end if;
  do
    Initialize();
    CallerWrong();
  handler
    is Relay => Recovered();
  end do;
  Done();
final
  Closing();
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 80, MaxStatements: 120},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	var child arch.ModuleLifecycleRecord
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind == "allocator-module" {
			child = lifecycle
		}
	}
	if child.ModuleID == "" {
		t.Fatalf("initializer function interrupt created no child: %#v", first.Modules)
	}
	start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
	relay := sourceNamedEvents(first.Poset, "factory", "Relay")
	recovered := sourceNamedEvents(first.Poset, "factory", "Recovered")
	done := sourceNamedEvents(first.Poset, "factory", "Done")
	closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
	if !startExists || !finishExists || len(born) != 1 || len(relay) != 1 ||
		len(recovered) != 1 || len(done) != 1 || len(closing) != 1 {
		t.Fatalf("initializer function interrupt Start/Finish/Born/Relay/Recovered/Done/Closing=%v/%v/%d/%d/%d/%d/%d child=%#v",
			startExists, finishExists, len(born), len(relay), len(recovered), len(done), len(closing), child)
	}
	for _, action := range []string{"Allocated", "CallerWrong", "FunctionWrong", "Initialize'Return"} {
		if events := sourceNamedEvents(first.Poset, "factory", action); len(events) != 0 {
			t.Fatalf("initializer function interrupt generated abandoned %s=%#v", action, events)
		}
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) ||
		!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
		t.Fatal("initializer function recovery and child cleanup gained a false edge")
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed initializer function-allocation interrupt bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("initializer function-allocation interrupt replay changed canonical bytes")
	}
}

func TestSourceDynamicInitializerHandlerInterruptsNestedFunctionAllocation(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory; depth : Integer);
  action out Born();
  action out Relay();
  action out Recovered();
  action out Done();
  action out Wrong();
  action out Closing();
  provides Spawn : function(Depth : Integer);
  provides Initialize : function();
  provides Create : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(Depth : Integer); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  Spawn : function(Depth : Integer) is begin Allocated(New(Depth is Depth), Depth); end function Spawn;
  Initialize : function() is begin Create(); Wrong(); end function Initialize;
  Create : function() is begin Allocated(New(Depth is 0), 0); Wrong(); end function Create;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Depth : Integer is -1)
  if Depth = 1 then
    do
      Initialize();
      Wrong();
    handler
      is Relay => Recovered();
    end do;
    Done();
  elsif Depth = 0 then
    Born();
  end if;
final
  Closing();
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(1); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 140, MaxStatements: 200},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	allocated := sourceNamedEvents(first.Poset, "factory", "Allocated")
	if len(allocated) != 1 || allocated[0].ParamInt("depth") != 1 {
		t.Fatalf("dynamic function-handler owner allocation=%#v", allocated)
	}
	outerValue, _ := allocated[0].Param("value")
	outer, ok := outerValue.(gorapide.RapideModuleValue)
	if !ok || outer.Identity() == "" {
		t.Fatalf("dynamic function-handler owner=%#v", outerValue)
	}
	var child arch.ModuleLifecycleRecord
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind == "allocator-module" && lifecycle.Parent == outer.Identity() {
			child = lifecycle
		}
	}
	if child.ModuleID == "" {
		t.Fatalf("dynamic initializer nested function created no grandchild: %#v", first.Modules)
	}
	start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
	relay := sourceNamedEvents(first.Poset, outer.Identity(), "Relay")
	recovered := sourceNamedEvents(first.Poset, outer.Identity(), "Recovered")
	done := sourceNamedEvents(first.Poset, outer.Identity(), "Done")
	closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
	initializeCalls := sourceNamedEvents(first.Poset, outer.Identity(), "Initialize'Call")
	createCalls := sourceNamedEvents(first.Poset, outer.Identity(), "Create'Call")
	if !startExists || !finishExists || len(born) != 1 || len(relay) != 1 ||
		len(recovered) != 1 || len(done) != 1 || len(closing) != 1 ||
		len(initializeCalls) != 1 || len(createCalls) != 1 {
		t.Fatalf("dynamic nested-function interrupt lifecycle/actions are incomplete: child=%#v", child)
	}
	for _, action := range []string{"Allocated", "Wrong", "Initialize'Return", "Create'Return"} {
		if events := sourceNamedEvents(first.Poset, outer.Identity(), action); len(events) != 0 {
			t.Fatalf("dynamic nested-function interrupt generated abandoned %s=%#v", action, events)
		}
	}
	if !first.Poset.IsCausallyBefore(initializeCalls[0].ID, createCalls[0].ID) ||
		!first.Poset.IsCausallyBefore(createCalls[0].ID, start.ID) {
		t.Fatal("dynamic nested-function interrupt lost its synchronous call prefix")
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) ||
		!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
		t.Fatal("dynamic nested-function recovery and cleanup gained a false edge")
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed dynamic nested-function allocation interrupt bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("dynamic nested-function allocation interrupt replay changed canonical bytes")
	}
}

func TestSourceAllocatedModuleInitialFunctionOwnedHandlerRecoversNestedAllocation(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Born();
  action out Relay();
  action out Recovered();
  action out FunctionAfter();
  action out InitialAfter();
  action out Wrong();
  action out Closing();
  provides Initialize : function();
  provides Create : function();
end interface Factory;
module FactoryModule() return Factory is
  Create : function() is begin Allocated(New(Child is True)); Wrong(); end function Create;
  Initialize : function() is
  begin
    do
      Create();
      Wrong();
    handler
      is Relay => Recovered();
    end do;
    FunctionAfter();
  end function Initialize;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  if Child then Born(); end if;
  Initialize();
  InitialAfter();
final
  Closing();
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 100, MaxStatements: 140},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	var child arch.ModuleLifecycleRecord
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind == "allocator-module" {
			child = lifecycle
		}
	}
	if child.ModuleID == "" {
		t.Fatalf("function-owned handler created no child: %#v", first.Modules)
	}
	start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
	relay := sourceNamedEvents(first.Poset, "factory", "Relay")
	recovered := sourceNamedEvents(first.Poset, "factory", "Recovered")
	functionAfter := sourceNamedEvents(first.Poset, "factory", "FunctionAfter")
	initializeReturn := sourceNamedEvents(first.Poset, "factory", "Initialize'Return")
	initialAfter := sourceNamedEvents(first.Poset, "factory", "InitialAfter")
	closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
	if !startExists || !finishExists || len(born) != 1 || len(relay) != 1 ||
		len(recovered) != 1 || len(functionAfter) != 1 || len(initializeReturn) != 1 ||
		len(initialAfter) != 1 || len(closing) != 1 {
		t.Fatalf("function-owned handler recovery lifecycle/actions are incomplete: child=%#v", child)
	}
	for _, action := range []string{"Allocated", "Wrong", "Create'Return"} {
		if events := sourceNamedEvents(first.Poset, "factory", action); len(events) != 0 {
			t.Fatalf("function-owned handler generated abandoned %s=%#v", action, events)
		}
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, functionAfter[0].ID) ||
		!first.Poset.IsCausallyBefore(functionAfter[0].ID, initializeReturn[0].ID) ||
		!first.Poset.IsCausallyBefore(initializeReturn[0].ID, initialAfter[0].ID) ||
		!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
		t.Fatal("function-owned handler recovery/Return/cleanup causality is incorrect")
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed function-owned handler allocation bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("function-owned handler allocation replay changed canonical bytes")
	}
}

func TestSourceDynamicInitializerFunctionOwnedHandlerRecoversGrandchildAllocation(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory; depth : Integer);
  action out Born();
  action out Relay();
  action out Recovered();
  action out FunctionAfter();
  action out InitialAfter();
  action out Wrong();
  action out Closing();
  provides Spawn : function(Depth : Integer);
  provides Initialize : function();
  provides Create : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(Depth : Integer); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  Spawn : function(Depth : Integer) is begin Allocated(New(Depth is Depth), Depth); end function Spawn;
  Create : function() is begin Allocated(New(Depth is 0), 0); Wrong(); end function Create;
  Initialize : function() is
  begin
    do
      Create();
      Wrong();
    handler
      is Relay => Recovered();
    end do;
    FunctionAfter();
  end function Initialize;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Depth : Integer is -1)
  if Depth = 1 then
    Initialize();
    InitialAfter();
  elsif Depth = 0 then
    Born();
  end if;
final
  Closing();
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(1); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 140, MaxStatements: 200},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	allocated := sourceNamedEvents(first.Poset, "factory", "Allocated")
	if len(allocated) != 1 || allocated[0].ParamInt("depth") != 1 {
		t.Fatalf("dynamic function-owned handler allocation=%#v", allocated)
	}
	outerValue, _ := allocated[0].Param("value")
	outer, ok := outerValue.(gorapide.RapideModuleValue)
	if !ok || outer.Identity() == "" {
		t.Fatalf("dynamic function-owned handler owner=%#v", outerValue)
	}
	var child arch.ModuleLifecycleRecord
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind == "allocator-module" && lifecycle.Parent == outer.Identity() {
			child = lifecycle
		}
	}
	if child.ModuleID == "" {
		t.Fatalf("dynamic function-owned handler created no grandchild: %#v", first.Modules)
	}
	start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
	relay := sourceNamedEvents(first.Poset, outer.Identity(), "Relay")
	recovered := sourceNamedEvents(first.Poset, outer.Identity(), "Recovered")
	functionAfter := sourceNamedEvents(first.Poset, outer.Identity(), "FunctionAfter")
	initializeReturn := sourceNamedEvents(first.Poset, outer.Identity(), "Initialize'Return")
	initialAfter := sourceNamedEvents(first.Poset, outer.Identity(), "InitialAfter")
	closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
	initializeCalls := sourceNamedEvents(first.Poset, outer.Identity(), "Initialize'Call")
	createCalls := sourceNamedEvents(first.Poset, outer.Identity(), "Create'Call")
	if !startExists || !finishExists || len(born) != 1 || len(relay) != 1 ||
		len(recovered) != 1 || len(functionAfter) != 1 || len(initializeReturn) != 1 ||
		len(initialAfter) != 1 || len(closing) != 1 || len(initializeCalls) != 1 ||
		len(createCalls) != 1 {
		t.Fatalf("dynamic function-owned handler recovery lifecycle/actions are incomplete: child=%#v", child)
	}
	for _, action := range []string{"Allocated", "Wrong", "Create'Return"} {
		if events := sourceNamedEvents(first.Poset, outer.Identity(), action); len(events) != 0 {
			t.Fatalf("dynamic function-owned handler generated abandoned %s=%#v", action, events)
		}
	}
	if !first.Poset.IsCausallyBefore(initializeCalls[0].ID, createCalls[0].ID) ||
		!first.Poset.IsCausallyBefore(createCalls[0].ID, start.ID) {
		t.Fatal("dynamic function-owned handler lost its synchronous call prefix")
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, functionAfter[0].ID) ||
		!first.Poset.IsCausallyBefore(functionAfter[0].ID, initializeReturn[0].ID) ||
		!first.Poset.IsCausallyBefore(initializeReturn[0].ID, initialAfter[0].ID) ||
		!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
		t.Fatal("dynamic function-owned handler recovery/Return/cleanup causality is incorrect")
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed dynamic function-owned handler allocation bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("dynamic function-owned handler allocation replay changed canonical bytes")
	}
}

func TestSourceAllocatedModuleInitialFunctionOwnedHandlerDirectAllocationRecovers(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Born();
  action out Relay();
  action out Recovered();
  action out FunctionAfter();
  action out InitialAfter();
  action out Wrong();
  action out Closing();
  provides Initialize : function();
end interface Factory;
module FactoryModule() return Factory is
  Initialize : function() is
  begin
    do
      Allocated(New(Child is True));
      Wrong();
    handler
      is Relay => Recovered();
    end do;
    FunctionAfter();
  end function Initialize;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  if Child then Born(); end if;
  Initialize();
  InitialAfter();
final
  Closing();
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 100, MaxStatements: 140},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	var child arch.ModuleLifecycleRecord
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind == "allocator-module" {
			child = lifecycle
		}
	}
	if child.ModuleID == "" {
		t.Fatalf("direct function-owned handler created no child: %#v", first.Modules)
	}
	start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	initializeCall := sourceNamedEvents(first.Poset, "factory", "Initialize'Call")
	born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
	relay := sourceNamedEvents(first.Poset, "factory", "Relay")
	recovered := sourceNamedEvents(first.Poset, "factory", "Recovered")
	functionAfter := sourceNamedEvents(first.Poset, "factory", "FunctionAfter")
	initializeReturn := sourceNamedEvents(first.Poset, "factory", "Initialize'Return")
	initialAfter := sourceNamedEvents(first.Poset, "factory", "InitialAfter")
	closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
	if !startExists || !finishExists || len(initializeCall) != 1 || len(born) != 1 ||
		len(relay) != 1 || len(recovered) != 1 || len(functionAfter) != 1 ||
		len(initializeReturn) != 1 || len(initialAfter) != 1 || len(closing) != 1 {
		t.Fatalf("direct function-owned handler recovery lifecycle/actions are incomplete: child=%#v", child)
	}
	for _, action := range []string{"Allocated", "Wrong"} {
		if events := sourceNamedEvents(first.Poset, "factory", action); len(events) != 0 {
			t.Fatalf("direct function-owned handler generated abandoned %s=%#v", action, events)
		}
	}
	if !first.Poset.IsCausallyBefore(initializeCall[0].ID, start.ID) {
		t.Fatal("direct function-owned handler lost its Call/Start prefix")
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, functionAfter[0].ID) ||
		!first.Poset.IsCausallyBefore(functionAfter[0].ID, initializeReturn[0].ID) ||
		!first.Poset.IsCausallyBefore(initializeReturn[0].ID, initialAfter[0].ID) ||
		!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
		t.Fatal("direct function-owned handler recovery/Return/cleanup causality is incorrect")
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed direct function-owned handler allocation bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("direct function-owned handler allocation replay changed canonical bytes")
	}
}

func TestSourceDynamicInitializerFunctionOwnedHandlerDirectAllocationRecovers(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory; depth : Integer);
  action out Born();
  action out Relay();
  action out Recovered();
  action out FunctionAfter();
  action out InitialAfter();
  action out Wrong();
  action out Closing();
  provides Spawn : function(Depth : Integer);
  provides Initialize : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(Depth : Integer); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  Spawn : function(Depth : Integer) is begin Allocated(New(Depth is Depth), Depth); end function Spawn;
  Initialize : function() is
  begin
    do
      Allocated(New(Depth is 0), 0);
      Wrong();
    handler
      is Relay => Recovered();
    end do;
    FunctionAfter();
  end function Initialize;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Depth : Integer is -1)
  if Depth = 1 then
    Initialize();
    InitialAfter();
  elsif Depth = 0 then
    Born();
  end if;
final
  Closing();
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(1); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 140, MaxStatements: 200},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	allocated := sourceNamedEvents(first.Poset, "factory", "Allocated")
	if len(allocated) != 1 || allocated[0].ParamInt("depth") != 1 {
		t.Fatalf("dynamic direct function-owned handler allocation=%#v", allocated)
	}
	outerValue, _ := allocated[0].Param("value")
	outer, ok := outerValue.(gorapide.RapideModuleValue)
	if !ok || outer.Identity() == "" {
		t.Fatalf("dynamic direct function-owned handler owner=%#v", outerValue)
	}
	var child arch.ModuleLifecycleRecord
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind == "allocator-module" && lifecycle.Parent == outer.Identity() {
			child = lifecycle
		}
	}
	if child.ModuleID == "" {
		t.Fatalf("dynamic direct function-owned handler created no grandchild: %#v", first.Modules)
	}
	start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	initializeCall := sourceNamedEvents(first.Poset, outer.Identity(), "Initialize'Call")
	born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
	relay := sourceNamedEvents(first.Poset, outer.Identity(), "Relay")
	recovered := sourceNamedEvents(first.Poset, outer.Identity(), "Recovered")
	functionAfter := sourceNamedEvents(first.Poset, outer.Identity(), "FunctionAfter")
	initializeReturn := sourceNamedEvents(first.Poset, outer.Identity(), "Initialize'Return")
	initialAfter := sourceNamedEvents(first.Poset, outer.Identity(), "InitialAfter")
	closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
	if !startExists || !finishExists || len(initializeCall) != 1 || len(born) != 1 ||
		len(relay) != 1 || len(recovered) != 1 || len(functionAfter) != 1 ||
		len(initializeReturn) != 1 || len(initialAfter) != 1 || len(closing) != 1 {
		t.Fatalf("dynamic direct function-owned recovery lifecycle/actions are incomplete: child=%#v", child)
	}
	for _, action := range []string{"Allocated", "Wrong"} {
		if events := sourceNamedEvents(first.Poset, outer.Identity(), action); len(events) != 0 {
			t.Fatalf("dynamic direct function-owned handler generated abandoned %s=%#v", action, events)
		}
	}
	if !first.Poset.IsCausallyBefore(initializeCall[0].ID, start.ID) {
		t.Fatal("dynamic direct function-owned handler lost its Call/Start prefix")
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, functionAfter[0].ID) ||
		!first.Poset.IsCausallyBefore(functionAfter[0].ID, initializeReturn[0].ID) ||
		!first.Poset.IsCausallyBefore(initializeReturn[0].ID, initialAfter[0].ID) ||
		!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
		t.Fatal("dynamic direct function-owned recovery/Return/cleanup causality is incorrect")
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed dynamic direct function-owned handler bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("dynamic direct function-owned handler replay changed canonical bytes")
	}
}

func TestSourceProcessFunctionOwnedHandlerDirectAllocationRecovers(t *testing.T) {
	source := []byte(`
type Factory is interface
  action in Trigger();
  action out Allocated(value : Factory);
  action out Born();
  action out Relay();
  action out Recovered();
  action out FunctionAfter();
  action out ProcessAfter();
  action out Wrong();
  action out Closing();
  provides Initialize : function();
end interface Factory;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  Initialize : function() is
  begin
    do
      Allocated(New(Child is True));
      Wrong();
    handler
      is Relay => Recovered();
    end do;
    FunctionAfter();
  end function Initialize;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  if Child then Born(); end if;
serial when Trigger() do Initialize(); ProcessAfter(); end when;
final
  Closing();
end module FactoryModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
connect stimulus.Trigger => factory.Trigger;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 40, MaxStatements: 60},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	var child arch.ModuleLifecycleRecord
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind == "allocator-module" {
			child = lifecycle
		}
	}
	if child.ModuleID == "" {
		t.Fatalf("process function-owned handler created no child: %#v", first.Modules)
	}
	start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	initializeCall := sourceNamedEvents(first.Poset, "factory", "Initialize'Call")
	born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
	relay := sourceNamedEvents(first.Poset, "factory", "Relay")
	recovered := sourceNamedEvents(first.Poset, "factory", "Recovered")
	functionAfter := sourceNamedEvents(first.Poset, "factory", "FunctionAfter")
	initializeReturn := sourceNamedEvents(first.Poset, "factory", "Initialize'Return")
	processAfter := sourceNamedEvents(first.Poset, "factory", "ProcessAfter")
	closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
	if !startExists || !finishExists || len(initializeCall) != 1 || len(born) != 1 ||
		len(relay) != 1 || len(recovered) != 1 || len(functionAfter) != 1 ||
		len(initializeReturn) != 1 || len(processAfter) != 1 || len(closing) != 1 {
		t.Fatalf("process function-owned recovery lifecycle/actions are incomplete: child=%#v", child)
	}
	for _, action := range []string{"Allocated", "Wrong"} {
		if events := sourceNamedEvents(first.Poset, "factory", action); len(events) != 0 {
			t.Fatalf("process function-owned handler generated abandoned %s=%#v", action, events)
		}
	}
	if !first.Poset.IsCausallyBefore(initializeCall[0].ID, start.ID) {
		t.Fatal("process function-owned handler lost its Call/Start prefix")
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, functionAfter[0].ID) ||
		!first.Poset.IsCausallyBefore(functionAfter[0].ID, initializeReturn[0].ID) ||
		!first.Poset.IsCausallyBefore(initializeReturn[0].ID, processAfter[0].ID) ||
		!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
		t.Fatal("process function-owned recovery/Return/cleanup causality is incorrect")
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed process function-owned handler bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("process function-owned handler replay changed canonical bytes")
	}
}

func TestSourceProcessFunctionOwnedHandlerRecoversNestedAllocation(t *testing.T) {
	source := []byte(`
type Factory is interface
  action in Trigger();
  action out Allocated(value : Factory);
  action out Born();
  action out Relay();
  action out Recovered();
  action out FunctionAfter();
  action out ProcessAfter();
  action out Wrong();
  action out Closing();
  provides Initialize : function();
  provides Create : function();
end interface Factory;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  Create : function() is begin Allocated(New(Child is True)); Wrong(); end function Create;
  Initialize : function() is
  begin
    do
      Create();
      Wrong();
    handler
      is Relay => Recovered();
    end do;
    FunctionAfter();
  end function Initialize;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  if Child then Born(); end if;
serial when Trigger() do Initialize(); ProcessAfter(); end when;
final
  Closing();
end module FactoryModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
connect stimulus.Trigger => factory.Trigger;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 60, MaxStatements: 90},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	var child arch.ModuleLifecycleRecord
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind == "allocator-module" {
			child = lifecycle
		}
	}
	if child.ModuleID == "" {
		t.Fatalf("process nested function-owned handler created no child: %#v", first.Modules)
	}
	start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	initializeCall := sourceNamedEvents(first.Poset, "factory", "Initialize'Call")
	createCall := sourceNamedEvents(first.Poset, "factory", "Create'Call")
	born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
	relay := sourceNamedEvents(first.Poset, "factory", "Relay")
	recovered := sourceNamedEvents(first.Poset, "factory", "Recovered")
	functionAfter := sourceNamedEvents(first.Poset, "factory", "FunctionAfter")
	initializeReturn := sourceNamedEvents(first.Poset, "factory", "Initialize'Return")
	processAfter := sourceNamedEvents(first.Poset, "factory", "ProcessAfter")
	closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
	if !startExists || !finishExists || len(initializeCall) != 1 || len(createCall) != 1 ||
		len(born) != 1 || len(relay) != 1 || len(recovered) != 1 ||
		len(functionAfter) != 1 || len(initializeReturn) != 1 || len(processAfter) != 1 ||
		len(closing) != 1 {
		t.Fatalf("process nested function-owned recovery lifecycle/actions are incomplete: child=%#v", child)
	}
	for _, action := range []string{"Allocated", "Wrong", "Create'Return"} {
		if events := sourceNamedEvents(first.Poset, "factory", action); len(events) != 0 {
			t.Fatalf("process nested function-owned handler generated abandoned %s=%#v", action, events)
		}
	}
	if !first.Poset.IsCausallyBefore(initializeCall[0].ID, createCall[0].ID) ||
		!first.Poset.IsCausallyBefore(createCall[0].ID, start.ID) {
		t.Fatal("process nested function-owned handler lost its Call/Start prefix")
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, functionAfter[0].ID) ||
		!first.Poset.IsCausallyBefore(functionAfter[0].ID, initializeReturn[0].ID) ||
		!first.Poset.IsCausallyBefore(initializeReturn[0].ID, processAfter[0].ID) ||
		!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
		t.Fatal("process nested function-owned recovery/Return/cleanup causality is incorrect")
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed process nested function-owned handler bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("process nested function-owned handler replay changed canonical bytes")
	}
}

func TestSourceArchitectureInitialFunctionOwnedHandlerAllocationRecoversDirectAndNested(t *testing.T) {
	tests := []struct {
		name, protected string
		nested          bool
	}{
		{name: "direct", protected: "Allocated(New(Child is True)); Wrong();"},
		{name: "nested", protected: "Create(); Wrong();", nested: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Boundary is interface
  action out ArchitectureAfter();
  provides Begin : function();
end interface Boundary;
type Factory is interface
  action out Allocated(value : Factory);
  action out Born();
  action out Relay();
  action out Recovered();
  action out FunctionAfter();
  action out Wrong();
  action out Closing();
  provides Initialize : function();
  provides Create : function();
end interface Factory;
module FactoryModule() return Factory is
  Create : function() is begin Allocated(New(Child is True)); Wrong(); end function Create;
  Initialize : function() is
  begin
    do
      ` + test.protected + `
    handler
      is Relay => Recovered();
    end do;
    FunctionAfter();
  end function Initialize;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  if Child then Born(); end if;
final
  Closing();
end module FactoryModule;
architecture System() return Boundary is
  factory : Factory is FactoryModule();
connect
  Begin to factory.Initialize;
initial
  Begin();
  ArchitectureAfter();
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
			journal := arch.NewExecutionJournalWithLimits(
				digest, arch.ExecutionLimits{MaxFirings: 40, MaxStatements: 60},
			)
			previous := runtime.GOMAXPROCS(1)
			first, err := model.ExecuteDeterministic(journal)
			if err != nil {
				runtime.GOMAXPROCS(previous)
				t.Fatal(err)
			}
			runtime.GOMAXPROCS(8)
			second, secondErr := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(previous)
			if secondErr != nil {
				t.Fatal(secondErr)
			}
			var providerModule arch.ModuleLifecycleRecord
			for _, lifecycle := range first.Modules {
				if lifecycle.Occurrence == "component:factory" {
					providerModule = lifecycle
				}
			}
			if providerModule.ModuleID == "" {
				t.Fatalf("architecture-initial recovery has no provider module: %#v", first.Modules)
			}
			var child arch.ModuleLifecycleRecord
			for _, lifecycle := range first.Modules {
				if lifecycle.Kind == "allocator-module" && lifecycle.Parent == providerModule.ModuleID {
					child = lifecycle
				}
			}
			if child.ModuleID == "" {
				t.Fatalf("architecture-initial provider handler child ownership=%#v", child)
			}
			start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
			finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
			boundaryCall := functionBoundaryEvents(first.Poset, arch.ArchitectureInterfaceID, "Begin'Call")
			providerCall := functionBoundaryEvents(first.Poset, "factory", "Initialize'Call")
			born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
			relay := sourceNamedEvents(first.Poset, "factory", "Relay")
			recovered := sourceNamedEvents(first.Poset, "factory", "Recovered")
			functionAfter := sourceNamedEvents(first.Poset, "factory", "FunctionAfter")
			providerReturn := functionBoundaryEvents(first.Poset, "factory", "Initialize'Return")
			boundaryReturn := functionBoundaryEvents(first.Poset, arch.ArchitectureInterfaceID, "Begin'Return")
			architectureAfter := sourceNamedEvents(first.Poset, arch.ArchitectureInterfaceID, "ArchitectureAfter")
			closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
			createCall := sourceNamedEvents(first.Poset, "factory", "Create'Call")
			if !startExists || !finishExists || len(boundaryCall) != 1 || len(providerCall) != 1 ||
				len(born) != 1 || len(relay) != 1 || len(recovered) != 1 || len(functionAfter) != 1 ||
				len(providerReturn) != 1 || len(boundaryReturn) != 1 || len(architectureAfter) != 1 ||
				len(closing) != 1 {
				t.Fatalf("architecture-initial provider recovery lifecycle/actions are incomplete: child=%#v", child)
			}
			if boundaryCall[0].ID != providerCall[0].ID || boundaryReturn[0].ID != providerReturn[0].ID {
				t.Fatal("architecture/provider recovery duplicated shared Call or Return occurrences")
			}
			if test.nested {
				if len(createCall) != 1 || !first.Poset.IsCausallyBefore(providerCall[0].ID, createCall[0].ID) ||
					!first.Poset.IsCausallyBefore(createCall[0].ID, start.ID) {
					t.Fatalf("nested architecture-initial provider call/start prefix=%#v", createCall)
				}
				if events := sourceNamedEvents(first.Poset, "factory", "Create'Return"); len(events) != 0 {
					t.Fatalf("nested architecture-initial provider emitted abandoned Create'Return=%#v", events)
				}
			} else if len(createCall) != 0 {
				t.Fatalf("direct architecture-initial provider unexpectedly called Create=%#v", createCall)
			}
			for _, action := range []string{"Allocated", "Wrong"} {
				if events := sourceNamedEvents(first.Poset, "factory", action); len(events) != 0 {
					t.Fatalf("architecture-initial provider handler generated abandoned %s=%#v", action, events)
				}
			}
			if !first.Poset.IsCausallyBefore(boundaryCall[0].ID, start.ID) {
				t.Fatal("architecture-initial provider handler lost its Call/Start prefix")
			}
			assertOnlyDirectCause(t, first.Poset, born[0], start)
			assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
			assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
			assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
			assertOnlyDirectCause(t, first.Poset, finish, closing[0])
			if !first.Poset.IsCausallyBefore(recovered[0].ID, functionAfter[0].ID) ||
				!first.Poset.IsCausallyBefore(functionAfter[0].ID, providerReturn[0].ID) ||
				!first.Poset.IsCausallyBefore(providerReturn[0].ID, architectureAfter[0].ID) ||
				!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
				t.Fatal("architecture-initial provider recovery/Return/cleanup causality is incorrect")
			}
			firstBytes, _ := first.MarshalCanonical()
			secondBytes, _ := second.MarshalCanonical()
			if !bytes.Equal(firstBytes, secondBytes) {
				t.Fatal("GOMAXPROCS changed architecture-initial provider handler bytes")
			}
			expected, _ := first.ArtifactDigest()
			replayed, err := model.ReplayDeterministic(journal, expected)
			if err != nil {
				t.Fatal(err)
			}
			replayedBytes, _ := replayed.MarshalCanonical()
			if !bytes.Equal(firstBytes, replayedBytes) {
				t.Fatal("architecture-initial provider handler replay changed canonical bytes")
			}
		})
	}
}

func TestSourceArchitectureInitialFunctionOwnedHandlerRetainsSecondProviderHopGate(t *testing.T) {
	source := []byte(`
type Boundary is interface provides Begin : function(); end interface Boundary;
type Gateway is interface
  provides Initialize : function();
  requires Create : function();
end interface Gateway;
type Factory is interface
  action out Allocated(value : Factory);
  action out Born();
  action out Relay();
  action out Recovered();
  provides Create : function();
end interface Factory;
module GatewayModule() return Gateway is
  Initialize : function() is begin Create(); end function Initialize;
end module GatewayModule;
module FactoryModule() return Factory is
  Create : function() is
  begin
    do
      Allocated(New(Child is True));
    handler
      is Relay => Recovered();
    end do;
  end function Create;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  if Child then Born(); end if;
end module FactoryModule;
architecture System() return Boundary is
  gateway : Gateway is GatewayModule();
  factory : Factory is FactoryModule();
connect
  Begin to gateway.Initialize;
  gateway.Create to factory.Create;
initial
  Begin();
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 30, MaxStatements: 40},
	))
	if err == nil || !strings.Contains(err.Error(), "direct architecture-initial connected-provider activation") {
		t.Fatalf("architecture-initial second provider hop boundary result=%#v err=%v", result, err)
	}
}

func TestSourceProcessGeneralForFunctionOwnedHandlerAllocationRecoversEveryControlPhase(t *testing.T) {
	tests := []struct {
		name, loop string
		loopBody   int
	}{
		{name: "initializer", loop: "for Control() in False next False do Wrong(); end for;"},
		{name: "test", loop: "for False in Control() next False do Wrong(); end for;"},
		{name: "next", loop: "for False in $keep next Control() do LoopBody(); end for;", loopBody: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Factory is interface
  action in Trigger();
  action out Allocated(value : Factory);
  action out Born();
  action out Relay();
  action out Recovered();
  action out FunctionAfter();
  action out ProcessAfter();
  action out LoopBody();
  action out Wrong();
  action out Closing();
  provides Control : function() return Boolean;
end interface Factory;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  keep : var Boolean := True;
  Control : function() return Boolean is
  begin
    do
      Allocated(New(Child is True));
      Wrong();
    handler
      is Relay => Recovered();
    end do;
    keep := False;
    FunctionAfter();
    return False;
  end function Control;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  if Child then Born(); end if;
serial when Trigger() do
  ` + test.loop + `
  ProcessAfter();
end when;
final
  Closing();
end module FactoryModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
connect stimulus.Trigger => factory.Trigger;
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
			journal := arch.NewExecutionJournalWithLimits(
				digest, arch.ExecutionLimits{MaxFirings: 80, MaxStatements: 120},
				arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
			)
			previous := runtime.GOMAXPROCS(1)
			first, err := model.ExecuteDeterministic(journal)
			if err != nil {
				runtime.GOMAXPROCS(previous)
				t.Fatal(err)
			}
			runtime.GOMAXPROCS(8)
			second, secondErr := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(previous)
			if secondErr != nil {
				t.Fatal(secondErr)
			}
			var child arch.ModuleLifecycleRecord
			for _, lifecycle := range first.Modules {
				if lifecycle.Kind == "allocator-module" {
					child = lifecycle
				}
			}
			if child.ModuleID == "" {
				t.Fatalf("general-for %s function-owned handler created no child: %#v", test.name, first.Modules)
			}
			start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
			finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
			controlCall := sourceNamedEvents(first.Poset, "factory", "Control'Call")
			born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
			relay := sourceNamedEvents(first.Poset, "factory", "Relay")
			recovered := sourceNamedEvents(first.Poset, "factory", "Recovered")
			functionAfter := sourceNamedEvents(first.Poset, "factory", "FunctionAfter")
			controlReturn := sourceNamedEvents(first.Poset, "factory", "Control'Return")
			processAfter := sourceNamedEvents(first.Poset, "factory", "ProcessAfter")
			loopBody := sourceNamedEvents(first.Poset, "factory", "LoopBody")
			closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
			if !startExists || !finishExists || len(controlCall) != 1 || len(born) != 1 ||
				len(relay) != 1 || len(recovered) != 1 || len(functionAfter) != 1 ||
				len(controlReturn) != 1 || len(processAfter) != 1 || len(loopBody) != test.loopBody ||
				len(closing) != 1 {
				t.Fatalf("general-for %s function-owned lifecycle/actions are incomplete: child=%#v", test.name, child)
			}
			for _, action := range []string{"Allocated", "Wrong"} {
				if events := sourceNamedEvents(first.Poset, "factory", action); len(events) != 0 {
					t.Fatalf("general-for %s generated abandoned %s=%#v", test.name, action, events)
				}
			}
			if !first.Poset.IsCausallyBefore(controlCall[0].ID, start.ID) {
				t.Fatalf("general-for %s lost its Call/Start prefix", test.name)
			}
			assertOnlyDirectCause(t, first.Poset, born[0], start)
			assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
			assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
			assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
			assertOnlyDirectCause(t, first.Poset, finish, closing[0])
			if !first.Poset.IsCausallyBefore(recovered[0].ID, functionAfter[0].ID) ||
				!first.Poset.IsCausallyBefore(functionAfter[0].ID, controlReturn[0].ID) ||
				!first.Poset.IsCausallyBefore(controlReturn[0].ID, processAfter[0].ID) ||
				!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
				t.Fatalf("general-for %s recovery/Return/cleanup causality is incorrect", test.name)
			}
			firstBytes, _ := first.MarshalCanonical()
			secondBytes, _ := second.MarshalCanonical()
			if !bytes.Equal(firstBytes, secondBytes) {
				t.Fatalf("GOMAXPROCS changed general-for %s function-owned handler bytes", test.name)
			}
			expected, _ := first.ArtifactDigest()
			replayed, err := model.ReplayDeterministic(journal, expected)
			if err != nil {
				t.Fatal(err)
			}
			replayedBytes, _ := replayed.MarshalCanonical()
			if !bytes.Equal(firstBytes, replayedBytes) {
				t.Fatalf("general-for %s function-owned handler replay changed bytes", test.name)
			}
		})
	}
}

func TestSourceProcessExternalFunctionOwnedHandlerDirectAllocationRecovers(t *testing.T) {
	source := []byte(`
type Caller is interface
  action in Trigger();
  action out ProcessAfter();
  requires Initialize : function();
end interface Caller;
type Factory is interface
  action out Allocated(value : Factory);
  action out Born();
  action out Relay();
  action out Recovered();
  action out FunctionAfter();
  action out Wrong();
  action out Closing();
  provides Initialize : function();
end interface Factory;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module CallerModule() return Caller is
serial when Trigger() do Initialize(); ProcessAfter(); end when;
end module CallerModule;
module FactoryModule() return Factory is
  Initialize : function() is
  begin
    do
      Allocated(New(Child is True));
      Wrong();
    handler
      is Relay => Recovered();
    end do;
    FunctionAfter();
  end function Initialize;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  if Child then Born(); end if;
final
  Closing();
end module FactoryModule;
architecture System() is
  stimulus : Stimulus;
  caller : Caller is CallerModule();
  provider : Factory is FactoryModule();
connect
  stimulus.Trigger => caller.Trigger;
  caller.Initialize to provider.Initialize;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 80, MaxStatements: 120},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	var providerModule arch.ModuleLifecycleRecord
	for _, lifecycle := range first.Modules {
		if lifecycle.Occurrence == "component:provider" {
			providerModule = lifecycle
		}
	}
	if providerModule.ModuleID == "" {
		t.Fatalf("external function-owned handler has no provider module: %#v", first.Modules)
	}
	var child arch.ModuleLifecycleRecord
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind == "allocator-module" && lifecycle.Parent == providerModule.ModuleID {
			child = lifecycle
		}
	}
	if child.ModuleID == "" {
		t.Fatalf("external function-owned handler created no child: %#v", first.Modules)
	}
	start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	callerCall := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "caller", "Initialize'Call"))
	providerCall := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "provider", "Initialize'Call"))
	born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
	relay := sourceNamedEvents(first.Poset, "provider", "Relay")
	recovered := sourceNamedEvents(first.Poset, "provider", "Recovered")
	functionAfter := sourceNamedEvents(first.Poset, "provider", "FunctionAfter")
	providerReturn := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "provider", "Initialize'Return"))
	callerReturn := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "caller", "Initialize'Return"))
	processAfter := sourceNamedEvents(first.Poset, "caller", "ProcessAfter")
	closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
	if !startExists || !finishExists || len(callerCall) != 1 || len(providerCall) != 1 ||
		len(born) != 1 || len(relay) != 1 || len(recovered) != 1 ||
		len(functionAfter) != 1 || len(providerReturn) != 1 || len(callerReturn) != 1 ||
		len(processAfter) != 1 || len(closing) != 1 {
		t.Fatalf("external function-owned recovery lifecycle/actions are incomplete: start=%t finish=%t callerCall=%d providerCall=%d born=%d relay=%d recovered=%d functionAfter=%d providerReturn=%d callerReturn=%d processAfter=%d closing=%d child=%#v", startExists, finishExists, len(callerCall), len(providerCall), len(born), len(relay), len(recovered), len(functionAfter), len(providerReturn), len(callerReturn), len(processAfter), len(closing), child)
	}
	if callerCall[0].ID != providerCall[0].ID || callerReturn[0].ID != providerReturn[0].ID {
		t.Fatal("external function-owned call/return aliases duplicated occurrences")
	}
	for _, action := range []string{"Allocated", "Wrong"} {
		if events := sourceNamedEvents(first.Poset, "provider", action); len(events) != 0 {
			t.Fatalf("external function-owned handler generated abandoned %s=%#v", action, events)
		}
	}
	if !first.Poset.IsCausallyBefore(providerCall[0].ID, start.ID) {
		t.Fatal("external function-owned handler lost its Call/Start prefix")
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, functionAfter[0].ID) ||
		!first.Poset.IsCausallyBefore(functionAfter[0].ID, providerReturn[0].ID) ||
		!first.Poset.IsCausallyBefore(providerReturn[0].ID, processAfter[0].ID) ||
		!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
		t.Fatal("external function-owned recovery/Return/cleanup causality is incorrect")
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed external function-owned handler bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("external function-owned handler replay changed canonical bytes")
	}
}

func TestSourceProcessGeneralForExternalFunctionOwnedHandlerAllocationRecoversEveryControlPhase(t *testing.T) {
	tests := []struct {
		name, loop string
		loopBody   int
	}{
		{name: "initializer", loop: "for Control() in False next False do CallerWrong(); end for;"},
		{name: "test", loop: "for False in Control() next False do CallerWrong(); end for;"},
		{name: "next", loop: "for False in $keep next Control() do LoopBody(); keep := False; end for;", loopBody: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Caller is interface
  action in Trigger();
  action out ProcessAfter();
  action out LoopBody();
  action out CallerWrong();
  requires Control : function() return Boolean;
end interface Caller;
type Factory is interface
  action out Allocated(value : Factory);
  action out Born();
  action out Relay();
  action out Recovered();
  action out FunctionAfter();
  action out ProviderWrong();
  action out Closing();
  provides Control : function() return Boolean;
end interface Factory;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module CallerModule() return Caller is
  keep : var Boolean := True;
serial when Trigger() do
  ` + test.loop + `
  ProcessAfter();
end when;
end module CallerModule;
module FactoryModule() return Factory is
  Control : function() return Boolean is
  begin
    do
      Allocated(New(Child is True));
      ProviderWrong();
    handler
      is Relay => Recovered();
    end do;
    FunctionAfter();
    return False;
  end function Control;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  if Child then Born(); end if;
final
  Closing();
end module FactoryModule;
architecture System() is
  stimulus : Stimulus;
  caller : Caller is CallerModule();
  provider : Factory is FactoryModule();
connect
  stimulus.Trigger => caller.Trigger;
  caller.Control to provider.Control;
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
			journal := arch.NewExecutionJournalWithLimits(
				digest, arch.ExecutionLimits{MaxFirings: 80, MaxStatements: 120},
				arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
			)
			previous := runtime.GOMAXPROCS(1)
			first, err := model.ExecuteDeterministic(journal)
			if err != nil {
				runtime.GOMAXPROCS(previous)
				t.Fatal(err)
			}
			runtime.GOMAXPROCS(8)
			second, secondErr := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(previous)
			if secondErr != nil {
				t.Fatal(secondErr)
			}
			var providerModule arch.ModuleLifecycleRecord
			for _, lifecycle := range first.Modules {
				if lifecycle.Occurrence == "component:provider" {
					providerModule = lifecycle
				}
			}
			if providerModule.ModuleID == "" {
				t.Fatalf("external general-for %s has no provider module: %#v", test.name, first.Modules)
			}
			var child arch.ModuleLifecycleRecord
			for _, lifecycle := range first.Modules {
				if lifecycle.Kind == "allocator-module" && lifecycle.Parent == providerModule.ModuleID {
					child = lifecycle
				}
			}
			if child.ModuleID == "" {
				t.Fatalf("external general-for %s created no provider child: %#v", test.name, first.Modules)
			}
			start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
			finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
			callerCall := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "caller", "Control'Call"))
			providerCall := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "provider", "Control'Call"))
			born := sourceNamedEvents(first.Poset, child.ModuleID, "Born")
			relay := sourceNamedEvents(first.Poset, "provider", "Relay")
			recovered := sourceNamedEvents(first.Poset, "provider", "Recovered")
			functionAfter := sourceNamedEvents(first.Poset, "provider", "FunctionAfter")
			providerReturn := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "provider", "Control'Return"))
			callerReturn := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "caller", "Control'Return"))
			processAfter := sourceNamedEvents(first.Poset, "caller", "ProcessAfter")
			loopBody := sourceNamedEvents(first.Poset, "caller", "LoopBody")
			closing := sourceNamedEvents(first.Poset, child.ModuleID, "Closing")
			if !startExists || !finishExists || len(callerCall) != 1 || len(providerCall) != 1 ||
				len(born) != 1 || len(relay) != 1 || len(recovered) != 1 ||
				len(functionAfter) != 1 || len(providerReturn) != 1 || len(callerReturn) != 1 ||
				len(processAfter) != 1 || len(loopBody) != test.loopBody || len(closing) != 1 {
				t.Fatalf("external general-for %s lifecycle/actions are incomplete: child=%#v", test.name, child)
			}
			if callerCall[0].ID != providerCall[0].ID || callerReturn[0].ID != providerReturn[0].ID {
				t.Fatalf("external general-for %s duplicated connected call/return occurrences", test.name)
			}
			for _, absent := range []struct{ source, action string }{
				{source: "provider", action: "Allocated"},
				{source: "provider", action: "ProviderWrong"},
				{source: "caller", action: "CallerWrong"},
			} {
				if events := sourceNamedEvents(first.Poset, absent.source, absent.action); len(events) != 0 {
					t.Fatalf("external general-for %s generated abandoned %s=%#v", test.name, absent.action, events)
				}
			}
			if !first.Poset.IsCausallyBefore(providerCall[0].ID, start.ID) {
				t.Fatalf("external general-for %s lost its connected Call/Start prefix", test.name)
			}
			assertOnlyDirectCause(t, first.Poset, born[0], start)
			assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
			assertOnlyDirectCause(t, first.Poset, recovered[0], relay[0])
			assertOnlyDirectCause(t, first.Poset, closing[0], relay[0])
			assertOnlyDirectCause(t, first.Poset, finish, closing[0])
			if !first.Poset.IsCausallyBefore(recovered[0].ID, functionAfter[0].ID) ||
				!first.Poset.IsCausallyBefore(functionAfter[0].ID, providerReturn[0].ID) ||
				!first.Poset.IsCausallyBefore(callerReturn[0].ID, processAfter[0].ID) ||
				!first.Poset.IsCausallyIndependent(recovered[0].ID, closing[0].ID) {
				t.Fatalf("external general-for %s recovery/result/cleanup causality is incorrect", test.name)
			}
			firstBytes, _ := first.MarshalCanonical()
			secondBytes, _ := second.MarshalCanonical()
			if !bytes.Equal(firstBytes, secondBytes) {
				t.Fatalf("GOMAXPROCS changed external general-for %s bytes", test.name)
			}
			expected, _ := first.ArtifactDigest()
			replayed, err := model.ReplayDeterministic(journal, expected)
			if err != nil {
				t.Fatal(err)
			}
			replayedBytes, _ := replayed.MarshalCanonical()
			if !bytes.Equal(firstBytes, replayedBytes) {
				t.Fatalf("external general-for %s replay changed canonical bytes", test.name)
			}
		})
	}
}

func TestSourceAllocatedModuleInitialLocalFunctionRecursivelyAllocates(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory; depth : Integer);
  action out Initialized(depth : Integer);
  provides Spawn : function(Depth : Integer);
  provides Initialize : function(Depth : Integer);
end interface Factory;
type Driver is interface
  action in Trigger(depth : Integer);
  requires Spawn : function(Depth : Integer);
end interface Driver;
type Stimulus is interface action out Trigger(depth : Integer); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function(Depth : Integer) is begin Allocated(New(Depth is Depth), Depth); end function Spawn;
  Initialize : function(Depth : Integer) is
  begin
    if Depth > 0 then Allocated(New(Depth is Depth - 1), Depth - 1); end if;
  end function Initialize;
initial (Depth : Integer is 0)
  Initialize(Depth);
  Initialized(Depth);
end module FactoryModule;
module DriverModule() return Driver is
serial when (?Depth : Integer) Trigger(?Depth) do Spawn(?Depth); end when;
end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  (?Depth : Integer) stimulus.Trigger(?Depth) => driver.Trigger(?Depth);
  driver.Spawn to factory.Spawn;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 100, MaxStatements: 160},
		arch.InputEvent{
			Key: "trigger", Source: "stimulus", Action: "Trigger",
			Params: map[string]any{"depth": 1},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	outer := sourceNamedEvents(result.Poset, "factory", "Allocated")
	if len(outer) != 1 || outer[0].ParamInt("depth") != 1 {
		t.Fatalf("outer allocation=%#v", outer)
	}
	outerValue, _ := outer[0].Param("value")
	parent, ok := outerValue.(gorapide.RapideModuleValue)
	if !ok || parent.Identity() == "" {
		t.Fatalf("outer allocated value=%#v", outerValue)
	}
	nested := sourceNamedEvents(result.Poset, parent.Identity(), "Allocated")
	if len(nested) != 1 || nested[0].ParamInt("depth") != 0 {
		t.Fatalf("function-mediated nested allocation=%#v", nested)
	}
	childValue, _ := nested[0].Param("value")
	child, ok := childValue.(gorapide.RapideModuleValue)
	if !ok || child.Identity() == "" || child.Identity() == parent.Identity() {
		t.Fatalf("function-mediated nested child=%#v parent=%s", childValue, parent.Identity())
	}
	calls := sourceNamedEvents(result.Poset, parent.Identity(), "Initialize'Call")
	returns := sourceNamedEvents(result.Poset, parent.Identity(), "Initialize'Return")
	initialized := sourceNamedEvents(result.Poset, parent.Identity(), "Initialized")
	if len(calls) != 1 || len(returns) != 1 || len(initialized) != 1 ||
		!result.Poset.IsCausallyBefore(calls[0].ID, nested[0].ID) ||
		!result.Poset.IsCausallyBefore(nested[0].ID, returns[0].ID) ||
		!result.Poset.IsCausallyBefore(returns[0].ID, initialized[0].ID) {
		t.Fatalf("function-mediated call/allocation/return/initializer causality calls=%#v nested=%#v returns=%#v initialized=%#v",
			calls, nested, returns, initialized)
	}
}

func TestSourceNestedAllocatedModuleInitializationFailureFinalizesCreationChain(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Before(depth : Integer);
  action out Allocated(value : Factory; depth : Integer);
  action out Initialized(depth : Integer);
  action out Closing();
  provides Spawn : function(Depth : Integer);
end interface Factory;
type Driver is interface
  action in Trigger(depth : Integer);
  action out After();
  requires Spawn : function(Depth : Integer);
end interface Driver;
type Stimulus is interface action out Trigger(depth : Integer); end interface Stimulus;

module FactoryModule() return Factory is
  exception Failure(code : Integer);
  Spawn : function(Depth : Integer) is begin Allocated(New(Depth is Depth), Depth); end function Spawn;
initial (Depth : Integer is -1)
  if Depth >= 0 then
    Before(Depth);
    if Depth > 0 then
      Allocated(New(Depth is Depth - 1), Depth - 1);
    else
      raise Failure(code is Depth);
    end if;
    Initialized(Depth);
  end if;
final
  Closing();
end module FactoryModule;
module DriverModule() return Driver is
serial when (?Depth : Integer) Trigger(?Depth) do Spawn(?Depth); After(); end when;
end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  (?Depth : Integer) stimulus.Trigger(?Depth) => driver.Trigger(?Depth);
  driver.Spawn to factory.Spawn;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 220, MaxStatements: 260},
		arch.InputEvent{
			Key: "trigger", Source: "stimulus", Action: "Trigger",
			Params: map[string]any{"depth": 2},
		},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}

	byDepth := make(map[int]arch.ModuleLifecycleRecord)
	var failure *gorapide.Event
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind != "allocator-module" {
			continue
		}
		before := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Before")
		if len(before) != 1 {
			t.Fatalf("failed recursive module %q Before=%#v", lifecycle.ModuleID, before)
		}
		depth := before[0].ParamInt("depth")
		byDepth[depth] = lifecycle
		if depth == 0 {
			failures := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Failure")
			if len(failures) != 1 || failures[0].ParamInt("code") != 0 {
				t.Fatalf("leaf failures=%#v", failures)
			}
			failure = failures[0]
		}
	}
	if len(byDepth) != 3 || failure == nil {
		t.Fatalf("recursive failure modules=%#v failure=%#v", byDepth, failure)
	}
	for depth := 0; depth <= 2; depth++ {
		lifecycle := byDepth[depth]
		if lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
			lifecycle.TerminationEventID != string(failure.ID) || lifecycle.FinishEventID == "" {
			t.Fatalf("depth %d failed lifecycle=%#v", depth, lifecycle)
		}
		finish, ok := first.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
		if !ok {
			t.Fatalf("depth %d missing Finish %q", depth, lifecycle.FinishEventID)
		}
		assertOnlyDirectCause(t, first.Poset, finish, failure)
		if events := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Closing"); len(events) != 0 {
			t.Fatalf("depth %d failed creation ran final part=%#v", depth, events)
		}
		if depth < 2 && lifecycle.Parent != byDepth[depth+1].ModuleID {
			t.Fatalf("depth %d parent=%q, want %q", depth, lifecycle.Parent, byDepth[depth+1].ModuleID)
		}
	}
	for _, absent := range []struct{ source, action string }{
		{"factory", "Allocated"}, {"driver", "After"},
		{byDepth[2].ModuleID, "Allocated"}, {byDepth[1].ModuleID, "Allocated"},
		{byDepth[2].ModuleID, "Initialized"}, {byDepth[1].ModuleID, "Initialized"},
		{byDepth[0].ModuleID, "Initialized"},
	} {
		if events := sourceNamedEvents(first.Poset, absent.source, absent.action); len(events) != 0 {
			t.Fatalf("failed recursive creation emitted %s.%s=%#v", absent.source, absent.action, events)
		}
	}
	leafPropagation := exceptionPropagationBySource(t, first, byDepth[0].ModuleID)
	middlePropagation := exceptionPropagationBySource(t, first, byDepth[1].ModuleID)
	outerPropagation := exceptionPropagationBySource(t, first, byDepth[2].ModuleID)
	if len(leafPropagation.Targets) != 1 || leafPropagation.Targets[0].ModuleID != byDepth[1].ModuleID ||
		leafPropagation.Targets[0].Disposition != "delivered" ||
		len(middlePropagation.Targets) != 1 || middlePropagation.Targets[0].ModuleID != byDepth[2].ModuleID ||
		middlePropagation.Targets[0].Disposition != "delivered" ||
		len(outerPropagation.Targets) != 1 || outerPropagation.Targets[0].Disposition != "delivered" {
		t.Fatalf("recursive failure propagation leaf=%#v middle=%#v outer=%#v",
			leafPropagation, middlePropagation, outerPropagation)
	}

	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed recursive failed-initialization artifact bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("recursive failed-initialization replay changed artifact bytes")
	}
}

func TestSourceNestedAllocatedModuleInitializationFailureHandledByUnreturnedParentFinalizesNormally(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Before(depth : Integer);
  action out Recovered();
  action out Closing();
  provides Spawn : function(Depth : Integer);
end interface Factory;
type Driver is interface action in Trigger(); action out After(); requires Spawn : function(Depth : Integer); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function(Depth : Integer) is begin Allocated(New(Depth is Depth)); end function Spawn;
initial (Depth : Integer is -1)
  Before(Depth);
  if Depth > 0 then Allocated(New(Depth is Depth - 1));
  elsif Depth = 0 then raise Failure;
  end if;
handler
  is Failure => Recovered();
final
  Closing();
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(2); After(); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 100, MaxStatements: 120},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, firstErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if firstErr != nil || secondErr != nil {
		t.Fatalf("handled nested failure execution first=%v second=%v", firstErr, secondErr)
	}

	byDepth := make(map[int]arch.ModuleLifecycleRecord)
	var failure *gorapide.Event
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind != "allocator-module" {
			continue
		}
		before := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Before")
		if len(before) != 1 {
			t.Fatalf("handled recursive module %q Before=%#v", lifecycle.ModuleID, before)
		}
		depth := before[0].ParamInt("depth")
		byDepth[depth] = lifecycle
		if depth == 0 {
			failures := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Failure")
			if len(failures) != 1 {
				t.Fatalf("handled recursive leaf failures=%#v", failures)
			}
			failure = failures[0]
		}
	}
	if len(byDepth) != 3 || failure == nil {
		t.Fatalf("handled recursive modules=%#v failure=%#v", byDepth, failure)
	}
	leaf := byDepth[0]
	if leaf.State != arch.ModuleFinalizedState || leaf.Namable ||
		leaf.TerminationEventID != string(failure.ID) || leaf.FinishEventID == "" {
		t.Fatalf("handled recursive leaf lifecycle=%#v", leaf)
	}
	if closing := sourceNamedEvents(first.Poset, leaf.ModuleID, "Closing"); len(closing) != 0 {
		t.Fatalf("failed leaf ran ordinary final part=%#v", closing)
	}
	leafFinish, ok := first.Poset.Get(gorapide.EventID(leaf.FinishEventID))
	if !ok {
		t.Fatalf("missing failed leaf Finish %q", leaf.FinishEventID)
	}
	assertOnlyDirectCause(t, first.Poset, leafFinish, failure)
	if len(leaf.Names) != 1 || leaf.Names[0].Kind != "implicit-self" || leaf.Names[0].Live ||
		len(leaf.Names[0].LostAfter) != 1 || leaf.Names[0].LostAfter[0] != string(failure.ID) {
		t.Fatalf("failed leaf provisional-name cleanup=%#v", leaf.Names)
	}

	recovered := sourceNamedEvents(first.Poset, byDepth[1].ModuleID, "Recovered")
	if len(recovered) != 1 {
		t.Fatalf("unreturned parent recovery=%#v", recovered)
	}
	assertOnlyDirectCause(t, first.Poset, recovered[0], failure)
	for depth := 1; depth <= 2; depth++ {
		lifecycle := byDepth[depth]
		if lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
			lifecycle.TerminationEventID != "" || lifecycle.FinishEventID == "" {
			t.Fatalf("depth %d handled-abandonment lifecycle=%#v", depth, lifecycle)
		}
		closing := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Closing")
		if len(closing) != 1 {
			t.Fatalf("depth %d ordinary final part=%#v", depth, closing)
		}
		assertOnlyDirectCause(t, first.Poset, closing[0], recovered[0])
		finish, ok := first.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
		if !ok {
			t.Fatalf("depth %d missing Finish %q", depth, lifecycle.FinishEventID)
		}
		assertOnlyDirectCause(t, first.Poset, finish, closing[0])
		if len(lifecycle.Names) != 1 || lifecycle.Names[0].Kind != "implicit-self" ||
			lifecycle.Names[0].Live || len(lifecycle.Names[0].LostAfter) != 1 ||
			lifecycle.Names[0].LostAfter[0] != string(recovered[0].ID) {
			t.Fatalf("depth %d handled-abandonment name cleanup=%#v", depth, lifecycle.Names)
		}
		contextClosed := false
		for _, communicationContext := range first.Contexts {
			if communicationContext.Kind == "initial-parent" &&
				communicationContext.Source == lifecycle.ModuleID &&
				!communicationContext.Live && len(communicationContext.LostAfter) == 1 &&
				communicationContext.LostAfter[0] == string(closing[0].ID) {
				contextClosed = true
				break
			}
		}
		if !contextClosed {
			t.Fatalf("depth %d handled-abandonment Context cleanup=%#v", depth, first.Contexts)
		}
	}
	if !first.Poset.IsCausallyIndependent(
		sourceNamedEvents(first.Poset, byDepth[1].ModuleID, "Closing")[0].ID,
		sourceNamedEvents(first.Poset, byDepth[2].ModuleID, "Closing")[0].ID,
	) {
		t.Fatal("host unwind falsely ordered independent abandoned-parent finalization branches")
	}
	if !first.Poset.IsCausallyIndependent(leafFinish.ID, recovered[0].ID) {
		t.Fatal("leaf exceptional cleanup was falsely ordered with parent-handler recovery")
	}
	if events := sourceNamedEvents(first.Poset, byDepth[0].ModuleID, "Recovered"); len(events) != 0 {
		t.Fatalf("failed leaf handled its own initializer exception=%#v", events)
	}
	if events := sourceNamedEvents(first.Poset, byDepth[2].ModuleID, "Recovered"); len(events) != 0 {
		t.Fatalf("outer unreturned caller received stopped propagation=%#v", events)
	}
	for _, absent := range []struct{ source, action string }{
		{"factory", "Allocated"}, {"driver", "After"},
		{byDepth[2].ModuleID, "Allocated"}, {byDepth[1].ModuleID, "Allocated"},
	} {
		if events := sourceNamedEvents(first.Poset, absent.source, absent.action); len(events) != 0 {
			t.Fatalf("handled recursive creation emitted %s.%s=%#v", absent.source, absent.action, events)
		}
	}
	if byDepth[0].Parent != byDepth[1].ModuleID || byDepth[1].Parent != byDepth[2].ModuleID {
		t.Fatalf("handled recursive parents leaf=%q middle=%q outer=%q",
			byDepth[0].Parent, byDepth[1].Parent, byDepth[2].Parent)
	}
	propagation := exceptionPropagationBySource(t, first, leaf.ModuleID)
	if len(propagation.Targets) != 1 || propagation.Targets[0].ModuleID != byDepth[1].ModuleID ||
		propagation.Targets[0].Disposition != "handled" {
		t.Fatalf("handled leaf propagation=%#v", propagation)
	}
	for _, record := range first.ExceptionPropagations {
		if record.SourceModuleID == byDepth[1].ModuleID || record.SourceModuleID == byDepth[2].ModuleID {
			t.Fatalf("handled occurrence propagated past recovery: %#v", record)
		}
	}
	abandonmentTargets := make([]string, 0, 2)
	for _, firing := range first.Firings {
		if firing.Transition == "initialization-abandonment-finalization" {
			abandonmentTargets = append(abandonmentTargets, firing.Target)
		}
	}
	if len(abandonmentTargets) != 2 || abandonmentTargets[0] != byDepth[1].ModuleID ||
		abandonmentTargets[1] != byDepth[2].ModuleID {
		t.Fatalf("handled-abandonment finalization audit=%#v", abandonmentTargets)
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
		t.Fatal("GOMAXPROCS changed handled nested-failure artifact bytes")
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
		t.Fatal("handled nested-failure replay changed canonical bytes")
	}
}

func TestSourceNestedAllocatedModuleInitializationStateOnlyParentRecoveryRetainsOrdinaryFinal(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Before(depth : Integer);
  action out Closing();
  provides Spawn : function(Depth : Integer);
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(Depth : Integer); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  exception Failure;
  WasRecovered : var Boolean := False;
  Spawn : function(Depth : Integer) is begin Allocated(New(Depth is Depth)); end function Spawn;
initial (Depth : Integer is -1)
  Before(Depth);
  if Depth > 0 then Allocated(New(Depth is Depth - 1));
  elsif Depth = 0 then raise Failure;
  end if;
handler
  is Failure => WasRecovered := True;
final
  Closing();
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(1); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 100, MaxStatements: 140},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	byDepth := make(map[int]arch.ModuleLifecycleRecord)
	var failure *gorapide.Event
	for _, lifecycle := range result.Modules {
		if lifecycle.Kind != "allocator-module" {
			continue
		}
		before := sourceNamedEvents(result.Poset, lifecycle.ModuleID, "Before")
		if len(before) != 1 {
			t.Fatalf("state-only recovery module %q Before=%#v", lifecycle.ModuleID, before)
		}
		depth := before[0].ParamInt("depth")
		byDepth[depth] = lifecycle
		if depth == 0 {
			failures := sourceNamedEvents(result.Poset, lifecycle.ModuleID, "Failure")
			if len(failures) != 1 {
				t.Fatalf("state-only recovery leaf failures=%#v", failures)
			}
			failure = failures[0]
		}
	}
	if len(byDepth) != 2 || failure == nil {
		t.Fatalf("state-only recovery modules=%#v failure=%#v", byDepth, failure)
	}
	parent := byDepth[1]
	closing := sourceNamedEvents(result.Poset, parent.ModuleID, "Closing")
	if len(closing) != 1 {
		t.Fatalf("state-only recovery ordinary final=%#v", closing)
	}
	assertOnlyDirectCause(t, result.Poset, closing[0], failure)
	if parent.State != arch.ModuleFinalizedState || parent.TerminationEventID != "" ||
		parent.Namable || parent.FinishEventID == "" {
		t.Fatalf("state-only recovery parent lifecycle=%#v", parent)
	}
	propagation := exceptionPropagationBySource(t, result, byDepth[0].ModuleID)
	if len(propagation.Targets) != 1 || propagation.Targets[0].ModuleID != parent.ModuleID ||
		propagation.Targets[0].Disposition != "handled" {
		t.Fatalf("state-only recovery propagation=%#v", propagation)
	}
	firings := 0
	for _, firing := range result.Firings {
		if firing.Transition == "module-handler" && firing.Target == parent.ModuleID {
			firings++
			if len(firing.Generated) != 0 || len(firing.StateWrites) != 1 {
				t.Fatalf("state-only module-handler audit=%#v", firing)
			}
		}
	}
	if firings != 1 {
		t.Fatalf("state-only module-handler firings=%d", firings)
	}
}

func TestSourceNestedAllocatedModuleInitializationParentHandlerReplacementContinuesExactChain(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Before(depth : Integer);
  action out Recovered();
  action out Closing();
  provides Spawn : function(Depth : Integer);
end interface Factory;
type Driver is interface action in Trigger(); action out After(); requires Spawn : function(Depth : Integer); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  exception Failure;
  exception Replacement;
  Spawn : function(Depth : Integer) is begin Allocated(New(Depth is Depth)); end function Spawn;
initial (Depth : Integer is -1)
  Before(Depth);
  if Depth > 0 then Allocated(New(Depth is Depth - 1));
  elsif Depth = 0 then raise Failure;
  end if;
handler
  is Failure => raise Replacement;
  is Replacement => Recovered();
final
  Closing();
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(2); After(); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 140, MaxStatements: 180},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, firstErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if firstErr != nil || secondErr != nil {
		t.Fatalf("nested replacement chain first=%v second=%v", firstErr, secondErr)
	}

	byDepth := make(map[int]arch.ModuleLifecycleRecord)
	var failure, replacement *gorapide.Event
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind != "allocator-module" {
			continue
		}
		before := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Before")
		if len(before) != 1 {
			t.Fatalf("replacement-chain module %q Before=%#v", lifecycle.ModuleID, before)
		}
		depth := before[0].ParamInt("depth")
		byDepth[depth] = lifecycle
		if depth == 0 {
			events := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Failure")
			if len(events) != 1 {
				t.Fatalf("replacement-chain leaf failures=%#v", events)
			}
			failure = events[0]
		}
		if depth == 1 {
			events := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Replacement")
			if len(events) != 1 {
				t.Fatalf("replacement-chain middle replacements=%#v", events)
			}
			replacement = events[0]
		}
	}
	if len(byDepth) != 3 || failure == nil || replacement == nil {
		t.Fatalf("replacement-chain modules=%#v failure=%#v replacement=%#v",
			byDepth, failure, replacement)
	}
	assertOnlyDirectCause(t, first.Poset, replacement, failure)

	leaf, middle, outer := byDepth[0], byDepth[1], byDepth[2]
	for _, expected := range []struct {
		name      string
		lifecycle arch.ModuleLifecycleRecord
		cause     *gorapide.Event
	}{
		{name: "leaf", lifecycle: leaf, cause: failure},
		{name: "middle", lifecycle: middle, cause: replacement},
	} {
		lifecycle := expected.lifecycle
		if lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
			lifecycle.TerminationEventID != string(expected.cause.ID) || lifecycle.FinishEventID == "" {
			t.Fatalf("replacement-chain %s exceptional lifecycle=%#v", expected.name, lifecycle)
		}
		if closing := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Closing"); len(closing) != 0 {
			t.Fatalf("replacement-chain %s ran ordinary final=%#v", expected.name, closing)
		}
		finish, ok := first.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
		if !ok {
			t.Fatalf("replacement-chain %s missing Finish %q", expected.name, lifecycle.FinishEventID)
		}
		assertOnlyDirectCause(t, first.Poset, finish, expected.cause)
		if len(lifecycle.Names) != 1 || lifecycle.Names[0].Kind != "implicit-self" ||
			lifecycle.Names[0].Live || len(lifecycle.Names[0].LostAfter) != 1 ||
			lifecycle.Names[0].LostAfter[0] != string(expected.cause.ID) {
			t.Fatalf("replacement-chain %s name cleanup=%#v", expected.name, lifecycle.Names)
		}
		contextClosed := false
		for _, communicationContext := range first.Contexts {
			if communicationContext.Kind == "initial-parent" &&
				communicationContext.Source == lifecycle.ModuleID &&
				!communicationContext.Live && len(communicationContext.LostAfter) == 1 &&
				communicationContext.LostAfter[0] == string(expected.cause.ID) {
				contextClosed = true
				break
			}
		}
		if !contextClosed {
			t.Fatalf("replacement-chain %s Context cleanup=%#v", expected.name, first.Contexts)
		}
	}
	recovered := sourceNamedEvents(first.Poset, outer.ModuleID, "Recovered")
	closing := sourceNamedEvents(first.Poset, outer.ModuleID, "Closing")
	if len(recovered) != 1 || len(closing) != 1 {
		t.Fatalf("replacement-chain outer recovery/final=%#v/%#v", recovered, closing)
	}
	assertOnlyDirectCause(t, first.Poset, recovered[0], replacement)
	assertOnlyDirectCause(t, first.Poset, closing[0], recovered[0])
	if outer.State != arch.ModuleFinalizedState || outer.Namable ||
		outer.TerminationEventID != "" || outer.FinishEventID == "" {
		t.Fatalf("replacement-chain outer normal lifecycle=%#v", outer)
	}
	outerFinish, ok := first.Poset.Get(gorapide.EventID(outer.FinishEventID))
	if !ok {
		t.Fatalf("replacement-chain outer missing Finish %q", outer.FinishEventID)
	}
	assertOnlyDirectCause(t, first.Poset, outerFinish, closing[0])
	if len(outer.Names) != 1 || outer.Names[0].Kind != "implicit-self" ||
		outer.Names[0].Live || len(outer.Names[0].LostAfter) != 1 ||
		outer.Names[0].LostAfter[0] != string(recovered[0].ID) {
		t.Fatalf("replacement-chain outer name cleanup=%#v", outer.Names)
	}
	outerContextClosed := false
	for _, communicationContext := range first.Contexts {
		if communicationContext.Kind == "initial-parent" &&
			communicationContext.Source == outer.ModuleID &&
			!communicationContext.Live && len(communicationContext.LostAfter) == 1 &&
			communicationContext.LostAfter[0] == string(closing[0].ID) {
			outerContextClosed = true
			break
		}
	}
	if !outerContextClosed {
		t.Fatalf("replacement-chain outer Context cleanup=%#v", first.Contexts)
	}
	if !first.Poset.IsCausallyIndependent(
		gorapide.EventID(middle.FinishEventID), recovered[0].ID,
	) {
		t.Fatal("replacement-chain middle cleanup was falsely ordered with outer recovery")
	}
	if !first.Poset.IsCausallyIndependent(
		gorapide.EventID(leaf.FinishEventID), replacement.ID,
	) {
		t.Fatal("replacement-chain leaf cleanup was falsely ordered with handler replacement")
	}

	failurePropagation := exceptionPropagationBySource(t, first, leaf.ModuleID)
	replacementPropagation := exceptionPropagationBySource(t, first, middle.ModuleID)
	if failurePropagation.ExceptionEventID != string(failure.ID) ||
		len(failurePropagation.Targets) != 1 ||
		failurePropagation.Targets[0].ModuleID != middle.ModuleID ||
		failurePropagation.Targets[0].Disposition != "handler-raised" {
		t.Fatalf("replacement-chain failure propagation=%#v", failurePropagation)
	}
	if replacementPropagation.ExceptionEventID != string(replacement.ID) ||
		len(replacementPropagation.Targets) != 1 ||
		replacementPropagation.Targets[0].ModuleID != outer.ModuleID ||
		replacementPropagation.Targets[0].Disposition != "handled" {
		t.Fatalf("replacement-chain replacement propagation=%#v", replacementPropagation)
	}
	for _, record := range first.ExceptionPropagations {
		if record.SourceModuleID == outer.ModuleID {
			t.Fatalf("replacement-chain propagated past outer recovery=%#v", record)
		}
	}
	for _, absent := range []struct{ source, action string }{
		{"factory", "Allocated"}, {"driver", "After"},
		{outer.ModuleID, "Allocated"}, {middle.ModuleID, "Allocated"},
	} {
		if events := sourceNamedEvents(first.Poset, absent.source, absent.action); len(events) != 0 {
			t.Fatalf("replacement-chain emitted %s.%s=%#v", absent.source, absent.action, events)
		}
	}
	var driverProcess *arch.ProcessExecutionRecord
	for index := range first.Processes {
		if first.Processes[index].ComponentID == "driver" {
			driverProcess = &first.Processes[index]
			break
		}
	}
	if driverProcess == nil || !driverProcess.Terminated || driverProcess.Completion != "exception" ||
		driverProcess.ExceptionEventID != string(failure.ID) {
		t.Fatalf("replacement-chain caller process=%#v", driverProcess)
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
		t.Fatal("GOMAXPROCS changed nested replacement-chain artifact bytes")
	}
	expectedDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expectedDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("nested replacement-chain replay changed canonical bytes")
	}
}

func TestSourceNestedAllocatedModuleInitializationFunctionFailureUnwindsWithoutReturn(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  provides Spawn : function(Depth : Integer);
  provides Build : function(Depth : Integer);
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(Depth : Integer); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function(Depth : Integer) is begin Allocated(New(Depth is Depth)); end function Spawn;
  Build : function(Depth : Integer) is
  begin
    if Depth > 0 then Allocated(New(Depth is Depth - 1)); else raise Failure; end if;
  end function Build;
initial (Depth : Integer is -1)
  if Depth >= 0 then Build(Depth); end if;
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(1); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 120, MaxStatements: 160},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	children := make([]arch.ModuleLifecycleRecord, 0, 2)
	for _, lifecycle := range result.Modules {
		if lifecycle.Kind == "allocator-module" {
			children = append(children, lifecycle)
		}
	}
	if len(children) != 2 {
		t.Fatalf("function-mediated failed creation modules=%#v", children)
	}
	var failure *gorapide.Event
	for _, child := range children {
		failures := sourceNamedEvents(result.Poset, child.ModuleID, "Failure")
		if len(failures) == 1 {
			failure = failures[0]
		}
	}
	if failure == nil {
		t.Fatal("function-mediated leaf failure is absent")
	}
	for _, child := range children {
		if child.State != arch.ModuleFinalizedState ||
			child.TerminationEventID != string(failure.ID) || child.FinishEventID == "" {
			t.Fatalf("function-mediated failed lifecycle=%#v", child)
		}
		if returns := sourceNamedEvents(result.Poset, child.ModuleID, "Build'Return"); len(returns) != 0 {
			t.Fatalf("abandoned initializer function returned in %q: %#v", child.ModuleID, returns)
		}
	}
	if returns := sourceNamedEvents(result.Poset, "factory", "Spawn'Return"); len(returns) != 0 {
		t.Fatalf("failed recursive Spawn returned=%#v", returns)
	}
}

func TestSourceNestedAllocatedModuleInitializationFailureCancelsEveryDeferredPlan(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Later(depth : Integer);
  provides Spawn : function(Depth : Integer);
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(Depth : Integer); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  exception Failure;
  C : Clock is Make_Clock();
  Spawn : function(Depth : Integer) is begin Allocated(New(Depth is Depth)); end function Spawn;
initial (Depth : Integer is -1)
  if Depth > 0 then
    Later(Depth) in C.Ticks(1);
    Allocated(New(Depth is Depth - 1));
  elsif Depth = 0 then
    raise Failure;
  end if;
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(2); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 140, MaxStatements: 180},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ScheduledEvents) != 0 || len(result.ClockAdvances) != 0 {
		t.Fatalf("failed recursive initialization installed deferred work: scheduled=%#v advances=%#v",
			result.ScheduledEvents, result.ClockAdvances)
	}
	for _, lifecycle := range result.Modules {
		if events := sourceNamedEvents(result.Poset, lifecycle.ModuleID, "Later"); len(events) != 0 {
			t.Fatalf("failed recursive initialization emitted deferred Later in %q: %#v", lifecycle.ModuleID, events)
		}
	}
	canceled := make(map[string]bool)
	for _, firing := range result.Firings {
		if firing.Transition != "process" || firing.Target != "driver" {
			continue
		}
		for _, scheduleID := range firing.CanceledSchedules {
			if scheduleID == "" || canceled[scheduleID] {
				t.Fatalf("recursive failure cancellation is empty or duplicated: %#v", firing.CanceledSchedules)
			}
			canceled[scheduleID] = true
		}
	}
	if len(canceled) != 2 {
		t.Fatalf("recursive failure canceled schedules=%#v, want two ancestor plans", canceled)
	}
	for _, clock := range result.Clocks {
		if clock.Now != "0" {
			t.Fatalf("recursive failure advanced clock=%#v", clock)
		}
	}
}

func TestSourceAllocatedModuleInitialFunctionLocalsRetainDynamicLexicalLifetime(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory; stage : Integer);
  action out Initialized(depth : Integer);
  action out Closing();
  provides Spawn : function(Depth : Integer);
  provides Initialize : function(Depth : Integer);
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(Depth : Integer); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  Spawn : function(Depth : Integer) is begin Allocated(New(Depth is Depth), 0); end function Spawn;
  Initialize : function(Depth : Integer) is
    First, Second : Factory is New(Depth is Depth - 1);
  begin
    Allocated(First, 1);
    Allocated(Second, 2);
  end function Initialize;
initial (Depth : Integer is 0)
  if Depth > 0 then Initialize(Depth); end if;
  Initialized(Depth);
final
  Closing();
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(1); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 180, MaxStatements: 220},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	outerEvents := sourceNamedEvents(first.Poset, "factory", "Allocated")
	if len(outerEvents) != 1 || outerEvents[0].ParamInt("stage") != 0 {
		t.Fatalf("outer allocation=%#v", outerEvents)
	}
	outerValue, _ := outerEvents[0].Param("value")
	outer, ok := outerValue.(gorapide.RapideModuleValue)
	if !ok || outer.Identity() == "" {
		t.Fatalf("outer module=%#v", outerValue)
	}
	localEvents := sourceNamedEvents(first.Poset, outer.Identity(), "Allocated")
	if len(localEvents) != 2 || localEvents[0].ParamInt("stage") != 1 ||
		localEvents[1].ParamInt("stage") != 2 ||
		!first.Poset.IsCausallyBefore(localEvents[0].ID, localEvents[1].ID) {
		t.Fatalf("dynamic initializer function-local events=%#v", localEvents)
	}
	locals := make([]gorapide.RapideModuleValue, 0, 2)
	for _, event := range localEvents {
		value, _ := event.Param("value")
		local, ok := value.(gorapide.RapideModuleValue)
		if !ok || local.Identity() == "" || local.Identity() == outer.Identity() {
			t.Fatalf("dynamic initializer function local=%#v", value)
		}
		locals = append(locals, local)
	}
	if locals[0].Identity() == locals[1].Identity() {
		t.Fatalf("dynamic initializer function locals reused identity=%#v", locals)
	}
	returns := sourceNamedEvents(first.Poset, outer.Identity(), "Initialize'Return")
	initialized := sourceNamedEvents(first.Poset, outer.Identity(), "Initialized")
	if len(returns) != 1 || len(initialized) != 1 ||
		!first.Poset.IsCausallyBefore(returns[0].ID, initialized[0].ID) {
		t.Fatalf("dynamic initializer local Return/Initialized=%#v/%#v", returns, initialized)
	}
	lifecycles := make(map[string]arch.ModuleLifecycleRecord)
	for _, lifecycle := range first.Modules {
		lifecycles[lifecycle.ModuleID] = lifecycle
	}
	outerLifecycle := lifecycles[outer.Identity()]
	if outerLifecycle.State != arch.ModuleFinalizedState || outerLifecycle.Namable ||
		outerLifecycle.FinishEventID == "" {
		t.Fatalf("completed dynamic initializer retained temporary execution root=%#v", outerLifecycle)
	}
	outerClosing := sourceNamedEvents(first.Poset, outer.Identity(), "Closing")
	outerFinish, _ := first.Poset.Get(gorapide.EventID(outerLifecycle.FinishEventID))
	if len(outerClosing) != 1 || outerFinish == nil {
		t.Fatalf("outer function-local owner Closing/Finish=%#v/%#v", outerClosing, outerFinish)
	}
	assertOnlyDirectCause(t, first.Poset, outerClosing[0], outerEvents[0])
	assertOnlyDirectCause(t, first.Poset, outerFinish, outerClosing[0])
	for _, local := range locals {
		lifecycle := lifecycles[local.Identity()]
		if lifecycle.Parent != outer.Identity() || lifecycle.State != arch.ModuleFinalizedState ||
			lifecycle.Namable || lifecycle.FinishEventID == "" {
			t.Fatalf("dynamic initializer function-local lifecycle=%#v", lifecycle)
		}
		var lexical arch.ModuleNameRecord
		for _, name := range lifecycle.Names {
			if name.Kind == "function-local" {
				lexical = name
			}
		}
		if lexical.NameID == "" || lexical.Owner != outer.Identity() || lexical.Live ||
			len(lexical.LostAfter) != 1 || lexical.LostAfter[0] != string(localEvents[1].ID) {
			t.Fatalf("dynamic initializer function-local name=%#v", lexical)
		}
		finish, _ := first.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
		if finish == nil {
			t.Fatalf("dynamic initializer function local %q missing Finish", local.Identity())
		}
		closing := sourceNamedEvents(first.Poset, local.Identity(), "Closing")
		if len(closing) != 1 {
			t.Fatalf("dynamic initializer function local %q Closing=%#v", local.Identity(), closing)
		}
		assertOnlyDirectCause(t, first.Poset, closing[0], localEvents[1])
		assertOnlyDirectCause(t, first.Poset, finish, closing[0])
		if !first.Poset.IsCausallyIndependent(finish.ID, returns[0].ID) {
			t.Fatalf("function-local Finish %s falsely ordered with Return %s", finish.ID, returns[0].ID)
		}
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed dynamic initializer function-local artifact bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("dynamic initializer function-local replay changed artifact bytes")
	}
}

func TestSourceAllocatedModuleInitialLaterFunctionLocalFailureReleasesEarlierLocal(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Before(depth : Integer);
  action out Allocated(value : Factory);
  action out Caught();
  action out Closing();
  provides Spawn : function();
  provides Initialize : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New(Depth is 2)); end function Spawn;
  Initialize : function() is
    First : Factory is New(Depth is 1);
    Second : Factory is New(Depth is 0);
  begin
    Allocated(First);
    Allocated(Second);
  end function Initialize;
initial (Depth : Integer is -1)
  if Depth >= 0 then Before(Depth); end if;
  if Depth = 2 then
    do Initialize(); handler is Failure => Caught(); end do;
  elsif Depth = 0 then
    raise Failure;
  end if;
final
  Closing();
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 160, MaxStatements: 200},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	byDepth := make(map[int]arch.ModuleLifecycleRecord)
	var failure *gorapide.Event
	for _, lifecycle := range first.Modules {
		if lifecycle.Kind != "allocator-module" {
			continue
		}
		before := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Before")
		if len(before) != 1 {
			t.Fatalf("function-local failure module %q Before=%#v", lifecycle.ModuleID, before)
		}
		depth := before[0].ParamInt("depth")
		byDepth[depth] = lifecycle
		if depth == 0 {
			failures := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Failure")
			if len(failures) != 1 {
				t.Fatalf("later function-local failure=%#v", failures)
			}
			failure = failures[0]
		}
	}
	if len(byDepth) != 3 || failure == nil {
		t.Fatalf("later function-local failure lifecycles=%#v failure=%#v", byDepth, failure)
	}
	earlier := byDepth[1]
	failed := byDepth[0]
	outer := byDepth[2]
	if earlier.State != arch.ModuleFinalizedState || earlier.TerminationEventID != "" ||
		earlier.FinishEventID == "" || earlier.Namable {
		t.Fatalf("earlier function local lifecycle=%#v", earlier)
	}
	earlierClosing := sourceNamedEvents(first.Poset, earlier.ModuleID, "Closing")
	earlierFinish, _ := first.Poset.Get(gorapide.EventID(earlier.FinishEventID))
	if len(earlierClosing) != 1 || earlierFinish == nil {
		t.Fatalf("earlier function local Closing/Finish=%#v/%#v", earlierClosing, earlierFinish)
	}
	assertOnlyDirectCause(t, first.Poset, earlierClosing[0], failure)
	assertOnlyDirectCause(t, first.Poset, earlierFinish, earlierClosing[0])
	for _, lifecycle := range []arch.ModuleLifecycleRecord{failed, outer} {
		if lifecycle.State != arch.ModuleFinalizedState || lifecycle.TerminationEventID != string(failure.ID) ||
			lifecycle.FinishEventID == "" || lifecycle.Namable {
			t.Fatalf("failed function-local creation lifecycle=%#v", lifecycle)
		}
		if closing := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Closing"); len(closing) != 0 {
			t.Fatalf("failed creation ran user final part in %q: %#v", lifecycle.ModuleID, closing)
		}
	}
	var lexical arch.ModuleNameRecord
	for _, name := range earlier.Names {
		if name.Kind == "function-local" {
			lexical = name
		}
	}
	if lexical.NameID == "" || lexical.Owner != outer.ModuleID || lexical.Live ||
		len(lexical.LostAfter) != 1 || lexical.LostAfter[0] != string(failure.ID) {
		t.Fatalf("earlier function local failure cleanup=%#v", lexical)
	}
	if events := sourceNamedEvents(first.Poset, outer.ModuleID, "Allocated"); len(events) != 0 {
		t.Fatalf("failed local declaration executed function body=%#v", events)
	}
	if caught := sourceNamedEvents(first.Poset, outer.ModuleID, "Caught"); len(caught) != 0 {
		t.Fatalf("lexical handler caught module-level failed creation=%#v", caught)
	}
	if returns := sourceNamedEvents(first.Poset, outer.ModuleID, "Initialize'Return"); len(returns) != 0 {
		t.Fatalf("failed local declaration returned=%#v", returns)
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed later function-local failure cleanup")
	}
}
