package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceAllocatedModuleInitialFunctionsUseDynamicIdentity(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Inside(value : Integer);
  action out Initialized(value : Integer; touches : Integer);
  action out Allocated(value : Factory);
  requires Calculate : function(value : Integer) return Integer;
  provides Compute : function(value : Integer) return Integer;
  provides Touch : function();
  provides Spawn : function(Value : Integer);
end interface Factory;
type Driver is interface
  action in Trigger(value : Integer);
  requires Spawn : function(Value : Integer);
end interface Driver;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;

module FactoryModule() return Factory is
  result : var Integer := 0;
  touches : var Integer := 0;
  Compute : function(value : Integer) return Integer is
  begin
    Inside(value);
    return value + 3;
  end function Compute;
  Touch : function() is begin touches := $touches + 1; end function Touch;
  Spawn : function(Value : Integer) is begin Allocated(New(Seed is Value)); end function Spawn;
connect
  Calculate to Compute;
initial (Seed : Integer is 4)
  result := Calculate(Seed);
  Touch();
  Initialized($result, $touches);
end module FactoryModule;

module DriverModule() return Driver is
serial when (?Value : Integer) Trigger(?Value) do Spawn(?Value); end when;
end module DriverModule;

architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  (?Value : Integer) stimulus.Trigger(?Value) => driver.Trigger(?Value);
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
		digest, arch.ExecutionLimits{MaxFirings: 100, MaxStatements: 100},
		arch.InputEvent{
			Key: "trigger", Source: "stimulus", Action: "Trigger",
			Params: map[string]any{"value": 6},
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

	static := sourceNamedEvents(first.Poset, "factory", "Initialized")
	if len(static) != 1 || static[0].ParamInt("value") != 7 || static[0].ParamInt("touches") != 1 {
		t.Fatalf("static function initialization=%#v", static)
	}
	allocated := sourceNamedEvents(first.Poset, "factory", "Allocated")
	if len(allocated) != 1 {
		t.Fatalf("allocated events=%#v", allocated)
	}
	value, present := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !present || !ok || child.Identity() == "" {
		t.Fatalf("allocated child=%#v", value)
	}
	initialized := sourceNamedEvents(first.Poset, child.Identity(), "Initialized")
	calculateCall := sourceNamedEvents(first.Poset, child.Identity(), "Calculate'Call")
	inside := sourceNamedEvents(first.Poset, child.Identity(), "Inside")
	calculateReturn := sourceNamedEvents(first.Poset, child.Identity(), "Calculate'Return")
	touchCall := sourceNamedEvents(first.Poset, child.Identity(), "Touch'Call")
	touchReturn := sourceNamedEvents(first.Poset, child.Identity(), "Touch'Return")
	if len(initialized) != 1 || initialized[0].ParamInt("value") != 9 ||
		initialized[0].ParamInt("touches") != 1 || len(calculateCall) != 1 ||
		len(inside) != 1 || len(calculateReturn) != 1 || len(touchCall) != 1 ||
		len(touchReturn) != 1 {
		t.Fatalf("dynamic function initialization events initialized=%#v call=%#v inside=%#v return=%#v touch=%#v/%#v",
			initialized, calculateCall, inside, calculateReturn, touchCall, touchReturn)
	}
	for _, pair := range [][2]*gorapide.Event{
		{calculateCall[0], inside[0]},
		{inside[0], calculateReturn[0]},
		{calculateReturn[0], touchCall[0]},
		{touchCall[0], touchReturn[0]},
		{touchReturn[0], initialized[0]},
		{initialized[0], allocated[0]},
	} {
		if !first.Poset.IsCausallyBefore(pair[0].ID, pair[1].ID) {
			t.Fatalf("missing dynamic initializer function causality %s -> %s", pair[0].ID, pair[1].ID)
		}
	}
	if !calculateCall[0].HasObservation(child.Identity(), "Compute'Call") ||
		!calculateReturn[0].HasObservation(child.Identity(), "Compute'Return") {
		t.Fatalf("dynamic self-route observations call=%#v return=%#v",
			calculateCall[0].ObservationViews(), calculateReturn[0].ObservationViews())
	}
	stateValues := make(map[string]string)
	for _, state := range first.State {
		if state.ComponentID == child.Identity() {
			stateValues[state.Name] = state.Value.Text
			if state.Version != 1 {
				t.Fatalf("child state %s version=%d, want 1", state.Name, state.Version)
			}
		}
	}
	if stateValues["result"] != "9" || stateValues["touches"] != "1" {
		t.Fatalf("dynamic initializer function state=%#v", stateValues)
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
		t.Fatal("GOMAXPROCS changed dynamic initializer function artifact bytes")
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
		t.Fatal("dynamic initializer function replay changed artifact bytes")
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
		t.Fatalf("dynamic initializer function exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourceAllocatedModuleInitialFunctionRejectsExistingModuleLocal(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  provides Initialize : function();
  provides Spawn : function();
end interface Factory;
module FactoryModule() return Factory is
  Initialize : function() is
    Child : Factory is Self;
  begin
    null;
  end function Initialize;
  Spawn : function() is begin Allocated(New()); end function Spawn;
initial
  Initialize();
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
`)
	_, err := Compile(source, "System")
	if err == nil || !strings.Contains(err.Error(), "requires a direct owner allocator New initializer") {
		t.Fatalf("Compile()=%v, want explicit existing-module function-local gate", err)
	}
}
