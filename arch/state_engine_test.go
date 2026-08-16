package arch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestProcessStateAssignmentReadProvenanceAndReplay(t *testing.T) {
	architecture := NewArchitecture("state-read-write")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input", P("n", "Integer")).
		OutAction("Output", P("n", "Integer")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("current", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	trigger := pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))
	process := Process("worker-process").StartAt("wait").States(
		AwaitState("wait", Await("update").On(trigger).
			Assign(AssignState("current", BoundValue("N"))).
			Emit("Output", StateParam("n", "current")).Terminate().Build()),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}

	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "worker", Action: "Input", Params: map[string]any{"n": 7}},
	)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	output := result.Poset.ByName("Output")
	if len(output) != 1 {
		t.Fatalf("output count=%d, want 1", len(output))
	}
	if value, ok := output[0].Param("n"); !ok || value != int64(7) {
		t.Fatalf("state-backed output value=%#v,%v; want int64(7)", value, ok)
	}
	input := result.Poset.ByName("Input")[0]
	if !result.Poset.IsCausallyBefore(input.ID, output[0].ID) {
		t.Fatal("state-backed output lost trigger/write provenance")
	}
	if len(result.State) != 1 || result.State[0].Name != "current" ||
		result.State[0].Version != 1 || result.State[0].Value.Kind != "integer" ||
		result.State[0].Value.Text != "7" || len(result.State[0].Causes) != 1 ||
		result.State[0].Causes[0] != string(input.ID) {
		t.Fatalf("final state is incomplete: %#v", result.State)
	}
	if len(result.Firings) != 1 || len(result.Firings[0].StateReads) != 1 ||
		result.Firings[0].StateReads[0].ComponentID != "worker" ||
		result.Firings[0].StateReads[0].Name != "current" || result.Firings[0].StateReads[0].Version != 1 ||
		len(result.Firings[0].StateWrites) != 1 ||
		result.Firings[0].StateWrites[0].ComponentID != "worker" ||
		result.Firings[0].StateWrites[0].Version != 1 {
		t.Fatalf("state access audit is incomplete: %#v", result.Firings)
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("stateful replay was not byte-identical")
	}
}

func TestDeclarativeRuleCanUpdateAndReadClosedState(t *testing.T) {
	architecture := NewArchitecture("rule-state")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input", P("n", "Integer")).
		OutAction("Output", P("n", "Integer")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("current", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	trigger := pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))
	rule := Rule("update").On(trigger).
		Assign(AssignState("current", BoundValue("N"))).
		Emit("Output", StateParam("n", "current")).Build()
	if err := component.AddDeclarativeRule(rule); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "worker", Action: "Input", Params: map[string]any{"n": 9}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := result.Poset.ByName("Output")[0].Param("n"); value != int64(9) {
		t.Fatalf("rule state output=%#v; want 9", value)
	}
	if len(result.Firings) != 1 || len(result.Firings[0].StateWrites) != 1 ||
		len(result.Firings[0].StateReads) != 1 || result.State[0].Value.Text != "9" {
		t.Fatalf("rule state audit is incomplete: firing=%#v state=%#v", result.Firings, result.State)
	}
}

func TestStateWriteCarriesPriorProcessEventIntoAnotherProcessRead(t *testing.T) {
	architecture := NewArchitecture("state-causality")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Continue").OutAction("Read").
		OutAction("A").OutAction("B", P("flag", "Boolean")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.SetModuleProcessMode(ParallelProcesses); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("flag", "Boolean", false)); err != nil {
		t.Fatal(err)
	}
	writer := Process("writer").StartAt("generate").States(
		AwaitState("generate", Await("make-a").On(pattern.MatchEvent("Start")).Emit("A").Then("write").Build()),
		AwaitState("write", Await("store").On(pattern.MatchEvent("Continue")).
			Assign(AssignState("flag", LiteralValue(true))).NoEvents().Terminate().Build()),
	).Build()
	reader := Process("reader").StartAt("read").States(
		AwaitState("read", Await("load").On(pattern.MatchEvent("Read")).
			Emit("B", StateParam("flag", "flag")).Terminate().Build()),
	).Build()
	if err := component.AddDeclarativeProcess(writer); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(reader); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20,
		InputEvent{Key: "1-start", Source: "worker", Action: "Start"},
		InputEvent{Key: "2-continue", Source: "worker", Action: "Continue", Causes: []string{"1-start"}},
		InputEvent{Key: "3-read", Source: "worker", Action: "Read", Causes: []string{"2-continue"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	a := result.Poset.ByName("A")
	b := result.Poset.ByName("B")
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("missing state causality events: A=%d B=%d", len(a), len(b))
	}
	if !result.Poset.IsCausallyBefore(a[0].ID, b[0].ID) {
		t.Fatal("a prior writer-process event did not cause the reader-process output through state")
	}
	if value, ok := b[0].Param("flag"); !ok || value != true {
		t.Fatalf("reader observed %#v,%v; want true", value, ok)
	}
	foundWrite := false
	for _, firing := range result.Firings {
		for _, write := range firing.StateWrites {
			if write.Name == "flag" {
				foundWrite = true
				foundA := false
				for _, cause := range write.Causes {
					foundA = foundA || cause == string(a[0].ID)
				}
				if !foundA {
					t.Fatalf("state write omitted writer frontier A: %#v", write)
				}
			}
		}
	}
	if !foundWrite {
		t.Fatal("state write was not audited")
	}
}

func TestNullProcessTransitionCarriesTriggerIntoLaterEventCausality(t *testing.T) {
	architecture := NewArchitecture("null-transition-frontier")
	component := NewComponent("worker", Interface("Worker").
		OutAction("First").OutAction("Second").OutAction("Result").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	process := Process("p").StartAt("first").States(
		AwaitState("first", Await("consume").On(pattern.MatchEvent("First")).NoEvents().Then("second").Build()),
		AwaitState("second", Await("emit").On(pattern.MatchEvent("Second")).Emit("Result").Terminate().Build()),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "first", Source: "worker", Action: "First"},
		InputEvent{Key: "second", Source: "worker", Action: "Second"},
	))
	if err != nil {
		t.Fatal(err)
	}
	first := result.Poset.ByName("First")[0]
	generated := result.Poset.ByName("Result")[0]
	if !result.Poset.IsCausallyBefore(first.ID, generated.ID) {
		t.Fatal("later process event is independent of the trigger consumed by a null prior transition")
	}
}

func TestStateOperationFrontierCrossesNullProcessTransition(t *testing.T) {
	architecture := NewArchitecture("state-operation-null-transition")
	component := NewComponent("worker", Interface("Worker").
		OutAction("First").OutAction("Second").OutAction("Result").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("value", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	process := Process("p").StartAt("first").States(
		AwaitState("first", Await("write").On(pattern.MatchEvent("First")).
			Assign(AssignState("value", LiteralValue(1))).NoEvents().Then("second").Build()),
		AwaitState("second", Await("emit").On(pattern.MatchEvent("Second")).
			Emit("Result").Terminate().Build()),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "first", Source: "worker", Action: "First"},
		InputEvent{Key: "second", Source: "worker", Action: "Second"},
	))
	if err != nil {
		t.Fatal(err)
	}
	generated := result.Poset.ByName("Result")
	if len(generated) != 1 || len(result.StateOperations) != 2 ||
		len(result.StateOperations[1].Successors) != 1 ||
		result.StateOperations[1].Successors[0] != string(generated[0].ID) {
		t.Fatalf("state-only process operation frontier=%#v result=%#v", result.StateOperations, generated)
	}
	augmented, err := result.AugmentedComputation()
	if err != nil {
		t.Fatal(err)
	}
	witnesses, err := augmented.ConsistentCutStateWitnesses(
		[]string{string(generated[0].ID)},
		ConsistentCutLimits{MaxCuts: 20, MaxOptionalOccurrences: 20},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(witnesses) != 1 || len(witnesses[0].State) != 1 || witnesses[0].State[0].Version != 1 {
		t.Fatalf("generated-event state witnesses=%#v, want exactly version 1", witnesses)
	}
}

func stateScheduleArchitecture(t *testing.T) *Architecture {
	t.Helper()
	architecture := NewArchitecture("state-schedule")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input").OutAction("Written").
		OutAction("Observed", P("value", "Integer")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.SetModuleProcessMode(ParallelProcesses); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("value", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	reader := Process("reader").StartAt("wait").States(
		AwaitState("wait", Await("read").On(pattern.MatchEvent("Input")).
			Emit("Observed", StateParam("value", "value")).Terminate().Build()),
	).Build()
	writer := Process("writer").StartAt("wait").States(
		AwaitState("wait", Await("write").On(pattern.MatchEvent("Input")).
			Assign(AssignState("value", LiteralValue(1))).Emit("Written").Terminate().Build()),
	).Build()
	if err := component.AddDeclarativeProcess(writer); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(reader); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestStatefulProcessSchedulesReplayAndExploreDistinctPosets(t *testing.T) {
	architecture := stateScheduleArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 20, InputEvent{Key: "input", Source: "worker", Action: "Input"})
	canonical, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	observed := canonical.Poset.ByName("Observed")
	if len(observed) != 1 {
		t.Fatalf("observed count=%d", len(observed))
	}
	if value, _ := observed[0].Param("value"); value != int64(0) {
		t.Fatalf("canonical reader-first schedule observed %#v; want 0", value)
	}
	var writerOption string
	for _, option := range canonical.Choices[0].Options {
		if strings.Contains(option, "/writer/") {
			writerOption = option
		}
	}
	journal.Choices = []ChoiceDecision{{Point: canonical.Choices[0].Point, Selection: writerOption}}
	writerFirst, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := writerFirst.Poset.ByName("Observed")[0].Param("value"); value != int64(1) {
		t.Fatalf("writer-first schedule observed %#v; want 1", value)
	}
	expected, _ := writerFirst.ArtifactDigest()
	if _, err := architecture.ReplayDeterministic(journal, expected); err != nil {
		t.Fatal(err)
	}

	journal.Choices = nil
	explored, err := architecture.ExploreDeterministic(journal,
		ExplorationLimits{MaxExecutions: 10, MaxChoiceDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || explored.Executions != 3 || len(explored.Computations) != 2 {
		t.Fatalf("state-dependent schedules were not fully explored: %#v", explored)
	}
}

func TestStateDeclarationsAreCanonicalAndInvalidStateFailsExplicitly(t *testing.T) {
	makeArchitecture := func(reverse bool, initial int) *Architecture {
		architecture := NewArchitecture("state-model")
		component := NewComponent("worker", Interface("Worker").OutAction("Input").Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		declarations := []StateDeclaration{
			StateReference("count", "Integer", initial),
			StateReference("ready", "Boolean", false),
		}
		if reverse {
			declarations[0], declarations[1] = declarations[1], declarations[0]
		}
		if err := component.DeclareState(declarations...); err != nil {
			t.Fatal(err)
		}
		return architecture
	}
	forward, err := makeArchitecture(false, 0).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := makeArchitecture(true, 0).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	changed, err := makeArchitecture(false, 1).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if forward != reverse || forward == changed {
		t.Fatalf("state model identity is not canonical/content-sensitive: %s %s %s", forward, reverse, changed)
	}

	tests := []struct {
		name  string
		state []StateDeclaration
	}{
		{name: "duplicate", state: []StateDeclaration{StateReference("x", "Integer", 0), StateReference("x", "Integer", 0)}},
		{name: "unsupported type", state: []StateDeclaration{StateReference("x", "HostPointer", 0)}},
		{name: "wrong initial type", state: []StateDeclaration{StateReference("x", "Boolean", 0)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			architecture := NewArchitecture("invalid-state")
			component := NewComponent("worker", Interface("Worker").OutAction("Input").Build(), nil)
			if err := architecture.AddComponent(component); err != nil {
				t.Fatal(err)
			}
			if err := component.DeclareState(test.state...); err != nil {
				t.Fatal(err)
			}
			if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidStateReference) {
				t.Fatalf("expected explicit invalid state error, got %v", err)
			}
		})
	}

	t.Run("undeclared assignment", func(t *testing.T) {
		architecture := NewArchitecture("invalid-state-body")
		component := NewComponent("worker", Interface("Worker").OutAction("Input").Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		process := Process("p").StartAt("wait").States(
			AwaitState("wait", Await("a").On(pattern.MatchEvent("Input")).
				Assign(AssignState("missing", LiteralValue(1))).NoEvents().Terminate().Build()),
		).Build()
		if err := component.AddDeclarativeProcess(process); err != nil {
			t.Fatal(err)
		}
		if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidStateReference) {
			t.Fatalf("expected undeclared assignment error, got %v", err)
		}
	})
}
