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

func TestSourceProcessHandlerProtectedAndRecoveryBodiesSuspend(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Worker is interface
  action in Trigger();
  action out Before();
  action out Wrong();
  action out Recovered();
  action out HandlerDone();
  action out AfterDo();
end interface Worker;
module WorkerModule() return Worker is
  exception Failure;
  C : Clock is Make_Clock();
serial when Trigger do
  do
    Before();
    pause C.Ticks(1);
    raise Failure;
    Wrong();
  handler
    is Failure =>
      Recovered();
      pause C.Ticks(1);
      HandlerDone();
  end do;
  AfterDo();
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger => worker.Trigger;
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
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}
	before := sourceNamedEvents(result.Poset, "worker", "Before")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	done := sourceNamedEvents(result.Poset, "worker", "HandlerDone")
	after := sourceNamedEvents(result.Poset, "worker", "AfterDo")
	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	if len(before) != 1 || len(failure) != 1 || len(recovered) != 1 || len(done) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
		t.Fatalf("Before/Failure/Recovered/HandlerDone/After=%d/%d/%d/%d/%d",
			len(before), len(failure), len(recovered), len(done), len(after))
	}
	if !result.Poset.IsCausallyBefore(before[0].ID, failure[0].ID) ||
		!result.Poset.IsCausallyBefore(failure[0].ID, recovered[0].ID) ||
		!result.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) ||
		!result.Poset.IsCausallyBefore(done[0].ID, after[0].ID) {
		t.Fatal("suspending protected/handler control did not preserve causal order")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed suspending handler artifact")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("suspending handler replay changed canonical bytes")
	}
}

func TestSourceExternalActionInterruptCancelsPauseAndHandlerSuspends(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); action out Pulse(value : Integer); end interface Stimulus;
type Worker is interface
  action in Trigger();
  action in Pulse(value : Integer);
  action out Wrong();
  action out Recovered(value : Integer);
  action out HandlerDone();
  action out AfterDo();
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
serial when Trigger do
  do
    Wrong() pause C.Ticks(5);
  handler
    is Pulse(?Caught) =>
      Recovered(?Caught);
      pause C.Ticks(1);
      HandlerDone();
  end do;
  AfterDo();
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger => worker.Trigger;
        stimulus.Pulse => worker.Pulse;
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
	journal := arch.NewExecutionJournal(digest, 60,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
		arch.InputEvent{
			Key: "pulse", Source: "stimulus", Action: "Pulse",
			Params: map[string]any{"value": int64(7)}, Causes: []string{"trigger"},
			Timings: []gorapide.EventTiming{{Clock: clockID, Start: 1, Finish: 1}},
		},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	pulse := sourceNamedEvents(result.Poset, "stimulus", "Pulse")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	done := sourceNamedEvents(result.Poset, "worker", "HandlerDone")
	after := sourceNamedEvents(result.Poset, "worker", "AfterDo")
	if len(pulse) != 1 || len(recovered) != 1 || len(done) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
		t.Fatalf("Pulse/Recovered/HandlerDone/After=%d/%d/%d/%d",
			len(pulse), len(recovered), len(done), len(after))
	}
	value, _ := recovered[0].Param("value")
	if value != int64(7) {
		t.Fatalf("external interrupt binding=%#v", value)
	}
	var firing *arch.FiringRecord
	for index := range result.Firings {
		if result.Firings[index].Transition == "process" && result.Firings[index].Target == "worker" {
			firing = &result.Firings[index]
			break
		}
	}
	var process *arch.ProcessExecutionRecord
	for index := range result.Processes {
		if result.Processes[index].ComponentID == "worker" {
			process = &result.Processes[index]
			break
		}
	}
	if firing == nil || len(firing.Suspensions) != 2 || process == nil ||
		len(process.CanceledSuspensions) != 1 ||
		process.CanceledSuspensions[0] != firing.Suspensions[0].SuspensionID {
		t.Fatalf("interrupt suspension cancellation audit=%#v", firing)
	}
	if firing.Suspensions[0].OutputID == "" || firing.Suspensions[0].EventID != "" {
		t.Fatalf("interrupted timed action audit=%#v", firing.Suspensions[0])
	}
}

func TestSourceInterruptedDelayWindowEndsAtInterrupt(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(value : Integer); action out Pulse(); end interface Stimulus;
type Worker is interface
  action in Trigger(value : Integer); action in Pulse();
  action out Recovered(); action out Completed(value : Integer); action out After(value : Integer);
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
serial when (?Value : Integer) Trigger(?Value) do
  do
    delay C.Ticks(5);
    Completed(?Value);
  handler
    is Pulse => Recovered();
  end do;
  After(?Value);
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger => worker.Trigger;
        stimulus.Pulse => worker.Pulse;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 100,
		arch.InputEvent{
			Key: "first", Source: "stimulus", Action: "Trigger",
			Params: map[string]any{"value": int64(1)},
		},
		arch.InputEvent{
			Key: "pulse", Source: "stimulus", Action: "Pulse", Causes: []string{"first"},
			Timings: []gorapide.EventTiming{{Clock: clockID, Start: 1, Finish: 1}},
		},
		arch.InputEvent{
			Key: "second", Source: "stimulus", Action: "Trigger", Causes: []string{"pulse"},
			Params:  map[string]any{"value": int64(2)},
			Timings: []gorapide.EventTiming{{Clock: clockID, Start: 2, Finish: 2}},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	completed := sourceNamedEvents(result.Poset, "worker", "Completed")
	after := sourceNamedEvents(result.Poset, "worker", "After")
	if len(recovered) != 1 || len(completed) != 1 || len(after) != 2 {
		t.Fatalf("Recovered/Completed/After=%d/%d/%d", len(recovered), len(completed), len(after))
	}
	value, _ := completed[0].Param("value")
	if value != int64(2) {
		t.Fatalf("post-interrupt delay admitted Completed(%#v), want second trigger", value)
	}
	if len(result.Clocks) != 1 || result.Clocks[0].Now != "7" {
		t.Fatalf("post-interrupt delay clock audit=%#v", result.Clocks)
	}
}

func TestSourceActiveHandlerDrainsConnectedInterruptBeforeReadyResume(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); action out Pulse(); end interface Stimulus;
type Worker is interface
  action in Trigger(); action in Pulse();
  action out FunctionDone(); action out ProtectedWrong();
  action out Recovered(); action out AfterDo();
  provides Work : function();
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
  Work : function() is
  begin
    pause C.Ticks(1);
    FunctionDone();
  end function Work;
serial when Trigger do
  do
    Work();
    ProtectedWrong();
  handler
    is Pulse => Recovered();
  end do;
  AfterDo();
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger => worker.Trigger;
        stimulus.Pulse => worker.Pulse;
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
		arch.InputEvent{
			Key: "pulse", Source: "stimulus", Action: "Pulse", Causes: []string{"trigger"},
			Timings: []gorapide.EventTiming{{Clock: arch.ClockID("worker", "C"), Start: 1, Finish: 1}},
		},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range result.Choices {
		if choice.Domain == "semantic-step" && choice.Selected == "resume" {
			t.Fatalf("active handler exposed resume before connected interrupt closure: %#v", result.Choices)
		}
	}
	workReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Return"))
	if len(workReturns) != 0 || len(sourceNamedEvents(result.Poset, "worker", "FunctionDone")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "Recovered")) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "AfterDo")) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "ProtectedWrong")) != 0 {
		t.Fatalf("connected interrupt counts Return/FunctionDone/Recovered/After/Wrong=%d/%d/%d/%d/%d choices=%#v",
			len(workReturns), len(sourceNamedEvents(result.Poset, "worker", "FunctionDone")),
			len(sourceNamedEvents(result.Poset, "worker", "Recovered")),
			len(sourceNamedEvents(result.Poset, "worker", "AfterDo")),
			len(sourceNamedEvents(result.Poset, "worker", "ProtectedWrong")), result.Choices)
	}
	var firing *arch.FiringRecord
	for index := range result.Firings {
		if result.Firings[index].Transition == "process" && result.Firings[index].Target == "worker" {
			firing = &result.Firings[index]
			break
		}
	}
	var process *arch.ProcessExecutionRecord
	for index := range result.Processes {
		if result.Processes[index].ComponentID == "worker" {
			process = &result.Processes[index]
			break
		}
	}
	if firing == nil || len(firing.Suspensions) != 1 || len(firing.Switches) != 0 || process == nil ||
		len(process.CanceledSwitches) != 0 || len(process.CanceledSuspensions) != 1 ||
		process.CanceledSuspensions[0] != firing.Suspensions[0].SuspensionID {
		t.Fatalf("connected interrupt cancellation firing=%#v process=%#v", firing, process)
	}
	expected, _ := result.ArtifactDigest()
	if _, err := model.ReplayDeterministic(journal, expected); err != nil {
		t.Fatal(err)
	}
}

func TestSourceSuspendedNestedHandlerUsesNewestActivationOnce(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); action out Pulse(); end interface Stimulus;
type Worker is interface
  action in Trigger(); action in Pulse();
  action out InnerRecovered(); action out InnerHandlerDone();
  action out InnerWrong(); action out OuterRecovered();
  action out OuterContinued(); action out AfterOuter();
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
serial when Trigger do
  do
    do
      delay C.Ticks(5);
      InnerWrong();
    handler
      is Pulse =>
        InnerRecovered();
        pause C.Ticks(1);
        InnerHandlerDone();
    end do;
    OuterContinued();
    pause C.Ticks(1);
  handler
    is Pulse => OuterRecovered();
  end do;
  AfterOuter();
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger => worker.Trigger;
        stimulus.Pulse => worker.Pulse;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 80,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
		arch.InputEvent{
			Key: "pulse", Source: "stimulus", Action: "Pulse", Causes: []string{"trigger"},
			Timings: []gorapide.EventTiming{{Clock: arch.ClockID("worker", "C"), Start: 1, Finish: 1}},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	inner := sourceNamedEvents(result.Poset, "worker", "InnerRecovered")
	innerDone := sourceNamedEvents(result.Poset, "worker", "InnerHandlerDone")
	outerContinued := sourceNamedEvents(result.Poset, "worker", "OuterContinued")
	after := sourceNamedEvents(result.Poset, "worker", "AfterOuter")
	if len(inner) != 1 || len(innerDone) != 1 || len(outerContinued) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "OuterRecovered")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "InnerWrong")) != 0 {
		t.Fatalf("Inner/InnerDone/OuterContinued/After=%d/%d/%d/%d",
			len(inner), len(innerDone), len(outerContinued), len(after))
	}
}

func TestSourceInterruptGeneratedBeforeHandlerActivationDoesNotMatch(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Pulse(); action out Trigger(); end interface Stimulus;
type Worker is interface
  action in Pulse(); action in Trigger();
  action out ProtectedDone(); action out WrongRecovery(); action out AfterDo();
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
serial when Trigger do
  do
    pause C.Ticks(1);
    ProtectedDone();
  handler
    is Pulse => WrongRecovery();
  end do;
  AfterDo();
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Pulse => worker.Pulse;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 40,
		arch.InputEvent{Key: "pulse", Source: "stimulus", Action: "Pulse"},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger", Causes: []string{"pulse"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceNamedEvents(result.Poset, "worker", "ProtectedDone")) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "AfterDo")) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "WrongRecovery")) != 0 {
		t.Fatal("pre-activation action occurrence incorrectly interrupted protected computation")
	}
}

func TestSourceSuspendedHandlerReraisesExactOccurrenceToOuterHandler(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Worker is interface
  action in Trigger();
  action out BeforeReraise(); action out InnerWrong();
  action out OuterRecovered(); action out AfterOuter();
end interface Worker;
module WorkerModule() return Worker is
  exception Failure;
  C : Clock is Make_Clock();
serial when Trigger do
  do
    do
      pause C.Ticks(1);
      raise Failure;
    handler
      is Failure =>
        BeforeReraise();
        pause C.Ticks(1);
        raise;
    end do;
    InnerWrong();
  handler
    is Failure => OuterRecovered();
  end do;
  AfterOuter();
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger => worker.Trigger;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 60,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	before := sourceNamedEvents(result.Poset, "worker", "BeforeReraise")
	outer := sourceNamedEvents(result.Poset, "worker", "OuterRecovered")
	after := sourceNamedEvents(result.Poset, "worker", "AfterOuter")
	if len(failure) != 1 || len(before) != 1 || len(outer) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "InnerWrong")) != 0 {
		t.Fatalf("Failure/BeforeReraise/OuterRecovered/After=%d/%d/%d/%d",
			len(failure), len(before), len(outer), len(after))
	}
	if !result.Poset.IsCausallyBefore(failure[0].ID, outer[0].ID) {
		t.Fatal("suspended unnamed re-raise lost the original exception occurrence")
	}
}

func TestSourceExternalInterruptUnwindsSuspendedNestedFunction(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); action out Pulse(value : Integer); end interface Stimulus;
type Worker is interface
  action in Trigger(); action in Pulse(value : Integer);
  action out InnerWrong(); action out OuterWrong();
  action out Recovered(value : Integer); action out HandlerDone(); action out CallerDone();
  provides Inner : function(); provides Outer : function();
end interface Worker;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
  Inner : function() is
  begin
    pause C.Ticks(5);
    InnerWrong();
  end function Inner;
  Outer : function() is
  begin
    Inner();
    OuterWrong();
  handler
    is Pulse(?Caught) =>
      Recovered(?Caught);
      pause C.Ticks(1);
      HandlerDone();
  end function Outer;
serial when Trigger do
  Outer();
  CallerDone();
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger => worker.Trigger;
        stimulus.Pulse => worker.Pulse;
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
		arch.InputEvent{
			Key: "pulse", Source: "stimulus", Action: "Pulse",
			Params: map[string]any{"value": int64(9)}, Causes: []string{"trigger"},
			Timings: []gorapide.EventTiming{{Clock: arch.ClockID("worker", "C"), Start: 1, Finish: 1}},
		},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	done := sourceNamedEvents(result.Poset, "worker", "HandlerDone")
	caller := sourceNamedEvents(result.Poset, "worker", "CallerDone")
	outerReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Outer'Return"))
	innerReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Inner'Return"))
	if len(recovered) != 1 || len(done) != 1 || len(caller) != 1 || len(outerReturns) != 1 || len(innerReturns) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "InnerWrong")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "OuterWrong")) != 0 {
		t.Fatalf("Recovered/HandlerDone/Caller/OuterReturn/InnerReturn=%d/%d/%d/%d/%d",
			len(recovered), len(done), len(caller), len(outerReturns), len(innerReturns))
	}
	value, _ := recovered[0].Param("value")
	if value != int64(9) {
		t.Fatalf("nested-function interrupt binding=%#v", value)
	}
	if !result.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) ||
		!result.Poset.IsCausallyBefore(done[0].ID, outerReturns[0].ID) ||
		!result.Poset.IsCausallyBefore(outerReturns[0].ID, caller[0].ID) {
		t.Fatal("nested-function interrupt did not unwind and resume in causal order")
	}
	expected, _ := result.ArtifactDigest()
	if _, err := model.ReplayDeterministic(journal, expected); err != nil {
		t.Fatal(err)
	}
	explored, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 64, MaxChoiceDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 64, MaxChoiceDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	left, _ := explored.MarshalCanonical()
	right, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || len(explored.Computations) == 0 || !bytes.Equal(left, right) {
		t.Fatalf("nested-function interrupt exploration=%#v", explored)
	}
}

func TestSourceProcessHandlerCatchesExceptionFromSuspendedFunction(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Worker is interface
  action in Trigger();
  action out InnerWrong(); action out ProtectedWrong();
  action out Recovered(); action out HandlerDone(); action out AfterDo();
  provides Inner : function();
end interface Worker;
module WorkerModule() return Worker is
  exception Failure;
  C : Clock is Make_Clock();
  Inner : function() is
  begin
    pause C.Ticks(1);
    raise Failure;
    InnerWrong();
  end function Inner;
serial when Trigger do
  do
    Inner();
    ProtectedWrong();
  handler
    is Failure =>
      Recovered();
      pause C.Ticks(1);
      HandlerDone();
  end do;
  AfterDo();
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger => worker.Trigger;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 80,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	done := sourceNamedEvents(result.Poset, "worker", "HandlerDone")
	after := sourceNamedEvents(result.Poset, "worker", "AfterDo")
	innerReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Inner'Return"))
	if len(failure) != 1 || len(recovered) != 1 || len(done) != 1 || len(after) != 1 || len(innerReturns) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "InnerWrong")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "ProtectedWrong")) != 0 {
		t.Fatalf("Failure/Recovered/HandlerDone/After/InnerReturn=%d/%d/%d/%d/%d",
			len(failure), len(recovered), len(done), len(after), len(innerReturns))
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("caught suspended-function exception propagated: %#v", result.ExceptionPropagations)
	}
}

func TestSourceDirectFunctionHandlerCatchesImmediateActionFromNestedCall(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Worker is interface
  action in Trigger();
  action out Pulse(); action out HelperWrong(); action out OuterWrong();
  action out Recovered(); action out CallerDone();
  provides Helper : function(); provides Outer : function();
end interface Worker;
module WorkerModule() return Worker is
  Helper : function() is
  begin
    Pulse();
    HelperWrong();
  end function Helper;
  Outer : function() is
  begin
    Helper();
    OuterWrong();
  handler
    is Pulse => Recovered();
  end function Outer;
serial when Trigger do
  Outer();
  CallerDone();
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger => worker.Trigger;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 40,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	pulse := sourceNamedEvents(result.Poset, "worker", "Pulse")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	caller := sourceNamedEvents(result.Poset, "worker", "CallerDone")
	outerReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Outer'Return"))
	helperReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Helper'Return"))
	if len(pulse) != 1 || len(recovered) != 1 || len(caller) != 1 || len(outerReturns) != 1 || len(helperReturns) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "HelperWrong")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "OuterWrong")) != 0 {
		t.Fatalf("Pulse/Recovered/Caller/OuterReturn/HelperReturn=%d/%d/%d/%d/%d",
			len(pulse), len(recovered), len(caller), len(outerReturns), len(helperReturns))
	}
	if !result.Poset.IsCausallyBefore(pulse[0].ID, recovered[0].ID) ||
		!result.Poset.IsCausallyBefore(recovered[0].ID, outerReturns[0].ID) ||
		!result.Poset.IsCausallyBefore(outerReturns[0].ID, caller[0].ID) {
		t.Fatal("nested immediate interrupt did not abandon and resume in causal order")
	}
}

func TestSourceRemoteFunctionActionInterruptsCallerAtGenerationTime(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Client is interface
  action in Trigger(); action in Pulse();
  action out Recovered(); action out Done();
  requires Lookup : function();
end interface Client;
type Server is interface
  action out Pulse(); action out Wrong();
  provides Fetch : function();
end interface Server;
module ClientModule() return Client is
serial when Trigger do
  do Lookup(); handler is Pulse => Recovered(); end do;
  Done();
end when;
end module ClientModule;
module ServerModule() return Server is
  Fetch : function() is begin Pulse(); Wrong(); end function Fetch;
end module ServerModule;
architecture System() is
  stimulus : Stimulus;
  client : Client is ClientModule();
  server : Server is ServerModule();
connect stimulus.Trigger => client.Trigger;
        client.Lookup to server.Fetch;
        server.Pulse => client.Pulse;
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
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}
	serverPulse := sourceNamedEvents(result.Poset, "server", "Pulse")
	clientPulse := sourceNamedEvents(result.Poset, "client", "Pulse")
	recovered := sourceNamedEvents(result.Poset, "client", "Recovered")
	done := sourceNamedEvents(result.Poset, "client", "Done")
	fetchReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "server", "Fetch'Return"))
	lookupReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "client", "Lookup'Return"))
	if len(serverPulse) != 1 || len(clientPulse) != 1 || len(recovered) != 1 || len(done) != 1 ||
		len(fetchReturns) != 0 || len(lookupReturns) != 0 ||
		len(sourceNamedEvents(result.Poset, "server", "Wrong")) != 0 {
		t.Fatalf("serverPulse/clientPulse/Recovered/Done/FetchReturn/LookupReturn=%d/%d/%d/%d/%d/%d",
			len(serverPulse), len(clientPulse), len(recovered), len(done), len(fetchReturns), len(lookupReturns))
	}
	if serverPulse[0].ID == clientPulse[0].ID ||
		!result.Poset.IsCausallyBefore(serverPulse[0].ID, clientPulse[0].ID) ||
		!result.Poset.IsCausallyBefore(clientPulse[0].ID, recovered[0].ID) ||
		!result.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) {
		t.Fatalf("remote generation-time interrupt causality same=%v source-target=%v target-recovery=%v recovery-done=%v",
			serverPulse[0].ID == clientPulse[0].ID,
			result.Poset.IsCausallyBefore(serverPulse[0].ID, clientPulse[0].ID),
			result.Poset.IsCausallyBefore(clientPulse[0].ID, recovered[0].ID),
			result.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID))
	}
	connectionFirings := 0
	var processSequence, connectionSequence uint64
	for _, firing := range result.Firings {
		if firing.Transition == "process" && firing.Target == "client" {
			processSequence = firing.Sequence
			for _, generated := range firing.Generated {
				if generated.EventID == string(clientPulse[0].ID) {
					t.Fatal("connected Pulse was misattributed as a direct process output")
				}
			}
		}
		if firing.Transition == "connection" && firing.TriggerID == string(serverPulse[0].ID) &&
			firing.Target == "client" && firing.ResultID == string(clientPulse[0].ID) {
			connectionFirings++
			connectionSequence = firing.Sequence
		}
	}
	if connectionFirings != 1 || processSequence == 0 || connectionSequence <= processSequence {
		t.Fatalf("generation-time audit ordering process=%d connection=%d matching-connections=%d",
			processSequence, connectionSequence, connectionFirings)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed remote generation-time interrupt artifact")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("remote generation-time interrupt replay changed canonical bytes")
	}
	explored, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 64, MaxChoiceDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 64, MaxChoiceDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	left, _ := explored.MarshalCanonical()
	right, _ := exploredAgain.MarshalCanonical()
	if !explored.Complete || len(explored.Computations) == 0 || !bytes.Equal(left, right) {
		t.Fatalf("remote generation-time interrupt exploration=%#v", explored)
	}
}

func TestSourceRemoteFunctionInterruptClosesEveryConnectionKind(t *testing.T) {
	tests := []struct {
		name, connector, kind string
		sameOccurrence        bool
	}{
		{name: "basic", connector: "to", kind: arch.BasicConnection.String(), sameOccurrence: true},
		{name: "pipe", connector: "=>", kind: arch.PipeConnection.String()},
		{name: "agent", connector: "||>", kind: arch.AgentConnection.String()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(fmt.Sprintf(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Client is interface
  action in Trigger(); action in Pulse(); action out Recovered(); action out Done();
  requires Lookup : function();
end interface Client;
type Server is interface
  action out Pulse(); action out Wrong(); provides Fetch : function();
end interface Server;
module ClientModule() return Client is
serial when Trigger do
  do Lookup(); handler is Pulse => Recovered(); end do;
  Done();
end when;
end module ClientModule;
module ServerModule() return Server is
  Fetch : function() is begin Pulse(); Wrong(); end function Fetch;
end module ServerModule;
architecture System() is
  stimulus : Stimulus; client : Client is ClientModule(); server : Server is ServerModule();
connect stimulus.Trigger => client.Trigger;
        client.Lookup to server.Fetch;
        server.Pulse %s client.Pulse;
end architecture System;
`, test.connector))
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
			serverPulse := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "server", "Pulse"))
			clientPulse := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "client", "Pulse"))
			recovered := sourceNamedEvents(result.Poset, "client", "Recovered")
			done := sourceNamedEvents(result.Poset, "client", "Done")
			if len(serverPulse) != 1 || len(clientPulse) != 1 || len(recovered) != 1 || len(done) != 1 ||
				len(sourceNamedEvents(result.Poset, "server", "Wrong")) != 0 ||
				len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "server", "Fetch'Return"))) != 0 {
				t.Fatalf("serverPulse/clientPulse/Recovered/Done=%d/%d/%d/%d",
					len(serverPulse), len(clientPulse), len(recovered), len(done))
			}
			if (serverPulse[0].ID == clientPulse[0].ID) != test.sameOccurrence {
				t.Fatalf("%s occurrence identity source=%s target=%s", test.name, serverPulse[0].ID, clientPulse[0].ID)
			}
			if !test.sameOccurrence && !result.Poset.IsCausallyBefore(serverPulse[0].ID, clientPulse[0].ID) {
				t.Fatalf("%s target is not caused by its source", test.name)
			}
			if !result.Poset.IsCausallyBefore(clientPulse[0].ID, recovered[0].ID) ||
				!result.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) {
				t.Fatalf("%s handler recovery causality is incomplete", test.name)
			}
			matchingFirings := 0
			for _, firing := range result.Firings {
				if firing.Transition == "connection" && firing.ConnectionKind == test.kind &&
					firing.TriggerID == string(serverPulse[0].ID) && firing.Target == "client" &&
					firing.ResultID == string(clientPulse[0].ID) {
					matchingFirings++
				}
			}
			if matchingFirings != 1 {
				t.Fatalf("%s generation-time firing count=%d, want 1", test.name, matchingFirings)
			}
		})
	}
}

func TestSourceNestedRemoteFunctionInterruptUnwindsEveryCall(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Client is interface
  action in Trigger(); action in Pulse(); action out Recovered(); action out Done();
  requires Lookup : function();
end interface Client;
type Server is interface
  action out Pulse(); action out HelperWrong(); action out FetchWrong();
  provides Fetch : function(); provides Helper : function();
end interface Server;
module ClientModule() return Client is
serial when Trigger do
  do Lookup(); handler is Pulse => Recovered(); end do;
  Done();
end when;
end module ClientModule;
module ServerModule() return Server is
  Helper : function() is begin Pulse(); HelperWrong(); end function Helper;
  Fetch : function() is begin Helper(); FetchWrong(); end function Fetch;
end module ServerModule;
architecture System() is
  stimulus : Stimulus; client : Client is ClientModule(); server : Server is ServerModule();
connect stimulus.Trigger => client.Trigger;
        client.Lookup to server.Fetch;
        server.Pulse => client.Pulse;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 60,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceNamedEvents(result.Poset, "client", "Recovered")) != 1 ||
		len(sourceNamedEvents(result.Poset, "client", "Done")) != 1 ||
		len(sourceNamedEvents(result.Poset, "server", "HelperWrong")) != 0 ||
		len(sourceNamedEvents(result.Poset, "server", "FetchWrong")) != 0 ||
		len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "server", "Helper'Return"))) != 0 ||
		len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "server", "Fetch'Return"))) != 0 ||
		len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "client", "Lookup'Return"))) != 0 {
		t.Fatalf("nested remote interrupt did not abandon every active call: %#v", result.Firings)
	}
}

func TestSourceTimedRemoteFunctionInterruptsAfterProviderPause(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Client is interface
  action in Trigger(); action in Pulse(); action out Recovered(); action out Done();
  requires Lookup : function();
end interface Client;
type Server is interface action out Pulse(); action out Wrong(); provides Fetch : function(); end interface Server;
module ClientModule() return Client is
serial when Trigger do
  do Lookup(); handler is Pulse => Recovered(); end do;
  Done();
end when;
end module ClientModule;
module ServerModule() return Server is
  S : Clock is Make_Clock();
  Fetch : function() is begin pause S.Ticks(2); Pulse(); Wrong(); end function Fetch;
end module ServerModule;
architecture System() is
  stimulus : Stimulus; client : Client is ClientModule(); server : Server is ServerModule();
connect stimulus.Trigger => client.Trigger;
        client.Lookup to server.Fetch;
        server.Pulse => client.Pulse;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 80,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	pulse := sourceNamedEvents(result.Poset, "server", "Pulse")
	if len(pulse) != 1 || len(sourceNamedEvents(result.Poset, "client", "Recovered")) != 1 ||
		len(sourceNamedEvents(result.Poset, "client", "Done")) != 1 ||
		len(sourceNamedEvents(result.Poset, "server", "Wrong")) != 0 ||
		len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "server", "Fetch'Return"))) != 0 {
		t.Fatalf("timed remote interrupt Pulse=%d firings=%#v", len(pulse), result.Firings)
	}
	timing, related := pulse[0].Timing(arch.ClockID("server", "S"))
	if !related || timing.Start != 2 || timing.Finish != 2 {
		t.Fatalf("timed remote interrupt timing=%#v related=%t", timing, related)
	}
}

func TestSourceRemoteFunctionInterruptRetainsOuterConnectionAndHandlerBindings(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(seed : Integer); end interface Stimulus;
type Client is interface
  action in Trigger(seed : Integer); action in Pulse(value : Integer);
  action out Recovered(value : Integer); action out Done();
  requires Lookup : function();
end interface Client;
type Server is interface action out Pulse(value : Integer); action out Wrong(); provides Fetch : function(); end interface Server;
module ClientModule() return Client is
serial when (?Seed : Integer) Trigger(?Seed) do
  do Lookup(); handler is Pulse(?Caught) => Recovered(?Seed + ?Caught); end do;
  Done();
end when;
end module ClientModule;
module ServerModule() return Server is
  Fetch : function() is begin Pulse(6); Wrong(); end function Fetch;
end module ServerModule;
architecture System() is
  stimulus : Stimulus; client : Client is ClientModule(); server : Server is ServerModule();
connect stimulus.Trigger => client.Trigger;
        client.Lookup to server.Fetch;
        (?N : Integer) server.Pulse(?N) => client.Pulse(?N + 1);
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 50,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger", Params: map[string]any{"seed": 5}},
	))
	if err != nil {
		t.Fatal(err)
	}
	recovered := sourceNamedEvents(result.Poset, "client", "Recovered")
	if len(recovered) != 1 || len(sourceNamedEvents(result.Poset, "server", "Wrong")) != 0 {
		t.Fatalf("bound remote recovery=%d firings=%#v", len(recovered), result.Firings)
	}
	if value, _ := recovered[0].Param("value"); value != int64(12) {
		t.Fatalf("bound remote recovery value=%#v, want 12", value)
	}
}

func TestSourceCompoundGenerationTimeInterruptClosesVisiblePendingPoset(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Client is interface
  action in Trigger(); action in Pulse(value : Integer);
  action out Recovered(value : Integer); action out Done();
  requires Lookup : function();
end interface Client;
type Server is interface
  action out First(value : Integer); action out Second(value : Integer);
  action out Wrong(); provides Fetch : function();
end interface Server;
module ClientModule() return Client is
serial when Trigger do
  do Lookup(); handler is Pulse(?Caught) => Recovered(?Caught); end do;
  Done();
end when;
end module ClientModule;
module ServerModule() return Server is
  Fetch : function() is begin First(6); Second(6); Wrong(); end function Fetch;
end module ServerModule;
architecture System() is
  stimulus : Stimulus; client : Client is ClientModule(); server : Server is ServerModule();
connect stimulus.Trigger => client.Trigger;
        client.Lookup to server.Fetch;
        (?N : Integer) (server.First(?N) -> server.Second(?N)) => client.Pulse(?N + 1);
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
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}
	first := sourceNamedEvents(result.Poset, "server", "First")
	second := sourceNamedEvents(result.Poset, "server", "Second")
	pulse := sourceNamedEvents(result.Poset, "client", "Pulse")
	recovered := sourceNamedEvents(result.Poset, "client", "Recovered")
	done := sourceNamedEvents(result.Poset, "client", "Done")
	if len(first) != 1 || len(second) != 1 || len(pulse) != 1 || len(recovered) != 1 || len(done) != 1 ||
		len(sourceNamedEvents(result.Poset, "server", "Wrong")) != 0 ||
		len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "server", "Fetch'Return"))) != 0 ||
		len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "client", "Lookup'Return"))) != 0 {
		t.Fatalf("compound generation-time events First/Second/Pulse/Recovered/Done=%d/%d/%d/%d/%d",
			len(first), len(second), len(pulse), len(recovered), len(done))
	}
	if !result.Poset.IsCausallyBefore(first[0].ID, pulse[0].ID) ||
		!result.Poset.IsCausallyBefore(second[0].ID, pulse[0].ID) ||
		!result.Poset.IsCausallyBefore(pulse[0].ID, recovered[0].ID) ||
		!result.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) {
		t.Fatal("compound generation-time interrupt lost match or recovery causality")
	}
	if pulse[0].ParamInt("value") != 7 || recovered[0].ParamInt("value") != 7 {
		t.Fatalf("compound generation-time binding Pulse/Recovered=%d/%d",
			pulse[0].ParamInt("value"), recovered[0].ParamInt("value"))
	}
	compoundFirings := 0
	for _, firing := range result.Firings {
		if firing.ConnectionScope == arch.ArchitectureConnectionScope.String() &&
			firing.ConnectionKind == arch.PipeConnection.String() && firing.Target == "client" &&
			firing.ResultID == string(pulse[0].ID) {
			compoundFirings++
			if len(firing.MatchedEvents) != 2 {
				t.Fatalf("compound generation-time firing=%#v", firing)
			}
		}
	}
	if compoundFirings != 1 {
		t.Fatalf("compound generation-time firing count=%d", compoundFirings)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed compound generation-time artifact")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("compound generation-time replay changed canonical bytes")
	}
}

func TestSourceIndependentGenerationTimeInterruptsAreReplayableChoices(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Client is interface
  action in Trigger(); action in Pulse(); action in Alert();
  action out ChosePulse(); action out ChoseAlert(); action out Done();
  requires Lookup : function();
end interface Client;
type Server is interface action out Signal(); action out Wrong(); provides Fetch : function(); end interface Server;
module ClientModule() return Client is
serial when Trigger do
  do Lookup(); handler is Pulse => ChosePulse(); is Alert => ChoseAlert(); end do;
  Done();
end when;
end module ClientModule;
module ServerModule() return Server is
  Fetch : function() is begin Signal(); Wrong(); end function Fetch;
end module ServerModule;
architecture System() is
  stimulus : Stimulus; client : Client is ClientModule(); server : Server is ServerModule();
connect stimulus.Trigger => client.Trigger;
        client.Lookup to server.Fetch;
        server.Signal => client.Pulse;
        server.Signal ||> client.Alert;
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
	assertBranch := func(t *testing.T, candidate *arch.ExecutionResult) string {
		t.Helper()
		pulse := len(sourceNamedEvents(candidate.Poset, "client", "ChosePulse"))
		alert := len(sourceNamedEvents(candidate.Poset, "client", "ChoseAlert"))
		if pulse+alert != 1 || len(sourceNamedEvents(candidate.Poset, "client", "Done")) != 1 ||
			len(sourceNamedEvents(candidate.Poset, "server", "Wrong")) != 0 ||
			len(distinctAllocatorEvents(sourceNamedEvents(candidate.Poset, "server", "Fetch'Return"))) != 0 {
			t.Fatalf("generation-time choice Pulse/Alert=%d/%d", pulse, alert)
		}
		if pulse == 1 {
			return "pulse"
		}
		return "alert"
	}
	selectedBranch := assertBranch(t, result)
	var interruptChoice *arch.ChoiceResolution
	for index := range result.Choices {
		if result.Choices[index].Domain == "generation-time-interrupt" {
			interruptChoice = &result.Choices[index]
			break
		}
	}
	if interruptChoice == nil || len(interruptChoice.Options) != 2 {
		t.Fatalf("generation-time interrupt choice=%#v", interruptChoice)
	}
	alternateSelection := interruptChoice.Options[0]
	if alternateSelection == interruptChoice.Selected {
		alternateSelection = interruptChoice.Options[1]
	}
	alternateJournal := journal
	alternateJournal.Choices = []arch.ChoiceDecision{{
		Point: interruptChoice.Point, Selection: alternateSelection,
	}}
	alternate, err := model.ExecuteDeterministic(alternateJournal)
	if err != nil {
		t.Fatal(err)
	}
	if alternateBranch := assertBranch(t, alternate); alternateBranch == selectedBranch {
		t.Fatalf("alternate generation-time selection retained %s branch", selectedBranch)
	}
	for _, candidate := range []struct {
		result  *arch.ExecutionResult
		journal arch.ExecutionJournal
	}{{result: result, journal: journal}, {result: alternate, journal: alternateJournal}} {
		expected, err := candidate.result.ArtifactDigest()
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := model.ReplayDeterministic(candidate.journal, expected)
		if err != nil {
			t.Fatal(err)
		}
		left, _ := candidate.result.MarshalCanonical()
		right, _ := replayed.MarshalCanonical()
		if !bytes.Equal(left, right) {
			t.Fatal("generation-time interrupt choice did not replay byte-identically")
		}
	}
	explored, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 128, MaxChoiceDepth: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 2 {
		t.Fatalf("generation-time interrupt exploration complete=%t computations=%d executions=%d",
			explored.Complete, len(explored.Computations), explored.Executions)
	}
}

func TestSourceModuleCompoundGenerationTimeInterruptClosesBeforeProviderContinues(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Client is interface
  action in Trigger(); action in Pulse(); action out Recovered(); action out Done();
  requires Lookup : function();
end interface Client;
type Server is interface
  action out First(); action out Second(); action out Combined(); action out Wrong();
  provides Fetch : function();
end interface Server;
module ClientModule() return Client is
serial when Trigger do
  do Lookup(); handler is Pulse => Recovered(); end do;
  Done();
end when;
end module ClientModule;
module ServerModule() return Server is
  Fetch : function() is begin First(); Second(); Wrong(); end function Fetch;
connect
  (First and Second) => Combined;
end module ServerModule;
architecture System() is
  stimulus : Stimulus; client : Client is ClientModule(); server : Server is ServerModule();
connect stimulus.Trigger => client.Trigger;
        client.Lookup to server.Fetch;
        server.Combined => client.Pulse;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 80,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	first := sourceNamedEvents(result.Poset, "server", "First")
	second := sourceNamedEvents(result.Poset, "server", "Second")
	combined := sourceNamedEvents(result.Poset, "server", "Combined")
	pulse := sourceNamedEvents(result.Poset, "client", "Pulse")
	recovered := sourceNamedEvents(result.Poset, "client", "Recovered")
	if len(first) != 1 || len(second) != 1 || len(combined) != 1 || len(pulse) != 1 || len(recovered) != 1 ||
		len(sourceNamedEvents(result.Poset, "server", "Wrong")) != 0 {
		t.Fatalf("module compound First/Second/Combined/Pulse/Recovered=%d/%d/%d/%d/%d",
			len(first), len(second), len(combined), len(pulse), len(recovered))
	}
	if !result.Poset.IsCausallyBefore(first[0].ID, combined[0].ID) ||
		!result.Poset.IsCausallyBefore(second[0].ID, combined[0].ID) ||
		!result.Poset.IsCausallyBefore(combined[0].ID, pulse[0].ID) ||
		!result.Poset.IsCausallyBefore(pulse[0].ID, recovered[0].ID) {
		t.Fatal("module compound generation-time closure lost causal structure")
	}
	moduleFirings := 0
	for _, firing := range result.Firings {
		if firing.ConnectionScope == arch.ModuleConnectionScope.String() &&
			firing.ResultID == string(combined[0].ID) {
			moduleFirings++
			if len(firing.MatchedEvents) != 2 {
				t.Fatalf("module compound generation-time firing=%#v", firing)
			}
		}
	}
	if moduleFirings != 1 {
		t.Fatalf("module compound generation-time firings=%d", moduleFirings)
	}
}

func TestSourceContextQualifiedGenerationTimeInterruptUsesLinkInterval(t *testing.T) {
	source := []byte(`
type Plane is interface
  action out Register(value : Plane); action out Radio(value : Integer); action out Wrong();
  provides Fetch : function();
end interface Plane;
type Sector is interface
  action in Accept(value : Plane); action out Receive(value : Integer);
  action out Recovered(value : Integer); action out Done();
  requires Lookup : function();
end interface Sector;
module PlaneModule() return Plane is
  Fetch : function() is begin Radio(9); Wrong(); end function Fetch;
initial
  Register(Self);
end module PlaneModule;
module SectorModule() return Sector is
connect
  (?Peer : Plane; ?N : Integer) ?Peer.Radio(?N) => Receive(?N);
serial when (?Peer : Plane) Accept(?Peer) do
  Link(?Peer);
  do Lookup(); handler is Receive(?Caught) => Recovered(?Caught); end do;
  Done();
end when;
end module SectorModule;
architecture System() is
  plane : Plane is PlaneModule();
  sector : Sector is SectorModule();
connect plane.Register to sector.Accept;
        sector.Lookup to plane.Fetch;
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
	journal := arch.NewExecutionJournal(digest, 100)
	previous := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}
	radio := sourceNamedEvents(result.Poset, "plane", "Radio")
	receive := sourceNamedEvents(result.Poset, "sector", "Receive")
	recovered := sourceNamedEvents(result.Poset, "sector", "Recovered")
	done := sourceNamedEvents(result.Poset, "sector", "Done")
	if len(radio) != 1 || len(receive) != 1 || len(recovered) != 1 || len(done) != 1 ||
		len(sourceNamedEvents(result.Poset, "plane", "Wrong")) != 0 ||
		len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "plane", "Fetch'Return"))) != 0 {
		t.Fatalf("Context generation-time Radio/Receive/Recovered/Done=%d/%d/%d/%d",
			len(radio), len(receive), len(recovered), len(done))
	}
	if receive[0].ParamInt("value") != 9 || recovered[0].ParamInt("value") != 9 ||
		!result.Poset.IsCausallyBefore(radio[0].ID, receive[0].ID) ||
		!result.Poset.IsCausallyBefore(receive[0].ID, recovered[0].ID) {
		t.Fatal("Context-qualified generation-time binding or causality is incomplete")
	}
	planeModule, sectorModule := "", ""
	for _, module := range result.Modules {
		if module.Occurrence == "component:plane" {
			planeModule = module.ModuleID
		}
		if module.Occurrence == "component:sector" {
			sectorModule = module.ModuleID
		}
	}
	linked := false
	for _, context := range result.Contexts {
		if context.Kind == "explicit-link" && context.Source == planeModule &&
			context.Destination == sectorModule && context.Live {
			linked = true
		}
	}
	qualifiedFiring := false
	for _, firing := range result.Firings {
		if firing.ConnectionScope == arch.ModuleConnectionScope.String() &&
			firing.TriggerID == string(radio[0].ID) && firing.ResultID == string(receive[0].ID) {
			qualifiedFiring = true
		}
	}
	if planeModule == "" || sectorModule == "" || !linked || !qualifiedFiring {
		t.Fatalf("Context interval or qualified firing missing plane=%q sector=%q linked=%t firing=%t",
			planeModule, sectorModule, linked, qualifiedFiring)
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	repeatedBytes, _ := repeated.MarshalCanonical()
	if !bytes.Equal(left, repeatedBytes) {
		t.Fatal("GOMAXPROCS changed Context-qualified generation-time interrupt bytes")
	}
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("Context-qualified generation-time interrupt replay changed canonical bytes")
	}
}

func TestSourceUnlinkedProviderIsExcludedFromGenerationTimeModuleConnection(t *testing.T) {
	source := []byte(`
type Plane is interface
  action out Register(value : Plane); action out Radio(value : Integer); action out Wrong();
  provides Fetch : function();
end interface Plane;
type Sector is interface
  action in Accept(value : Plane); action out Receive(value : Integer);
  action out Recovered(value : Integer); action out Done();
  requires Lookup : function();
end interface Sector;
module PlaneModule() return Plane is
  Fetch : function() is begin Radio(9); Wrong(); end function Fetch;
initial Register(Self); end module PlaneModule;
module SectorModule() return Sector is
connect
  (?Peer : Plane; ?N : Integer) ?Peer.Radio(?N) => Receive(?N);
serial when (?Peer : Plane) Accept(?Peer) do
  Link(?Peer); Unlink(?Peer);
  do Lookup(); handler is Receive(?Caught) => Recovered(?Caught); end do;
  Done();
end when;
end module SectorModule;
architecture System() is
  plane : Plane is PlaneModule(); sector : Sector is SectorModule();
connect plane.Register to sector.Accept; sector.Lookup to plane.Fetch;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 100))
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceNamedEvents(result.Poset, "plane", "Radio")) != 1 ||
		len(sourceNamedEvents(result.Poset, "plane", "Wrong")) != 1 ||
		len(sourceNamedEvents(result.Poset, "sector", "Receive")) != 0 ||
		len(sourceNamedEvents(result.Poset, "sector", "Recovered")) != 0 ||
		len(sourceNamedEvents(result.Poset, "sector", "Done")) != 1 ||
		len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "plane", "Fetch'Return"))) != 1 ||
		len(distinctAllocatorEvents(sourceNamedEvents(result.Poset, "sector", "Lookup'Return"))) != 1 {
		t.Fatalf("unlinked generation-time module visibility firings=%#v", result.Firings)
	}
	for _, firing := range result.Firings {
		if firing.ConnectionScope == arch.ModuleConnectionScope.String() &&
			firing.TriggerAction == "Radio" {
			t.Fatalf("unlinked provider fired Context-qualified route: %#v", firing)
		}
	}
	closedLink := false
	for _, context := range result.Contexts {
		if context.Kind == "explicit-link" && !context.Live && len(context.LostAfter) != 0 {
			closedLink = true
		}
	}
	if !closedLink {
		t.Fatalf("closed explicit Context interval is absent: %#v", result.Contexts)
	}
}

func TestSourceCausallyEarlierGenerationTimeInterruptPrecedesDescendant(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Client is interface
  action in Trigger(); action in Pulse(); action out Alert();
  action out ChosePulse(); action out ChoseAlert(); action out Done();
  requires Lookup : function();
end interface Client;
type Server is interface action out Signal(); action out Wrong(); provides Fetch : function(); end interface Server;
module ClientModule() return Client is
connect Pulse => Alert;
serial when Trigger do
  do Lookup(); handler is Pulse => ChosePulse(); is Alert => ChoseAlert(); end do;
  Done();
end when;
end module ClientModule;
module ServerModule() return Server is
  Fetch : function() is begin Signal(); Wrong(); end function Fetch;
end module ServerModule;
architecture System() is
  stimulus : Stimulus; client : Client is ClientModule(); server : Server is ServerModule();
connect stimulus.Trigger => client.Trigger;
        client.Lookup to server.Fetch;
        server.Signal => client.Pulse;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 80,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	pulse := sourceNamedEvents(result.Poset, "client", "Pulse")
	alert := sourceNamedEvents(result.Poset, "client", "Alert")
	if len(pulse) != 1 || len(alert) != 1 ||
		len(sourceNamedEvents(result.Poset, "client", "ChosePulse")) != 1 ||
		len(sourceNamedEvents(result.Poset, "client", "ChoseAlert")) != 0 ||
		!result.Poset.IsCausallyBefore(pulse[0].ID, alert[0].ID) {
		t.Fatalf("causal interrupt priority Pulse/Alert=%d/%d choices=%#v", len(pulse), len(alert), result.Choices)
	}
	for _, choice := range result.Choices {
		if choice.Domain == "generation-time-interrupt" {
			t.Fatal("causally ordered interrupt occurrences became a false semantic choice")
		}
	}
}

func TestSourceRemoteFunctionModuleAllocationUnderActiveInterruptRetainsLifecycle(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Client is interface
  action in Trigger(); action in Pulse(); action out Done();
  requires Lookup : function();
end interface Client;
type Server is interface
  action out Used(value : Server);
  provides Fetch : function();
end interface Server;
module ClientModule() return Client is
serial when Trigger do
  do Lookup(); handler is Pulse => null; end do;
  Done();
end when;
end module ClientModule;
module ServerModule() return Server is
  Fetch : function() is
    Child : Server is New();
  begin
    Used(Child);
  end function Fetch;
end module ServerModule;
architecture System() is
  stimulus : Stimulus; client : Client is ClientModule(); server : Server is ServerModule();
connect stimulus.Trigger => client.Trigger; client.Lookup to server.Fetch;
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

	used := sourceNamedEvents(first.Poset, "server", "Used")
	fetchCalls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "server", "Fetch'Call"))
	fetchReturns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "server", "Fetch'Return"))
	lookupReturns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "client", "Lookup'Return"))
	done := sourceNamedEvents(first.Poset, "client", "Done")
	if len(used) != 1 || len(fetchCalls) != 1 || len(fetchReturns) != 1 ||
		len(lookupReturns) != 1 || len(done) != 1 ||
		len(sourceNamedEvents(first.Poset, "client", "Pulse")) != 0 {
		t.Fatalf("active allocation Used/Call/FetchReturn/LookupReturn/Done=%d/%d/%d/%d/%d",
			len(used), len(fetchCalls), len(fetchReturns), len(lookupReturns), len(done))
	}
	value, exists := used[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || child.Identity() == "" {
		t.Fatalf("active allocation child=%#v", value)
	}
	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range first.Modules {
		if candidate.ModuleID == child.Identity() {
			lifecycle = candidate
			break
		}
	}
	start, startExists := first.Poset.Get(gorapide.EventID(lifecycle.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
		!startExists || !finishExists {
		t.Fatalf("active allocation lifecycle=%#v start=%v finish=%v",
			lifecycle, startExists, finishExists)
	}
	assertOnlyDirectCause(t, first.Poset, start, fetchCalls[0])
	assertOnlyDirectCause(t, first.Poset, used[0], start)
	assertOnlyDirectCause(t, first.Poset, finish, used[0])
	assertOnlyDirectCause(t, first.Poset, fetchReturns[0], used[0])
	if fetchReturns[0].ID != lookupReturns[0].ID ||
		!first.Poset.IsCausallyIndependent(finish.ID, fetchReturns[0].ID) ||
		!first.Poset.IsCausallyBefore(fetchReturns[0].ID, done[0].ID) {
		t.Fatal("active allocation return/finalization causality is incomplete")
	}
	localNames := 0
	for _, name := range lifecycle.Names {
		if name.Kind == "function-local" {
			localNames++
			if name.Live || len(name.LostAfter) != 1 || name.LostAfter[0] != string(used[0].ID) {
				t.Fatalf("active allocation function-local name=%#v", name)
			}
		}
	}
	if localNames != 1 {
		t.Fatalf("active allocation function-local names=%d lifecycle=%#v", localNames, lifecycle)
	}

	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed active-handler allocation artifact bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("active-handler allocation replay changed canonical bytes")
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
		t.Fatalf("active-handler allocation exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourceAllocatedInitializerInterruptsRemoteFunctionDeterministically(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Client is interface
  action in Trigger(); action in Pulse(); action out Recovered(); action out Done();
  requires Lookup : function();
end interface Client;
type Server is interface
  action out Born(); action out Relay(); action out Deferred();
  action out Closing(); action out Wrong();
  provides Fetch : function();
end interface Server;
module ClientModule() return Client is
serial when Trigger do
  do Lookup(); handler is Pulse => Recovered(); end do;
  Done();
end when;
end module ClientModule;
module ServerModule() return Server is
  C : Clock is MakeClock();
  Two : C.Ticks is 2;
  Fetch : function() is
    Child : Server is New(True);
  begin
    Wrong();
  end function Fetch;
connect
  (?Peer : Server) ?Peer.Born ||> Relay;
initial (Active : Boolean is False)
  if Active then
    Deferred() in Two;
    Born();
  end if;
final
  Closing();
end module ServerModule;
architecture System() is
  stimulus : Stimulus; client : Client is ClientModule(); server : Server is ServerModule();
connect stimulus.Trigger => client.Trigger;
        client.Lookup to server.Fetch;
        server.Relay ||> client.Pulse;
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

	var childLifecycle, serverLifecycle arch.ModuleLifecycleRecord
	for _, candidate := range first.Modules {
		if candidate.Occurrence == "component:server" {
			serverLifecycle = candidate
		}
		if candidate.Kind == "allocator-module" && strings.Contains(candidate.Occurrence, "|local=000000:child") {
			childLifecycle = candidate
		}
	}
	if childLifecycle.ModuleID == "" || serverLifecycle.ModuleID == "" {
		t.Fatalf("interrupted allocation lifecycles=%#v", first.Modules)
	}
	childID := childLifecycle.ModuleID
	start, startExists := first.Poset.Get(gorapide.EventID(childLifecycle.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(childLifecycle.FinishEventID))
	born := sourceNamedEvents(first.Poset, childID, "Born")
	relay := sourceNamedEvents(first.Poset, "server", "Relay")
	pulse := sourceNamedEvents(first.Poset, "client", "Pulse")
	closing := sourceNamedEvents(first.Poset, childID, "Closing")
	recovered := sourceNamedEvents(first.Poset, "client", "Recovered")
	done := sourceNamedEvents(first.Poset, "client", "Done")
	if !startExists || !finishExists || len(born) != 1 || len(relay) != 1 ||
		len(pulse) != 1 || len(closing) != 1 || len(recovered) != 1 || len(done) != 1 {
		t.Fatalf("interrupted Start/Finish/Born/Relay/Pulse/Closing/Recovered/Done=%v/%v/%d/%d/%d/%d/%d/%d",
			startExists, finishExists, len(born), len(relay), len(pulse), len(closing), len(recovered), len(done))
	}
	if len(sourceNamedEvents(first.Poset, childID, "Deferred")) != 0 ||
		len(sourceNamedEvents(first.Poset, "server", "Wrong")) != 0 ||
		len(distinctAllocatorEvents(sourceNamedEvents(first.Poset, "server", "Fetch'Return"))) != 0 ||
		len(distinctAllocatorEvents(sourceNamedEvents(first.Poset, "client", "Lookup'Return"))) != 0 {
		t.Fatal("interrupted allocator executed an abandoned action, schedule, or function return")
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, pulse[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], pulse[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], pulse[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) ||
		!first.Poset.IsCausallyIndependent(closing[0].ID, recovered[0].ID) ||
		!first.Poset.IsCausallyIndependent(finish.ID, done[0].ID) {
		t.Fatal("interrupted initializer finalization acquired a procedural handler edge")
	}
	if childLifecycle.State != arch.ModuleFinalizedState || childLifecycle.Namable ||
		childLifecycle.TerminationEventID != "" {
		t.Fatalf("interrupted allocation lifecycle=%#v", childLifecycle)
	}
	selfLoss, allocatorNames, localNames := false, 0, 0
	for _, name := range childLifecycle.Names {
		switch name.Kind {
		case "implicit-self":
			selfLoss = !name.Live && len(name.LostAfter) == 1 && name.LostAfter[0] == string(pulse[0].ID)
		case "allocator-result":
			allocatorNames++
		case "function-local":
			localNames++
		}
	}
	if !selfLoss || allocatorNames != 0 || localNames != 0 {
		t.Fatalf("interrupted unreturned-name graph self=%t allocator=%d local=%d names=%#v",
			selfLoss, allocatorNames, localNames, childLifecycle.Names)
	}
	contextClosed := false
	for _, context := range first.Contexts {
		if context.Kind == "initial-parent" && context.Source == childID &&
			context.Destination == serverLifecycle.ModuleID && !context.Live &&
			len(context.LostAfter) == 1 && context.LostAfter[0] == string(closing[0].ID) {
			contextClosed = true
		}
	}
	if !contextClosed {
		t.Fatalf("interrupted child Context did not remain live through final part: %#v", first.Contexts)
	}
	canceled := make([]string, 0)
	for _, firing := range first.Firings {
		canceled = append(canceled, firing.CanceledSchedules...)
	}
	if len(canceled) != 1 {
		t.Fatalf("interrupted initializer canceled schedules=%#v", canceled)
	}

	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed interrupted allocator artifact bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("interrupted allocator replay changed canonical bytes")
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
		t.Fatalf("interrupted allocator exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourceSuspendedRemoteFunctionRetainsAllocatedLocalUntilInterrupt(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Client is interface
  action in Trigger(); action in Pulse(value : Server);
  action out Recovered(value : Server); action out Done();
  requires Lookup : function();
end interface Client;
type Server is interface
  action out Signal(value : Server); action out Closing(); action out Wrong();
  provides Fetch : function();
end interface Server;
module ClientModule() return Client is
serial when Trigger do
  do Lookup(); handler is Pulse(?Caught) => Recovered(?Caught); end do;
  Done();
end when;
end module ClientModule;
module ServerModule() return Server is
  C : Clock is MakeClock();
  Fetch : function() is
    Child : Server is New();
  begin
    pause C.Ticks(1);
    Signal(Child);
    Wrong();
  end function Fetch;
final Closing();
end module ServerModule;
architecture System() is
  stimulus : Stimulus; client : Client is ClientModule(); server : Server is ServerModule();
connect stimulus.Trigger => client.Trigger;
        client.Lookup to server.Fetch;
        (?Child : Server) server.Signal(?Child) ||> client.Pulse(?Child);
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

	signal := sourceNamedEvents(first.Poset, "server", "Signal")
	pulse := sourceNamedEvents(first.Poset, "client", "Pulse")
	recovered := sourceNamedEvents(first.Poset, "client", "Recovered")
	done := sourceNamedEvents(first.Poset, "client", "Done")
	if len(signal) != 1 || len(pulse) != 1 || len(recovered) != 1 || len(done) != 1 ||
		len(sourceNamedEvents(first.Poset, "server", "Wrong")) != 0 ||
		len(distinctAllocatorEvents(sourceNamedEvents(first.Poset, "server", "Fetch'Return"))) != 0 ||
		len(distinctAllocatorEvents(sourceNamedEvents(first.Poset, "client", "Lookup'Return"))) != 0 {
		t.Fatalf("suspended allocation Signal/Pulse/Recovered/Done=%d/%d/%d/%d",
			len(signal), len(pulse), len(recovered), len(done))
	}
	signalValue, signalExists := signal[0].Param("value")
	recoveredValue, recoveredExists := recovered[0].Param("value")
	child, childOK := signalValue.(gorapide.RapideModuleValue)
	equal, equalErr := gorapide.CanonicalValuesEqual(signalValue, recoveredValue)
	if !signalExists || !recoveredExists || !childOK || child.Identity() == "" ||
		equalErr != nil || !equal {
		t.Fatalf("suspended allocation value transport signal=%#v recovered=%#v err=%v",
			signalValue, recoveredValue, equalErr)
	}
	var lifecycle arch.ModuleLifecycleRecord
	for _, candidate := range first.Modules {
		if candidate.ModuleID == child.Identity() {
			lifecycle = candidate
			break
		}
	}
	start, startExists := first.Poset.Get(gorapide.EventID(lifecycle.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	closing := sourceNamedEvents(first.Poset, child.Identity(), "Closing")
	if lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
		!startExists || !finishExists || len(closing) != 1 {
		t.Fatalf("suspended allocation lifecycle=%#v closing=%#v", lifecycle, closing)
	}
	assertOnlyDirectCause(t, first.Poset, signal[0], start)
	assertOnlyDirectCause(t, first.Poset, pulse[0], signal[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], pulse[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], pulse[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) ||
		!first.Poset.IsCausallyIndependent(closing[0].ID, recovered[0].ID) ||
		!first.Poset.IsCausallyIndependent(finish.ID, done[0].ID) {
		t.Fatal("suspended allocation unwind acquired a false handler edge")
	}
	localLostAtInterrupt := false
	for _, name := range lifecycle.Names {
		if name.Kind == "function-local" {
			localLostAtInterrupt = !name.Live && len(name.LostAfter) == 1 &&
				name.LostAfter[0] == string(pulse[0].ID)
		}
	}
	if !localLostAtInterrupt {
		t.Fatalf("suspended function-local name did not survive until interrupt: %#v", lifecycle.Names)
	}
	suspensions, switches := 0, 0
	for _, firing := range first.Firings {
		suspensions += len(firing.Suspensions)
		switches += len(firing.Switches)
	}
	if suspensions != 1 || switches != 0 {
		t.Fatalf("suspended interrupted function suspension/switch audit=%d/%d", suspensions, switches)
	}

	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed suspended allocation interrupt artifact bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("suspended allocation interrupt replay changed canonical bytes")
	}
}

func TestSourceActiveHandlerDirectAllocatorObservesNestedInitializerAtGenerationTime(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Factory is interface
  action in Trigger(); action in Pulse();
  action out Born(); action out Relay(); action out Allocated(value : Factory);
  action out Recovered(); action out Done(); action out Wrong(); action out Closing();
end interface Factory;
module FactoryModule() return Factory is
  ChildMode : var Boolean := False;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  ChildMode := Child;
  if Child then Born(); end if;
serial when Trigger do
  do
    Allocated(New(True));
    Wrong();
  handler is
    Pulse => Recovered();
  end do;
  Done();
end when;
final
  Closing();
end module FactoryModule;
architecture System() is
  stimulus : Stimulus; factory : Factory is FactoryModule();
connect stimulus.Trigger => factory.Trigger;
        factory.Relay ||> factory.Pulse;
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

	var childLifecycle arch.ModuleLifecycleRecord
	for _, candidate := range first.Modules {
		if candidate.Kind == "allocator-module" {
			if childLifecycle.ModuleID != "" {
				t.Fatalf("direct interrupt created multiple children: %#v", first.Modules)
			}
			childLifecycle = candidate
		}
	}
	if childLifecycle.ModuleID == "" {
		t.Fatalf("direct interrupt created no child: %#v", first.Modules)
	}
	childID := childLifecycle.ModuleID
	start, startExists := first.Poset.Get(gorapide.EventID(childLifecycle.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(childLifecycle.FinishEventID))
	born := sourceNamedEvents(first.Poset, childID, "Born")
	relay := sourceNamedEvents(first.Poset, "factory", "Relay")
	pulse := sourceNamedEvents(first.Poset, "factory", "Pulse")
	closing := sourceNamedEvents(first.Poset, childID, "Closing")
	recovered := sourceNamedEvents(first.Poset, "factory", "Recovered")
	done := sourceNamedEvents(first.Poset, "factory", "Done")
	if !startExists || !finishExists || len(born) != 1 || len(relay) != 1 ||
		len(pulse) != 1 || len(closing) != 1 || len(recovered) != 1 || len(done) != 1 {
		t.Fatalf("direct interrupt Start/Finish/Born/Relay/Pulse/Closing/Recovered/Done=%v/%v/%d/%d/%d/%d/%d/%d lifecycle=%#v",
			startExists, finishExists, len(born), len(relay), len(pulse), len(closing), len(recovered), len(done),
			childLifecycle)
	}
	if len(sourceNamedEvents(first.Poset, "factory", "Allocated")) != 0 ||
		len(sourceNamedEvents(first.Poset, "factory", "Wrong")) != 0 {
		t.Fatal("direct interrupt returned an allocator value or executed an abandoned statement")
	}
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, pulse[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], pulse[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], pulse[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) ||
		!first.Poset.IsCausallyIndependent(closing[0].ID, recovered[0].ID) ||
		!first.Poset.IsCausallyIndependent(finish.ID, done[0].ID) {
		t.Fatal("direct interrupt cleanup acquired a false procedural edge")
	}
	if childLifecycle.State != arch.ModuleFinalizedState || childLifecycle.Namable ||
		childLifecycle.TerminationEventID != "" {
		t.Fatalf("direct interrupted child lifecycle=%#v", childLifecycle)
	}
	selfLoss, allocatorNames := false, 0
	for _, name := range childLifecycle.Names {
		switch name.Kind {
		case "implicit-self":
			selfLoss = !name.Live && len(name.LostAfter) == 1 && name.LostAfter[0] == string(pulse[0].ID)
		case "allocator-result":
			allocatorNames++
		}
	}
	if !selfLoss || allocatorNames != 0 {
		t.Fatalf("direct interrupted child name graph self=%t allocator=%d names=%#v",
			selfLoss, allocatorNames, childLifecycle.Names)
	}
	for _, process := range first.Processes {
		if process.ComponentID == childID {
			t.Fatalf("direct interrupted initializer elaborated child process %#v", process)
		}
	}

	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed direct nested-allocation interrupt bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("direct nested-allocation interrupt replay changed canonical bytes")
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
		t.Fatalf("direct nested-allocation exploration changed: complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourceActiveHandlerDirectAllocatorElaboratesProcessAfterSuccessfulReturn(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Factory is interface
  action in Trigger(); action in Cancel();
  action out Ready(); action out Allocated(value : Factory);
  action out ChildDone(); action out Recovered(); action out Done(); action out Closing();
end interface Factory;
module FactoryModule() return Factory is
  ChildMode : var Boolean := False;
  C : Clock is Make_Clock();
initial (Child : Boolean is False)
  ChildMode := Child;
  if Child then Ready(); end if;
serial
  await Trigger where not $ChildMode =>
    do
      Allocated(New(True));
    handler is
      Cancel => Recovered();
    end do;
    Done();
  or Ready where $ChildMode =>
    pause C.Ticks(2);
    ChildDone();
  end await;
final
  Closing();
end module FactoryModule;
architecture System() is
  stimulus : Stimulus; factory : Factory is FactoryModule();
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
	journal := arch.NewExecutionJournal(digest, 120,
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
	done := sourceNamedEvents(first.Poset, "factory", "Done")
	if len(allocated) != 1 || len(done) != 1 ||
		len(sourceNamedEvents(first.Poset, "factory", "Recovered")) != 0 {
		t.Fatalf("successful direct allocation Allocated/Done/Recovered=%d/%d/%d",
			len(allocated), len(done), len(sourceNamedEvents(first.Poset, "factory", "Recovered")))
	}
	value, exists := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || child.Identity() == "" {
		t.Fatalf("successful direct allocation child=%#v", value)
	}
	childID := child.Identity()
	ready := sourceNamedEvents(first.Poset, childID, "Ready")
	childDone := sourceNamedEvents(first.Poset, childID, "ChildDone")
	closing := sourceNamedEvents(first.Poset, childID, "Closing")
	if len(ready) != 1 || len(childDone) != 1 || len(closing) != 1 {
		t.Fatalf("successful direct child Ready/ChildDone/Closing=%d/%d/%d",
			len(ready), len(childDone), len(closing))
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
		t.Fatalf("successful direct active child lifecycle=%#v", lifecycle)
	}
	finish, exists := first.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if !exists {
		t.Fatalf("successful direct active child Finish %q is absent", lifecycle.FinishEventID)
	}
	var childProcess *arch.ProcessExecutionRecord
	for index := range first.Processes {
		if first.Processes[index].ComponentID == childID {
			childProcess = &first.Processes[index]
			break
		}
	}
	if childProcess == nil || !childProcess.Terminated || childProcess.Completion != "normal" ||
		len(childProcess.Frontier) != 1 || childProcess.Frontier[0] != string(childDone[0].ID) {
		t.Fatalf("successful direct child process=%#v", childProcess)
	}
	if !first.Poset.IsCausallyIndependent(allocated[0].ID, childDone[0].ID) ||
		!first.Poset.IsCausallyBefore(allocated[0].ID, done[0].ID) {
		t.Fatal("successful direct allocation acquired incorrect process or caller causality")
	}
	closingCauses := first.Poset.DirectCauses(closing[0].ID)
	closingCauseIDs := make(map[gorapide.EventID]bool, len(closingCauses))
	for _, cause := range closingCauses {
		closingCauseIDs[cause.ID] = true
	}
	if len(closingCauses) != 2 || !closingCauseIDs[allocated[0].ID] ||
		!closingCauseIDs[childDone[0].ID] {
		t.Fatalf("successful direct finalization conjunction=%#v", closingCauses)
	}
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	clockID := arch.ClockID(childID, "C")
	if len(first.ClockAdvances) != 1 || first.ClockAdvances[0].Clock != clockID ||
		first.ClockAdvances[0].From != "0" || first.ClockAdvances[0].To != "2" {
		t.Fatalf("successful direct child clock=%#v", first.ClockAdvances)
	}

	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed successful direct active-allocation bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("successful direct active-allocation replay changed canonical bytes")
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
	if !explored.Complete || len(explored.Computations) != 1 {
		t.Fatalf("successful direct active-allocation exploration complete=%v computations=%d",
			explored.Complete, len(explored.Computations))
	}
}

func TestSourceActiveHandlerLocalFunctionAllocatorObservesNestedInitializerAtGenerationTime(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Factory is interface
  action in Trigger(); action in Pulse();
  action out Born(); action out Relay(); action out Recovered();
  action out Done(); action out Wrong(); action out Closing();
  provides Lookup : function();
end interface Factory;
module FactoryModule() return Factory is
  ChildMode : var Boolean := False;
  Lookup : function() is
    Child : Factory is New(True);
  begin
    Wrong();
  end function Lookup;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (Child : Boolean is False)
  ChildMode := Child;
  if Child then Born(); end if;
serial when Trigger where not $ChildMode do
  do
    Lookup();
  handler is
    Pulse => Recovered();
  end do;
  Done();
end when;
final
  Closing();
end module FactoryModule;
architecture System() is
  stimulus : Stimulus; factory : Factory is FactoryModule();
connect stimulus.Trigger => factory.Trigger;
        factory.Relay ||> factory.Pulse;
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

	var childLifecycle arch.ModuleLifecycleRecord
	for _, candidate := range first.Modules {
		if candidate.Kind == "allocator-module" {
			if childLifecycle.ModuleID != "" {
				t.Fatalf("local-function interrupt created multiple children: %#v", first.Modules)
			}
			childLifecycle = candidate
		}
	}
	if childLifecycle.ModuleID == "" {
		t.Fatalf("local-function interrupt created no child: %#v", first.Modules)
	}
	childID := childLifecycle.ModuleID
	start, startExists := first.Poset.Get(gorapide.EventID(childLifecycle.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(childLifecycle.FinishEventID))
	call := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "factory", "Lookup'Call"))
	returns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "factory", "Lookup'Return"))
	born := sourceNamedEvents(first.Poset, childID, "Born")
	relay := sourceNamedEvents(first.Poset, "factory", "Relay")
	pulse := sourceNamedEvents(first.Poset, "factory", "Pulse")
	closing := sourceNamedEvents(first.Poset, childID, "Closing")
	recovered := sourceNamedEvents(first.Poset, "factory", "Recovered")
	done := sourceNamedEvents(first.Poset, "factory", "Done")
	if !startExists || !finishExists || len(call) != 1 || len(returns) != 0 ||
		len(born) != 1 || len(relay) != 1 || len(pulse) != 1 || len(closing) != 1 ||
		len(recovered) != 1 || len(done) != 1 {
		t.Fatalf("local-function interrupt Start/Finish/Call/Return/Born/Relay/Pulse/Closing/Recovered/Done=%v/%v/%d/%d/%d/%d/%d/%d/%d/%d",
			startExists, finishExists, len(call), len(returns), len(born), len(relay), len(pulse), len(closing), len(recovered), len(done))
	}
	if len(sourceNamedEvents(first.Poset, "factory", "Wrong")) != 0 {
		t.Fatal("local-function interrupt executed an abandoned body statement")
	}
	assertOnlyDirectCause(t, first.Poset, start, call[0])
	assertOnlyDirectCause(t, first.Poset, born[0], start)
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, pulse[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], pulse[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], pulse[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) ||
		!first.Poset.IsCausallyIndependent(closing[0].ID, recovered[0].ID) {
		t.Fatal("local-function interrupt acquired a false unwind edge")
	}
	if childLifecycle.State != arch.ModuleFinalizedState || childLifecycle.Namable ||
		childLifecycle.TerminationEventID != "" {
		t.Fatalf("local-function interrupted child lifecycle=%#v", childLifecycle)
	}
	allocatorNames, localNames := 0, 0
	for _, name := range childLifecycle.Names {
		switch name.Kind {
		case "allocator-result":
			allocatorNames++
		case "function-local":
			localNames++
		}
	}
	if allocatorNames != 0 || localNames != 0 {
		t.Fatalf("local-function interrupt fabricated returned names: %#v", childLifecycle.Names)
	}
	for _, process := range first.Processes {
		if process.ComponentID == childID {
			t.Fatalf("local-function interrupted initializer elaborated child process %#v", process)
		}
	}

	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed local-function nested-allocation interrupt bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("local-function nested-allocation interrupt replay changed canonical bytes")
	}
}

func TestSourceDynamicProcessActiveHandlerInterruptsGrandchildInitialization(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Factory is interface
  action in Trigger();
  action out Pulse(); action out Ready(depth : Integer); action out Born(); action out Relay();
  action out Allocated(value : Factory; depth : Integer);
  action out Recovered(depth : Integer); action out Done(depth : Integer);
  action out Wrong(); action out Closing();
end interface Factory;
module FactoryModule() return Factory is
  Depth : var Integer := 0;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
  Relay ||> Pulse;
initial (InitialDepth : Integer is 0)
  Depth := InitialDepth;
  if InitialDepth > 0 then Ready(InitialDepth); end if;
  if InitialDepth = 2 then Born(); end if;
serial
  await Trigger where $Depth = 0 =>
    Allocated(New(1), 1);
  or (?D : Integer) Ready(?D) where $Depth = 1 =>
    do
      Allocated(New(2), 2);
      Wrong();
    handler is
      Pulse => Recovered(?D);
    end do;
    Done(?D);
  or (?D : Integer) Ready(?D) where $Depth = 2 =>
    Wrong();
  end await;
final
  Closing();
end module FactoryModule;
architecture System() is
  stimulus : Stimulus; factory : Factory is FactoryModule();
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
		digest, arch.ExecutionLimits{MaxFirings: 220, MaxStatements: 300},
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

	allocated := distinctAllocatorEvents(first.Poset.ByName("Allocated"))
	if len(allocated) != 1 || allocated[0].ParamInt("depth") != 1 {
		t.Fatalf("dynamic handler returned allocations=%#v", allocated)
	}
	value, exists := allocated[0].Param("value")
	firstChild, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || firstChild.Identity() == "" {
		t.Fatalf("dynamic handler first child=%#v", value)
	}
	firstChildID := firstChild.Identity()
	var grandchildLifecycle *arch.ModuleLifecycleRecord
	for index := range first.Modules {
		candidate := &first.Modules[index]
		if candidate.Kind == "allocator-module" && candidate.ModuleID != firstChildID {
			if grandchildLifecycle != nil {
				t.Fatalf("dynamic handler created multiple grandchildren: %#v", first.Modules)
			}
			grandchildLifecycle = candidate
		}
	}
	if grandchildLifecycle == nil {
		t.Fatalf("dynamic handler created no grandchild: %#v", first.Modules)
	}
	grandchildID := grandchildLifecycle.ModuleID
	start, startExists := first.Poset.Get(gorapide.EventID(grandchildLifecycle.StartEventID))
	finish, finishExists := first.Poset.Get(gorapide.EventID(grandchildLifecycle.FinishEventID))
	ready := sourceNamedEvents(first.Poset, grandchildID, "Ready")
	born := sourceNamedEvents(first.Poset, grandchildID, "Born")
	relay := sourceNamedEvents(first.Poset, firstChildID, "Relay")
	pulse := sourceNamedEvents(first.Poset, firstChildID, "Pulse")
	recovered := sourceNamedEvents(first.Poset, firstChildID, "Recovered")
	done := sourceNamedEvents(first.Poset, firstChildID, "Done")
	closing := sourceNamedEvents(first.Poset, grandchildID, "Closing")
	if !startExists || !finishExists || len(ready) != 1 || len(born) != 1 || len(relay) != 1 ||
		len(pulse) != 1 || len(recovered) != 1 || len(done) != 1 || len(closing) != 1 {
		t.Fatalf("dynamic handler Start/Finish/Ready/Born/Relay/Pulse/Recovered/Done/Closing=%v/%v/%d/%d/%d/%d/%d/%d/%d",
			startExists, finishExists, len(ready), len(born), len(relay), len(pulse), len(recovered), len(done), len(closing))
	}
	if recovered[0].ParamInt("depth") != 1 || done[0].ParamInt("depth") != 1 ||
		len(first.Poset.ByName("Wrong")) != 0 {
		t.Fatalf("dynamic handler recovery values/wrong=%d/%d/%d",
			recovered[0].ParamInt("depth"), done[0].ParamInt("depth"), len(first.Poset.ByName("Wrong")))
	}
	assertOnlyDirectCause(t, first.Poset, ready[0], start)
	assertOnlyDirectCause(t, first.Poset, born[0], ready[0])
	assertOnlyDirectCause(t, first.Poset, relay[0], born[0])
	assertOnlyDirectCause(t, first.Poset, pulse[0], relay[0])
	assertOnlyDirectCause(t, first.Poset, closing[0], pulse[0])
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	assertOnlyDirectCause(t, first.Poset, recovered[0], pulse[0])
	if !first.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) ||
		!first.Poset.IsCausallyIndependent(closing[0].ID, recovered[0].ID) {
		t.Fatal("dynamic handler grandchild cleanup acquired a false recovery edge")
	}
	if grandchildLifecycle.State != arch.ModuleFinalizedState || grandchildLifecycle.Namable ||
		grandchildLifecycle.TerminationEventID != "" {
		t.Fatalf("dynamic handler grandchild lifecycle=%#v", grandchildLifecycle)
	}
	for _, process := range first.Processes {
		if process.ComponentID == grandchildID {
			t.Fatalf("dynamic handler elaborated interrupted grandchild process %#v", process)
		}
	}
	firstChildProcess := false
	for _, process := range first.Processes {
		if process.ComponentID == firstChildID && process.Terminated && process.Completion == "normal" {
			firstChildProcess = true
		}
	}
	if !firstChildProcess {
		t.Fatalf("dynamic handler first-child process did not complete: %#v", first.Processes)
	}

	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed dynamic-handler grandchild interrupt bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("dynamic-handler grandchild interrupt replay changed canonical bytes")
	}
}

func TestSourceActiveHandlerLaterAllocatorInterruptReleasesEarlierActualExactly(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Factory is interface
  action in Trigger(); action in Pulse();
  action out Ready(depth : Integer); action out Born(); action out Relay();
  action out Pair(left : Factory; right : Factory);
  action out Recovered(); action out Done(depth : Integer);
  action out Wrong(); action out Closing();
end interface Factory;
module FactoryModule() return Factory is
  Depth : var Integer := 0;
connect
  (?Peer : Factory) ?Peer.Born ||> Relay;
initial (InitialDepth : Integer is 0)
  Depth := InitialDepth;
  if InitialDepth > 0 then Ready(InitialDepth); end if;
  if InitialDepth = 2 then Born(); end if;
serial
  await Trigger where $Depth = 0 =>
    do
      Pair(New(1), New(2));
      Wrong();
    handler is
      Pulse => Recovered();
    end do;
  or (?D : Integer) Ready(?D) where $Depth = 1 =>
    Done(?D);
  or (?D : Integer) Ready(?D) where $Depth = 2 =>
    Wrong();
  end await;
final
  Closing();
end module FactoryModule;
architecture System() is
  stimulus : Stimulus; factory : Factory is FactoryModule();
connect stimulus.Trigger => factory.Trigger;
        factory.Relay ||> factory.Pulse;
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
		digest, arch.ExecutionLimits{MaxFirings: 220, MaxStatements: 300},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Pair")) != 0 || len(result.Poset.ByName("Wrong")) != 0 ||
		len(sourceNamedEvents(result.Poset, "factory", "Recovered")) != 1 {
		t.Fatalf("later interrupt Pair/Wrong/Recovered=%d/%d/%d",
			len(result.Poset.ByName("Pair")), len(result.Poset.ByName("Wrong")),
			len(sourceNamedEvents(result.Poset, "factory", "Recovered")))
	}
	children := make(map[int]string, 2)
	for _, event := range result.Poset.ByName("Ready") {
		if event.Source != "factory" {
			children[event.ParamInt("depth")] = event.Source
		}
	}
	firstChildID, secondChildID := children[1], children[2]
	if firstChildID == "" || secondChildID == "" || firstChildID == secondChildID {
		t.Fatalf("later interrupt child identities=%#v", children)
	}
	pulse := sourceNamedEvents(result.Poset, "factory", "Pulse")
	firstDone := sourceNamedEvents(result.Poset, firstChildID, "Done")
	firstClosing := sourceNamedEvents(result.Poset, firstChildID, "Closing")
	secondClosing := sourceNamedEvents(result.Poset, secondChildID, "Closing")
	if len(pulse) != 1 || len(firstDone) != 1 || firstDone[0].ParamInt("depth") != 1 ||
		len(firstClosing) != 1 || len(secondClosing) != 1 {
		t.Fatalf("later interrupt Pulse/FirstDone/FirstClosing/SecondClosing=%d/%d/%d/%d",
			len(pulse), len(firstDone), len(firstClosing), len(secondClosing))
	}
	if !result.Poset.IsCausallyIndependent(pulse[0].ID, firstDone[0].ID) {
		t.Fatal("later interrupt serialized earlier child completion with parameter evaluation")
	}
	firstCauses := result.Poset.DirectCauses(firstClosing[0].ID)
	firstCauseIDs := make(map[gorapide.EventID]bool, len(firstCauses))
	for _, cause := range firstCauses {
		firstCauseIDs[cause.ID] = true
	}
	if len(firstCauses) != 2 || !firstCauseIDs[pulse[0].ID] || !firstCauseIDs[firstDone[0].ID] {
		t.Fatalf("earlier actual finalization lacks interrupt/process conjunction: %#v", firstCauses)
	}
	assertOnlyDirectCause(t, result.Poset, secondClosing[0], pulse[0])
	processes := make(map[string]*arch.ProcessExecutionRecord)
	for index := range result.Processes {
		process := &result.Processes[index]
		if process.ComponentID == firstChildID || process.ComponentID == secondChildID {
			processes[process.ComponentID] = process
		}
	}
	if processes[firstChildID] == nil || !processes[firstChildID].Terminated ||
		processes[firstChildID].Completion != "normal" || processes[secondChildID] != nil {
		t.Fatalf("later interrupt child processes=%#v", processes)
	}
	for _, childID := range []string{firstChildID, secondChildID} {
		var lifecycle *arch.ModuleLifecycleRecord
		for index := range result.Modules {
			if result.Modules[index].ModuleID == childID {
				lifecycle = &result.Modules[index]
				break
			}
		}
		if lifecycle == nil || lifecycle.State != arch.ModuleFinalizedState || lifecycle.Namable ||
			lifecycle.FinishEventID == "" {
			t.Fatalf("later interrupt child %s lifecycle=%#v", childID, lifecycle)
		}
	}

	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	resultBytes, _ := result.MarshalCanonical()
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(resultBytes, replayedBytes) {
		t.Fatal("later allocator interrupt replay changed canonical bytes")
	}
}

func TestSourceActiveHandlerDirectAllocatorRemainsActiveForDeferredChildAction(t *testing.T) {
	source := []byte(`
type Stimulus is interface action out Trigger(); end interface Stimulus;
type Factory is interface
  action in Trigger(); action in Pulse();
  action out Ready(); action out Signal(); action out Relay();
  action out Allocated(value : Factory); action out ChildDone();
  action out Recovered(); action out Done(); action out Wrong(); action out Closing();
end interface Factory;
module FactoryModule() return Factory is
  ChildMode : var Boolean := False;
  C : Clock is Make_Clock();
  Two : C.Ticks is 2;
  Three : C.Ticks is 3;
connect
  (?Peer : Factory) ?Peer.Signal ||> Relay;
initial (Child : Boolean is False)
  ChildMode := Child;
  if Child then
    Signal() in Two;
    Ready();
  end if;
serial
  await Trigger where not $ChildMode =>
    do
      Allocated(New(True));
      pause Three;
      Wrong();
    handler is
      Pulse => Recovered();
    end do;
    Done();
  or Ready where $ChildMode =>
    ChildDone();
  end await;
final
  Closing();
end module FactoryModule;
architecture System() is
  stimulus : Stimulus; factory : Factory is FactoryModule();
connect stimulus.Trigger => factory.Trigger;
        factory.Relay ||> factory.Pulse;
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
		digest, arch.ExecutionLimits{MaxFirings: 220, MaxStatements: 300},
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	)
	baseline, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	baselineAllocated := distinctAllocatorEvents(sourceNamedEvents(baseline.Poset, "factory", "Allocated"))
	if len(baselineAllocated) != 1 {
		t.Fatalf("deferred direct baseline allocation events=%#v", baselineAllocated)
	}
	baselineValue, exists := baselineAllocated[0].Param("value")
	baselineChild, ok := baselineValue.(gorapide.RapideModuleValue)
	if !exists || !ok || baselineChild.Identity() == "" {
		t.Fatalf("deferred direct baseline child=%#v", baselineValue)
	}
	childID := baselineChild.Identity()
	childClockChoice := ""
	childClockPoint := ""
	for _, choice := range baseline.Choices {
		if choice.Domain != "clock-advance" {
			continue
		}
		for _, option := range choice.Options {
			if option == string(arch.ClockID(childID, "C"))+"@2" {
				childClockPoint = choice.Point
				childClockChoice = option
			}
		}
	}
	if childClockPoint == "" || childClockChoice == "" {
		t.Fatalf("deferred direct child clock choice absent: %#v", baseline.Choices)
	}
	journal.Choices = []arch.ChoiceDecision{{Point: childClockPoint, Selection: childClockChoice}}
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
		t.Fatalf("deferred direct allocation events=%#v", allocated)
	}
	value, exists := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || child.Identity() == "" {
		t.Fatalf("deferred direct allocation child=%#v", value)
	}
	if child.Identity() != childID {
		t.Fatalf("deferred direct scheduled child=%q, baseline=%q", child.Identity(), childID)
	}
	ready := sourceNamedEvents(first.Poset, childID, "Ready")
	childDone := sourceNamedEvents(first.Poset, childID, "ChildDone")
	signal := sourceNamedEvents(first.Poset, childID, "Signal")
	relay := sourceNamedEvents(first.Poset, "factory", "Relay")
	pulse := sourceNamedEvents(first.Poset, "factory", "Pulse")
	recovered := sourceNamedEvents(first.Poset, "factory", "Recovered")
	done := sourceNamedEvents(first.Poset, "factory", "Done")
	closing := sourceNamedEvents(first.Poset, childID, "Closing")
	if len(ready) != 1 || len(childDone) != 1 || len(signal) != 1 || len(relay) != 1 ||
		len(pulse) != 1 || len(recovered) != 1 || len(done) != 1 || len(closing) != 1 {
		t.Fatalf("deferred direct Ready/ChildDone/Signal/Relay/Pulse/Recovered/Done/Closing=%d/%d/%d/%d/%d/%d/%d/%d",
			len(ready), len(childDone), len(signal), len(relay), len(pulse), len(recovered), len(done), len(closing))
	}
	if len(first.Poset.ByName("Wrong")) != 0 {
		t.Fatal("deferred child interrupt executed the abandoned protected remainder")
	}
	assertOnlyDirectCause(t, first.Poset, relay[0], signal[0])
	assertOnlyDirectCause(t, first.Poset, pulse[0], relay[0])
	recoveryCauses := first.Poset.DirectCauses(recovered[0].ID)
	recoveryCauseIDs := make(map[gorapide.EventID]bool, len(recoveryCauses))
	for _, cause := range recoveryCauses {
		recoveryCauseIDs[cause.ID] = true
	}
	if len(recoveryCauses) != 2 || !recoveryCauseIDs[allocated[0].ID] || !recoveryCauseIDs[pulse[0].ID] {
		t.Fatalf("deferred direct recovery conjunction=%#v", recoveryCauses)
	}
	if !first.Poset.IsCausallyBefore(recovered[0].ID, done[0].ID) ||
		!first.Poset.IsCausallyIndependent(closing[0].ID, recovered[0].ID) {
		t.Fatal("deferred direct allocation acquired a false finalization/recovery edge")
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
		t.Fatalf("deferred direct child lifecycle=%#v", lifecycle)
	}
	finish, exists := first.Poset.Get(gorapide.EventID(lifecycle.FinishEventID))
	if !exists {
		t.Fatalf("deferred direct child Finish %q is absent", lifecycle.FinishEventID)
	}
	assertOnlyDirectCause(t, first.Poset, finish, closing[0])
	closingCauses := first.Poset.DirectCauses(closing[0].ID)
	closingCauseIDs := make(map[gorapide.EventID]bool, len(closingCauses))
	for _, cause := range closingCauses {
		closingCauseIDs[cause.ID] = true
	}
	if len(closingCauses) != 3 || !closingCauseIDs[allocated[0].ID] ||
		!closingCauseIDs[childDone[0].ID] || !closingCauseIDs[signal[0].ID] {
		labels := make([]string, 0, len(closingCauses))
		for _, cause := range closingCauses {
			labels = append(labels, fmt.Sprintf("%s=%#v", cause.ID, cause.Observations))
		}
		t.Fatalf("deferred direct finalization conjunction=%v", labels)
	}
	canceledSuspensions := 0
	for _, process := range first.Processes {
		if process.ComponentID == "factory" {
			canceledSuspensions += len(process.CanceledSuspensions)
		}
	}
	if canceledSuspensions != 1 {
		t.Fatalf("deferred direct canceled parent suspensions=%d processes=%#v", canceledSuspensions, first.Processes)
	}
	childClock := arch.ClockID(childID, "C")
	childAdvanced := false
	for _, advance := range first.ClockAdvances {
		if advance.Clock == childClock && advance.From == "0" && advance.To == "2" {
			childAdvanced = true
		}
	}
	if !childAdvanced {
		t.Fatalf("deferred direct child clock did not advance to signal: %#v", first.ClockAdvances)
	}

	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed deferred direct-allocation interrupt bytes")
	}
	expected, _ := first.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("deferred direct-allocation interrupt replay changed canonical bytes")
	}
}
