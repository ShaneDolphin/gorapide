package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func sourceProcessFiring(t *testing.T, result *arch.ExecutionResult, componentID string) *arch.FiringRecord {
	t.Helper()
	for index := range result.Firings {
		if result.Firings[index].Transition == "process" && result.Firings[index].Target == componentID {
			return &result.Firings[index]
		}
	}
	t.Fatalf("process firing for %q is absent", componentID)
	return nil
}

func TestSourceFunctionInSchedulesWithoutSuspendingCaller(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Scheduled();
  action out Returned();
  provides Schedule : function();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module WorkerModule() return Worker is
  C : Clock is Make_Clock();
  Two : C.Ticks is 2;
  Schedule : function() is begin Scheduled() in Two; end function Schedule;
serial
  when Trigger() do
    Schedule();
    Returned();
  end when;
end module WorkerModule;

architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect
  stimulus.Trigger => worker.Trigger;
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
		digest, arch.ExecutionLimits{MaxFirings: 60, MaxStatements: 80},
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

	calls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "worker", "Schedule'Call"))
	returns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "worker", "Schedule'Return"))
	returned := sourceNamedEvents(first.Poset, "worker", "Returned")
	scheduled := sourceNamedEvents(first.Poset, "worker", "Scheduled")
	if len(calls) != 1 || len(returns) != 1 || len(returned) != 1 || len(scheduled) != 1 {
		t.Fatalf("function Call/Return/Returned/Scheduled=%#v/%#v/%#v/%#v",
			calls, returns, returned, scheduled)
	}
	for _, pair := range [][2]int{{0, 1}, {1, 2}, {2, 3}} {
		sequence := []*gorapide.Event{calls[0], returns[0], returned[0], scheduled[0]}
		if !first.Poset.IsCausallyBefore(sequence[pair[0]].ID, sequence[pair[1]].ID) {
			t.Fatalf("function in sequence %s !< %s",
				sequence[pair[0]].ID, sequence[pair[1]].ID)
		}
	}
	clockID := arch.ClockID("worker", "C")
	timing, related := scheduled[0].Timing(clockID)
	if !related || timing.Start != 2 || timing.Finish != 2 {
		t.Fatalf("function scheduled timing=%#v related=%t", timing, related)
	}
	planFound := false
	for _, firing := range first.Firings {
		for _, plan := range firing.Scheduled {
			if plan.Clock == clockID && plan.Tick == "2" {
				planFound = true
			}
		}
	}
	if !planFound || len(first.ScheduledEvents) != 1 ||
		first.ScheduledEvents[0].EventID != string(scheduled[0].ID) {
		t.Fatalf("function scheduled audit plans=%t events=%#v", planFound, first.ScheduledEvents)
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
		t.Fatal("GOMAXPROCS changed function in artifact")
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
		t.Fatal("function in replay changed canonical bytes")
	}
}

func TestSourceProcessFunctionDelayResumesExactlyOnce(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger(n : Integer);
  action out Before(n : Integer);
  action out After(n : Integer);
  action out Returned(n : Integer);
  provides Work : function(n : Integer) return Integer;
end interface Worker;
type Stimulus is interface action out Trigger(n : Integer); end interface Stimulus;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
  Two : C.Ticks is 2;
  result : var Integer := 0;
  Work : function(n : Integer) return Integer is
  begin
    Before(n);
    delay Two;
    After(n);
    return n + 1;
  end function Work;
serial
  when (?N : Integer) Trigger(?N) do
    result := Work(?N);
    Returned($result);
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 40, MaxStatements: 80},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger", Params: map[string]any{"n": 4}},
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

	sequence := [][]*gorapide.Event{
		distinctAllocatorEvents(sourceNamedEvents(first.Poset, "worker", "Trigger")),
		distinctAllocatorEvents(sourceNamedEvents(first.Poset, "worker", "Work'Call")),
		sourceNamedEvents(first.Poset, "worker", "Before"),
		sourceNamedEvents(first.Poset, "worker", "After"),
		distinctAllocatorEvents(sourceNamedEvents(first.Poset, "worker", "Work'Return")),
		sourceNamedEvents(first.Poset, "worker", "Returned"),
	}
	for index, events := range sequence {
		if len(events) != 1 {
			t.Fatalf("sequence event %d count=%d, want one", index, len(events))
		}
		if index != 0 && !first.Poset.IsCausallyBefore(sequence[index-1][0].ID, events[0].ID) {
			t.Fatalf("sequence event %d is not after event %d", index, index-1)
		}
	}
	clockID := arch.ClockID("worker", "C")
	for _, event := range []*gorapide.Event{sequence[3][0], sequence[4][0], sequence[5][0]} {
		timing, related := event.Timing(clockID)
		if !related || timing.Start != 2 || timing.Finish != 2 {
			t.Fatalf("%s timing=%#v related=%t, want tick 2", event.Name, timing, related)
		}
	}
	var processFiring *arch.FiringRecord
	for index := range first.Firings {
		if first.Firings[index].Transition == "process" {
			processFiring = &first.Firings[index]
			break
		}
	}
	if processFiring == nil || len(processFiring.Suspensions) != 1 ||
		len(processFiring.Switches) != 1 {
		t.Fatalf("function continuation audit=%#v", first.Firings)
	}
	suspension := processFiring.Suspensions[0]
	if suspension.Kind != arch.DelayTimingClause || suspension.Start != "0" ||
		suspension.Finish != "2" || !strings.Contains(suspension.Statement, "/body/1") {
		t.Fatalf("function suspension=%#v", suspension)
	}
	switchRecord := processFiring.Switches[0]
	if switchRecord.Kind != "function-completion" ||
		switchRecord.CallEventID != string(sequence[1][0].ID) ||
		switchRecord.ReturnEventID != string(sequence[4][0].ID) {
		t.Fatalf("function switch=%#v", switchRecord)
	}
	if len(first.State) != 1 || first.State[0].Value.Text != "5" || first.State[0].Version != 1 {
		t.Fatalf("function result assignment=%#v", first.State)
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
		t.Fatal("GOMAXPROCS changed resumable function artifact")
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
		t.Fatal("resumable function replay changed canonical bytes")
	}
	var completionChoice *arch.ChoiceResolution
	for index := range first.Choices {
		if first.Choices[index].Domain == "semantic-step" {
			completionChoice = &first.Choices[index]
			break
		}
	}
	if completionChoice == nil || len(completionChoice.Options) != 2 {
		t.Fatalf("function completion lacks scheduler choice: %#v", first.Choices)
	}
	resumeJournal := journal
	resumeJournal.Choices = []arch.ChoiceDecision{{
		Point: completionChoice.Point, Selection: "resume",
	}}
	resumed, err := model.ExecuteDeterministic(resumeJournal)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.Choices) == 0 || !resumed.Choices[0].Scheduled ||
		resumed.Choices[0].Selected != "resume" {
		t.Fatalf("scheduled function completion resume=%#v", resumed.Choices)
	}
	firstPoset, _ := first.Poset.SemanticDigest()
	resumedPoset, _ := resumed.Poset.SemanticDigest()
	if firstPoset != resumedPoset {
		t.Fatalf("function completion scheduling changed causal computation: %s != %s", firstPoset, resumedPoset)
	}
	resumeDigest, err := resumed.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.ReplayDeterministic(resumeJournal, resumeDigest); err != nil {
		t.Fatal(err)
	}
	explored, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 8, MaxChoiceDepth: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || explored.Executions < 3 || len(explored.Computations) != 1 {
		t.Fatalf("function completion exploration=%#v", explored)
	}
}

func TestSourceNestedRemoteFunctionSuspensionsRetainTargetClocksAndCallerState(t *testing.T) {
	source := []byte(`
type Driver is interface action out Start(n : Integer); end interface Driver;
type Client is interface
  action in Start(n : Integer);
  action out Done(n : Integer);
  provides Compute : function(n : Integer) return Integer;
  requires Lookup : function(value : Integer) return Integer;
end interface Client;
type Server is interface
  action out Seen(value : Integer);
  provides Fetch : function(operand : Integer) return Integer;
end interface Server;

module ClientModule() return Client is
  C : Clock is Make_Clock();
  Two : C.Ticks is 2;
  scratch : var Integer := 0;
  result : var Integer := 0;
  Compute : function(n : Integer) return Integer is
  begin
    scratch := Lookup(n);
    pause Two;
    return $scratch + 1;
  end function Compute;
serial
  when (?N : Integer) Start(?N) do
    result := Compute(?N);
    Done($result);
  end when;
end module ClientModule;

module ServerModule() return Server is
  S : Clock is Make_Clock();
  One : S.Ticks is 1;
  Fetch : function(operand : Integer) return Integer is
  begin
    Seen(operand) pause One;
    return operand * 2;
  end function Fetch;
end module ServerModule;

architecture System() is
  driver : Driver;
  client : Client is ClientModule();
  server : Server is ServerModule();
connect
  driver.Start to client.Start;
  client.Lookup to server.Fetch;
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
		digest, arch.ExecutionLimits{MaxFirings: 60, MaxStatements: 120},
		arch.InputEvent{Key: "start", Source: "driver", Action: "Start", Params: map[string]any{"n": 3}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	computeCall := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "client", "Compute'Call"))
	lookupCall := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "client", "Lookup'Call"))
	fetchCall := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "server", "Fetch'Call"))
	seen := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "server", "Seen"))
	fetchReturn := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "server", "Fetch'Return"))
	lookupReturn := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "client", "Lookup'Return"))
	computeReturn := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "client", "Compute'Return"))
	done := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "client", "Done"))
	for name, events := range map[string][]*gorapide.Event{
		"Compute'Call": computeCall, "Lookup'Call": lookupCall, "Fetch'Call": fetchCall,
		"Seen": seen, "Fetch'Return": fetchReturn, "Lookup'Return": lookupReturn,
		"Compute'Return": computeReturn, "Done": done,
	} {
		if len(events) != 1 {
			t.Fatalf("%s count=%d, want one", name, len(events))
		}
	}
	if lookupCall[0].ID != fetchCall[0].ID || lookupReturn[0].ID != fetchReturn[0].ID {
		t.Fatal("remote function route duplicated Call or Return occurrences")
	}
	ordered := []*gorapide.Event{
		computeCall[0], lookupCall[0], seen[0], lookupReturn[0], computeReturn[0], done[0],
	}
	for index := 1; index < len(ordered); index++ {
		if !result.Poset.IsCausallyBefore(ordered[index-1].ID, ordered[index].ID) {
			t.Fatalf("nested remote event %s is not after %s", ordered[index].Name, ordered[index-1].Name)
		}
	}
	serverClock := arch.ClockID("server", "S")
	seenTiming, related := seen[0].Timing(serverClock)
	if !related || seenTiming.Start != 0 || seenTiming.Finish != 1 {
		t.Fatalf("remote pause action timing=%#v related=%t", seenTiming, related)
	}
	clientClock := arch.ClockID("client", "C")
	computeTiming, related := computeReturn[0].Timing(clientClock)
	if !related || computeTiming.Start != 2 || computeTiming.Finish != 2 {
		t.Fatalf("outer function return timing=%#v related=%t", computeTiming, related)
	}
	if value, _ := done[0].Param("n"); value != int64(7) {
		t.Fatalf("nested function result=%#v, want 7", value)
	}
	var processFiring *arch.FiringRecord
	for index := range result.Firings {
		if result.Firings[index].Transition == "process" {
			processFiring = &result.Firings[index]
			break
		}
	}
	if processFiring == nil || len(processFiring.Suspensions) != 2 || len(processFiring.Switches) != 2 {
		t.Fatalf("nested continuation audit=%#v", result.Firings)
	}
	if processFiring.Suspensions[0].Clock != serverClock ||
		processFiring.Suspensions[0].EventID != string(seen[0].ID) ||
		processFiring.Suspensions[1].Clock != clientClock ||
		processFiring.Suspensions[1].EventID != "" {
		t.Fatalf("nested suspension ownership=%#v", processFiring.Suspensions)
	}
	if len(result.State) != 2 {
		t.Fatalf("nested function state audit=%#v", result.State)
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("nested remote function replay changed canonical bytes")
	}
}

func TestSourceFunctionPlainDoRetainsNamedControlAcrossSuspensions(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Worker is interface
  action in Trigger();
  action out Before();
  action out InnerAfter();
  action out AfterDo();
  action out Returned(value : Integer);
  action out Wrong();
  provides Inner : function();
  provides Work : function() return Integer;
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
  One : C.Ticks is 1;
  result : var Integer := 0;
  Inner : function() is begin
    pause One;
    InnerAfter();
  end function Inner;
  Work : function() return Integer is begin
    Outer : do
      Before();
      delay One;
      do
        Inner();
        next Outer;
        Wrong();
      end do;
      Wrong();
    end do Outer;
    AfterDo();
    return 9;
  end function Work;
serial when Trigger do
  result := Work();
  Returned($result);
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 40, MaxStatements: 120},
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
	before := sourceNamedEvents(result.Poset, "worker", "Before")
	innerAfter := sourceNamedEvents(result.Poset, "worker", "InnerAfter")
	afterDo := sourceNamedEvents(result.Poset, "worker", "AfterDo")
	returned := sourceNamedEvents(result.Poset, "worker", "Returned")
	workCalls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Call"))
	workReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Return"))
	innerCalls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Inner'Call"))
	innerReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Inner'Return"))
	if len(before) != 1 || len(innerAfter) != 1 || len(afterDo) != 1 || len(returned) != 1 ||
		len(workCalls) != 1 || len(workReturns) != 1 || len(innerCalls) != 1 || len(innerReturns) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
		t.Fatalf("plain-do function events before/inner/after/returned/work/inner=%d/%d/%d/%d/%d:%d/%d:%d",
			len(before), len(innerAfter), len(afterDo), len(returned),
			len(workCalls), len(workReturns), len(innerCalls), len(innerReturns))
	}
	if value, _ := returned[0].Param("value"); value != int64(9) {
		t.Fatalf("plain-do function result=%#v", value)
	}
	for _, edge := range [][2]gorapide.EventID{
		{workCalls[0].ID, before[0].ID},
		{before[0].ID, innerCalls[0].ID},
		{innerCalls[0].ID, innerAfter[0].ID},
		{innerAfter[0].ID, innerReturns[0].ID},
		{innerReturns[0].ID, afterDo[0].ID},
		{afterDo[0].ID, workReturns[0].ID},
		{workReturns[0].ID, returned[0].ID},
	} {
		if !result.Poset.IsCausallyBefore(edge[0], edge[1]) {
			t.Fatalf("plain-do function missing causal edge %s < %s", edge[0], edge[1])
		}
	}
	firing := sourceProcessFiring(t, result, "worker")
	if len(firing.Suspensions) != 2 || len(firing.Switches) != 2 {
		t.Fatalf("plain-do function continuation audit=%#v", firing)
	}
	clockID := arch.ClockID("worker", "C")
	if len(result.Clocks) != 1 || result.Clocks[0].Clock != clockID || result.Clocks[0].Now != "2" {
		t.Fatalf("plain-do function clock audit=%#v", result.Clocks)
	}
	left, _ := result.MarshalCanonical()
	right, _ := repeated.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("GOMAXPROCS changed function plain-do continuation")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, replayedBytes) {
		t.Fatal("function plain-do replay changed canonical bytes")
	}
}

func TestSourceGeneralForControlFunctionsSuspendInEveryPhase(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Worker is interface
  action in Trigger();
  action out Emit(value : Integer);
  action out Done(value : Integer);
  provides Initialize : function() return Integer;
  provides More : function() return Boolean;
  provides Advance : function() return Integer;
  provides Work : function() return Integer;
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
  One : C.Ticks is 1;
  i : var Integer := 0;
  Initialize : function() return Integer is begin
    delay One;
    i := 1;
    return $i;
  end function Initialize;
  More : function() return Boolean is begin
    pause One;
    return $i <= 2;
  end function More;
  Advance : function() return Integer is begin
    delay One;
    i := $i + 1;
    return $i;
  end function Advance;
  Work : function() return Integer is begin
    for Initialize() in More() next Advance() do
      if $i = 1 then next; end if;
      Emit($i);
    end for;
    Done($i);
    return $i;
  end function Work;
serial when Trigger do Work(); end when;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 80, MaxStatements: 240},
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
	emitted := sourceNamedEvents(result.Poset, "worker", "Emit")
	done := sourceNamedEvents(result.Poset, "worker", "Done")
	if len(emitted) != 1 || len(done) != 1 {
		t.Fatalf("general-for suspended controls Emit/Done=%d/%d", len(emitted), len(done))
	}
	if value, _ := emitted[0].Param("value"); value != int64(2) {
		t.Fatalf("general-for Emit=%#v, want 2 after body next", value)
	}
	if value, _ := done[0].Param("value"); value != int64(3) {
		t.Fatalf("general-for Done=%#v, want 3", value)
	}
	for name, want := range map[string]int{
		"Initialize'Call": 1, "Initialize'Return": 1,
		"More'Call": 3, "More'Return": 3,
		"Advance'Call": 2, "Advance'Return": 2,
		"Work'Call": 1, "Work'Return": 1,
	} {
		if got := len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", name))); got != want {
			t.Fatalf("general-for %s count=%d, want %d", name, got, want)
		}
	}
	firing := sourceProcessFiring(t, result, "worker")
	if len(firing.Suspensions) != 6 || len(firing.Switches) != 7 {
		t.Fatalf("general-for continuation audit suspensions/switches=%d/%d",
			len(firing.Suspensions), len(firing.Switches))
	}
	clockID := arch.ClockID("worker", "C")
	if len(result.Clocks) != 1 || result.Clocks[0].Clock != clockID || result.Clocks[0].Now != "6" {
		t.Fatalf("general-for clock audit=%#v", result.Clocks)
	}
	if len(result.State) != 1 || result.State[0].Value.Text != "3" || result.State[0].Version != 3 {
		t.Fatalf("general-for state audit=%#v", result.State)
	}
	left, _ := result.MarshalCanonical()
	right, _ := repeated.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("GOMAXPROCS changed general-for suspended controls")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, replayedBytes) {
		t.Fatal("general-for suspended controls replay changed canonical bytes")
	}
	explored, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 512, MaxChoiceDepth: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 1 {
		t.Fatalf("general-for suspended controls exploration=%#v", explored)
	}
}

func TestSourceModuleTerminationCancelsSuspendedFunctionWithoutReturn(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Start(); action out Fail(); end interface Stimulus;
type Worker is interface
  action in Start();
  action in Fail();
  action out Wrong();
  provides Work : function();
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
  Two : C.Ticks is 2;
  exception Failure;
  Work : function() is begin delay Two; Wrong(); end function Work;
parallel
  when Start do Work(); Wrong(); end when;
||
  when Fail do raise Failure; end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect
  stimulus.Start to worker.Start;
  stimulus.Fail to worker.Fail;
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
	clockID := arch.ClockID("worker", "C")
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 60, MaxStatements: 80},
		arch.InputEvent{Key: "start", Source: "stimulus", Action: "Start"},
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail",
			Timings: []gorapide.EventTiming{{Clock: clockID, Start: 1, Finish: 1}}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	calls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Call"))
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Return"))
	wrong := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Wrong"))
	if len(calls) != 1 || len(returns) != 0 || len(wrong) != 0 {
		t.Fatalf("canceled function Call/Return/Wrong=%d/%d/%d", len(calls), len(returns), len(wrong))
	}
	var canceled *arch.ProcessExecutionRecord
	for index := range result.Processes {
		process := &result.Processes[index]
		if process.ComponentID == "worker" && process.Completion == "module-termination" {
			canceled = process
			break
		}
	}
	if canceled == nil || len(canceled.CanceledSuspensions) != 1 ||
		len(canceled.CanceledSwitches) != 0 {
		t.Fatalf("canceled suspended function process=%#v", result.Processes)
	}
	if len(result.ExceptionPropagations) == 0 {
		t.Fatal("unhandled sibling exception did not retain propagation evidence")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.ReplayDeterministic(journal, expected); err != nil {
		t.Fatal(err)
	}
}

func TestSourceFunctionFiniteIteratorSurvivesMultipleSuspensions(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Worker is interface
  action in Trigger();
  action out Done(value : Integer);
  provides Work : function() return Integer;
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
  One : C.Ticks is 1;
  total : var Integer := 0;
  result : var Integer := 0;
  Work : function() return Integer is
  begin
    for i : Integer in 1..2 do
      pause One;
      total := $total + i;
    end for;
    return $total;
  end function Work;
serial when Trigger do
  result := Work();
  Done($result);
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 60, MaxStatements: 120},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	calls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Call"))
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Return"))
	done := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Done"))
	if len(calls) != 1 || len(returns) != 1 || len(done) != 1 {
		t.Fatalf("iterator function Call/Return/Done=%d/%d/%d", len(calls), len(returns), len(done))
	}
	if value, _ := done[0].Param("value"); value != int64(3) {
		t.Fatalf("iterator function result=%#v, want 3", value)
	}
	clockID := arch.ClockID("worker", "C")
	timing, related := returns[0].Timing(clockID)
	if !related || timing.Start != 2 || timing.Finish != 2 {
		t.Fatalf("iterator function return timing=%#v related=%t", timing, related)
	}
	var processFiring *arch.FiringRecord
	for index := range result.Firings {
		if result.Firings[index].Transition == "process" {
			processFiring = &result.Firings[index]
			break
		}
	}
	if processFiring == nil || len(processFiring.Suspensions) != 2 ||
		len(processFiring.Switches) != 1 {
		t.Fatalf("iterator function continuation audit=%#v", result.Firings)
	}
	if processFiring.Suspensions[0].Finish != "1" ||
		processFiring.Suspensions[1].Start != "1" ||
		processFiring.Suspensions[1].Finish != "2" {
		t.Fatalf("iterator function suspension intervals=%#v", processFiring.Suspensions)
	}
	iteratorStarts := distinctAllocatorEvents(result.Poset.ByName("Start"))
	iteratorFinishes := distinctAllocatorEvents(result.Poset.ByName("Finish"))
	if len(iteratorStarts) < 1 || len(iteratorFinishes) < 1 {
		t.Fatalf("iterator lifecycle Start/Finish=%d/%d", len(iteratorStarts), len(iteratorFinishes))
	}
	if !result.Poset.IsCausallyBefore(calls[0].ID, returns[0].ID) ||
		!result.Poset.IsCausallyBefore(returns[0].ID, done[0].ID) {
		t.Fatal("iterator function lost caller causality")
	}
}

func TestSourceFunctionZeroPauseCompletesWithoutClockAdvance(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Worker is interface
  action in Trigger();
  action out Before();
  action out After();
  provides Work : function();
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
  Work : function() is begin Before(); pause C.Ticks(0); After(); end function Work;
serial when Trigger do Work(); end when;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 30, MaxStatements: 40},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ClockAdvances) != 0 {
		t.Fatalf("zero function pause advanced a clock: %#v", result.ClockAdvances)
	}
	var processFiring *arch.FiringRecord
	for index := range result.Firings {
		if result.Firings[index].Transition == "process" {
			processFiring = &result.Firings[index]
			break
		}
	}
	if processFiring == nil || len(processFiring.Suspensions) != 1 ||
		processFiring.Suspensions[0].Start != "0" ||
		processFiring.Suspensions[0].Finish != "0" ||
		len(processFiring.Switches) != 1 {
		t.Fatalf("zero function pause audit=%#v", result.Firings)
	}
	before := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Before"))
	after := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "After"))
	if len(before) != 1 || len(after) != 1 ||
		!result.Poset.IsCausallyBefore(before[0].ID, after[0].ID) {
		t.Fatalf("zero function pause Before/After=%#v/%#v", before, after)
	}
}

func TestSourceFunctionExceptionAfterSuspensionEmitsNoReturn(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Worker is interface
  action in Trigger();
  action out Wrong();
  provides Work : function();
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
  One : C.Ticks is 1;
  exception Failure;
  Work : function() is begin delay One; raise Failure; Wrong(); end function Work;
serial when Trigger do Work(); Wrong(); end when;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 40, MaxStatements: 50},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	calls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Call"))
	failures := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Failure"))
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Return"))
	wrong := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Wrong"))
	if len(calls) != 1 || len(failures) != 1 || len(returns) != 0 || len(wrong) != 0 {
		t.Fatalf("suspended raising function Call/Failure/Return/Wrong=%d/%d/%d/%d",
			len(calls), len(failures), len(returns), len(wrong))
	}
	if !result.Poset.IsCausallyBefore(calls[0].ID, failures[0].ID) {
		t.Fatal("suspended function exception lost Call causality")
	}
	var processFiring *arch.FiringRecord
	for index := range result.Firings {
		if result.Firings[index].Transition == "process" {
			processFiring = &result.Firings[index]
			break
		}
	}
	if processFiring == nil || len(processFiring.Suspensions) != 1 ||
		len(processFiring.Switches) != 0 {
		t.Fatalf("suspended function exception audit=%#v", result.Firings)
	}
	if len(result.ExceptionPropagations) == 0 {
		t.Fatal("suspended function exception did not retain propagation evidence")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.ReplayDeterministic(journal, expected); err != nil {
		t.Fatal(err)
	}
}

func TestSourceGeneralForSuspendedControlExceptionUnwindsEveryCaller(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Worker is interface
  action in Trigger();
  action out Wrong();
  provides More : function() return Boolean;
  provides Work : function();
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
  One : C.Ticks is 1;
  exception Failure;
  More : function() return Boolean is begin
    delay One;
    raise Failure;
    return True;
  end function More;
  Work : function() is begin
    for 0 in More() next 0 do Wrong(); end for;
    Wrong();
  end function Work;
serial when Trigger do Work(); Wrong(); end when;
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
	journal := arch.NewExecutionJournalWithLimits(
		digest, arch.ExecutionLimits{MaxFirings: 40, MaxStatements: 80},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	workCalls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Call"))
	moreCalls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "More'Call"))
	failures := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Failure"))
	if len(workCalls) != 1 || len(moreCalls) != 1 || len(failures) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Work'Return")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "More'Return")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
		t.Fatalf("general-for exception Work/More/Failure=%d/%d/%d",
			len(workCalls), len(moreCalls), len(failures))
	}
	if !result.Poset.IsCausallyBefore(workCalls[0].ID, moreCalls[0].ID) ||
		!result.Poset.IsCausallyBefore(moreCalls[0].ID, failures[0].ID) {
		t.Fatal("general-for suspended exception lost nested Call causality")
	}
	firing := sourceProcessFiring(t, result, "worker")
	if len(firing.Suspensions) != 1 || len(firing.Switches) != 0 {
		t.Fatalf("general-for exception continuation audit=%#v", firing)
	}
	if len(result.ExceptionPropagations) == 0 {
		t.Fatal("general-for suspended exception lacks propagation evidence")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("general-for suspended exception replay changed canonical bytes")
	}
}
