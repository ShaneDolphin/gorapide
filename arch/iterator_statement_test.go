package arch

import (
	"bytes"
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func distinctIteratorEvents(events gorapide.EventSet) gorapide.EventSet {
	result := make(gorapide.EventSet, 0, len(events))
	seen := make(map[gorapide.EventID]bool, len(events))
	for _, event := range events {
		if !seen[event.ID] {
			seen[event.ID] = true
			result = append(result, event)
		}
	}
	return result
}

func finiteIteratorRuleArchitecture(t *testing.T, first, last RuleValue) *Architecture {
	t.Helper()
	architecture := NewArchitecture("finite-range-iterator")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Emit", P("value", "Integer")).Build(), nil)
	if err := component.AddDeclarativeRule(
		Rule("iterate").On(pattern.MatchEvent("Start")).Do(
			ForEachIntegerRange("I", first, last,
				CallAction("emit", "Emit", BindingParam("value", "I")),
			),
		).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestFiniteRangeForExecutesObservableIteratorProtocolAndReplays(t *testing.T) {
	architecture := finiteIteratorRuleArchitecture(t, LiteralValue(1), LiteralValue(3))
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 32},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if result.StatementSteps != 11 {
		t.Fatalf("statement steps=%d, want 11", result.StatementSteps)
	}
	emitted := result.Poset.ByName("Emit")
	moreCalls := distinctIteratorEvents(result.Poset.ByName("More'Call"))
	moreReturns := distinctIteratorEvents(result.Poset.ByName("More'Return"))
	itemCalls := distinctIteratorEvents(result.Poset.ByName("Item'Call"))
	itemReturns := distinctIteratorEvents(result.Poset.ByName("Item'Return"))
	if len(emitted) != 3 || len(moreCalls) != 4 || len(moreReturns) != 4 ||
		len(itemCalls) != 3 || len(itemReturns) != 3 {
		t.Fatalf("protocol counts emit/more-call/more-return/item-call/item-return=%d/%d/%d/%d/%d",
			len(emitted), len(moreCalls), len(moreReturns), len(itemCalls), len(itemReturns))
	}
	if len(result.Poset.ByName("More'Call")) != 2*len(moreCalls) ||
		len(result.Poset.ByName("More'Return")) != 2*len(moreReturns) ||
		len(result.Poset.ByName("Item'Call")) != 2*len(itemCalls) ||
		len(result.Poset.ByName("Item'Return")) != 2*len(itemReturns) {
		t.Fatal("iterator protocol did not retain both caller and provider observations")
	}
	values := make(map[int64]*gorapide.Event)
	for _, event := range emitted {
		value, ok := event.Param("value")
		if !ok {
			t.Fatal("Emit omitted iterator value")
		}
		values[value.(int64)] = event
	}
	if values[1] == nil || values[2] == nil || values[3] == nil {
		t.Fatalf("iterator values=%#v, want 1,2,3", values)
	}
	falseReturns := 0
	iteratorSource := ""
	for _, event := range moreReturns {
		value, ok := event.Param("Return")
		if !ok {
			t.Fatal("More'Return omitted Return")
		}
		if !value.(bool) {
			falseReturns++
		}
		if iteratorSource == "" {
			iteratorSource = event.Source
		} else if event.Source != iteratorSource {
			t.Fatalf("range expression allocated multiple iterators: %q and %q", iteratorSource, event.Source)
		}
	}
	if falseReturns != 1 || len(iteratorSource) < len("mod1-") || iteratorSource[:len("mod1-")] != "mod1-" {
		t.Fatalf("exhaustion/allocation false=%d source=%q", falseReturns, iteratorSource)
	}
	for _, event := range itemReturns {
		if event.Source != iteratorSource {
			t.Fatalf("Item used iterator %q, want %q", event.Source, iteratorSource)
		}
	}
	if !result.Poset.IsCausallyBefore(values[1].ID, values[2].ID) ||
		!result.Poset.IsCausallyBefore(values[2].ID, values[3].ID) {
		t.Fatal("successive iterator bodies lost source-process causality")
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
		t.Fatal("finite iterator replay was not byte-identical")
	}
}

func TestFiniteRangeForUsesEmptyIdentityAndExplicitCardinalityBound(t *testing.T) {
	empty := finiteIteratorRuleArchitecture(t, LiteralValue(3), LiteralValue(1))
	digest, err := empty.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := empty.ExecuteDeterministic(NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 10, MaxStatements: 8},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Emit")) != 0 || len(distinctIteratorEvents(result.Poset.ByName("More'Call"))) != 1 ||
		len(distinctIteratorEvents(result.Poset.ByName("Item'Call"))) != 0 || result.StatementSteps != 2 {
		t.Fatalf("empty iterator emitted body/protocol unexpectedly: poset=%d steps=%d", result.Poset.Len(), result.StatementSteps)
	}

	tooLarge := finiteIteratorRuleArchitecture(t, LiteralValue(0), LiteralValue(256))
	digest, err = tooLarge.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	_, err = tooLarge.ExecuteDeterministic(NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 10, MaxStatements: 1024},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if !errors.Is(err, ErrExecutionLimit) {
		t.Fatalf("expected explicit finite iterator bound, got %v", err)
	}
}

func TestFiniteRangeForEvaluatesRangeExpressionOnceBeforeBody(t *testing.T) {
	architecture := NewArchitecture("finite-iterator-evaluate-once")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Emit", P("value", "Integer")).Build(), nil)
	if err := component.DeclareState(
		StateReference("first", "Integer", 1),
		StateReference("last", "Integer", 2),
	); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeRule(
		Rule("iterate").On(pattern.MatchEvent("Start")).Do(
			ForEachIntegerRange("I", ReadState("first"), ReadState("last"),
				CallAction("emit", "Emit", BindingParam("value", "I")),
				SetState("last", LiteralValue(0)),
			),
		).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 10, MaxStatements: 32},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[int64]bool)
	for _, event := range result.Poset.ByName("Emit") {
		value, _ := event.Param("value")
		values[value.(int64)] = true
	}
	if !values[1] || !values[2] || len(result.Firings) != 1 ||
		len(result.Firings[0].StateReads) != 2 {
		t.Fatalf("range expression was reevaluated after body mutation: values=%#v reads=%#v",
			values, result.Firings[0].StateReads)
	}
}

func TestFiniteRangeForBindingSurvivesPauseAndLoopControl(t *testing.T) {
	architecture := NewArchitecture("resumable-finite-iterator")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Tick", P("value", "Integer")).OutAction("Done").Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	body := ForEachIntegerRange("I", LiteralValue(1), LiteralValue(4),
		NextWhen(EqualValues(BoundValue("I"), LiteralValue(2))),
		PauseFor("C", 1),
		CallAction("tick", "Tick", BindingParam("value", "I")),
		ExitWhen(EqualValues(BoundValue("I"), LiteralValue(3))),
	)
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("start").On(pattern.MatchEvent("Start")).Do(
			body, CallAction("done", "Done"),
		).Terminate().Build()),
	).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 64},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	ticks := result.Poset.ByName("Tick")
	moreCalls := distinctIteratorEvents(result.Poset.ByName("More'Call"))
	itemCalls := distinctIteratorEvents(result.Poset.ByName("Item'Call"))
	if len(ticks) != 2 || len(result.Poset.ByName("Done")) != 1 ||
		len(moreCalls) != 3 || len(itemCalls) != 3 {
		t.Fatalf("resumable iterator counts tick/done/more/item=%d/%d/%d/%d",
			len(ticks), len(result.Poset.ByName("Done")), len(moreCalls), len(itemCalls))
	}
	values := make(map[int64]bool)
	for _, tick := range ticks {
		value, _ := tick.Param("value")
		values[value.(int64)] = true
	}
	if !values[1] || !values[3] || values[2] || len(result.ClockAdvances) != 2 {
		t.Fatalf("pause/next/exit lost iterator state: values=%#v clocks=%#v", values, result.ClockAdvances)
	}
}

func TestFiniteRangeForEndpointsChangeModelIdentity(t *testing.T) {
	left := finiteIteratorRuleArchitecture(t, LiteralValue(1), LiteralValue(2))
	right := finiteIteratorRuleArchitecture(t, LiteralValue(1), LiteralValue(3))
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest == rightDigest {
		t.Fatal("finite iterator endpoint did not affect canonical model identity")
	}
}

func TestFiniteRangeForRejectsMalformedDeclarations(t *testing.T) {
	tests := []Statement{
		ForEachIntegerRange("I", LiteralValue(true), LiteralValue(2), NullStatement()),
		ForEachIntegerRange("I", LiteralValue(1), LiteralValue(false), NullStatement()),
	}
	for index, statement := range tests {
		architecture := NewArchitecture("invalid-finite-iterator")
		component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
		if err := component.AddDeclarativeRule(
			Rule("iterate").On(pattern.MatchEvent("Start")).Do(statement).Build(),
		); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		_, err := architecture.DeterministicModelDigest()
		if !errors.Is(err, ErrUnsupportedDeterministicModel) ||
			!errors.Is(err, ErrInvalidDeclarativeStatement) {
			t.Fatalf("case %d expected malformed iterator declaration, got %v", index, err)
		}
	}
}
