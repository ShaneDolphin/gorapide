package arch

import (
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func executeCaseModel(t *testing.T, mode CaseMode, selector int) *ExecutionResult {
	t.Helper()
	architecture := NewArchitecture("case-" + string(mode))
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("A").OutAction("B").OutAction("Default").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("selector", "Integer", selector)); err != nil {
		t.Fatal(err)
	}
	statement := CaseOfDefault(
		ReadState("selector"), mode, []Statement{CallAction("default", "Default")},
		CaseWhen(LiteralValue(3), CallAction("a", "A")),
		CaseWhenAny([]RuleValue{LiteralValue(2), LiteralValue(3)}, CallAction("b", "B")),
	)
	if err := component.AddDeclarativeRule(
		Rule("choose").On(pattern.MatchEvent("Start")).Do(statement).Build(),
	); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCaseOrExecutesEveryEligibleAlternativeInSourceOrder(t *testing.T) {
	result := executeCaseModel(t, CaseOrMode, 3)
	a := result.Poset.ByName("A")
	b := result.Poset.ByName("B")
	if len(a) != 1 || len(b) != 1 || len(result.Poset.ByName("Default")) != 0 {
		t.Fatalf("or case selected the wrong alternatives: A=%d B=%d default=%d", len(a), len(b), len(result.Poset.ByName("Default")))
	}
	if !result.Poset.IsCausallyBefore(a[0].ID, b[0].ID) {
		t.Fatal("or case alternatives did not execute in source order")
	}
	if result.StatementSteps != 3 || len(result.Firings[0].StateReads) != 1 {
		t.Fatalf("case expression was not evaluated exactly once: steps=%d reads=%#v", result.StatementSteps, result.Firings[0].StateReads)
	}
}

func TestCaseElseExecutesOnlyFirstEligibleAlternative(t *testing.T) {
	result := executeCaseModel(t, CaseElseMode, 3)
	if len(result.Poset.ByName("A")) != 1 || len(result.Poset.ByName("B")) != 0 || len(result.Poset.ByName("Default")) != 0 {
		t.Fatal("else case did not stop after its first eligible alternative")
	}
	if result.StatementSteps != 2 || len(result.Firings[0].StateReads) != 1 {
		t.Fatalf("unexpected else-case audit: steps=%d reads=%#v", result.StatementSteps, result.Firings[0].StateReads)
	}
}

func TestCaseXorAndDefaultSelectExactlyOneBody(t *testing.T) {
	selected := executeCaseModel(t, CaseXorMode, 2)
	if len(selected.Poset.ByName("A")) != 0 || len(selected.Poset.ByName("B")) != 1 || len(selected.Poset.ByName("Default")) != 0 {
		t.Fatal("xor case did not select its one eligible alternative")
	}
	defaulted := executeCaseModel(t, CaseXorMode, 9)
	if len(defaulted.Poset.ByName("A")) != 0 || len(defaulted.Poset.ByName("B")) != 0 || len(defaulted.Poset.ByName("Default")) != 1 {
		t.Fatal("case default did not execute when no alternative was eligible")
	}
}

func TestCaseXorConflictFailsDeterministically(t *testing.T) {
	architecture := NewArchitecture("case-xor-conflict")
	component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	statement := CaseOf(LiteralValue(1), CaseXorMode,
		CaseWhen(LiteralValue(1), NullStatement()),
		CaseWhen(LiteralValue(1), NullStatement()),
	)
	if err := component.AddDeclarativeRule(
		Rule("choose").On(pattern.MatchEvent("Start")).Do(statement).Build(),
	); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10, InputEvent{Key: "start", Source: "worker", Action: "Start"})
	for run := 0; run < 2; run++ {
		if _, err := architecture.ExecuteDeterministic(journal); !errors.Is(err, ErrCaseChoiceConflict) {
			t.Fatalf("run %d expected xor conflict, got %v", run, err)
		}
	}
}

func TestCasePropagatesExitAndNextToTheNearestLoop(t *testing.T) {
	architecture := NewArchitecture("case-loop-control")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Tick", P("count", "Integer")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("count", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	loop := LoopDo(
		SetState("count", AddValues(ReadState("count"), LiteralValue(1))),
		CaseOf(ReadState("count"), CaseElseMode,
			CaseWhen(LiteralValue(3), ExitLoop()),
			CaseWhen(LiteralValue(2), NextLoop()),
		),
		CallAction("tick", "Tick", StateParam("count", "count")),
	)
	if err := component.AddDeclarativeRule(
		Rule("loop").On(pattern.MatchEvent("Start")).Do(loop).Build(),
	); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 10, MaxStatements: 20},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	ticks := result.Poset.ByName("Tick")
	if len(ticks) != 1 || result.State[0].Value.Text != "3" || result.StatementSteps != 10 {
		t.Fatalf("case loop control is incomplete: ticks=%d state=%#v steps=%d", len(ticks), result.State, result.StatementSteps)
	}
	if value, _ := ticks[0].Param("count"); value != int64(1) {
		t.Fatalf("unexpected tick value %#v", value)
	}
}

func TestCaseValidationAndCanonicalIdentity(t *testing.T) {
	makeDigest := func(mode CaseMode, alternatives ...CaseAlternative) (string, error) {
		architecture := NewArchitecture("case-identity")
		component := NewComponent("worker", Interface("Worker").OutAction("Input", P("n", "Integer")).Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		trigger := pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))
		if err := component.AddDeclarativeRule(
			Rule("choose").On(trigger).Do(CaseOf(BoundValue("N"), mode, alternatives...)).Build(),
		); err != nil {
			t.Fatal(err)
		}
		return architecture.DeterministicModelDigest()
	}
	forward, err := makeDigest(CaseOrMode,
		CaseWhen(LiteralValue(1), NullStatement()),
		CaseWhen(LiteralValue(2), NullStatement()),
	)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := makeDigest(CaseOrMode,
		CaseWhen(LiteralValue(2), NullStatement()),
		CaseWhen(LiteralValue(1), NullStatement()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if forward == reverse {
		t.Fatal("case alternative order did not affect canonical model identity")
	}

	invalidCases := []Statement{
		CaseOf(LiteralValue(1), CaseMode("random"), CaseWhen(LiteralValue(1), NullStatement())),
		CaseOf(LiteralValue(1), CaseXorMode),
		CaseOf(LiteralValue(1), CaseXorMode, CaseAlternative{}),
		CaseOf(BoundValue("N"), CaseXorMode, CaseWhen(LiteralValue("one"), NullStatement())),
	}
	for index, statement := range invalidCases {
		_, err := makeDigest(CaseXorMode, CaseWhen(LiteralValue(index), statement))
		if !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidDeclarativeStatement) {
			t.Fatalf("invalid case %d was not rejected explicitly: %v", index, err)
		}
	}
}

func TestCaseIntegerRangeIsInclusiveAndDescendingRangeIsEmpty(t *testing.T) {
	run := func(selector, first, last int) bool {
		t.Helper()
		architecture := NewArchitecture("case-range")
		component := NewComponent("worker", Interface("Worker").
			OutAction("Start").OutAction("Range").OutAction("Default").Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		statement := CaseOfDefault(
			LiteralValue(selector), CaseXorMode, []Statement{CallAction("default", "Default")},
			CaseWhenRange(LiteralValue(first), LiteralValue(last), CallAction("range", "Range")),
		)
		if err := component.AddDeclarativeRule(
			Rule("choose").On(pattern.MatchEvent("Start")).Do(statement).Build(),
		); err != nil {
			t.Fatal(err)
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
			InputEvent{Key: "start", Source: "worker", Action: "Start"},
		))
		if err != nil {
			t.Fatal(err)
		}
		return len(result.Poset.ByName("Range")) == 1
	}
	for _, selector := range []int{2, 3, 4} {
		if !run(selector, 2, 4) {
			t.Fatalf("inclusive range 2..4 did not contain %d", selector)
		}
	}
	for _, selector := range []int{1, 5} {
		if run(selector, 2, 4) {
			t.Fatalf("inclusive range 2..4 incorrectly contained %d", selector)
		}
	}
	if run(3, 4, 2) {
		t.Fatal("descending range 4..2 was not empty")
	}
}

func TestCaseRangeValidationAndEndpointsAreCanonical(t *testing.T) {
	makeDigest := func(statement Statement) (string, error) {
		architecture := NewArchitecture("case-range-identity")
		component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		if err := component.AddDeclarativeRule(
			Rule("choose").On(pattern.MatchEvent("Start")).Do(statement).Build(),
		); err != nil {
			t.Fatal(err)
		}
		return architecture.DeterministicModelDigest()
	}
	first, err := makeDigest(CaseOf(LiteralValue(3), CaseXorMode,
		CaseWhenRange(LiteralValue(1), LiteralValue(3), NullStatement())))
	if err != nil {
		t.Fatal(err)
	}
	second, err := makeDigest(CaseOf(LiteralValue(3), CaseXorMode,
		CaseWhenRange(LiteralValue(1), LiteralValue(4), NullStatement())))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("case range endpoint did not affect canonical model identity")
	}
	invalid := []Statement{
		CaseOf(LiteralValue("three"), CaseXorMode,
			CaseWhenRange(LiteralValue(1), LiteralValue(3), NullStatement())),
		CaseOf(LiteralValue(3), CaseXorMode,
			CaseWhenRange(LiteralValue("one"), LiteralValue(3), NullStatement())),
		CaseOf(LiteralValue(3), CaseXorMode,
			CaseWhenChoices([]CaseChoice{{kind: "type"}}, NullStatement())),
	}
	for index, statement := range invalid {
		if _, err := makeDigest(statement); !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidDeclarativeStatement) {
			t.Fatalf("invalid range case %d was not rejected explicitly: %v", index, err)
		}
	}
}
