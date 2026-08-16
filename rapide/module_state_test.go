package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func TestCompileModuleStateSurvivesTimedProcessAndAuditsCausality(t *testing.T) {
	source := []byte(`
type Driver is interface action out Start(n : Integer); end interface Driver;
type Worker is interface
  action in Start(n : Integer);
  action out Snapshot(n : Integer);
  action out Final(n : Integer);
end interface Worker;

module Stateful() return Worker is
  count : var Integer := 0;
  C : Clock is MakeClock();
serial
  when (?N : Integer) Start(?N) where ?N > $count do
    count := ?N;
    Snapshot($count) pause C.Ticks range 1..1;
    count := $count + 1;
    Final($count);
  end when;
end module Stateful;

architecture System() is
  driver : Driver;
  worker : Worker is Stateful();
connect driver.Start => worker.Start;
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
	journal := arch.NewExecutionJournal(digest, 20, arch.InputEvent{
		Key: "start", Source: "driver", Action: "Start", Params: map[string]any{"n": 3},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	clock := arch.ClockID("worker", "C")
	snapshot := result.Poset.ByName("Snapshot")
	final := result.Poset.ByName("Final")
	if len(snapshot) != 1 || len(final) != 1 {
		t.Fatalf("module state outputs: Snapshot=%d Final=%d", len(snapshot), len(final))
	}
	if value, _ := snapshot[0].Param("n"); value != int64(3) {
		t.Fatalf("Snapshot captured %#v, want 3", value)
	}
	if value, _ := final[0].Param("n"); value != int64(4) {
		t.Fatalf("Final captured %#v, want 4", value)
	}
	snapshotTiming, related := snapshot[0].Timing(clock)
	if !related || snapshotTiming.Start != 0 || snapshotTiming.Finish != 1 {
		t.Fatalf("Snapshot timing=%#v,%v", snapshotTiming, related)
	}
	finalTiming, related := final[0].Timing(clock)
	if !related || finalTiming.Start != 1 || finalTiming.Finish != 1 {
		t.Fatalf("Final timing=%#v,%v", finalTiming, related)
	}
	if !result.Poset.IsCausallyBefore(snapshot[0].ID, final[0].ID) {
		t.Fatal("module state/timing continuation lost process causality")
	}
	if len(result.State) != 1 || result.State[0].ComponentID != "worker" ||
		result.State[0].Name != "count" || result.State[0].Version != 2 || result.State[0].Value.Text != "4" {
		t.Fatalf("module final state=%#v", result.State)
	}
	var processFiring *arch.FiringRecord
	for index := range result.Firings {
		if result.Firings[index].Transition == "process" {
			processFiring = &result.Firings[index]
		}
	}
	if processFiring == nil || len(processFiring.StateWrites) != 2 ||
		len(processFiring.StateReads) < 3 || len(processFiring.Suspensions) != 1 {
		t.Fatalf("module state firing audit=%#v", processFiring)
	}
	for _, read := range processFiring.StateReads {
		if read.ComponentID != "worker" || read.Name != "count" {
			t.Fatalf("unqualified module state read=%#v", read)
		}
	}
	for _, write := range processFiring.StateWrites {
		if write.ComponentID != "worker" || write.Name != "count" {
			t.Fatalf("unqualified module state write=%#v", write)
		}
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
		t.Fatal("module state/timing replay was not byte-identical")
	}
}

func TestModuleStateIsIsolatedPerGeneratorInstance(t *testing.T) {
	source := []byte(`
type Driver is interface action out Start(n : Integer); end interface Driver;
type Worker is interface action in Start(n : Integer); action out Snapshot(n : Integer); end interface Worker;
module Stateful() return Worker is
  count : var Integer := 0;
parallel
  when (?N : Integer) Start(?N) do
    count := $count + ?N;
    Snapshot($count);
  end when;
end module Stateful;
architecture System() is
  d1 : Driver; d2 : Driver;
  w1 : Worker is Stateful(); w2 : Worker is Stateful();
connect
  d1.Start => w1.Start;
  d2.Start => w2.Start;
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
		arch.InputEvent{Key: "one", Source: "d1", Action: "Start", Params: map[string]any{"n": 2}},
		arch.InputEvent{Key: "two", Source: "d2", Action: "Start", Params: map[string]any{"n": 5}},
	))
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string)
	for _, state := range result.State {
		values[state.ComponentID+"."+state.Name] = state.Value.Text
	}
	if values["w1.count"] != "2" || values["w2.count"] != "5" || len(values) != 2 {
		t.Fatalf("module instance state was not isolated: %#v", values)
	}
	outputs := result.Poset.ByName("Snapshot")
	if len(outputs) != 2 {
		t.Fatalf("Snapshot outputs=%d, want two", len(outputs))
	}
	outputValues := make(map[string]int64)
	for _, event := range outputs {
		value, _ := event.Param("n")
		outputValues[event.Source] = value.(int64)
	}
	if outputValues["w1"] != 2 || outputValues["w2"] != 5 {
		t.Fatalf("module instance outputs crossed state: %#v", outputValues)
	}
}

func TestModuleStateDeclarationOrderIsNotSemantic(t *testing.T) {
	build := func(states string) []byte {
		return []byte(`
type W is interface action in Start(); action out Done(a : Integer, b : Integer); end interface W;
module M() return W is
` + states + `
parallel
  when Start do Done($a, $b); end when;
end module M;
architecture X() is w : W is M(); end architecture X;
`)
	}
	left, err := Compile(build("  a : var Integer := 1;\n  b : var Integer := 2;"), "X")
	if err != nil {
		t.Fatal(err)
	}
	right, err := Compile(build("  b : var Integer := 2;\n  a : var Integer := 1;"), "X")
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
		t.Fatalf("module state declaration order changed model identity: %s != %s", leftDigest, rightDigest)
	}
}

func TestCompileRejectsMalformedModuleState(t *testing.T) {
	tests := []struct {
		name  string
		state string
		body  string
		want  string
	}{
		{name: "uninitialized", state: "count : var Integer;", body: "Done(0);", want: "requires an explicit initializer"},
		{name: "duplicate", state: "count : var Integer := 0; COUNT : var Integer := 1;", body: "Done(0);", want: "duplicate module state"},
		{name: "initializer type mismatch", state: "count : var String := 0;", body: "Done(0);", want: "initializer has type Integer, want String"},
		{name: "wrong initializer", state: "count : var Integer := true;", body: "Done(0);", want: "initializer has type Boolean"},
		{name: "unknown assignment", state: "count : var Integer := 0;", body: "missing := 1;", want: "targets undeclared state"},
		{name: "wrong assignment type", state: "count : var Integer := 0;", body: "count := false;", want: "has type Boolean, want Integer"},
		{name: "missing dereference", state: "count : var Integer := 0;", body: "Done(count);", want: "must be dereferenced with '$'"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type W is interface action in Start(); action out Done(n : Integer); end interface W;
module M() return W is ` + test.state + ` parallel when Start do ` + test.body + ` end when; end module M;
architecture X() is w : W is M(); end architecture X;
`)
			_, err := Compile(source, "X")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want diagnostic containing %q", err, test.want)
			}
		})
	}
}

func TestSourceModuleStateStableAcrossGOMAXPROCS(t *testing.T) {
	source := []byte(`
type Driver is interface action out Start(n : Integer); end interface Driver;
type Worker is interface action in Start(n : Integer); action out Done(n : Integer); end interface Worker;
module M() return Worker is
  count : var Integer := 0;
parallel
  when (?N : Integer) Start(?N) do count := $count + ?N; Done($count); end when;
end module M;
architecture X() is d : Driver; w : Worker is M(); connect d.Start => w.Start; end architecture X;
`)
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for run := 0; run < 10; run++ {
			model, err := Compile(source, "X")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
				arch.InputEvent{Key: "start", Source: "d", Action: "Start", Params: map[string]any{"n": 7}},
			))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := result.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			if baseline == nil {
				baseline = encoded
			} else if !bytes.Equal(baseline, encoded) {
				t.Fatalf("module state changed under GOMAXPROCS=%d run=%d", processors, run)
			}
		}
	}
}
