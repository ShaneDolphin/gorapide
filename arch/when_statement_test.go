package arch

import (
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestWhenStatePreservesAndRepeatsOrdinaryStatementBody(t *testing.T) {
	architecture := NewArchitecture("when-statements")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input").OutAction("Count", P("value", "Integer")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("count", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	body := StatementBody(
		SetState("count", AddValues(ReadState("count"), LiteralValue(1))),
		CallAction("count", "Count", StateParam("value", "count")),
	)
	process := Process("counter").StartAt("watch").States(
		WhenState("watch", pattern.MatchEvent("Input"), body),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20,
		InputEvent{Key: "one", Source: "worker", Action: "Input"},
		InputEvent{Key: "two", Source: "worker", Action: "Input", Causes: []string{"one"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	counts := result.Poset.ByName("Count")
	if len(counts) != 2 || result.State[0].Version != 2 || result.State[0].Value.Text != "2" || result.StatementSteps != 4 {
		t.Fatalf("when body was not preserved and repeated: counts=%d state=%#v steps=%d", len(counts), result.State, result.StatementSteps)
	}
	values := make(map[int64]bool)
	for _, event := range counts {
		value, _ := event.Param("value")
		values[value.(int64)] = true
	}
	if !values[1] || !values[2] {
		t.Fatalf("when body emitted unexpected values: %#v", values)
	}
	if !result.Poset.IsCausallyBefore(counts[0].ID, counts[1].ID) &&
		!result.Poset.IsCausallyBefore(counts[1].ID, counts[0].ID) {
		t.Fatal("repeated when activations are not process ordered")
	}
	if result.Processes[0].Terminated || result.Processes[0].State != "watch" {
		t.Fatalf("when did not remain suspended for another match: %#v", result.Processes[0])
	}
}

func TestWhenStatePreservesRestrictedAssignmentsAndOutputs(t *testing.T) {
	architecture := NewArchitecture("when-restricted-body")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input").OutAction("Count", P("value", "Integer")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("count", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	body := &RuleBody{
		Assignments: []StateAssignment{AssignState("count", AddValues(ReadState("count"), LiteralValue(1)))},
		Outputs:     []RuleOutput{RuleEvent("count", "Count", StateParam("value", "count"))},
	}
	process := Process("counter").StartAt("watch").States(
		WhenState("watch", pattern.MatchEvent("Input"), body),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
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
	counts := result.Poset.ByName("Count")
	if len(counts) != 1 || result.State[0].Value.Text != "1" || len(result.Firings[0].StateWrites) != 1 {
		t.Fatalf("when restricted body was discarded: counts=%d state=%#v firing=%#v", len(counts), result.State, result.Firings[0])
	}
}
