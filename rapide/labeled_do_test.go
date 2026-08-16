package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestSourceNamedExitAndNextTargetLexicallyEnclosingDo(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Tick(value : Integer);
  action out Wrong();
  action out Done(value : Integer);
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  count : var Integer := 0;
serial when Trigger do
  Outer : loop do
    count := $count + 1;
    Tick($count);
    Inner : loop do
      next Outer where $count = 1;
      exit Outer;
    end do Inner;
    Wrong();
  end do Outer;
  Done($count);
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
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	ticks := sourceNamedEvents(result.Poset, "worker", "Tick")
	done := sourceNamedEvents(result.Poset, "worker", "Done")
	if len(ticks) != 2 || len(done) != 1 || len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
		t.Fatalf("Tick/Done/Wrong=%d/%d/%d", len(ticks), len(done), len(sourceNamedEvents(result.Poset, "worker", "Wrong")))
	}
	byValue := make(map[int64]int)
	for _, tick := range ticks {
		value, _ := tick.Param("value")
		byValue[value.(int64)]++
	}
	if byValue[1] != 1 || byValue[2] != 1 {
		t.Fatalf("Tick values=%#v", byValue)
	}
	value, _ := done[0].Param("value")
	if value != int64(2) {
		t.Fatalf("Done value=%#v", value)
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := result.MarshalCanonical()
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("named do replay changed canonical bytes")
	}
}

func TestSourceExitCompletesPlainAndHandlerBearingDo(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger();
  action out Before();
  action out Wrong();
  action out Recovered();
  action out Done();
end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
serial when Trigger do
  do
    Before();
    exit;
    Wrong();
  end do;
  Guarded : do
    raise Failure;
  handler
    is Failure =>
      Recovered();
      exit Guarded;
  end do Guarded;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceNamedEvents(result.Poset, "worker", "Before")) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Recovered")) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Done")) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
		t.Fatal("plain or handler-bearing do did not consume its own exit")
	}
}

func TestSourceDoLabelsCoverSupportedProceduralForms(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); action out Wrong(); action out Done(); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  count : var Integer := 0;
serial when Trigger do
  Plain : do
    next Plain;
    Wrong();
  end do Plain;
  While_Do : while True do
    exit While_Do;
    Wrong();
  end do While_Do;
  Range_Do : for I : Integer in 1..1 do
    exit Range_Do;
    Wrong();
  end do Range_Do;
  General_Do : for count := 0 in $count = 0 next count := 1 do
    exit General_Do;
    Wrong();
  end do General_Do;
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
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 40,
		arch.InputEvent{Key: "trigger", Source: "stimulus", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceNamedEvents(result.Poset, "worker", "Done")) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
		t.Fatal("one of the supported named procedural do forms lost directed control")
	}
}

func TestSourceNamedExitSurvivesProcessSuspension(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); action out Wrong(); action out Done(); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
serial when Trigger do
  Outer : loop do
    pause C.Ticks(1);
    Inner : loop do exit Outer; end do Inner;
    Wrong();
  end do Outer;
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
	prior := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(prior)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}
	if len(sourceNamedEvents(result.Poset, "worker", "Done")) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
		t.Fatal("named exit did not cross the suspended nested loop")
	}
	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) {
		t.Fatal("GOMAXPROCS changed suspended named-do execution")
	}
}

func TestSourceDoLabelsAreCanonicalAndValidated(t *testing.T) {
	build := func(label string) []byte {
		return []byte(`
type API is interface action out Start(); behavior begin Start => ` + label + ` : loop do exit ` + label + `; end do ` + label + `; ; end interface API;
architecture M() is api : API; end architecture M;
`)
	}
	lower, err := Compile(build("outer"), "M")
	if err != nil {
		t.Fatal(err)
	}
	upper, err := Compile(build("OUTER"), "M")
	if err != nil {
		t.Fatal(err)
	}
	lowerDigest, _ := lower.DeterministicModelDigest()
	upperDigest, _ := upper.DeterministicModelDigest()
	if lowerDigest != upperDigest {
		t.Fatalf("do-label case changed model identity: %s != %s", lowerDigest, upperDigest)
	}

	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "mismatched terminator", body: "Outer : loop do exit; end do Wrong;", want: "does not match statement label"},
		{name: "duplicate label", body: "Outer : loop do Outer : loop do exit Outer; end do Outer; end do Outer;", want: "overloads do label"},
		{name: "non-enclosing target", body: "Outer : loop do exit Missing; end do Outer;", want: "names non-enclosing do"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type API is interface action out Start(); behavior begin Start => " + test.body + "; end interface API; architecture M() is api : API; end architecture M;")
			model, err := Compile(source, "M")
			if err == nil {
				_, err = model.DeterministicModelDigest()
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want containing %q", err, test.want)
			}
		})
	}
}
