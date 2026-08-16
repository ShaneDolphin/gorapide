package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

const sourceAwaitSingle = `
type Worker is interface
  action out Request(n : Integer);
  action out Done(n : Integer);
end interface Worker;

module Awaiting() return Worker is
initial
  Request(7);
parallel
  await (?N : Integer) Request(?N) =>
    Done(?N);
  end await;
end module Awaiting;

architecture System() is
  worker : Worker is Awaiting();
end architecture System;
`

func processFiring(result *arch.ExecutionResult) *arch.FiringRecord {
	for index := range result.Firings {
		if result.Firings[index].Transition == "process" {
			return &result.Firings[index]
		}
	}
	return nil
}

func TestSourceTopLevelAwaitSelectsConsumesAndTerminates(t *testing.T) {
	model, err := Compile([]byte(sourceAwaitSingle), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 20)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	request, done := result.Poset.ByName("Request"), result.Poset.ByName("Done")
	if len(request) != 1 || len(done) != 1 {
		t.Fatalf("await events Request=%d Done=%d, want one each", len(request), len(done))
	}
	if value, ok := done[0].Param("n"); !ok || value != int64(7) {
		t.Fatalf("await binding=%#v,%v, want int64(7)", value, ok)
	}
	if !result.Poset.IsCausallyBefore(request[0].ID, done[0].ID) {
		t.Fatal("await body does not causally depend on its selected match")
	}
	firing := processFiring(result)
	if firing == nil || firing.ProcessState != "await" ||
		firing.AlternativeID != "alternative:000000" || len(firing.MatchedEvents) != 1 ||
		firing.MatchedEvents[0] != string(request[0].ID) {
		t.Fatalf("await firing audit=%#v", firing)
	}
	if len(result.Processes) != 1 || !result.Processes[0].Terminated || result.Processes[0].State != "" {
		t.Fatalf("await final process state=%#v", result.Processes)
	}

	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("source await replay was not byte-identical")
	}
}

func sourceAwaitChoice(reverse bool) string {
	alternatives := `
  await Input =>
    Accepted();
  or Input =>
    Rejected();
  end await;`
	if reverse {
		alternatives = `
  AWAIT input =>
    rejected();
  OR input =>
    accepted();
  END AWAIT;`
	}
	return `
type Worker is interface
  action out Input();
  action out Accepted(); action out Rejected();
end interface Worker;
module Awaiting() return Worker is
initial
  Input();
parallel` + alternatives + `
end module Awaiting;
architecture System() is worker : Worker is Awaiting(); end architecture System;
`
}

func TestSourceAwaitAlternativesAreOrderCaseAndGOMAXPROCSInvariant(t *testing.T) {
	models := make([]*arch.Architecture, 2)
	digests := make([]string, 2)
	for index, reverse := range []bool{false, true} {
		model, err := Compile([]byte(sourceAwaitChoice(reverse)), "System")
		if err != nil {
			t.Fatal(err)
		}
		models[index] = model
		digests[index], err = model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
	}
	if digests[0] != digests[1] {
		t.Fatalf("await alternative order/case changed model identity: %s != %s", digests[0], digests[1])
	}

	previous := runtime.GOMAXPROCS(1)
	first, err := models[0].ExecuteDeterministic(arch.NewExecutionJournal(digests[0], 20))
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := models[1].ExecuteDeterministic(arch.NewExecutionJournal(digests[1], 20))
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("await alternative order, case, or GOMAXPROCS changed the canonical artifact")
	}
	if len(first.Choices) != 1 || !strings.HasPrefix(first.Choices[0].Domain, "await:") ||
		len(first.Choices[0].Options) != 2 {
		t.Fatalf("await choice audit=%#v", first.Choices)
	}
	scheduledJournal := arch.NewExecutionJournal(digests[0], 20)
	scheduledJournal.Choices = []arch.ChoiceDecision{{
		Point: first.Choices[0].Point, Selection: first.Choices[0].Options[1],
	}}
	scheduled, err := models[0].ExecuteDeterministic(scheduledJournal)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Poset.ByName("Accepted")) == len(scheduled.Poset.ByName("Accepted")) ||
		len(first.Poset.ByName("Rejected")) == len(scheduled.Poset.ByName("Rejected")) {
		t.Fatal("explicit source-await choice did not select the other permitted body")
	}
	scheduledDigest, err := scheduled.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := models[1].ReplayDeterministic(scheduledJournal, scheduledDigest)
	if err != nil {
		t.Fatal(err)
	}
	scheduledBytes, _ := scheduled.MarshalCanonical()
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(scheduledBytes, replayedBytes) {
		t.Fatal("explicit source-await alternative did not replay byte-identically")
	}

	explored, err := models[0].ExploreDeterministic(
		arch.NewExecutionJournal(digests[0], 20),
		arch.ExplorationLimits{MaxExecutions: 8, MaxChoiceDepth: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 2 {
		t.Fatalf("await exploration=%d complete=%v, want two complete computations",
			len(explored.Computations), explored.Complete)
	}
	outputs := make(map[string]bool)
	for _, computation := range explored.Computations {
		if len(computation.Result.Poset.ByName("Accepted")) == 1 {
			outputs["Accepted"] = true
		}
		if len(computation.Result.Poset.ByName("Rejected")) == 1 {
			outputs["Rejected"] = true
		}
	}
	if !outputs["Accepted"] || !outputs["Rejected"] {
		t.Fatalf("await exploration missed a permitted alternative: %#v", outputs)
	}
}

func TestSourceAwaitElseUsesStartupMatchAndOtherwiseDefaults(t *testing.T) {
	build := func(initial string) string {
		return `
type Worker is interface
  action out Ready(); action out Hit(); action out Miss();
end interface Worker;
module Awaiting() return Worker is
` + initial + `
parallel
  await Ready => Hit();
  else Miss();
  end await;
end module Awaiting;
architecture System() is worker : Worker is Awaiting(); end architecture System;
`
	}
	matched, err := Compile([]byte(build("initial Ready();")), "System")
	if err != nil {
		t.Fatal(err)
	}
	matchedDigest, _ := matched.DeterministicModelDigest()
	matchedResult, err := matched.ExecuteDeterministic(arch.NewExecutionJournal(matchedDigest, 20))
	if err != nil {
		t.Fatal(err)
	}
	if len(matchedResult.Poset.ByName("Hit")) != 1 || len(matchedResult.Poset.ByName("Miss")) != 0 {
		t.Fatalf("available startup match did not take priority over await else: %#v", matchedResult.Firings)
	}

	defaulted, err := Compile([]byte(build("")), "System")
	if err != nil {
		t.Fatal(err)
	}
	defaultDigest, _ := defaulted.DeterministicModelDigest()
	defaultResult, err := defaulted.ExecuteDeterministic(arch.NewExecutionJournal(defaultDigest, 20,
		arch.InputEvent{Key: "late", Source: "worker", Action: "Ready"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultResult.Poset.ByName("Hit")) != 0 || len(defaultResult.Poset.ByName("Miss")) != 1 {
		t.Fatalf("await else did not run before the first journal observation: %#v", defaultResult.Firings)
	}
	firing := processFiring(defaultResult)
	if firing == nil || firing.AlternativeID != "else" || len(firing.MatchedEvents) != 0 {
		t.Fatalf("await else firing audit=%#v", firing)
	}
}

func TestSourceAwaitShorthandConsumesAndTerminatesWithoutOutput(t *testing.T) {
	source := []byte(`
type Worker is interface action out Ready(); end interface Worker;
module Awaiting() return Worker is initial Ready(); parallel await Ready; end module Awaiting;
architecture System() is worker : Worker is Awaiting(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10))
	if err != nil {
		t.Fatal(err)
	}
	firing := processFiring(result)
	if firing == nil || len(firing.MatchedEvents) != 1 || len(firing.Generated) != 0 {
		t.Fatalf("shorthand await firing=%#v", firing)
	}
	if len(result.Processes) != 1 || !result.Processes[0].Terminated {
		t.Fatalf("shorthand await process=%#v", result.Processes)
	}
}

func TestSourceAwaitFailuresAreExplicit(t *testing.T) {
	prefix := `
type Worker is interface
  action out Input(n : Integer); action out Ready(); action out Done(n : Integer);
end interface Worker;
module Awaiting() return Worker is parallel
`
	suffix := `
end module Awaiting;
architecture System() is worker : Worker is Awaiting(); end architecture System;
`
	tests := []struct {
		name, process, want string
	}{
		{"unbound placeholder", "await (?N : Integer) Ready => null; end await;", "never bound"},
		{"nonboolean guard", "await Input(1) where 1 => null; end await;", "guard has type Integer, want Boolean"},
		{"else placeholder scope", "await (?N : Integer) Input(?N) => null; else Done(?N); end await;", "not declared"},
		{"named terminator", "await Ready => null; end await Named;", "named await terminators"},
		{"empty long body", "await Ready => end await;", "await alternative statement"},
		{"nested await", "if true then await Ready; end if;", "nested await statements"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(prefix+test.process+suffix), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile error=%v, want substring %q", err, test.want)
			}
		})
	}
}
