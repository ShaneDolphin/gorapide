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

func TestSourceFunctionRaiseTransfersImmediatelyToProcessHandler(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger(value : Integer);
  action out FunctionContinued(value : Integer);
  action out OuterContinued(value : Integer);
	  action out Recovered(available : Integer);
	  action out GenericRecovery();
  provides Initialize : function(limit : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;

module WorkerModule() return Worker is
	  exception Capacity(available : Integer);
	  exception Other;
  Initialize : function(limit : Integer) is
	  begin
	    raise Other where limit < 0;
	    raise Capacity(7) where limit > 7;
    FunctionContinued(limit);
  end function Initialize;
serial
  when (?Value : Integer) Trigger(?Value) do
    Initialize(?Value);
    OuterContinued(?Value);
  handler
	    is Capacity(?Available) =>
	      Recovered(?Available);
	    else
	      GenericRecovery();
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
	journal := arch.NewExecutionJournal(digest, 60,
		arch.InputEvent{Key: "normal", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": 5}},
		arch.InputEvent{Key: "raised", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": 10}, Causes: []string{"normal"}},
		arch.InputEvent{Key: "else", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": -1}, Causes: []string{"raised"}},
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

	continuedFunction := sourceNamedEvents(result.Poset, "worker", "FunctionContinued")
	continuedOuter := sourceNamedEvents(result.Poset, "worker", "OuterContinued")
	raised := sourceNamedEvents(result.Poset, "worker", "Capacity")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	other := sourceNamedEvents(result.Poset, "worker", "Other")
	generic := sourceNamedEvents(result.Poset, "worker", "GenericRecovery")
	calls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Initialize'Call"))
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Initialize'Return"))
	if len(continuedFunction) != 1 || len(continuedOuter) != 1 || len(raised) != 1 ||
		len(recovered) != 1 || len(other) != 1 || len(generic) != 1 || len(calls) != 3 || len(returns) != 1 {
		t.Fatalf("FunctionContinued/OuterContinued/Capacity/Recovered/Other/Generic/Call/Return=%d/%d/%d/%d/%d/%d/%d/%d",
			len(continuedFunction), len(continuedOuter), len(raised), len(recovered), len(other), len(generic), len(calls), len(returns))
	}
	for _, event := range []*gorapide.Event{continuedFunction[0], continuedOuter[0]} {
		value, _ := event.Param("value")
		if value != int64(5) {
			t.Fatalf("normal continuation value=%#v", value)
		}
	}
	if available, _ := raised[0].Param("available"); available != int64(7) {
		t.Fatalf("raised exception available=%#v", available)
	}
	if available, _ := recovered[0].Param("available"); available != int64(7) {
		t.Fatalf("handler binding available=%#v", available)
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], raised[0])
	assertOnlyDirectCause(t, result.Poset, generic[0], other[0])
	if !result.Poset.IsCausallyBefore(raised[0].ID, recovered[0].ID) {
		t.Fatal("handler action does not follow the exception event")
	}
	for _, returned := range returns {
		if result.Poset.IsCausallyBefore(raised[0].ID, returned.ID) {
			t.Fatal("raising function generated a Return occurrence")
		}
	}
	exceptionAudited := false
	for _, firing := range result.Firings {
		for _, generated := range firing.Generated {
			if generated.EventID == string(raised[0].ID) && generated.Exception {
				exceptionAudited = true
			}
		}
	}
	if !exceptionAudited {
		t.Fatal("raised event is not explicitly classified as an exception in firing audit")
	}

	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed raise/handler execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("raise/handler replay changed canonical artifact bytes")
	}
}

func TestSourceDoHandlerResumesAfterBlockAndReraisesOutward(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger(value : Integer);
  action out BodyContinued(value : Integer);
  action out LocalRecovered(value : Integer);
  action out WrongSameHandler(value : Integer);
  action out AfterDo(value : Integer);
  action out OuterRecovered(value : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;

module WorkerModule() return Worker is
  exception Inner(value : Integer);
  exception Outer(value : Integer);
  exception Escaped(value : Integer);
serial
  when (?Value : Integer) Trigger(?Value) do
    do
      raise Inner(?Value) where ?Value = 1 or ?Value = 4;
      raise Escaped(?Value) where ?Value = 2;
      BodyContinued(?Value);
    handler
      is Inner(?Caught) =>
        raise Outer(?Caught) where ?Caught = 4;
        LocalRecovered(?Caught);
      is Outer(?Caught) =>
        WrongSameHandler(?Caught);
    end do;
    AfterDo(?Value);
  handler
    is Escaped(?Caught) => OuterRecovered(?Caught);
    is Outer(?Caught) => OuterRecovered(?Caught);
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
	journal := arch.NewExecutionJournal(digest, 80,
		arch.InputEvent{Key: "local", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": 1}},
		arch.InputEvent{Key: "unhandled", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": 2}, Causes: []string{"local"}},
		arch.InputEvent{Key: "normal", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": 3}, Causes: []string{"unhandled"}},
		arch.InputEvent{Key: "handler-raise", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": 4}, Causes: []string{"normal"}},
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

	eventsByInteger := func(action, formal string) map[int64]*gorapide.Event {
		t.Helper()
		byValue := make(map[int64]*gorapide.Event)
		for _, event := range sourceNamedEvents(result.Poset, "worker", action) {
			value, _ := event.Param(formal)
			integer, ok := value.(int64)
			if !ok {
				t.Fatalf("%s.%s=%#v", action, formal, value)
			}
			byValue[integer] = event
		}
		return byValue
	}
	inner := eventsByInteger("Inner", "value")
	outer := eventsByInteger("Outer", "value")
	escaped := eventsByInteger("Escaped", "value")
	local := eventsByInteger("LocalRecovered", "value")
	body := eventsByInteger("BodyContinued", "value")
	after := eventsByInteger("AfterDo", "value")
	outerRecovered := eventsByInteger("OuterRecovered", "value")
	wrong := eventsByInteger("WrongSameHandler", "value")
	if len(inner) != 2 || len(outer) != 1 || len(escaped) != 1 || len(local) != 1 ||
		len(body) != 1 || len(after) != 2 || len(outerRecovered) != 2 || len(wrong) != 0 {
		t.Fatalf("Inner/Outer/Escaped/Local/Body/After/OuterRecovery/Wrong=%d/%d/%d/%d/%d/%d/%d/%d body=%v after=%v",
			len(inner), len(outer), len(escaped), len(local), len(body), len(after), len(outerRecovered), len(wrong), body, after)
	}
	assertOnlyDirectCause(t, result.Poset, local[1], inner[1])
	assertOnlyDirectCause(t, result.Poset, outerRecovered[2], escaped[2])
	assertOnlyDirectCause(t, result.Poset, outerRecovered[4], outer[4])
	if !result.Poset.IsCausallyBefore(local[1].ID, after[1].ID) {
		t.Fatal("handled do block did not continue after its selected handler")
	}
	if _, exists := after[2]; exists {
		t.Fatal("unhandled do exception executed the statement following the do block")
	}
	if _, exists := after[4]; exists {
		t.Fatal("handler-body raise executed the statement following the do block")
	}
	if _, exists := body[1]; exists {
		t.Fatal("handled exception did not abandon the protected do body")
	}

	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed nested do-handler execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("nested do-handler replay changed canonical artifact bytes")
	}
}

func TestSourceFunctionDoHandlerRecoversAndReturnsNormally(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger(value : Integer);
  action out FunctionRecovered(value : Integer);
  action out FunctionContinued(value : Integer);
  action out CallerContinued(value : Integer);
  provides Work : function(value : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;

module WorkerModule() return Worker is
  exception Failure(value : Integer);
  Work : function(value : Integer) is
  begin
    do
      raise Failure(value);
    handler
      is Failure(?Caught) => FunctionRecovered(?Caught);
    end do;
    FunctionContinued(value);
  end function Work;
serial
  when (?Value : Integer) Trigger(?Value) do
    Work(?Value);
    CallerContinued(?Value);
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
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": 7}},
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
	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	recovered := sourceNamedEvents(result.Poset, "worker", "FunctionRecovered")
	continued := sourceNamedEvents(result.Poset, "worker", "FunctionContinued")
	caller := sourceNamedEvents(result.Poset, "worker", "CallerContinued")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Return"))
	if len(failure) != 1 || len(recovered) != 1 || len(continued) != 1 || len(caller) != 1 || len(returns) != 1 {
		t.Fatalf("Failure/Recovered/FunctionContinued/CallerContinued/Return=%d/%d/%d/%d/%d",
			len(failure), len(recovered), len(continued), len(caller), len(returns))
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], failure[0])
	if !result.Poset.IsCausallyBefore(recovered[0].ID, continued[0].ID) ||
		!result.Poset.IsCausallyBefore(continued[0].ID, returns[0].ID) ||
		!result.Poset.IsCausallyBefore(returns[0].ID, caller[0].ID) {
		t.Fatal("function-local do handler did not resume through Return and caller continuation")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed function-local do-handler execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("function-local do-handler replay changed canonical artifact bytes")
	}
}

func TestSourceDirectFunctionHandlerRecoversAndReturnsTailValue(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Before();
  action out ProtectedContinued();
  action out Recovered();
  action out Returned(value : Integer);
  provides Work : function() return Integer;
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module WorkerModule() return Worker is
  exception Failure;
  result : var Integer := 0;
  Work : function() return Integer is
  begin
    Before();
    raise Failure;
    ProtectedContinued();
    return 42;
  handler
    is Failure => Recovered();
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
	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Return"))
	returned := sourceNamedEvents(result.Poset, "worker", "Returned")
	if len(failure) != 1 || len(recovered) != 1 || len(returns) != 1 || len(returned) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "ProtectedContinued")) != 0 {
		t.Fatalf("Failure/Recovered/Return/Returned=%d/%d/%d/%d",
			len(failure), len(recovered), len(returns), len(returned))
	}
	value, _ := returned[0].Param("value")
	if value != int64(42) {
		t.Fatalf("direct function handler tail result=%#v", value)
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], failure[0])
	if !result.Poset.IsCausallyBefore(recovered[0].ID, returns[0].ID) ||
		!result.Poset.IsCausallyBefore(returns[0].ID, returned[0].ID) {
		t.Fatal("direct function handler did not resume through tail return and caller")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed direct function-handler recovery")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("direct function-handler replay changed canonical bytes")
	}
}

func TestSourceDirectFunctionHandlerCatchesImmediateActionInterrupt(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Pulse();
  action out ProtectedContinued();
  action out Recovered();
  action out CallerContinued();
  provides Work : function();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  Work : function() is
  begin
    Pulse();
    ProtectedContinued();
  handler
    is Pulse => Recovered();
  end function Work;
serial when Trigger do
  Work();
  CallerContinued();
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	pulse := sourceNamedEvents(result.Poset, "worker", "Pulse")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	caller := sourceNamedEvents(result.Poset, "worker", "CallerContinued")
	if len(pulse) != 1 || len(recovered) != 1 || len(caller) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "ProtectedContinued")) != 0 {
		t.Fatalf("Pulse/Recovered/CallerContinued=%d/%d/%d", len(pulse), len(recovered), len(caller))
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], pulse[0])
	if !result.Poset.IsCausallyBefore(recovered[0].ID, caller[0].ID) {
		t.Fatal("direct function interrupt did not resume its caller")
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("direct function action interrupt entered exception propagation: %#v", result.ExceptionPropagations)
	}
}

func TestSourceDirectFunctionHandlerCatchesNestedCallException(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out HelperContinued();
  action out WorkContinued();
  action out Recovered();
  action out CallerContinued();
  provides Helper : function();
  provides Work : function();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  Helper : function() is
  begin
    raise Failure;
    HelperContinued();
  end function Helper;
  Work : function() is
  begin
    Helper();
    WorkContinued();
  handler
    is Failure => Recovered();
  end function Work;
serial when Trigger do
  Work();
  CallerContinued();
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 40,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	workReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Return"))
	helperReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Helper'Return"))
	caller := sourceNamedEvents(result.Poset, "worker", "CallerContinued")
	if len(failure) != 1 || len(recovered) != 1 || len(workReturns) != 1 || len(helperReturns) != 0 || len(caller) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "HelperContinued")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "WorkContinued")) != 0 {
		t.Fatalf("Failure/Recovered/WorkReturn/HelperReturn/Caller=%d/%d/%d/%d/%d",
			len(failure), len(recovered), len(workReturns), len(helperReturns), len(caller))
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], failure[0])
	if !result.Poset.IsCausallyBefore(recovered[0].ID, workReturns[0].ID) ||
		!result.Poset.IsCausallyBefore(workReturns[0].ID, caller[0].ID) {
		t.Fatal("nested-call exception did not resume through caller function recovery")
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("nested-call exception escaped its direct function handler: %#v", result.ExceptionPropagations)
	}
}

func TestSourceDirectFunctionHandlerRejectsEmptyProtectedBody(t *testing.T) {
	t.Run("empty protected body", func(t *testing.T) {
		source := []byte(`
type Worker is interface provides Work : function(); end interface Worker;
module WorkerModule() return Worker is
  Work : function() is
  begin
  handler
    else null;
  end function Work;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`)
		_, err := Compile(source, "System")
		if err == nil || !strings.Contains(err.Error(), "handler requires a nonempty protected statement list") {
			t.Fatalf("empty direct function-handler boundary=%v", err)
		}
	})
}

func TestSourceActionInterruptAbandonsProtectedBlockAndResumes(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Pulse(value : Integer);
  action out ProtectedContinued();
  action out Recovered(value : Integer);
  action out AfterDo();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;

module WorkerModule() return Worker is
serial
  when Trigger do
    do
      Pulse(7);
      ProtectedContinued();
    handler
      is Pulse(?Caught) => Recovered(?Caught);
    end do;
    AfterDo();
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
	journal := arch.NewExecutionJournal(digest, 30,
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
	pulse := sourceNamedEvents(result.Poset, "worker", "Pulse")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	after := sourceNamedEvents(result.Poset, "worker", "AfterDo")
	if len(pulse) != 1 || len(recovered) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "ProtectedContinued")) != 0 {
		t.Fatalf("Pulse/Recovered/After=%d/%d/%d", len(pulse), len(recovered), len(after))
	}
	value, _ := recovered[0].Param("value")
	if value != int64(7) {
		t.Fatalf("interrupt handler binding=%#v", value)
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], pulse[0])
	if !result.Poset.IsCausallyBefore(recovered[0].ID, after[0].ID) {
		t.Fatal("interrupt handler did not resume after the protected block")
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("ordinary action interrupt was treated as an exception: %#v", result.ExceptionPropagations)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed action-interrupt execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("action-interrupt replay changed canonical artifact bytes")
	}
}

func TestSourceNestedActionInterruptSelectsMostRecentHandler(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Pulse();
  action out InnerRecovered();
  action out OuterRecovered();
  action out InnerContinued();
  action out OuterContinued();
  action out AfterOuter();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
serial when Trigger do
  do
    do
      Pulse();
      InnerContinued();
    handler
      is Pulse => InnerRecovered();
    end do;
    OuterContinued();
  handler
    is Pulse => OuterRecovered();
  end do;
  AfterOuter();
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	inner := sourceNamedEvents(result.Poset, "worker", "InnerRecovered")
	outerContinued := sourceNamedEvents(result.Poset, "worker", "OuterContinued")
	after := sourceNamedEvents(result.Poset, "worker", "AfterOuter")
	if len(inner) != 1 || len(outerContinued) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "OuterRecovered")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "InnerContinued")) != 0 {
		t.Fatalf("Inner/OuterContinued/After=%d/%d/%d", len(inner), len(outerContinued), len(after))
	}
	if !result.Poset.IsCausallyBefore(inner[0].ID, outerContinued[0].ID) ||
		!result.Poset.IsCausallyBefore(outerContinued[0].ID, after[0].ID) {
		t.Fatal("most-recent interrupt recovery did not resume through enclosing blocks")
	}
}

func TestSourceNestedActionInterruptFallsBackToOlderMatchingHandler(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Pulse();
  action out Other();
  action out InnerRecovered();
  action out OuterRecovered();
  action out InnerContinued();
  action out OuterContinued();
  action out AfterOuter();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
serial when Trigger do
  do
    do
      Pulse();
      InnerContinued();
    handler
      is Other => InnerRecovered();
    end do;
    OuterContinued();
  handler
    is Pulse => OuterRecovered();
  end do;
  AfterOuter();
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	outer := sourceNamedEvents(result.Poset, "worker", "OuterRecovered")
	after := sourceNamedEvents(result.Poset, "worker", "AfterOuter")
	if len(outer) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "InnerRecovered")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "InnerContinued")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "OuterContinued")) != 0 {
		t.Fatalf("OuterRecovered/After=%d/%d", len(outer), len(after))
	}
	if !result.Poset.IsCausallyBefore(outer[0].ID, after[0].ID) {
		t.Fatal("older interrupt handler did not resume after its protected block")
	}
}

func TestSourceFunctionLocalActionInterruptResumesFunctionAndCaller(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Pulse(value : Integer);
  action out ProtectedContinued();
  action out Recovered(value : Integer);
  action out FunctionContinued();
  action out AfterCall();
  provides Work : function();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  Work : function() is
  begin
    do
      Pulse(9);
      ProtectedContinued();
    handler
      is Pulse(?Caught) => Recovered(?Caught);
    end do;
    FunctionContinued();
  end function Work;
serial when Trigger do
  Work();
  AfterCall();
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
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	continued := sourceNamedEvents(result.Poset, "worker", "FunctionContinued")
	after := sourceNamedEvents(result.Poset, "worker", "AfterCall")
	if len(recovered) != 1 || len(continued) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "ProtectedContinued")) != 0 {
		t.Fatalf("Recovered/FunctionContinued/AfterCall=%d/%d/%d", len(recovered), len(continued), len(after))
	}
	value, _ := recovered[0].Param("value")
	if value != int64(9) {
		t.Fatalf("function-local interrupt binding=%#v", value)
	}
	if !result.Poset.IsCausallyBefore(recovered[0].ID, continued[0].ID) ||
		!result.Poset.IsCausallyBefore(continued[0].ID, after[0].ID) {
		t.Fatal("function-local interrupt did not resume through function and caller")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed function-local action-interrupt execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("function-local action-interrupt replay changed canonical bytes")
	}
}

func TestSourceAnyHandlerPatternCatchesImmediateActionInterrupt(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Pulse(value : Integer);
  action out ProtectedContinued();
  action out Recovered();
  action out AfterDo();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
serial when Trigger do
  do
    Pulse(7);
    ProtectedContinued();
  handler
    is any => Recovered();
  end do;
  AfterDo();
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
	journal := arch.NewExecutionJournal(digest, 30,
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
	pulse := sourceNamedEvents(result.Poset, "worker", "Pulse")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	after := sourceNamedEvents(result.Poset, "worker", "AfterDo")
	if len(pulse) != 1 || len(recovered) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "ProtectedContinued")) != 0 {
		t.Fatalf("Pulse/Recovered/AfterDo=%d/%d/%d", len(pulse), len(recovered), len(after))
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], pulse[0])
	if !result.Poset.IsCausallyBefore(recovered[0].ID, after[0].ID) {
		t.Fatal("any action handler did not resume after its protected block")
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("any action interrupt entered exception propagation: %#v", result.ExceptionPropagations)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed predefined-any action handling")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("predefined-any action replay changed canonical bytes")
	}
}

func TestSourceAnyHandlerPatternCatchesRaisedException(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out ProtectedContinued();
  action out Recovered();
  action out AfterDo();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure(value : Integer);
serial when Trigger do
  do
    raise Failure(6);
    ProtectedContinued();
  handler
    is any => Recovered();
  end do;
  AfterDo();
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	after := sourceNamedEvents(result.Poset, "worker", "AfterDo")
	if len(failure) != 1 || len(recovered) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "ProtectedContinued")) != 0 {
		t.Fatalf("Failure/Recovered/AfterDo=%d/%d/%d", len(failure), len(recovered), len(after))
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], failure[0])
	if !result.Poset.IsCausallyBefore(recovered[0].ID, after[0].ID) {
		t.Fatal("any exception handler did not resume after its protected block")
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("locally handled exception propagated: %#v", result.ExceptionPropagations)
	}
}

func TestSourceAnyHandlerPatternRejectsAmbiguousForms(t *testing.T) {
	tests := []struct {
		name    string
		handler string
		want    string
	}{
		{name: "parameter association", handler: "handler is any(?N) => null;", want: "cannot have parameter associations"},
		{name: "named overlap", handler: "handler is any => null; is Pulse => null;", want: "must be the handler's sole choice"},
		{name: "else overlap", handler: "handler is any => null; else null;", want: "must be the handler's sole choice"},
		{name: "unnamed reraise", handler: "handler is any => raise;", want: "unnamed raise requires a lexically enclosing active handler"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Worker is interface action in Trigger(); action out Pulse(); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
serial when Trigger do
  do Pulse(); ` + test.handler + ` end do;
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`)
			model, err := Compile(source, "System")
			if err == nil {
				_, err = model.DeterministicModelDigest()
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("predefined-any boundary=%v, want containing %q", err, test.want)
			}
		})
	}

	t.Run("module activation", func(t *testing.T) {
		source := []byte(`
type Worker is interface action in Trigger(); end interface Worker;
module WorkerModule() return Worker is
serial when Trigger do null; end when;
handler is any => null;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`)
		_, err := Compile(source, "System")
		if err == nil || !strings.Contains(err.Error(), "requires an enclosing active procedural block") {
			t.Fatalf("module any-handler activation boundary=%v", err)
		}
	})
}

func TestSourceActionInterruptChoiceOrderIsCanonical(t *testing.T) {
	build := func(reverse bool) []byte {
		choices := "is Alpha(n is ?N) => Seen(?N); is Failure => Seen(-1); is Beta => Seen(0);"
		if reverse {
			choices = "is Beta => Seen(0); is Failure => Seen(-1); is Alpha(n is ?N) => Seen(?N);"
		}
		return []byte(`
type Worker is interface
  action in Trigger();
  action out Alpha(n : Integer);
  action out Beta();
  action out Seen(n : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
serial when Trigger do
  do Alpha(4); handler ` + choices + ` end do;
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
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
		t.Fatalf("interrupt handler choice order changed model identity: %s != %s", leftDigest, rightDigest)
	}
}

func TestSourceActionInterruptRejectsStandaloneModuleLifetime(t *testing.T) {
	t.Run("module handler", func(t *testing.T) {
		source := []byte(`
type Worker is interface action in Fail(); end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
serial when Fail do null; end when;
handler is Fail => null;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
end architecture System;
`)
		_, err := Compile(source, "System")
		if err == nil || !strings.Contains(err.Error(), "requires an enclosing active procedural block") {
			t.Fatalf("module interrupt handler boundary=%v", err)
		}
	})
}

func TestSourceInitializerExceptionFinalizesWithoutElaboratingProcesses(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Before();
  action out InitialContinued();
  action out ProcessRan();
  action out WrongModuleHandler();
  action out FinalRan();
end interface Worker;

module WorkerModule() return Worker is
  exception Failure;
initial
  Before();
  raise Failure;
  InitialContinued();
serial
  ProcessRan();
handler
  is Failure => WrongModuleHandler();
final
  FinalRan();
end module WorkerModule;

architecture System() is
  worker : Worker is WorkerModule();
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
	journal := arch.NewExecutionJournal(digest, 40)
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
	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	if len(before) != 1 || len(failure) != 1 {
		t.Fatalf("Before/Failure=%d/%d", len(before), len(failure))
	}
	for _, action := range []string{"InitialContinued", "ProcessRan", "WrongModuleHandler", "FinalRan"} {
		if events := sourceNamedEvents(result.Poset, "worker", action); len(events) != 0 {
			t.Fatalf("initializer exception did not suppress %s: %#v", action, events)
		}
	}
	assertOnlyDirectCause(t, result.Poset, failure[0], before[0])
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	root := lifecycleModuleByOccurrence(t, result, "architecture:root")
	if worker.State != arch.ModuleFinalizedState || worker.Namable ||
		worker.TerminationEventID != string(failure[0].ID) || worker.FinishEventID == "" {
		t.Fatalf("failed initializer lifecycle=%#v", worker)
	}
	for _, name := range worker.Names {
		if name.Live || strings.Join(name.LostAfter, ",") != string(failure[0].ID) {
			t.Fatalf("failed initializer retained provisional name=%#v", name)
		}
	}
	if root.State != arch.ModuleFinalizedState || root.Namable || root.FinishEventID == "" ||
		root.TerminationEventID != string(failure[0].ID) {
		t.Fatalf("initializer exception parent lifecycle=%#v", root)
	}
	finish, exists := result.Poset.Get(gorapide.EventID(worker.FinishEventID))
	if !exists || finish.Name != arch.ModuleFinishAction || finish.Source != worker.ModuleID {
		t.Fatalf("initializer Finish=%#v exists=%t", finish, exists)
	}
	assertOnlyDirectCause(t, result.Poset, finish, failure[0])
	rootFinish, exists := result.Poset.Get(gorapide.EventID(root.FinishEventID))
	if !exists || rootFinish.Name != arch.ModuleFinishAction || rootFinish.Source != root.ModuleID {
		t.Fatalf("initializer architecture Finish=%#v exists=%t", rootFinish, exists)
	}
	assertOnlyDirectCause(t, result.Poset, rootFinish, failure[0])
	if !result.Poset.IsCausallyIndependent(finish.ID, rootFinish.ID) {
		t.Fatal("host unwind ordered initializer and architecture Finish siblings")
	}
	for _, process := range result.Processes {
		if process.ComponentID == "worker" {
			t.Fatalf("initializer-failed process was recorded as elaborated: %#v", process)
		}
	}
	propagation := exceptionPropagationBySource(t, result, worker.ModuleID)
	if propagation.ExceptionEventID != string(failure[0].ID) || len(propagation.Targets) != 1 ||
		propagation.Targets[0].ModuleID != root.ModuleID || propagation.Targets[0].Disposition != "delivered" {
		t.Fatalf("initializer exception propagation=%#v", propagation)
	}
	foundFinalization := false
	for _, firing := range result.Firings {
		if firing.Transition == "initialization-finalization" && firing.Target == "worker" &&
			len(firing.Generated) == 1 && firing.Generated[0].EventID == worker.FinishEventID {
			foundFinalization = true
		}
	}
	if !foundFinalization {
		t.Fatal("initializer exception finalization is absent from firing audit")
	}

	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed initializer exception finalization")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("initializer exception replay changed canonical artifact bytes")
	}
}

func TestSourceInitializerDoHandlerRecoversBeforeProcessElaboration(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out ProtectedContinued();
  action out Recovered(value : Integer);
  action out AfterInitial();
  action out ProcessRan();
end interface Worker;

module WorkerModule() return Worker is
  exception Failure(value : Integer);
initial
  do
    raise Failure(5);
    ProtectedContinued();
  handler
    is Failure(?Caught) => Recovered(?Caught);
  end do;
  AfterInitial();
serial
  Named: declare exception Failure(code : Integer); do
    ProcessRan();
  end do Named;
end module WorkerModule;

architecture System() is
  worker : Worker is WorkerModule();
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30))
	if err != nil {
		t.Fatal(err)
	}
	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	after := sourceNamedEvents(result.Poset, "worker", "AfterInitial")
	process := sourceNamedEvents(result.Poset, "worker", "ProcessRan")
	if len(failure) != 1 || len(recovered) != 1 || len(after) != 1 || len(process) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "ProtectedContinued")) != 0 {
		t.Fatalf("Failure/Recovered/After/Process=%d/%d/%d/%d", len(failure), len(recovered), len(after), len(process))
	}
	value, _ := recovered[0].Param("value")
	if value != int64(5) {
		t.Fatalf("initializer handler binding=%#v", value)
	}
	if !result.Poset.IsCausallyBefore(failure[0].ID, recovered[0].ID) ||
		!result.Poset.IsCausallyBefore(recovered[0].ID, after[0].ID) ||
		!result.Poset.IsCausallyBefore(after[0].ID, process[0].ID) {
		t.Fatal("initializer local recovery did not precede later initialization and process execution")
	}
	foundProcess := false
	for _, record := range result.Processes {
		if record.ComponentID == "worker" {
			if !record.Terminated || record.Completion != "normal" || record.ExceptionEventID != "" {
				t.Fatalf("recovered initializer process audit=%#v", record)
			}
			foundProcess = true
		}
	}
	if !foundProcess {
		t.Fatal("successfully recovered initializer did not elaborate its process")
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("locally handled initializer exception propagated: %#v", result.ExceptionPropagations)
	}
}

func TestSourceInitializerDeclarationBearingDoUsesExactLexicalExceptions(t *testing.T) {
	source := []byte(`
type Worker is interface
  exception InterfaceNoise;
  action out NamedRecovered(code : Integer);
  action out UnnamedRecovered(code : Integer);
  action out InitialContinued();
  action out ProcessRan();
  action out Wrong();
end interface Worker;

module WorkerModule() return Worker is
  exception Failure(code : Integer);
initial
  Named: declare exception Failure(code : Integer); do
    raise Named::Failure(code is 2);
  handler
    is WorkerModule::Failure(code is ?Code) => Wrong();
    is WorkerModule::Named::Failure(code is ?Code) => NamedRecovered(?Code);
  end do Named;
  declare exception Failure(code : Integer); do
    raise Failure(code is 3);
  handler
    is WorkerModule::Failure(code is ?Code) => Wrong();
    is Failure(code is ?Code) => UnnamedRecovered(?Code);
  end do;
  InitialContinued();
serial
  ProcessRan();
end module WorkerModule;

architecture System() is
  worker : Worker is WorkerModule();
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
	journal := arch.NewExecutionJournal(digest, 40)
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

	failures := sourceNamedEvents(result.Poset, "worker", "Failure")
	named := sourceNamedEvents(result.Poset, "worker", "NamedRecovered")
	unnamed := sourceNamedEvents(result.Poset, "worker", "UnnamedRecovered")
	continued := sourceNamedEvents(result.Poset, "worker", "InitialContinued")
	process := sourceNamedEvents(result.Poset, "worker", "ProcessRan")
	if len(failures) != 2 || len(named) != 1 || len(unnamed) != 1 || len(continued) != 1 ||
		len(process) != 1 || len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
		t.Fatalf("Failure/Named/Unnamed/Continued/Process=%d/%d/%d/%d/%d",
			len(failures), len(named), len(unnamed), len(continued), len(process))
	}
	byCode := make(map[int]*gorapide.Event, len(failures))
	for _, failure := range failures {
		byCode[failure.ParamInt("code")] = failure
	}
	if byCode[2] == nil || byCode[3] == nil || named[0].ParamInt("code") != 2 ||
		unnamed[0].ParamInt("code") != 3 {
		t.Fatalf("initializer lexical exceptions=%#v named=%#v unnamed=%#v",
			byCode, named[0].Params, unnamed[0].Params)
	}
	assertOnlyDirectCause(t, result.Poset, named[0], byCode[2])
	assertOnlyDirectCause(t, result.Poset, byCode[3], named[0])
	assertOnlyDirectCause(t, result.Poset, unnamed[0], byCode[3])
	assertOnlyDirectCause(t, result.Poset, continued[0], unnamed[0])
	assertOnlyDirectCause(t, result.Poset, process[0], continued[0])
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State == arch.ModuleTerminatedState || worker.TerminationEventID != "" {
		t.Fatalf("declaration-bearing initializer terminated module=%#v", worker)
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("locally handled initializer exception propagated: %#v", result.ExceptionPropagations)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed declaration-bearing initializer recovery")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("declaration-bearing initializer replay changed canonical bytes")
	}
}

func TestSourceInitializerDeclarationBearingDoScopeDoesNotLeak(t *testing.T) {
	_, err := Compile([]byte(`
type Worker is interface action out Done(); end interface Worker;
module WorkerModule() return Worker is
initial
  declare exception LocalOnly; do null; end do;
  raise LocalOnly;
  Done();
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`), "System")
	if err == nil || !strings.Contains(err.Error(), `undeclared exception "LocalOnly"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestSourceInitializerDeclarationBearingDoIdentityIsCanonical(t *testing.T) {
	build := func(reverse bool) []byte {
		declarations := "exception LocalA; exception LocalB;"
		choices := "is LocalA => Seen(1); is LocalB => Seen(2);"
		if reverse {
			declarations = "exception LocalB; exception LocalA;"
			choices = "is LocalB => Seen(2); is LocalA => Seen(1);"
		}
		return []byte(`
type Worker is interface action out Seen(value : Integer); action out Continued(); end interface Worker;
module WorkerModule() return Worker is
initial
  Stable: declare ` + declarations + ` do
    raise Stable::LocalA;
  handler
    ` + choices + `
  end do Stable;
  Continued();
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
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
		t.Fatalf("initializer declaration-bearing do order changed model identity: %s != %s",
			leftDigest, rightDigest)
	}
	leftResult, err := left.ExecuteDeterministic(arch.NewExecutionJournal(leftDigest, 20))
	if err != nil {
		t.Fatal(err)
	}
	rightResult, err := right.ExecuteDeterministic(arch.NewExecutionJournal(rightDigest, 20))
	if err != nil {
		t.Fatal(err)
	}
	leftArtifact, _ := leftResult.MarshalCanonical()
	rightArtifact, _ := rightResult.MarshalCanonical()
	if !bytes.Equal(leftArtifact, rightArtifact) {
		t.Fatal("initializer exception declaration or handler-choice order changed canonical execution")
	}
}

func TestSourceInitializerExceptionCancelsOutstandingTimedWork(t *testing.T) {
	source := []byte(`
type Worker is interface action out Later(); end interface Worker;
module WorkerModule() return Worker is
  exception Failure;
  C : Clock is Make_Clock();
initial
  Later() in C.Ticks(1);
  raise Failure;
end module WorkerModule;
architecture System() is
  worker : Worker is WorkerModule();
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
	journal := arch.NewExecutionJournal(digest, 20)
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
	if len(sourceNamedEvents(result.Poset, "worker", "Later")) != 0 ||
		len(result.ScheduledEvents) != 0 || len(result.ClockAdvances) != 0 {
		t.Fatalf("canceled initializer work leaked: Later=%d scheduled=%#v advances=%#v",
			len(sourceNamedEvents(result.Poset, "worker", "Later")), result.ScheduledEvents, result.ClockAdvances)
	}
	if len(result.Clocks) != 1 || result.Clocks[0].Now != "0" {
		t.Fatalf("canceled initializer advanced its clock: %#v", result.Clocks)
	}
	foundCancellation := false
	for _, firing := range result.Firings {
		if firing.Transition != "initial" || firing.Target != "worker" {
			continue
		}
		if len(firing.Scheduled) != 1 || len(firing.CanceledSchedules) != 1 ||
			firing.Scheduled[0].ScheduleID != firing.CanceledSchedules[0] {
			t.Fatalf("initializer cancellation audit=%#v", firing)
		}
		foundCancellation = true
	}
	if !foundCancellation {
		t.Fatal("initializer cancellation firing is absent")
	}
	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if len(failure) != 1 || worker.State != arch.ModuleFinalizedState ||
		worker.TerminationEventID != string(failure[0].ID) || worker.FinishEventID == "" {
		t.Fatalf("canceled initializer lifecycle=%#v Failure=%#v", worker, failure)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed exceptional initializer cancellation")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("exceptional initializer cancellation replay changed artifact bytes")
	}
}

func TestSourceDoHandlerRejectsInvalidNamedTerminator(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "unlabeled named terminator", body: "do null; handler else null; end do Block;", want: "named do terminator requires a statement label"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Worker is interface action in Trigger(); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  C : Clock is Make_Clock();
serial when Trigger do
  ` + test.body + `
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
`)
			model, err := Compile(source, "System")
			if err == nil {
				_, err = model.DeterministicModelDigest()
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("do-handler boundary=%v, want containing %q", err, test.want)
			}
		})
	}
}

func TestSourceDoHandlerChoiceOrderIsCanonical(t *testing.T) {
	build := func(reverse bool) []byte {
		exceptions := "exception Alpha(n : Integer); exception Beta;"
		choices := "is Alpha(n is ?N) => Seen(?N); is Beta => Seen(0);"
		if reverse {
			exceptions = "exception Beta; exception Alpha(n : Integer);"
			choices = "is Beta => Seen(0); is Alpha(n is ?N) => Seen(?N);"
		}
		return []byte(`
type Worker is interface action in Trigger(); action out Seen(n : Integer); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  ` + exceptions + `
serial when Trigger do
  do
    raise Alpha(4);
  handler ` + choices + `
  end do;
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
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
		t.Fatalf("do-handler choice order changed model identity: %s != %s", leftDigest, rightDigest)
	}
}

func TestSourceUnnamedReraisePreservesExceptionOccurrence(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger(value : Integer);
  action out BeforeReraise(value : Integer);
  action out HandlerContinued(value : Integer);
  action out AfterDo(value : Integer);
  action out OuterRecovered(value : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;

module WorkerModule() return Worker is
  exception Failure(value : Integer);
serial
  when (?Value : Integer) Trigger(?Value) do
    do
      raise Failure(?Value);
    handler
      is Failure(?Caught) =>
        BeforeReraise(?Caught);
        if ?Caught > 0 then
          raise where ?Caught = 2;
        end if;
        HandlerContinued(?Caught);
    end do;
    AfterDo(?Value);
  handler
    is Failure(?Caught) => OuterRecovered(?Caught);
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
	journal := arch.NewExecutionJournal(digest, 60,
		arch.InputEvent{Key: "false", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": 1}},
		arch.InputEvent{Key: "true", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": 2}, Causes: []string{"false"}},
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

	byValue := func(action string) map[int64]*gorapide.Event {
		t.Helper()
		found := make(map[int64]*gorapide.Event)
		for _, event := range sourceNamedEvents(result.Poset, "worker", action) {
			value, _ := event.Param("value")
			integer, ok := value.(int64)
			if !ok {
				t.Fatalf("%s.value=%#v", action, value)
			}
			found[integer] = event
		}
		return found
	}
	failures := byValue("Failure")
	before := byValue("BeforeReraise")
	continued := byValue("HandlerContinued")
	after := byValue("AfterDo")
	outer := byValue("OuterRecovered")
	if len(failures) != 2 || len(before) != 2 || len(continued) != 1 || len(after) != 1 || len(outer) != 1 {
		t.Fatalf("Failure/Before/Continued/After/Outer=%d/%d/%d/%d/%d",
			len(failures), len(before), len(continued), len(after), len(outer))
	}
	if continued[1] == nil || after[1] == nil || outer[2] == nil {
		t.Fatalf("conditional unnamed raise selected wrong branches: continued=%v after=%v outer=%v",
			continued, after, outer)
	}
	assertOnlyDirectCause(t, result.Poset, before[1], failures[1])
	assertOnlyDirectCause(t, result.Poset, before[2], failures[2])
	assertOnlyDirectCause(t, result.Poset, outer[2], before[2])
	if !result.Poset.IsCausallyBefore(before[1].ID, continued[1].ID) ||
		!result.Poset.IsCausallyBefore(continued[1].ID, after[1].ID) {
		t.Fatal("false unnamed raise did not continue the active handler and enclosing do block")
	}
	exceptionEvents := 0
	for _, firing := range result.Firings {
		for _, generated := range firing.Generated {
			if generated.Exception {
				exceptionEvents++
			}
		}
	}
	if exceptionEvents != 2 {
		t.Fatalf("unnamed re-raise generated a replacement exception occurrence: audited=%d", exceptionEvents)
	}

	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed unnamed re-raise execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("unnamed re-raise replay changed canonical artifact bytes")
	}
}

func TestSourceExceptionCompilerRejectsUnsupportedOrMalformedForms(t *testing.T) {
	tests := []struct {
		name         string
		declarations string
		body         string
		handler      string
		want         string
	}{
		{name: "duplicate exception", declarations: "exception Capacity(n : Integer); exception capacity(n : Integer);", body: "null;", want: "duplicate module exception"},
		{name: "structural parameter", declarations: "exception Capacity(value : Worker);", body: "null;", want: "unique predefined-scalar type"},
		{name: "action conflict", declarations: "exception Trigger(value : Integer);", body: "null;", want: "conflicts with a returned-interface action"},
		{name: "undeclared raise", body: "raise Missing;", want: "undeclared exception"},
		{name: "nonboolean where", declarations: "exception Capacity;", body: "raise Capacity where 1;", want: "where condition has type Integer"},
		{name: "unnamed reraise outside handler", declarations: "exception Capacity;", body: "raise;", want: "unnamed raise requires a lexically enclosing active handler"},
		{name: "qualified action handler pattern", declarations: "exception Capacity;", body: "raise Capacity;", handler: "handler is worker.Trigger(?N) => null;", want: "one unqualified basic exception or action pattern"},
		{name: "literal handler association", declarations: "exception Capacity(n : Integer);", body: "raise Capacity(1);", handler: "handler is Capacity(1) => null;", want: "must uniquely bind direct existential placeholders"},
		{name: "duplicate handler choice", declarations: "exception Capacity;", body: "raise Capacity;", handler: "handler is Capacity => null; is capacity => null;", want: "duplicate exception"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Worker is interface action in Trigger(value : Integer); end interface Worker;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;
module WorkerModule() return Worker is
  ` + test.declarations + `
serial when (?N : Integer) Trigger(?N) do
  ` + test.body + `
  ` + test.handler + `
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger => worker.Trigger;
end architecture System;
`)
			model, err := Compile(source, "System")
			if err == nil {
				_, err = model.DeterministicModelDigest()
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("exception form error=%v, want containing %q", err, test.want)
			}
		})
	}
}

func TestSourceUnhandledExceptionTerminatesSingleProcessModule(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
serial when Trigger() do raise Failure; end when;
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
	journal := arch.NewExecutionJournal(digest, 20,
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
	failure := assertExceptionTerminatedModule(t, result, "worker", "Failure")
	workerModule := lifecycleModuleByOccurrence(t, result, "component:worker")
	rootModule := lifecycleModuleByOccurrence(t, result, "architecture:root")
	if rootModule.State != arch.ModuleTerminatedState ||
		rootModule.TerminationEventID != string(failure.ID) {
		t.Fatalf("parent architecture exception termination=%#v", rootModule)
	}
	workerPropagation := exceptionPropagationBySource(t, result, workerModule.ModuleID)
	if workerPropagation.ExceptionEventID != string(failure.ID) || workerPropagation.Exception != "Failure" ||
		len(workerPropagation.Targets) != 1 || workerPropagation.Targets[0].ModuleID != rootModule.ModuleID ||
		strings.Join(workerPropagation.Targets[0].Relations, ",") != "parent" ||
		workerPropagation.Targets[0].Disposition != "delivered" {
		t.Fatalf("worker-to-parent exception propagation=%#v", workerPropagation)
	}
	rootPropagation := exceptionPropagationBySource(t, result, rootModule.ModuleID)
	if len(rootPropagation.Targets) != 1 || rootPropagation.Targets[0].ModuleID != "$environment" ||
		rootPropagation.Targets[0].Disposition != "escaped-environment" {
		t.Fatalf("root exception escape=%#v", rootPropagation)
	}
	if len(result.Poset.EventsByName(arch.ModuleFinishAction)) != 0 {
		t.Fatal("still-named terminated module was incorrectly finalized")
	}
	if len(result.Poset.DirectCauses(failure.ID)) == 0 {
		t.Fatal("terminating exception lost its process trigger frontier")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed exception termination artifact")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("exception termination replay changed canonical artifact bytes")
	}
}

func TestSourceUnhandledExceptionPropagatesToParentAndLinkedModulesOnce(t *testing.T) {
	source := []byte(`
type Aircraft is interface
  action out Register(value : Aircraft);
  action in Go();
end interface Aircraft;
type Sector is interface
  action in Accept(value : Aircraft);
  action out Ready();
end interface Sector;

module AircraftModule() return Aircraft is
  exception Failure;
initial Register(Self);
serial when Go do Link(Self); raise Failure; end when;
end module AircraftModule;

module SectorModule() return Sector is
serial when (?Aircraft : Aircraft) Accept(?Aircraft) do
  Link(?Aircraft);
  Ready();
end when;
end module SectorModule;

architecture System() is
  plane : Aircraft is AircraftModule();
  sector : Sector is SectorModule();
connect
  (?Aircraft : Aircraft) plane.Register(?Aircraft) to sector.Accept(?Aircraft);
  sector.Ready to plane.Go;
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
	journal := arch.NewExecutionJournal(digest, 50)
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

	failures := sourceNamedEvents(result.Poset, "plane", "Failure")
	if len(failures) != 1 {
		t.Fatalf("propagated Failure occurrences=%d, want one", len(failures))
	}
	failure := failures[0]
	planeModule := lifecycleModuleByOccurrence(t, result, "component:plane")
	sectorModule := lifecycleModuleByOccurrence(t, result, "component:sector")
	rootModule := lifecycleModuleByOccurrence(t, result, "architecture:root")
	for _, module := range []*arch.ModuleLifecycleRecord{planeModule, sectorModule, rootModule} {
		if module.State != arch.ModuleTerminatedState || module.TerminationEventID != string(failure.ID) {
			t.Fatalf("propagated exception lifecycle=%#v", module)
		}
	}

	planePropagation := exceptionPropagationBySource(t, result, planeModule.ModuleID)
	if planePropagation.ExceptionEventID != string(failure.ID) || len(planePropagation.Targets) != 3 {
		t.Fatalf("plane propagation=%#v", planePropagation)
	}
	wantTargetIDs := []string{planeModule.ModuleID, rootModule.ModuleID, sectorModule.ModuleID}
	sort.Strings(wantTargetIDs)
	for index, target := range planePropagation.Targets {
		if target.ModuleID != wantTargetIDs[index] || target.Disposition != "delivered" {
			t.Fatalf("canonical plane target[%d]=%#v, want module %q", index, target, wantTargetIDs[index])
		}
		wantRelation := "parent"
		if target.ModuleID == sectorModule.ModuleID || target.ModuleID == planeModule.ModuleID {
			wantRelation = "linked"
		}
		if strings.Join(target.Relations, ",") != wantRelation {
			t.Fatalf("plane target relation=%#v, want %q", target, wantRelation)
		}
	}
	sectorPropagation := exceptionPropagationBySource(t, result, sectorModule.ModuleID)
	if len(sectorPropagation.Targets) != 1 || sectorPropagation.Targets[0].ModuleID != rootModule.ModuleID ||
		sectorPropagation.Targets[0].Disposition != "delivered" {
		t.Fatalf("linked sector parent propagation=%#v", sectorPropagation)
	}
	rootPropagation := exceptionPropagationBySource(t, result, rootModule.ModuleID)
	if len(rootPropagation.Targets) != 1 || rootPropagation.Targets[0].ModuleID != "$environment" ||
		rootPropagation.Targets[0].Disposition != "escaped-environment" {
		t.Fatalf("linked propagation root escape=%#v", rootPropagation)
	}

	sectorCompleted := false
	for _, process := range result.Processes {
		if process.ComponentID == "sector" {
			if !process.Terminated || process.Completion != "module-termination" ||
				process.ExceptionEventID != string(failure.ID) {
				t.Fatalf("linked target process completion=%#v", process)
			}
			sectorCompleted = true
		}
	}
	if !sectorCompleted {
		t.Fatal("linked target process was not completed")
	}

	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed parent/linked exception propagation")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("parent/linked exception propagation replay changed canonical bytes")
	}
}

func TestSourceUnhandledExceptionStopsAtAlreadyTerminatedParent(t *testing.T) {
	source := []byte(`
type First is interface action in Fail(); end interface First;
type Second is interface action in Fail(); end interface Second;
type Stimulus is interface action out FailFirst(); action out FailSecond(); end interface Stimulus;

module FirstModule() return First is
  exception FirstFailure;
serial when Fail do raise FirstFailure; end when;
end module FirstModule;

module SecondModule() return Second is
  exception SecondFailure;
  C : Clock is Make_Clock();
serial when Fail do pause C.Ticks(1); raise SecondFailure; end when;
end module SecondModule;

architecture System() is
  stimulus : Stimulus;
  first : First is FirstModule();
  second : Second is SecondModule();
connect
  stimulus.FailFirst to first.Fail;
  stimulus.FailSecond to second.Fail;
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
		arch.InputEvent{Key: "first", Source: "stimulus", Action: "FailFirst"},
		arch.InputEvent{Key: "second", Source: "stimulus", Action: "FailSecond"},
	))
	if err != nil {
		t.Fatal(err)
	}
	firstFailure := assertExceptionTerminatedModule(t, result, "first", "FirstFailure")
	secondFailure := assertExceptionTerminatedModule(t, result, "second", "SecondFailure")
	if firstFailure.ID == secondFailure.ID {
		t.Fatal("distinct raises reused an exception occurrence")
	}
	rootModule := lifecycleModuleByOccurrence(t, result, "architecture:root")
	if rootModule.TerminationEventID != string(firstFailure.ID) {
		t.Fatalf("parent termination witness=%#v, want first exception %s", rootModule, firstFailure.ID)
	}
	secondModule := lifecycleModuleByOccurrence(t, result, "component:second")
	propagation := exceptionPropagationBySource(t, result, secondModule.ModuleID)
	if propagation.ExceptionEventID != string(secondFailure.ID) || len(propagation.Targets) != 1 ||
		propagation.Targets[0].ModuleID != rootModule.ModuleID ||
		propagation.Targets[0].Disposition != "ignored-already-terminated" {
		t.Fatalf("already-terminated parent propagation=%#v", propagation)
	}
	for _, record := range result.ExceptionPropagations {
		if record.SourceModuleID == rootModule.ModuleID && record.ExceptionEventID == string(secondFailure.ID) {
			t.Fatalf("second exception propagated beyond already-terminated parent: %#v", record)
		}
	}
}

func TestSourceLinkedModuleHandlerElseHandlesPropagatedDeclaration(t *testing.T) {
	source := []byte(`
type Aircraft is interface action out Register(value : Aircraft); action in Go(); end interface Aircraft;
type Sector is interface
  action in Accept(value : Aircraft);
  action out Ready();
  action out NamedRecovery();
  action out ElseRecovery();
end interface Sector;

module AircraftModule() return Aircraft is
  exception Failure;
initial Register(Self);
serial when Go do raise Failure; end when;
end module AircraftModule;

module SectorModule() return Sector is
  exception Failure;
serial when (?Aircraft : Aircraft) Accept(?Aircraft) do
  Link(?Aircraft);
  Ready();
end when;
handler
  is Failure => NamedRecovery();
  else ElseRecovery();
end module SectorModule;

architecture System() is
  plane : Aircraft is AircraftModule();
  sector : Sector is SectorModule();
connect
  (?Aircraft : Aircraft) plane.Register(?Aircraft) to sector.Accept(?Aircraft);
  sector.Ready to plane.Go;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 50))
	if err != nil {
		t.Fatal(err)
	}
	failures := sourceNamedEvents(result.Poset, "plane", "Failure")
	named := sourceNamedEvents(result.Poset, "sector", "NamedRecovery")
	elseRecovery := sourceNamedEvents(result.Poset, "sector", "ElseRecovery")
	if len(failures) != 1 || len(named) != 0 || len(elseRecovery) != 1 {
		t.Fatalf("Failure/NamedRecovery/ElseRecovery=%d/%d/%d", len(failures), len(named), len(elseRecovery))
	}
	assertOnlyDirectCause(t, result.Poset, elseRecovery[0], failures[0])
	planeModule := lifecycleModuleByOccurrence(t, result, "component:plane")
	sectorModule := lifecycleModuleByOccurrence(t, result, "component:sector")
	rootModule := lifecycleModuleByOccurrence(t, result, "architecture:root")
	if planeModule.State != arch.ModuleTerminatedState || rootModule.State != arch.ModuleTerminatedState ||
		sectorModule.State != arch.ModuleRunningState || sectorModule.TerminationEventID != "" {
		t.Fatalf("propagated-handler lifecycles plane=%#v sector=%#v root=%#v",
			planeModule, sectorModule, rootModule)
	}
	propagation := exceptionPropagationBySource(t, result, planeModule.ModuleID)
	sectorHandled := false
	for _, target := range propagation.Targets {
		if target.ModuleID == sectorModule.ModuleID {
			if target.Disposition != "handled" || strings.Join(target.Relations, ",") != "linked" {
				t.Fatalf("linked handler propagation target=%#v", target)
			}
			sectorHandled = true
		}
	}
	if !sectorHandled {
		t.Fatalf("linked handled target absent from propagation=%#v", propagation)
	}
	for _, record := range result.ExceptionPropagations {
		if record.SourceModuleID == sectorModule.ModuleID && record.ExceptionEventID == string(failures[0].ID) {
			t.Fatalf("handled linked exception propagated onward=%#v", record)
		}
	}
	foundElseFiring := false
	for _, firing := range result.Firings {
		if firing.Transition == "module-handler" && firing.Target == "sector" && firing.RuleProcess == "else" {
			foundElseFiring = true
		}
	}
	if !foundElseFiring {
		t.Fatal("propagated module-handler else selection is absent from firing audit")
	}
}

func TestSourcePropagatedModuleHandlerReraisesSameOccurrence(t *testing.T) {
	source := []byte(`
type Producer is interface
  action out Register(value : Producer);
  action in Go();
end interface Producer;
type Monitor is interface
  action in Accept(value : Producer);
  action out Ready();
  action out BeforeReraise();
  action out Unreachable();
end interface Monitor;
module ProducerModule() return Producer is
  exception Failure;
initial Register(Self);
serial when Go do raise Failure; end when;
end module ProducerModule;
module MonitorModule() return Monitor is
serial when (?Producer : Producer) Accept(?Producer) do
  Link(?Producer);
  Ready();
end when;
handler
  else
    BeforeReraise();
    raise;
    Unreachable();
end module MonitorModule;
architecture System() is
  producer : Producer is ProducerModule();
  monitor : Monitor is MonitorModule();
connect
  (?Producer : Producer) producer.Register(?Producer) to monitor.Accept(?Producer);
  monitor.Ready to producer.Go;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 60))
	if err != nil {
		t.Fatal(err)
	}
	failure := sourceNamedEvents(result.Poset, "producer", "Failure")
	before := sourceNamedEvents(result.Poset, "monitor", "BeforeReraise")
	if len(failure) != 1 || len(before) != 1 || len(sourceNamedEvents(result.Poset, "monitor", "Unreachable")) != 0 {
		t.Fatalf("Failure/BeforeReraise/Unreachable=%d/%d/%d",
			len(failure), len(before), len(sourceNamedEvents(result.Poset, "monitor", "Unreachable")))
	}
	assertOnlyDirectCause(t, result.Poset, before[0], failure[0])
	producer := lifecycleModuleByOccurrence(t, result, "component:producer")
	monitor := lifecycleModuleByOccurrence(t, result, "component:monitor")
	if producer.State != arch.ModuleTerminatedState || monitor.State != arch.ModuleTerminatedState ||
		producer.TerminationEventID != string(failure[0].ID) || monitor.TerminationEventID != string(failure[0].ID) {
		t.Fatalf("propagated re-raise lifecycles producer=%#v monitor=%#v", producer, monitor)
	}
	producerPropagation := exceptionPropagationBySource(t, result, producer.ModuleID)
	foundFailedHandler := false
	for _, target := range producerPropagation.Targets {
		if target.ModuleID == monitor.ModuleID {
			if target.Disposition != "handler-raised" || strings.Join(target.Relations, ",") != "linked" {
				t.Fatalf("propagated re-raise target=%#v", target)
			}
			foundFailedHandler = true
		}
	}
	if !foundFailedHandler {
		t.Fatalf("handler-raised target absent from propagation=%#v", producerPropagation)
	}
	monitorPropagation := exceptionPropagationBySource(t, result, monitor.ModuleID)
	if monitorPropagation.ExceptionEventID != string(failure[0].ID) || monitorPropagation.Exception != "Failure" {
		t.Fatalf("monitor re-propagation=%#v", monitorPropagation)
	}
	if len(sourceNamedEvents(result.Poset, "producer", "Failure")) != 1 {
		t.Fatal("propagated unnamed re-raise generated a replacement occurrence")
	}
}

func TestSourceModuleHandlerRejectsSuspendingAndInterruptForms(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "pause", body: "pause C.Ticks(1);", want: "pause requires a resumable declarative-process continuation"},
		{name: "nested interrupt", body: "do null; handler is Fail => null; end do;", want: "nested module-handler interrupt choice is outside the immediate recovery subset"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Worker is interface action in Fail(); end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  C : Clock is Make_Clock();
serial when Fail do raise Failure; end when;
handler else ` + test.body + `
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
end architecture System;
`)
			model, err := Compile(source, "System")
			if err == nil {
				_, err = model.DeterministicModelDigest()
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("module handler boundary=%v, want containing %q", err, test.want)
			}
		})
	}
}

func TestSourceModuleHandlerLinkAndUnlinkMutateExceptionTopology(t *testing.T) {
	for _, test := range []struct {
		name           string
		processPrefix  string
		handlerBody    string
		exception      string
		wantSelfTarget bool
		wantLiveLink   bool
	}{
		{
			name:        "Link affects replacement occurrence",
			handlerBody: `AdjustContext(); raise Escalated;`,
			exception:   "Escalated", wantSelfTarget: true, wantLiveLink: true,
		},
		{
			name:          "Unlink affects reraised occurrence",
			processPrefix: `Link(Self);`,
			handlerBody:   `Unlink(Self); raise;`,
			exception:     "Failure", wantSelfTarget: false, wantLiveLink: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Worker is interface
  action in Fail();
  provides AdjustContext : function();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  exception Escalated;
  AdjustContext : function() is
  begin
    Link(Self);
  end function AdjustContext;
serial when Fail do
  ` + test.processPrefix + `
  raise Failure;
end when;
handler is Failure =>
  ` + test.handlerBody + `
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
			journal := arch.NewExecutionJournal(digest, 30,
				arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

			failure := sourceNamedEvents(result.Poset, "worker", "Failure")
			propagated := sourceNamedEvents(result.Poset, "worker", test.exception)
			if len(failure) != 1 || len(propagated) != 1 {
				t.Fatalf("Failure/%s occurrences=%d/%d", test.exception, len(failure), len(propagated))
			}
			if test.exception == "Escalated" && failure[0].ID == propagated[0].ID {
				t.Fatal("named handler raise reused the handled exception occurrence")
			}
			if test.exception == "Failure" && failure[0].ID != propagated[0].ID {
				t.Fatal("unnamed handler raise replaced the handled exception occurrence")
			}

			worker := lifecycleModuleByOccurrence(t, result, "component:worker")
			propagation := exceptionPropagationBySource(t, result, worker.ModuleID)
			if propagation.Exception != test.exception || propagation.ExceptionEventID != string(propagated[0].ID) {
				t.Fatalf("handler Context propagation=%#v", propagation)
			}
			selfTarget := false
			for _, target := range propagation.Targets {
				if target.ModuleID == worker.ModuleID {
					selfTarget = true
					if strings.Join(target.Relations, ",") != "linked" {
						t.Fatalf("self propagation relation=%#v", target)
					}
				}
			}
			if selfTarget != test.wantSelfTarget {
				t.Fatalf("self propagation target=%v, want %v: %#v", selfTarget, test.wantSelfTarget, propagation)
			}

			var explicitSelf *arch.CommunicationContextRecord
			for index := range result.Contexts {
				candidate := &result.Contexts[index]
				if candidate.Kind == "explicit-link" && candidate.Source == worker.ModuleID &&
					candidate.Destination == worker.ModuleID {
					explicitSelf = candidate
					break
				}
			}
			if explicitSelf == nil || explicitSelf.Live != test.wantLiveLink {
				t.Fatalf("handler Context interval=%#v, want live=%v", explicitSelf, test.wantLiveLink)
			}
			if !test.wantLiveLink &&
				(len(explicitSelf.LostAfter) != 1 || explicitSelf.LostAfter[0] != string(failure[0].ID)) {
				t.Fatalf("handler Unlink loss frontier=%#v, want %s", explicitSelf.LostAfter, failure[0].ID)
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
				t.Fatal("GOMAXPROCS changed module-handler Context mutation")
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
				t.Fatal("module-handler Context replay changed canonical bytes")
			}
		})
	}
}

func TestSourceModuleHandlerReraisesExactHandledOccurrence(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out BeforeReraise();
  action out Unreachable();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    BeforeReraise();
    raise;
    Unreachable();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	before := sourceNamedEvents(result.Poset, "worker", "BeforeReraise")
	if len(failure) != 1 || len(before) != 1 || len(sourceNamedEvents(result.Poset, "worker", "Unreachable")) != 0 {
		t.Fatalf("Failure/BeforeReraise/Unreachable=%d/%d/%d", len(failure), len(before), len(sourceNamedEvents(result.Poset, "worker", "Unreachable")))
	}
	assertOnlyDirectCause(t, result.Poset, before[0], failure[0])
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State != arch.ModuleTerminatedState || worker.TerminationEventID != string(failure[0].ID) {
		t.Fatalf("re-raising module lifecycle=%#v", worker)
	}
	propagation := exceptionPropagationBySource(t, result, worker.ModuleID)
	if propagation.ExceptionEventID != string(failure[0].ID) || propagation.Exception != "Failure" {
		t.Fatalf("re-raised propagation=%#v", propagation)
	}
	if len(sourceNamedEvents(result.Poset, "worker", "Failure")) != 1 {
		t.Fatal("unnamed module-handler re-raise generated a replacement exception occurrence")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed module-handler re-raise")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("module-handler re-raise replay changed canonical artifact bytes")
	}
}

func TestSourceModuleHandlerNamedRaiseEscapesAsNewOccurrence(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out BeforeEscalation();
  action out Unreachable();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  exception Escalated(code : Integer);
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    BeforeEscalation();
    raise Escalated(9);
    Unreachable();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
	))
	if err != nil {
		t.Fatal(err)
	}
	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	before := sourceNamedEvents(result.Poset, "worker", "BeforeEscalation")
	escalated := sourceNamedEvents(result.Poset, "worker", "Escalated")
	if len(failure) != 1 || len(before) != 1 || len(escalated) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Unreachable")) != 0 {
		t.Fatalf("Failure/BeforeEscalation/Escalated/Unreachable=%d/%d/%d/%d",
			len(failure), len(before), len(escalated), len(sourceNamedEvents(result.Poset, "worker", "Unreachable")))
	}
	code, _ := escalated[0].Param("code")
	if code != int64(9) {
		t.Fatalf("Escalated code=%#v", code)
	}
	assertOnlyDirectCause(t, result.Poset, before[0], failure[0])
	assertOnlyDirectCause(t, result.Poset, escalated[0], before[0])
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State != arch.ModuleTerminatedState || worker.TerminationEventID != string(escalated[0].ID) {
		t.Fatalf("named handler raise lifecycle=%#v", worker)
	}
	propagation := exceptionPropagationBySource(t, result, worker.ModuleID)
	if propagation.ExceptionEventID != string(escalated[0].ID) || propagation.Exception != "Escalated" {
		t.Fatalf("named handler raise propagation=%#v", propagation)
	}
}

func TestSourceModuleHandlerFalseConditionalRaiseContinues(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out BeforeConditionalRaise();
  action out AfterConditionalRaise();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  exception Escalated(code : Integer);
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    BeforeConditionalRaise();
    raise Escalated(9) where False;
    AfterConditionalRaise();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	before := sourceNamedEvents(result.Poset, "worker", "BeforeConditionalRaise")
	after := sourceNamedEvents(result.Poset, "worker", "AfterConditionalRaise")
	if len(failure) != 1 || len(before) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Escalated")) != 0 {
		t.Fatalf("Failure/BeforeConditionalRaise/AfterConditionalRaise/Escalated=%d/%d/%d/%d",
			len(failure), len(before), len(after), len(sourceNamedEvents(result.Poset, "worker", "Escalated")))
	}
	assertOnlyDirectCause(t, result.Poset, before[0], failure[0])
	assertOnlyDirectCause(t, result.Poset, after[0], before[0])
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State == arch.ModuleTerminatedState || worker.TerminationEventID != "" {
		t.Fatalf("false conditional handler raise terminated module=%#v", worker)
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("false conditional handler raise propagated=%#v", result.ExceptionPropagations)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed false conditional module-handler raise")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("false conditional module-handler raise replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerCallsSameModuleFunctionAndContinues(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail(code : Integer);
  action out FunctionBody(code : Integer);
  action out HandlerContinued(code : Integer);
  provides Recover : function(code : Integer);
end interface Worker;
type Stimulus is interface action out Fail(code : Integer); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure(code : Integer);
  Recover : function(code : Integer) is
  begin
    FunctionBody(code);
  end function Recover;
serial when (?Code : Integer) Fail(?Code) do raise Failure(?Code); end when;
handler
  is Failure(?Code) =>
    Recover(?Code);
    HandlerContinued(?Code);
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail", Params: map[string]any{"code": 7}},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	calls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Recover'Call"))
	body := sourceNamedEvents(result.Poset, "worker", "FunctionBody")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Recover'Return"))
	continued := sourceNamedEvents(result.Poset, "worker", "HandlerContinued")
	if len(failure) != 1 || len(calls) != 1 || len(body) != 1 || len(returns) != 1 || len(continued) != 1 {
		t.Fatalf("Failure/Recover'Call/FunctionBody/Recover'Return/HandlerContinued=%d/%d/%d/%d/%d",
			len(failure), len(calls), len(body), len(returns), len(continued))
	}
	for index, event := range []*gorapide.Event{body[0], continued[0]} {
		if event.ParamInt("code") != 7 {
			t.Fatalf("event[%d] code=%#v", index, event.Params)
		}
	}
	assertOnlyDirectCause(t, result.Poset, calls[0], failure[0])
	assertOnlyDirectCause(t, result.Poset, body[0], calls[0])
	assertOnlyDirectCause(t, result.Poset, returns[0], body[0])
	assertOnlyDirectCause(t, result.Poset, continued[0], returns[0])
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State == arch.ModuleTerminatedState || worker.TerminationEventID != "" {
		t.Fatalf("successful handler function call terminated module=%#v", worker)
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("successful handler function call propagated=%#v", result.ExceptionPropagations)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed module-handler function call")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("module-handler function call replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerCallsCrossComponentFunctionAndContinues(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail(code : Integer);
  action out HandlerContinued(code : Integer);
  requires Recover : function(code : Integer);
end interface Worker;
type Provider is interface
  action out ProviderRecovered(code : Integer);
  provides Recover : function(code : Integer);
  provides Helper : function(code : Integer);
end interface Provider;
type Stimulus is interface action out Fail(code : Integer); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure(code : Integer);
serial when (?Code : Integer) Fail(?Code) do raise Failure(?Code); end when;
handler
  is Failure(?Code) =>
    Recover(?Code);
    HandlerContinued(?Code);
end module WorkerModule;
module ProviderModule() return Provider is
  Recover : function(code : Integer) is
  begin
    Helper(code);
  end function Recover;
  Helper : function(code : Integer) is
  begin
    ProviderRecovered(code);
  end function Helper;
end module ProviderModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
  provider : Provider is ProviderModule();
connect
  stimulus.Fail to worker.Fail;
  worker.Recover to provider.Recover;
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
	journal := arch.NewExecutionJournal(digest, 70,
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail", Params: map[string]any{"code": 7}},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	calls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Recover'Call"))
	helperCalls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "provider", "Helper'Call"))
	body := sourceNamedEvents(result.Poset, "provider", "ProviderRecovered")
	helperReturns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "provider", "Helper'Return"))
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Recover'Return"))
	continued := sourceNamedEvents(result.Poset, "worker", "HandlerContinued")
	if len(failure) != 1 || len(calls) != 1 || len(helperCalls) != 1 || len(body) != 1 ||
		len(helperReturns) != 1 || len(returns) != 1 || len(continued) != 1 {
		t.Fatalf("Failure/Recover'Call/Helper'Call/ProviderRecovered/Helper'Return/Recover'Return/HandlerContinued=%d/%d/%d/%d/%d/%d/%d",
			len(failure), len(calls), len(helperCalls), len(body), len(helperReturns), len(returns), len(continued))
	}
	for index, event := range []*gorapide.Event{body[0], continued[0]} {
		if event.ParamInt("code") != 7 {
			t.Fatalf("event[%d] code=%#v", index, event.Params)
		}
	}
	assertOnlyDirectCause(t, result.Poset, calls[0], failure[0])
	assertOnlyDirectCause(t, result.Poset, helperCalls[0], calls[0])
	assertOnlyDirectCause(t, result.Poset, body[0], helperCalls[0])
	assertOnlyDirectCause(t, result.Poset, helperReturns[0], body[0])
	assertOnlyDirectCause(t, result.Poset, returns[0], helperReturns[0])
	assertOnlyDirectCause(t, result.Poset, continued[0], returns[0])
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	provider := lifecycleModuleByOccurrence(t, result, "component:provider")
	if worker.State == arch.ModuleTerminatedState || worker.TerminationEventID != "" ||
		provider.State == arch.ModuleTerminatedState || provider.TerminationEventID != "" {
		t.Fatalf("cross-component handler recovery lifecycles worker=%#v provider=%#v", worker, provider)
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("cross-component handler recovery propagated=%#v", result.ExceptionPropagations)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed cross-component module-handler call")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("cross-component module-handler call replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerRoutedFunctionLocalNewFinalizesAndContinues(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out HandlerContinued();
  requires Recover : function();
end interface Worker;
type Provider is interface
  action out Allocated(value : Provider);
  provides Recover : function();
end interface Provider;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
serial when Fail do raise Failure; end when;
handler is Failure =>
  Recover();
  HandlerContinued();
end module WorkerModule;
module ProviderModule() return Provider is
  Recover : function() is
    Child : Provider is New();
  begin
    Allocated(Child);
  end function Recover;
end module ProviderModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
  provider : Provider is ProviderModule();
connect
  stimulus.Fail to worker.Fail;
  worker.Recover to provider.Recover;
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
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	calls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Recover'Call"))
	allocated := sourceNamedEvents(result.Poset, "provider", "Allocated")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Recover'Return"))
	continued := sourceNamedEvents(result.Poset, "worker", "HandlerContinued")
	if len(failure) != 1 || len(calls) != 1 || len(allocated) != 1 || len(returns) != 1 || len(continued) != 1 {
		t.Fatalf("Failure/Recover'Call/Allocated/Recover'Return/HandlerContinued=%d/%d/%d/%d/%d",
			len(failure), len(calls), len(allocated), len(returns), len(continued))
	}
	value, exists := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || child.Identity() == "" {
		t.Fatalf("handler provider allocation=%#v", value)
	}
	provider := lifecycleModuleByOccurrence(t, result, "component:provider")
	var childLifecycle *arch.ModuleLifecycleRecord
	for index := range result.Modules {
		if result.Modules[index].ModuleID == child.Identity() {
			childLifecycle = &result.Modules[index]
			break
		}
	}
	if childLifecycle == nil || childLifecycle.Kind != "allocator-module" ||
		childLifecycle.Parent != provider.ModuleID || childLifecycle.State != arch.ModuleFinalizedState ||
		childLifecycle.Namable || childLifecycle.FinishEventID == "" {
		t.Fatalf("handler provider child lifecycle=%#v", childLifecycle)
	}
	start, _ := result.Poset.Get(gorapide.EventID(childLifecycle.StartEventID))
	finish, _ := result.Poset.Get(gorapide.EventID(childLifecycle.FinishEventID))
	if start == nil || finish == nil {
		t.Fatalf("handler provider child Start/Finish=%#v/%#v", start, finish)
	}
	assertOnlyDirectCause(t, result.Poset, calls[0], failure[0])
	assertOnlyDirectCause(t, result.Poset, start, calls[0])
	assertOnlyDirectCause(t, result.Poset, allocated[0], start)
	assertOnlyDirectCause(t, result.Poset, finish, allocated[0])
	assertOnlyDirectCause(t, result.Poset, returns[0], allocated[0])
	assertOnlyDirectCause(t, result.Poset, continued[0], returns[0])
	if !result.Poset.IsCausallyIndependent(finish.ID, returns[0].ID) ||
		!result.Poset.IsCausallyIndependent(finish.ID, continued[0].ID) {
		t.Fatal("function-local finalization was falsely ordered with handler return/continuation")
	}
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State == arch.ModuleTerminatedState || provider.State == arch.ModuleTerminatedState {
		t.Fatalf("handler provider allocation terminated worker/provider=%#v/%#v", worker, provider)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed handler provider allocation")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("handler provider allocation replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerFailedCreationAbandonsActivationExactly(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out BeforeHandler();
  action out WrongSameHandler();
  action out UnreachableInHandler();
  requires Spawn : function(Depth : Integer);
end interface Worker;
type Provider is interface
  action out Before(depth : Integer);
  action out UnreachableInFunction();
  provides Spawn : function(Depth : Integer);
end interface Provider;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception TriggerFailure;
serial when Fail do raise TriggerFailure; end when;
handler
  is TriggerFailure =>
    BeforeHandler();
    Spawn(0);
    UnreachableInHandler();
  else WrongSameHandler();
end module WorkerModule;
module ProviderModule() return Provider is
  exception CreationFailure;
  Spawn : function(Depth : Integer) is
    Child : Provider is New(Depth is Depth);
  begin
    UnreachableInFunction();
  end function Spawn;
initial (Depth : Integer is -1)
  if Depth >= 0 then
    Before(Depth);
    if Depth = 0 then raise CreationFailure; end if;
  end if;
end module ProviderModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
  provider : Provider is ProviderModule();
connect
  stimulus.Fail to worker.Fail;
  worker.Spawn to provider.Spawn;
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
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, firstErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if firstErr != nil || secondErr != nil {
		t.Fatalf("module-handler failed creation first=%v second=%v", firstErr, secondErr)
	}

	trigger := sourceNamedEvents(first.Poset, "worker", "TriggerFailure")
	beforeHandler := sourceNamedEvents(first.Poset, "worker", "BeforeHandler")
	calls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "worker", "Spawn'Call"))
	returns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "worker", "Spawn'Return"))
	if len(trigger) != 1 || len(beforeHandler) != 1 || len(calls) != 1 || len(returns) != 0 ||
		len(sourceNamedEvents(first.Poset, "worker", "WrongSameHandler")) != 0 ||
		len(sourceNamedEvents(first.Poset, "provider", "UnreachableInFunction")) != 0 ||
		len(sourceNamedEvents(first.Poset, "worker", "UnreachableInHandler")) != 0 {
		t.Fatalf("Trigger/BeforeHandler/Spawn'Call/Return/Wrong/FunctionRemainder/HandlerRemainder=%d/%d/%d/%d/%d/%d/%d",
			len(trigger), len(beforeHandler), len(calls), len(returns),
			len(sourceNamedEvents(first.Poset, "worker", "WrongSameHandler")),
			len(sourceNamedEvents(first.Poset, "provider", "UnreachableInFunction")),
			len(sourceNamedEvents(first.Poset, "worker", "UnreachableInHandler")))
	}
	assertOnlyDirectCause(t, first.Poset, beforeHandler[0], trigger[0])
	assertOnlyDirectCause(t, first.Poset, calls[0], beforeHandler[0])

	var child *arch.ModuleLifecycleRecord
	for index := range first.Modules {
		if first.Modules[index].Kind == "allocator-module" {
			if child != nil {
				t.Fatalf("module-handler failure allocated multiple children: %#v", first.Modules)
			}
			child = &first.Modules[index]
		}
	}
	if child == nil {
		t.Fatal("module-handler failure has no allocated child lifecycle")
	}
	start, startOK := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishOK := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	before := sourceNamedEvents(first.Poset, child.ModuleID, "Before")
	failure := sourceNamedEvents(first.Poset, child.ModuleID, "CreationFailure")
	if !startOK || !finishOK || len(before) != 1 || before[0].ParamInt("depth") != 0 || len(failure) != 1 {
		t.Fatalf("child Start/Before/CreationFailure/Finish=%#v/%#v/%#v/%#v", start, before, failure, finish)
	}
	assertOnlyDirectCause(t, first.Poset, start, calls[0])
	assertOnlyDirectCause(t, first.Poset, before[0], start)
	assertOnlyDirectCause(t, first.Poset, failure[0], before[0])
	assertOnlyDirectCause(t, first.Poset, finish, failure[0])
	if child.State != arch.ModuleFinalizedState || child.Namable ||
		child.TerminationEventID != string(failure[0].ID) || child.FinishEventID == "" {
		t.Fatalf("failed handler child lifecycle=%#v", child)
	}

	worker := lifecycleModuleByOccurrence(t, first, "component:worker")
	provider := lifecycleModuleByOccurrence(t, first, "component:provider")
	root := lifecycleModuleByOccurrence(t, first, "architecture:root")
	if worker.State == arch.ModuleTerminatedState || worker.TerminationEventID != "" ||
		provider.State != arch.ModuleTerminatedState || provider.TerminationEventID != string(failure[0].ID) ||
		root.State != arch.ModuleTerminatedState || root.TerminationEventID != string(failure[0].ID) {
		t.Fatalf("failed handler worker/provider/root lifecycle worker=%#v provider=%#v root=%#v",
			worker, provider, root)
	}
	childPropagation := exceptionPropagationBySource(t, first, child.ModuleID)
	if childPropagation.ExceptionEventID != string(failure[0].ID) || len(childPropagation.Targets) != 1 ||
		childPropagation.Targets[0].ModuleID != provider.ModuleID ||
		childPropagation.Targets[0].Disposition != "delivered" ||
		strings.Join(childPropagation.Targets[0].Relations, ",") != "parent" {
		t.Fatalf("failed handler child propagation=%#v", childPropagation)
	}
	providerPropagation := exceptionPropagationBySource(t, first, provider.ModuleID)
	if providerPropagation.ExceptionEventID != string(failure[0].ID) || providerPropagation.Exception != "CreationFailure" ||
		len(providerPropagation.Targets) != 1 || providerPropagation.Targets[0].ModuleID != root.ModuleID ||
		providerPropagation.Targets[0].Disposition != "delivered" ||
		strings.Join(providerPropagation.Targets[0].Relations, ",") != "parent" {
		t.Fatalf("failed handler provider propagation=%#v", providerPropagation)
	}
	handlerFirings := 0
	for _, firing := range first.Firings {
		if firing.Transition != "module-handler" || firing.Target != "worker" {
			continue
		}
		handlerFirings++
		if firing.Completion != "exception" || firing.ExceptionEventID != string(failure[0].ID) ||
			len(firing.MatchedEvents) != 1 || firing.MatchedEvents[0] != string(trigger[0].ID) {
			t.Fatalf("failed handler firing=%#v", firing)
		}
	}
	if handlerFirings != 1 {
		t.Fatalf("failed creation re-entered module handler: firings=%d", handlerFirings)
	}
	processes := 0
	for _, process := range first.Processes {
		if process.ComponentID != "worker" {
			continue
		}
		processes++
		if !process.Terminated || process.Completion != "exception" ||
			process.ExceptionEventID != string(trigger[0].ID) {
			t.Fatalf("handler-raising process witness=%#v", process)
		}
	}
	if processes != 1 {
		t.Fatalf("worker process records=%d", processes)
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
		t.Fatal("GOMAXPROCS changed module-handler failed creation")
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
		t.Fatal("module-handler failed-creation replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerHandledNestedFailedCreationAbandonsActivationExactly(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out BeforeHandler();
  action out WrongSameHandler();
  action out UnreachableInHandler();
  requires Spawn : function(Depth : Integer);
end interface Worker;
type Provider is interface
  action out Allocated(value : Provider);
  action out Before(depth : Integer);
  action out Recovered();
  action out Closing();
  action out UnreachableInFunction();
  provides Spawn : function(Depth : Integer);
end interface Provider;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception TriggerFailure;
serial when Fail do raise TriggerFailure; end when;
handler
  is TriggerFailure =>
    BeforeHandler();
    Spawn(1);
    UnreachableInHandler();
  else WrongSameHandler();
end module WorkerModule;
module ProviderModule() return Provider is
  exception CreationFailure;
  Spawn : function(Depth : Integer) is
    Child : Provider is New(Depth is Depth);
  begin
    UnreachableInFunction();
  end function Spawn;
initial (Depth : Integer is -1)
  if Depth >= 0 then
    Before(Depth);
    if Depth > 0 then Allocated(New(Depth is Depth - 1));
    elsif Depth = 0 then raise CreationFailure;
    end if;
  end if;
handler
  is CreationFailure => Recovered();
final
  Closing();
end module ProviderModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
  provider : Provider is ProviderModule();
connect
  stimulus.Fail to worker.Fail;
  worker.Spawn to provider.Spawn;
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
		digest, arch.ExecutionLimits{MaxFirings: 180, MaxStatements: 240},
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
	)
	previous := runtime.GOMAXPROCS(1)
	first, firstErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if firstErr != nil || secondErr != nil {
		t.Fatalf("handled nested module-handler failure first=%v second=%v", firstErr, secondErr)
	}

	trigger := sourceNamedEvents(first.Poset, "worker", "TriggerFailure")
	beforeHandler := sourceNamedEvents(first.Poset, "worker", "BeforeHandler")
	calls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "worker", "Spawn'Call"))
	returns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "worker", "Spawn'Return"))
	if len(trigger) != 1 || len(beforeHandler) != 1 || len(calls) != 1 || len(returns) != 0 ||
		len(sourceNamedEvents(first.Poset, "worker", "WrongSameHandler")) != 0 ||
		len(sourceNamedEvents(first.Poset, "provider", "UnreachableInFunction")) != 0 ||
		len(sourceNamedEvents(first.Poset, "worker", "UnreachableInHandler")) != 0 ||
		len(sourceNamedEvents(first.Poset, "provider", "Allocated")) != 0 {
		t.Fatalf("Trigger/BeforeHandler/Spawn'Call/Return/Wrong/FunctionRemainder/HandlerRemainder/Allocated=%d/%d/%d/%d/%d/%d/%d/%d",
			len(trigger), len(beforeHandler), len(calls), len(returns),
			len(sourceNamedEvents(first.Poset, "worker", "WrongSameHandler")),
			len(sourceNamedEvents(first.Poset, "provider", "UnreachableInFunction")),
			len(sourceNamedEvents(first.Poset, "worker", "UnreachableInHandler")),
			len(sourceNamedEvents(first.Poset, "provider", "Allocated")))
	}
	assertOnlyDirectCause(t, first.Poset, beforeHandler[0], trigger[0])
	assertOnlyDirectCause(t, first.Poset, calls[0], beforeHandler[0])

	byDepth := make(map[int]*arch.ModuleLifecycleRecord)
	for index := range first.Modules {
		lifecycle := &first.Modules[index]
		if lifecycle.Kind != "allocator-module" {
			continue
		}
		before := sourceNamedEvents(first.Poset, lifecycle.ModuleID, "Before")
		if len(before) != 1 {
			t.Fatalf("handled nested module %q Before=%#v", lifecycle.ModuleID, before)
		}
		byDepth[before[0].ParamInt("depth")] = lifecycle
	}
	if len(byDepth) != 2 || byDepth[0] == nil || byDepth[1] == nil {
		t.Fatalf("handled nested module-handler children=%#v", byDepth)
	}
	leaf := byDepth[0]
	parent := byDepth[1]
	failure := sourceNamedEvents(first.Poset, leaf.ModuleID, "CreationFailure")
	recovered := sourceNamedEvents(first.Poset, parent.ModuleID, "Recovered")
	parentClosing := sourceNamedEvents(first.Poset, parent.ModuleID, "Closing")
	leafClosing := sourceNamedEvents(first.Poset, leaf.ModuleID, "Closing")
	if len(failure) != 1 || len(recovered) != 1 || len(parentClosing) != 1 || len(leafClosing) != 0 {
		t.Fatalf("handled nested Failure/Recovered/parent Closing/leaf Closing=%d/%d/%d/%d",
			len(failure), len(recovered), len(parentClosing), len(leafClosing))
	}
	assertOnlyDirectCause(t, first.Poset, recovered[0], failure[0])
	assertOnlyDirectCause(t, first.Poset, parentClosing[0], recovered[0])
	leafFinish, leafFinishOK := first.Poset.Get(gorapide.EventID(leaf.FinishEventID))
	parentFinish, parentFinishOK := first.Poset.Get(gorapide.EventID(parent.FinishEventID))
	if !leafFinishOK || !parentFinishOK {
		t.Fatalf("handled nested leaf/parent Finish=%#v/%#v", leafFinish, parentFinish)
	}
	assertOnlyDirectCause(t, first.Poset, leafFinish, failure[0])
	assertOnlyDirectCause(t, first.Poset, parentFinish, parentClosing[0])
	if leaf.State != arch.ModuleFinalizedState || leaf.TerminationEventID != string(failure[0].ID) || leaf.Namable ||
		parent.State != arch.ModuleFinalizedState || parent.TerminationEventID != "" || parent.Namable {
		t.Fatalf("handled nested leaf/parent lifecycle leaf=%#v parent=%#v", leaf, parent)
	}
	leafPropagation := exceptionPropagationBySource(t, first, leaf.ModuleID)
	if len(leafPropagation.Targets) != 1 || leafPropagation.Targets[0].ModuleID != parent.ModuleID ||
		leafPropagation.Targets[0].Disposition != "handled" ||
		strings.Join(leafPropagation.Targets[0].Relations, ",") != "parent" {
		t.Fatalf("handled nested leaf propagation=%#v", leafPropagation)
	}
	for _, propagation := range first.ExceptionPropagations {
		if propagation.SourceModuleID == parent.ModuleID {
			t.Fatalf("handled nested parent propagated outward=%#v", propagation)
		}
	}
	worker := lifecycleModuleByOccurrence(t, first, "component:worker")
	provider := lifecycleModuleByOccurrence(t, first, "component:provider")
	root := lifecycleModuleByOccurrence(t, first, "architecture:root")
	if worker.State == arch.ModuleTerminatedState || worker.TerminationEventID != "" ||
		provider.State == arch.ModuleTerminatedState || provider.TerminationEventID != "" ||
		root.State == arch.ModuleTerminatedState || root.TerminationEventID != "" {
		t.Fatalf("handled nested failure escaped worker/provider/root=%#v/%#v/%#v", worker, provider, root)
	}
	workerFirings := 0
	for _, firing := range first.Firings {
		if firing.Transition != "module-handler" || firing.Target != "worker" {
			continue
		}
		workerFirings++
		if firing.Completion != "exception" || firing.ExceptionEventID != string(failure[0].ID) ||
			len(firing.MatchedEvents) != 1 || firing.MatchedEvents[0] != string(trigger[0].ID) {
			t.Fatalf("handled nested outer handler firing=%#v", firing)
		}
	}
	if workerFirings != 1 {
		t.Fatalf("handled nested outer handler firings=%d", workerFirings)
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
		t.Fatal("GOMAXPROCS changed handled nested module-handler failure")
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
		t.Fatal("handled nested module-handler failure replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerLinkedFailedCreationBypassesActiveHandlerExactly(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Accept(value : Provider);
  action out Ready();
  action out BeforeHandler();
  action out WrongSameHandler();
  action out UnreachableInHandler();
  requires Spawn : function(Depth : Integer);
end interface Worker;
type Provider is interface
  action out Register(value : Provider);
  action out Before(depth : Integer);
  action out UnreachableInFunction();
  provides Spawn : function(Depth : Integer);
end interface Provider;
module WorkerModule() return Worker is
  exception TriggerFailure;
serial when (?Provider : Provider) Accept(?Provider) do
  Link(?Provider);
  Ready();
  raise TriggerFailure;
end when;
handler
  is TriggerFailure =>
    BeforeHandler();
    Spawn(0);
    UnreachableInHandler();
  else WrongSameHandler();
end module WorkerModule;
module ProviderModule() return Provider is
  exception CreationFailure;
  Spawn : function(Depth : Integer) is
    Child : Provider is New(Depth is Depth);
  begin
    UnreachableInFunction();
  end function Spawn;
initial (Depth : Integer is -1)
  if Depth < 0 then
    Register(Self);
  else
    Before(Depth);
    if Depth = 0 then raise CreationFailure; end if;
  end if;
end module ProviderModule;
architecture System() is
  worker : Worker is WorkerModule();
  provider : Provider is ProviderModule();
connect
  (?Provider : Provider) provider.Register(?Provider) to worker.Accept(?Provider);
  worker.Spawn to provider.Spawn;
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
	)
	previous := runtime.GOMAXPROCS(1)
	first, firstErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if firstErr != nil || secondErr != nil {
		t.Fatalf("linked module-handler failed creation first=%v second=%v", firstErr, secondErr)
	}

	ready := sourceNamedEvents(first.Poset, "worker", "Ready")
	trigger := sourceNamedEvents(first.Poset, "worker", "TriggerFailure")
	beforeHandler := sourceNamedEvents(first.Poset, "worker", "BeforeHandler")
	calls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "worker", "Spawn'Call"))
	returns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "worker", "Spawn'Return"))
	if len(ready) != 1 || len(trigger) != 1 || len(beforeHandler) != 1 || len(calls) != 1 || len(returns) != 0 ||
		len(sourceNamedEvents(first.Poset, "worker", "WrongSameHandler")) != 0 ||
		len(sourceNamedEvents(first.Poset, "provider", "UnreachableInFunction")) != 0 ||
		len(sourceNamedEvents(first.Poset, "worker", "UnreachableInHandler")) != 0 {
		t.Fatalf("Ready/Trigger/BeforeHandler/Spawn'Call/Return/Wrong/FunctionRemainder/HandlerRemainder=%d/%d/%d/%d/%d/%d/%d/%d",
			len(ready), len(trigger), len(beforeHandler), len(calls), len(returns),
			len(sourceNamedEvents(first.Poset, "worker", "WrongSameHandler")),
			len(sourceNamedEvents(first.Poset, "provider", "UnreachableInFunction")),
			len(sourceNamedEvents(first.Poset, "worker", "UnreachableInHandler")))
	}
	assertOnlyDirectCause(t, first.Poset, trigger[0], ready[0])
	assertOnlyDirectCause(t, first.Poset, beforeHandler[0], trigger[0])
	assertOnlyDirectCause(t, first.Poset, calls[0], beforeHandler[0])

	var child *arch.ModuleLifecycleRecord
	for index := range first.Modules {
		if first.Modules[index].Kind == "allocator-module" {
			if child != nil {
				t.Fatalf("linked handler failure allocated multiple children: %#v", first.Modules)
			}
			child = &first.Modules[index]
		}
	}
	if child == nil {
		t.Fatal("linked handler failure has no allocated child lifecycle")
	}
	start, startOK := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishOK := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	before := sourceNamedEvents(first.Poset, child.ModuleID, "Before")
	failure := sourceNamedEvents(first.Poset, child.ModuleID, "CreationFailure")
	if !startOK || !finishOK || len(before) != 1 || before[0].ParamInt("depth") != 0 || len(failure) != 1 {
		t.Fatalf("linked child Start/Before/CreationFailure/Finish=%#v/%#v/%#v/%#v", start, before, failure, finish)
	}
	assertOnlyDirectCause(t, first.Poset, start, calls[0])
	assertOnlyDirectCause(t, first.Poset, before[0], start)
	assertOnlyDirectCause(t, first.Poset, failure[0], before[0])
	assertOnlyDirectCause(t, first.Poset, finish, failure[0])
	if child.State != arch.ModuleFinalizedState || child.Namable ||
		child.TerminationEventID != string(failure[0].ID) || child.FinishEventID == "" {
		t.Fatalf("linked failed handler child lifecycle=%#v", child)
	}

	worker := lifecycleModuleByOccurrence(t, first, "component:worker")
	provider := lifecycleModuleByOccurrence(t, first, "component:provider")
	root := lifecycleModuleByOccurrence(t, first, "architecture:root")
	if worker.State != arch.ModuleTerminatedState || worker.TerminationEventID != string(failure[0].ID) ||
		provider.State != arch.ModuleTerminatedState || provider.TerminationEventID != string(failure[0].ID) ||
		root.State != arch.ModuleTerminatedState || root.TerminationEventID != string(failure[0].ID) {
		t.Fatalf("linked failed handler worker/provider/root lifecycle worker=%#v provider=%#v root=%#v",
			worker, provider, root)
	}
	childPropagation := exceptionPropagationBySource(t, first, child.ModuleID)
	if childPropagation.ExceptionEventID != string(failure[0].ID) || len(childPropagation.Targets) != 1 ||
		childPropagation.Targets[0].ModuleID != provider.ModuleID ||
		childPropagation.Targets[0].Disposition != "delivered" ||
		strings.Join(childPropagation.Targets[0].Relations, ",") != "parent" {
		t.Fatalf("linked failed handler child propagation=%#v", childPropagation)
	}
	providerPropagation := exceptionPropagationBySource(t, first, provider.ModuleID)
	rootDelivered := false
	workerDelivered := false
	for _, target := range providerPropagation.Targets {
		switch target.ModuleID {
		case root.ModuleID:
			if target.Disposition != "delivered" || strings.Join(target.Relations, ",") != "parent" {
				t.Fatalf("linked failed handler root target=%#v", target)
			}
			rootDelivered = true
		case worker.ModuleID:
			if target.Disposition != "delivered" || strings.Join(target.Relations, ",") != "linked" {
				t.Fatalf("active handler was not bypassed for linked failure: %#v", target)
			}
			workerDelivered = true
		default:
			t.Fatalf("unexpected linked failed-handler target=%#v", target)
		}
	}
	if !rootDelivered || !workerDelivered || providerPropagation.ExceptionEventID != string(failure[0].ID) {
		t.Fatalf("linked failed handler provider propagation=%#v", providerPropagation)
	}
	handlerFirings := 0
	for _, firing := range first.Firings {
		if firing.Transition != "module-handler" || firing.Target != "worker" {
			continue
		}
		handlerFirings++
		if firing.Completion != "exception" || firing.ExceptionEventID != string(failure[0].ID) ||
			len(firing.MatchedEvents) != 1 || firing.MatchedEvents[0] != string(trigger[0].ID) {
			t.Fatalf("linked failed handler firing=%#v", firing)
		}
	}
	if handlerFirings != 1 {
		t.Fatalf("linked failed creation re-entered module handler: firings=%d", handlerFirings)
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
		t.Fatal("GOMAXPROCS changed linked module-handler failed creation")
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
		t.Fatal("linked module-handler failed-creation replay changed canonical bytes")
	}
}

func TestSourcePropagatedModuleHandlerFailedCreationMarksHandlerRaisedExactly(t *testing.T) {
	source := []byte(`
type Producer is interface
  action out Register(value : Producer);
  action in Go();
end interface Producer;
type Provider is interface
  action out Register(value : Provider);
  action out Before(depth : Integer);
  action out UnreachableInFunction();
  provides Spawn : function(Depth : Integer);
end interface Provider;
type Monitor is interface
  action in Setup(producer : Producer; provider : Provider);
  action out Ready();
  action out BeforeHandler();
  action out UnreachableInHandler();
  requires Spawn : function(Depth : Integer);
end interface Monitor;
module ProducerModule() return Producer is
  exception ProducerFailure;
initial Register(Self);
serial when Go do raise ProducerFailure; end when;
end module ProducerModule;
module ProviderModule() return Provider is
  exception CreationFailure;
  Spawn : function(Depth : Integer) is
    Child : Provider is New(Depth is Depth);
  begin
    UnreachableInFunction();
  end function Spawn;
initial (Depth : Integer is -1)
  if Depth < 0 then
    Register(Self);
  else
    Before(Depth);
    if Depth = 0 then raise CreationFailure; end if;
  end if;
end module ProviderModule;
module MonitorModule() return Monitor is
serial when (?Producer : Producer; ?Provider : Provider) Setup(?Producer, ?Provider) do
  Link(?Producer);
  Link(?Provider);
  Ready();
end when;
handler
  else
    BeforeHandler();
    Spawn(0);
    UnreachableInHandler();
end module MonitorModule;
architecture System() is
  producer : Producer is ProducerModule();
  provider : Provider is ProviderModule();
  monitor : Monitor is MonitorModule();
connect
  (?Producer : Producer; ?Provider : Provider)
    (producer.Register(?Producer) and provider.Register(?Provider))
      => monitor.Setup(?Producer, ?Provider);
  monitor.Ready to producer.Go;
  monitor.Spawn to provider.Spawn;
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
		digest, arch.ExecutionLimits{MaxFirings: 240, MaxStatements: 280},
	)
	previous := runtime.GOMAXPROCS(1)
	first, firstErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(8)
	second, secondErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if firstErr != nil || secondErr != nil {
		t.Fatalf("propagated module-handler failed creation first=%v second=%v", firstErr, secondErr)
	}

	producerFailure := sourceNamedEvents(first.Poset, "producer", "ProducerFailure")
	beforeHandler := sourceNamedEvents(first.Poset, "monitor", "BeforeHandler")
	calls := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "monitor", "Spawn'Call"))
	returns := distinctAllocatorEvents(sourceNamedEvents(first.Poset, "monitor", "Spawn'Return"))
	if len(producerFailure) != 1 || len(beforeHandler) != 1 || len(calls) != 1 || len(returns) != 0 ||
		len(sourceNamedEvents(first.Poset, "provider", "UnreachableInFunction")) != 0 ||
		len(sourceNamedEvents(first.Poset, "monitor", "UnreachableInHandler")) != 0 {
		t.Fatalf("ProducerFailure/BeforeHandler/Spawn'Call/Return/FunctionRemainder/HandlerRemainder=%d/%d/%d/%d/%d/%d",
			len(producerFailure), len(beforeHandler), len(calls), len(returns),
			len(sourceNamedEvents(first.Poset, "provider", "UnreachableInFunction")),
			len(sourceNamedEvents(first.Poset, "monitor", "UnreachableInHandler")))
	}
	assertOnlyDirectCause(t, first.Poset, beforeHandler[0], producerFailure[0])
	assertOnlyDirectCause(t, first.Poset, calls[0], beforeHandler[0])

	var child *arch.ModuleLifecycleRecord
	for index := range first.Modules {
		if first.Modules[index].Kind == "allocator-module" {
			if child != nil {
				t.Fatalf("propagated failed handler allocated multiple children: %#v", first.Modules)
			}
			child = &first.Modules[index]
		}
	}
	if child == nil {
		t.Fatal("propagated failed handler has no allocated child lifecycle")
	}
	start, startOK := first.Poset.Get(gorapide.EventID(child.StartEventID))
	finish, finishOK := first.Poset.Get(gorapide.EventID(child.FinishEventID))
	before := sourceNamedEvents(first.Poset, child.ModuleID, "Before")
	creationFailure := sourceNamedEvents(first.Poset, child.ModuleID, "CreationFailure")
	if !startOK || !finishOK || len(before) != 1 || before[0].ParamInt("depth") != 0 || len(creationFailure) != 1 {
		t.Fatalf("propagated child Start/Before/CreationFailure/Finish=%#v/%#v/%#v/%#v",
			start, before, creationFailure, finish)
	}
	assertOnlyDirectCause(t, first.Poset, start, calls[0])
	assertOnlyDirectCause(t, first.Poset, before[0], start)
	assertOnlyDirectCause(t, first.Poset, creationFailure[0], before[0])
	assertOnlyDirectCause(t, first.Poset, finish, creationFailure[0])

	producer := lifecycleModuleByOccurrence(t, first, "component:producer")
	provider := lifecycleModuleByOccurrence(t, first, "component:provider")
	monitor := lifecycleModuleByOccurrence(t, first, "component:monitor")
	root := lifecycleModuleByOccurrence(t, first, "architecture:root")
	if producer.State != arch.ModuleTerminatedState || producer.TerminationEventID != string(producerFailure[0].ID) ||
		provider.State != arch.ModuleTerminatedState || provider.TerminationEventID != string(creationFailure[0].ID) ||
		monitor.State != arch.ModuleTerminatedState || monitor.TerminationEventID != string(creationFailure[0].ID) ||
		root.State != arch.ModuleTerminatedState || root.TerminationEventID != string(producerFailure[0].ID) {
		t.Fatalf("propagated failed handler lifecycles producer=%#v provider=%#v monitor=%#v root=%#v",
			producer, provider, monitor, root)
	}
	producerPropagation := exceptionPropagationBySource(t, first, producer.ModuleID)
	rootOriginal := false
	monitorHandlerRaised := false
	for _, target := range producerPropagation.Targets {
		switch target.ModuleID {
		case root.ModuleID:
			if target.Disposition != "delivered" || strings.Join(target.Relations, ",") != "parent" {
				t.Fatalf("producer failure root target=%#v", target)
			}
			rootOriginal = true
		case monitor.ModuleID:
			if target.Disposition != "handler-raised" || strings.Join(target.Relations, ",") != "linked" {
				t.Fatalf("failed handler incoming target=%#v", target)
			}
			monitorHandlerRaised = true
		default:
			t.Fatalf("unexpected producer failure target=%#v", target)
		}
	}
	if !rootOriginal || !monitorHandlerRaised ||
		producerPropagation.ExceptionEventID != string(producerFailure[0].ID) {
		t.Fatalf("producer failed-handler propagation=%#v", producerPropagation)
	}
	providerPropagation := exceptionPropagationBySource(t, first, provider.ModuleID)
	rootIgnored := false
	monitorDelivered := false
	for _, target := range providerPropagation.Targets {
		switch target.ModuleID {
		case root.ModuleID:
			if target.Disposition != "ignored-already-terminated" || strings.Join(target.Relations, ",") != "parent" {
				t.Fatalf("creation failure root target=%#v", target)
			}
			rootIgnored = true
		case monitor.ModuleID:
			if target.Disposition != "delivered" || strings.Join(target.Relations, ",") != "linked" {
				t.Fatalf("creation failure active-handler target=%#v", target)
			}
			monitorDelivered = true
		default:
			t.Fatalf("unexpected creation failure target=%#v", target)
		}
	}
	if !rootIgnored || !monitorDelivered ||
		providerPropagation.ExceptionEventID != string(creationFailure[0].ID) {
		t.Fatalf("provider failed-handler propagation=%#v", providerPropagation)
	}
	handlerFirings := 0
	for _, firing := range first.Firings {
		if firing.Transition != "module-handler" || firing.Target != "monitor" {
			continue
		}
		handlerFirings++
		if firing.Completion != "exception" || firing.ExceptionEventID != string(creationFailure[0].ID) ||
			len(firing.MatchedEvents) != 1 || firing.MatchedEvents[0] != string(producerFailure[0].ID) {
			t.Fatalf("propagated failed handler firing=%#v", firing)
		}
	}
	if handlerFirings != 1 {
		t.Fatalf("propagated failed creation re-entered handler: firings=%d", handlerFirings)
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
		t.Fatal("GOMAXPROCS changed propagated module-handler failed creation")
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
		t.Fatal("propagated module-handler failed-creation replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerRoutedFunctionLocalNewFinalizesOnException(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out BeforeCall();
  action out UnreachableInHandler();
  requires Escalate : function();
end interface Worker;
type Provider is interface
  action out Allocated(value : Provider);
  action out UnreachableInFunction();
  provides Escalate : function();
end interface Provider;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    BeforeCall();
    Escalate();
    UnreachableInHandler();
end module WorkerModule;
module ProviderModule() return Provider is
  exception Escalated(code : Integer);
  Escalate : function() is
    Child : Provider is New();
  begin
    Allocated(Child);
    raise Escalated(9);
    UnreachableInFunction();
  end function Escalate;
end module ProviderModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
  provider : Provider is ProviderModule();
connect
  stimulus.Fail to worker.Fail;
  worker.Escalate to provider.Escalate;
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
	journal := arch.NewExecutionJournal(digest, 70,
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	before := sourceNamedEvents(result.Poset, "worker", "BeforeCall")
	calls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Escalate'Call"))
	allocated := sourceNamedEvents(result.Poset, "provider", "Allocated")
	escalated := sourceNamedEvents(result.Poset, "provider", "Escalated")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Escalate'Return"))
	if len(failure) != 1 || len(before) != 1 || len(calls) != 1 || len(allocated) != 1 ||
		len(escalated) != 1 || len(returns) != 0 ||
		len(sourceNamedEvents(result.Poset, "provider", "UnreachableInFunction")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "UnreachableInHandler")) != 0 {
		t.Fatalf("Failure/BeforeCall/Escalate'Call/Allocated/Escalated/Return=%d/%d/%d/%d/%d/%d",
			len(failure), len(before), len(calls), len(allocated), len(escalated), len(returns))
	}
	if escalated[0].ParamInt("code") != 9 {
		t.Fatalf("remote Escalated code=%#v", escalated[0].Params)
	}
	assertOnlyDirectCause(t, result.Poset, before[0], failure[0])
	assertOnlyDirectCause(t, result.Poset, calls[0], before[0])
	value, exists := allocated[0].Param("value")
	child, ok := value.(gorapide.RapideModuleValue)
	if !exists || !ok || child.Identity() == "" {
		t.Fatalf("exceptional handler provider allocation=%#v", value)
	}
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	provider := lifecycleModuleByOccurrence(t, result, "component:provider")
	var childLifecycle *arch.ModuleLifecycleRecord
	for index := range result.Modules {
		if result.Modules[index].ModuleID == child.Identity() {
			childLifecycle = &result.Modules[index]
			break
		}
	}
	if childLifecycle == nil || childLifecycle.Parent != provider.ModuleID ||
		childLifecycle.State != arch.ModuleFinalizedState || childLifecycle.FinishEventID == "" {
		t.Fatalf("exceptional handler provider child lifecycle=%#v", childLifecycle)
	}
	start, _ := result.Poset.Get(gorapide.EventID(childLifecycle.StartEventID))
	finish, _ := result.Poset.Get(gorapide.EventID(childLifecycle.FinishEventID))
	if start == nil || finish == nil {
		t.Fatalf("exceptional handler child Start/Finish=%#v/%#v", start, finish)
	}
	assertOnlyDirectCause(t, result.Poset, start, calls[0])
	assertOnlyDirectCause(t, result.Poset, allocated[0], start)
	assertOnlyDirectCause(t, result.Poset, escalated[0], allocated[0])
	assertOnlyDirectCause(t, result.Poset, finish, escalated[0])
	if worker.State != arch.ModuleTerminatedState || worker.TerminationEventID != string(escalated[0].ID) ||
		provider.State == arch.ModuleTerminatedState || provider.TerminationEventID != "" {
		t.Fatalf("remote handler exception lifecycles worker=%#v provider=%#v", worker, provider)
	}
	propagation := exceptionPropagationBySource(t, result, worker.ModuleID)
	if propagation.ExceptionEventID != string(escalated[0].ID) || propagation.Exception != "Escalated" {
		t.Fatalf("remote handler exception propagation=%#v", propagation)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed cross-component module-handler exception")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("cross-component module-handler exception replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerFunctionExceptionEscapesAtCallPoint(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out BeforeCall();
  action out UnreachableInFunction();
  action out UnreachableInHandler();
  action out WrongSameHandler();
  provides Escalate : function();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  exception Escalated(code : Integer);
  Escalate : function() is
  begin
    raise Escalated(9);
    UnreachableInFunction();
  end function Escalate;
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    BeforeCall();
    Escalate();
    UnreachableInHandler();
  is Escalated => WrongSameHandler();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	before := sourceNamedEvents(result.Poset, "worker", "BeforeCall")
	calls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Escalate'Call"))
	escalated := sourceNamedEvents(result.Poset, "worker", "Escalated")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Escalate'Return"))
	if len(failure) != 1 || len(before) != 1 || len(calls) != 1 || len(escalated) != 1 || len(returns) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "UnreachableInFunction")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "UnreachableInHandler")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "WrongSameHandler")) != 0 {
		t.Fatalf("Failure/BeforeCall/Escalate'Call/Escalated/Return=%d/%d/%d/%d/%d",
			len(failure), len(before), len(calls), len(escalated), len(returns))
	}
	if escalated[0].ParamInt("code") != 9 {
		t.Fatalf("Escalated code=%#v", escalated[0].Params)
	}
	assertOnlyDirectCause(t, result.Poset, before[0], failure[0])
	assertOnlyDirectCause(t, result.Poset, calls[0], before[0])
	assertOnlyDirectCause(t, result.Poset, escalated[0], calls[0])
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State != arch.ModuleTerminatedState || worker.TerminationEventID != string(escalated[0].ID) {
		t.Fatalf("handler function exception lifecycle=%#v", worker)
	}
	propagation := exceptionPropagationBySource(t, result, worker.ModuleID)
	if propagation.ExceptionEventID != string(escalated[0].ID) || propagation.Exception != "Escalated" {
		t.Fatalf("handler function exception propagation=%#v", propagation)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed module-handler function exception")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("module-handler function exception replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerNestedDoRecoversAndContinues(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out BeforeNestedDo();
  action out ProtectedUnreachable();
  action out NestedRecovered(code : Integer);
  action out NestedContinued();
  action out HandlerContinued();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  exception Inner(code : Integer);
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    BeforeNestedDo();
    do
      do
        raise Inner(7);
        ProtectedUnreachable();
      handler
        is Inner(?Code) => NestedRecovered(?Code);
      end do;
      NestedContinued();
    end do;
    HandlerContinued();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
	journal := arch.NewExecutionJournal(digest, 70,
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	before := sourceNamedEvents(result.Poset, "worker", "BeforeNestedDo")
	inner := sourceNamedEvents(result.Poset, "worker", "Inner")
	recovered := sourceNamedEvents(result.Poset, "worker", "NestedRecovered")
	nestedContinued := sourceNamedEvents(result.Poset, "worker", "NestedContinued")
	handlerContinued := sourceNamedEvents(result.Poset, "worker", "HandlerContinued")
	if len(failure) != 1 || len(before) != 1 || len(inner) != 1 || len(recovered) != 1 ||
		len(nestedContinued) != 1 || len(handlerContinued) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "ProtectedUnreachable")) != 0 {
		t.Fatalf("Failure/Before/Inner/Recovered/NestedContinued/HandlerContinued=%d/%d/%d/%d/%d/%d",
			len(failure), len(before), len(inner), len(recovered), len(nestedContinued), len(handlerContinued))
	}
	if recovered[0].ParamInt("code") != 7 {
		t.Fatalf("nested handler binding=%#v", recovered[0].Params)
	}
	assertOnlyDirectCause(t, result.Poset, before[0], failure[0])
	assertOnlyDirectCause(t, result.Poset, inner[0], before[0])
	assertOnlyDirectCause(t, result.Poset, recovered[0], inner[0])
	assertOnlyDirectCause(t, result.Poset, nestedContinued[0], recovered[0])
	assertOnlyDirectCause(t, result.Poset, handlerContinued[0], nestedContinued[0])
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State == arch.ModuleTerminatedState || worker.TerminationEventID != "" {
		t.Fatalf("nested module-handler recovery terminated module=%#v", worker)
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("nested module-handler recovery propagated=%#v", result.ExceptionPropagations)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed nested module-handler recovery")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("nested module-handler recovery replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerDeclarationBearingDoUsesExactLexicalExceptions(t *testing.T) {
	source := []byte(`
type Worker is interface
  exception InterfaceNoise;
  action in Fail();
  action out NamedRecovered(code : Integer);
  action out UnnamedRecovered(code : Integer);
  action out HandlerContinued();
  action out Wrong();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure(code : Integer);
serial when Fail do raise Failure(code is 1); end when;
handler
  is Failure(code is ?Original) =>
    Named: declare exception Failure(code : Integer); do
      raise Named::Failure(code is 2);
    handler
      is WorkerModule::Failure(code is ?Code) => Wrong();
      is WorkerModule::Named::Failure(code is ?Code) => NamedRecovered(?Code);
    end do Named;
    declare exception Failure(code : Integer); do
      raise Failure(code is 3);
    handler
      is WorkerModule::Failure(code is ?Code) => Wrong();
      is Failure(code is ?Code) => UnnamedRecovered(?Code);
    end do;
    HandlerContinued();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failures := sourceNamedEvents(result.Poset, "worker", "Failure")
	named := sourceNamedEvents(result.Poset, "worker", "NamedRecovered")
	unnamed := sourceNamedEvents(result.Poset, "worker", "UnnamedRecovered")
	continued := sourceNamedEvents(result.Poset, "worker", "HandlerContinued")
	if len(failures) != 3 || len(named) != 1 || len(unnamed) != 1 || len(continued) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
		t.Fatalf("Failure/Named/Unnamed/Continued=%d/%d/%d/%d",
			len(failures), len(named), len(unnamed), len(continued))
	}
	byCode := make(map[int]*gorapide.Event, len(failures))
	for _, failure := range failures {
		byCode[failure.ParamInt("code")] = failure
	}
	if byCode[1] == nil || byCode[2] == nil || byCode[3] == nil ||
		named[0].ParamInt("code") != 2 || unnamed[0].ParamInt("code") != 3 {
		t.Fatalf("exact lexical exception events=%#v named=%#v unnamed=%#v", byCode, named[0].Params, unnamed[0].Params)
	}
	assertOnlyDirectCause(t, result.Poset, byCode[2], byCode[1])
	assertOnlyDirectCause(t, result.Poset, named[0], byCode[2])
	assertOnlyDirectCause(t, result.Poset, byCode[3], named[0])
	assertOnlyDirectCause(t, result.Poset, unnamed[0], byCode[3])
	assertOnlyDirectCause(t, result.Poset, continued[0], unnamed[0])
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State == arch.ModuleTerminatedState || worker.TerminationEventID != "" {
		t.Fatalf("declaration-bearing module handler terminated module=%#v", worker)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed declaration-bearing module-handler recovery")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("declaration-bearing module-handler replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerDeclarationBearingDoScopeDoesNotLeak(t *testing.T) {
	_, err := Compile([]byte(`
type Worker is interface action in Fail(); end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
serial when Fail do raise Failure; end when;
handler is Failure =>
  declare exception LocalOnly; do null; end do;
  raise LocalOnly;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
end architecture System;
`), "System")
	if err == nil || !strings.Contains(err.Error(), `undeclared exception "LocalOnly"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestSourceModuleHandlerDeclarationBearingDoIdentityIsCanonical(t *testing.T) {
	build := func(reverse bool) []byte {
		declarations := "exception LocalA; exception LocalB;"
		localChoices := "is LocalA => Seen(1); is LocalB => Seen(2);"
		moduleChoices := `is Alpha =>
    declare ` + declarations + ` do
      raise LocalA;
    handler ` + localChoices + ` end do;
  is Beta => Seen(3);`
		if reverse {
			declarations = "exception LocalB; exception LocalA;"
			localChoices = "is LocalB => Seen(2); is LocalA => Seen(1);"
			moduleChoices = `is Beta => Seen(3);
  is Alpha =>
    declare ` + declarations + ` do
      raise LocalA;
    handler ` + localChoices + ` end do;`
		}
		return []byte(`
type Worker is interface action in Trigger(); action out Seen(value : Integer); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Alpha;
  exception Beta;
serial when Trigger do raise Alpha; end when;
handler ` + moduleChoices + `
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
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
		t.Fatalf("module-handler declaration-bearing do order changed model identity: %s != %s", leftDigest, rightDigest)
	}
}

func TestSourceNestedModuleHandlerReraiseEscapesAllActiveHandlers(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out BeforeReraise();
  action out WrongNestedHandler();
  action out WrongModuleHandler();
  action out UnreachableHandlerRemainder();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  exception Inner;
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    do
      raise Inner;
    handler
      is Inner =>
        BeforeReraise();
        raise;
        WrongNestedHandler();
    end do;
    UnreachableHandlerRemainder();
  is Inner => WrongModuleHandler();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	inner := sourceNamedEvents(result.Poset, "worker", "Inner")
	before := sourceNamedEvents(result.Poset, "worker", "BeforeReraise")
	if len(failure) != 1 || len(inner) != 1 || len(before) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "WrongNestedHandler")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "WrongModuleHandler")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "UnreachableHandlerRemainder")) != 0 {
		t.Fatalf("Failure/Inner/BeforeReraise=%d/%d/%d", len(failure), len(inner), len(before))
	}
	assertOnlyDirectCause(t, result.Poset, inner[0], failure[0])
	assertOnlyDirectCause(t, result.Poset, before[0], inner[0])
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State != arch.ModuleTerminatedState || worker.TerminationEventID != string(inner[0].ID) {
		t.Fatalf("nested re-raise lifecycle=%#v", worker)
	}
	propagation := exceptionPropagationBySource(t, result, worker.ModuleID)
	if propagation.ExceptionEventID != string(inner[0].ID) || propagation.Exception != "Inner" {
		t.Fatalf("nested re-raise propagation=%#v", propagation)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed nested module-handler re-raise")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("nested module-handler re-raise replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerExecutesWhileAndNamedLoopExit(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out Tick(value : Integer);
  action out EscapedNestedLoop();
  action out WrongAfterNext();
  action out WrongAfterInner();
  action out HandlerContinued();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  remaining : var Integer := 2;
  step : var Integer := 0;
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    Skip : loop do
      step := $step + 1;
      next Skip where $step = 1;
      exit Skip where $step = 2;
      WrongAfterNext();
    end do Skip;
    while $remaining > 0 do
      Tick($remaining);
      remaining := $remaining - 1;
    end do;
    Outer : loop do
      Inner : loop do
        EscapedNestedLoop();
        exit Outer;
      end do Inner;
      WrongAfterInner();
    end do Outer;
    HandlerContinued();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	ticks := sourceNamedEvents(result.Poset, "worker", "Tick")
	escaped := sourceNamedEvents(result.Poset, "worker", "EscapedNestedLoop")
	continued := sourceNamedEvents(result.Poset, "worker", "HandlerContinued")
	if len(failure) != 1 || len(ticks) != 2 || len(escaped) != 1 || len(continued) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "WrongAfterNext")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "WrongAfterInner")) != 0 {
		t.Fatalf("Failure/Tick/EscapedNestedLoop/HandlerContinued=%d/%d/%d/%d",
			len(failure), len(ticks), len(escaped), len(continued))
	}
	sort.Slice(ticks, func(left, right int) bool {
		return result.Poset.IsCausallyBefore(ticks[left].ID, ticks[right].ID)
	})
	if ticks[0].ParamInt("value") != 2 || ticks[1].ParamInt("value") != 1 {
		t.Fatalf("while Tick values=%#v/%#v", ticks[0].Params, ticks[1].Params)
	}
	if !result.Poset.IsCausallyBefore(failure[0].ID, ticks[0].ID) ||
		!result.Poset.IsCausallyBefore(ticks[0].ID, ticks[1].ID) ||
		!result.Poset.IsCausallyBefore(ticks[1].ID, escaped[0].ID) ||
		!result.Poset.IsCausallyBefore(escaped[0].ID, continued[0].ID) {
		t.Fatal("module-handler loop causality is incomplete")
	}
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State == arch.ModuleTerminatedState || worker.TerminationEventID != "" {
		t.Fatalf("module-handler loop control terminated module=%#v", worker)
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("module-handler loop control propagated=%#v", result.ExceptionPropagations)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed module-handler loop control")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("module-handler loop control replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerFiniteRangeForRetainsIteratorLifecycle(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out Seen(value : Integer);
  action out HandlerContinued();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    for I : Integer in 1..3 do
      Seen(I);
    end;
    HandlerContinued();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
	journal := arch.NewExecutionJournalWithLimits(digest,
		arch.ExecutionLimits{MaxFirings: 80, MaxStatements: 80},
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	seen := sourceNamedEvents(result.Poset, "worker", "Seen")
	continued := sourceNamedEvents(result.Poset, "worker", "HandlerContinued")
	if len(failure) != 1 || len(seen) != 3 || len(continued) != 1 ||
		distinctSourceIteratorEventCount(result.Poset.ByName("More'Call")) != 4 ||
		distinctSourceIteratorEventCount(result.Poset.ByName("Item'Call")) != 3 {
		t.Fatalf("Failure/Seen/HandlerContinued/More/Item=%d/%d/%d/%d/%d",
			len(failure), len(seen), len(continued),
			distinctSourceIteratorEventCount(result.Poset.ByName("More'Call")),
			distinctSourceIteratorEventCount(result.Poset.ByName("Item'Call")))
	}
	sort.Slice(seen, func(left, right int) bool {
		return result.Poset.IsCausallyBefore(seen[left].ID, seen[right].ID)
	})
	for index, event := range seen {
		if event.ParamInt("value") != index+1 {
			t.Fatalf("Seen[%d]=%#v", index, event.Params)
		}
	}
	var iterator *arch.ModuleLifecycleRecord
	for index := range result.Modules {
		if result.Modules[index].Kind == "predefined-range-iterator" {
			iterator = &result.Modules[index]
			break
		}
	}
	if iterator == nil || iterator.State != arch.ModuleFinalizedState || iterator.Namable ||
		iterator.StartEventID == "" || iterator.FinishEventID == "" {
		t.Fatalf("module-handler range iterator lifecycle=%#v", iterator)
	}
	start, startExists := result.Poset.Event(gorapide.EventID(iterator.StartEventID))
	finish, finishExists := result.Poset.Event(gorapide.EventID(iterator.FinishEventID))
	if !startExists || !finishExists || !result.Poset.IsCausallyBefore(failure[0].ID, start.ID) ||
		!result.Poset.IsCausallyBefore(start.ID, seen[0].ID) ||
		!result.Poset.IsCausallyBefore(seen[2].ID, finish.ID) ||
		!result.Poset.IsCausallyIndependent(finish.ID, continued[0].ID) {
		t.Fatalf("module-handler iterator Start/Finish/continuation relation start=%#v finish=%#v", start, finish)
	}
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State == arch.ModuleTerminatedState || worker.TerminationEventID != "" {
		t.Fatalf("module-handler range for terminated module=%#v", worker)
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("module-handler range for propagated=%#v", result.ExceptionPropagations)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed module-handler range iterator")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("module-handler range iterator replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerRangeExceptionFinalizesIterator(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out Seen(value : Integer);
  action out UnreachableHandlerRemainder();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  exception Escalated(value : Integer);
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    for I : Integer in 1..3 do
      raise Escalated(I) where I = 2;
      Seen(I);
    end;
    UnreachableHandlerRemainder();
  is Escalated => UnreachableHandlerRemainder();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
	journal := arch.NewExecutionJournalWithLimits(digest,
		arch.ExecutionLimits{MaxFirings: 80, MaxStatements: 80},
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	seen := sourceNamedEvents(result.Poset, "worker", "Seen")
	escalated := sourceNamedEvents(result.Poset, "worker", "Escalated")
	if len(failure) != 1 || len(seen) != 1 || len(escalated) != 1 ||
		seen[0].ParamInt("value") != 1 || escalated[0].ParamInt("value") != 2 ||
		len(sourceNamedEvents(result.Poset, "worker", "UnreachableHandlerRemainder")) != 0 {
		t.Fatalf("Failure/Seen/Escalated=%d/%d/%d values=%#v/%#v",
			len(failure), len(seen), len(escalated), seen, escalated)
	}
	var iterator *arch.ModuleLifecycleRecord
	for index := range result.Modules {
		if result.Modules[index].Kind == "predefined-range-iterator" {
			iterator = &result.Modules[index]
			break
		}
	}
	if iterator == nil || iterator.State != arch.ModuleFinalizedState || iterator.Namable ||
		iterator.FinishEventID == "" {
		t.Fatalf("exceptional module-handler iterator lifecycle=%#v", iterator)
	}
	finish, exists := result.Poset.Event(gorapide.EventID(iterator.FinishEventID))
	if !exists || !result.Poset.IsCausallyBefore(escalated[0].ID, finish.ID) {
		t.Fatalf("exceptional module-handler iterator Finish=%#v", finish)
	}
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State != arch.ModuleTerminatedState || worker.TerminationEventID != string(escalated[0].ID) {
		t.Fatalf("range-body exception lifecycle=%#v", worker)
	}
	propagation := exceptionPropagationBySource(t, result, worker.ModuleID)
	if propagation.ExceptionEventID != string(escalated[0].ID) || propagation.Exception != "Escalated" {
		t.Fatalf("range-body exception propagation=%#v", propagation)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed exceptional module-handler range iterator")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("exceptional module-handler range iterator replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerGeneralForUsesRefStateAndContinues(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out Seen(value : Integer);
  action out HandlerContinued();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  count : var Integer := 99;
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    for count := 0 in $count < 3 next count := $count + 1 do
      Seen($count);
    end for;
    HandlerContinued();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
	journal := arch.NewExecutionJournalWithLimits(digest,
		arch.ExecutionLimits{MaxFirings: 80, MaxStatements: 80},
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	seen := sourceNamedEvents(result.Poset, "worker", "Seen")
	continued := sourceNamedEvents(result.Poset, "worker", "HandlerContinued")
	if len(failure) != 1 || len(seen) != 3 || len(continued) != 1 {
		t.Fatalf("Failure/Seen/HandlerContinued=%d/%d/%d", len(failure), len(seen), len(continued))
	}
	sort.Slice(seen, func(left, right int) bool {
		return result.Poset.IsCausallyBefore(seen[left].ID, seen[right].ID)
	})
	for index, event := range seen {
		if event.ParamInt("value") != index {
			t.Fatalf("Seen[%d]=%#v", index, event.Params)
		}
	}
	if !result.Poset.IsCausallyBefore(failure[0].ID, seen[0].ID) ||
		!result.Poset.IsCausallyBefore(seen[0].ID, seen[1].ID) ||
		!result.Poset.IsCausallyBefore(seen[1].ID, seen[2].ID) ||
		!result.Poset.IsCausallyBefore(seen[2].ID, continued[0].ID) {
		t.Fatal("module-handler general-for causality is incomplete")
	}
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State == arch.ModuleTerminatedState || worker.TerminationEventID != "" {
		t.Fatalf("module-handler general for terminated module=%#v", worker)
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("module-handler general for propagated=%#v", result.ExceptionPropagations)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed module-handler general for")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("module-handler general for replay changed canonical bytes")
	}
}

func TestSourceModuleHandlerGeneralForInitializerExceptionEscapes(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out WrongLoopBody();
  action out UnreachableHandlerRemainder();
  action out WrongSameHandler();
  provides Begin : function();
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  exception Escalated;
  Begin : function() is
  begin
    raise Escalated;
  end function Begin;
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    for Begin() in True next 0 do
      WrongLoopBody();
    end for;
    UnreachableHandlerRemainder();
  is Escalated => WrongSameHandler();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
	journal := arch.NewExecutionJournalWithLimits(digest,
		arch.ExecutionLimits{MaxFirings: 80, MaxStatements: 80},
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
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

	failure := sourceNamedEvents(result.Poset, "worker", "Failure")
	calls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Begin'Call"))
	escalated := sourceNamedEvents(result.Poset, "worker", "Escalated")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Begin'Return"))
	if len(failure) != 1 || len(calls) != 1 || len(escalated) != 1 || len(returns) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "WrongLoopBody")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "UnreachableHandlerRemainder")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "WrongSameHandler")) != 0 {
		t.Fatalf("Failure/Begin'Call/Escalated/Begin'Return=%d/%d/%d/%d",
			len(failure), len(calls), len(escalated), len(returns))
	}
	assertOnlyDirectCause(t, result.Poset, calls[0], failure[0])
	assertOnlyDirectCause(t, result.Poset, escalated[0], calls[0])
	worker := lifecycleModuleByOccurrence(t, result, "component:worker")
	if worker.State != arch.ModuleTerminatedState || worker.TerminationEventID != string(escalated[0].ID) {
		t.Fatalf("general-for initializer exception lifecycle=%#v", worker)
	}
	propagation := exceptionPropagationBySource(t, result, worker.ModuleID)
	if propagation.ExceptionEventID != string(escalated[0].ID) || propagation.Exception != "Escalated" {
		t.Fatalf("general-for initializer exception propagation=%#v", propagation)
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed general-for initializer exception")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("general-for initializer exception replay changed canonical bytes")
	}
}

func TestSourceGeneralForFunctionExceptionTransfersFromEveryControlPhase(t *testing.T) {
	tests := []struct {
		name       string
		loop       string
		bodyEvents int
	}{
		{name: "initializer", loop: "for Control() in True next False do LoopBody(); end for;"},
		{name: "test", loop: "for False in Control() next False do LoopBody(); end for;"},
		{name: "next", loop: "for False in True next Control() do LoopBody(); end for;", bodyEvents: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Worker is interface
  action in Fail();
  action out LoopBody();
  action out UnreachableHandlerRemainder();
  action out WrongSameHandler();
  provides Control : function() return Boolean;
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  exception Escalated;
  Control : function() return Boolean is
  begin
    raise Escalated;
    return True;
  end function Control;
serial when Fail do raise Failure; end when;
handler
  is Failure =>
    ` + test.loop + `
    UnreachableHandlerRemainder();
  is Escalated => WrongSameHandler();
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail to worker.Fail;
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
			result, err := model.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(digest,
				arch.ExecutionLimits{MaxFirings: 80, MaxStatements: 80},
				arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail"},
			))
			if err != nil {
				t.Fatal(err)
			}
			calls := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Control'Call"))
			escalated := sourceNamedEvents(result.Poset, "worker", "Escalated")
			returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Control'Return"))
			if len(calls) != 1 || len(escalated) != 1 || len(returns) != 0 ||
				len(sourceNamedEvents(result.Poset, "worker", "LoopBody")) != test.bodyEvents ||
				len(sourceNamedEvents(result.Poset, "worker", "UnreachableHandlerRemainder")) != 0 ||
				len(sourceNamedEvents(result.Poset, "worker", "WrongSameHandler")) != 0 {
				t.Fatalf("Call/Escalated/Return/LoopBody=%d/%d/%d/%d",
					len(calls), len(escalated), len(returns), len(sourceNamedEvents(result.Poset, "worker", "LoopBody")))
			}
			worker := lifecycleModuleByOccurrence(t, result, "component:worker")
			if worker.State != arch.ModuleTerminatedState || worker.TerminationEventID != string(escalated[0].ID) {
				t.Fatalf("general-for %s exception lifecycle=%#v", test.name, worker)
			}
		})
	}
}

func TestSourceModuleHandlerChoiceOrderIsCanonical(t *testing.T) {
	build := func(reverse bool) []byte {
		exceptions := "exception Alpha(n : Integer); exception Beta;"
		choices := "is Alpha(n is ?N) => Seen(?N); is Beta => Seen(0);"
		if reverse {
			exceptions = "exception Beta; exception Alpha(n : Integer);"
			choices = "is Beta => Seen(0); is Alpha(n is ?N) => Seen(?N);"
		}
		return []byte(`
type Worker is interface action in Trigger(); action out Seen(n : Integer); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  ` + exceptions + `
serial when Trigger do raise Alpha(4); end when;
handler ` + choices + `
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger to worker.Trigger;
end architecture System;
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
		t.Fatalf("module handler choice order changed model identity: %s != %s", leftDigest, rightDigest)
	}
}

func lifecycleModuleByOccurrence(
	t *testing.T,
	result *arch.ExecutionResult,
	occurrence string,
) *arch.ModuleLifecycleRecord {
	t.Helper()
	for index := range result.Modules {
		if result.Modules[index].Occurrence == occurrence {
			return &result.Modules[index]
		}
	}
	t.Fatalf("module lifecycle occurrence %q is absent", occurrence)
	return nil
}

func exceptionPropagationBySource(
	t *testing.T,
	result *arch.ExecutionResult,
	sourceModuleID string,
) *arch.ExceptionPropagationRecord {
	t.Helper()
	for index := range result.ExceptionPropagations {
		if result.ExceptionPropagations[index].SourceModuleID == sourceModuleID {
			return &result.ExceptionPropagations[index]
		}
	}
	t.Fatalf("exception propagation source %q is absent", sourceModuleID)
	return nil
}

func TestSourceHandlerBodyRaiseEscapesSameHandlerAndTerminatesModule(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
serial when Trigger() do raise Failure;
handler is Failure => raise Failure;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	terminating := assertExceptionTerminatedModule(t, result, "worker", "Failure")
	failures := sourceNamedEvents(result.Poset, "worker", "Failure")
	if len(failures) != 2 {
		t.Fatalf("Failure events=%d, want protected and handler-body raises", len(failures))
	}
	var handled *gorapide.Event
	for _, failure := range failures {
		if failure.ID != terminating.ID {
			handled = failure
		}
	}
	if handled == nil || !result.Poset.IsCausallyBefore(handled.ID, terminating.ID) {
		t.Fatal("handler-body raise did not escape after the handled exception")
	}
}

func TestSourceModuleHandlerRecoversWithoutTerminatingSiblingProcesses(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail(value : Integer);
  action in Continue();
  action out Recovered(value : Integer);
  action out Continued();
end interface Worker;
type Stimulus is interface action out Fail(value : Integer); action out Continue(); end interface Stimulus;

module WorkerModule() return Worker is
  exception Failure(value : Integer);
  C : Clock is Make_Clock();
parallel
  when (?Value : Integer) Fail(?Value) do raise Failure(?Value); end when;
||
  when Continue do pause C.Ticks(1); Continued(); end when;
handler
  is Failure(?Recovered) => Recovered(?Recovered);
end module WorkerModule;

architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect
  stimulus.Fail to worker.Fail;
  stimulus.Continue to worker.Continue;
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
		arch.InputEvent{Key: "fail", Source: "stimulus", Action: "Fail", Params: map[string]any{"value": 7}},
		arch.InputEvent{Key: "continue", Source: "stimulus", Action: "Continue", Causes: []string{"fail"}},
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

	failures := sourceNamedEvents(result.Poset, "worker", "Failure")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	continued := sourceNamedEvents(result.Poset, "worker", "Continued")
	if len(failures) != 1 || len(recovered) != 1 || len(continued) != 1 {
		t.Fatalf("Failure/Recovered/Continued=%d/%d/%d", len(failures), len(recovered), len(continued))
	}
	if recovered[0].ParamInt("value") != 7 {
		t.Fatalf("module handler binding=%#v", recovered[0].Params)
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], failures[0])
	workerModule := lifecycleModuleByOccurrence(t, result, "component:worker")
	if workerModule.State != arch.ModuleRunningState || workerModule.TerminationEventID != "" {
		t.Fatalf("handled module lifecycle=%#v", workerModule)
	}
	completionCounts := make(map[string]int)
	for _, process := range result.Processes {
		if process.ComponentID == "worker" {
			completionCounts[process.Completion]++
			if process.Completion == "module-termination" {
				t.Fatalf("module handler incorrectly terminated sibling=%#v", process)
			}
		}
	}
	if completionCounts["exception"] != 1 || completionCounts[""] != 1 {
		t.Fatalf("module-handler process completions=%v", completionCounts)
	}
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("handled local exception propagated=%#v", result.ExceptionPropagations)
	}
	foundHandlerFiring := false
	for _, firing := range result.Firings {
		if firing.Transition == "module-handler" && firing.Target == "worker" &&
			len(firing.MatchedEvents) == 1 && firing.MatchedEvents[0] == string(failures[0].ID) {
			foundHandlerFiring = true
		}
	}
	if !foundHandlerFiring {
		t.Fatal("module handler selection is absent from firing audit")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed module-handler recovery")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("module-handler recovery replay changed canonical bytes")
	}
}

func TestSourceUnhandledExceptionCompletesWaitingSiblingProcesses(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); action in Other(); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
parallel
  when Trigger() do raise Failure; end when;
||
  when Other() do null; end when;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	terminating := assertExceptionTerminatedModule(t, result, "worker", "Failure")
	completionCounts := make(map[string]int)
	for _, process := range result.Processes {
		if process.ComponentID != "worker" {
			continue
		}
		if !process.Terminated || process.ExceptionEventID != string(terminating.ID) {
			t.Fatalf("multi-process termination audit=%#v", process)
		}
		completionCounts[process.Completion]++
	}
	if completionCounts["exception"] != 1 || completionCounts["module-termination"] != 1 {
		t.Fatalf("multi-process completion reasons=%v", completionCounts)
	}
}

func TestSourceUnhandledExceptionTerminatesAfterProcessSuspension(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  C : Clock is Make_Clock();
serial when Trigger() do
  pause C.Ticks(1);
  raise Failure;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	assertExceptionTerminatedModule(t, result, "worker", "Failure")
	if len(result.ClockAdvances) != 1 || result.ClockAdvances[0].To != "1" {
		t.Fatalf("exception suspension clock audit=%#v", result.ClockAdvances)
	}
}

func TestSourceUnhandledExceptionCancelsActiveSiblingSuspension(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Slow();
  action in Fail();
  action out Done();
end interface Worker;
type Stimulus is interface action out Slow(); action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  C : Clock is Make_Clock();
parallel
  when Slow() do pause C.Ticks(5); Done(); end when;
||
  when Fail() do raise Failure; end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Slow => worker.Slow;
        stimulus.Fail => worker.Fail;
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
		arch.InputEvent{Key: "slow", Source: "stimulus", Action: "Slow"},
		arch.InputEvent{
			Key: "fail", Source: "stimulus", Action: "Fail", Causes: []string{"slow"},
			Timings: []gorapide.EventTiming{{Clock: arch.ClockID("worker", "C"), Start: 1, Finish: 1}},
		},
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
	terminating := assertExceptionTerminatedModule(t, result, "worker", "Failure")
	if len(sourceNamedEvents(result.Poset, "worker", "Done")) != 0 {
		t.Fatal("canceled sibling suspension resumed after module termination")
	}
	if len(result.ClockAdvances) != 1 || result.ClockAdvances[0].To != "1" {
		t.Fatalf("active sibling cancellation clock audit=%#v", result.ClockAdvances)
	}
	foundCanceledSibling := false
	for _, process := range result.Processes {
		if process.ComponentID == "worker" && process.Completion == "module-termination" {
			if process.ExceptionEventID != string(terminating.ID) ||
				len(process.CanceledSuspensions) != 1 || len(process.CanceledSchedules) != 0 {
				t.Fatalf("canceled sibling process audit=%#v", process)
			}
			foundCanceledSibling = true
		}
	}
	if !foundCanceledSibling {
		t.Fatal("missing module-terminated suspended sibling audit")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed active-sibling cancellation artifact")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("active-sibling cancellation replay changed artifact bytes")
	}
}

func TestSourceUnhandledExceptionReleasesActiveIteratorNameAndFlushesFinish(t *testing.T) {
	source := []byte(`
type Worker is interface action in Slow(); action in Fail(); action out Done(); end interface Worker;
type Stimulus is interface action out Slow(); action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  C : Clock is Make_Clock();
parallel
  when Slow() do
    for I : Integer in 1..2 do pause C.Ticks(5); Done(); end;
  end when;
||
  when Fail() do raise Failure; end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Slow => worker.Slow;
        stimulus.Fail => worker.Fail;
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
		arch.InputEvent{Key: "slow", Source: "stimulus", Action: "Slow"},
		arch.InputEvent{
			Key: "fail", Source: "stimulus", Action: "Fail", Causes: []string{"slow"},
			Timings: []gorapide.EventTiming{{Clock: arch.ClockID("worker", "C"), Start: 1, Finish: 1}},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	terminating := assertExceptionTerminatedModule(t, result, "worker", "Failure")
	if len(sourceNamedEvents(result.Poset, "worker", "Done")) != 0 {
		t.Fatal("iterator body continued after module termination")
	}
	var iterator *arch.ModuleLifecycleRecord
	for index := range result.Modules {
		if result.Modules[index].Kind == "predefined-range-iterator" {
			iterator = &result.Modules[index]
			break
		}
	}
	if iterator == nil || iterator.State != arch.ModuleFinalizedState || iterator.Namable ||
		iterator.FinishEventID == "" {
		t.Fatalf("exception-released iterator lifecycle=%#v", iterator)
	}
	finish, exists := result.Poset.Event(gorapide.EventID(iterator.FinishEventID))
	if !exists || !result.Poset.IsCausallyBefore(terminating.ID, finish.ID) {
		t.Fatalf("iterator Finish=%#v does not follow terminating exception %s", finish, terminating.ID)
	}
	foundCanceledIteratorProcess := false
	for _, process := range result.Processes {
		if process.ComponentID == "worker" && process.Completion == "module-termination" {
			if len(process.CanceledSuspensions) != 1 {
				t.Fatalf("iterator process cancellation audit=%#v", process)
			}
			foundCanceledIteratorProcess = true
		}
	}
	if !foundCanceledIteratorProcess {
		t.Fatal("missing exception-completed iterator process")
	}
}

func TestSourceUnhandledExceptionReleasesSuspendedPatternModuleName(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Fail();
  action out Offer(value : Worker);
  action out Done(value : Worker);
end interface Worker;
type Stimulus is interface action out Fail(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  C : Clock is Make_Clock();
initial Offer(Self);
parallel
  when (?Peer : Worker) Offer(?Peer) do pause C.Ticks(5); Done(?Peer); end when;
||
  when Fail() do raise Failure; end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Fail => worker.Fail;
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
		arch.InputEvent{
			Key: "fail", Source: "stimulus", Action: "Fail",
			Timings: []gorapide.EventTiming{{Clock: arch.ClockID("worker", "C"), Start: 1, Finish: 1}},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	terminating := assertExceptionTerminatedModule(t, result, "worker", "Failure")
	if len(sourceNamedEvents(result.Poset, "worker", "Done")) != 0 {
		t.Fatal("pattern-bound process continued after module termination")
	}
	var binding *arch.ModuleNameRecord
	for moduleIndex := range result.Modules {
		for nameIndex := range result.Modules[moduleIndex].Names {
			name := &result.Modules[moduleIndex].Names[nameIndex]
			if name.Kind == "pattern-binding" {
				binding = name
				break
			}
		}
	}
	if binding == nil || binding.Live || len(binding.LostAfter) != 1 ||
		binding.LostAfter[0] != string(terminating.ID) {
		t.Fatalf("exception-released pattern module name=%#v", binding)
	}
}

func TestSourceUnhandledExceptionCancelsPendingTimedAction(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); action out Later(); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
  C : Clock is Make_Clock();
serial when Trigger() do
  Later() in C.Ticks(1);
  raise Failure;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	assertExceptionTerminatedModule(t, result, "worker", "Failure")
	if len(sourceNamedEvents(result.Poset, "worker", "Later")) != 0 ||
		len(result.ScheduledEvents) != 0 || len(result.ClockAdvances) != 0 {
		t.Fatalf("canceled timed action leaked: Later=%d scheduled=%#v advances=%#v",
			len(sourceNamedEvents(result.Poset, "worker", "Later")), result.ScheduledEvents, result.ClockAdvances)
	}
	var process *arch.ProcessExecutionRecord
	for index := range result.Processes {
		if result.Processes[index].ComponentID == "worker" && result.Processes[index].Completion == "exception" {
			process = &result.Processes[index]
			break
		}
	}
	if process == nil || len(process.CanceledSchedules) != 1 || len(process.CanceledSuspensions) != 0 {
		t.Fatalf("canceled timed-action process audit=%#v", process)
	}
}

func assertExceptionTerminatedModule(
	t *testing.T,
	result *arch.ExecutionResult,
	componentID, action string,
) *gorapide.Event {
	t.Helper()
	var process *arch.ProcessExecutionRecord
	for index := range result.Processes {
		if result.Processes[index].ComponentID == componentID &&
			result.Processes[index].Completion == "exception" {
			process = &result.Processes[index]
			break
		}
	}
	if process == nil || !process.Terminated || process.Completion != "exception" ||
		process.ExceptionEventID == "" || process.State != "" {
		t.Fatalf("exception process audit=%#v", process)
	}
	var module *arch.ModuleLifecycleRecord
	for index := range result.Modules {
		for _, name := range result.Modules[index].Names {
			if name.Kind == "architecture-constituent" && name.Name == componentID {
				module = &result.Modules[index]
				break
			}
		}
	}
	if module == nil || module.State != arch.ModuleTerminatedState || !module.Namable ||
		module.TerminationEventID != process.ExceptionEventID || module.FinishEventID != "" {
		t.Fatalf("exception module audit=%#v", module)
	}
	event, exists := result.Poset.Event(gorapide.EventID(process.ExceptionEventID))
	if !exists || event.Source != componentID || event.Name != action {
		t.Fatalf("terminating exception event=%#v", event)
	}
	return event
}

func TestSourceExceptionAndHandlerDeclarationOrderIsCanonical(t *testing.T) {
	build := func(reverse bool) []byte {
		exceptions := "exception Alpha(n : Integer; flag : Boolean); exception Beta;"
		raise := "raise Alpha(n is 4, flag is True);"
		choices := "is Alpha(n is ?N, flag is ?Flag) => Seen(?N); is Beta => Seen(0);"
		if reverse {
			exceptions = "exception Beta; exception Alpha(n : Integer; flag : Boolean);"
			raise = "raise Alpha(flag is True, n is 4);"
			choices = "is Beta => Seen(0); is Alpha(flag is ?Flag, n is ?N) => Seen(?N);"
		}
		return []byte(`
type Worker is interface action in Trigger(); action out Seen(n : Integer); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  ` + exceptions + `
serial when Trigger() do ` + raise + `
handler ` + choices + `
end when;
end module WorkerModule;
architecture System() is
  stimulus : Stimulus;
  worker : Worker is WorkerModule();
connect stimulus.Trigger => worker.Trigger;
end architecture System;
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
		t.Fatalf("exception/handler declaration order changed canonical model: %s != %s", leftDigest, rightDigest)
	}
}
