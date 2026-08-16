package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestSourceLabeledWhenConsumesNamedNextAndExitAtProcessBoundary(t *testing.T) {
	source := []byte(`
type Worker is interface
  action in Trigger(value : Integer);
  action out Seen(value : Integer);
  action out After(value : Integer);
  action out Wrong();
end interface Worker;
type Stimulus is interface action out Trigger(value : Integer); end interface Stimulus;
module WorkerModule() return Worker is
  C : Clock is Make_Clock();
serial Cycle : when (?N : Integer) Trigger(?N) do
  Seen(?N);
  pause C.Ticks(1);
  Inner : loop do
    next Cycle where ?N = 1;
    exit Inner;
  end do Inner;
  After(?N);
  exit Cycle where ?N = 2;
  Wrong();
end when Cycle;
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
	journal := arch.NewExecutionJournalWithLimits(digest, arch.ExecutionLimits{
		MaxFirings: 80, MaxStatements: 80,
	},
		arch.InputEvent{Key: "one", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": 1}},
		arch.InputEvent{Key: "two", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": 2}, Causes: []string{"one"}},
		arch.InputEvent{Key: "three", Source: "stimulus", Action: "Trigger", Params: map[string]any{"value": 3}, Causes: []string{"two"}},
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

	seen := sourceNamedEvents(result.Poset, "worker", "Seen")
	after := sourceNamedEvents(result.Poset, "worker", "After")
	triggers := sourceNamedEvents(result.Poset, "worker", "Trigger")
	triggersByValue := make(map[int64]*gorapide.Event, 3)
	for _, trigger := range triggers {
		value, _ := trigger.Param("value")
		triggersByValue[value.(int64)] = trigger
	}
	if len(triggersByValue) != 3 {
		t.Fatalf("worker Trigger values=%d observations=%d", len(triggersByValue), len(triggers))
	}
	if !result.Poset.IsCausallyBefore(triggersByValue[1].ID, triggersByValue[2].ID) ||
		!result.Poset.IsCausallyBefore(triggersByValue[2].ID, triggersByValue[3].ID) {
		t.Fatal("causally ordered inputs lost their ordering through a basic connection")
	}
	earlier, err := pattern.IsEarlierMatch(
		gorapide.EventSet{triggersByValue[1]}, gorapide.EventSet{triggersByValue[2]}, result.Poset,
	)
	if err != nil || !earlier {
		t.Fatalf("causal singleton earlier selection=%v, %v", earlier, err)
	}
	if len(seen) != 2 || len(after) != 1 || len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 {
		t.Fatalf("Seen/After/Wrong=%d/%d/%d firings=%#v processes=%#v", len(seen), len(after), len(sourceNamedEvents(result.Poset, "worker", "Wrong")), result.Firings, result.Processes)
	}
	value, _ := after[0].Param("value")
	if value != int64(2) {
		t.Fatalf("After value=%#v, want 2", value)
	}
	if len(result.Processes) != 1 || !result.Processes[0].Terminated || result.Processes[0].Completion != "normal" {
		t.Fatalf("labeled when did not terminate normally: %#v", result.Processes)
	}
	if len(result.Firings) != 5 || len(result.Firings[1].Suspensions) != 1 || len(result.Firings[4].Suspensions) != 1 {
		t.Fatalf("labeled when firing/suspension audit=%#v", result.Firings)
	}

	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := result.MarshalCanonical()
	repeatedEncoded, _ := repeated.MarshalCanonical()
	replayedEncoded, _ := replayed.MarshalCanonical()
	if !bytes.Equal(encoded, repeatedEncoded) || !bytes.Equal(encoded, replayedEncoded) {
		t.Fatal("labeled when execution changed across GOMAXPROCS or exact replay")
	}
}

func TestSourceUnlabeledExitAndNextTargetTopLevelWhen(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); action out After(); action out Wrong(); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  count : var Integer := 0;
serial when Trigger do
  count := $count + 1;
  next where $count = 1;
  After();
  exit;
  Wrong();
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
		arch.InputEvent{Key: "one", Source: "stimulus", Action: "Trigger"},
		arch.InputEvent{Key: "two", Source: "stimulus", Action: "Trigger", Causes: []string{"one"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceNamedEvents(result.Poset, "worker", "After")) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 ||
		len(result.Processes) != 1 || !result.Processes[0].Terminated {
		t.Fatalf("unnamed when control result=%#v processes=%#v", result.Firings, result.Processes)
	}
}

func TestSourceWhenHandlerControlTargetsLabeledWhen(t *testing.T) {
	source := []byte(`
type Worker is interface action in Trigger(); action out Recovered(); action out Wrong(); end interface Worker;
type Stimulus is interface action out Trigger(); end interface Stimulus;
module WorkerModule() return Worker is
  exception Failure;
serial Cycle : when Trigger do
  raise Failure;
handler
  is Failure =>
    Recovered();
    exit Cycle;
    Wrong();
end when Cycle;
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
		arch.InputEvent{Key: "one", Source: "stimulus", Action: "Trigger"},
		arch.InputEvent{Key: "two", Source: "stimulus", Action: "Trigger", Causes: []string{"one"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(sourceNamedEvents(result.Poset, "worker", "Recovered")) != 1 ||
		len(sourceNamedEvents(result.Poset, "worker", "Wrong")) != 0 ||
		len(result.Processes) != 1 || !result.Processes[0].Terminated {
		t.Fatalf("when-handler control result=%#v processes=%#v", result.Firings, result.Processes)
	}
}

func TestSourceWhenLabelsAreCanonicalAndValidated(t *testing.T) {
	build := func(label string) []byte {
		return []byte(`
type Worker is interface action in Trigger(); end interface Worker;
module WorkerModule() return Worker is
serial ` + label + ` : when Trigger do exit ` + label + `; end do ` + label + `;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`)
	}
	lower, err := Compile(build("cycle"), "System")
	if err != nil {
		t.Fatal(err)
	}
	upper, err := Compile(build("CYCLE"), "System")
	if err != nil {
		t.Fatal(err)
	}
	lowerDigest, err := lower.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	upperDigest, err := upper.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if lowerDigest != upperDigest {
		t.Fatalf("when-label case changed model identity: %s != %s", lowerDigest, upperDigest)
	}

	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "mismatched terminator", body: "Cycle : when Trigger do exit Cycle; end when Wrong;", want: "does not match statement label"},
		{name: "unlabeled named terminator", body: "when Trigger do exit; end when Cycle;", want: "named do terminator requires a statement label"},
		{name: "non-enclosing target", body: "Cycle : when Trigger do exit Missing; end when Cycle;", want: "names non-enclosing do"},
		{name: "nested duplicate", body: "Cycle : when Trigger do Cycle : loop do exit Cycle; end do Cycle; end when Cycle;", want: "overloads do label"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type Worker is interface action in Trigger(); end interface Worker; module WorkerModule() return Worker is serial " + test.body + " end module WorkerModule; architecture System() is worker : Worker is WorkerModule(); end architecture System;")
			model, compileErr := Compile(source, "System")
			if compileErr == nil {
				_, compileErr = model.DeterministicModelDigest()
			}
			if compileErr == nil || !strings.Contains(compileErr.Error(), test.want) {
				t.Fatalf("got %v, want containing %q", compileErr, test.want)
			}
		})
	}

	t.Run("duplicate sibling process labels", func(t *testing.T) {
		source := []byte(`
type Worker is interface action in First(); action in Second(); end interface Worker;
module WorkerModule() return Worker is
parallel Cycle : when First do exit Cycle; end when Cycle;
|| Cycle : when Second do exit Cycle; end when Cycle;
end module WorkerModule;
architecture System() is worker : Worker is WorkerModule(); end architecture System;
`)
		_, err := Compile(source, "System")
		if err == nil || !strings.Contains(err.Error(), "overloads do label") {
			t.Fatalf("got %v, want duplicate sibling process-label rejection", err)
		}
	})
}
