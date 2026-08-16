package arch

import (
	"bytes"
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestFalseAssertionGeneratesPredefinedInconsistentEventInProcessOrder(t *testing.T) {
	architecture := NewArchitecture("assert-false")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("After").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("balance", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeRule(
		Rule("check").On(pattern.MatchEvent("Start")).Do(
			AssertThat(GreaterValues(ReadState("balance"), LiteralValue(0))),
			CallAction("after", "After"),
		).Build(),
	); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10, InputEvent{Key: "start", Source: "worker", Action: "Start"})
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	start := gorapide.EventSet{onlySourceNamedEvent(t, result.Poset, "worker", "Start")}
	inconsistent := result.Poset.ByName("Inconsistent")
	after := result.Poset.ByName("After")
	if len(inconsistent) != 1 || len(after) != 1 {
		t.Fatalf("assertion output is incomplete: start=%d inconsistent=%d after=%d", len(start), len(inconsistent), len(after))
	}
	if len(inconsistent[0].Params) != 0 {
		t.Fatalf("unlabeled Inconsistent event has parameters: %#v", inconsistent[0].Params)
	}
	if !result.Poset.IsCausallyBefore(start[0].ID, inconsistent[0].ID) ||
		!result.Poset.IsCausallyBefore(inconsistent[0].ID, after[0].ID) {
		t.Fatal("assertion event did not retain sequential process causality")
	}
	if result.StatementSteps != 2 || len(result.Firings) != 1 ||
		len(result.Firings[0].StateReads) != 1 || len(result.Firings[0].Generated) != 2 ||
		result.Firings[0].Generated[0].OutputID != "assert@0" {
		t.Fatalf("assertion audit is incomplete: steps=%d firing=%#v", result.StatementSteps, result.Firings)
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
		t.Fatal("assertion replay was not byte-identical")
	}
}

func TestTrueAssertionGeneratesNoEventButAuditsItsStateRead(t *testing.T) {
	architecture := NewArchitecture("assert-true")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("After").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("healthy", "Boolean", true)); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeRule(
		Rule("check").On(pattern.MatchEvent("Start")).Do(
			AssertThat(ReadState("healthy")),
			CallAction("after", "After"),
		).Build(),
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
	if len(result.Poset.ByName("Inconsistent")) != 0 || len(result.Poset.ByName("After")) != 1 {
		t.Fatal("true assertion generated an event or stopped subsequent execution")
	}
	if len(result.Firings[0].StateReads) != 1 || len(result.Firings[0].Generated) != 1 {
		t.Fatalf("true assertion audit is incomplete: %#v", result.Firings[0])
	}
}

func TestRepeatedFalseAssertionsHaveDistinctOrderedOccurrences(t *testing.T) {
	architecture := NewArchitecture("assert-loop")
	component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("count", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	loop := LoopDo(
		SetState("count", AddValues(ReadState("count"), LiteralValue(1))),
		AssertThat(LiteralValue(false)),
		ExitWhen(GreaterOrEqualValues(ReadState("count"), LiteralValue(2))),
	)
	if err := component.AddDeclarativeRule(
		Rule("check").On(pattern.MatchEvent("Start")).Do(loop).Build(),
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
	events := result.Poset.ByName("Inconsistent")
	if len(events) != 2 || events[0].ID == events[1].ID || result.StatementSteps != 7 {
		t.Fatalf("repeated assertions are incomplete: events=%#v steps=%d", events, result.StatementSteps)
	}
	if !result.Poset.IsCausallyBefore(events[0].ID, events[1].ID) &&
		!result.Poset.IsCausallyBefore(events[1].ID, events[0].ID) {
		t.Fatal("repeated assertion events are not process ordered")
	}
}

func TestAssertionMustBeBooleanAndChangesCanonicalIdentity(t *testing.T) {
	makeDigest := func(condition RuleValue) (string, error) {
		architecture := NewArchitecture("assert-identity")
		component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		if err := component.AddDeclarativeRule(
			Rule("check").On(pattern.MatchEvent("Start")).Do(AssertThat(condition)).Build(),
		); err != nil {
			t.Fatal(err)
		}
		return architecture.DeterministicModelDigest()
	}
	trueDigest, err := makeDigest(LiteralValue(true))
	if err != nil {
		t.Fatal(err)
	}
	falseDigest, err := makeDigest(LiteralValue(false))
	if err != nil {
		t.Fatal(err)
	}
	if trueDigest == falseDigest {
		t.Fatal("assertion condition did not affect canonical model identity")
	}
	if _, err := makeDigest(LiteralValue(1)); !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidDeclarativeStatement) {
		t.Fatalf("non-Boolean assertion was not rejected explicitly: %v", err)
	}
}
