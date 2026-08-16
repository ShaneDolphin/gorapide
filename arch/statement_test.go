package arch

import (
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestOrdinaryStatementSequencePreservesStateAndEventOrder(t *testing.T) {
	architecture := NewArchitecture("statement-sequence")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input", P("n", "Integer")).
		OutAction("Before", P("value", "Integer")).
		OutAction("After", P("value", "Integer")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("value", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	trigger := pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))
	process := Process("p").StartAt("wait").States(
		AwaitState("wait", Await("run").On(trigger).Do(
			SetState("value", BoundValue("N")),
			CallAction("before", "Before", StateParam("value", "value")),
			SetState("value", AddValues(ReadState("value"), LiteralValue(1))),
			CallAction("after", "After", StateParam("value", "value")),
		).Terminate().Build()),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20,
		InputEvent{Key: "input", Source: "worker", Action: "Input", Params: map[string]any{"n": 5}},
	))
	if err != nil {
		t.Fatal(err)
	}
	before := result.Poset.ByName("Before")
	after := result.Poset.ByName("After")
	if len(before) != 1 || len(after) != 1 || !result.Poset.IsCausallyBefore(before[0].ID, after[0].ID) {
		t.Fatalf("sequential calls are missing or unordered: before=%#v after=%#v", before, after)
	}
	if value, _ := before[0].Param("value"); value != int64(5) {
		t.Fatalf("Before value=%#v, want 5", value)
	}
	if value, _ := after[0].Param("value"); value != int64(6) {
		t.Fatalf("After value=%#v, want 6", value)
	}
	if result.State[0].Version != 2 || result.State[0].Value.Text != "6" ||
		len(result.Firings) != 1 || len(result.Firings[0].StateWrites) != 2 ||
		len(result.Firings[0].Generated) != 2 {
		t.Fatalf("statement audit is incomplete: state=%#v firing=%#v", result.State, result.Firings)
	}
	operations := result.StateOperations
	if len(operations) != 6 {
		t.Fatalf("Ref operations=%#v, want creation plus five statement operations", operations)
	}
	// Version-1 dereferences share one value source and are canonically ordered
	// before the assignment that creates version 2.
	for index := range operations {
		if operations[index].Sequence != uint64(index+1) {
			t.Fatalf("Ref sequence[%d]=%#v", index, operations[index])
		}
		if index > 0 && operations[index].Predecessor != operations[index-1].ID {
			t.Fatalf("Ref predecessor[%d]=%#v", index, operations[index])
		}
	}
	if operations[0].Kind != StateOperationCreate || operations[1].Kind != StateOperationAssign ||
		operations[1].Version != 1 || operations[2].Kind != StateOperationDereference ||
		operations[3].Kind != StateOperationDereference || operations[2].ValueSource != operations[1].ID ||
		operations[3].ValueSource != operations[1].ID || operations[4].Kind != StateOperationAssign ||
		operations[4].Version != 2 || operations[5].Kind != StateOperationDereference ||
		operations[5].ValueSource != operations[4].ID {
		t.Fatalf("Ref operation content=%#v", operations)
	}
	augmented, err := result.AugmentedComputation()
	if err != nil {
		t.Fatal(err)
	}
	limits := ConsistentCutLimits{MaxCuts: 10, MaxOptionalOccurrences: 20}
	beforeCuts, err := augmented.ConsistentCutStateWitnesses([]string{string(before[0].ID)}, limits)
	if err != nil {
		t.Fatal(err)
	}
	afterCuts, err := augmented.ConsistentCutStateWitnesses([]string{string(after[0].ID)}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeCuts) != 1 || len(beforeCuts[0].State) != 1 || beforeCuts[0].State[0].Version != 1 ||
		len(afterCuts) != 1 || len(afterCuts[0].State) != 1 || afterCuts[0].State[0].Version != 2 {
		t.Fatalf("program-point cut states before=%#v after=%#v", beforeCuts, afterCuts)
	}
	input := result.Poset.ByName("Input")[0]
	if !result.Poset.IsCausallyBefore(input.ID, before[0].ID) || !result.Poset.IsCausallyBefore(input.ID, after[0].ID) {
		t.Fatal("ordinary statement calls lost trigger causality")
	}
}

func TestDoBlockRequiresNonemptyCanonicalBody(t *testing.T) {
	architecture := NewArchitecture("empty-do-block")
	component := NewComponent("worker", Interface("Worker").InAction("Input").Build(), nil)
	if err := component.AddDeclarativeRule(
		Rule("empty").On(pattern.MatchEvent("Input")).Do(DoBlock()).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if err == nil || !errors.Is(err, ErrInvalidDeclarativeStatement) ||
		!strings.Contains(err.Error(), "plain do block 0 is empty") {
		t.Fatalf("empty plain-do boundary=%v", err)
	}
}

func TestIfStatementExecutesOneBranchAndCapturesGenerationState(t *testing.T) {
	architecture := NewArchitecture("if-statement")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input", P("n", "Integer")).
		OutAction("Positive", P("n", "Integer")).
		OutAction("NonPositive", P("n", "Integer")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("last", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	trigger := pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))
	branch := IfThen(
		GreaterValues(BoundValue("N"), LiteralValue(0)),
		[]Statement{
			SetState("last", BoundValue("N")),
			CallAction("positive", "Positive", StateParam("n", "last")),
		},
		[]Statement{
			SetState("last", NegateValue(BoundValue("N"))),
			CallAction("non-positive", "NonPositive", StateParam("n", "last")),
		},
	)
	if err := component.AddDeclarativeRule(Rule("classify").On(trigger).Do(branch).Build()); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "worker", Action: "Input", Params: map[string]any{"n": -3}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Positive")) != 0 || len(result.Poset.ByName("NonPositive")) != 1 {
		t.Fatal("if statement executed the wrong branch")
	}
	if value, _ := result.Poset.ByName("NonPositive")[0].Param("n"); value != int64(3) {
		t.Fatalf("else branch value=%#v, want 3", value)
	}
	if len(result.Firings[0].StateWrites) != 1 || result.Firings[0].Generated[0].OutputID != "non-positive" {
		t.Fatalf("selected branch audit is incomplete: %#v", result.Firings[0])
	}
}

func TestStatementEventCapturesStateAtItsOwnGenerationPoint(t *testing.T) {
	architecture := NewArchitecture("statement-snapshot")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Check").OutAction("Accepted").Build(), nil)
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
		AwaitState("start", Await("produce").On(pattern.MatchEvent("Start")).Do(
			SetState("enabled", LiteralValue(true)),
			CallAction("check", "Check"),
			SetState("enabled", LiteralValue(false)),
		).Terminate().Build()),
	).Build()
	observer := Process("observer").StartAt("check").States(
		AwaitState("check", Await("observe").On(pattern.MatchEvent("Check")).
			Where(ReadState("enabled")).Emit("Accepted").Terminate().Build()),
	).Build()
	for _, process := range []*DeclarativeProcess{producer, observer} {
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
	if len(result.Poset.ByName("Accepted")) != 1 || result.State[0].Value.Bool {
		t.Fatalf("event did not capture state at its statement position: state=%#v firings=%#v", result.State, result.Firings)
	}
}

func TestOrdinaryStatementsAreCanonicalAndInvalidFormsFailExplicitly(t *testing.T) {
	makeDigest := func(statements ...Statement) string {
		architecture := NewArchitecture("statement-identity")
		component := NewComponent("worker", Interface("Worker").
			OutAction("Input").OutAction("One").OutAction("Two").Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		if err := component.AddDeclarativeRule(
			Rule("r").On(pattern.MatchEvent("Input")).Do(statements...).Build(),
		); err != nil {
			t.Fatal(err)
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	forward := makeDigest(CallAction("one", "One"), CallAction("two", "Two"))
	reverse := makeDigest(CallAction("two", "Two"), CallAction("one", "One"))
	if forward == reverse {
		t.Fatal("ordinary statement order did not affect canonical model identity")
	}

	invalidArchitecture := NewArchitecture("invalid-statement")
	invalidComponent := NewComponent("worker", Interface("Worker").OutAction("Input").Build(), nil)
	if err := invalidArchitecture.AddComponent(invalidComponent); err != nil {
		t.Fatal(err)
	}
	invalid := Statement{kind: StatementKind("host-callback")}
	if err := invalidComponent.AddDeclarativeRule(
		Rule("bad").On(pattern.MatchEvent("Input")).Do(invalid).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := invalidArchitecture.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidDeclarativeStatement) {
		t.Fatalf("expected explicit invalid statement error, got %v", err)
	}
}
