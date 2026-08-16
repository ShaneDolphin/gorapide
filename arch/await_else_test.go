package arch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func awaitElseArchitecture(t *testing.T, process *DeclarativeProcess) *Architecture {
	t.Helper()
	architecture := NewArchitecture("await-else")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input").
		OutAction("Ready").
		OutAction("Matched").
		OutAction("Default").
		OutAction("Tick").Build(), nil)
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestAwaitElseExecutesAtInitialActivationWithoutInput(t *testing.T) {
	process := Process("p").StartAt("wait").States(
		AwaitStateWithElse("wait",
			AwaitElse("default").Emit("Default").Terminate().Build(),
			Await("input").On(pattern.MatchEvent("Input")).Emit("Matched").Terminate().Build(),
		),
	).Build()
	architecture := awaitElseArchitecture(t, process)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10)
	first, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("eventless await else execution is not byte-identical")
	}
	if len(first.Poset.ByName("Default")) != 1 || len(first.Poset.ByName("Matched")) != 0 {
		t.Fatal("await else did not select its nonblocking default body")
	}
	if len(first.Firings) != 1 || first.Firings[0].AlternativeID != "default" || len(first.Firings[0].MatchedEvents) != 0 {
		t.Fatalf("await else firing audit=%#v", first.Firings)
	}
	if len(first.Processes) != 1 || !first.Processes[0].Terminated {
		t.Fatalf("await else final process state=%#v", first.Processes)
	}
}

func TestInitialAwaitElsePrecedesFirstJournalObservation(t *testing.T) {
	process := Process("p").StartAt("wait").States(
		AwaitStateWithElse("wait",
			AwaitElse("default").Emit("Default").Terminate().Build(),
			Await("input").On(pattern.MatchEvent("Input")).Emit("Matched").Terminate().Build(),
		),
	).Build()
	architecture := awaitElseArchitecture(t, process)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "worker", Action: "Input"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Default")) != 1 || len(result.Poset.ByName("Matched")) != 0 {
		t.Fatal("journal input became available before the documented initial process activation")
	}
	if len(result.Firings) != 1 || result.Firings[0].AlternativeID != "default" {
		t.Fatalf("initial activation audit=%#v", result.Firings)
	}
}

func TestAwaitElseDoesNotRunWhenMatchIsAvailableAtStateEntry(t *testing.T) {
	process := Process("p").StartAt("start").States(
		AwaitState("start", Await("start").On(pattern.MatchEvent("Input")).Emit("Ready").Then("check").Build()),
		AwaitStateWithElse("check",
			AwaitElse("default").Emit("Default").Terminate().Build(),
			Await("ready").On(pattern.MatchEvent("Ready")).Emit("Matched").Terminate().Build(),
		),
	).Build()
	architecture := awaitElseArchitecture(t, process)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "worker", Action: "Input"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Matched")) != 1 || len(result.Poset.ByName("Default")) != 0 {
		t.Fatalf("available await match did not take priority over else: %#v", result.Firings)
	}
	if len(result.Firings) != 2 || result.Firings[1].AlternativeID != "ready" || len(result.Firings[1].MatchedEvents) != 1 {
		t.Fatalf("matched await audit=%#v", result.Firings)
	}
}

func TestAwaitElseAfterNullTransitionRetainsProcessCausality(t *testing.T) {
	process := Process("p").StartAt("start").States(
		AwaitState("start", Await("start").On(pattern.MatchEvent("Input")).NoEvents().Then("check").Build()),
		AwaitStateWithElse("check",
			AwaitElse("default").Emit("Default").Terminate().Build(),
			Await("missing").On(pattern.MatchEvent("Ready")).Emit("Matched").Terminate().Build(),
		),
	).Build()
	architecture := awaitElseArchitecture(t, process)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "worker", Action: "Input"},
	))
	if err != nil {
		t.Fatal(err)
	}
	input, output := result.Poset.ByName("Input"), result.Poset.ByName("Default")
	if len(input) != 1 || len(output) != 1 || !result.Poset.IsCausallyBefore(input[0].ID, output[0].ID) {
		t.Fatal("await else lost the process frontier across a null transition")
	}
	if len(result.Firings) != 2 || len(result.Firings[1].MatchedEvents) != 0 {
		t.Fatalf("null-transition await else audit=%#v", result.Firings)
	}
}

func TestEmptyAwaitTriggerActivatesWithoutExternalEventAndIsBounded(t *testing.T) {
	empty := pattern.IterateZeroOrMore(pattern.MatchEvent("Input"), pattern.RelationDisjoint)
	process := Process("p").StartAt("wait").States(
		AwaitState("wait", Await("empty").On(empty).Emit("Tick").Then("wait").Build()),
	).Build()
	architecture := awaitElseArchitecture(t, process)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	_, err = architecture.ExecuteDeterministic(NewExecutionJournal(digest, 3))
	if !errors.Is(err, ErrExecutionLimit) || !strings.Contains(err.Error(), "max_firings=3") {
		t.Fatalf("empty await activation got %v, want explicit firing bound", err)
	}
}

func TestAwaitElseBodyChangesCanonicalModelIdentity(t *testing.T) {
	build := func(action string) string {
		t.Helper()
		process := Process("p").StartAt("wait").States(
			AwaitStateWithElse("wait",
				AwaitElse("default").Emit(action).Terminate().Build(),
				Await("input").On(pattern.MatchEvent("Input")).Emit("Matched").Terminate().Build(),
			),
		).Build()
		architecture := awaitElseArchitecture(t, process)
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	if build("Default") == build("Tick") {
		t.Fatal("await else body did not change canonical model identity")
	}
}

func TestAwaitElseValidationRejectsMalformedBranches(t *testing.T) {
	tests := []struct {
		name  string
		state ProcessState
		want  string
	}{
		{
			name:  "missing ordinary trigger",
			state: AwaitState("wait", Await("ordinary").Emit("Matched").Terminate().Build()),
			want:  "has no trigger",
		},
		{
			name: "else trigger",
			state: AwaitStateWithElse("wait",
				AwaitElse("default").On(pattern.MatchEvent("Input")).Emit("Default").Terminate().Build(),
				Await("ordinary").On(pattern.MatchEvent("Input")).Emit("Matched").Terminate().Build()),
			want: "cannot declare a trigger or guard",
		},
		{
			name: "else guard",
			state: AwaitStateWithElse("wait",
				AwaitElse("default").Where(LiteralValue(true)).Emit("Default").Terminate().Build(),
				Await("ordinary").On(pattern.MatchEvent("Input")).Emit("Matched").Terminate().Build()),
			want: "cannot declare a trigger or guard",
		},
		{
			name: "duplicate else identity",
			state: AwaitStateWithElse("wait",
				AwaitElse("same").Emit("Default").Terminate().Build(),
				Await("same").On(pattern.MatchEvent("Input")).Emit("Matched").Terminate().Build()),
			want: "duplicate else alternative",
		},
		{
			name: "missing else next state",
			state: AwaitStateWithElse("wait",
				AwaitElse("default").Emit("Default").Then("missing").Build(),
				Await("ordinary").On(pattern.MatchEvent("Input")).Emit("Matched").Terminate().Build()),
			want: "references missing next state",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			process := Process("p").StartAt("wait").States(test.state).Build()
			architecture := awaitElseArchitecture(t, process)
			_, err := architecture.DeterministicModelDigest()
			if !errors.Is(err, ErrUnsupportedDeterministicModel) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want unsupported model error containing %q", err, test.want)
			}
		})
	}
}
