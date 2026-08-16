package arch

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func observationChoiceArchitecture(t *testing.T) *Architecture {
	t.Helper()
	architecture := NewArchitecture("observation-choice")
	component := NewComponent("worker", Interface("Worker").
		OutAction("A").OutAction("B").OutAction("Picked", P("name", "String")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	process := Process("picker").StartAt("wait").States(
		AwaitState("wait",
			Await("a").On(pattern.MatchEvent("A")).Emit("Picked", LiteralParam("name", "A")).Terminate().Build(),
			Await("b").On(pattern.MatchEvent("B")).Emit("Picked", LiteralParam("name", "B")).Terminate().Build(),
		),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestIndependentEventObservationOrderIsScheduledAndReplayable(t *testing.T) {
	architecture := observationChoiceArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "a", Source: "worker", Action: "A"},
		InputEvent{Key: "b", Source: "worker", Action: "B"},
	)
	canonical, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical.Choices) == 0 || canonical.Choices[0].Domain != "event-observation" || len(canonical.Choices[0].Options) != 2 {
		t.Fatalf("missing observation-order choice witness: %#v", canonical.Choices)
	}
	var selectB string
	for _, option := range canonical.Choices[0].Options {
		if strings.HasSuffix(option, "\x00worker\x00B") {
			selectB = option
		}
	}
	if selectB == "" {
		t.Fatalf("observation options do not identify B: %#v", canonical.Choices[0].Options)
	}
	journal.Choices = []ChoiceDecision{{Point: canonical.Choices[0].Point, Selection: selectB}}
	replay, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	picked := replay.Poset.ByName("Picked")
	if len(picked) != 1 {
		t.Fatalf("scheduled observation produced %d selections", len(picked))
	}
	if value, _ := picked[0].Param("name"); value != "B" {
		t.Fatalf("scheduled observation selected %#v, want B", value)
	}
	second, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := replay.MarshalCanonical()
	right, _ := second.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("observation schedule replay was not byte-identical")
	}
}

func TestObservationScheduleNeverOffersEventBeforeItsCausalParents(t *testing.T) {
	architecture := NewArchitecture("observation-causality")
	component := NewComponent("worker", Interface("Worker").
		OutAction("A").OutAction("B").OutAction("C").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "a", Source: "worker", Action: "A"},
		InputEvent{Key: "b", Source: "worker", Action: "B", Causes: []string{"a"}},
		InputEvent{Key: "c", Source: "worker", Action: "C"},
	)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Choices) < 1 || result.Choices[0].Domain != "event-observation" {
		t.Fatalf("unexpected observation choices: %#v", result.Choices)
	}
	for _, option := range result.Choices[0].Options {
		if strings.HasSuffix(option, "\x00worker\x00B") {
			t.Fatal("causal child B was offered before parent A was observed")
		}
	}
	a := result.Poset.ByName("A")[0]
	b := result.Poset.ByName("B")[0]
	if !result.Poset.IsCausallyBefore(a.ID, b.ID) {
		t.Fatal("explicit input causality was lost")
	}
}

func TestExplorationEnumeratesObservationDependentComputations(t *testing.T) {
	architecture := observationChoiceArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "a", Source: "worker", Action: "A"},
		InputEvent{Key: "b", Source: "worker", Action: "B"},
	)
	result, err := architecture.ExploreDeterministic(journal,
		ExplorationLimits{MaxExecutions: 10, MaxChoiceDepth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || len(result.Computations) != 2 {
		t.Fatalf("observation exploration is incomplete: %#v", result)
	}
	seen := make(map[string]bool)
	for _, computation := range result.Computations {
		picked := computation.Result.Poset.ByName("Picked")
		if len(picked) != 1 {
			t.Fatalf("computation has %d Picked events", len(picked))
		}
		value, _ := picked[0].Param("name")
		seen[value.(string)] = true
	}
	if !seen["A"] || !seen["B"] {
		t.Fatalf("observation exploration missed a permitted result: %#v", seen)
	}
}
