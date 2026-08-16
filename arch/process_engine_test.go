package arch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func awaitChoiceArchitecture(t *testing.T, reverse bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("await-choice")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input", P("n", "Integer")).
		OutAction("Accepted", P("n", "Integer")).
		OutAction("Rejected", P("n", "Integer")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	trigger := pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))
	alternatives := []AwaitAlternative{
		Await("accept").On(trigger).Emit("Accepted", BindingParam("n", "N")).Terminate().Build(),
		Await("reject").On(trigger).Emit("Rejected", BindingParam("n", "N")).Terminate().Build(),
	}
	if reverse {
		alternatives[0], alternatives[1] = alternatives[1], alternatives[0]
	}
	process := Process("worker-process").StartAt("waiting").States(
		AwaitState("waiting", alternatives...),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestDeclarativeAwaitChoiceIsDeterministicAuditableAndReplayable(t *testing.T) {
	architecture := awaitChoiceArchitecture(t, false)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "worker", Action: "Input", Params: map[string]any{"n": 7}},
	)
	defaultResult, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultResult.Choices) != 1 || defaultResult.Choices[0].Scheduled {
		t.Fatalf("missing default await choice witness: %#v", defaultResult.Choices)
	}
	if len(defaultResult.Poset.ByName("Accepted")) != 1 || len(defaultResult.Poset.ByName("Rejected")) != 0 {
		t.Fatal("canonical await choice did not select the stable accept alternative")
	}
	if len(defaultResult.Firings) != 1 || defaultResult.Firings[0].Transition != "process" ||
		defaultResult.Firings[0].ProcessID != "worker-process" ||
		defaultResult.Firings[0].ProcessState != "waiting" ||
		defaultResult.Firings[0].AlternativeID != "accept" {
		t.Fatalf("incomplete await firing witness: %#v", defaultResult.Firings)
	}
	input := defaultResult.Poset.ByName("Input")[0]
	accepted := defaultResult.Poset.ByName("Accepted")[0]
	if value, ok := accepted.Param("n"); !ok || value != int64(7) {
		t.Fatalf("await binding=%#v,%v; want int64(7)", value, ok)
	}
	if !defaultResult.Poset.IsCausallyBefore(input.ID, accepted.ID) {
		t.Fatal("await body does not causally depend on its triggering match")
	}
	if len(defaultResult.Processes) != 1 || !defaultResult.Processes[0].Terminated || defaultResult.Processes[0].State != "" {
		t.Fatalf("final process control state is not auditable: %#v", defaultResult.Processes)
	}

	var reject string
	for _, option := range defaultResult.Choices[0].Options {
		if strings.Contains(option, "/reject@") {
			reject = option
		}
	}
	journal.Choices = []ChoiceDecision{{Point: defaultResult.Choices[0].Point, Selection: reject}}
	rejectedResult, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejectedResult.Poset.ByName("Accepted")) != 0 || len(rejectedResult.Poset.ByName("Rejected")) != 1 {
		t.Fatal("explicit await choice schedule was not honored")
	}
	expected, err := rejectedResult.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	left, err := rejectedResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	right, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("scheduled await replay did not reproduce byte-identical artifact")
	}
}

func TestDeclarativeWhenRepeatsAndPreservesSequentialProcessCausality(t *testing.T) {
	architecture := NewArchitecture("when-loop")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input", P("n", "Integer")).
		OutAction("Seen", P("n", "Integer")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	trigger := pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))
	state := WhenState("watch", trigger, EventBody(
		RuleEvent("seen", "Seen", BindingParam("n", "N")),
	))
	if err := component.AddDeclarativeProcess(
		Process("watcher").StartAt("watch").States(state).Build(),
	); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "one", Source: "worker", Action: "Input", Params: map[string]any{"n": 1}},
		InputEvent{Key: "two", Source: "worker", Action: "Input", Params: map[string]any{"n": 2}},
	))
	if err != nil {
		t.Fatal(err)
	}
	outputs := result.Poset.ByName("Seen")
	if len(outputs) != 2 || len(result.Firings) != 2 {
		t.Fatalf("when did not repeat once per consumed event: outputs=%d firings=%d", len(outputs), len(result.Firings))
	}
	ordered := result.Poset.IsCausallyBefore(outputs[0].ID, outputs[1].ID) ||
		result.Poset.IsCausallyBefore(outputs[1].ID, outputs[0].ID)
	if !ordered {
		t.Fatal("events generated by one sequential process are not totally ordered")
	}
	if len(result.Processes) != 1 || result.Processes[0].Terminated || result.Processes[0].State != "watch" {
		t.Fatalf("when process did not return to its suspended state: %#v", result.Processes)
	}
	if len(result.Consumption.Rules) != 1 || len(result.Consumption.Rules[0].Events) != 2 {
		t.Fatalf("process-scoped consumption is incomplete: %#v", result.Consumption)
	}
}

func TestAwaitSelectsCausalEarliestMatchWithoutBehaviorFirstPriority(t *testing.T) {
	poset := gorapide.NewPoset()
	early := &gorapide.Event{ID: "early", Source: "worker", Name: "Input"}
	late := &gorapide.Event{ID: "late", Source: "worker", Name: "Input"}
	if err := poset.AddEvent(early); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(late, early.ID); err != nil {
		t.Fatal(err)
	}
	state := AwaitState("waiting",
		Await("receive").On(pattern.MatchEvent("Input")).Terminate().Build(),
	)
	// Deliberately oppose canonical observation rank to causal order. Module
	// processes use earliest/maximal selection; behavior-only "first" priority
	// must not discard the causally earlier process match.
	ranks := map[string]uint64{
		observationRankKey(early): 2,
		observationRankKey(late):  1,
	}
	candidates, err := eligibleAwaitCandidates(
		"worker", "watcher", state, poset, gorapide.EventSet{late, early}, ranks,
		NewRuleConsumption(), processConsumptionScope("worker", "watcher"),
		nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || len(candidates[0].match.Events) != 1 || candidates[0].match.Events[0].ID != early.ID {
		t.Fatalf("await candidates = %#v, want only causally earliest event %s", candidates, early.ID)
	}
}

func TestAwaitExplorationReturnsBothPermittedPosetsInCanonicalOrder(t *testing.T) {
	architecture := awaitChoiceArchitecture(t, true)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "worker", Action: "Input", Params: map[string]any{"n": 1}},
	)
	result, err := architecture.ExploreDeterministic(journal,
		ExplorationLimits{MaxExecutions: 10, MaxChoiceDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || len(result.Computations) != 2 || result.Executions != 3 {
		t.Fatalf("await exploration is incomplete: %#v", result)
	}
	if result.Computations[0].PosetDigest >= result.Computations[1].PosetDigest {
		t.Fatal("exploration results are not canonically ordered by semantic poset digest")
	}
	names := make(map[string]bool)
	for _, computation := range result.Computations {
		for _, event := range computation.Result.Poset.Events() {
			if event.Name == "Accepted" || event.Name == "Rejected" {
				names[event.Name] = true
			}
		}
	}
	if !names["Accepted"] || !names["Rejected"] {
		t.Fatalf("exploration omitted a permitted await alternative: %#v", names)
	}
}

func TestDeclarativeProcessModelIgnoresStateAndAlternativeDeclarationOrder(t *testing.T) {
	forward := awaitChoiceArchitecture(t, false)
	reverse := awaitChoiceArchitecture(t, true)
	left, err := forward.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	right, err := reverse.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("process declaration order changed model identity: %s != %s", left, right)
	}
}

func multiProcessArchitecture(t *testing.T, mode ModuleProcessMode, reverse bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("multi-process")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input").OutAction("One").OutAction("Two").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if mode != UnspecifiedProcessMode {
		if err := component.SetModuleProcessMode(mode); err != nil {
			t.Fatal(err)
		}
	}
	type processOutput struct {
		process string
		action  string
	}
	declarations := []processOutput{{process: "one", action: "One"}, {process: "two", action: "Two"}}
	if reverse {
		declarations[0], declarations[1] = declarations[1], declarations[0]
	}
	for _, declaration := range declarations {
		process := Process(declaration.process).StartAt("wait").States(
			AwaitState("wait", Await("receive").On(pattern.MatchEvent("Input")).
				Emit(declaration.action).Terminate().Build()),
		).Build()
		if err := component.AddDeclarativeProcess(process); err != nil {
			t.Fatal(err)
		}
	}
	return architecture
}

func TestSerialAndParallelProcessesUseReplayableSchedulesWithoutFalseCausality(t *testing.T) {
	for _, mode := range []ModuleProcessMode{SerialProcesses, ParallelProcesses} {
		t.Run(mode.String(), func(t *testing.T) {
			architecture := multiProcessArchitecture(t, mode, true)
			digest, err := architecture.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			forwardDigest, err := multiProcessArchitecture(t, mode, false).DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			if digest != forwardDigest {
				t.Fatal("process declaration order changed the multi-process model digest")
			}

			journal := NewExecutionJournal(digest, 10,
				InputEvent{Key: "input", Source: "worker", Action: "Input"},
			)
			canonical, err := architecture.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			one := canonical.Poset.ByName("One")
			two := canonical.Poset.ByName("Two")
			if len(one) != 1 || len(two) != 1 || len(canonical.Processes) != 2 {
				t.Fatalf("both processes did not observe the input: one=%d two=%d processes=%#v", len(one), len(two), canonical.Processes)
			}
			if !canonical.Poset.IsCausallyIndependent(one[0].ID, two[0].ID) {
				t.Fatal("scheduler order created a false causal relation between independent processes")
			}
			if len(canonical.Consumption.Rules) != 2 ||
				len(canonical.Consumption.Rules[0].Events) != 1 ||
				len(canonical.Consumption.Rules[1].Events) != 1 ||
				canonical.Consumption.Rules[0].Events[0] != canonical.Consumption.Rules[1].Events[0] {
				t.Fatalf("the input was not independently available to both processes: %#v", canonical.Consumption)
			}
			if len(canonical.Choices) != 1 || canonical.Choices[0].Domain != "process-schedule:"+mode.String()+":worker" {
				t.Fatalf("missing auditable module schedule choice: %#v", canonical.Choices)
			}

			var selectTwo string
			for _, option := range canonical.Choices[0].Options {
				if strings.Contains(option, "/two/") {
					selectTwo = option
				}
			}
			journal.Choices = []ChoiceDecision{{Point: canonical.Choices[0].Point, Selection: selectTwo}}
			scheduled, err := architecture.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			if len(scheduled.Firings) != 2 || scheduled.Firings[0].ProcessID != "two" {
				t.Fatalf("explicit module schedule was not honored: %#v", scheduled.Firings)
			}
			leftPoset, err := canonical.Poset.SemanticDigest()
			if err != nil {
				t.Fatal(err)
			}
			rightPoset, err := scheduled.Poset.SemanticDigest()
			if err != nil {
				t.Fatal(err)
			}
			if leftPoset != rightPoset {
				t.Fatal("scheduling independent processes changed the semantic poset")
			}
			expected, err := scheduled.ArtifactDigest()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := architecture.ReplayDeterministic(journal, expected); err != nil {
				t.Fatalf("scheduled module replay failed: %v", err)
			}
		})
	}
}

func TestMultiProcessModeIsSemanticAndSchedulesExploreToOneIndependentPoset(t *testing.T) {
	serial := multiProcessArchitecture(t, SerialProcesses, false)
	parallel := multiProcessArchitecture(t, ParallelProcesses, false)
	serialDigest, err := serial.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	parallelDigest, err := parallel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if serialDigest == parallelDigest {
		t.Fatal("serial and parallel module declarations have the same model identity")
	}
	result, err := parallel.ExploreDeterministic(
		NewExecutionJournal(parallelDigest, 10, InputEvent{Key: "input", Source: "worker", Action: "Input"}),
		ExplorationLimits{MaxExecutions: 10, MaxChoiceDepth: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.Executions != 3 || len(result.Computations) != 1 {
		t.Fatalf("equivalent independent schedules were not completely explored and poset-deduplicated: %#v", result)
	}
}

func TestDeclarativeProcessRejectsInvalidControlGraphsAndUnsupportedMixing(t *testing.T) {
	makeComponent := func() (*Architecture, *Component) {
		architecture := NewArchitecture("invalid-process")
		component := NewComponent("component", Interface("Component").OutAction("Input").OutAction("Output").Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		return architecture, component
	}

	tests := []struct {
		name    string
		process *DeclarativeProcess
	}{
		{name: "missing initial", process: Process("p").StartAt("missing").States(
			AwaitState("wait", Await("a").On(pattern.MatchEvent("Input")).NoEvents().Terminate().Build()),
		).Build()},
		{name: "missing next", process: Process("p").StartAt("wait").States(
			AwaitState("wait", Await("a").On(pattern.MatchEvent("Input")).NoEvents().Then("missing").Build()),
		).Build()},
		{name: "duplicate state", process: Process("p").StartAt("wait").States(
			AwaitState("wait", Await("a").On(pattern.MatchEvent("Input")).NoEvents().Terminate().Build()),
			AwaitState("wait", Await("b").On(pattern.MatchEvent("Input")).NoEvents().Terminate().Build()),
		).Build()},
		{name: "nonsequential body", process: Process("p").StartAt("wait").States(
			AwaitState("wait", Await("a").On(pattern.MatchEvent("Input")).Generate(
				RuleEvent("one", "Output"), RuleEvent("two", "Output"),
			).Terminate().Build()),
		).Build()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			architecture, component := makeComponent()
			if err := component.AddDeclarativeProcess(test.process); err != nil {
				t.Fatal(err)
			}
			if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidDeclarativeProcess) {
				t.Fatalf("expected invalid process error, got %v", err)
			}
		})
	}

	t.Run("multiple processes without module mode", func(t *testing.T) {
		architecture, component := makeComponent()
		for _, id := range []string{"one", "two"} {
			if err := component.AddDeclarativeProcess(Process(id).StartAt("wait").States(
				AwaitState("wait", Await("a").On(pattern.MatchEvent("Input")).NoEvents().Terminate().Build()),
			).Build()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) {
			t.Fatalf("expected explicit missing module mode error, got %v", err)
		}
	})

	t.Run("rules and process", func(t *testing.T) {
		architecture, component := makeComponent()
		if err := component.AddDeclarativeRule(Rule("rule").On(pattern.MatchEvent("Input")).Emit("Output").Build()); err != nil {
			t.Fatal(err)
		}
		if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
			AwaitState("wait", Await("a").On(pattern.MatchEvent("Input")).NoEvents().Terminate().Build()),
		).Build()); err != nil {
			t.Fatal(err)
		}
		if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) {
			t.Fatalf("expected explicit rule/process mixing limitation, got %v", err)
		}
	})
}
