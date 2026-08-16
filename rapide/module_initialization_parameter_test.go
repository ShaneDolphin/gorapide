package rapide

import (
	"bytes"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceModuleInitializationParametersDriveStaticAndAllocatorCreation(t *testing.T) {
	compile := func(explicit string) *arch.Architecture {
		source := []byte(`
type Factory is interface
  action out Initialized(value : Integer; next : Integer);
  action out Allocated(value : Factory);
  provides Spawn : function(Value : Integer);
end interface Factory;
type Driver is interface
  action in Trigger(value : Integer);
  requires Spawn : function(Value : Integer);
end interface Driver;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;

module FactoryModule() return Factory is
  Current : var Integer := 5;
  Spawn : function(Value : Integer) is
  begin
    Current := Value;
    Allocated(New());
    Allocated(New(` + explicit + `));
  end function Spawn;
initial (Initial : Integer is $Current; Next : Integer is Initial + 1)
  Current := Initial;
  Initialized(Initial, Next);
end module FactoryModule;

module DriverModule() return Driver is
serial
  when (?Value : Integer) Trigger(?Value) do
    Spawn(?Value);
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
`)
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		return model
	}
	named := compile("Initial is Value + 1")
	positional := compile("Value + 1")
	namedDigest, err := named.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	positionalDigest, err := positional.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if namedDigest != positionalDigest {
		t.Fatalf("named/positional initialization association digests=%q/%q", namedDigest, positionalDigest)
	}
	journal := arch.NewExecutionJournal(namedDigest, 100,
		arch.InputEvent{
			Key: "trigger", Source: "stimulus", Action: "Trigger",
			Params: map[string]any{"value": 7},
		},
	)
	previous := runtime.GOMAXPROCS(1)
	first, err := named.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, secondErr := positional.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if secondErr != nil {
		t.Fatal(secondErr)
	}

	staticInitial := sourceNamedEvents(first.Poset, "factory", "Initialized")
	if len(staticInitial) != 1 || staticInitial[0].ParamInt("value") != 5 ||
		staticInitial[0].ParamInt("next") != 6 {
		t.Fatalf("static generator initialization=%#v", staticInitial)
	}
	allocated := sourceNamedEvents(first.Poset, "factory", "Allocated")
	if len(allocated) != 2 {
		t.Fatalf("allocated events=%#v", allocated)
	}
	childValues := make(map[string]int, len(allocated))
	for _, event := range allocated {
		value, present := event.Param("value")
		module, ok := value.(gorapide.RapideModuleValue)
		if !present || !ok || module.Identity() == "" {
			t.Fatalf("allocated module value=%#v", value)
		}
		initialized := sourceNamedEvents(first.Poset, module.Identity(), "Initialized")
		if len(initialized) != 1 {
			t.Fatalf("child %s initialization=%#v", module.Identity(), initialized)
		}
		actual := initialized[0].ParamInt("value")
		if initialized[0].ParamInt("next") != actual+1 {
			t.Fatalf("child %s chained initialization defaults=%#v", module.Identity(), initialized[0].Params)
		}
		childValues[module.Identity()] = actual
		assertOnlyDirectCause(t, first.Poset, event, initialized[0])
		var lifecycle arch.ModuleLifecycleRecord
		for _, candidate := range first.Modules {
			if candidate.ModuleID == module.Identity() {
				lifecycle = candidate
				break
			}
		}
		start, exists := first.Poset.Get(gorapide.EventID(lifecycle.StartEventID))
		if lifecycle.State != arch.ModuleFinalizedState || !exists {
			t.Fatalf("child %s lifecycle=%#v", module.Identity(), lifecycle)
		}
		assertOnlyDirectCause(t, first.Poset, initialized[0], start)
	}
	seenSeven, seenEight := false, false
	for _, value := range childValues {
		seenSeven = seenSeven || value == 7
		seenEight = seenEight || value == 8
	}
	if !seenSeven || !seenEight {
		t.Fatalf("allocator default/override child values=%#v", childValues)
	}
	state := make(map[string]arch.StateRecord, len(first.State))
	for _, record := range first.State {
		state[record.ComponentID] = record
	}
	for child, value := range childValues {
		record, exists := state[child]
		if !exists || record.Name != "Current" || record.Version != 1 ||
			record.Value.Text != strconv.Itoa(value) {
			t.Fatalf("child %s final state=%#v, value=%d", child, record, value)
		}
	}
	foundAllocatorDefaultRead := false
	for _, operation := range first.StateOperations {
		if operation.ComponentID == "factory" && operation.Name == "Current" &&
			operation.Kind == arch.StateOperationDereference && strings.Contains(operation.Owner, "allocator New") {
			foundAllocatorDefaultRead = true
		}
	}
	if !foundAllocatorDefaultRead {
		t.Fatal("allocator default did not audit its caller-state dereference")
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
		t.Fatal("association spelling or GOMAXPROCS changed initialization artifacts")
	}
	expected, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := named.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("module initialization parameter replay changed canonical artifact bytes")
	}
	explorationJournal := journal
	explorationJournal.Choices = make([]arch.ChoiceDecision, len(first.Choices))
	for index, choice := range first.Choices {
		explorationJournal.Choices[index] = choice.Decision()
	}
	explored, err := named.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := named.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || len(explored.Computations) != 1 ||
		!bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("module initialization fixed exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestParseModuleInitializationParameterRestrictions(t *testing.T) {
	valid, err := Parse([]byte(`
type API is interface action out Ready(value : Integer); end interface API;
module M() return API is
initial (First : Integer is 1; Second : Integer is First + 1)
  Ready(Second);
end module M;
architecture System() is api : API is M(); end architecture System;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(valid.Modules) != 1 || len(valid.Modules[0].InitialParameters) != 2 ||
		valid.Modules[0].InitialParameters[1].Name != "Second" {
		t.Fatalf("initialization parameter AST=%#v", valid.Modules)
	}
	for _, test := range []struct {
		name   string
		params string
		want   string
	}{
		{name: "empty", params: "()", want: "may not be empty"},
		{name: "type", params: "(type Item)", want: "may not include type formals"},
		{name: "missing default", params: "(Value : Integer)", want: "requires a default association"},
		{name: "no statements", params: "(Value : Integer is 1)", want: "requires at least one statement"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := " Ready();"
			if test.name == "no statements" {
				body = ""
			}
			_, err := Parse([]byte(`
type API is interface action out Ready(); end interface API;
module M() return API is initial ` + test.params + body + ` end module M;
architecture System() is api : API is M(); end architecture System;
`))
			if err == nil || !bytes.Contains([]byte(err.Error()), []byte(test.want)) {
				t.Fatalf("Parse()=%v, want %q", err, test.want)
			}
		})
	}
}

func TestSourceModuleInitializationParametersRejectUnsupportedForms(t *testing.T) {
	for _, test := range []struct {
		name       string
		parameters string
		initial    string
		newActual  string
		want       string
	}{
		{
			name: "wrong default type", parameters: "Value : Integer is True",
			initial: "Ready(1);", newActual: "", want: "default has type Boolean, want Integer",
		},
		{
			name: "duplicate formal", parameters: "Value : Integer is 1; value : Integer is 2",
			initial: "Ready(Value);", newActual: "", want: "empty, duplicate, or conflicts",
		},
		{
			name: "unknown named actual", parameters: "Value : Integer is 1",
			initial: "Ready(Value);", newActual: "Missing is 2", want: "has no formal named \"Missing\"",
		},
		{
			name: "actual type mismatch", parameters: "Value : Integer is 1",
			initial: "Ready(Value);", newActual: "True", want: "actual type Boolean, want Integer",
		},
		{
			name: "nested New in general-for initializer", parameters: "Value : Integer is 1",
			initial: "for New() in False next 0 do Ready(Value); end for;", newActual: "2",
			want: "requires the current deterministic dynamic-module specialization slice",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type API is interface
  action out Ready(value : Integer);
  action out Allocated(value : API);
  provides Spawn : function();
end interface API;
module M() return API is
  Spawn : function() is begin Allocated(New(` + test.newActual + `)); end function Spawn;
initial (` + test.parameters + `)
  ` + test.initial + `
end module M;
architecture System() is api : API is M(); end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile()=%v, want error containing %q", err, test.want)
			}
		})
	}
}
