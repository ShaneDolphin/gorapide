package arch

import (
	"bytes"
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestRuleConsumptionIsPerRuleAndAtomic(t *testing.T) {
	state := NewRuleConsumption()
	first := &gorapide.Event{ID: "event-a"}
	second := &gorapide.Event{ID: "event-b"}
	if err := state.Consume("rule-a", gorapide.EventSet{first}); err != nil {
		t.Fatal(err)
	}
	if !state.IsConsumed("rule-a", first.ID) || state.IsConsumed("rule-b", first.ID) {
		t.Fatal("consumption was not scoped to one rule")
	}
	if err := state.Consume("rule-b", gorapide.EventSet{first}); err != nil {
		t.Fatalf("another rule could not consume the same event: %v", err)
	}
	err := state.Consume("rule-a", gorapide.EventSet{first, second})
	if !errors.Is(err, ErrEventConsumedByRule) {
		t.Fatalf("expected ErrEventConsumedByRule, got %v", err)
	}
	if state.IsConsumed("rule-a", second.ID) {
		t.Fatal("failed consumption partially committed a new event")
	}
	available, err := state.Available("rule-a", gorapide.EventSet{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if len(available) != 1 || available[0].ID != second.ID {
		t.Fatalf("available events = %v", available.IDs())
	}
}

func TestRuleConsumptionCanonicalStateIgnoresUpdateOrder(t *testing.T) {
	first := NewRuleConsumption()
	second := NewRuleConsumption()
	eventA := &gorapide.Event{ID: "event-a"}
	eventB := &gorapide.Event{ID: "event-b"}
	if err := first.Consume("rule-b", gorapide.EventSet{eventB}); err != nil {
		t.Fatal(err)
	}
	if err := first.Consume("rule-a", gorapide.EventSet{eventA, eventB}); err != nil {
		t.Fatal(err)
	}
	if err := second.Consume("rule-a", gorapide.EventSet{eventB, eventA}); err != nil {
		t.Fatal(err)
	}
	if err := second.Consume("rule-b", gorapide.EventSet{eventB}); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("canonical states differ:\n%s\n%s", firstBytes, secondBytes)
	}
	restored, err := ParseRuleConsumption(firstBytes)
	if err != nil {
		t.Fatal(err)
	}
	restoredBytes, err := restored.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, restoredBytes) {
		t.Fatal("canonical consumption round trip changed bytes")
	}
}

func TestBehaviorRuleCannotReuseEventInDifferentMatch(t *testing.T) {
	poset := gorapide.NewPoset()
	a := gorapide.NewEvent("A", "component", nil)
	if err := poset.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	b := gorapide.NewEvent("B", "component", nil)
	if err := poset.AddEventWithCause(b, a.ID); err != nil {
		t.Fatal(err)
	}
	component := NewComponent("component", Interface("I").InAction("A").InAction("B").Build(), poset)
	firings := 0
	component.OnPattern("one-rule",
		pattern.Or(pattern.MatchEvent("A"), pattern.Seq(pattern.MatchEvent("A"), pattern.MatchEvent("B"))),
		func(BehaviorContext) { firings++ },
	)
	component.observe(a)
	component.observe(b)
	if firings != 1 {
		t.Fatalf("same rule reused A in a second match: %d firings", firings)
	}
}

func TestSameEventRemainsAvailableToDifferentRules(t *testing.T) {
	poset := gorapide.NewPoset()
	event := gorapide.NewEvent("A", "component", nil)
	if err := poset.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	component := NewComponent("component", Interface("I").InAction("A").Build(), poset)
	firstFirings := 0
	secondFirings := 0
	component.OnPattern("first-rule", pattern.MatchEvent("A"), func(BehaviorContext) { firstFirings++ })
	component.OnPattern("second-rule", pattern.MatchEvent("A"), func(BehaviorContext) { secondFirings++ })
	component.observe(event)
	if firstFirings != 1 || secondFirings != 1 {
		t.Fatalf("firings = %d, %d; want 1, 1", firstFirings, secondFirings)
	}
}
