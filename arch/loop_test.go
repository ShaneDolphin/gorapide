package arch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestDoExitAndNextExecuteDeterministicallyWithinStatementBudget(t *testing.T) {
	architecture := NewArchitecture("do-exit-next")
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
		NextWhen(EqualValues(ReadState("count"), LiteralValue(2))),
		CallAction("tick", "Tick", StateParam("count", "count")),
		ExitWhen(GreaterOrEqualValues(ReadState("count"), LiteralValue(3))),
	)
	process := Process("counter").StartAt("start").States(
		AwaitState("start", Await("run").On(pattern.MatchEvent("Start")).Do(loop).Terminate().Build()),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 10, MaxStatements: 20},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	ticks := result.Poset.ByName("Tick")
	if len(ticks) != 2 || result.StatementSteps != 11 || result.State[0].Value.Text != "3" {
		t.Fatalf("loop execution is incomplete: ticks=%d steps=%d state=%#v", len(ticks), result.StatementSteps, result.State)
	}
	values := make(map[int64]bool)
	for _, tick := range ticks {
		value, _ := tick.Param("count")
		values[value.(int64)] = true
	}
	if !values[1] || !values[3] || values[2] {
		t.Fatalf("next did not skip iteration 2: %#v", values)
	}
	if !result.Poset.IsCausallyBefore(ticks[0].ID, ticks[1].ID) &&
		!result.Poset.IsCausallyBefore(ticks[1].ID, ticks[0].ID) {
		t.Fatal("events from separate loop iterations are not process ordered")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replay, err := architecture.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replay.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("loop replay was not byte-identical")
	}
}

func TestNamedDoTargetsAreCanonicalModelContentAndValidated(t *testing.T) {
	digestFor := func(label, target string) (string, error) {
		architecture := NewArchitecture("named-do-identity")
		component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		loop := NameDo(label, LoopDo(ExitNamed(target)))
		if err := component.AddDeclarativeRule(
			Rule("loop").On(pattern.MatchEvent("Start")).Do(loop).Build(),
		); err != nil {
			t.Fatal(err)
		}
		return architecture.DeterministicModelDigest()
	}
	lower, err := digestFor("outer", "outer")
	if err != nil {
		t.Fatal(err)
	}
	upper, err := digestFor("OUTER", "Outer")
	if err != nil {
		t.Fatal(err)
	}
	if lower != upper {
		t.Fatalf("do-name case changed canonical model: %s != %s", lower, upper)
	}
	other, err := digestFor("different", "different")
	if err != nil {
		t.Fatal(err)
	}
	if lower == other {
		t.Fatal("a semantically different do name did not change canonical model identity")
	}
	if _, err := digestFor("outer", "missing"); !errors.Is(err, ErrInvalidDeclarativeStatement) ||
		!strings.Contains(err.Error(), "names non-enclosing do") {
		t.Fatalf("non-enclosing target error=%v", err)
	}
	if _, err := digestFor("bad label", "bad label"); !errors.Is(err, ErrInvalidDeclarativeStatement) {
		t.Fatalf("invalid label error=%v", err)
	}

	architecture := NewArchitecture("duplicate-do-label")
	component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	duplicate := NameDo("outer", LoopDo(NameDo("OUTER", LoopDo(ExitLoop()))))
	if err := component.AddDeclarativeRule(
		Rule("loop").On(pattern.MatchEvent("Start")).Do(duplicate).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrInvalidDeclarativeStatement) ||
		!strings.Contains(err.Error(), "overloads do label") {
		t.Fatalf("duplicate label error=%v", err)
	}
}

func TestStatementBudgetFailsInfiniteLoopExplicitly(t *testing.T) {
	architecture := NewArchitecture("bounded-loop")
	component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeRule(
		Rule("loop").On(pattern.MatchEvent("Start")).Do(LoopDo(NullStatement())).Build(),
	); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 10, MaxStatements: 5},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	for run := 0; run < 2; run++ {
		if _, err := architecture.ExecuteDeterministic(journal); !errors.Is(err, ErrExecutionLimit) {
			t.Fatalf("run %d expected explicit statement limit, got %v", run, err)
		}
	}
}

func TestExitAndNextOutsideDoAndZeroStatementLimitFailValidation(t *testing.T) {
	for _, statement := range []Statement{ExitLoop(), NextLoop()} {
		architecture := NewArchitecture("invalid-loop-control")
		component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		if err := component.AddDeclarativeRule(
			Rule("bad").On(pattern.MatchEvent("Start")).Do(statement).Build(),
		); err != nil {
			t.Fatal(err)
		}
		if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidDeclarativeStatement) {
			t.Fatalf("expected loop-control validation error, got %v", err)
		}
	}

	architecture := NewArchitecture("invalid-limit")
	component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 1, MaxStatements: 0},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	if _, err := architecture.ExecuteDeterministic(journal); !errors.Is(err, ErrInvalidExecutionJournal) {
		t.Fatalf("expected zero statement limit rejection, got %v", err)
	}
}

func TestLoopBodyChangesModelIdentity(t *testing.T) {
	makeDigest := func(limit int) string {
		architecture := NewArchitecture("loop-identity")
		component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		if err := component.DeclareState(StateReference("count", "Integer", 0)); err != nil {
			t.Fatal(err)
		}
		loop := LoopDo(
			SetState("count", AddValues(ReadState("count"), LiteralValue(1))),
			ExitWhen(GreaterOrEqualValues(ReadState("count"), LiteralValue(limit))),
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
		return digest
	}
	if makeDigest(2) == makeDigest(3) {
		t.Fatal("loop condition did not affect canonical model identity")
	}
}
