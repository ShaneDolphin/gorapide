package arch

import (
	"bytes"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func addConformanceTrigger(t *testing.T, p *gorapide.Poset, occurrence string) *gorapide.Event {
	t.Helper()
	event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile:    "stanford-rapide-1.0",
		Model:      "connection-conformance",
		Instance:   "A",
		Action:     "Input",
		Occurrence: occurrence,
	}, map[string]any{"occurrence": occurrence})
	if err != nil {
		t.Fatalf("NewDeterministicEvent: %v", err)
	}
	if err := p.AddEvent(event); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	return event
}

func onlyEffect(t *testing.T, p *gorapide.Poset, trigger *gorapide.Event) *gorapide.Event {
	t.Helper()
	effects := p.DirectEffects(trigger.ID)
	if len(effects) != 1 {
		t.Fatalf("trigger %s: want one direct effect, got %d", trigger.ID, len(effects))
	}
	return effects[0]
}

func TestStanfordRapideBasicConnectionIdentity(t *testing.T) {
	p := gorapide.NewPoset()
	source := NewComponent("A", Interface("Source").OutAction("Input").Build(), p)
	target := NewComponent("B", Interface("Target").InAction("Output").Build(), p)
	trigger := addConformanceTrigger(t, p, "basic/1")
	connection := Connect("A", "B").
		IdentifiedBy("connections/basic").
		On(pattern.MatchEvent("Input")).
		Send("Output").
		Build()

	if err := connection.Execute(trigger, source, target); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if p.Len() != 1 {
		t.Fatalf("basic connection created another occurrence: got %d events", p.Len())
	}
	outputs := p.ByName("Output")
	if len(outputs) != 1 || outputs[0].ID != trigger.ID {
		t.Fatalf("target output must be the trigger occurrence: %#v", outputs.IDs())
	}
	if len(p.DirectCauses(trigger.ID)) != 0 || len(p.DirectEffects(trigger.ID)) != 0 {
		t.Fatal("basic identity must not create a causal self-edge")
	}
}

func TestStanfordRapidePipeOrdersOutputsAcrossIndependentTriggers(t *testing.T) {
	p := gorapide.NewPoset()
	source := NewComponent("A", Interface("Source").OutAction("Input").Build(), p)
	target := NewComponent("B", Interface("Target").InAction("Output").Build(), p)
	firstTrigger := addConformanceTrigger(t, p, "pipe/1")
	secondTrigger := addConformanceTrigger(t, p, "pipe/2")
	if !p.IsCausallyIndependent(firstTrigger.ID, secondTrigger.ID) {
		t.Fatal("test requires independent triggers")
	}
	connection := Connect("A", "B").
		IdentifiedBy("connections/pipe").
		On(pattern.MatchEvent("Input")).
		Pipe().
		Send("Output").
		Build()

	if err := connection.Execute(firstTrigger, source, target); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	firstOutput := onlyEffect(t, p, firstTrigger)
	if err := connection.Execute(secondTrigger, source, target); err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	secondOutput := onlyEffect(t, p, secondTrigger)

	if !p.IsCausallyBefore(firstOutput.ID, secondOutput.ID) {
		t.Fatal("successive pipe outputs must follow the connection's firing order")
	}
	if !p.IsCausallyBefore(secondTrigger.ID, secondOutput.ID) {
		t.Fatal("pipe output must depend on its own trigger")
	}
}

func TestStanfordRapideAgentOutputsRemainIndependent(t *testing.T) {
	p := gorapide.NewPoset()
	source := NewComponent("A", Interface("Source").OutAction("Input").Build(), p)
	target := NewComponent("B", Interface("Target").InAction("Output").Build(), p)
	firstTrigger := addConformanceTrigger(t, p, "agent/1")
	secondTrigger := addConformanceTrigger(t, p, "agent/2")
	connection := Connect("A", "B").
		IdentifiedBy("connections/agent").
		On(pattern.MatchEvent("Input")).
		Agent().
		Send("Output").
		Build()

	if err := connection.Execute(firstTrigger, source, target); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	firstOutput := onlyEffect(t, p, firstTrigger)
	if err := connection.Execute(secondTrigger, source, target); err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	secondOutput := onlyEffect(t, p, secondTrigger)

	if !p.IsCausallyIndependent(firstOutput.ID, secondOutput.ID) {
		t.Fatal("separate agent outputs must not acquire connection-local order")
	}
	if !p.IsCausallyBefore(firstTrigger.ID, firstOutput.ID) ||
		!p.IsCausallyBefore(secondTrigger.ID, secondOutput.ID) {
		t.Fatal("each agent output must depend on its own trigger")
	}
}

func TestConnectionDerivedOutputIdentityIsReplayable(t *testing.T) {
	build := func() gorapide.EventID {
		p := gorapide.NewPoset()
		source := NewComponent("A", Interface("Source").OutAction("Input").Build(), p)
		target := NewComponent("B", Interface("Target").InAction("Output").Build(), p)
		trigger := addConformanceTrigger(t, p, "replay/1")
		connection := Connect("A", "B").
			IdentifiedBy("connections/replay").
			On(pattern.MatchEvent("Input")).
			Agent().
			Send("Output").
			Build()
		if err := connection.Execute(trigger, source, target); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		return onlyEffect(t, p, trigger).ID
	}

	first := build()
	second := build()
	if first != second {
		t.Fatalf("replay produced different output IDs:\nfirst=%s\nsecond=%s", first, second)
	}
}

func TestAgentCanonicalResultIgnoresIndependentProcessingOrder(t *testing.T) {
	build := func(reverse bool) []byte {
		p := gorapide.NewPoset()
		source := NewComponent("A", Interface("Source").OutAction("Input").Build(), p)
		target := NewComponent("B", Interface("Target").InAction("Output").Build(), p)
		first, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: "stanford-rapide-1.0", Model: "agent-order", Instance: "A",
			Action: "Input", Occurrence: "1",
		}, map[string]any{"n": 1})
		if err != nil {
			t.Fatal(err)
		}
		second, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: "stanford-rapide-1.0", Model: "agent-order", Instance: "A",
			Action: "Input", Occurrence: "2",
		}, map[string]any{"n": 2})
		if err != nil {
			t.Fatal(err)
		}
		triggers := []*gorapide.Event{first, second}
		if reverse {
			triggers[0], triggers[1] = triggers[1], triggers[0]
		}
		for _, trigger := range triggers {
			if err := p.AddEvent(trigger); err != nil {
				t.Fatal(err)
			}
		}
		connection := Connect("A", "B").
			IdentifiedBy("connections/agent-order").
			On(pattern.MatchEvent("Input")).
			Agent().
			Send("Output").
			Build()
		for _, trigger := range triggers {
			if err := connection.Execute(trigger, source, target); err != nil {
				t.Fatal(err)
			}
		}
		result, err := p.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	forward := build(false)
	reverse := build(true)
	if !bytes.Equal(forward, reverse) {
		t.Fatalf("independent agent processing order changed canonical result:\nforward=%s\nreverse=%s", forward, reverse)
	}
}

func TestPipeCanonicalResultIgnoresRootInsertionOrder(t *testing.T) {
	build := func(reverseInsertion bool) []byte {
		p := gorapide.NewPoset()
		source := NewComponent("A", Interface("Source").OutAction("Input").Build(), p)
		target := NewComponent("B", Interface("Target").InAction("Output").Build(), p)
		first, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: "stanford-rapide-1.0", Model: "pipe-order", Instance: "A",
			Action: "Input", Occurrence: "1",
		}, map[string]any{"n": 1})
		if err != nil {
			t.Fatal(err)
		}
		second, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: "stanford-rapide-1.0", Model: "pipe-order", Instance: "A",
			Action: "Input", Occurrence: "2",
		}, map[string]any{"n": 2})
		if err != nil {
			t.Fatal(err)
		}
		insertion := []*gorapide.Event{first, second}
		if reverseInsertion {
			insertion[0], insertion[1] = insertion[1], insertion[0]
		}
		for _, trigger := range insertion {
			if err := p.AddEvent(trigger); err != nil {
				t.Fatal(err)
			}
		}
		connection := Connect("A", "B").
			IdentifiedBy("connections/pipe-order").
			On(pattern.MatchEvent("Input")).
			Pipe().
			Send("Output").
			Build()
		// The pipe firing order is semantic input and remains fixed.
		if err := connection.Execute(first, source, target); err != nil {
			t.Fatal(err)
		}
		if err := connection.Execute(second, source, target); err != nil {
			t.Fatal(err)
		}
		result, err := p.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	forward := build(false)
	reverse := build(true)
	if !bytes.Equal(forward, reverse) {
		t.Fatalf("root insertion order changed canonical pipe result:\nforward=%s\nreverse=%s", forward, reverse)
	}
}
