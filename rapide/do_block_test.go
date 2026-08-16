package rapide

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourcePlainDoBlockPreservesSequentialControlAndFunctionReturn(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Before();
  action out Inside();
  action out WrongContinuation();
  action out Returned(value : Integer);
  provides Work : function() return Integer;
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  result : var Integer := 0;
  Work : function() return Integer is
  begin
    do
      Before();
      do
        Inside();
      end do;
      return 7;
      WrongContinuation();
    end do;
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
	before := sourceNamedEvents(result.Poset, "worker", "Before")
	inside := sourceNamedEvents(result.Poset, "worker", "Inside")
	returns := distinctAllocatorEvents(sourceNamedEvents(result.Poset, "worker", "Work'Return"))
	returned := sourceNamedEvents(result.Poset, "worker", "Returned")
	if len(before) != 1 || len(inside) != 1 || len(returns) != 1 || len(returned) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "WrongContinuation")) != 0 {
		t.Fatalf("Before/Inside/Return/Returned=%d/%d/%d/%d",
			len(before), len(inside), len(returns), len(returned))
	}
	value, _ := returned[0].Param("value")
	if value != int64(7) {
		t.Fatalf("plain do return value=%#v", value)
	}
	if !result.Poset.IsCausallyBefore(before[0].ID, inside[0].ID) ||
		!result.Poset.IsCausallyBefore(inside[0].ID, returns[0].ID) ||
		!result.Poset.IsCausallyBefore(returns[0].ID, returned[0].ID) {
		t.Fatal("plain do did not preserve nested sequential/return control")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed plain do execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("plain do replay changed canonical bytes")
	}
}

func TestSourcePlainDoBlockPropagatesInterruptToOuterHandler(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Pulse();
  action out InnerContinued();
  action out OuterContinued();
  action out Recovered();
  action out AfterDo();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
serial when Trigger do
  do
    do
      Pulse();
      InnerContinued();
    end do;
    OuterContinued();
  handler
    is Pulse => Recovered();
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
	pulse := sourceNamedEvents(result.Poset, "worker", "Pulse")
	recovered := sourceNamedEvents(result.Poset, "worker", "Recovered")
	after := sourceNamedEvents(result.Poset, "worker", "AfterDo")
	if len(pulse) != 1 || len(recovered) != 1 || len(after) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "InnerContinued")) != 0 ||
		len(sourceNamedEvents(result.Poset, "worker", "OuterContinued")) != 0 {
		t.Fatalf("Pulse/Recovered/AfterDo=%d/%d/%d", len(pulse), len(recovered), len(after))
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], pulse[0])
	if !result.Poset.IsCausallyBefore(recovered[0].ID, after[0].ID) {
		t.Fatal("plain do did not propagate interrupt control to its outer handler")
	}
}

func TestSourceProcessPlainDoBlockSuspendsAndResumesInPlace(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Before();
  action out After();
  action out Done();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
serial when Trigger do
  do
    Before();
    pause C.Ticks(1);
    After();
  end do;
  Done();
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
	before := sourceNamedEvents(result.Poset, "worker", "Before")
	after := sourceNamedEvents(result.Poset, "worker", "After")
	done := sourceNamedEvents(result.Poset, "worker", "Done")
	if len(before) != 1 || len(after) != 1 || len(done) != 1 ||
		!result.Poset.IsCausallyBefore(before[0].ID, after[0].ID) ||
		!result.Poset.IsCausallyBefore(after[0].ID, done[0].ID) {
		t.Fatalf("plain-do Before/After/Done=%d/%d/%d", len(before), len(after), len(done))
	}
	clockID := arch.ClockID("worker", "C")
	if len(result.Clocks) != 1 || result.Clocks[0].Clock != clockID || result.Clocks[0].Now != "1" {
		t.Fatalf("plain-do clock audit=%#v", result.Clocks)
	}
	firing := sourceProcessFiring(t, result, "worker")
	if len(firing.Suspensions) != 1 || firing.Suspensions[0].Clock != clockID ||
		firing.Suspensions[0].Start != "0" || firing.Suspensions[0].Finish != "1" {
		t.Fatalf("plain-do suspension audit=%#v", firing.Suspensions)
	}
	left, _ := result.MarshalCanonical()
	right, _ := repeated.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("GOMAXPROCS changed plain-do suspension")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, replayedBytes) {
		t.Fatal("plain-do suspension replay changed canonical bytes")
	}
}
