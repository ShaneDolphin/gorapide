package rapide

import (
	"bytes"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func distinctAllocatorEvents(events gorapide.EventSet) gorapide.EventSet {
	seen := make(map[gorapide.EventID]bool, len(events))
	result := make(gorapide.EventSet, 0, len(events))
	for _, event := range events {
		if event != nil && !seen[event.ID] {
			seen[event.ID] = true
			result = append(result, event)
		}
	}
	return result
}

func TestParseNamedAllocatorExpressionAssociations(t *testing.T) {
	file, err := Parse([]byte(`
type Factory is interface provides Spawn : function(); end interface Factory;
module FactoryModule(Seed : Integer; Offset : Integer) return Factory is
  Spawn : function() is
    Child : Factory is New(Offset is Offset, Seed is Seed);
  begin null; end function Spawn;
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(1, 2); end architecture System;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Modules) != 1 || len(file.Modules[0].Functions) != 1 ||
		len(file.Modules[0].Functions[0].Objects) != 1 {
		t.Fatalf("parsed allocator function=%#v", file.Modules)
	}
	initial := file.Modules[0].Functions[0].Objects[0].Initial
	if initial.Kind != ExpressionCall || !keyword(initial.Name, "New") ||
		len(initial.Arguments) != 2 || len(initial.ArgumentFormals) != 2 ||
		initial.ArgumentFormals[0] != "Offset" || initial.ArgumentFormals[1] != "Seed" ||
		initial.Arguments[0].Name != "Offset" || initial.Arguments[1].Name != "Seed" {
		t.Fatalf("named allocator expression=%#v", initial)
	}
}

func TestSourceAllocatorNewRejectsForbiddenOrUnimplementedForms(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "interface declaration",
			source: `
type API is interface provides New : function(); end interface API;
architecture System() is api : API; end architecture System;
`,
			want: "may not declare allocator function New",
		},
		{
			name: "outside owner",
			source: `
type API is interface action out Allocated(value : API); end interface API;
architecture System() return API is
initial Allocated(New());
end architecture System;
`,
			want: "allocator New is callable only from its owning module",
		},
		{
			name: "too many generator actuals",
			source: `
type API is interface action out Allocated(value : API); provides Spawn : function(); end interface API;
module M() return API is
  Spawn : function() is begin Allocated(New(1)); end function Spawn;
end module M;
architecture System() is api : API is M(); end architecture System;
`,
			want: "allocator New supplies 1 generator actuals, but 0 are declared",
		},
		{
			name: "missing required generator actual",
			source: `
type API is interface action out Allocated(value : API); provides Spawn : function(); end interface API;
module M(Seed : Integer) return API is
  Spawn : function() is begin Allocated(New()); end function Spawn;
end module M;
architecture System() is api : API is M(1); end architecture System;
`,
			want: "allocator New formal \"Seed\" has no actual or default",
		},
		{
			name: "different generator specialization",
			source: `
type API is interface action out Allocated(value : API); provides Spawn : function(); end interface API;
module M(Seed : Integer) return API is
  Spawn : function() is begin Allocated(New(Seed + 1)); end function Spawn;
end module M;
architecture System() is api : API is M(1); end architecture System;
`,
			want: "allocator New actual for formal \"Seed\" selects a different module specialization",
		},
		{
			name: "omitted actual uses default rather than current specialization",
			source: `
type API is interface action out Allocated(value : API); provides Spawn : function(); end interface API;
module M(Seed : Integer is 1) return API is
  Spawn : function() is begin Allocated(New()); end function Spawn;
end module M;
architecture System() is api : API is M(2); end architecture System;
`,
			want: "allocator New actual for formal \"Seed\" selects a different module specialization",
		},
		{
			name: "open function actual",
			source: `
type API is interface action out Allocated(value : API); provides Spawn : function(Value : Integer); end interface API;
module M(Seed : Integer) return API is
  Spawn : function(Value : Integer) is begin Allocated(New(Value)); end function Spawn;
end module M;
architecture System() is api : API is M(1); end architecture System;
`,
			want: "allocator New formal \"Seed\" requires a closed deterministic actual",
		},
		{
			name: "unknown named generator formal",
			source: `
type API is interface action out Allocated(value : API); provides Spawn : function(); end interface API;
module M(Seed : Integer) return API is
  Spawn : function() is begin Allocated(New(Missing is Seed)); end function Spawn;
end module M;
architecture System() is api : API is M(1); end architecture System;
`,
			want: "allocator New has no generator formal named \"Missing\"",
		},
		{
			name: "duplicate named generator formal",
			source: `
type API is interface action out Allocated(value : API); provides Spawn : function(); end interface API;
module M(Seed : Integer; Offset : Integer is 2) return API is
  Spawn : function() is begin Allocated(New(Seed, Seed is Seed)); end function Spawn;
end module M;
architecture System() is api : API is M(1, 2); end architecture System;
`,
			want: "allocator New supplies generator formal \"Seed\" more than once",
		},
		{
			name: "positional after named generator formal",
			source: `
type API is interface action out Allocated(value : API); provides Spawn : function(); end interface API;
module M(Seed : Integer; Offset : Integer) return API is
  Spawn : function() is begin Allocated(New(Seed is Seed, Offset)); end function Spawn;
end module M;
architecture System() is api : API is M(1, 2); end architecture System;
`,
			want: "positional expression-call arguments must precede named associations",
		},
		{
			name: "structural Record module object",
			source: `
type Config is record Value : Integer; end record Config;
type API is interface
  Setting : Config;
  action out Allocated(value : API);
  provides Spawn : function();
end interface API;
module M() return API is
	Setting : Config is (Value is 1);
  Spawn : function() is begin Allocated(New()); end function Spawn;
end module M;
architecture System() is api : API is M(); end architecture System;
`,
			want: "allocator New requires the current deterministic dynamic-module specialization slice",
		},
		{
			name: "dynamic process module handler",
			source: `
type API is interface
  action in Trigger(); action out Allocated(value : API);
  provides Spawn : function();
end interface API;
module M() return API is
  exception Failure;
  Spawn : function() is begin Allocated(New()); end function Spawn;
serial when Trigger do null; end when;
handler is Failure => null;
end module M;
architecture System() is api : API is M(); end architecture System;
`,
			want: "allocator New requires the current deterministic dynamic-module specialization slice",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile()=%v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestSourceAllocatorNewElaboratesDynamicTimedProcessAndFinalizesAfterCompletion(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Factory is interface
  action in Trigger();
  action out Ready();
  action out Allocated(value : Factory);
  action out ChildDone();
  action out Closing();
end interface Factory;

module FactoryModule() return Factory is
  ChildMode : var Boolean := False;
  C : Clock is Make_Clock();
initial (Child : Boolean is False)
  ChildMode := Child;
  if Child then Ready(); end if;
serial
  await Trigger where not $ChildMode =>
    Allocated(New(True));
  or Ready where $ChildMode =>
    pause C.Ticks(2);
    ChildDone();
  end await;
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
	journal := arch.NewExecutionJournal(digest, 100,
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

	allocated := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "factory", "Allocated"))
	if len(allocated) != 1 {
		t.Fatalf("dynamic process allocation events=%#v", allocated)
	}
	value, present := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !present || !ok || child.Identity() == "" {
		t.Fatalf("dynamic process allocation value=%#v", value)
	}
	childID := child.Identity()
	ready := sourceNamedEvents(first.Poset, childID, "Ready")
	done := sourceNamedEvents(first.Poset, childID, "ChildDone")
	closing := sourceNamedEvents(first.Poset, childID, "Closing")
	if len(ready) != 1 || len(done) != 1 || len(closing) != 1 {
		t.Fatalf("dynamic child Ready/ChildDone/Closing=%d/%d/%d", len(ready), len(done), len(closing))
	}
	var lifecycle *arch.ModuleLifecycleRecord
	for index := range first.Modules {
		if first.Modules[index].ModuleID == childID {
			lifecycle = &first.Modules[index]
			break
		}
	}
	if lifecycle == nil || lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
		lifecycle.FinishEventID == "" || lifecycle.TerminationEventID != "" {
		t.Fatalf("dynamic active child lifecycle=%#v", lifecycle)
	}
	finish, exists := first.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if !exists {
		t.Fatalf("dynamic active child Finish %q is absent", lifecycle.FinishEventID)
	}
	var childProcess *arch.ProcessExecutionRecord
	for index := range first.Processes {
		if first.Processes[index].ComponentID == childID {
			childProcess = &first.Processes[index]
			break
		}
	}
	if childProcess == nil || !childProcess.Terminated || childProcess.Completion != "normal" ||
		len(childProcess.Frontier) != 1 || childProcess.Frontier[0] != string(done[0].ID) {
		t.Fatalf("dynamic child process audit=%#v", childProcess)
	}
	if !first.Poset.IsCausallyIndependent(allocated[0].ID, done[0].ID) {
		t.Fatal("allocator-result name loss and dynamic process completion acquired a false order")
	}
	closingCauses := first.Poset.DirectCauses(closing[0].ID)
	if len(closingCauses) != 2 {
		t.Fatalf("dynamic child final-part causes=%#v", closingCauses)
	}
	closingCauseIDs := map[gorapide.EventID]bool{}
	for _, cause := range closingCauses {
		closingCauseIDs[cause.ID] = true
	}
	if !closingCauseIDs[allocated[0].ID] || !closingCauseIDs[done[0].ID] {
		t.Fatalf("dynamic child final part lacks name-loss/process-completion conjunction: %#v", closingCauses)
	}
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	clockID := arch.ClockID(childID, "C")
	if len(first.ClockAdvances) != 1 || first.ClockAdvances[0].Clock != clockID ||
		first.ClockAdvances[0].From != "0" || first.ClockAdvances[0].To != "2" {
		t.Fatalf("dynamic child clock rebinding/advance=%#v", first.ClockAdvances)
	}

	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed dynamic-process allocation artifact bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("dynamic-process allocation replay changed canonical bytes")
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
		t.Fatalf("dynamic-process exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourceAllocatorNewFinalizesAfterEveryDynamicParallelProcessCompletes(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Factory is interface
  action in Trigger(); action in Never();
  action out Ready(); action out Allocated(value : Factory);
  action out Left(); action out Right(); action out Closing();
end interface Factory;
module FactoryModule() return Factory is
  ChildMode : var Boolean := False;
initial (Child : Boolean is False)
  ChildMode := Child;
  if Child then Ready(); end if;
parallel
  await Trigger where not $ChildMode =>
    Allocated(New(True));
  or Ready where $ChildMode =>
    Left();
  end await;
||
  await Never where not $ChildMode =>
    null;
  or Ready where $ChildMode =>
    Right();
  end await;
final Closing();
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 100,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	allocated := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Allocated"))
	if len(allocated) != 1 {
		t.Fatalf("parallel dynamic allocation events=%#v", allocated)
	}
	value, present := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !present || !ok || child.Identity() == "" {
		t.Fatalf("parallel dynamic allocation value=%#v", value)
	}
	childID := child.Identity()
	left := sourceNamedEvents(result.Poset, childID, "Left")
	right := sourceNamedEvents(result.Poset, childID, "Right")
	closing := sourceNamedEvents(result.Poset, childID, "Closing")
	if len(left) != 1 || len(right) != 1 || len(closing) != 1 {
		t.Fatalf("parallel dynamic Left/Right/Closing=%d/%d/%d", len(left), len(right), len(closing))
	}
	if !result.Poset.IsCausallyIndependent(left[0].ID, right[0].ID) ||
		!result.Poset.IsCausallyIndependent(allocated[0].ID, left[0].ID) ||
		!result.Poset.IsCausallyIndependent(allocated[0].ID, right[0].ID) {
		t.Fatal("parallel dynamic process branches or allocator-result loss acquired a scheduler edge")
	}
	causes := result.Poset.DirectCauses(closing[0].ID)
	causeIDs := make(map[gorapide.EventID]bool, len(causes))
	for _, cause := range causes {
		causeIDs[cause.ID] = true
	}
	if len(causes) != 3 || !causeIDs[allocated[0].ID] ||
		!causeIDs[left[0].ID] || !causeIDs[right[0].ID] {
		t.Fatalf("parallel dynamic finalization conjunction=%#v", causes)
	}
	processes := 0
	for _, process := range result.Processes {
		if process.ComponentID != childID {
			continue
		}
		processes++
		if !process.Terminated || process.Completion != "normal" || len(process.Frontier) != 1 {
			t.Fatalf("parallel dynamic child process=%#v", process)
		}
	}
	if processes != 2 {
		t.Fatalf("parallel dynamic child process records=%d, want 2", processes)
	}
	var lifecycle *arch.ModuleLifecycleRecord
	for index := range result.Modules {
		if result.Modules[index].ModuleID == childID {
			lifecycle = &result.Modules[index]
			break
		}
	}
	if lifecycle == nil || lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
		lifecycle.FinishEventID == "" {
		t.Fatalf("parallel dynamic child lifecycle=%#v", lifecycle)
	}
}

func TestSourceDynamicProcessCanAllocateFromItsCanonicalGeneratorTemplate(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Factory is interface
  action in Trigger(); action out Ready(depth : Integer);
  action out Allocated(value : Factory; depth : Integer);
  action out Done(depth : Integer); action out Closing();
end interface Factory;
module FactoryModule() return Factory is
  Depth : var Integer := 0;
initial (InitialDepth : Integer is 0)
  Depth := InitialDepth;
  if InitialDepth > 0 then Ready(InitialDepth); end if;
serial
  await Trigger where $Depth = 0 =>
    Allocated(New(1), 1);
  or (?D : Integer) Ready(?D) where $Depth = 1 =>
    Allocated(New(2), 2);
  or (?D : Integer) Ready(?D) where $Depth = 2 =>
    Done(?D);
  end await;
final Closing();
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 140,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	allocated := distinctAllocatorEvents(result.Poset.ByName("Allocated"))
	if len(allocated) != 2 {
		t.Fatalf("recursive dynamic allocation events=%#v", allocated)
	}
	children := make(map[int]string, 2)
	for _, event := range allocated {
		value, present := event.Param("value")
		child, ok := value.(gorapide.RapideModuleValue)
		if !present || !ok || child.Identity() == "" {
			t.Fatalf("recursive dynamic allocation value=%#v", value)
		}
		children[event.ParamInt("depth")] = child.Identity()
	}
	firstChild, secondChild := children[1], children[2]
	if firstChild == "" || secondChild == "" || firstChild == secondChild ||
		allocated[0].Source == allocated[1].Source {
		t.Fatalf("recursive dynamic child identities/sources=%#v/%#v", children, allocated)
	}
	done := sourceNamedEvents(result.Poset, secondChild, "Done")
	if len(done) != 1 || done[0].ParamInt("depth") != 2 {
		t.Fatalf("recursive dynamic terminal process=%#v", done)
	}
	processes := make(map[string]*arch.ProcessExecutionRecord)
	for index := range result.Processes {
		process := &result.Processes[index]
		if process.ComponentID == firstChild || process.ComponentID == secondChild {
			processes[process.ComponentID] = process
		}
	}
	for _, childID := range []string{firstChild, secondChild} {
		process := processes[childID]
		if process == nil || !process.Terminated || process.Completion != "normal" {
			t.Fatalf("recursive dynamic process %s=%#v", childID, process)
		}
	}
	if len(sourceNamedEvents(result.Poset, firstChild, "Closing")) != 1 ||
		len(sourceNamedEvents(result.Poset, secondChild, "Closing")) != 1 {
		t.Fatal("recursive dynamic children did not finalize normally")
	}
}

func TestSourceAllocatorNewKeepsDynamicModuleRunningUntilItsProcessCompletes(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Factory is interface
  action in Trigger(); action in Never();
  action out Allocated(value : Factory); action out Closing();
end interface Factory;
module FactoryModule() return Factory is
  ChildMode : var Boolean := False;
initial (Child : Boolean is False)
  ChildMode := Child;
serial
  await Trigger where not $ChildMode =>
    Allocated(New(True));
  or Never where $ChildMode =>
    null;
  end await;
final Closing();
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
	journal := arch.NewExecutionJournal(digest, 40,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	allocated := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Allocated"))
	if len(allocated) != 1 {
		t.Fatalf("live dynamic allocation events=%#v", allocated)
	}
	value, present := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !present || !ok || child.Identity() == "" {
		t.Fatalf("live dynamic allocation value=%#v", value)
	}
	childID := child.Identity()
	var lifecycle *arch.ModuleLifecycleRecord
	for index := range result.Modules {
		if result.Modules[index].ModuleID == childID {
			lifecycle = &result.Modules[index]
			break
		}
	}
	if lifecycle == nil || lifecycle.State != arch.ModuleRunningState || lifecycle.Namable ||
		lifecycle.FinishEventID != "" || lifecycle.TerminationEventID != "" {
		t.Fatalf("live dynamic child lifecycle=%#v", lifecycle)
	}
	if closing := sourceNamedEvents(result.Poset, childID, "Closing"); len(closing) != 0 {
		t.Fatalf("live dynamic child finalized early: %#v", closing)
	}
	var childProcess *arch.ProcessExecutionRecord
	for index := range result.Processes {
		if result.Processes[index].ComponentID == childID {
			childProcess = &result.Processes[index]
			break
		}
	}
	if childProcess == nil || childProcess.Terminated || childProcess.State != "await" ||
		childProcess.Completion != "" {
		t.Fatalf("live dynamic child process audit=%#v", childProcess)
	}
	artifact, _ := result.MarshalCanonical()
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("live dynamic-process replay changed canonical bytes")
	}
}

func TestSourceAllocatorNewDoesNotElaborateDynamicProcessesAfterFailedInitialization(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Factory is interface
  action in Trigger();
  action out Before(value : Integer); action out Initialized(value : Integer);
  action out Allocated(value : Factory); action out ProcessRan(); action out Closing();
end interface Factory;
module FactoryModule() return Factory is
  exception Failure(code : Integer);
initial (Seed : Integer is 1)
  Before(Seed);
  if Seed > 1 then raise Failure(code is Seed); end if;
  Initialized(Seed);
serial when Trigger do
  Allocated(New(4));
end when;
final Closing();
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
	journal := arch.NewExecutionJournal(digest, 80,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	var child *arch.ModuleLifecycleRecord
	for index := range result.Modules {
		candidate := &result.Modules[index]
		if candidate.Kind == "allocator-module" {
			child = candidate
			break
		}
	}
	if child == nil {
		t.Fatal("failed active allocator child lifecycle is absent")
	}
	before := sourceNamedEvents(result.Poset, child.ModuleID, "Before")
	failure := sourceNamedEvents(result.Poset, child.ModuleID, "Failure")
	if len(before) != 1 || before[0].ParamInt("value") != 4 ||
		len(failure) != 1 || failure[0].ParamInt("code") != 4 {
		t.Fatalf("failed active child Before/Failure=%#v/%#v", before, failure)
	}
	if child.State != arch.ModuleFinalizedState || child.Namable ||
		child.TerminationEventID != string(failure[0].ID) || child.FinishEventID == "" {
		t.Fatalf("failed active child lifecycle=%#v", child)
	}
	if len(sourceNamedEvents(result.Poset, "factory", "Allocated")) != 0 ||
		len(sourceNamedEvents(result.Poset, child.ModuleID, "Initialized")) != 0 ||
		len(sourceNamedEvents(result.Poset, child.ModuleID, "Closing")) != 0 ||
		len(sourceNamedEvents(result.Poset, child.ModuleID, "ProcessRan")) != 0 {
		t.Fatal("failed active child returned, finalized normally, or ran a process")
	}
	for _, process := range result.Processes {
		if process.ComponentID == child.ModuleID {
			t.Fatalf("failed active child process was elaborated: %#v", process)
		}
	}
	finish, exists := result.Poset.Get(gorapide.EventID(child.FinishEventID))
	if !exists {
		t.Fatalf("failed active child Finish %q is absent", child.FinishEventID)
	}
	assertOnlyDirectCause(t, result.Poset, failure[0], before[0])
	assertOnlyDirectCause(t, result.Poset, finish, failure[0])
}

func TestSourceAllocatorNewFinalizesDynamicModuleAfterProcessExceptionPropagation(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Factory is interface
  action in Trigger(); action out Ready(); action out Allocated(value : Factory);
  action out BeforeFailure(); action out Closing();
end interface Factory;
module FactoryModule() return Factory is
  exception Failure;
  ChildMode : var Boolean := False;
initial (Child : Boolean is False)
  ChildMode := Child;
  if Child then Ready(); end if;
serial
  await Trigger where not $ChildMode =>
    Allocated(New(True));
  or Ready where $ChildMode =>
    BeforeFailure();
    raise Failure;
  end await;
final Closing();
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 100,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	allocated := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Allocated"))
	if len(allocated) != 1 {
		t.Fatalf("exceptional dynamic allocation events=%#v", allocated)
	}
	value, present := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !present || !ok || child.Identity() == "" {
		t.Fatalf("exceptional dynamic allocation value=%#v", value)
	}
	childID := child.Identity()
	before := sourceNamedEvents(result.Poset, childID, "BeforeFailure")
	failure := sourceNamedEvents(result.Poset, childID, "Failure")
	closing := sourceNamedEvents(result.Poset, childID, "Closing")
	if len(before) != 1 || len(failure) != 1 || len(closing) != 1 {
		t.Fatalf("exceptional dynamic Before/Failure/Closing=%d/%d/%d",
			len(before), len(failure), len(closing))
	}
	var lifecycle *arch.ModuleLifecycleRecord
	for index := range result.Modules {
		if result.Modules[index].ModuleID == childID {
			lifecycle = &result.Modules[index]
			break
		}
	}
	if lifecycle == nil || lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
		lifecycle.TerminationEventID != string(failure[0].ID) || lifecycle.FinishEventID == "" {
		t.Fatalf("exceptional dynamic child lifecycle=%#v", lifecycle)
	}
	var childProcess *arch.ProcessExecutionRecord
	for index := range result.Processes {
		if result.Processes[index].ComponentID == childID {
			childProcess = &result.Processes[index]
			break
		}
	}
	if childProcess == nil || !childProcess.Terminated || childProcess.Completion != "exception" ||
		childProcess.ExceptionEventID != string(failure[0].ID) {
		t.Fatalf("exceptional dynamic child process=%#v", childProcess)
	}
	if !result.Poset.IsCausallyIndependent(allocated[0].ID, failure[0].ID) {
		t.Fatal("dynamic process exception and allocator-result loss acquired a false order")
	}
	causes := result.Poset.DirectCauses(closing[0].ID)
	causeIDs := make(map[gorapide.EventID]bool, len(causes))
	for _, cause := range causes {
		causeIDs[cause.ID] = true
	}
	if len(causes) != 2 || !causeIDs[allocated[0].ID] || !causeIDs[failure[0].ID] {
		t.Fatalf("exceptional dynamic finalization conjunction=%#v", causes)
	}
	finish, exists := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if !exists {
		t.Fatalf("exceptional dynamic child Finish %q is absent", lifecycle.FinishEventID)
	}
	assertOnlyDirectCause(t, result.Poset, finish, closing[0])
	propagation := exceptionPropagationBySource(t, result, childID)
	if propagation.ExceptionEventID != string(failure[0].ID) || len(propagation.Targets) != 1 ||
		propagation.Targets[0].Disposition != "delivered" {
		t.Fatalf("dynamic process exception propagation=%#v", propagation)
	}
}

func TestSourceAllocatorNewCreatesFreshPassiveModulesAndFinalizes(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  provides Spawn : function();
end interface Factory;

type Driver is interface
  action in Trigger();
  requires Spawn : function();
end interface Driver;

type Stimulus is interface
  action out Trigger();
end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is
  begin
    Allocated(New());
  end function Spawn;
end module FactoryModule;

module DriverModule() return Driver is
serial
  when Trigger() do
    Spawn();
  end when;
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
		arch.InputEvent{Key: "first", Source: "stimulus", Action: "Trigger"},
		arch.InputEvent{Key: "second", Source: "stimulus", Action: "Trigger", Causes: []string{"first"}},
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

	allocatedEvents := sourceNamedEvents(result.Poset, "factory", "Allocated")
	callEvents := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Call"))
	returnEvents := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocatedEvents) != 2 || len(callEvents) != 2 || len(returnEvents) != 2 {
		t.Fatalf("allocator Allocated/Call/Return counts=%d/%d/%d",
			len(allocatedEvents), len(callEvents), len(returnEvents))
	}
	var factory arch.ModuleLifecycleRecord
	allocatedModules := make(map[string]arch.ModuleLifecycleRecord)
	for _, lifecycle := range result.Modules {
		for _, name := range lifecycle.Names {
			if name.NameID == "component-name:factory" {
				factory = lifecycle
			}
		}
		if lifecycle.Kind == "allocator-module" {
			allocatedModules[lifecycle.ModuleID] = lifecycle
		}
	}
	if factory.ModuleID == "" || len(allocatedModules) != 2 {
		t.Fatalf("factory/allocator lifecycle=%#v/%#v", factory, allocatedModules)
	}
	seenValues := make(map[string]bool)
	for _, event := range allocatedEvents {
		value, exists := event.Param("value")
		module, ok := value.(gorapide.RapideModuleValue)
		if !exists || !ok || module.Identity() == "" {
			t.Fatalf("Allocated value=%#v, want module allocation", value)
		}
		if seenValues[module.Identity()] {
			t.Fatalf("repeated New returned allocation %q", module.Identity())
		}
		seenValues[module.Identity()] = true
		lifecycle, exists := allocatedModules[module.Identity()]
		if !exists || lifecycle.Parent != factory.ModuleID ||
			lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
			lifecycle.StartEventID == "" || lifecycle.FinishEventID == "" {
			t.Fatalf("allocated module lifecycle=%#v", lifecycle)
		}
		start, exists := result.Poset.Get(gorapide.EventID(lifecycle.StartEventID))
		if !exists {
			t.Fatalf("allocator Start %q is absent", lifecycle.StartEventID)
		}
		finish, exists := result.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
		if !exists {
			t.Fatalf("allocator Finish %q is absent", lifecycle.FinishEventID)
		}
		assertOnlyDirectCause(t, result.Poset, event, start)
		assertOnlyDirectCause(t, result.Poset, finish, event)
		var call, returned *gorapide.Event
		for _, candidate := range callEvents {
			if direct := result.Poset.DirectCauses(start.ID); len(direct) == 1 && direct[0].ID == candidate.ID {
				call = candidate
				break
			}
		}
		for _, candidate := range returnEvents {
			if direct := result.Poset.DirectCauses(candidate.ID); len(direct) == 1 && direct[0].ID == event.ID {
				returned = candidate
				break
			}
		}
		if call == nil || returned == nil || !result.Poset.IsCausallyIndependent(finish.ID, returned.ID) {
			t.Fatalf("allocator call/start/action/return/finalization relation call=%#v return=%#v finish=%s",
				call, returned, finish.ID)
		}
		if !result.Poset.IsCausallyBefore(start.ID, event.ID) ||
			!result.Poset.IsCausallyBefore(event.ID, finish.ID) {
			t.Fatal("allocator Start/value/Finish causality is incomplete")
		}
		var initialContext *arch.CommunicationContextRecord
		for index := range result.Contexts {
			candidate := &result.Contexts[index]
			if candidate.Kind == "initial-parent" && candidate.Source == module.Identity() {
				initialContext = candidate
				break
			}
		}
		if initialContext == nil || initialContext.Live || initialContext.Destination != factory.ModuleID ||
			len(initialContext.AcquiredAfter) != 1 || initialContext.AcquiredAfter[0] != lifecycle.StartEventID ||
			len(initialContext.LostAfter) != 1 || initialContext.LostAfter[0] != string(event.ID) {
			t.Fatalf("allocator initial communication Context=%#v", initialContext)
		}
		selfNames, resultNames := 0, 0
		for _, name := range lifecycle.Names {
			switch name.Kind {
			case "implicit-self":
				selfNames++
				if !name.Live || name.Owner != lifecycle.ModuleID {
					t.Fatalf("allocator Self edge=%#v", name)
				}
			case "allocator-result":
				resultNames++
				if name.Live || name.Owner != factory.ModuleID ||
					len(name.AcquiredAfter) != 1 || name.AcquiredAfter[0] != lifecycle.StartEventID ||
					len(name.LostAfter) != 1 || name.LostAfter[0] != string(event.ID) {
					t.Fatalf("allocator result edge=%#v", name)
				}
			}
		}
		if selfNames != 1 || resultNames != 1 {
			t.Fatalf("allocator name kinds Self/result=%d/%d", selfNames, resultNames)
		}
	}

	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	repeatedArtifact, err := repeated.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed allocator identities or lifecycle")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("allocator New replay changed canonical artifact bytes")
	}
}

func TestSourceParameterizedAllocatorReusesExactCompiledSpecialization(t *testing.T) {
	compile := func(application, localActuals, directActuals string) *arch.Architecture {
		source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  private action Input(step : Integer);
  action out Output(step : Integer);
  action out FinalValue(value : Integer);
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule(Seed : Integer is 1; Offset : Integer is 10; Enabled : Boolean is True) return Factory is
  Spawn : function() is
    Child : Factory is New(` + localActuals + `);
  begin
    Allocated(Child);
    Allocated(New(` + directActuals + `));
  end function Spawn;
connect
  if Enabled generate
    Input(Seed) ||> Output(Seed + Offset);
  end generate;
final
  Input(Seed);
  FinalValue(Offset);
end module FactoryModule;
module DriverModule() return Driver is serial when Trigger() do Spawn(); end when; end module DriverModule;
architecture System() is
  stimulus : Stimulus;
  factory : Factory is FactoryModule(` + application + `);
  driver : Driver is DriverModule();
connect stimulus.Trigger => driver.Trigger; driver.Spawn to factory.Spawn;
end architecture System;
`)
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		return model
	}
	defaulted := compile("", "", "Seed, Offset")
	explicit := compile("1, 10, True", "Seed, Offset, Enabled", "1, 10, True")
	named := compile(
		"Enabled is True, Offset is 10, Seed is 1",
		"Enabled is Enabled, Seed is Seed, Offset is Offset",
		"Offset is 10, Enabled is True, Seed is 1",
	)
	defaultedDigest, err := defaulted.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if defaultedDigest != explicitDigest {
		t.Fatalf("defaulted/explicit allocator specialization digests=%q/%q", defaultedDigest, explicitDigest)
	}
	namedDigest, err := named.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if namedDigest != defaultedDigest {
		t.Fatalf("named allocator association digest=%q, want %q", namedDigest, defaultedDigest)
	}
	journal := arch.NewExecutionJournal(defaultedDigest, 200,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	prior := runtime.GOMAXPROCS(1)
	defaultedResult, err := defaulted.ExecuteDeterministic(journal)
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
	namedResult, err := named.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	defaultedArtifact, err := defaultedResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	explicitArtifact, err := explicitResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(defaultedArtifact, explicitArtifact) {
		t.Fatal("defaulted/explicit allocator actuals or GOMAXPROCS changed canonical artifact bytes")
	}
	namedArtifact, err := namedResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(defaultedArtifact, namedArtifact) {
		t.Fatal("named allocator association order changed canonical artifact bytes")
	}

	allocated := sourceNamedEvents(defaultedResult.Poset, "factory", "Allocated")
	if len(allocated) != 2 {
		t.Fatalf("Allocated count=%d, want 2", len(allocated))
	}
	seen := make(map[string]bool, len(allocated))
	for _, allocation := range allocated {
		value, _ := allocation.Param("value")
		module, ok := value.(gorapide.RapideModuleValue)
		if !ok || module.Identity() == "" || seen[module.Identity()] {
			t.Fatalf("parameterized allocator value=%#v seen=%v", value, seen)
		}
		seen[module.Identity()] = true
		inputs := sourceNamedEvents(defaultedResult.Poset, module.Identity(), "Input")
		outputs := sourceNamedEvents(defaultedResult.Poset, module.Identity(), "Output")
		finalValues := sourceNamedEvents(defaultedResult.Poset, module.Identity(), "FinalValue")
		if len(inputs) != 1 || len(outputs) != 1 || len(finalValues) != 1 ||
			inputs[0].ParamInt("step") != 1 || outputs[0].ParamInt("step") != 11 ||
			finalValues[0].ParamInt("value") != 10 {
			t.Fatalf("child %s specialized Input/Output/FinalValue=%#v/%#v/%#v",
				module.Identity(), inputs, outputs, finalValues)
		}
		assertOnlyDirectCause(t, defaultedResult.Poset, outputs[0], inputs[0])
		assertOnlyDirectCause(t, defaultedResult.Poset, finalValues[0], inputs[0])
		var lifecycle arch.ModuleLifecycleRecord
		for _, candidate := range defaultedResult.Modules {
			if candidate.ModuleID == module.Identity() {
				lifecycle = candidate
				break
			}
		}
		finish, exists := defaultedResult.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
		if lifecycle.State != arch.ModuleFinalizedState || !exists {
			t.Fatalf("child %s lifecycle/Finish=%#v/%v", module.Identity(), lifecycle, exists)
		}
		assertOnlyDirectCause(t, defaultedResult.Poset, finish, finalValues[0])
		if !defaultedResult.Poset.IsCausallyIndependent(outputs[0].ID, finish.ID) {
			t.Fatalf("child %s terminal Output was falsely ordered with Finish", module.Identity())
		}
	}
	if len(seen) != 2 {
		t.Fatalf("fresh parameterized allocations=%d, want 2", len(seen))
	}
	afterDigest, err := defaulted.DeterministicModelDigest()
	if err != nil || afterDigest != defaultedDigest {
		t.Fatalf("parameterized allocation mutated model digest before=%q after=%q err=%v",
			defaultedDigest, afterDigest, err)
	}
	expected, err := defaultedResult.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := defaulted.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(defaultedArtifact, replayedArtifact) {
		t.Fatal("parameterized allocator replay changed canonical artifact bytes")
	}
	explorationJournal := journal
	explorationJournal.Choices = make([]arch.ChoiceDecision, len(defaultedResult.Choices))
	for index, choice := range defaultedResult.Choices {
		explorationJournal.Choices[index] = choice.Decision()
	}
	explored, err := defaulted.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := defaulted.ExploreDeterministic(
		explorationJournal, arch.ExplorationLimits{MaxExecutions: 2, MaxChoiceDepth: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || len(explored.Computations) != 1 ||
		!bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("parameterized allocator fixed exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourceFunctionLocalModuleNewRejectsUnsupportedDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		local string
		want  string
	}{
		{name: "scalar local", local: `Count : Integer is 1;`, want: "requires the enclosing interface type Factory"},
		{name: "nonallocator initializer", local: `Child : Factory is Self;`, want: "requires a direct owner allocator New initializer"},
		{name: "allocator arguments", local: `Child : Factory is New(1);`, want: "allocator New supplies 1 generator actuals, but 0 are declared"},
		{name: "parameter conflict", local: `Seed : Factory is New();`, want: "conflicts with an enclosing declaration"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  provides Spawn : function(Seed : Integer);
end interface Factory;
module FactoryModule() return Factory is
  Spawn : function(Seed : Integer) is
    ` + test.local + `
  begin
    null;
  end function Spawn;
end module FactoryModule;
architecture System() is factory : Factory is FactoryModule(); end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile()=%v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestSourceFunctionLocalModulesRetainNamesUntilScopeExit(t *testing.T) {
	source := []byte(`
type Factory is interface
  action out Allocated(value : Factory);
  provides Spawn : function();
end interface Factory;
type Driver is interface action in Trigger(); requires Spawn : function(); end interface Driver;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module FactoryModule() return Factory is
  Spawn : function() is
    First, Second : Factory is New();
  begin
    Allocated(First);
    Allocated(First);
    Allocated(Second);
  end function Spawn;
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
	splitSource := bytes.Replace(source,
		[]byte(`First, Second : Factory is New();`),
		[]byte("First : Factory is New();\n    Second : Factory is New();"), 1,
	)
	splitModel, err := Compile(splitSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	splitDigest, err := splitModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != splitDigest {
		t.Fatalf("grouped/split function-local declarations changed model identity: %s != %s", digest, splitDigest)
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
	sort.Slice(allocated, func(left, right int) bool {
		return result.Poset.IsCausallyBefore(allocated[left].ID, allocated[right].ID)
	})
	calls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Call"))
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "factory", "Spawn'Return"))
	if len(allocated) != 3 || len(calls) != 1 || len(returns) != 1 {
		t.Fatalf("Allocated/Call/Return counts=%d/%d/%d", len(allocated), len(calls), len(returns))
	}
	values := make([]gorapide.RapideModuleValue, len(allocated))
	for index, event := range allocated {
		value, exists := event.Param("value")
		module, ok := value.(gorapide.RapideModuleValue)
		if !exists || !ok {
			t.Fatalf("Allocated[%d] value=%#v", index, value)
		}
		values[index] = module
	}
	if values[0].Identity() == "" || values[0].Identity() != values[1].Identity() ||
		values[0].Identity() == values[2].Identity() {
		t.Fatalf("function-local module reuse/freshness=%#v", values)
	}

	var factory arch.ModuleLifecycleRecord
	byID := make(map[string]arch.ModuleLifecycleRecord)
	for _, lifecycle := range result.Modules {
		for _, name := range lifecycle.Names {
			if name.NameID == "component-name:factory" {
				factory = lifecycle
			}
		}
		if lifecycle.Kind == "allocator-module" {
			byID[lifecycle.ModuleID] = lifecycle
		}
	}
	if factory.ModuleID == "" || len(byID) != 2 {
		t.Fatalf("factory/locals=%#v/%#v", factory, byID)
	}
	first := byID[values[0].Identity()]
	second := byID[values[2].Identity()]
	firstStart, _ := result.Poset.Get(gorapide.EventID(first.StartEventID))
	secondStart, _ := result.Poset.Get(gorapide.EventID(second.StartEventID))
	firstFinish, _ := result.Poset.Get(gorapide.EventID(first.FinishEventID))
	secondFinish, _ := result.Poset.Get(gorapide.EventID(second.FinishEventID))
	if firstStart == nil || secondStart == nil || firstFinish == nil || secondFinish == nil ||
		first.Parent != factory.ModuleID || second.Parent != factory.ModuleID ||
		first.State != arch.ModuleFinalizedState || second.State != arch.ModuleFinalizedState ||
		first.Namable || second.Namable {
		t.Fatalf("function-local lifecycles first=%#v second=%#v", first, second)
	}
	assertOnlyDirectCause(t, result.Poset, firstStart, calls[0])
	assertOnlyDirectCause(t, result.Poset, secondStart, firstStart)
	assertOnlyDirectCause(t, result.Poset, allocated[0], secondStart)
	assertOnlyDirectCause(t, result.Poset, allocated[1], allocated[0])
	assertOnlyDirectCause(t, result.Poset, allocated[2], allocated[1])
	assertOnlyDirectCause(t, result.Poset, firstFinish, allocated[2])
	assertOnlyDirectCause(t, result.Poset, secondFinish, allocated[2])
	assertOnlyDirectCause(t, result.Poset, returns[0], allocated[2])
	if !result.Poset.IsCausallyIndependent(firstFinish.ID, secondFinish.ID) ||
		!result.Poset.IsCausallyIndependent(firstFinish.ID, returns[0].ID) ||
		!result.Poset.IsCausallyIndependent(secondFinish.ID, returns[0].ID) {
		t.Fatal("function-local finalizers or enclosing return were falsely ordered")
	}
	for _, lifecycle := range []arch.ModuleLifecycleRecord{first, second} {
		kinds := make(map[string]arch.ModuleNameRecord)
		for _, name := range lifecycle.Names {
			kinds[name.Kind] = name
		}
		resultName, localName := kinds["allocator-result"], kinds["function-local"]
		if resultName.Live || len(resultName.LostAfter) != 1 || resultName.LostAfter[0] != lifecycle.StartEventID ||
			localName.Live || localName.Owner != factory.ModuleID || len(localName.LostAfter) != 1 ||
			localName.LostAfter[0] != string(allocated[2].ID) {
			t.Fatalf("function-local name edges=%#v", lifecycle.Names)
		}
	}

	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed function-local allocation or lifecycle")
	}
	splitResult, err := splitModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	splitArtifact, _ := splitResult.MarshalCanonical()
	if !bytes.Equal(artifact, splitArtifact) {
		t.Fatal("grouped/split function-local declarations changed canonical execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("function-local module allocation replay changed canonical bytes")
	}
}
