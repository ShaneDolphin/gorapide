package arch

import (
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestRuleGuardReadsStateAtLastMatchEventGeneration(t *testing.T) {
	architecture := NewArchitecture("historical-rule-guard")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input").OutAction("Accepted").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("enabled", "Boolean", true)); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeRule(
		Rule("a-reset").On(pattern.MatchEvent("Input")).
			Assign(AssignState("enabled", LiteralValue(false))).NoEvents().Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeRule(
		Rule("z-observe").On(pattern.MatchEvent("Input")).
			Where(EqualValues(ReadState("enabled"), LiteralValue(true))).
			Emit("Accepted").Build(),
	); err != nil {
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
	if len(result.Poset.ByName("Accepted")) != 1 || len(result.Firings) != 2 ||
		result.Firings[0].RuleID != "a-reset" || result.State[0].Value.Bool {
		t.Fatalf("historical guard execution is incomplete: firings=%#v state=%#v", result.Firings, result.State)
	}
	observer := result.Firings[1]
	if observer.RuleID != "z-observe" || len(observer.StateReads) != 1 ||
		observer.StateReads[0].Name != "enabled" || observer.StateReads[0].Version != 0 ||
		!observer.StateReads[0].Value.Bool {
		t.Fatalf("guard read current state instead of generation-time version: %#v", observer.StateReads)
	}
	operations := result.StateOperations
	if len(operations) != 4 || operations[0].Kind != StateOperationCreate ||
		operations[1].Kind != StateOperationDereference || operations[1].Version != 0 ||
		operations[1].ValueSource != operations[0].ID ||
		operations[2].Kind != StateOperationDereference || operations[2].Version != 0 ||
		operations[2].ValueSource != operations[0].ID ||
		operations[3].Kind != StateOperationAssign || operations[3].Version != 1 ||
		operations[1].Predecessor != operations[0].ID ||
		operations[2].Predecessor != operations[1].ID ||
		operations[3].Predecessor != operations[2].ID ||
		observer.StateReads[0].OperationID != operations[2].ID ||
		result.Firings[0].StateWrites[0].OperationID != operations[3].ID {
		t.Fatalf("historical Ref semantic order=%#v firings=%#v", operations, result.Firings)
	}
}

func TestAwaitGuardUsesGeneratedEventStateSnapshotAfterLaterMutation(t *testing.T) {
	architecture := NewArchitecture("historical-await-guard")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Reset").OutAction("Check").OutAction("Accepted").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.SetModuleProcessMode(ParallelProcesses); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("enabled", "Boolean", false)); err != nil {
		t.Fatal(err)
	}
	producer := Process("producer").StartAt("start").States(
		AwaitState("start", Await("produce").On(pattern.MatchEvent("Start")).
			Assign(AssignState("enabled", LiteralValue(true))).
			Generate(
				RuleEvent("reset", "Reset"),
				RuleEvent("check", "Check").After("reset"),
			).Terminate().Build()),
	).Build()
	resetter := Process("resetter").StartAt("reset").States(
		AwaitState("reset", Await("clear").On(pattern.MatchEvent("Reset")).
			Assign(AssignState("enabled", LiteralValue(false))).NoEvents().Terminate().Build()),
	).Build()
	observer := Process("observer").StartAt("check").States(
		AwaitState("check", Await("observe").On(pattern.MatchEvent("Check")).
			Where(ReadState("enabled")).Emit("Accepted").Terminate().Build()),
	).Build()
	for _, process := range []*DeclarativeProcess{producer, resetter, observer} {
		if err := component.AddDeclarativeProcess(process); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Accepted")) != 1 || result.State[0].Version != 2 || result.State[0].Value.Bool {
		t.Fatalf("await guard did not use generated-event snapshot: state=%#v firings=%#v", result.State, result.Firings)
	}
	foundHistoricalRead := false
	for _, firing := range result.Firings {
		if firing.ProcessID != "observer" {
			continue
		}
		if len(firing.StateReads) == 1 && firing.StateReads[0].Version == 1 && firing.StateReads[0].Value.Bool {
			foundHistoricalRead = true
		}
	}
	if !foundHistoricalRead {
		t.Fatalf("observer guard did not audit historical version 1=true: %#v", result.Firings)
	}
}

func TestFalseGuardDoesNotFireOrConsumeAndGuardMustBeBoolean(t *testing.T) {
	architecture := NewArchitecture("false-guard")
	component := NewComponent("worker", Interface("Worker").OutAction("Input").OutAction("Output").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("enabled", "Boolean", false)); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("a").On(pattern.MatchEvent("Input")).
			Where(ReadState("enabled")).Emit("Output").Terminate().Build()),
	).Build()); err != nil {
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
	if len(result.Poset.ByName("Output")) != 0 || len(result.Firings) != 0 ||
		len(result.Consumption.Rules) != 0 || result.Processes[0].State != "wait" {
		t.Fatalf("false guard fired or consumed its match: %#v", result)
	}

	invalid := NewArchitecture("invalid-guard")
	invalidComponent := NewComponent("worker", Interface("Worker").OutAction("Input").Build(), nil)
	if err := invalid.AddComponent(invalidComponent); err != nil {
		t.Fatal(err)
	}
	if err := invalidComponent.AddDeclarativeRule(
		Rule("bad").On(pattern.MatchEvent("Input")).Where(LiteralValue(1)).NoEvents().Build(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := invalid.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidDeclarativeRule) {
		t.Fatalf("expected non-Boolean guard error, got %v", err)
	}
}

func TestGuardExpressionChangesCanonicalModelIdentity(t *testing.T) {
	makeDigest := func(expected bool) string {
		architecture := NewArchitecture("guard-identity")
		component := NewComponent("worker", Interface("Worker").OutAction("Input").Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		if err := component.AddDeclarativeRule(
			Rule("guarded").On(pattern.MatchEvent("Input")).Where(LiteralValue(expected)).NoEvents().Build(),
		); err != nil {
			t.Fatal(err)
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	if makeDigest(true) == makeDigest(false) {
		t.Fatal("guard content did not affect canonical model identity")
	}
}
