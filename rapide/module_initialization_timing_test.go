package rapide

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceAllocatedModuleInitialDeferredActionRetainsDynamicIdentity(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Deferred();
  action out Routed();
  action out Initialized();
  action out Finalized();
  action out Allocated(value : Factory);
  provides Spawn : function();
  provides Schedule : function();
end interface Factory;
type Driver is interface
  action in Trigger();
  requires Spawn : function();
end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  C : Clock is MakeClock();
  Two : C.Ticks is 2;
  Spawn : function() is
    Child : Factory is New();
  begin
    Allocated(Child);
  end function Spawn;
  Schedule : function() is begin Deferred() in Two; end function Schedule;
connect
  Deferred => Routed;
initial
  Schedule();
  Initialized();
final
  Finalized();
end module FactoryModule;

module DriverModule() return Driver is
  serial when Trigger() do Spawn(); end when;
end module DriverModule;
architecture System() is
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
  stimulus : Stimulus;
connect
  stimulus.Trigger to driver.Trigger;
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
		digest, arch.ExecutionLimits{MaxFirings: 120, MaxStatements: 180},
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
	if len(allocated) != 1 {
		t.Fatalf("dynamic timed initializer Allocated events=%#v", allocated)
	}
	value, exists := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || child.Identity() == "" {
		t.Fatalf("dynamic timed initializer child=%#v", value)
	}
	childID := child.Identity()
	deferred := sourceNamedEvents(first.Poset, childID, "Deferred")
	routed := sourceNamedEvents(first.Poset, childID, "Routed")
	initialized := sourceNamedEvents(first.Poset, childID, "Initialized")
	finalized := sourceNamedEvents(first.Poset, childID, "Finalized")
	scheduleCalls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, childID, "Schedule'Call"))
	scheduleReturns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, childID, "Schedule'Return"))
	if len(deferred) != 1 || len(routed) != 1 || len(initialized) != 1 || len(finalized) != 1 {
		t.Fatalf("dynamic Deferred/Routed/Initialized/Finalized=%#v/%#v/%#v/%#v",
			deferred, routed, initialized, finalized)
	}
	if len(scheduleCalls) != 1 || len(scheduleReturns) != 1 ||
		!first.Poset.IsCausallyBefore(scheduleCalls[0].ID, scheduleReturns[0].ID) ||
		!first.Poset.IsCausallyBefore(scheduleReturns[0].ID, initialized[0].ID) {
		t.Fatalf("dynamic Schedule Call/Return/Initialized=%#v/%#v/%#v",
			scheduleCalls, scheduleReturns, initialized)
	}
	clockID := arch.ClockID(childID, "C")
	timing, related := deferred[0].Timing(clockID)
	if !related || timing.Start != 2 || timing.Finish != 2 {
		t.Fatalf("dynamic deferred timing=%#v related=%t clock=%s", timing, related, clockID)
	}
	if !first.Poset.IsCausallyBefore(initialized[0].ID, allocated[0].ID) ||
		!first.Poset.IsCausallyBefore(initialized[0].ID, deferred[0].ID) ||
		!first.Poset.IsCausallyBefore(deferred[0].ID, routed[0].ID) {
		t.Fatal("dynamic initializer completion does not precede publication and deferred release")
	}

	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range first.Modules {
		if candidate.ModuleID == childID {
			lifecycle = candidate
			break
		}
	}
	if lifecycle.ModuleID == "" || lifecycle.State != arch.ModuleFinalizedState ||
		lifecycle.Namable || lifecycle.FinishEventID == "" {
		t.Fatalf("dynamic scheduled lifecycle=%#v", lifecycle)
	}
	finish, exists := first.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if !exists {
		t.Fatalf("dynamic scheduled Finish %q is absent", lifecycle.FinishEventID)
	}
	finalCauses := first.Poset.DirectCauses(finalized[0].ID)
	if len(finalCauses) != 3 ||
		!eventSetContainsID(finalCauses, initialized[0].ID) ||
		!eventSetContainsID(finalCauses, allocated[0].ID) ||
		!eventSetContainsID(finalCauses, deferred[0].ID) {
		t.Fatalf("dynamic final name-loss frontier=%#v", finalCauses)
	}
	assertOnlyDirectCause(t, first.Poset, finish, finalized[0])

	resultNames, localNames, scheduleNames := 0, 0, 0
	for _, name := range lifecycle.Names {
		switch name.Kind {
		case "allocator-result":
			resultNames++
			if name.Live || len(name.LostAfter) != 1 || name.LostAfter[0] != string(initialized[0].ID) {
				t.Fatalf("allocator result name=%#v", name)
			}
		case "function-local":
			localNames++
			if name.Live || len(name.LostAfter) != 1 || name.LostAfter[0] != string(allocated[0].ID) {
				t.Fatalf("function-local name=%#v", name)
			}
		case "scheduled-action":
			scheduleNames++
			if name.Live || name.Owner != "$scheduler" ||
				len(name.LostAfter) != 1 || name.LostAfter[0] != string(deferred[0].ID) {
				t.Fatalf("scheduled action name=%#v", name)
			}
		}
	}
	if resultNames != 1 || localNames != 1 || scheduleNames != 1 {
		t.Fatalf("dynamic allocator/local/schedule names=%d/%d/%d",
			resultNames, localNames, scheduleNames)
	}

	clockFound, scheduleFound, finalizationFound := false, false, false
	for _, clock := range first.Clocks {
		if clock.Clock == clockID && clock.Owner == childID && clock.Name == "C" && clock.Now == "2" {
			clockFound = true
		}
	}
	for _, scheduled := range first.ScheduledEvents {
		if scheduled.Component == childID && scheduled.Clock == clockID &&
			scheduled.Tick == "2" && scheduled.EventID == string(deferred[0].ID) {
			scheduleFound = true
		}
	}
	for _, firing := range first.Firings {
		if firing.Transition == "scheduled-finalization" && firing.Target == childID &&
			firing.TriggerID == string(deferred[0].ID) {
			finalizationFound = true
		}
	}
	if !clockFound || !scheduleFound || !finalizationFound {
		t.Fatalf("dynamic clock/schedule/finalization audit=%t/%t/%t",
			clockFound, scheduleFound, finalizationFound)
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
		t.Fatal("GOMAXPROCS changed dynamic initializer deferred-action artifact")
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
		t.Fatal("dynamic initializer deferred-action replay changed canonical bytes")
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
		t.Fatalf("dynamic initializer deferred-action exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}
