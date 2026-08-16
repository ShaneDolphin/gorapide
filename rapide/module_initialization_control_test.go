package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceAllocatedModuleInitialControlUsesChildState(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Initialized(value : Integer);
  action out Allocated(value : Factory);
  provides Spawn : function(Value : Integer);
end interface Factory;
type Driver is interface
  action in Trigger(value : Integer);
  requires Spawn : function(Value : Integer);
end interface Driver;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;

module FactoryModule() return Factory is
  Counter : var Integer := 0;
  Spawn : function(Value : Integer) is begin Allocated(New(Value)); end function Spawn;
initial (Limit : Integer is 2)
  do
    for I : Integer in 1..Limit do
      next where I = 2;
      Counter := $Counter + I;
    end;
    case Limit of
      3 => Counter := $Counter + 10;
      default => null;
    end case;
    loop do
      Counter := $Counter + 1;
      exit where $Counter >= Limit + 2;
    end do;
    if $Counter >= Limit then Initialized($Counter); else assert False; end if;
  end do;
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
			Params: map[string]any{"value": 3},
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
	if len(static) != 1 || static[0].ParamInt("value") != 4 {
		t.Fatalf("static controlled initialization=%#v", static)
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
	if len(initialized) != 1 || initialized[0].ParamInt("value") != 15 {
		t.Fatalf("dynamic controlled initialization=%#v", initialized)
	}
	assertOnlyDirectCause(t, first.Poset, allocated[0], initialized[0])
	var start *gorapide.Event
	for _, lifecycle := range first.Modules {
		if lifecycle.ModuleID == child.Identity() {
			start, _ = first.Poset.Get(gorapide.EventID(lifecycle.StartEventID))
			break
		}
	}
	if start == nil || !first.Poset.IsCausallyBefore(start.ID, initialized[0].ID) {
		t.Fatalf("child Start does not precede controlled initializer: start=%#v initialized=%s", start, initialized[0].ID)
	}
	childStateFound := false
	for _, state := range first.State {
		if state.ComponentID == child.Identity() && state.Name == "Counter" {
			childStateFound = state.Value.Text == "15" && state.Version == 4
		}
	}
	if !childStateFound {
		t.Fatalf("dynamic child state=%#v", first.State)
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
		t.Fatal("GOMAXPROCS changed controlled initializer artifact bytes")
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
		t.Fatal("controlled initializer replay changed artifact bytes")
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
		t.Fatalf("controlled initializer exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourceAllocatedModuleInitialHandlersUseDynamicIdentity(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Before(value : Integer);
  action out Recovered(value : Integer);
  action out Reraised(value : Integer);
  action out ElseRecovered(value : Integer);
  action out FunctionRecovered(value : Integer);
  action out Initialized(value : Integer);
  action out Unreachable();
  provides Spawn : function(Value : Integer);
  provides RecoverLocally : function(Value : Integer);
end interface Factory;
type Driver is interface
  action in Trigger(value : Integer);
  requires Spawn : function(Value : Integer);
end interface Driver;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;

module FactoryModule() return Factory is
  exception Failure(code : Integer);
  exception Retry(code : Integer);
  exception Fallback(code : Integer);
  exception FunctionFailure(code : Integer);
  Spawn : function(Value : Integer) is begin Allocated(New(Seed is Value)); end function Spawn;
  RecoverLocally : function(Value : Integer) is
  begin
    raise FunctionFailure(code is Value);
    Unreachable();
  handler
    is FunctionFailure(code is ?Code) => FunctionRecovered(?Code);
  end function RecoverLocally;
initial (Seed : Integer is 1)
  do
    Before(Seed);
    raise Failure(code is Seed);
    Unreachable();
  handler
    is Failure(code is ?Code) => Recovered(?Code);
  end do;
  do
    do
      raise Retry(code is Seed);
    handler
      is Retry(code is ?Code) => raise;
    end do;
  handler
    is Retry(code is ?Code) => Reraised(?Code);
  end do;
  do
    raise Fallback(code is Seed);
  handler
    is Failure(code is ?Code) => Unreachable();
    else ElseRecovered(Seed);
  end do;
  RecoverLocally(Seed);
  Initialized(Seed + 1);
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
			Params: map[string]any{"value": 4},
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

	allocated := sourceNamedEvents(first.Poset, "factory", "Allocated")
	if len(allocated) != 1 {
		t.Fatalf("allocated events=%#v", allocated)
	}
	value, present := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !present || !ok || child.Identity() == "" {
		t.Fatalf("allocated child=%#v", value)
	}
	before := sourceNamedEvents(first.Poset, child.Identity(), "Before")
	failure := sourceNamedEvents(first.Poset, child.Identity(), "Failure")
	recovered := sourceNamedEvents(first.Poset, child.Identity(), "Recovered")
	retry := sourceNamedEvents(first.Poset, child.Identity(), "Retry")
	reraised := sourceNamedEvents(first.Poset, child.Identity(), "Reraised")
	fallback := sourceNamedEvents(first.Poset, child.Identity(), "Fallback")
	elseRecovered := sourceNamedEvents(first.Poset, child.Identity(), "ElseRecovered")
	functionFailure := sourceNamedEvents(first.Poset, child.Identity(), "FunctionFailure")
	functionRecovered := sourceNamedEvents(first.Poset, child.Identity(), "FunctionRecovered")
	initialized := sourceNamedEvents(first.Poset, child.Identity(), "Initialized")
	unreachable := sourceNamedEvents(first.Poset, child.Identity(), "Unreachable")
	if len(before) != 1 || before[0].ParamInt("value") != 4 ||
		len(failure) != 1 || failure[0].ParamInt("code") != 4 ||
		len(recovered) != 1 || recovered[0].ParamInt("value") != 4 ||
		len(retry) != 1 || retry[0].ParamInt("code") != 4 ||
		len(reraised) != 1 || reraised[0].ParamInt("value") != 4 ||
		len(fallback) != 1 || fallback[0].ParamInt("code") != 4 ||
		len(elseRecovered) != 1 || elseRecovered[0].ParamInt("value") != 4 ||
		len(functionFailure) != 1 || functionFailure[0].ParamInt("code") != 4 ||
		len(functionRecovered) != 1 || functionRecovered[0].ParamInt("value") != 4 ||
		len(initialized) != 1 || initialized[0].ParamInt("value") != 5 ||
		len(unreachable) != 0 {
		t.Fatalf("dynamic handler events before=%#v failure=%#v recovered=%#v retry=%#v reraised=%#v fallback=%#v else=%#v functionFailure=%#v functionRecovered=%#v initialized=%#v unreachable=%#v",
			before, failure, recovered, retry, reraised, fallback, elseRecovered,
			functionFailure, functionRecovered, initialized, unreachable)
	}
	for _, pair := range [][2]*gorapide.Event{
		{before[0], failure[0]},
		{failure[0], recovered[0]},
		{recovered[0], retry[0]},
		{retry[0], reraised[0]},
		{reraised[0], fallback[0]},
		{fallback[0], elseRecovered[0]},
		{elseRecovered[0], functionFailure[0]},
		{functionFailure[0], functionRecovered[0]},
		{functionRecovered[0], initialized[0]},
		{initialized[0], allocated[0]},
	} {
		if !first.Poset.IsCausallyBefore(pair[0].ID, pair[1].ID) {
			t.Fatalf("missing dynamic initializer-handler causality %s -> %s", pair[0].ID, pair[1].ID)
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
		t.Fatal("GOMAXPROCS changed dynamic initializer-handler artifact bytes")
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
		t.Fatal("dynamic initializer-handler replay changed artifact bytes")
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
		t.Fatalf("dynamic initializer-handler exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourceAllocatedModuleInitialHandlersAdmitEscapeAndRejectInterrupts(t *testing.T) {
	template := `
type Factory is interface
  action in Incoming();
  action out Allocated(value : Factory);
  action out Pulse();
  action out Recovered();
  provides Spawn : function();
  provides Helper : function();
end interface Factory;
module FactoryModule() return Factory is
  exception Failure;
  exception Other;
  Helper : function() is begin raise Failure; end function Helper;
  Spawn : function() is begin Allocated(New()); end function Spawn;
initial
  BODY
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
`
	tests := []struct {
		name        string
		body        string
		wantCompile bool
	}{
		{name: "unhandled top-level raise", body: `raise Failure;`, wantCompile: true},
		{name: "unmatched local handler", body: `do raise Failure; handler is Other => Recovered(); end do;`, wantCompile: true},
		{name: "out-action interrupt", body: `do Pulse(); handler is Pulse => Recovered(); end do;`, wantCompile: true},
		{name: "predefined any interrupt", body: `do raise Failure; handler is any => Recovered(); end do;`, wantCompile: true},
		{name: "external in-action interrupt", body: `do Pulse(); handler is Incoming => Recovered(); end do;`},
		{name: "nonmatching action handler permits function exception escape", body: `do Helper(); handler is Pulse => Recovered(); end do;`, wantCompile: true},
		{name: "same handler cannot catch handler-body raise", body: `do raise Failure; handler is Failure => raise Other; is Other => Recovered(); end do;`, wantCompile: true},
		{name: "uncontained unnamed reraise", body: `do raise Failure; handler is Failure => raise; end do;`, wantCompile: true},
		{name: "same helper protected then unprotected", body: `do Helper(); handler is Failure => Recovered(); end do; Helper();`, wantCompile: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(strings.Replace(template, "BODY", test.body, 1))
			_, err := Compile(source, "System")
			if test.wantCompile {
				if err != nil {
					t.Fatalf("Compile()=%v, want supported named-exception escape", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "requires the current deterministic dynamic-module specialization slice") {
				t.Fatalf("Compile()=%v, want explicit external dynamic-initializer interrupt gate", err)
			}
		})
	}
}

func TestSourceAllocatedModuleInitialHandlerCatchesCalledFunctionException(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out Recovered(value : Integer);
  action out Initialized(value : Integer);
  provides Spawn : function(Value : Integer);
  provides Helper : function();
end interface Factory;
type Driver is interface
  action in Trigger(value : Integer);
  requires Spawn : function(Value : Integer);
end interface Driver;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;

module FactoryModule() return Factory is
  exception Failure;
  Helper : function() is begin raise Failure; end function Helper;
  Spawn : function(Value : Integer) is begin Allocated(New(Value)); end function Spawn;
initial (Seed : Integer is 1)
  do
    Helper();
  handler
    is Failure => Recovered(Seed);
  end do;
  Initialized(Seed);
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
	journal := arch.NewExecutionJournal(
		digest, 100,
		arch.InputEvent{
			Key: "trigger", Source: "stimulus", Action: "Trigger",
			Params: map[string]any{"value": 4},
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
	firstBytes, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("cross-function initializer recovery changed with GOMAXPROCS")
	}
	journalBytes, err := journal.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	replayJournal, err := arch.ParseExecutionJournal(journalBytes)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ExecuteDeterministic(replayJournal)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("canonical replay changed cross-function initializer recovery")
	}
	explored, err := model.ExploreDeterministic(
		journal, arch.ExplorationLimits{MaxExecutions: 64, MaxChoiceDepth: 16},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(
		journal, arch.ExplorationLimits{MaxExecutions: 64, MaxChoiceDepth: 16},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || len(explored.Computations) != 1 ||
		!bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("cross-function initializer exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
	if len(first.ExceptionPropagations) != 0 {
		t.Fatalf("cross-function initializer recovery propagated: %#v", first.ExceptionPropagations)
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
	for _, expectation := range []struct {
		source string
		seed   int
	}{
		{source: "factory", seed: 1},
		{source: child.Identity(), seed: 4},
	} {
		calls := sourceNamedEvents(first.Poset, expectation.source, "Helper'Call")
		failures := sourceNamedEvents(first.Poset, expectation.source, "Failure")
		recovered := sourceNamedEvents(first.Poset, expectation.source, "Recovered")
		initialized := sourceNamedEvents(first.Poset, expectation.source, "Initialized")
		returns := sourceNamedEvents(first.Poset, expectation.source, "Helper'Return")
		if len(calls) != 1 || len(failures) != 1 || len(recovered) != 1 ||
			len(initialized) != 1 || len(returns) != 0 {
			t.Fatalf("source %s call/failure/recovery/initialized/return=%d/%d/%d/%d/%d",
				expectation.source, len(calls), len(failures), len(recovered), len(initialized), len(returns))
		}
		if recovered[0].ParamInt("value") != expectation.seed ||
			initialized[0].ParamInt("value") != expectation.seed {
			t.Fatalf("source %s recovered/initialized values=%d/%d, want %d",
				expectation.source, recovered[0].ParamInt("value"), initialized[0].ParamInt("value"), expectation.seed)
		}
		if !first.Poset.IsCausallyBefore(calls[0].ID, failures[0].ID) ||
			!first.Poset.IsCausallyBefore(failures[0].ID, recovered[0].ID) ||
			!first.Poset.IsCausallyBefore(recovered[0].ID, initialized[0].ID) {
			t.Fatalf("source %s lost call -> failure -> recovery -> initialized causality", expectation.source)
		}
	}
}

func TestSourceFailedAllocatedModuleInitializationCompletesProcessOwnedLifecycle(t *testing.T) {
	variants := []struct {
		name              string
		spawn             string
		driverDeclaration string
		processBody       string
		moduleHandler     string
		parentHandled     bool
	}{
		{
			name: "direct action actual",
			spawn: `Spawn : function(Value : Integer) is
  begin
    do
      Allocated(New(Seed is Value));
    handler
      is Failure(code is ?Code) => Caught(?Code);
    end do;
    Unreachable();
  end function Spawn;`,
			processBody: `Spawn(?Value); After();`,
		},
		{
			name: "function local after process suspension",
			spawn: `Spawn : function(Value : Integer) is
    Child : Factory is New(Seed is Value);
  begin
    Allocated(Child);
    Unreachable();
  end function Spawn;`,
			driverDeclaration: `C : Clock is Make_Clock();`,
			processBody:       `pause C.Ticks(1); Spawn(?Value); After();`,
		},
		{
			name: "parent module handler recovery",
			spawn: `Spawn : function(Value : Integer) is
  begin
    Allocated(New(Seed is Value));
    Unreachable();
  end function Spawn;`,
			processBody:   `Spawn(?Value); After();`,
			moduleHandler: `handler is Failure(code is ?Code) => ParentRecovered(?Code);`,
			parentHandled: true,
		},
	}

	template := `
type Factory is interface
  action out Before(value : Integer);
  action out Initialized(value : Integer);
  action out Allocated(value : Factory);
  action out Caught(value : Integer);
  action out ParentRecovered(value : Integer);
  action out Unreachable();
  action out Closing();
  provides Spawn : function(Value : Integer);
end interface Factory;
type Driver is interface
  action in Trigger(value : Integer);
  action out After();
  requires Spawn : function(Value : Integer);
end interface Driver;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;

module FactoryModule() return Factory is
  exception Failure(code : Integer);
  SPAWN
initial (Seed : Integer is 1)
  Before(Seed);
  if Seed > 1 then
    raise Failure(code is Seed);
  end if;
  Initialized(Seed);
  MODULE_HANDLER
final
  Closing();
end module FactoryModule;

module DriverModule() return Driver is
  DRIVER_DECLARATION
serial when (?Value : Integer) Trigger(?Value) do
  PROCESS_BODY
end when;
end module DriverModule;

architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  (?Value : Integer) stimulus.Trigger(?Value) => driver.Trigger(?Value);
  driver.Spawn to factory.Spawn;
end architecture System;
`

	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			source := []byte(strings.NewReplacer(
				"SPAWN", variant.spawn,
				"DRIVER_DECLARATION", variant.driverDeclaration,
				"PROCESS_BODY", variant.processBody,
				"MODULE_HANDLER", variant.moduleHandler,
			).Replace(template))
			model, err := Compile(source, "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			journal := arch.NewExecutionJournalWithLimits(
				digest, arch.ExecutionLimits{MaxFirings: 200, MaxStatements: 200},
				arch.InputEvent{
					Key: "trigger", Source: "stimulus", Action: "Trigger",
					Params: map[string]any{"value": 4},
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

			staticBefore := sourceNamedEvents(first.Poset, "factory", "Before")
			staticInitialized := sourceNamedEvents(first.Poset, "factory", "Initialized")
			if len(staticBefore) != 1 || staticBefore[0].ParamInt("value") != 1 ||
				len(staticInitialized) != 1 || staticInitialized[0].ParamInt("value") != 1 {
				t.Fatalf("static factory initialization Before/Initialized=%#v/%#v", staticBefore, staticInitialized)
			}
			factory := lifecycleModuleByOccurrence(t, first, "component:factory")
			driver := lifecycleModuleByOccurrence(t, first, "component:driver")
			root := lifecycleModuleByOccurrence(t, first, "architecture:root")
			var child *arch.ModuleLifecycleRecord
			for index := range first.Modules {
				candidate := &first.Modules[index]
				if candidate.Kind == "allocator-module" && candidate.Parent == factory.ModuleID {
					if child != nil {
						t.Fatalf("multiple failed allocator children=%#v/%#v", child, candidate)
					}
					child = candidate
				}
			}
			if child == nil {
				t.Fatal("failed allocator child lifecycle is absent")
			}
			calls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "driver", "Spawn'Call"))
			returns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "driver", "Spawn'Return"))
			before := sourceNamedEvents(first.Poset, child.ModuleID, "Before")
			failure := sourceNamedEvents(first.Poset, child.ModuleID, "Failure")
			finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
			if len(calls) != 1 || len(returns) != 0 || len(before) != 1 ||
				before[0].ParamInt("value") != 4 || len(failure) != 1 ||
				failure[0].ParamInt("code") != 4 || !finishExists || finish == nil {
				t.Fatalf("Call/Return/Before/Failure/Finish=%d/%d/%#v/%#v/%#v",
					len(calls), len(returns), before, failure, finish)
			}
			for _, absent := range []struct {
				source string
				name   string
			}{
				{source: "factory", name: "Allocated"},
				{source: "factory", name: "Caught"},
				{source: "factory", name: "Unreachable"},
				{source: "driver", name: "After"},
				{source: child.ModuleID, name: "Initialized"},
				{source: child.ModuleID, name: "Closing"},
				{source: "factory", name: "Closing"},
			} {
				if events := sourceNamedEvents(first.Poset, absent.source, absent.name); len(events) != 0 {
					t.Fatalf("failed allocation emitted %s.%s=%#v", absent.source, absent.name, events)
				}
			}
			parentRecovered := sourceNamedEvents(first.Poset, "factory", "ParentRecovered")
			if variant.parentHandled {
				if len(parentRecovered) != 1 || parentRecovered[0].ParamInt("value") != 4 ||
					!first.Poset.IsCausallyBefore(failure[0].ID, parentRecovered[0].ID) {
					t.Fatalf("parent module-handler recovery=%#v", parentRecovered)
				}
			} else if len(parentRecovered) != 0 {
				t.Fatalf("unexpected parent module-handler recovery=%#v", parentRecovered)
			}
			start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
			if !startExists || start == nil || finish.Name != arch.ModuleFinishAction ||
				finish.Source != child.ModuleID {
				t.Fatalf("failed child Start/Finish=%#v/%#v", start, finish)
			}
			assertOnlyDirectCause(t, first.Poset, start, calls[0])
			assertOnlyDirectCause(t, first.Poset, before[0], start)
			assertOnlyDirectCause(t, first.Poset, failure[0], before[0])
			assertOnlyDirectCause(t, first.Poset, finish, failure[0])

			if child.State != arch.ModuleFinalizedState || child.Namable ||
				child.TerminationEventID != string(failure[0].ID) || child.FinishEventID == "" {
				t.Fatalf("failed child lifecycle=%#v", child)
			}
			for _, name := range child.Names {
				if name.Live || strings.Join(name.LostAfter, ",") != string(failure[0].ID) {
					t.Fatalf("failed child retained provisional name=%#v", name)
				}
			}
			if variant.parentHandled {
				if factory.State == arch.ModuleTerminatedState || factory.TerminationEventID != "" ||
					root.State == arch.ModuleTerminatedState || root.TerminationEventID != "" {
					t.Fatalf("handled child failure terminated parent/root=%#v/%#v", factory, root)
				}
			} else if factory.State != arch.ModuleTerminatedState ||
				factory.TerminationEventID != string(failure[0].ID) ||
				root.State != arch.ModuleTerminatedState ||
				root.TerminationEventID != string(failure[0].ID) {
				t.Fatalf("failed allocation parent/root lifecycles=%#v/%#v", factory, root)
			}
			if driver.State == arch.ModuleTerminatedState || driver.TerminationEventID != "" {
				t.Fatalf("failed allocation caller module was terminated=%#v", driver)
			}

			childPropagation := exceptionPropagationBySource(t, first, child.ModuleID)
			wantDisposition := "delivered"
			if variant.parentHandled {
				wantDisposition = "handled"
			}
			if childPropagation.ExceptionEventID != string(failure[0].ID) ||
				len(childPropagation.Targets) != 1 ||
				childPropagation.Targets[0].ModuleID != factory.ModuleID ||
				childPropagation.Targets[0].Disposition != wantDisposition {
				t.Fatalf("child-to-factory propagation=%#v", childPropagation)
			}
			if variant.parentHandled {
				if len(first.ExceptionPropagations) != 1 {
					t.Fatalf("handled child failure propagated beyond parent=%#v", first.ExceptionPropagations)
				}
			} else {
				factoryPropagation := exceptionPropagationBySource(t, first, factory.ModuleID)
				rootPropagation := exceptionPropagationBySource(t, first, root.ModuleID)
				if factoryPropagation.ExceptionEventID != string(failure[0].ID) ||
					len(factoryPropagation.Targets) != 1 ||
					factoryPropagation.Targets[0].ModuleID != root.ModuleID ||
					factoryPropagation.Targets[0].Disposition != "delivered" {
					t.Fatalf("factory-to-root propagation=%#v", factoryPropagation)
				}
				if rootPropagation.ExceptionEventID != string(failure[0].ID) ||
					len(rootPropagation.Targets) != 1 ||
					rootPropagation.Targets[0].ModuleID != "$environment" ||
					rootPropagation.Targets[0].Disposition != "escaped-environment" {
					t.Fatalf("root-to-environment propagation=%#v", rootPropagation)
				}
			}

			contextClosed := false
			for _, record := range first.Contexts {
				if record.Source == child.ModuleID && record.Destination == factory.ModuleID {
					if record.Live || strings.Join(record.LostAfter, ",") != string(failure[0].ID) {
						t.Fatalf("failed child Context interval=%#v", record)
					}
					contextClosed = true
				}
			}
			if !contextClosed {
				t.Fatal("failed child initial Context interval is absent")
			}
			processFound := false
			for _, process := range first.Processes {
				if process.ComponentID != "driver" {
					continue
				}
				processFound = true
				if !process.Terminated || process.Completion != "exception" ||
					process.ExceptionEventID != string(failure[0].ID) || process.State != "" {
					t.Fatalf("failed allocator caller process=%#v", process)
				}
			}
			if !processFound {
				t.Fatal("failed allocator caller process audit is absent")
			}
			finalizationFound := false
			for _, firing := range first.Firings {
				if firing.Transition == "initialization-finalization" &&
					firing.Target == child.ModuleID && len(firing.Generated) == 1 &&
					firing.Generated[0].EventID == child.FinishEventID {
					finalizationFound = true
				}
			}
			if !finalizationFound {
				t.Fatal("failed dynamic initializer finalization firing is absent")
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
				t.Fatal("GOMAXPROCS changed failed dynamic initialization artifact bytes")
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
				t.Fatal("failed dynamic initialization replay changed canonical bytes")
			}
			explored, err := model.ExploreDeterministic(
				journal, arch.ExplorationLimits{MaxExecutions: 64, MaxChoiceDepth: 16},
			)
			if err != nil {
				t.Fatal(err)
			}
			exploredAgain, err := model.ExploreDeterministic(
				journal, arch.ExplorationLimits{MaxExecutions: 64, MaxChoiceDepth: 16},
			)
			if err != nil {
				t.Fatal(err)
			}
			exploredBytes, _ := explored.MarshalCanonical()
			exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
			if !explored.Complete || len(explored.Computations) != 1 ||
				!bytes.Equal(exploredBytes, exploredAgainBytes) {
				t.Fatalf("failed dynamic initialization exploration changed: complete=%v computations=%d",
					explored.Complete, len(explored.Computations))
			}
		})
	}
}

func TestSourceFailedAllocatedModuleInitializationCompletesDeclarativeRuleActivation(t *testing.T) {
	variants := []struct {
		name          string
		operator      string
		ruleProcess   string
		moduleHandler string
		parentHandled bool
	}{
		{name: "pipe unhandled", operator: "=>", ruleProcess: "Pipe"},
		{
			name: "agent parent handler", operator: "||>", ruleProcess: "Agent", parentHandled: true,
			moduleHandler: "handler is Failure(code is ?Code) => ParentRecovered(?Code);",
		},
	}
	template := `
type Factory is interface
  action out Before(value : Integer);
  action out Initialized(value : Integer);
  action out Allocated(value : Factory);
  action out ParentRecovered(value : Integer);
  action out Closing();
  provides Spawn : function(Value : Integer);
end interface Factory;
type Caller is interface
  action out Trigger(value : Integer);
  action out After();
  requires Spawn : function(Value : Integer);
  behavior
  begin
    (?Value : Integer) Trigger(?Value) OPERATOR
      Spawn(?Value);
      After();
    ;
end interface Caller;
module FactoryModule() return Factory is
  exception Failure(code : Integer);
  Spawn : function(Value : Integer) is begin Allocated(New(Seed is Value)); end function Spawn;
initial (Seed : Integer is 1)
  Before(Seed);
  if Seed > 1 then raise Failure(code is Seed); end if;
  Initialized(Seed);
  MODULE_HANDLER
final
  Closing();
end module FactoryModule;
architecture System() is
  factory : Factory is FactoryModule();
  caller : Caller;
connect caller.Spawn to factory.Spawn;
end architecture System;
	`
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			source := []byte(strings.NewReplacer(
				"OPERATOR", variant.operator,
				"MODULE_HANDLER", variant.moduleHandler,
			).Replace(template))
			model, err := Compile(source, "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			journal := arch.NewExecutionJournalWithLimits(
				digest, arch.ExecutionLimits{MaxFirings: 160, MaxStatements: 160},
				arch.InputEvent{
					Key: "trigger", Source: "caller", Action: "Trigger",
					Params: map[string]any{"value": 4},
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

			factory := lifecycleModuleByOccurrence(t, first, "component:factory")
			root := lifecycleModuleByOccurrence(t, first, "architecture:root")
			var child *arch.ModuleLifecycleRecord
			for index := range first.Modules {
				candidate := &first.Modules[index]
				if candidate.Kind == "allocator-module" && candidate.Parent == factory.ModuleID {
					if child != nil {
						t.Fatalf("multiple failed allocator children=%#v/%#v", child, candidate)
					}
					child = candidate
				}
			}
			if child == nil {
				t.Fatal("failed allocator child lifecycle is absent")
			}
			calls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "caller", "Spawn'Call"))
			returns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "caller", "Spawn'Return"))
			before := sourceNamedEvents(first.Poset, child.ModuleID, "Before")
			failure := sourceNamedEvents(first.Poset, child.ModuleID, "Failure")
			start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
			finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
			if len(calls) != 1 || len(returns) != 0 || len(before) != 1 ||
				before[0].ParamInt("value") != 4 || len(failure) != 1 ||
				failure[0].ParamInt("code") != 4 || !startExists || start == nil ||
				!finishExists || finish == nil {
				t.Fatalf("Call/Return/Before/Failure/Finish=%d/%d/%#v/%#v/%#v",
					len(calls), len(returns), before, failure, finish)
			}
			for _, absent := range []struct{ source, name string }{
				{source: "factory", name: "Allocated"},
				{source: "caller", name: "After"},
				{source: child.ModuleID, name: "Initialized"},
				{source: child.ModuleID, name: "Closing"},
				{source: "factory", name: "Closing"},
			} {
				if events := sourceNamedEvents(first.Poset, absent.source, absent.name); len(events) != 0 {
					t.Fatalf("failed rule allocation emitted %s.%s=%#v", absent.source, absent.name, events)
				}
			}
			assertOnlyDirectCause(t, first.Poset, start, calls[0])
			assertOnlyDirectCause(t, first.Poset, before[0], start)
			assertOnlyDirectCause(t, first.Poset, failure[0], before[0])
			assertOnlyDirectCause(t, first.Poset, finish, failure[0])
			if child.State != arch.ModuleFinalizedState || child.Namable ||
				child.TerminationEventID != string(failure[0].ID) || child.FinishEventID == "" {
				t.Fatalf("failed rule child lifecycle=%#v", child)
			}
			for _, name := range child.Names {
				if name.Live || strings.Join(name.LostAfter, ",") != string(failure[0].ID) {
					t.Fatalf("failed rule child retained provisional name=%#v", name)
				}
			}

			parentRecovered := sourceNamedEvents(first.Poset, "factory", "ParentRecovered")
			childPropagation := exceptionPropagationBySource(t, first, child.ModuleID)
			wantDisposition := "delivered"
			if variant.parentHandled {
				wantDisposition = "handled"
				if len(parentRecovered) != 1 || parentRecovered[0].ParamInt("value") != 4 ||
					!first.Poset.IsCausallyBefore(failure[0].ID, parentRecovered[0].ID) ||
					factory.State == arch.ModuleTerminatedState || factory.TerminationEventID != "" ||
					root.State == arch.ModuleTerminatedState || root.TerminationEventID != "" {
					t.Fatalf("handled declarative-rule parent/root recovery=%#v/%#v/%#v", parentRecovered, factory, root)
				}
				if len(first.ExceptionPropagations) != 1 {
					t.Fatalf("handled declarative-rule failure propagated beyond parent=%#v", first.ExceptionPropagations)
				}
			} else {
				if len(parentRecovered) != 0 || factory.State != arch.ModuleTerminatedState ||
					factory.TerminationEventID != string(failure[0].ID) ||
					root.State != arch.ModuleTerminatedState ||
					root.TerminationEventID != string(failure[0].ID) {
					t.Fatalf("unhandled declarative-rule parent/root lifecycle=%#v/%#v/%#v", parentRecovered, factory, root)
				}
				factoryPropagation := exceptionPropagationBySource(t, first, factory.ModuleID)
				rootPropagation := exceptionPropagationBySource(t, first, root.ModuleID)
				if len(factoryPropagation.Targets) != 1 ||
					factoryPropagation.Targets[0].ModuleID != root.ModuleID ||
					factoryPropagation.Targets[0].Disposition != "delivered" ||
					len(rootPropagation.Targets) != 1 ||
					rootPropagation.Targets[0].ModuleID != "$environment" ||
					rootPropagation.Targets[0].Disposition != "escaped-environment" {
					t.Fatalf("unhandled declarative-rule propagation=%#v/%#v", factoryPropagation, rootPropagation)
				}
			}
			if childPropagation.ExceptionEventID != string(failure[0].ID) ||
				len(childPropagation.Targets) != 1 ||
				childPropagation.Targets[0].ModuleID != factory.ModuleID ||
				childPropagation.Targets[0].Disposition != wantDisposition {
				t.Fatalf("declarative-rule child propagation=%#v", childPropagation)
			}

			var ruleFiring *arch.FiringRecord
			finalizationSequence := uint64(0)
			for index := range first.Firings {
				firing := &first.Firings[index]
				if firing.Transition == "rule" && firing.Target == "caller" {
					if ruleFiring != nil {
						t.Fatalf("multiple declarative-rule failure firings=%#v/%#v", ruleFiring, firing)
					}
					ruleFiring = firing
				}
				if firing.Transition == "initialization-finalization" && firing.Target == child.ModuleID {
					finalizationSequence = firing.Sequence
				}
			}
			if ruleFiring == nil || ruleFiring.Completion != "exception" ||
				ruleFiring.ExceptionEventID != string(failure[0].ID) ||
				ruleFiring.RuleProcess != variant.ruleProcess ||
				finalizationSequence == 0 || ruleFiring.Sequence >= finalizationSequence {
				t.Fatalf("declarative-rule failure audit=%#v finalization-sequence=%d", ruleFiring, finalizationSequence)
			}
			for _, process := range first.Processes {
				if process.ComponentID == "caller" {
					t.Fatalf("interface behavior invented a caller process=%#v", process)
				}
			}
			contextClosed := false
			for _, record := range first.Contexts {
				if record.Source == child.ModuleID && record.Destination == factory.ModuleID {
					if record.Live || strings.Join(record.LostAfter, ",") != string(failure[0].ID) {
						t.Fatalf("failed rule child Context interval=%#v", record)
					}
					contextClosed = true
				}
			}
			if !contextClosed {
				t.Fatal("failed rule child initial Context interval is absent")
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
				t.Fatal("GOMAXPROCS changed declarative-rule failed-initialization artifact bytes")
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
				t.Fatal("declarative-rule failed-initialization replay changed canonical bytes")
			}
			explored, err := model.ExploreDeterministic(
				journal, arch.ExplorationLimits{MaxExecutions: 64, MaxChoiceDepth: 16},
			)
			if err != nil {
				t.Fatal(err)
			}
			exploredAgain, err := model.ExploreDeterministic(
				journal, arch.ExplorationLimits{MaxExecutions: 64, MaxChoiceDepth: 16},
			)
			if err != nil {
				t.Fatal(err)
			}
			exploredBytes, _ := explored.MarshalCanonical()
			exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
			if !explored.Complete || len(explored.Computations) != 1 ||
				!bytes.Equal(exploredBytes, exploredAgainBytes) {
				t.Fatalf("declarative-rule failed-initialization exploration changed: complete=%v computations=%d",
					explored.Complete, len(explored.Computations))
			}
		})
	}
}

func TestSourceDeclarativeBehaviorContinuesAfterHandledFailedCreationActivation(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Before(value : Integer);
  action out Initialized(value : Integer);
  action out Allocated(value : Factory);
  action out ParentRecovered(value : Integer);
  provides Spawn : function(Value : Integer);
end interface Factory;
type Caller is interface
  action out Trigger(value : Integer);
  action out After(value : Integer);
  requires Spawn : function(Value : Integer);
  behavior
  begin
    (?Value : Integer) Trigger(?Value) =>
      Spawn(?Value);
      After(?Value);
    ;
end interface Caller;
module FactoryModule() return Factory is
  exception Failure(code : Integer);
  Spawn : function(Value : Integer) is begin Allocated(New(Seed is Value)); end function Spawn;
initial (Seed : Integer is 1)
  Before(Seed);
  if Seed > 1 then raise Failure(code is Seed); end if;
  Initialized(Seed);
handler is Failure(code is ?Code) => ParentRecovered(?Code);
end module FactoryModule;
architecture System() is
  factory : Factory is FactoryModule();
  caller : Caller;
connect caller.Spawn to factory.Spawn;
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
		digest, arch.ExecutionLimits{MaxFirings: 200, MaxStatements: 240},
		arch.InputEvent{
			Key: "fail", Source: "caller", Action: "Trigger",
			Params: map[string]any{"value": 4},
		},
		arch.InputEvent{
			Key: "succeed", Source: "caller", Action: "Trigger",
			Params: map[string]any{"value": 1}, Causes: []string{"fail"},
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

	failures := first.Poset.ByName("Failure")
	if len(failures) != 1 || failures[0].ParamInt("code") != 4 {
		t.Fatalf("failed activation exceptions=%#v", failures)
	}
	recovered := sourceNamedEvents(first.Poset, "factory", "ParentRecovered")
	allocated := sourceNamedEvents(first.Poset, "factory", "Allocated")
	after := sourceNamedEvents(first.Poset, "caller", "After")
	if len(recovered) != 1 || recovered[0].ParamInt("value") != 4 ||
		len(allocated) != 1 || len(after) != 1 || after[0].ParamInt("value") != 1 {
		t.Fatalf("recovery/allocated/continued=%#v/%#v/%#v", recovered, allocated, after)
	}
	calls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "caller", "Spawn'Call"))
	returns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "caller", "Spawn'Return"))
	if len(calls) != 2 || len(returns) != 1 {
		t.Fatalf("continued behavior Call/Return=%d/%d", len(calls), len(returns))
	}
	var laterCall *gorapide.Event
	for _, call := range calls {
		if call.ParamInt("Value") == 1 {
			laterCall = call
		}
	}
	if laterCall == nil || !first.Poset.IsCausallyBefore(failures[0].ID, laterCall.ID) ||
		!first.Poset.IsCausallyBefore(laterCall.ID, after[0].ID) {
		t.Fatalf("pipe failure-to-next-activation causality failure=%s later=%#v after=%s",
			failures[0].ID, laterCall, after[0].ID)
	}
	ruleFirings := make([]arch.FiringRecord, 0, 2)
	for _, firing := range first.Firings {
		if firing.Transition == "rule" && firing.Target == "caller" {
			ruleFirings = append(ruleFirings, firing)
		}
	}
	if len(ruleFirings) != 2 || ruleFirings[0].Completion != "exception" ||
		ruleFirings[0].ExceptionEventID != string(failures[0].ID) ||
		ruleFirings[1].Completion != "" || ruleFirings[1].ExceptionEventID != "" {
		t.Fatalf("continued behavior firing audit=%#v", ruleFirings)
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
		t.Fatal("GOMAXPROCS changed continued behavior artifact bytes")
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
		t.Fatal("continued behavior replay changed canonical bytes")
	}
}

func TestSourceStaticModuleInitialPropagatedFailedCreationFinalizesExactly(t *testing.T) {
	variants := []struct {
		name          string
		moduleHandler string
		moduleProcess string
		handlerRaised bool
	}{
		{name: "unhandled", moduleProcess: "serial when Kick do Running(); end when;"},
		{
			name: "module handler raises replacement", handlerRaised: true,
			moduleHandler: "handler is Failure(code is ?Code) => raise Replacement(code is 9);",
		},
	}
	template := `
type Factory is interface
  action out Before(value : Integer);
  action out Initialized(value : Integer);
  action out Allocated(value : Factory);
  action out ParentRecovered(value : Integer);
  action out AfterInitial();
  action out Kick();
  action out Running();
  action out Closing();
  provides Spawn : function(Value : Integer);
  provides Ping : function();
end interface Factory;
type Driver is interface
  action in Trigger();
  requires Ping : function();
end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  exception Failure(code : Integer);
  exception Replacement(code : Integer);
  Spawn : function(Value : Integer) is begin Allocated(New(Seed is Value)); end function Spawn;
  Ping : function() is begin Running(); end function Ping;
initial (Seed : Integer is 1)
  Before(Seed);
  if Seed = 1 then
    Spawn(4);
    AfterInitial();
  elsif Seed > 1 then
    raise Failure(code is Seed);
  end if;
  Initialized(Seed);
MODULE_PROCESS
MODULE_HANDLER
final
  Closing();
end module FactoryModule;
module DriverModule() return Driver is
serial when Trigger do Ping(); end when;
end module DriverModule;
architecture System() is
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
  stimulus : Stimulus;
connect
  stimulus.Trigger to driver.Trigger;
  driver.Ping to factory.Ping;
end architecture System;
`
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			source := []byte(strings.NewReplacer(
				"MODULE_PROCESS", variant.moduleProcess,
				"MODULE_HANDLER", variant.moduleHandler,
			).Replace(template))
			model, err := Compile(source, "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			journal := arch.NewExecutionJournalWithLimits(
				digest, arch.ExecutionLimits{MaxFirings: 160, MaxStatements: 180},
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

			factory := lifecycleModuleByOccurrence(t, first, "component:factory")
			root := lifecycleModuleByOccurrence(t, first, "architecture:root")
			var child *arch.ModuleLifecycleRecord
			for index := range first.Modules {
				candidate := &first.Modules[index]
				if candidate.Kind == "allocator-module" && candidate.Parent == factory.ModuleID {
					if child != nil {
						t.Fatalf("multiple static-initial children=%#v/%#v", child, candidate)
					}
					child = candidate
				}
			}
			if child == nil {
				t.Fatal("failed static-initial child lifecycle is absent")
			}
			calls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "factory", "Spawn'Call"))
			returns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "factory", "Spawn'Return"))
			before := sourceNamedEvents(first.Poset, child.ModuleID, "Before")
			failures := sourceNamedEvents(first.Poset, child.ModuleID, "Failure")
			childStart, childStartExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
			childFinish, childFinishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
			if len(calls) != 1 || len(returns) != 0 || len(before) != 1 ||
				before[0].ParamInt("value") != 4 || len(failures) != 1 ||
				failures[0].ParamInt("code") != 4 || !childStartExists || childStart == nil ||
				!childFinishExists || childFinish == nil {
				t.Fatalf("Call/Return/Before/Failure/Finish=%d/%d/%#v/%#v/%#v",
					len(calls), len(returns), before, failures, childFinish)
			}
			for _, absent := range []struct{ source, name string }{
				{source: "factory", name: "Allocated"},
				{source: "factory", name: "AfterInitial"},
				{source: "factory", name: "Initialized"},
				{source: "factory", name: "Closing"},
				{source: child.ModuleID, name: "Initialized"},
				{source: child.ModuleID, name: "Closing"},
			} {
				if events := sourceNamedEvents(first.Poset, absent.source, absent.name); len(events) != 0 {
					t.Fatalf("failed static initial emitted %s.%s=%#v", absent.source, absent.name, events)
				}
			}
			for _, process := range first.Processes {
				if process.ComponentID == "factory" {
					t.Fatalf("failed static initializer elaborated process=%#v", process)
				}
			}
			assertOnlyDirectCause(t, first.Poset, childStart, calls[0])
			assertOnlyDirectCause(t, first.Poset, before[0], childStart)
			assertOnlyDirectCause(t, first.Poset, failures[0], before[0])
			assertOnlyDirectCause(t, first.Poset, childFinish, failures[0])
			if child.State != arch.ModuleFinalizedState || child.Namable ||
				child.TerminationEventID != string(failures[0].ID) {
				t.Fatalf("failed static-initial child lifecycle=%#v", child)
			}
			childPropagation := exceptionPropagationBySource(t, first, child.ModuleID)
			wantDisposition := "delivered"
			if variant.handlerRaised {
				wantDisposition = "handler-raised"
			}
			if len(childPropagation.Targets) != 1 ||
				childPropagation.Targets[0].ModuleID != factory.ModuleID ||
				childPropagation.Targets[0].Disposition != wantDisposition {
				t.Fatalf("static-initial child propagation=%#v", childPropagation)
			}

			var initialFiring *arch.FiringRecord
			for index := range first.Firings {
				firing := &first.Firings[index]
				if firing.Transition == "initial" && firing.Target == "factory" {
					initialFiring = firing
					break
				}
			}
			if initialFiring == nil || initialFiring.Completion != "exception" ||
				initialFiring.ExceptionEventID != string(failures[0].ID) {
				t.Fatalf("static-initial exceptional firing=%#v", initialFiring)
			}

			recovered := sourceNamedEvents(first.Poset, "factory", "ParentRecovered")
			running := sourceNamedEvents(first.Poset, "factory", "Running")
			if variant.handlerRaised {
				replacements := sourceNamedEvents(first.Poset, "factory", "Replacement")
				if len(recovered) != 0 || len(running) != 0 || len(replacements) != 1 ||
					replacements[0].ParamInt("code") != 9 ||
					factory.State != arch.ModuleFinalizedState || factory.Namable ||
					factory.TerminationEventID != string(replacements[0].ID) || factory.FinishEventID == "" ||
					root.State != arch.ModuleFinalizedState || root.Namable || root.FinishEventID == "" ||
					root.TerminationEventID != string(replacements[0].ID) {
					t.Fatalf("replacement static-initial lifecycle=%#v/%#v/%#v/%#v/%#v",
						recovered, running, replacements, factory, root)
				}
				if !first.Poset.IsCausallyBefore(failures[0].ID, replacements[0].ID) {
					t.Fatalf("handler replacement is not caused by leaf failure %s -> %s",
						failures[0].ID, replacements[0].ID)
				}
				factoryFinish, exists := first.Poset.Get(gorapide.EventID(factory.FinishEventID))
				if !exists || factoryFinish == nil {
					t.Fatalf("replacement-failed static module Finish=%#v", factoryFinish)
				}
				assertOnlyDirectCause(t, first.Poset, factoryFinish, replacements[0])
				rootFinish, exists := first.Poset.Get(gorapide.EventID(root.FinishEventID))
				if !exists || rootFinish == nil {
					t.Fatalf("replacement-failed architecture Finish=%#v", rootFinish)
				}
				assertOnlyDirectCause(t, first.Poset, rootFinish, replacements[0])
				if !first.Poset.IsCausallyIndependent(factoryFinish.ID, rootFinish.ID) {
					t.Fatal("host unwind ordered static and architecture Finish siblings")
				}
				factoryPropagation := exceptionPropagationBySource(t, first, factory.ModuleID)
				rootPropagation := exceptionPropagationBySource(t, first, root.ModuleID)
				if factoryPropagation.ExceptionEventID != string(replacements[0].ID) ||
					len(factoryPropagation.Targets) != 1 ||
					factoryPropagation.Targets[0].ModuleID != root.ModuleID ||
					factoryPropagation.Targets[0].Disposition != "delivered" ||
					rootPropagation.ExceptionEventID != string(replacements[0].ID) ||
					len(rootPropagation.Targets) != 1 ||
					rootPropagation.Targets[0].ModuleID != "$environment" ||
					rootPropagation.Targets[0].Disposition != "escaped-environment" {
					t.Fatalf("replacement static-initial propagation=%#v/%#v",
						factoryPropagation, rootPropagation)
				}
			} else {
				if len(recovered) != 0 || len(running) != 0 ||
					factory.State != arch.ModuleFinalizedState || factory.Namable ||
					factory.TerminationEventID != string(failures[0].ID) || factory.FinishEventID == "" ||
					root.State != arch.ModuleFinalizedState || root.Namable || root.FinishEventID == "" ||
					root.TerminationEventID != string(failures[0].ID) {
					t.Fatalf("unhandled static-initial lifecycle=%#v/%#v/%#v/%#v",
						recovered, running, factory, root)
				}
				factoryFinish, exists := first.Poset.Get(gorapide.EventID(factory.FinishEventID))
				if !exists || factoryFinish == nil {
					t.Fatalf("failed static module Finish=%#v", factoryFinish)
				}
				assertOnlyDirectCause(t, first.Poset, factoryFinish, failures[0])
				rootFinish, exists := first.Poset.Get(gorapide.EventID(root.FinishEventID))
				if !exists || rootFinish == nil {
					t.Fatalf("failed architecture Finish=%#v", rootFinish)
				}
				assertOnlyDirectCause(t, first.Poset, rootFinish, failures[0])
				if !first.Poset.IsCausallyIndependent(childFinish.ID, factoryFinish.ID) {
					t.Fatal("host unwind ordered child and static-parent Finish siblings")
				}
				if !first.Poset.IsCausallyIndependent(factoryFinish.ID, rootFinish.ID) ||
					!first.Poset.IsCausallyIndependent(childFinish.ID, rootFinish.ID) {
					t.Fatal("host unwind ordered architecture and module Finish siblings")
				}
				factoryPropagation := exceptionPropagationBySource(t, first, factory.ModuleID)
				rootPropagation := exceptionPropagationBySource(t, first, root.ModuleID)
				if factoryPropagation.ExceptionEventID != string(failures[0].ID) ||
					len(factoryPropagation.Targets) != 1 ||
					factoryPropagation.Targets[0].ModuleID != root.ModuleID ||
					factoryPropagation.Targets[0].Disposition != "delivered" ||
					rootPropagation.ExceptionEventID != string(failures[0].ID) ||
					len(rootPropagation.Targets) != 1 ||
					rootPropagation.Targets[0].ModuleID != "$environment" ||
					rootPropagation.Targets[0].Disposition != "escaped-environment" {
					t.Fatalf("unhandled static-initial propagation=%#v/%#v",
						factoryPropagation, rootPropagation)
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
				t.Fatal("GOMAXPROCS changed static-initial failed-creation artifact bytes")
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
				t.Fatal("static-initial failed-creation replay changed canonical bytes")
			}
			explored, err := model.ExploreDeterministic(
				journal, arch.ExplorationLimits{MaxExecutions: 64, MaxChoiceDepth: 16},
			)
			if err != nil {
				t.Fatal(err)
			}
			exploredAgain, err := model.ExploreDeterministic(
				journal, arch.ExplorationLimits{MaxExecutions: 64, MaxChoiceDepth: 16},
			)
			if err != nil {
				t.Fatal(err)
			}
			exploredBytes, _ := explored.MarshalCanonical()
			exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
			if !explored.Complete || len(explored.Computations) != 1 ||
				!bytes.Equal(exploredBytes, exploredAgainBytes) {
				t.Fatalf("static-initial failed-creation exploration changed: complete=%v computations=%d",
					explored.Complete, len(explored.Computations))
			}
		})
	}
}

func TestSourceStaticModuleInitialRecursivePropagatedFailedCreationFinalizesChain(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  action out AfterInitial();
  action out Closing();
  provides Spawn : function(Value : Integer);
end interface Factory;
module FactoryModule() return Factory is
  exception Failure(code : Integer);
  Spawn : function(Value : Integer) is begin Allocated(New(Seed is Value)); end function Spawn;
initial (Seed : Integer is 1)
  if Seed = 1 then
    Spawn(4);
  elsif Seed = 4 then
    Spawn(5);
  elsif Seed = 5 then
    raise Failure(code is Seed);
  end if;
  AfterInitial();
final
  Closing();
end module FactoryModule;
architecture System() is
  factory : Factory is FactoryModule();
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
		digest, arch.ExecutionLimits{MaxFirings: 180, MaxStatements: 200},
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

	factory := lifecycleModuleByOccurrence(t, first, "component:factory")
	root := lifecycleModuleByOccurrence(t, first, "architecture:root")
	var parent, leaf *arch.ModuleLifecycleRecord
	for index := range first.Modules {
		candidate := &first.Modules[index]
		if candidate.Kind == "allocator-module" && candidate.Parent == factory.ModuleID {
			parent = candidate
		}
	}
	if parent == nil {
		t.Fatal("recursive static-initial parent lifecycle is absent")
	}
	for index := range first.Modules {
		candidate := &first.Modules[index]
		if candidate.Kind == "allocator-module" && candidate.Parent == parent.ModuleID {
			leaf = candidate
		}
	}
	if leaf == nil {
		t.Fatal("recursive static-initial leaf lifecycle is absent")
	}
	failures := sourceNamedEvents(first.Poset, leaf.ModuleID, "Failure")
	if len(failures) != 1 || failures[0].ParamInt("code") != 5 {
		t.Fatalf("recursive static-initial failure=%#v", failures)
	}
	if len(first.Poset.ByName("Allocated")) != 0 || len(first.Poset.ByName("AfterInitial")) != 0 ||
		len(first.Poset.ByName("Closing")) != 0 {
		t.Fatalf("recursive failure returned or finalized normally: Allocated=%d After=%d Closing=%d",
			len(first.Poset.ByName("Allocated")), len(first.Poset.ByName("AfterInitial")),
			len(first.Poset.ByName("Closing")))
	}
	for _, module := range []*arch.ModuleLifecycleRecord{leaf, parent, factory} {
		if module.State != arch.ModuleFinalizedState || module.Namable ||
			module.TerminationEventID != string(failures[0].ID) || module.FinishEventID == "" {
			t.Fatalf("recursive static-initial exceptional module=%#v", module)
		}
	}
	if root.State != arch.ModuleFinalizedState || root.Namable || root.FinishEventID == "" ||
		root.TerminationEventID != string(failures[0].ID) {
		t.Fatalf("recursive static-initial root=%#v", root)
	}
	finishes := make([]*gorapide.Event, 0, 4)
	for _, module := range []*arch.ModuleLifecycleRecord{leaf, parent, factory, root} {
		finish, exists := first.Poset.Get(gorapide.EventID(module.FinishEventID))
		if !exists || finish == nil {
			t.Fatalf("recursive static-initial Finish=%#v", finish)
		}
		assertOnlyDirectCause(t, first.Poset, finish, failures[0])
		finishes = append(finishes, finish)
	}
	for left := 0; left < len(finishes); left++ {
		for right := left + 1; right < len(finishes); right++ {
			if !first.Poset.IsCausallyIndependent(finishes[left].ID, finishes[right].ID) {
				t.Fatalf("host unwind ordered recursive Finish siblings %s/%s",
					finishes[left].ID, finishes[right].ID)
			}
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
		t.Fatal("GOMAXPROCS changed recursive static-initial failure artifact bytes")
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
		t.Fatal("recursive static-initial failure replay changed canonical bytes")
	}
}

func TestSourceStaticModuleInitialHandledFailedCreationFinalizesArchitectureCallerExactly(t *testing.T) {
	template := `
type Factory is interface
  action out Allocated(value : Factory);
  action out Recovered(value : Integer);
  action out AfterInitial();
  action out Closing();
  provides Spawn : function(Value : Integer);
end interface Factory;
type Boundary is interface
  action in Trigger();
  action out ArchitectureAfter();
end interface Boundary;
type Passive is interface action out PassiveClosing(); end interface Passive;
module FactoryModule() return Factory is
  exception Failure(code : Integer);
  Spawn : function(Value : Integer) is begin Allocated(New(Seed is Value)); end function Spawn;
initial (Seed : Integer is 1)
  INITIAL_BODY
  AfterInitial();
handler is Failure(code is ?Code) => Recovered(?Code);
final
  Closing();
end module FactoryModule;
module PassiveModule() return Passive is
final
  PassiveClosing();
end module PassiveModule;
architecture System() return Boundary is
  factory : Factory is FactoryModule();
  passive : Passive is PassiveModule();
initial
  ArchitectureAfter();
end architecture System;
`
	for _, test := range []struct {
		name string
		body string
	}{
		{
			name: "static module handler",
			body: "if Seed = 1 then Spawn(4); elsif Seed = 4 then raise Failure(code is Seed); end if;",
		},
		{
			name: "fresh ancestor handler",
			body: "if Seed = 1 then Spawn(4); elsif Seed = 4 then Spawn(5); elsif Seed = 5 then raise Failure(code is Seed); end if;",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(strings.Replace(template, "INITIAL_BODY", test.body, 1))
			model, err := Compile(source, "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			journal := arch.NewExecutionJournalWithLimits(
				digest, arch.ExecutionLimits{MaxFirings: 160, MaxStatements: 180},
				arch.InputEvent{Key: "after-failed-startup", Source: arch.ArchitectureInterfaceID, Action: "Trigger"},
			)
			previous := runtime.GOMAXPROCS(1)
			first, firstErr := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(8)
			second, secondErr := model.ExecuteDeterministic(journal)
			runtime.GOMAXPROCS(previous)
			if firstErr != nil || secondErr != nil {
				t.Fatalf("handled static-initial architecture completion=%v/%v", firstErr, secondErr)
			}
			factory := lifecycleModuleByOccurrence(t, first, "component:factory")
			passive := lifecycleModuleByOccurrence(t, first, "component:passive")
			root := lifecycleModuleByOccurrence(t, first, "architecture:root")
			if factory.State != arch.ModuleFinalizedState || factory.Namable ||
				factory.TerminationEventID != "" || factory.FinishEventID == "" ||
				passive.State != arch.ModuleFinalizedState || passive.Namable ||
				passive.TerminationEventID != "" || passive.FinishEventID == "" ||
				root.State != arch.ModuleFinalizedState || root.Namable ||
				root.TerminationEventID != "" || root.FinishEventID == "" {
				t.Fatalf("handled static/sibling/architecture lifecycles=%#v/%#v/%#v", factory, passive, root)
			}
			failures := first.Poset.ByName("Failure")
			recovered := first.Poset.ByName("Recovered")
			if len(failures) != 1 || len(recovered) != 1 ||
				len(first.Poset.ByName("Allocated")) != 0 ||
				len(first.Poset.ByName("AfterInitial")) != 0 ||
				len(first.Poset.ByName("ArchitectureAfter")) != 0 ||
				len(first.Poset.ByName("Trigger")) != 0 {
				t.Fatalf("handled startup events failure/recovery/allocated/after/architecture/input=%d/%d/%d/%d/%d/%d",
					len(failures), len(recovered), len(first.Poset.ByName("Allocated")),
					len(first.Poset.ByName("AfterInitial")), len(first.Poset.ByName("ArchitectureAfter")),
					len(first.Poset.ByName("Trigger")))
			}
			var factoryClosing *gorapide.Event
			for _, event := range first.Poset.ByName("Closing") {
				if event.Source == factory.ModuleID {
					factoryClosing = event
					break
				}
			}
			factoryFinish, factoryFinishExists := first.Poset.Get(gorapide.EventID(factory.FinishEventID))
			passiveClosing := sourceNamedEvents(first.Poset, passive.ModuleID, "PassiveClosing")
			passiveFinish, passiveFinishExists := first.Poset.Get(gorapide.EventID(passive.FinishEventID))
			rootFinish, rootFinishExists := first.Poset.Get(gorapide.EventID(root.FinishEventID))
			if factoryClosing == nil || !factoryFinishExists || factoryFinish == nil ||
				len(passiveClosing) != 1 || !passiveFinishExists || passiveFinish == nil ||
				!rootFinishExists || rootFinish == nil {
				t.Fatalf("handled startup finalization=%#v/%#v/%#v/%#v/%#v",
					factoryClosing, factoryFinish, passiveClosing, passiveFinish, rootFinish)
			}
			assertOnlyDirectCause(t, first.Poset, factoryClosing, recovered[0])
			assertOnlyDirectCause(t, first.Poset, factoryFinish, factoryClosing)
			assertOnlyDirectCause(t, first.Poset, passiveClosing[0], recovered[0])
			assertOnlyDirectCause(t, first.Poset, passiveFinish, passiveClosing[0])
			assertOnlyDirectCause(t, first.Poset, rootFinish, recovered[0])
			if !first.Poset.IsCausallyIndependent(factoryClosing.ID, rootFinish.ID) ||
				!first.Poset.IsCausallyIndependent(factoryFinish.ID, rootFinish.ID) ||
				!first.Poset.IsCausallyIndependent(passiveClosing[0].ID, factoryClosing.ID) ||
				!first.Poset.IsCausallyIndependent(passiveFinish.ID, rootFinish.ID) {
				t.Fatal("architecture abandonment ordered independent constituent/root finalization branches")
			}
			for _, module := range []*arch.ModuleLifecycleRecord{factory, root} {
				for _, name := range module.Names {
					if name.Live || len(name.LostAfter) != 1 || name.LostAfter[0] != string(recovered[0].ID) {
						t.Fatalf("handled startup name loss=%#v", name)
					}
				}
			}
			var passiveConstituent arch.ModuleNameRecord
			for _, name := range passive.Names {
				if name.Kind == "architecture-constituent" {
					passiveConstituent = name
				}
			}
			if passiveConstituent.NameID == "" || passiveConstituent.Live ||
				len(passiveConstituent.LostAfter) != 1 ||
				passiveConstituent.LostAfter[0] != string(recovered[0].ID) {
				t.Fatalf("handled startup completed sibling name loss=%#v", passiveConstituent)
			}
			for _, process := range first.Processes {
				if process.ComponentID == "factory" {
					t.Fatalf("handled abandoned static initializer elaborated process=%#v", process)
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
				t.Fatal("GOMAXPROCS changed handled static architecture-abandonment bytes")
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
				t.Fatal("handled static architecture-abandonment replay changed bytes")
			}
		})
	}
}

func TestSourceFailedAllocatedModuleInitializationEscapesProcessObjectExpression(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  provides Open : function(Value : Integer) return Integer;
end interface Factory;
type Driver is interface
  action in Trigger(value : Integer);
  action out Body();
  action out After();
  requires Open : function(Value : Integer) return Integer;
end interface Driver;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;
module FactoryModule() return Factory is
  exception Failure(code : Integer);
  Open : function(Value : Integer) return Integer is
    Child : Factory is New(Seed is Value);
  begin
    Allocated(Child);
    return Value;
  end function Open;
initial (Seed : Integer is 1)
  if Seed > 1 then raise Failure(code is Seed); end if;
end module FactoryModule;
module DriverModule() return Driver is
  C : Clock is Make_Clock();
serial when (?Value : Integer) Trigger(?Value) do
  pause C.Ticks(1);
  for Open(?Value) in False next 0 do Body(); end for;
  After();
end when;
end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  (?Value : Integer) stimulus.Trigger(?Value) => driver.Trigger(?Value);
  driver.Open to factory.Open;
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
		digest, arch.ExecutionLimits{MaxFirings: 120, MaxStatements: 120},
		arch.InputEvent{
			Key: "trigger", Source: "stimulus", Action: "Trigger",
			Params: map[string]any{"value": 4},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceNamedEvents(result.Poset, "factory", "Allocated")) != 0 ||
		len(sourceNamedEvents(result.Poset, "driver", "Body")) != 0 ||
		len(sourceNamedEvents(result.Poset, "driver", "After")) != 0 ||
		len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "driver", "Open'Return"))) != 0 {
		t.Fatalf("failed object-expression allocation continued: Allocated=%d Body=%d After=%d Return=%d",
			len(sourceNamedEvents(result.Poset, "factory", "Allocated")),
			len(sourceNamedEvents(result.Poset, "driver", "Body")),
			len(sourceNamedEvents(result.Poset, "driver", "After")),
			len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "driver", "Open'Return"))))
	}
	factory := lifecycleModuleByOccurrence(t, result, "component:factory")
	var child *arch.ModuleLifecycleRecord
	for index := range result.Modules {
		candidate := &result.Modules[index]
		if candidate.Kind == "allocator-module" && candidate.Parent == factory.ModuleID {
			child = candidate
			break
		}
	}
	if child == nil || child.State != arch.ModuleFinalizedState || child.TerminationEventID == "" ||
		child.FinishEventID == "" {
		t.Fatalf("failed object-expression child lifecycle=%#v", child)
	}
	processFound := false
	for _, process := range result.Processes {
		if process.ComponentID == "driver" {
			processFound = process.Terminated && process.Completion == "exception" &&
				process.ExceptionEventID == child.TerminationEventID
		}
	}
	if !processFound {
		t.Fatalf("failed object-expression caller process=%#v", result.Processes)
	}
}

func TestSourceAllocatedModuleInitialHandlersIgnoreDeclarationAndChoiceOrder(t *testing.T) {
	template := `
type Factory is interface
  action out Allocated(value : Factory);
  action out Recovered();
  action out Wrong();
  provides Spawn : function();
  provides Helper : function();
end interface Factory;
type Driver is interface
  action in Trigger();
  requires Spawn : function();
end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module FactoryModule() return Factory is
  EXCEPTIONS
  Helper : function() is begin raise Failure; end function Helper;
  Spawn : function() is begin Allocated(New()); end function Spawn;
initial
  do
    Helper();
  handler
    CHOICES
  end do;
end module FactoryModule;
module DriverModule() return Driver is
serial when Trigger do Spawn(); end when;
end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
connect
  stimulus.Trigger to driver.Trigger;
  driver.Spawn to factory.Spawn;
end architecture System;
`
	variants := []string{
		strings.NewReplacer(
			"EXCEPTIONS", "exception Failure; exception Other;",
			"CHOICES", "is Failure => Recovered(); is Other => Wrong();",
		).Replace(template),
		strings.NewReplacer(
			"EXCEPTIONS", "exception Other; exception Failure;",
			"CHOICES", "is Other => Wrong(); is Failure => Recovered();",
		).Replace(template),
	}

	models := make([]*arch.Architecture, len(variants))
	digests := make([]string, len(variants))
	for index, source := range variants {
		model, err := Compile([]byte(source), "System")
		if err != nil {
			t.Fatalf("variant %d: %v", index, err)
		}
		models[index] = model
		digests[index], err = model.DeterministicModelDigest()
		if err != nil {
			t.Fatalf("variant %d digest: %v", index, err)
		}
	}
	if digests[0] != digests[1] {
		t.Fatalf("declaration/handler choice order changed model digest: %s != %s", digests[0], digests[1])
	}
	journal := arch.NewExecutionJournal(
		digests[0], 100,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := models[0].ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := models[1].ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
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
		t.Fatal("declaration/handler choice order or GOMAXPROCS changed artifact bytes")
	}
}

func TestSourceAllocatedModuleInitialDeclarationBearingDoRecoversExactly(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out FirstRecovered(code : Integer);
  action out SecondRecovered(code : Integer);
  action out FunctionRecovered(code : Integer);
  action out Initialized(value : Integer);
  action out Allocated(value : Factory);
  action out Wrong();
  provides Spawn : function(Value : Integer);
  provides Helper : function(Value : Integer);
end interface Factory;
type Driver is interface
  action in Trigger(value : Integer);
  requires Spawn : function(Value : Integer);
end interface Driver;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;

module FactoryModule() return Factory is
  exception Failure(code : Integer);
  Spawn : function(Value : Integer) is begin Allocated(New(Seed is Value)); end function Spawn;
  Helper : function(Value : Integer) is begin
    declare exception FunctionFailure(code : Integer); do
      raise FunctionFailure(code is Value + 3);
    handler
      is FunctionFailure(code is ?Code) => FunctionRecovered(?Code);
    end do;
  end function Helper;
initial (Seed : Integer is 1)
  declare exception Failure(code : Integer); do
    raise Failure(code is Seed + 1);
  handler
    is FactoryModule::Failure(code is ?Code) => Wrong();
    is Failure(code is ?Code) => FirstRecovered(?Code);
  end do;
  declare exception Failure(code : Integer); do
    raise Failure(code is Seed + 2);
  handler
    is FactoryModule::Failure(code is ?Code) => Wrong();
    is Failure(code is ?Code) => SecondRecovered(?Code);
  end do;
  Helper(Seed);
  Initialized(Seed);
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
		digest, arch.ExecutionLimits{MaxFirings: 120, MaxStatements: 120},
		arch.InputEvent{
			Key: "trigger", Source: "stimulus", Action: "Trigger",
			Params: map[string]any{"value": 5},
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

	allocated := sourceNamedEvents(first.Poset, "factory", "Allocated")
	if len(allocated) != 1 {
		t.Fatalf("allocated events=%#v", allocated)
	}
	value, exists := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || child.Identity() == "" {
		t.Fatalf("allocated child=%#v", value)
	}
	assertRecovery := func(source string, seed int) {
		t.Helper()
		failures := sourceNamedEvents(first.Poset, source, "Failure")
		firstRecovered := sourceNamedEvents(first.Poset, source, "FirstRecovered")
		secondRecovered := sourceNamedEvents(first.Poset, source, "SecondRecovered")
		functionFailures := sourceNamedEvents(first.Poset, source, "FunctionFailure")
		functionRecovered := sourceNamedEvents(first.Poset, source, "FunctionRecovered")
		helperCalls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, source, "Helper'Call"))
		helperReturns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, source, "Helper'Return"))
		initialized := sourceNamedEvents(first.Poset, source, "Initialized")
		if len(failures) != 2 || len(firstRecovered) != 1 || len(secondRecovered) != 1 ||
			len(functionFailures) != 1 || len(functionRecovered) != 1 || len(helperCalls) != 1 ||
			len(helperReturns) != 1 || len(initialized) != 1 || initialized[0].ParamInt("value") != seed {
			t.Fatalf("source %s Failure/First/Second/FunctionFailure/FunctionRecovered/Call/Return/Initialized=%#v/%#v/%#v/%#v/%#v/%#v/%#v/%#v",
				source, failures, firstRecovered, secondRecovered, functionFailures,
				functionRecovered, helperCalls, helperReturns, initialized)
		}
		byCode := make(map[int]*gorapide.Event, len(failures))
		for _, failure := range failures {
			byCode[failure.ParamInt("code")] = failure
		}
		if byCode[seed+1] == nil || byCode[seed+2] == nil ||
			firstRecovered[0].ParamInt("code") != seed+1 ||
			secondRecovered[0].ParamInt("code") != seed+2 ||
			functionFailures[0].ParamInt("code") != seed+3 ||
			functionRecovered[0].ParamInt("code") != seed+3 {
			t.Fatalf("source %s lexical recovery values=%#v/%#v/%#v",
				source, byCode, firstRecovered, secondRecovered)
		}
		assertOnlyDirectCause(t, first.Poset, firstRecovered[0], byCode[seed+1])
		assertOnlyDirectCause(t, first.Poset, byCode[seed+2], firstRecovered[0])
		assertOnlyDirectCause(t, first.Poset, secondRecovered[0], byCode[seed+2])
		assertOnlyDirectCause(t, first.Poset, helperCalls[0], secondRecovered[0])
		assertOnlyDirectCause(t, first.Poset, functionFailures[0], helperCalls[0])
		assertOnlyDirectCause(t, first.Poset, functionRecovered[0], functionFailures[0])
		assertOnlyDirectCause(t, first.Poset, helperReturns[0], functionRecovered[0])
		assertOnlyDirectCause(t, first.Poset, initialized[0], helperReturns[0])
	}
	assertRecovery("factory", 1)
	assertRecovery(child.Identity(), 5)
	childInitialized := sourceNamedEvents(first.Poset, child.Identity(), "Initialized")
	assertOnlyDirectCause(t, first.Poset, allocated[0], childInitialized[0])
	if wrong := sourceNamedEvents(first.Poset, "factory", "Wrong"); len(wrong) != 0 {
		t.Fatalf("module exception declaration captured lexical exception=%#v", wrong)
	}
	if len(first.ExceptionPropagations) != 0 {
		t.Fatalf("locally recovered dynamic exceptions propagated=%#v", first.ExceptionPropagations)
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
		t.Fatal("GOMAXPROCS changed dynamic declaration-bearing initializer recovery")
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
		t.Fatal("dynamic declaration-bearing initializer replay changed canonical bytes")
	}
}

func TestSourceFailedAllocatedModuleInitialLocalExceptionCompletesLifecycle(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Ready(value : Integer);
  action out Allocated(value : Factory);
  action out ParentRecovered();
  action out Wrong(code : Integer);
  action out Closing();
  provides Spawn : function(Value : Integer);
end interface Factory;
type Driver is interface
  action in Trigger(value : Integer);
  action out After();
  requires Spawn : function(Value : Integer);
end interface Driver;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function(Value : Integer) is begin
    do
      Allocated(New(Seed is Value));
    handler
      else Wrong(Value);
    end do;
  end function Spawn;
initial (Seed : Integer is 1)
  declare exception LocalFailure(code : Integer); do
    if Seed > 1 then raise LocalFailure(code is Seed); end if;
  end do;
  Ready(Seed);
handler
  else ParentRecovered();
final
  Closing();
end module FactoryModule;

module DriverModule() return Driver is
serial when (?Value : Integer) Trigger(?Value) do Spawn(?Value); After(); end when;
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
		digest, arch.ExecutionLimits{MaxFirings: 120, MaxStatements: 120},
		arch.InputEvent{
			Key: "trigger", Source: "stimulus", Action: "Trigger",
			Params: map[string]any{"value": 4},
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

	ready := sourceNamedEvents(first.Poset, "factory", "Ready")
	if len(ready) != 1 || ready[0].ParamInt("value") != 1 {
		t.Fatalf("static initialization Ready=%#v", ready)
	}
	factory := lifecycleModuleByOccurrence(t, first, "component:factory")
	driver := lifecycleModuleByOccurrence(t, first, "component:driver")
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
		t.Fatal("failed dynamic child lifecycle is absent")
	}
	failures := sourceNamedEvents(first.Poset, child.ModuleID, "LocalFailure")
	recovered := sourceNamedEvents(first.Poset, "factory", "ParentRecovered")
	finish, finishExists := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	if len(failures) != 1 || failures[0].ParamInt("code") != 4 ||
		len(recovered) != 1 ||
		!finishExists || finish == nil {
		t.Fatalf("LocalFailure/ParentRecovered/Finish=%#v/%#v/%#v", failures, recovered, finish)
	}
	for _, absent := range []struct{ source, name string }{
		{source: "factory", name: "Allocated"},
		{source: "factory", name: "Wrong"},
		{source: "factory", name: "Closing"},
		{source: "driver", name: "After"},
		{source: child.ModuleID, name: "Ready"},
		{source: child.ModuleID, name: "Closing"},
	} {
		if events := sourceNamedEvents(first.Poset, absent.source, absent.name); len(events) != 0 {
			t.Fatalf("failed local initializer emitted %s.%s=%#v", absent.source, absent.name, events)
		}
	}
	start, startExists := first.Poset.Get(gorapide.EventID(child.StartEventID))
	if !startExists || start == nil {
		t.Fatalf("failed dynamic child Start=%#v", start)
	}
	assertOnlyDirectCause(t, first.Poset, failures[0], start)
	assertOnlyDirectCause(t, first.Poset, recovered[0], failures[0])
	assertOnlyDirectCause(t, first.Poset, finish, failures[0])
	if child.State != arch.ModuleFinalizedState || child.Namable ||
		child.TerminationEventID != string(failures[0].ID) {
		t.Fatalf("failed local-exception child lifecycle=%#v", child)
	}
	if factory.State == arch.ModuleTerminatedState || root.State == arch.ModuleTerminatedState ||
		driver.State == arch.ModuleTerminatedState {
		t.Fatalf("handled local child failure terminated factory/root/driver=%#v/%#v/%#v",
			factory, root, driver)
	}
	propagation := exceptionPropagationBySource(t, first, child.ModuleID)
	if len(first.ExceptionPropagations) != 1 || len(propagation.Targets) != 1 ||
		propagation.Targets[0].ModuleID != factory.ModuleID ||
		propagation.Targets[0].Disposition != "handled" {
		t.Fatalf("local child failure propagation=%#v all=%#v", propagation, first.ExceptionPropagations)
	}
	processFound := false
	for _, process := range first.Processes {
		if process.ComponentID == "driver" {
			processFound = process.Terminated && process.Completion == "exception" &&
				process.ExceptionEventID == string(failures[0].ID)
		}
	}
	if !processFound {
		t.Fatalf("local child failure caller process=%#v", first.Processes)
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
		t.Fatal("GOMAXPROCS changed failed local dynamic initialization artifact bytes")
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
		t.Fatal("failed local dynamic initialization replay changed canonical bytes")
	}
}

func TestSourceAllocatedModuleInitialNamedDoControlUsesExactTargets(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Tick(value : Integer);
  action out Recovered();
  action out FunctionSeen();
  action out Initialized(value : Integer);
  action out Allocated(value : Factory);
  action out Wrong();
  provides Spawn : function();
  provides Helper : function();
end interface Factory;
type Driver is interface
  action in Trigger();
  requires Spawn : function();
end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  exception Local;
  Count : var Integer := 0;
  Spawn : function() is begin Allocated(New()); end function Spawn;
  Helper : function() is begin
    FunctionLoop : loop do
      FunctionSeen();
      exit FunctionLoop;
      Wrong();
    end do FunctionLoop;
  end function Helper;
initial
  Outer : loop do
    Count := $Count + 1;
    Tick($Count);
    Inner : loop do
      next Outer where $Count = 1;
      exit Outer;
    end do Inner;
    Wrong();
  end do Outer;
  Plain : do
    next Plain;
    Wrong();
  end do Plain;
  Guarded : declare exception Local; do
    raise Guarded::Local;
  handler
    is FactoryModule::Local => Wrong();
    is FactoryModule::Guarded::Local =>
      Recovered();
      exit Guarded;
      Wrong();
  end do Guarded;
  Helper();
  Initialized($Count);
end module FactoryModule;

module DriverModule() return Driver is
serial when Trigger do Spawn(); end when;
end module DriverModule;

architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
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
		digest, arch.ExecutionLimits{MaxFirings: 160, MaxStatements: 240},
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
		t.Fatalf("allocated events=%#v", allocated)
	}
	value, exists := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || child.Identity() == "" {
		t.Fatalf("allocated child=%#v", value)
	}
	assertNamedControl := func(source string) {
		t.Helper()
		ticks := sourceNamedEvents(first.Poset, source, "Tick")
		recovered := sourceNamedEvents(first.Poset, source, "Recovered")
		local := sourceNamedEvents(first.Poset, source, "Local")
		helperCalls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, source, "Helper'Call"))
		seen := sourceNamedEvents(first.Poset, source, "FunctionSeen")
		helperReturns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, source, "Helper'Return"))
		initialized := sourceNamedEvents(first.Poset, source, "Initialized")
		if len(ticks) != 2 || len(recovered) != 1 || len(local) != 1 ||
			len(helperCalls) != 1 || len(seen) != 1 || len(helperReturns) != 1 ||
			len(initialized) != 1 || initialized[0].ParamInt("value") != 2 {
			t.Fatalf("source %s Tick/Local/Recovered/Call/Seen/Return/Initialized=%#v/%#v/%#v/%#v/%#v/%#v/%#v",
				source, ticks, local, recovered, helperCalls, seen, helperReturns, initialized)
		}
		byValue := make(map[int]*gorapide.Event, len(ticks))
		for _, tick := range ticks {
			byValue[tick.ParamInt("value")] = tick
		}
		if byValue[1] == nil || byValue[2] == nil {
			t.Fatalf("source %s Tick values=%#v", source, byValue)
		}
		sequence := []*gorapide.Event{
			byValue[1], byValue[2], local[0], recovered[0], helperCalls[0],
			seen[0], helperReturns[0], initialized[0],
		}
		for index := 1; index < len(sequence); index++ {
			if !first.Poset.IsCausallyBefore(sequence[index-1].ID, sequence[index].ID) {
				t.Fatalf("source %s named-control sequence %s !< %s",
					source, sequence[index-1].ID, sequence[index].ID)
			}
		}
		if wrong := sourceNamedEvents(first.Poset, source, "Wrong"); len(wrong) != 0 {
			t.Fatalf("source %s named control executed unreachable Wrong=%#v", source, wrong)
		}
	}
	assertNamedControl("factory")
	assertNamedControl(child.Identity())
	childInitialized := sourceNamedEvents(first.Poset, child.Identity(), "Initialized")
	assertOnlyDirectCause(t, first.Poset, allocated[0], childInitialized[0])
	if len(first.ExceptionPropagations) != 0 {
		t.Fatalf("locally recovered named-do exception propagated=%#v", first.ExceptionPropagations)
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
		t.Fatal("GOMAXPROCS changed dynamic initializer named-control artifact")
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
		t.Fatal("dynamic initializer named-control replay changed canonical bytes")
	}
}

func TestSourceAllocatedModuleInitialNamedDoControlRejectsInvalidTargets(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "mismatched terminator",
			body: "Outer : loop do exit; end do Wrong;",
			want: "does not match statement label",
		},
		{
			name: "duplicate label",
			body: "Outer : loop do Outer : loop do exit Outer; end do Outer; end do Outer;",
			want: "overloads do label",
		},
		{
			name: "non-enclosing target",
			body: "Outer : loop do exit Missing; end do Outer;",
			want: "names non-enclosing do",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  provides Spawn : function();
end interface Factory;
module FactoryModule() return Factory is
  Spawn : function() is begin Allocated(New()); end function Spawn;
initial
  ` + test.body + `
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
`)
			model, err := Compile(source, "System")
			if err == nil {
				_, err = model.DeterministicModelDigest()
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile()=%v, want containing %q", err, test.want)
			}
		})
	}
}

func TestSourceAllocatedModuleInitialGeneralForPreservesObjectExpressionPhases(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Checked(value : Integer);
  action out Advanced(value : Integer);
  action out Tick(value : Integer);
  action out Initialized(value : Integer);
  action out Allocated(value : Factory);
  action out Wrong();
  provides Spawn : function();
  provides Initialize : function();
  provides More : function() return Boolean;
  provides Advance : function();
end interface Factory;
type Driver is interface
  action in Trigger();
  requires Spawn : function();
end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Count : var Integer := 99;
  Spawn : function() is begin Allocated(New()); end function Spawn;
  Initialize : function() is begin Count := 0; end function Initialize;
  More : function() return Boolean is begin
    Checked($Count);
    return $Count < 3;
  end function More;
  Advance : function() is begin
    Count := $Count + 1;
    Advanced($Count);
  end function Advance;
initial
  General : for Initialize() in More() next Advance() do
    next General where $Count = 0;
    Tick($Count);
    if $Count = 2 then
      exit General;
      Wrong();
    end if;
  end for General;
  Initialized($Count);
end module FactoryModule;

module DriverModule() return Driver is
serial when Trigger do Spawn(); end when;
end module DriverModule;

architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
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
		digest, arch.ExecutionLimits{MaxFirings: 220, MaxStatements: 360},
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
		t.Fatalf("allocated events=%#v", allocated)
	}
	value, exists := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || child.Identity() == "" {
		t.Fatalf("allocated child=%#v", value)
	}
	assertGeneralFor := func(source string) {
		t.Helper()
		checked := sourceNamedEvents(first.Poset, source, "Checked")
		advanced := sourceNamedEvents(first.Poset, source, "Advanced")
		ticks := sourceNamedEvents(first.Poset, source, "Tick")
		initialized := sourceNamedEvents(first.Poset, source, "Initialized")
		initializeCalls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, source, "Initialize'Call"))
		initializeReturns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, source, "Initialize'Return"))
		moreCalls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, source, "More'Call"))
		moreReturns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, source, "More'Return"))
		advanceCalls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, source, "Advance'Call"))
		advanceReturns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, source, "Advance'Return"))
		if len(checked) != 3 || len(advanced) != 2 || len(ticks) != 2 ||
			len(initialized) != 1 || initialized[0].ParamInt("value") != 2 ||
			len(initializeCalls) != 1 || len(initializeReturns) != 1 ||
			len(moreCalls) != 3 || len(moreReturns) != 3 ||
			len(advanceCalls) != 2 || len(advanceReturns) != 2 {
			t.Fatalf("source %s Checked/Advanced/Tick/Initialized/Init/More/Advance=%#v/%#v/%#v/%#v/%d:%d/%d:%d/%d:%d",
				source, checked, advanced, ticks, initialized,
				len(initializeCalls), len(initializeReturns), len(moreCalls), len(moreReturns),
				len(advanceCalls), len(advanceReturns))
		}
		byValue := func(events []*gorapide.Event) map[int]*gorapide.Event {
			result := make(map[int]*gorapide.Event, len(events))
			for _, event := range events {
				result[event.ParamInt("value")] = event
			}
			return result
		}
		checkedByValue := byValue(checked)
		advancedByValue := byValue(advanced)
		ticksByValue := byValue(ticks)
		sequence := []*gorapide.Event{
			checkedByValue[0], advancedByValue[1], checkedByValue[1], ticksByValue[1],
			advancedByValue[2], checkedByValue[2], ticksByValue[2], initialized[0],
		}
		for index, event := range sequence {
			if event == nil {
				t.Fatalf("source %s general-for value sequence %d is absent", source, index)
			}
			if index != 0 && !first.Poset.IsCausallyBefore(sequence[index-1].ID, event.ID) {
				t.Fatalf("source %s general-for sequence %s !< %s",
					source, sequence[index-1].ID, event.ID)
			}
		}
		if wrong := sourceNamedEvents(first.Poset, source, "Wrong"); len(wrong) != 0 {
			t.Fatalf("source %s general-for exit executed Wrong=%#v", source, wrong)
		}
		stateFound := false
		for _, state := range first.State {
			if state.ComponentID == source && state.Name == "Count" {
				stateFound = state.Value.Text == "2" && state.Version == 3
			}
		}
		if !stateFound {
			t.Fatalf("source %s general-for state=%#v", source, first.State)
		}
	}
	assertGeneralFor("factory")
	assertGeneralFor(child.Identity())
	childInitialized := sourceNamedEvents(first.Poset, child.Identity(), "Initialized")
	assertOnlyDirectCause(t, first.Poset, allocated[0], childInitialized[0])
	if len(first.ExceptionPropagations) != 0 {
		t.Fatalf("general-for initialization propagated exception=%#v", first.ExceptionPropagations)
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
		t.Fatal("GOMAXPROCS changed dynamic initializer general-for artifact")
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
		t.Fatal("dynamic initializer general-for replay changed canonical bytes")
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
		t.Fatalf("dynamic initializer general-for exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourceAllocatedModuleInitialSelfInterruptsAtDynamicIdentity(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Signal(value : Integer);
  private action Pulse();
  action out Recovered(value : Integer);
  action out AnyRecovered();
  action out AnyExceptionRecovered();
  action out FunctionRecovered(value : Integer);
  action out Initialized();
  action out Allocated(value : Factory);
  action out Wrong();
  provides Spawn : function();
  provides Helper : function();
end interface Factory;
type Driver is interface
  action in Trigger();
  requires Spawn : function();
end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  exception Failure;
  Spawn : function() is begin Allocated(New()); end function Spawn;
  Helper : function() is
  begin
    Signal(9);
    Wrong();
  handler
    is Signal(value is ?Value) => FunctionRecovered(?Value);
  end function Helper;
initial
  do
    Signal(4);
    Wrong();
  handler
    is Signal(value is ?Value) => Recovered(?Value);
  end do;
  do
    Pulse();
    Wrong();
  handler
    is any => AnyRecovered();
  end do;
  do
    raise Failure;
    Wrong();
  handler
    is any => AnyExceptionRecovered();
  end do;
  Helper();
  Initialized();
end module FactoryModule;

module DriverModule() return Driver is
serial when Trigger do Spawn(); end when;
end module DriverModule;

architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule();
  driver : Driver is DriverModule();
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
		digest, arch.ExecutionLimits{MaxFirings: 220, MaxStatements: 320},
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
		t.Fatalf("allocated events=%#v", allocated)
	}
	value, exists := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || child.Identity() == "" {
		t.Fatalf("allocated child=%#v", value)
	}
	assertInterrupts := func(source string) {
		t.Helper()
		signals := sourceNamedEvents(first.Poset, source, "Signal")
		pulses := sourceNamedEvents(first.Poset, source, "Pulse")
		recovered := sourceNamedEvents(first.Poset, source, "Recovered")
		anyRecovered := sourceNamedEvents(first.Poset, source, "AnyRecovered")
		failures := sourceNamedEvents(first.Poset, source, "Failure")
		anyExceptionRecovered := sourceNamedEvents(first.Poset, source, "AnyExceptionRecovered")
		functionRecovered := sourceNamedEvents(first.Poset, source, "FunctionRecovered")
		initialized := sourceNamedEvents(first.Poset, source, "Initialized")
		calls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, source, "Helper'Call"))
		returns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, source, "Helper'Return"))
		if len(signals) != 2 || len(pulses) != 1 || len(recovered) != 1 ||
			len(anyRecovered) != 1 || len(failures) != 1 || len(anyExceptionRecovered) != 1 ||
			len(functionRecovered) != 1 ||
			len(initialized) != 1 || len(calls) != 1 || len(returns) != 1 {
			t.Fatalf("source %s Signal/Pulse/Recovered/Any/Failure/AnyException/Function/Initialized/Call/Return=%d/%d/%d/%d/%d/%d/%d/%d/%d/%d",
				source, len(signals), len(pulses), len(recovered), len(anyRecovered),
				len(failures), len(anyExceptionRecovered), len(functionRecovered),
				len(initialized), len(calls), len(returns))
		}
		signalByValue := make(map[int]*gorapide.Event, len(signals))
		for _, signal := range signals {
			signalByValue[signal.ParamInt("value")] = signal
		}
		if signalByValue[4] == nil || signalByValue[9] == nil ||
			recovered[0].ParamInt("value") != 4 ||
			functionRecovered[0].ParamInt("value") != 9 {
			t.Fatalf("source %s interrupt binding Signal/Recovered/Function=%#v/%#v/%#v",
				source, signals, recovered, functionRecovered)
		}
		assertOnlyDirectCause(t, first.Poset, recovered[0], signalByValue[4])
		assertOnlyDirectCause(t, first.Poset, anyRecovered[0], pulses[0])
		assertOnlyDirectCause(t, first.Poset, anyExceptionRecovered[0], failures[0])
		assertOnlyDirectCause(t, first.Poset, functionRecovered[0], signalByValue[9])
		sequence := []*gorapide.Event{
			signalByValue[4], recovered[0], pulses[0], anyRecovered[0], failures[0],
			anyExceptionRecovered[0], calls[0],
			signalByValue[9], functionRecovered[0], returns[0], initialized[0],
		}
		for index := 1; index < len(sequence); index++ {
			if !first.Poset.IsCausallyBefore(sequence[index-1].ID, sequence[index].ID) {
				t.Fatalf("source %s interrupt sequence %s !< %s",
					source, sequence[index-1].ID, sequence[index].ID)
			}
		}
		if wrong := sourceNamedEvents(first.Poset, source, "Wrong"); len(wrong) != 0 {
			t.Fatalf("source %s interrupt executed abandoned Wrong=%#v", source, wrong)
		}
	}
	assertInterrupts("factory")
	assertInterrupts(child.Identity())
	childInitialized := sourceNamedEvents(first.Poset, child.Identity(), "Initialized")
	assertOnlyDirectCause(t, first.Poset, allocated[0], childInitialized[0])
	if len(first.ExceptionPropagations) != 0 {
		t.Fatalf("dynamic initializer action interrupt propagated exception=%#v", first.ExceptionPropagations)
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
		t.Fatal("GOMAXPROCS changed dynamic initializer self-interrupt artifact")
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
		t.Fatal("dynamic initializer self-interrupt replay changed canonical bytes")
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
		t.Fatalf("dynamic initializer self-interrupt exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}
