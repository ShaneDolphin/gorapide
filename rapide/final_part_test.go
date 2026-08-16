package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourcePassiveNewExecutesFinalPartBeforeFinish(t *testing.T) {
	source := []byte(`
type Factory is interface
	  action out Allocated(value : Factory);
	  action out Closing(step : Integer);
	  private action Quiet(step : Integer);
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
final
	  Closing(1);
	  Quiet(9);
	  Closing(2);
end module FactoryModule;
module DriverModule() return Driver is
serial when Trigger() do Spawn(); end when;
end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  stimulus.Trigger => driver.Trigger;
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
	journal := arch.NewExecutionJournal(digest, 40,
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

	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 1 || len(returns) != 1 {
		t.Fatalf("Allocated/Spawn'Return counts=%d/%d", len(allocated), len(returns))
	}
	value, exists := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v, want allocated module", value)
	}
	closing := sourceNamedEvents(result.Poset, module.Identity(), "Closing")
	quiet := sourceNamedEvents(result.Poset, module.Identity(), "Quiet")
	if len(closing) != 2 {
		t.Fatalf("dynamic module Closing count=%d, want 2", len(closing))
	}
	if len(quiet) != 1 {
		t.Fatalf("dynamic module private Quiet count=%d, want 1", len(quiet))
	}
	var closingOne, closingTwo *gorapide.Event
	for _, event := range closing {
		step, _ := event.Param("step")
		switch step {
		case int64(1):
			closingOne = event
		case int64(2):
			closingTwo = event
		}
	}
	if closingOne == nil || closingTwo == nil {
		t.Fatalf("final Closing values=%#v", closing)
	}
	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == module.Identity() {
			lifecycle = candidate
			break
		}
	}
	finish, present := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if lifecycle.State != arch.ModuleFinalizedState || !present {
		t.Fatalf("dynamic lifecycle/Finish=%#v/%v", lifecycle, present)
	}
	assertOnlyDirectCause(t, result.Poset, closingOne, allocated[0])
	assertOnlyDirectCause(t, result.Poset, quiet[0], closingOne)
	assertOnlyDirectCause(t, result.Poset, closingTwo, quiet[0])
	assertOnlyDirectCause(t, result.Poset, finish, closingTwo)
	assertOnlyDirectCause(t, result.Poset, returns[0], allocated[0])
	if !result.Poset.IsCausallyIndependent(returns[0].ID, closingOne.ID) ||
		!result.Poset.IsCausallyIndependent(returns[0].ID, quiet[0].ID) ||
		!result.Poset.IsCausallyIndependent(returns[0].ID, finish.ID) {
		t.Fatal("enclosing function return was falsely ordered with the passive module final part")
	}
	var context *arch.CommunicationContextRecord
	for index := range result.Contexts {
		if result.Contexts[index].Source == module.Identity() && result.Contexts[index].Kind == "initial-parent" {
			context = &result.Contexts[index]
			break
		}
	}
	if context == nil || context.Live || len(context.LostAfter) != 1 || context.LostAfter[0] != string(closingTwo.ID) {
		t.Fatalf("finalized communication Context=%#v", context)
	}

	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed final-part execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("final-part replay changed canonical artifact bytes")
	}
}

func TestSourcePassiveNewFinalCallsLocalProvidedFunctionAtDynamicIdentity(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Closing(step : Integer);
  action out After();
  provides Spawn : function();
  provides Cleanup : function(step : Integer) return Integer;
  provides Mark : function(step : Integer);
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
  Cleanup : function(step : Integer) return Integer is
    begin
      Mark(step);
      if step = 4 then
        return step + 1;
      end if;
      return 0;
    end function Cleanup;
  Mark : function(step : Integer) is
    begin
      Closing(step);
    end function Mark;
final
  Cleanup(4);
  After();
end module FactoryModule;
module DriverModule() return Driver is
serial when Trigger() do Spawn(); end when;
end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  stimulus.Trigger => driver.Trigger;
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
	journal := arch.NewExecutionJournal(digest, 50,
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

	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	spawnReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 1 || len(spawnReturns) != 1 {
		t.Fatalf("Allocated/Spawn'Return=%d/%d", len(allocated), len(spawnReturns))
	}
	value, exists := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v, want allocated module", value)
	}
	cleanupCalls := sourceNamedEvents(result.Poset, module.Identity(), "Cleanup'Call")
	markCalls := sourceNamedEvents(result.Poset, module.Identity(), "Mark'Call")
	closing := sourceNamedEvents(result.Poset, module.Identity(), "Closing")
	markReturns := sourceNamedEvents(result.Poset, module.Identity(), "Mark'Return")
	cleanupReturns := sourceNamedEvents(result.Poset, module.Identity(), "Cleanup'Return")
	after := sourceNamedEvents(result.Poset, module.Identity(), "After")
	if len(cleanupCalls) != 1 || len(markCalls) != 1 || len(closing) != 1 ||
		len(markReturns) != 1 || len(cleanupReturns) != 1 || len(after) != 1 {
		t.Fatalf("dynamic final function events cleanup-call=%d mark-call=%d closing=%d mark-return=%d cleanup-return=%d after=%d",
			len(cleanupCalls), len(markCalls), len(closing), len(markReturns), len(cleanupReturns), len(after))
	}
	if cleanupCalls[0].ParamInt("step") != 4 || markCalls[0].ParamInt("step") != 4 ||
		closing[0].ParamInt("step") != 4 || cleanupReturns[0].ParamInt("Return") != 5 {
		t.Fatalf("dynamic final function values call=%#v/%#v closing=%#v return=%#v",
			cleanupCalls[0].Params, markCalls[0].Params, closing[0].Params, cleanupReturns[0].Params)
	}
	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == module.Identity() {
			lifecycle = candidate
			break
		}
	}
	finish, present := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if lifecycle.State != arch.ModuleFinalizedState || !present {
		t.Fatalf("dynamic lifecycle/Finish=%#v/%v", lifecycle, present)
	}
	assertOnlyDirectCause(t, result.Poset, cleanupCalls[0], allocated[0])
	assertOnlyDirectCause(t, result.Poset, markCalls[0], cleanupCalls[0])
	assertOnlyDirectCause(t, result.Poset, closing[0], markCalls[0])
	assertOnlyDirectCause(t, result.Poset, markReturns[0], closing[0])
	assertOnlyDirectCause(t, result.Poset, cleanupReturns[0], markReturns[0])
	assertOnlyDirectCause(t, result.Poset, after[0], cleanupReturns[0])
	assertOnlyDirectCause(t, result.Poset, finish, after[0])
	assertOnlyDirectCause(t, result.Poset, spawnReturns[0], allocated[0])
	if !result.Poset.IsCausallyIndependent(spawnReturns[0].ID, cleanupCalls[0].ID) ||
		!result.Poset.IsCausallyIndependent(spawnReturns[0].ID, finish.ID) {
		t.Fatal("enclosing return was falsely ordered with final local-function execution")
	}

	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed dynamic final local-function execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("dynamic final local-function replay changed canonical bytes")
	}
	explorationLimits := arch.ExplorationLimits{MaxExecutions: 32, MaxChoiceDepth: 12}
	explored, err := model.ExploreDeterministic(journal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(journal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if len(explored.Computations) != 1 || !bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("dynamic final local-function exploration changed: computations=%d", len(explored.Computations))
	}
}

func TestSourcePassiveNewFinalReevaluatesGeneratorFunctionConnection(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Closing(step : Integer);
  provides Spawn : function();
  requires Remote : function(step : Integer) return Integer;
  provides Local : function(step : Integer) return Integer;
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
  Local : function(step : Integer) return Integer is
    begin
      Closing(step);
      return step + 1;
    end function Local;
connect
  Remote to Local;
final
  Remote(6);
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
	journal := arch.NewExecutionJournal(digest, 50,
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
	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	spawnReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 1 || len(spawnReturns) != 1 {
		t.Fatalf("Allocated/Spawn'Return=%d/%d", len(allocated), len(spawnReturns))
	}
	value, _ := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v", value)
	}
	requiredCalls := sourceNamedEvents(result.Poset, module.Identity(), "Remote'Call")
	providedCalls := sourceNamedEvents(result.Poset, module.Identity(), "Local'Call")
	closing := sourceNamedEvents(result.Poset, module.Identity(), "Closing")
	requiredReturns := sourceNamedEvents(result.Poset, module.Identity(), "Remote'Return")
	providedReturns := sourceNamedEvents(result.Poset, module.Identity(), "Local'Return")
	if len(requiredCalls) != 1 || len(providedCalls) != 1 || len(closing) != 1 ||
		len(requiredReturns) != 1 || len(providedReturns) != 1 {
		t.Fatalf("dynamic module-route events call=%d/%d closing=%d return=%d/%d",
			len(requiredCalls), len(providedCalls), len(closing), len(requiredReturns), len(providedReturns))
	}
	if requiredCalls[0].ID != providedCalls[0].ID || requiredReturns[0].ID != providedReturns[0].ID ||
		closing[0].ParamInt("step") != 6 || requiredReturns[0].ParamInt("Return") != 7 {
		t.Fatalf("dynamic module route identity/values call=%#v/%#v return=%#v/%#v closing=%#v",
			requiredCalls[0], providedCalls[0], requiredReturns[0], providedReturns[0], closing[0])
	}
	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == module.Identity() {
			lifecycle = candidate
			break
		}
	}
	finish, present := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if lifecycle.State != arch.ModuleFinalizedState || !present {
		t.Fatalf("dynamic lifecycle/Finish=%#v/%v", lifecycle, present)
	}
	assertOnlyDirectCause(t, result.Poset, requiredCalls[0], allocated[0])
	assertOnlyDirectCause(t, result.Poset, closing[0], requiredCalls[0])
	assertOnlyDirectCause(t, result.Poset, requiredReturns[0], closing[0])
	assertOnlyDirectCause(t, result.Poset, finish, requiredReturns[0])
	assertOnlyDirectCause(t, result.Poset, spawnReturns[0], allocated[0])
	if !result.Poset.IsCausallyIndependent(spawnReturns[0].ID, requiredCalls[0].ID) ||
		!result.Poset.IsCausallyIndependent(spawnReturns[0].ID, finish.ID) {
		t.Fatal("enclosing return was falsely ordered with dynamic module function route")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed dynamic module function route")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("dynamic module function route replay changed canonical bytes")
	}
	explorationLimits := arch.ExplorationLimits{MaxExecutions: 64, MaxChoiceDepth: 16}
	explored, err := model.ExploreDeterministic(journal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(journal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if len(explored.Computations) != 1 || !bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("dynamic module function route exploration changed: computations=%d", len(explored.Computations))
	}
	for _, event := range []*gorapide.Event{requiredCalls[0], closing[0], requiredReturns[0], finish} {
		if event.Source != module.Identity() {
			t.Fatalf("dynamic module route event %s source=%q", event.Name, event.Source)
		}
	}
}

func TestSourcePassiveNewFinalReevaluatesGeneratorActionConnection(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Closing(step : Integer);
  action out Routed(step : Integer);
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Sink is interface action in Leaked(step : Integer); end interface Sink;

module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
connect
  Closing to Routed;
final
  Closing(8);
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
  sink : Sink;
connect
  stimulus.Trigger => driver.Trigger;
  driver.Spawn to factory.Spawn;
  factory.Routed => sink.Leaked;
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
	journal := arch.NewExecutionJournal(digest, 50,
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
	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	spawnReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 1 || len(spawnReturns) != 1 {
		t.Fatalf("Allocated/Spawn'Return=%d/%d", len(allocated), len(spawnReturns))
	}
	value, _ := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v", value)
	}
	closing := sourceNamedEvents(result.Poset, module.Identity(), "Closing")
	routed := sourceNamedEvents(result.Poset, module.Identity(), "Routed")
	leaked := sourceNamedEvents(result.Poset, "sink", "Leaked")
	if len(closing) != 1 || len(routed) != 1 || len(leaked) != 0 {
		t.Fatalf("dynamic action route Closing/Routed/Leaked=%d/%d/%d", len(closing), len(routed), len(leaked))
	}
	if closing[0].ID != routed[0].ID || closing[0].ParamInt("step") != 8 || routed[0].ParamInt("step") != 8 {
		t.Fatalf("dynamic basic route identity/values Closing=%#v Routed=%#v", closing[0], routed[0])
	}
	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == module.Identity() {
			lifecycle = candidate
			break
		}
	}
	finish, present := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if lifecycle.State != arch.ModuleFinalizedState || !present {
		t.Fatalf("dynamic lifecycle/Finish=%#v/%v", lifecycle, present)
	}
	assertOnlyDirectCause(t, result.Poset, closing[0], allocated[0])
	assertOnlyDirectCause(t, result.Poset, finish, closing[0])
	assertOnlyDirectCause(t, result.Poset, spawnReturns[0], allocated[0])
	if !result.Poset.IsCausallyIndependent(spawnReturns[0].ID, closing[0].ID) ||
		!result.Poset.IsCausallyIndependent(spawnReturns[0].ID, finish.ID) {
		t.Fatal("enclosing return was falsely ordered with dynamic module action route")
	}
	foundRoute := false
	for _, firing := range result.Firings {
		if firing.ConnectionScope == arch.ModuleConnectionScope.String() &&
			firing.TriggerSource == module.Identity() {
			foundRoute = firing.ConnectionKind == arch.BasicConnection.String() &&
				firing.Target == module.Identity() && firing.TriggerID == string(closing[0].ID) &&
				firing.ResultID == string(closing[0].ID)
		}
	}
	if !foundRoute {
		t.Fatalf("dynamic module action route audit=%#v", result.Firings)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed dynamic module action route")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("dynamic module action route replay changed canonical bytes")
	}
	explorationLimits := arch.ExplorationLimits{MaxExecutions: 32, MaxChoiceDepth: 12}
	explored, err := model.ExploreDeterministic(journal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(journal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if len(explored.Computations) != 1 || !bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("dynamic module action route exploration changed: computations=%d", len(explored.Computations))
	}
}

func TestSourcePassiveNewFinalReevaluatesMappedBasicActionConnection(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Closing(step : Integer);
  action out Routed(value : Integer);
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
connect
  (?N : Integer) Closing(?N) where ?N > 0 to Routed(?N + 1);
final
  Closing(-1);
  Closing(8);
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
	journal := arch.NewExecutionJournal(digest, 50,
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
	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	spawnReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 1 || len(spawnReturns) != 1 {
		t.Fatalf("Allocated/Spawn'Return=%d/%d", len(allocated), len(spawnReturns))
	}
	value, _ := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v", value)
	}
	closing := sourceNamedEvents(result.Poset, module.Identity(), "Closing")
	routed := sourceNamedEvents(result.Poset, module.Identity(), "Routed")
	if len(closing) != 2 || len(routed) != 1 {
		t.Fatalf("mapped dynamic action route Closing/Routed=%d/%d", len(closing), len(routed))
	}
	var blocked, selected *gorapide.Event
	for _, event := range closing {
		switch event.ParamInt("step") {
		case -1:
			blocked = event
		case 8:
			selected = event
		}
	}
	if blocked == nil || selected == nil || routed[0].ID != selected.ID || routed[0].ParamInt("value") != 9 {
		t.Fatalf("mapped dynamic route blocked=%#v selected=%#v routed=%#v", blocked, selected, routed[0])
	}
	var route *arch.FiringRecord
	for index := range result.Firings {
		firing := &result.Firings[index]
		if firing.ConnectionScope == arch.ModuleConnectionScope.String() &&
			firing.TriggerSource == module.Identity() {
			route = firing
			break
		}
	}
	if route == nil || route.ConnectionKind != arch.BasicConnection.String() ||
		route.TriggerID != string(selected.ID) || route.ResultID != string(selected.ID) ||
		len(route.Bindings) != 1 || route.Bindings[0].Placeholder != "N" || route.Bindings[0].Value.Text != "8" {
		t.Fatalf("mapped dynamic route audit=%#v", route)
	}
	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == module.Identity() {
			lifecycle = candidate
			break
		}
	}
	finish, present := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if lifecycle.State != arch.ModuleFinalizedState || !present {
		t.Fatalf("dynamic lifecycle/Finish=%#v/%v", lifecycle, present)
	}
	assertOnlyDirectCause(t, result.Poset, blocked, allocated[0])
	assertOnlyDirectCause(t, result.Poset, selected, blocked)
	assertOnlyDirectCause(t, result.Poset, finish, selected)
	assertOnlyDirectCause(t, result.Poset, spawnReturns[0], allocated[0])
	if !result.Poset.IsCausallyIndependent(spawnReturns[0].ID, selected.ID) ||
		!result.Poset.IsCausallyIndependent(spawnReturns[0].ID, finish.ID) {
		t.Fatal("enclosing return was falsely ordered with mapped dynamic module action route")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed mapped dynamic module action route")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("mapped dynamic module action route replay changed canonical bytes")
	}
	explorationLimits := arch.ExplorationLimits{MaxExecutions: 32, MaxChoiceDepth: 12}
	explored, err := model.ExploreDeterministic(journal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(journal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if len(explored.Computations) != 1 || !bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("mapped dynamic module action route exploration changed: computations=%d", len(explored.Computations))
	}
}

func TestSourcePassiveNewActionRoutesAreIsolatedAcrossAllocations(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Closing(step : Integer);
  action out Routed(step : Integer);
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); Allocated(New()); end function Spawn;
connect Closing to Routed;
final Closing(1);
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
	afterDigest, err := model.DeterministicModelDigest()
	if err != nil || afterDigest != digest {
		t.Fatalf("dynamic route registration mutated model digest before=%q after=%q err=%v", digest, afterDigest, err)
	}
	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	if len(allocated) != 2 {
		t.Fatalf("Allocated count=%d, want 2", len(allocated))
	}
	moduleIDs := make([]string, 0, 2)
	closingEvents := make([]*gorapide.Event, 0, 2)
	for _, event := range allocated {
		value, _ := event.Param("value")
		module, ok := value.(gorapide.RapideModuleValue)
		if !ok || module.Identity() == "" {
			t.Fatalf("Allocated value=%#v", value)
		}
		moduleID := module.Identity()
		moduleIDs = append(moduleIDs, moduleID)
		closing := sourceNamedEvents(result.Poset, moduleID, "Closing")
		routed := sourceNamedEvents(result.Poset, moduleID, "Routed")
		if len(closing) != 1 || len(routed) != 1 || closing[0].ID != routed[0].ID ||
			closing[0].ParamInt("step") != 1 || routed[0].ParamInt("step") != 1 {
			t.Fatalf("allocation %s Closing/Routed=%#v/%#v", moduleID, closing, routed)
		}
		closingEvents = append(closingEvents, closing[0])
		found := false
		for _, firing := range result.Firings {
			if firing.ConnectionScope == arch.ModuleConnectionScope.String() &&
				firing.TriggerSource == moduleID {
				found = firing.Target == moduleID && firing.TriggerID == string(closing[0].ID) &&
					firing.ResultID == string(closing[0].ID)
			}
		}
		if !found {
			t.Fatalf("allocation %s has no isolated route firing", moduleID)
		}
	}
	if moduleIDs[0] == moduleIDs[1] || closingEvents[0].ID == closingEvents[1].ID {
		t.Fatalf("allocations/routes collapsed modules=%v events=%v", moduleIDs,
			[]gorapide.EventID{closingEvents[0].ID, closingEvents[1].ID})
	}
	if !result.Poset.IsCausallyIndependent(closingEvents[0].ID, closingEvents[1].ID) {
		t.Fatal("separate passive-child final routes acquired a traversal-order causal edge")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed multi-allocation dynamic routes")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("multi-allocation dynamic route replay changed canonical bytes")
	}
}

func TestSourcePassiveNewFinalReevaluatesSingletonPipeAndAgentConnections(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Closing(step : Integer);
  action out Piped(step : Integer);
  action out Agented(step : Integer);
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
connect
  (?N : Integer) Closing(?N) => Piped(?N);
  (?M : Integer) Closing(?M) ||> Agented(?M);
final
  Closing(1);
  Closing(2);
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
	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	spawnReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 1 || len(spawnReturns) != 1 {
		t.Fatalf("Allocated/Spawn'Return=%d/%d", len(allocated), len(spawnReturns))
	}
	value, _ := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v", value)
	}
	moduleID := module.Identity()
	byStep := func(name string) map[int]*gorapide.Event {
		indexed := make(map[int]*gorapide.Event, 2)
		for _, event := range sourceNamedEvents(result.Poset, moduleID, name) {
			indexed[event.ParamInt("step")] = event
		}
		return indexed
	}
	closing, piped, agented := byStep("Closing"), byStep("Piped"), byStep("Agented")
	for _, step := range []int{1, 2} {
		if closing[step] == nil || piped[step] == nil || agented[step] == nil {
			t.Fatalf("step %d dynamic Closing/Piped/Agented=%#v/%#v/%#v", step, closing, piped, agented)
		}
	}
	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == moduleID {
			lifecycle = candidate
			break
		}
	}
	finish, present := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if lifecycle.State != arch.ModuleFinalizedState || !present {
		t.Fatalf("dynamic lifecycle/Finish=%#v/%v", lifecycle, present)
	}
	assertOnlyDirectCause(t, result.Poset, closing[1], allocated[0])
	assertOnlyDirectCause(t, result.Poset, closing[2], closing[1])
	assertOnlyDirectCause(t, result.Poset, piped[1], closing[1])
	assertOnlyDirectCause(t, result.Poset, agented[1], closing[1])
	assertOnlyDirectCause(t, result.Poset, agented[2], closing[2])
	assertOnlyDirectCause(t, result.Poset, finish, closing[2])
	pipedTwoCauses := result.Poset.DirectCauses(piped[2].ID)
	pipedTwoCauseIDs := make(map[gorapide.EventID]bool, len(pipedTwoCauses))
	for _, cause := range pipedTwoCauses {
		pipedTwoCauseIDs[cause.ID] = true
	}
	if len(pipedTwoCauses) != 2 || !pipedTwoCauseIDs[closing[2].ID] || !pipedTwoCauseIDs[piped[1].ID] {
		t.Fatalf("second pipe output direct causes=%#v, want Closing(2) and Piped(1)", pipedTwoCauses)
	}
	if !result.Poset.IsCausallyBefore(piped[1].ID, piped[2].ID) {
		t.Fatal("dynamic pipe route did not preserve connection-local output order")
	}
	if !result.Poset.IsCausallyIndependent(agented[1].ID, agented[2].ID) {
		t.Fatal("dynamic agent route falsely ordered distinct outputs")
	}
	for _, output := range []*gorapide.Event{piped[2], agented[2]} {
		if !result.Poset.IsCausallyIndependent(output.ID, finish.ID) {
			t.Fatalf("terminal %s output was falsely ordered with Finish", output.Name)
		}
	}
	if !result.Poset.IsCausallyIndependent(spawnReturns[0].ID, piped[2].ID) ||
		!result.Poset.IsCausallyIndependent(spawnReturns[0].ID, agented[2].ID) ||
		!result.Poset.IsCausallyIndependent(spawnReturns[0].ID, finish.ID) {
		t.Fatal("enclosing return was falsely ordered with dynamic pipe/agent final branches")
	}
	kindCounts := map[string]int{}
	for _, firing := range result.Firings {
		if firing.ConnectionScope != arch.ModuleConnectionScope.String() || firing.TriggerSource != moduleID {
			continue
		}
		kindCounts[firing.ConnectionKind]++
		if firing.Target != moduleID || firing.TriggerAction != "Closing" {
			t.Fatalf("dynamic pipe/agent route audit=%#v", firing)
		}
	}
	if kindCounts[arch.PipeConnection.String()] != 2 || kindCounts[arch.AgentConnection.String()] != 2 {
		t.Fatalf("dynamic pipe/agent firing counts=%v", kindCounts)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed dynamic pipe/agent action routes")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("dynamic pipe/agent action route replay changed canonical bytes")
	}
	explorationLimits := arch.ExplorationLimits{MaxExecutions: 4096, MaxChoiceDepth: 32}
	explored, err := model.ExploreDeterministic(journal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(journal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if len(explored.Computations) != 1 || !bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("dynamic pipe/agent action route exploration changed: computations=%d executions=%d complete=%v choices=%d",
			len(explored.Computations), explored.Executions, explored.Complete, len(result.Choices))
	}
}

func TestSourcePassiveNewFinalReevaluatesCompoundPipeAndAgentConnections(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  private action Left(step : Integer);
  private action Right(step : Integer);
  action out Piped(step : Integer);
  action out Agented(step : Integer);
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); Allocated(New()); end function Spawn;
connect
  (?N : Integer) (Left(?N) and Right(?N)) => Piped(?N);
  (?M : Integer) (Left(?M) and Right(?M)) ||> Agented(?M);
final
  Left(1);
  Right(1);
  Left(2);
  Right(2);
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
	journal := arch.NewExecutionJournal(digest, 160,
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
	afterDigest, err := model.DeterministicModelDigest()
	if err != nil || afterDigest != digest {
		t.Fatalf("dynamic compound route registration mutated model digest before=%q after=%q err=%v", digest, afterDigest, err)
	}
	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	if len(allocated) != 2 {
		t.Fatalf("Allocated count=%d, want 2", len(allocated))
	}
	moduleIDs := make([]string, 0, 2)
	firstPipes := make([]*gorapide.Event, 0, 2)
	firstAgents := make([]*gorapide.Event, 0, 2)
	for _, allocation := range allocated {
		value, _ := allocation.Param("value")
		module, ok := value.(gorapide.RapideModuleValue)
		if !ok || module.Identity() == "" {
			t.Fatalf("Allocated value=%#v", value)
		}
		moduleID := module.Identity()
		moduleIDs = append(moduleIDs, moduleID)
		byStep := func(name string) map[int]*gorapide.Event {
			indexed := make(map[int]*gorapide.Event, 2)
			for _, event := range sourceNamedEvents(result.Poset, moduleID, name) {
				indexed[event.ParamInt("step")] = event
			}
			return indexed
		}
		left, right := byStep("Left"), byStep("Right")
		piped, agented := byStep("Piped"), byStep("Agented")
		for _, step := range []int{1, 2} {
			if left[step] == nil || right[step] == nil || piped[step] == nil || agented[step] == nil {
				t.Fatalf("module %s step %d Left/Right/Piped/Agented=%#v/%#v/%#v/%#v",
					moduleID, step, left, right, piped, agented)
			}
			for _, output := range []*gorapide.Event{piped[step], agented[step]} {
				if !result.Poset.IsCausallyBefore(left[step].ID, output.ID) ||
					!result.Poset.IsCausallyBefore(right[step].ID, output.ID) {
					t.Fatalf("module %s %s(%d) does not follow its complete compound match", moduleID, output.Name, step)
				}
			}
		}
		if !result.Poset.IsCausallyBefore(piped[1].ID, piped[2].ID) {
			t.Fatalf("module %s compound pipe outputs are not connection-locally ordered", moduleID)
		}
		if !result.Poset.IsCausallyIndependent(agented[1].ID, agented[2].ID) {
			t.Fatalf("module %s compound agent outputs acquired a prior-output edge", moduleID)
		}
		var lifecycle arch.ModuleLifecycleRecord
		for _, candidate := range result.Modules {
			if candidate.ModuleID == moduleID {
				lifecycle = candidate
				break
			}
		}
		finish, present := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
		if lifecycle.State != arch.ModuleFinalizedState || !present {
			t.Fatalf("module %s lifecycle/Finish=%#v/%v", moduleID, lifecycle, present)
		}
		assertOnlyDirectCause(t, result.Poset, finish, right[2])
		if !result.Poset.IsCausallyIndependent(piped[2].ID, finish.ID) ||
			!result.Poset.IsCausallyIndependent(agented[2].ID, finish.ID) {
			t.Fatalf("module %s terminal compound outputs were falsely ordered with Finish", moduleID)
		}
		kindCounts := map[string]int{}
		for _, firing := range result.Firings {
			if firing.ConnectionScope != arch.ModuleConnectionScope.String() || firing.TriggerSource != moduleID {
				continue
			}
			kindCounts[firing.ConnectionKind]++
			if firing.Target != moduleID || len(firing.MatchedEvents) != 2 || len(firing.Bindings) != 1 {
				t.Fatalf("module %s dynamic compound firing audit=%#v", moduleID, firing)
			}
			for _, matchedID := range firing.MatchedEvents {
				matched, exists := result.Poset.Get(gorapide.EventID(matchedID))
				if !exists || matched.Source != moduleID {
					t.Fatalf("module %s firing imported nonlocal match %q", moduleID, matchedID)
				}
			}
		}
		if kindCounts[arch.PipeConnection.String()] != 2 || kindCounts[arch.AgentConnection.String()] != 2 {
			t.Fatalf("module %s dynamic compound firing counts=%v", moduleID, kindCounts)
		}
		firstPipes = append(firstPipes, piped[1])
		firstAgents = append(firstAgents, agented[1])
	}
	if moduleIDs[0] == moduleIDs[1] ||
		!result.Poset.IsCausallyIndependent(firstPipes[0].ID, firstPipes[1].ID) ||
		!result.Poset.IsCausallyIndependent(firstAgents[0].ID, firstAgents[1].ID) {
		t.Fatalf("dynamic compound route state leaked across allocations modules=%v", moduleIDs)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed dynamic compound pipe/agent action routes")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("dynamic compound pipe/agent action route replay changed canonical bytes")
	}
	explorationJournal := journal
	explorationJournal.Choices = make([]arch.ChoiceDecision, len(result.Choices))
	for index, choice := range result.Choices {
		explorationJournal.Choices[index] = choice.Decision()
	}
	explorationLimits := arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1}
	explored, err := model.ExploreDeterministic(explorationJournal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(explorationJournal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || len(explored.Computations) != 1 ||
		!bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("dynamic compound route fixed-schedule exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourcePassiveNewFinalReevaluatesModuleQualifiedActionConnectionAcrossInitialContext(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Closing(step : Integer);
  action out Heard(step : Integer);
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
connect
  (?Peer : Factory; ?N : Integer) ?Peer.Closing(?N) ||> Heard(?N);
final
  Closing(7);
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
	journal := arch.NewExecutionJournal(digest, 100,
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
	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	spawnReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 1 || len(spawnReturns) != 1 {
		t.Fatalf("Allocated/Spawn'Return=%d/%d", len(allocated), len(spawnReturns))
	}
	value, _ := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v", value)
	}
	moduleID := module.Identity()
	closing := sourceNamedEvents(result.Poset, moduleID, "Closing")
	childHeard := sourceNamedEvents(result.Poset, moduleID, "Heard")
	parentHeard := sourceNamedEvents(result.Poset, "factory", "Heard")
	contextAcquiredBeforeClosing := false
	if len(closing) == 1 {
		for _, context := range result.Contexts {
			if context.Source == moduleID && len(context.AcquiredAfter) == 1 {
				contextAcquiredBeforeClosing = result.Poset.IsCausallyBefore(
					gorapide.EventID(context.AcquiredAfter[0]), closing[0].ID,
				)
			}
		}
	}
	if len(closing) != 1 || len(childHeard) != 1 || len(parentHeard) != 1 ||
		closing[0].ParamInt("step") != 7 || childHeard[0].ParamInt("step") != 7 ||
		parentHeard[0].ParamInt("step") != 7 {
		t.Fatalf("dynamic qualified Closing/child Heard/parent Heard=%#v/%#v/%#v context-acquired-before=%v contexts=%#v firings=%#v",
			closing, childHeard, parentHeard, contextAcquiredBeforeClosing, result.Contexts, result.Firings)
	}
	var childLifecycle, parentLifecycle arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == moduleID {
			childLifecycle = candidate
		}
		if candidate.Occurrence == "component:factory" {
			parentLifecycle = candidate
		}
	}
	finish, present := result.Poset.Get(gorapide.EventID(childLifecycle.FinishEventID))
	if childLifecycle.State != arch.ModuleFinalizedState || !present || parentLifecycle.ModuleID == "" {
		t.Fatalf("child/parent lifecycle and Finish=%#v/%#v/%v", childLifecycle, parentLifecycle, present)
	}
	assertOnlyDirectCause(t, result.Poset, closing[0], allocated[0])
	assertOnlyDirectCause(t, result.Poset, childHeard[0], closing[0])
	assertOnlyDirectCause(t, result.Poset, parentHeard[0], closing[0])
	assertOnlyDirectCause(t, result.Poset, finish, closing[0])
	if !result.Poset.IsCausallyIndependent(childHeard[0].ID, parentHeard[0].ID) ||
		!result.Poset.IsCausallyIndependent(childHeard[0].ID, finish.ID) ||
		!result.Poset.IsCausallyIndependent(parentHeard[0].ID, finish.ID) ||
		!result.Poset.IsCausallyIndependent(spawnReturns[0].ID, finish.ID) {
		t.Fatal("dynamic qualified recipient branches acquired a worklist-order edge")
	}
	var initialContext *arch.CommunicationContextRecord
	for index := range result.Contexts {
		candidate := &result.Contexts[index]
		if candidate.Kind == "initial-parent" && candidate.Source == moduleID {
			initialContext = candidate
			break
		}
	}
	if initialContext == nil || initialContext.Live || initialContext.Destination != parentLifecycle.ModuleID ||
		len(initialContext.AcquiredAfter) != 1 || initialContext.AcquiredAfter[0] != childLifecycle.StartEventID ||
		len(initialContext.LostAfter) != 1 || initialContext.LostAfter[0] != string(closing[0].ID) {
		t.Fatalf("dynamic qualified initial Context=%#v", initialContext)
	}
	targets := map[string]bool{}
	qualifiedFirings := 0
	for _, firing := range result.Firings {
		if firing.ConnectionScope != arch.ModuleConnectionScope.String() ||
			firing.ConnectionKind != arch.AgentConnection.String() || firing.TriggerSource != moduleID ||
			firing.TriggerAction != "Closing" {
			continue
		}
		qualifiedFirings++
		targets[firing.Target] = true
		if len(firing.MatchedEvents) != 1 || firing.MatchedEvents[0] != string(closing[0].ID) ||
			len(firing.Bindings) != 2 {
			t.Fatalf("dynamic qualified firing audit=%#v", firing)
		}
		bindings := make(map[string]gorapide.CanonicalValue, len(firing.Bindings))
		for _, binding := range firing.Bindings {
			bindings[binding.Placeholder] = binding.Value
		}
		if bindings["Peer"].Kind != "module" || bindings["Peer"].Text != moduleID ||
			bindings["N"].Text != "7" {
			t.Fatalf("dynamic qualified firing bindings=%#v", firing.Bindings)
		}
	}
	if qualifiedFirings != 2 || !targets[moduleID] || !targets["factory"] {
		t.Fatalf("dynamic qualified firings=%d targets=%v", qualifiedFirings, targets)
	}
	afterDigest, err := model.DeterministicModelDigest()
	if err != nil || afterDigest != digest {
		t.Fatalf("dynamic qualified route mutated model digest before=%q after=%q err=%v", digest, afterDigest, err)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed dynamic module-qualified route or Context delivery")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("dynamic module-qualified route replay changed canonical bytes")
	}
	explorationJournal := journal
	explorationJournal.Choices = make([]arch.ChoiceDecision, len(result.Choices))
	for index, choice := range result.Choices {
		explorationJournal.Choices[index] = choice.Decision()
	}
	explorationLimits := arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1}
	explored, err := model.ExploreDeterministic(explorationJournal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(explorationJournal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || len(explored.Computations) != 1 ||
		!bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("dynamic qualified fixed-schedule exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourcePassiveNewFinalScopesMixedLocalAndModuleQualifiedConnectionLeaves(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Local(step : Integer);
  action out Closing(step : Integer);
  action out Heard(step : Integer);
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  Spawn : function() is begin Local(7); Allocated(New()); end function Spawn;
connect
  (?Peer : Factory; ?N : Integer) ?Peer.Closing(?N) and Local(?N) ||> Heard(?N);
final
  Local(7);
  Closing(7);
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
	journal := arch.NewExecutionJournal(digest, 120,
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

	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	parentLocal := sourceNamedEvents(result.Poset, "factory", "Local")
	parentHeard := sourceNamedEvents(result.Poset, "factory", "Heard")
	if len(allocated) != 1 || len(parentLocal) != 1 || len(parentHeard) != 1 {
		t.Fatalf("parent Allocated/Local/Heard=%d/%d/%d firings=%#v",
			len(allocated), len(parentLocal), len(parentHeard), result.Firings)
	}
	value, _ := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v", value)
	}
	moduleID := module.Identity()
	childLocal := sourceNamedEvents(result.Poset, moduleID, "Local")
	closing := sourceNamedEvents(result.Poset, moduleID, "Closing")
	childHeard := sourceNamedEvents(result.Poset, moduleID, "Heard")
	if len(childLocal) != 1 || len(closing) != 1 || len(childHeard) != 1 {
		t.Fatalf("child Local/Closing/Heard=%d/%d/%d module=%q firings=%#v",
			len(childLocal), len(closing), len(childHeard), moduleID, result.Firings)
	}
	for _, event := range []*gorapide.Event{parentLocal[0], childLocal[0], closing[0], parentHeard[0], childHeard[0]} {
		if event.ParamInt("step") != 7 {
			t.Fatalf("%s.%s step=%d", event.Source, event.Name, event.ParamInt("step"))
		}
	}
	if !result.Poset.IsCausallyBefore(parentLocal[0].ID, parentHeard[0].ID) ||
		!result.Poset.IsCausallyBefore(closing[0].ID, parentHeard[0].ID) ||
		!result.Poset.IsCausallyBefore(childLocal[0].ID, childHeard[0].ID) ||
		!result.Poset.IsCausallyBefore(closing[0].ID, childHeard[0].ID) {
		t.Fatal("mixed connection output does not depend on its complete owner-scoped match")
	}

	matchedByTarget := make(map[string]map[string]bool)
	qualifiedFirings := 0
	for _, firing := range result.Firings {
		if firing.ConnectionScope != arch.ModuleConnectionScope.String() ||
			firing.ConnectionKind != arch.AgentConnection.String() ||
			firing.TriggerSource != moduleID || firing.TriggerAction != "Closing" {
			continue
		}
		qualifiedFirings++
		if len(firing.MatchedEvents) != 2 || len(firing.Bindings) != 2 {
			t.Fatalf("mixed dynamic firing audit=%#v", firing)
		}
		matched := make(map[string]bool, len(firing.MatchedEvents))
		for _, eventID := range firing.MatchedEvents {
			matched[eventID] = true
		}
		matchedByTarget[firing.Target] = matched
		bindings := make(map[string]gorapide.CanonicalValue, len(firing.Bindings))
		for _, binding := range firing.Bindings {
			bindings[binding.Placeholder] = binding.Value
		}
		if bindings["Peer"].Kind != "module" || bindings["Peer"].Text != moduleID ||
			bindings["N"].Text != "7" {
			t.Fatalf("mixed dynamic firing bindings=%#v", firing.Bindings)
		}
	}
	if qualifiedFirings != 2 ||
		!matchedByTarget["factory"][string(parentLocal[0].ID)] ||
		matchedByTarget["factory"][string(childLocal[0].ID)] ||
		!matchedByTarget["factory"][string(closing[0].ID)] ||
		!matchedByTarget[moduleID][string(childLocal[0].ID)] ||
		matchedByTarget[moduleID][string(parentLocal[0].ID)] ||
		!matchedByTarget[moduleID][string(closing[0].ID)] {
		t.Fatalf("mixed route per-leaf source witnesses=%#v count=%d", matchedByTarget, qualifiedFirings)
	}

	var childLifecycle, parentLifecycle arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == moduleID {
			childLifecycle = candidate
		}
		if candidate.Occurrence == "component:factory" {
			parentLifecycle = candidate
		}
	}
	finish, present := result.Poset.Get(gorapide.EventID(childLifecycle.FinishEventID))
	if childLifecycle.State != arch.ModuleFinalizedState || !present || parentLifecycle.ModuleID == "" {
		t.Fatalf("child/parent lifecycle and Finish=%#v/%#v/%v", childLifecycle, parentLifecycle, present)
	}
	assertOnlyDirectCause(t, result.Poset, finish, closing[0])
	if !result.Poset.IsCausallyIndependent(childHeard[0].ID, parentHeard[0].ID) ||
		!result.Poset.IsCausallyIndependent(childHeard[0].ID, finish.ID) ||
		!result.Poset.IsCausallyIndependent(parentHeard[0].ID, finish.ID) {
		t.Fatal("mixed dynamic recipient branches acquired an evaluation-order edge")
	}
	var initialContext *arch.CommunicationContextRecord
	for index := range result.Contexts {
		candidate := &result.Contexts[index]
		if candidate.Kind == "initial-parent" && candidate.Source == moduleID {
			initialContext = candidate
			break
		}
	}
	if initialContext == nil || initialContext.Live || initialContext.Destination != parentLifecycle.ModuleID ||
		len(initialContext.LostAfter) != 1 || initialContext.LostAfter[0] != string(closing[0].ID) {
		t.Fatalf("mixed dynamic initial Context=%#v", initialContext)
	}
	afterDigest, err := model.DeterministicModelDigest()
	if err != nil || afterDigest != digest {
		t.Fatalf("mixed route mutated model digest before=%q after=%q err=%v", digest, afterDigest, err)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed mixed local/module-qualified routing")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("mixed local/module-qualified route replay changed canonical bytes")
	}
	explorationJournal := journal
	explorationJournal.Choices = make([]arch.ChoiceDecision, len(result.Choices))
	for index, choice := range result.Choices {
		explorationJournal.Choices[index] = choice.Decision()
	}
	explorationLimits := arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1}
	explored, err := model.ExploreDeterministic(explorationJournal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(explorationJournal, explorationLimits)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || len(explored.Computations) != 1 ||
		!bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("mixed route fixed-schedule exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourcePassiveNewClosedConnectionGeneratorsEqualExplicitRoutes(t *testing.T) {
	declarations := `
type Factory is interface
  action out Allocated(value : Factory);
  private action Input(step : Integer);
  action out Output(step : Integer);
  action out Cleaned(step : Integer);
  provides Spawn : function();
  requires Remote : function(step : Integer) return Integer;
  provides Local : function(step : Integer) return Integer;
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
`
	generatedSource := []byte(declarations + `
module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
  Local : function(step : Integer) return Integer is
    begin
      Cleaned(step);
      return step + 1;
    end function Local;
connect
  if True generate
    for I : Integer in 1..2 generate
      Input(I) ||> Output(I);
    end generate;
    Remote to Local;
  end generate;
  Input(3) ||> for I : Integer in 30..31 generate Output(I) end generate for;
  if False generate
    Missing to Output;
  end generate;
final
  Input(1);
  Input(2);
  Input(3);
  Remote(4);
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
end architecture System;
`)
	explicitSource := []byte(declarations + `
module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
  Local : function(step : Integer) return Integer is
    begin
      Cleaned(step);
      return step + 1;
    end function Local;
connect
  Input(3) ||> Output(31);
  Remote to Local;
  Input(2) ||> Output(2);
  Input(3) ||> Output(30);
  Input(1) ||> Output(1);
final
  Input(1);
  Input(2);
  Input(3);
  Remote(4);
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(); end when; end module DriverModule;
architecture System() is
  driver : Driver is DriverModule();
  factory : Factory is FactoryModule();
  stimulus : Stimulus;
connect driver.Spawn to factory.Spawn; stimulus.Trigger => driver.Trigger;
end architecture System;
`)
	generated, err := Compile(generatedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(explicitSource, "system")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, err := generated.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if generatedDigest != explicitDigest {
		t.Fatalf("passive generated/explicit model digests=%q/%q", generatedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 160,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	prior := runtime.GOMAXPROCS(1)
	generatedResult, err := generated.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	explicitResult, explicitErr := explicit.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if explicitErr != nil {
		t.Fatal(explicitErr)
	}
	generatedArtifact, _ := generatedResult.MarshalCanonical()
	explicitArtifact, _ := explicitResult.MarshalCanonical()
	if !bytes.Equal(generatedArtifact, explicitArtifact) {
		t.Fatal("closed connection-generator spelling changed passive allocation artifact bytes")
	}

	allocated := sourceNamedEvents(generatedResult.Poset, "factory", "Allocated")
	if len(allocated) != 1 {
		t.Fatalf("Allocated count=%d", len(allocated))
	}
	value, _ := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v", value)
	}
	moduleID := module.Identity()
	inputs := sourceNamedEvents(generatedResult.Poset, moduleID, "Input")
	outputs := sourceNamedEvents(generatedResult.Poset, moduleID, "Output")
	if len(inputs) != 3 || len(outputs) != 4 {
		t.Fatalf("generated child Input/Output=%d/%d firings=%#v", len(inputs), len(outputs), generatedResult.Firings)
	}
	inputByStep := make(map[int]*gorapide.Event, len(inputs))
	for _, input := range inputs {
		inputByStep[input.ParamInt("step")] = input
	}
	outputCounts := make(map[int]int, len(outputs))
	for _, output := range outputs {
		step := output.ParamInt("step")
		outputCounts[step]++
		causeStep := step
		if step == 30 || step == 31 {
			causeStep = 3
		}
		if inputByStep[causeStep] == nil ||
			!generatedResult.Poset.IsCausallyBefore(inputByStep[causeStep].ID, output.ID) {
			t.Fatalf("Output(%d) lacks generated route source Input(%d)", step, causeStep)
		}
	}
	if len(outputCounts) != 4 || outputCounts[1] != 1 || outputCounts[2] != 1 ||
		outputCounts[30] != 1 || outputCounts[31] != 1 {
		t.Fatalf("generated output values=%v", outputCounts)
	}
	dynamicFirings := 0
	for _, firing := range generatedResult.Firings {
		if firing.ConnectionScope == arch.ModuleConnectionScope.String() &&
			firing.ConnectionKind == arch.AgentConnection.String() && firing.Target == moduleID {
			dynamicFirings++
			if len(firing.MatchedEvents) != 1 {
				t.Fatalf("generated route firing witness=%#v", firing)
			}
		}
	}
	if dynamicFirings != 4 {
		t.Fatalf("generated dynamic route firings=%d, want 4", dynamicFirings)
	}
	requiredCalls := sourceNamedEvents(generatedResult.Poset, moduleID, "Remote'Call")
	providedCalls := sourceNamedEvents(generatedResult.Poset, moduleID, "Local'Call")
	cleaned := sourceNamedEvents(generatedResult.Poset, moduleID, "Cleaned")
	requiredReturns := sourceNamedEvents(generatedResult.Poset, moduleID, "Remote'Return")
	providedReturns := sourceNamedEvents(generatedResult.Poset, moduleID, "Local'Return")
	if len(requiredCalls) != 1 || len(providedCalls) != 1 || len(cleaned) != 1 ||
		len(requiredReturns) != 1 || len(providedReturns) != 1 {
		t.Fatalf("generated function route events call=%d/%d cleaned=%d return=%d/%d",
			len(requiredCalls), len(providedCalls), len(cleaned), len(requiredReturns), len(providedReturns))
	}
	if requiredCalls[0].ID != providedCalls[0].ID ||
		requiredReturns[0].ID != providedReturns[0].ID || cleaned[0].ParamInt("step") != 4 ||
		requiredReturns[0].ParamInt("Return") != 5 {
		t.Fatalf("generated function route identity/values call=%#v/%#v cleaned=%#v return=%#v/%#v",
			requiredCalls[0], providedCalls[0], cleaned[0], requiredReturns[0], providedReturns[0])
	}
	assertOnlyDirectCause(t, generatedResult.Poset, requiredCalls[0], inputByStep[3])
	assertOnlyDirectCause(t, generatedResult.Poset, cleaned[0], requiredCalls[0])
	assertOnlyDirectCause(t, generatedResult.Poset, requiredReturns[0], cleaned[0])
	var childLifecycle arch.ModuleLifecycleRecord
	for _, candidate := range generatedResult.Modules {
		if candidate.ModuleID == moduleID {
			childLifecycle = candidate
			break
		}
	}
	finish, present := generatedResult.Poset.Get(gorapide.EventID(childLifecycle.FinishEventID))
	if childLifecycle.State != arch.ModuleFinalizedState || !present {
		t.Fatalf("generated child lifecycle/Finish=%#v/%v", childLifecycle, present)
	}
	assertOnlyDirectCause(t, generatedResult.Poset, finish, requiredReturns[0])
	for _, output := range outputs {
		if (output.ParamInt("step") == 30 || output.ParamInt("step") == 31) &&
			!generatedResult.Poset.IsCausallyIndependent(output.ID, finish.ID) {
			t.Fatalf("terminal generated Output(%d) was ordered with Finish", output.ParamInt("step"))
		}
	}
	afterDigest, err := generated.DeterministicModelDigest()
	if err != nil || afterDigest != generatedDigest {
		t.Fatalf("generated route mutated model digest before=%q after=%q err=%v", generatedDigest, afterDigest, err)
	}
	expected, _ := generatedResult.ArtifactDigest()
	replayed, err := generated.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(generatedArtifact, replayedArtifact) {
		t.Fatal("generated passive route replay changed canonical bytes")
	}
	explorationJournal := journal
	explorationJournal.Choices = make([]arch.ChoiceDecision, len(generatedResult.Choices))
	for index, choice := range generatedResult.Choices {
		explorationJournal.Choices[index] = choice.Decision()
	}
	explored, err := generated.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := generated.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || len(explored.Computations) != 1 ||
		!bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("generated route fixed exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourcePassiveNewImmutableObjectsElaborateGeneratedTopology(t *testing.T) {
	declarations := `
type Factory is interface
  action out Allocated(value : Factory);
  private action Input(step : Integer);
  action out Output(step : Integer);
  action out FinalValue(value : Integer);
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
`
	generatedSource := []byte(declarations + `
module FactoryModule() return Factory is
  Enabled : Boolean is True;
  First : Integer is 1;
  Last : Integer is 2;
  Offset : Integer is 10;
  Spawn : function() is begin Allocated(New()); Allocated(New()); end function Spawn;
connect
  if Enabled generate
    for I : Integer in First..Last generate
      Input(I) => Output(I + Offset);
    end generate;
  end generate;
final
  Input(First);
  Input(Last);
  FinalValue(Offset);
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
end architecture System;
`)
	explicitSource := []byte(declarations + `
module FactoryModule() return Factory is
  Enabled : Boolean is True;
  First : Integer is 1;
  Last : Integer is 2;
  Offset : Integer is 10;
  Spawn : function() is begin Allocated(New()); Allocated(New()); end function Spawn;
connect
  Input(2) => Output(2 + Offset);
  Input(1) => Output(1 + Offset);
final
  Input(First);
  Input(Last);
  FinalValue(Offset);
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(); end when; end module DriverModule;
architecture System() is
  driver : Driver is DriverModule();
  factory : Factory is FactoryModule();
  stimulus : Stimulus;
connect driver.Spawn to factory.Spawn; stimulus.Trigger => driver.Trigger;
end architecture System;
`)
	generated, err := Compile(generatedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(explicitSource, "system")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, err := generated.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if generatedDigest != explicitDigest {
		t.Fatalf("object-bound generated/explicit model digests=%q/%q", generatedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 200,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	prior := runtime.GOMAXPROCS(1)
	generatedResult, err := generated.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	explicitResult, explicitErr := explicit.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if explicitErr != nil {
		t.Fatal(explicitErr)
	}
	generatedArtifact, _ := generatedResult.MarshalCanonical()
	explicitArtifact, _ := explicitResult.MarshalCanonical()
	if !bytes.Equal(generatedArtifact, explicitArtifact) {
		t.Fatal("immutable-object generator spelling changed passive allocation artifact bytes")
	}

	allocated := sourceNamedEvents(generatedResult.Poset, "factory", "Allocated")
	if len(allocated) != 2 {
		t.Fatalf("Allocated count=%d", len(allocated))
	}
	seenModules := make(map[string]bool, len(allocated))
	for _, allocation := range allocated {
		value, _ := allocation.Param("value")
		module, ok := value.(gorapide.RapideModuleValue)
		if !ok || module.Identity() == "" || seenModules[module.Identity()] {
			t.Fatalf("Allocated value=%#v seen=%v", value, seenModules)
		}
		moduleID := module.Identity()
		seenModules[moduleID] = true
		inputs := sourceNamedEvents(generatedResult.Poset, moduleID, "Input")
		outputs := sourceNamedEvents(generatedResult.Poset, moduleID, "Output")
		finalValues := sourceNamedEvents(generatedResult.Poset, moduleID, "FinalValue")
		if len(inputs) != 2 || len(outputs) != 2 || len(finalValues) != 1 ||
			finalValues[0].ParamInt("value") != 10 {
			t.Fatalf("child %s Input/Output/FinalValue=%d/%d/%#v", moduleID, len(inputs), len(outputs), finalValues)
		}
		inputByStep := make(map[int]*gorapide.Event, len(inputs))
		outputByStep := make(map[int]*gorapide.Event, len(outputs))
		for _, input := range inputs {
			inputByStep[input.ParamInt("step")] = input
		}
		for _, output := range outputs {
			outputByStep[output.ParamInt("step")] = output
		}
		if inputByStep[1] == nil || inputByStep[2] == nil ||
			outputByStep[11] == nil || outputByStep[12] == nil {
			t.Fatalf("child %s object-specialized values Input=%v Output=%v", moduleID, inputByStep, outputByStep)
		}
		assertOnlyDirectCause(t, generatedResult.Poset, outputByStep[11], inputByStep[1])
		assertOnlyDirectCause(t, generatedResult.Poset, outputByStep[12], inputByStep[2])
		assertOnlyDirectCause(t, generatedResult.Poset, finalValues[0], inputByStep[2])
		var lifecycle arch.ModuleLifecycleRecord
		for _, candidate := range generatedResult.Modules {
			if candidate.ModuleID == moduleID {
				lifecycle = candidate
				break
			}
		}
		finish, present := generatedResult.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
		if lifecycle.State != arch.ModuleFinalizedState || !present {
			t.Fatalf("child %s lifecycle/Finish=%#v/%v", moduleID, lifecycle, present)
		}
		assertOnlyDirectCause(t, generatedResult.Poset, finish, finalValues[0])
		if !generatedResult.Poset.IsCausallyIndependent(outputByStep[12].ID, finish.ID) {
			t.Fatalf("child %s terminal object-specialized Output was ordered with Finish", moduleID)
		}
		firings := 0
		for _, firing := range generatedResult.Firings {
			if firing.ConnectionScope == arch.ModuleConnectionScope.String() &&
				firing.ConnectionKind == arch.PipeConnection.String() &&
				firing.TriggerSource == moduleID {
				firings++
				if firing.Target != moduleID || len(firing.MatchedEvents) != 1 {
					t.Fatalf("child %s imported or malformed object-specialized route=%#v", moduleID, firing)
				}
			}
		}
		if firings != 2 {
			t.Fatalf("child %s object-specialized pipe firings=%d, want 2", moduleID, firings)
		}
	}
	afterDigest, err := generated.DeterministicModelDigest()
	if err != nil || afterDigest != generatedDigest {
		t.Fatalf("object-bound allocation mutated model digest before=%q after=%q err=%v",
			generatedDigest, afterDigest, err)
	}
	expected, _ := generatedResult.ArtifactDigest()
	replayed, err := generated.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(generatedArtifact, replayedArtifact) {
		t.Fatal("object-bound passive allocation replay changed canonical bytes")
	}
	explorationJournal := journal
	explorationJournal.Choices = make([]arch.ChoiceDecision, len(generatedResult.Choices))
	for index, choice := range generatedResult.Choices {
		explorationJournal.Choices[index] = choice.Decision()
	}
	explored, err := generated.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := generated.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || len(explored.Computations) != 1 ||
		!bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("object-bound allocation fixed exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourcePassiveNewFinalRejectsArchitectureFunctionRoute(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  provides Spawn : function();
  requires Remote : function();
end interface Factory;
type Provider is interface provides Local : function(); end interface Provider;
module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
final
  Remote();
end module FactoryModule;
module ProviderModule() return Provider is Local : function() is begin null; end function Local; end module ProviderModule;
architecture System() is
  factory : Factory is FactoryModule();
  provider : Provider is ProviderModule();
connect factory.Remote to provider.Local;
end architecture System;
`)
	model, err := Compile(source, "System")
	if err == nil {
		_, err = model.DeterministicModelDigest()
	}
	if err == nil || !strings.Contains(err.Error(), "generator-owned self connections") {
		t.Fatalf("architecture-connected final function error=%v", err)
	}
}

func TestSourcePassiveNewFinalFunctionExceptionTransfersWithoutReturn(t *testing.T) {
	source := []byte(`
type Factory is interface
  exception Failure(code : Integer);
  action out Allocated(value : Factory);
  action out Recovered(code : Integer);
  action out ProtectedRemainder();
  action out After();
  provides Spawn : function();
  provides Cleanup : function(code : Integer);
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
  Cleanup : function(code : Integer) is
    begin
      raise Failure(code is code);
      ProtectedRemainder();
    end function Cleanup;
final
  do
    Cleanup(3);
    ProtectedRemainder();
  handler
    is Failure(code is ?Code) => Recovered(?Code);
  end do;
  After();
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
	journal := arch.NewExecutionJournal(digest, 40,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	if len(allocated) != 1 {
		t.Fatalf("Allocated count=%d", len(allocated))
	}
	value, _ := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v", value)
	}
	calls := sourceNamedEvents(result.Poset, module.Identity(), "Cleanup'Call")
	failures := sourceNamedEvents(result.Poset, module.Identity(), "Failure")
	recovered := sourceNamedEvents(result.Poset, module.Identity(), "Recovered")
	after := sourceNamedEvents(result.Poset, module.Identity(), "After")
	returns := sourceNamedEvents(result.Poset, module.Identity(), "Cleanup'Return")
	protected := sourceNamedEvents(result.Poset, module.Identity(), "ProtectedRemainder")
	if len(calls) != 1 || len(failures) != 1 || len(recovered) != 1 || len(after) != 1 ||
		len(returns) != 0 || len(protected) != 0 {
		t.Fatalf("final function exception events call=%d failure=%d recovered=%d after=%d return=%d protected=%d",
			len(calls), len(failures), len(recovered), len(after), len(returns), len(protected))
	}
	if failures[0].ParamInt("code") != 3 || recovered[0].ParamInt("code") != 3 {
		t.Fatalf("final function exception values failure=%#v recovered=%#v", failures[0].Params, recovered[0].Params)
	}
	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == module.Identity() {
			lifecycle = candidate
			break
		}
	}
	finish, present := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if lifecycle.State != arch.ModuleFinalizedState || !present {
		t.Fatalf("dynamic lifecycle/Finish=%#v/%v", lifecycle, present)
	}
	assertOnlyDirectCause(t, result.Poset, calls[0], allocated[0])
	assertOnlyDirectCause(t, result.Poset, failures[0], calls[0])
	assertOnlyDirectCause(t, result.Poset, recovered[0], failures[0])
	assertOnlyDirectCause(t, result.Poset, after[0], recovered[0])
	assertOnlyDirectCause(t, result.Poset, finish, after[0])
	artifact, _ := result.MarshalCanonical()
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("final local-function exception replay changed canonical bytes")
	}
}

func TestSourcePassiveNewFinalDeclarationBearingDoRecoversBeforeFinish(t *testing.T) {
	source := []byte(`
type Factory is interface
  exception InterfaceNoise;
  action out Allocated(value : Factory);
  action out Before();
  action out NamedRecovered(code : Integer);
  action out UnnamedRecovered(code : Integer);
  action out ProtectedContinued();
  action out After();
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  exception Failure(code : Integer);
  Spawn : function() is begin Allocated(New()); end function Spawn;
final
  Before();
  Named: declare exception Failure(code : Integer); do
    raise Named::Failure(code is 2);
    ProtectedContinued();
  handler
    is FactoryModule::Failure(code is ?Code) => ProtectedContinued();
    is FactoryModule::Named::Failure(code is ?Code) => NamedRecovered(?Code);
  end do Named;
  declare exception Failure(code : Integer); do
    raise Failure(code is 3);
    ProtectedContinued();
  handler
    is FactoryModule::Failure(code is ?Code) => ProtectedContinued();
    is Failure(code is ?Code) => UnnamedRecovered(?Code);
  end do;
  null;
  After();
end module FactoryModule;

module DriverModule() return Driver is
serial when Trigger() do Spawn(); end when;
end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  stimulus.Trigger => driver.Trigger;
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
	journal := arch.NewExecutionJournal(digest, 60,
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

	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 1 || len(returns) != 1 {
		t.Fatalf("Allocated/Spawn'Return=%d/%d", len(allocated), len(returns))
	}
	value, exists := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v, want allocated module", value)
	}
	before := sourceNamedEvents(result.Poset, module.Identity(), "Before")
	failures := sourceNamedEvents(result.Poset, module.Identity(), "Failure")
	named := sourceNamedEvents(result.Poset, module.Identity(), "NamedRecovered")
	unnamed := sourceNamedEvents(result.Poset, module.Identity(), "UnnamedRecovered")
	after := sourceNamedEvents(result.Poset, module.Identity(), "After")
	if len(before) != 1 || len(failures) != 2 || len(named) != 1 || len(unnamed) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, module.Identity(), "ProtectedContinued")) != 0 {
		t.Fatalf("Before/Failure/Named/Unnamed/After=%d/%d/%d/%d/%d",
			len(before), len(failures), len(named), len(unnamed), len(after))
	}
	byCode := make(map[int]*gorapide.Event, len(failures))
	for _, failure := range failures {
		byCode[failure.ParamInt("code")] = failure
	}
	if byCode[2] == nil || byCode[3] == nil || named[0].ParamInt("code") != 2 ||
		unnamed[0].ParamInt("code") != 3 {
		t.Fatalf("final lexical exceptions=%#v named=%#v unnamed=%#v",
			byCode, named[0].Params, unnamed[0].Params)
	}
	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == module.Identity() {
			lifecycle = candidate
			break
		}
	}
	finish, present := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if lifecycle.State != arch.ModuleFinalizedState || !present {
		t.Fatalf("dynamic lifecycle/Finish=%#v/%v", lifecycle, present)
	}
	assertOnlyDirectCause(t, result.Poset, before[0], allocated[0])
	assertOnlyDirectCause(t, result.Poset, byCode[2], before[0])
	assertOnlyDirectCause(t, result.Poset, named[0], byCode[2])
	assertOnlyDirectCause(t, result.Poset, byCode[3], named[0])
	assertOnlyDirectCause(t, result.Poset, unnamed[0], byCode[3])
	assertOnlyDirectCause(t, result.Poset, after[0], unnamed[0])
	assertOnlyDirectCause(t, result.Poset, finish, after[0])
	assertOnlyDirectCause(t, result.Poset, returns[0], allocated[0])
	if !result.Poset.IsCausallyIndependent(returns[0].ID, before[0].ID) ||
		!result.Poset.IsCausallyIndependent(returns[0].ID, finish.ID) {
		t.Fatal("enclosing return was falsely ordered with exception-handling finalization")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed declaration-bearing final recovery")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("declaration-bearing final replay changed canonical bytes")
	}
}

func TestSourcePassiveNewFinalClosedIfCaseAssertBeforeFinish(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out IfSelected(code : Integer);
  action out CaseSelected(code : Integer);
  action out After();
  action out Wrong();
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
final
  declare exception Selection(code : Integer); do
    raise Selection(code is 2);
  handler
    is Selection(code is ?Code) =>
      if ?Code = 2 then IfSelected(?Code); else Wrong(); end if;
      case ?Code of
        1 => Wrong();
        xor 2 => CaseSelected(?Code);
        default => Wrong();
      end case;
  end do;
  assert False;
  After();
end module FactoryModule;

module DriverModule() return Driver is
serial when Trigger() do Spawn(); end when;
end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  stimulus.Trigger => driver.Trigger;
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
	journal := arch.NewExecutionJournal(digest, 50,
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

	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 1 || len(returns) != 1 {
		t.Fatalf("Allocated/Spawn'Return=%d/%d", len(allocated), len(returns))
	}
	value, exists := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v, want allocated module", value)
	}
	failures := sourceNamedEvents(result.Poset, module.Identity(), "Selection")
	ifSelected := sourceNamedEvents(result.Poset, module.Identity(), "IfSelected")
	caseSelected := sourceNamedEvents(result.Poset, module.Identity(), "CaseSelected")
	inconsistent := sourceNamedEvents(result.Poset, module.Identity(), "Inconsistent")
	after := sourceNamedEvents(result.Poset, module.Identity(), "After")
	if len(failures) != 1 || len(ifSelected) != 1 || len(caseSelected) != 1 ||
		len(inconsistent) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, module.Identity(), "Wrong")) != 0 ||
		failures[0].ParamInt("code") != 2 || ifSelected[0].ParamInt("code") != 2 ||
		caseSelected[0].ParamInt("code") != 2 {
		t.Fatalf("Selection/If/Case/Inconsistent/After=%#v/%#v/%#v/%#v/%#v",
			failures, ifSelected, caseSelected, inconsistent, after)
	}
	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == module.Identity() {
			lifecycle = candidate
			break
		}
	}
	finish, present := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if lifecycle.State != arch.ModuleFinalizedState || !present {
		t.Fatalf("dynamic lifecycle/Finish=%#v/%v", lifecycle, present)
	}
	assertOnlyDirectCause(t, result.Poset, failures[0], allocated[0])
	assertOnlyDirectCause(t, result.Poset, ifSelected[0], failures[0])
	assertOnlyDirectCause(t, result.Poset, caseSelected[0], ifSelected[0])
	assertOnlyDirectCause(t, result.Poset, inconsistent[0], caseSelected[0])
	assertOnlyDirectCause(t, result.Poset, after[0], inconsistent[0])
	assertOnlyDirectCause(t, result.Poset, finish, after[0])
	assertOnlyDirectCause(t, result.Poset, returns[0], allocated[0])
	if !result.Poset.IsCausallyIndependent(returns[0].ID, failures[0].ID) ||
		!result.Poset.IsCausallyIndependent(returns[0].ID, finish.ID) {
		t.Fatal("enclosing return was falsely ordered with conditional finalization")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed closed conditional finalization")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("closed conditional final replay changed canonical bytes")
	}
}

func TestSourcePassiveNewFinalClosedLoopsExitBeforeFinish(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out First();
  action out Inner();
  action out After();
  action out Wrong();
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
final
  First();
  while False do Wrong(); end while;
  Outer: loop do
    InnerLoop: loop do
      Inner();
      exit Outer;
      Wrong();
    end do InnerLoop;
    Wrong();
  end do Outer;
  After();
end module FactoryModule;

module DriverModule() return Driver is
serial when Trigger() do Spawn(); end when;
end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  stimulus.Trigger => driver.Trigger;
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
	journal := arch.NewExecutionJournal(digest, 40,
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

	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 1 || len(returns) != 1 {
		t.Fatalf("Allocated/Spawn'Return=%d/%d", len(allocated), len(returns))
	}
	value, exists := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v, want allocated module", value)
	}
	first := sourceNamedEvents(result.Poset, module.Identity(), "First")
	inner := sourceNamedEvents(result.Poset, module.Identity(), "Inner")
	after := sourceNamedEvents(result.Poset, module.Identity(), "After")
	if len(first) != 1 || len(inner) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, module.Identity(), "Wrong")) != 0 {
		t.Fatalf("First/Inner/After=%#v/%#v/%#v", first, inner, after)
	}
	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == module.Identity() {
			lifecycle = candidate
			break
		}
	}
	finish, present := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if lifecycle.State != arch.ModuleFinalizedState || !present {
		t.Fatalf("dynamic lifecycle/Finish=%#v/%v", lifecycle, present)
	}
	assertOnlyDirectCause(t, result.Poset, first[0], allocated[0])
	assertOnlyDirectCause(t, result.Poset, inner[0], first[0])
	assertOnlyDirectCause(t, result.Poset, after[0], inner[0])
	assertOnlyDirectCause(t, result.Poset, finish, after[0])
	assertOnlyDirectCause(t, result.Poset, returns[0], allocated[0])
	if !result.Poset.IsCausallyIndependent(returns[0].ID, inner[0].ID) ||
		!result.Poset.IsCausallyIndependent(returns[0].ID, finish.ID) {
		t.Fatal("enclosing return was falsely ordered with loop finalization")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed closed final loop execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("closed final loop replay changed canonical bytes")
	}
}

func TestSourcePassiveNewFinalFiniteRangeIteratorBeforeFinish(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Closing(step : Integer);
  action out After();
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
final
  for I : Integer in 1 .. 3 do
    Closing(I);
  end for;
  After();
end module FactoryModule;

module DriverModule() return Driver is
serial when Trigger() do Spawn(); end when;
end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  stimulus.Trigger => driver.Trigger;
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

	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 1 || len(returns) != 1 {
		t.Fatalf("Allocated/Spawn'Return=%d/%d", len(allocated), len(returns))
	}
	value, exists := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v, want allocated module", value)
	}
	closing := sourceNamedEvents(result.Poset, module.Identity(), "Closing")
	after := sourceNamedEvents(result.Poset, module.Identity(), "After")
	if len(closing) != 3 || len(after) != 1 ||
		distinctSourceIteratorEventCount(result.Poset.ByName("More'Call")) != 4 ||
		distinctSourceIteratorEventCount(result.Poset.ByName("Item'Call")) != 3 {
		t.Fatalf("Closing/After/More/Item=%d/%d/%d/%d", len(closing), len(after),
			distinctSourceIteratorEventCount(result.Poset.ByName("More'Call")),
			distinctSourceIteratorEventCount(result.Poset.ByName("Item'Call")))
	}
	steps := make(map[int64]bool)
	for _, event := range closing {
		step, present := event.Param("step")
		if !present {
			t.Fatalf("Closing has no step: %#v", event)
		}
		steps[step.(int64)] = true
	}
	if !steps[1] || !steps[2] || !steps[3] {
		t.Fatalf("final range values=%#v", steps)
	}

	var child, iterator arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		switch {
		case candidate.ModuleID == module.Identity():
			child = candidate
		case candidate.Kind == "predefined-range-iterator":
			iterator = candidate
		}
	}
	childFinish, childFinished := result.Poset.Get(gorapide.EventID(child.FinishEventID))
	iteratorFinish, iteratorFinished := result.Poset.Get(gorapide.EventID(iterator.FinishEventID))
	iteratorStart, iteratorStarted := result.Poset.Get(gorapide.EventID(iterator.StartEventID))
	if child.State != arch.ModuleFinalizedState || !childFinished ||
		iterator.State != arch.ModuleFinalizedState || !iteratorStarted || !iteratorFinished ||
		iterator.Parent != module.Identity() {
		t.Fatalf("child/iterator lifecycle=%#v/%#v", child, iterator)
	}
	assertOnlyDirectCause(t, result.Poset, iteratorStart, allocated[0])
	assertOnlyDirectCause(t, result.Poset, returns[0], allocated[0])
	if !result.Poset.IsCausallyIndependent(iteratorFinish.ID, after[0].ID) {
		t.Fatal("range iterator Finish incorrectly ordered the following final statement")
	}
	causes := result.Poset.DirectCauses(childFinish.ID)
	wantCauses := map[gorapide.EventID]bool{iteratorFinish.ID: true, after[0].ID: true}
	if len(causes) != len(wantCauses) {
		t.Fatalf("dynamic child Finish causes=%#v", causes)
	}
	for _, cause := range causes {
		if !wantCauses[cause.ID] {
			t.Fatalf("dynamic child Finish has unexpected cause %s", cause.ID)
		}
	}
	if !result.Poset.IsCausallyIndependent(returns[0].ID, iteratorStart.ID) ||
		!result.Poset.IsCausallyIndependent(returns[0].ID, iteratorFinish.ID) ||
		!result.Poset.IsCausallyIndependent(returns[0].ID, childFinish.ID) {
		t.Fatal("enclosing return was falsely ordered with final range lifecycle")
	}

	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed final range execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("final range replay changed canonical bytes")
	}
}

func TestSourcePassiveNewFinalSelfInterruptsBeforeFinish(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Closing(step : Integer);
  private action Pulse();
  action out Recovered(step : Integer);
  action out AnyRecovered();
  action out After();
  action out Wrong();
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
final
  do
    Closing(7);
    Wrong();
  handler
    is Closing(step is ?Step) => Recovered(?Step);
  end do;
  do
    Pulse();
    Wrong();
  handler
    is any => AnyRecovered();
  end do;
  After();
end module FactoryModule;

module DriverModule() return Driver is
serial when Trigger() do Spawn(); end when;
end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  stimulus.Trigger => driver.Trigger;
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
	journal := arch.NewExecutionJournal(digest, 60,
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

	allocated := sourceNamedEvents(result.Poset, "factory", "Allocated")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 1 || len(returns) != 1 {
		t.Fatalf("Allocated/Spawn'Return=%d/%d", len(allocated), len(returns))
	}
	value, exists := allocated[0].Param("value")
	module, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || module.Identity() == "" {
		t.Fatalf("Allocated value=%#v, want allocated module", value)
	}
	closing := sourceNamedEvents(result.Poset, module.Identity(), "Closing")
	pulse := sourceNamedEvents(result.Poset, module.Identity(), "Pulse")
	recovered := sourceNamedEvents(result.Poset, module.Identity(), "Recovered")
	anyRecovered := sourceNamedEvents(result.Poset, module.Identity(), "AnyRecovered")
	after := sourceNamedEvents(result.Poset, module.Identity(), "After")
	if len(closing) != 1 || len(pulse) != 1 || len(recovered) != 1 ||
		len(anyRecovered) != 1 || len(after) != 1 || len(result.Poset.ByName("Wrong")) != 0 {
		t.Fatalf("Closing/Pulse/Recovered/AnyRecovered/After/Wrong=%d/%d/%d/%d/%d/%d",
			len(closing), len(pulse), len(recovered), len(anyRecovered), len(after),
			len(result.Poset.ByName("Wrong")))
	}
	if recovered[0].ParamInt("step") != 7 {
		t.Fatalf("final interrupt binding=%#v", recovered[0])
	}
	assertOnlyDirectCause(t, result.Poset, closing[0], allocated[0])
	assertOnlyDirectCause(t, result.Poset, recovered[0], closing[0])
	assertOnlyDirectCause(t, result.Poset, pulse[0], recovered[0])
	assertOnlyDirectCause(t, result.Poset, anyRecovered[0], pulse[0])
	assertOnlyDirectCause(t, result.Poset, after[0], anyRecovered[0])
	assertOnlyDirectCause(t, result.Poset, returns[0], allocated[0])

	var child arch.ModuleLifecycleRecord
	for _, candidate := range result.Modules {
		if candidate.ModuleID == module.Identity() {
			child = candidate
			break
		}
	}
	finish, finished := result.Poset.Get(gorapide.EventID(child.FinishEventID))
	if child.State != arch.ModuleFinalizedState || !finished {
		t.Fatalf("child lifecycle=%#v", child)
	}
	assertOnlyDirectCause(t, result.Poset, finish, after[0])
	if !result.Poset.IsCausallyIndependent(returns[0].ID, closing[0].ID) ||
		!result.Poset.IsCausallyIndependent(returns[0].ID, finish.ID) {
		t.Fatal("enclosing return was falsely ordered with self-interrupting finalization")
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("ordinary final action interrupt entered exception propagation: %#v", result.ExceptionPropagations)
	}

	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed final self-interrupt execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("final self-interrupt replay changed canonical bytes")
	}
}

func TestSourceFinalDeclarationBearingDoScopeDoesNotLeak(t *testing.T) {
	_, err := Compile([]byte(`
type Factory is interface action out Closing(); end interface Factory;
module FactoryModule() return Factory is
final
  declare exception LocalOnly; do null; end do;
  raise LocalOnly;
  Closing();
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
`), "System")
	if err == nil || !strings.Contains(err.Error(), `undeclared exception "LocalOnly"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestSourceFinalDeclarationBearingDoIdentityIsCanonical(t *testing.T) {
	build := func(reverse bool) []byte {
		declarations := "exception LocalA; exception LocalB;"
		choices := "is LocalA => Closing(1); is LocalB => Closing(2);"
		if reverse {
			declarations = "exception LocalB; exception LocalA;"
			choices = "is LocalB => Closing(2); is LocalA => Closing(1);"
		}
		return []byte(`
type Factory is interface action out Closing(step : Integer); end interface Factory;
module FactoryModule() return Factory is
final
  Stable: declare ` + declarations + ` do
    raise Stable::LocalA;
  handler ` + choices + ` end do Stable;
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
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
		t.Fatalf("final declaration-bearing do order changed model identity: %s != %s",
			leftDigest, rightDigest)
	}
}

func TestSourceFinalExceptionEscapeFailsExplicitly(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New()); end function Spawn;
final
  raise Failure;
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
	_, err = model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err == nil || !strings.Contains(err.Error(), "unhandled Rapide exception") ||
		!strings.Contains(err.Error(), "final part: Failure") {
		t.Fatalf("escaping final exception error=%v", err)
	}
}

func TestSourceModuleFinalPartRejectsUnsupportedForms(t *testing.T) {
	tests := []struct {
		name  string
		final string
		want  string
	}{
		{name: "empty", final: `final`, want: "module final part requires at least one statement"},
		{name: "general for", final: `final for 0 in True next 0 do Closing(1); end for;`, want: "general finalization-control slice"},
		{name: "unconnected required function", final: `final Remote();`, want: "does not match an implemented local or connected function"},
		{name: "allocating function", final: `final Spawn();`, want: "is not a closed scalar expression"},
		{name: "in action", final: `final Trigger();`, want: "must name a declared out- or private-action"},
		{name: "self actual", final: `final Allocated(Self);`, want: "is not a closed scalar expression"},
		{
			name:  "external in-action interrupt",
			final: `final do Closing(1); handler is Trigger => Closing(2); end do;`,
			want:  "final external in-action interrupt choice",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Factory is interface
  action in Trigger();
  action out Allocated(value : Factory);
  action out Closing(step : Integer);
  provides Spawn : function();
  requires Remote : function();
end interface Factory;
module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
` + test.final + `
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
`)
			model, err := Compile(source, "System")
			if err == nil {
				_, err = model.DeterministicModelDigest()
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("final form error=%v, want containing %q", err, test.want)
			}
		})
	}
}

func TestSourceModuleFinalPartChangesCanonicalModelIdentity(t *testing.T) {
	base := []byte(`
type Factory is interface action out Closing(step : Integer); end interface Factory;
module FactoryModule() return Factory is
%s
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
`)
	without, err := Compile([]byte(strings.Replace(string(base), "%s", "", 1)), "System")
	if err != nil {
		t.Fatal(err)
	}
	with, err := Compile([]byte(strings.Replace(string(base), "%s", "final Closing(1);", 1)), "System")
	if err != nil {
		t.Fatal(err)
	}
	left, err := without.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	right, err := with.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatal("module final part did not change canonical model identity")
	}
}
