package arch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func compoundConnectionArchitecture(t *testing.T, kind ConnectionKind) *Architecture {
	t.Helper()
	architecture := NewArchitecture("compound-connection")
	source := NewComponent("source", Interface("Source").
		OutAction("Begin", P("n", "Integer")).
		OutAction("End", P("n", "Integer")).Build(), nil)
	target := NewComponent("target", Interface("Target").
		InAction("Combined", P("n", "Integer")).Build(), nil)
	if err := architecture.AddComponent(source); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(target); err != nil {
		t.Fatal(err)
	}
	number := pattern.Var("N").WithType("Integer")
	trigger := pattern.Seq(
		pattern.MatchEvent("Begin").BindParam("n", number),
		pattern.MatchEvent("End").BindParam("n", number),
	)
	builder := Connect("source", "target").IdentifiedBy("join").On(trigger).
		SendParameters("Combined", ConnectionBindingParam("n", "N"))
	if kind == PipeConnection {
		builder.Pipe()
	} else {
		builder.Agent()
	}
	if err := architecture.AddConnection(builder.Build()); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestCompoundAgentConnectionFiresOncePerCanonicalMatch(t *testing.T) {
	architecture := compoundConnectionArchitecture(t, AgentConnection)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "begin", Source: "source", Action: "Begin", Params: map[string]any{"n": 7}},
		InputEvent{Key: "end", Source: "source", Action: "End", Params: map[string]any{"n": 7}, Causes: []string{"begin"}},
	)
	first, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
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
		t.Fatal("compound connection replay is not byte-identical")
	}
	combined := first.Poset.ByName("Combined")
	begin, end := first.Poset.ByName("Begin"), first.Poset.ByName("End")
	if len(combined) != 1 || len(begin) != 1 || len(end) != 1 {
		t.Fatalf("compound event counts combined/begin/end=%d/%d/%d", len(combined), len(begin), len(end))
	}
	value, _ := combined[0].Param("n")
	if value != int64(7) {
		t.Fatalf("compound binding value=%#v", value)
	}
	if !first.Poset.IsCausallyBefore(begin[0].ID, combined[0].ID) ||
		!first.Poset.IsCausallyBefore(end[0].ID, combined[0].ID) {
		t.Fatal("compound output does not depend on its complete trigger match")
	}
	if len(first.Firings) != 1 || len(first.Firings[0].MatchedEvents) != 2 ||
		len(first.Firings[0].Bindings) != 1 || first.Firings[0].Bindings[0].Placeholder != "N" {
		t.Fatalf("compound firing audit=%#v", first.Firings)
	}
}

func TestCompoundConnectionRequiresSharedBindingEquality(t *testing.T) {
	architecture := compoundConnectionArchitecture(t, AgentConnection)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "begin", Source: "source", Action: "Begin", Params: map[string]any{"n": 1}},
		InputEvent{Key: "end", Source: "source", Action: "End", Params: map[string]any{"n": 2}, Causes: []string{"begin"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Combined")) != 0 || len(result.Firings) != 0 {
		t.Fatalf("mismatched compound binding fired: %#v", result.Firings)
	}
}

func TestCompoundPipeConnectionOrdersDistinctMatches(t *testing.T) {
	architecture := compoundConnectionArchitecture(t, PipeConnection)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	inputs := []InputEvent{
		{Key: "begin-1", Source: "source", Action: "Begin", Params: map[string]any{"n": 1}},
		{Key: "end-1", Source: "source", Action: "End", Params: map[string]any{"n": 1}, Causes: []string{"begin-1"}},
		{Key: "begin-2", Source: "source", Action: "Begin", Params: map[string]any{"n": 2}},
		{Key: "end-2", Source: "source", Action: "End", Params: map[string]any{"n": 2}, Causes: []string{"begin-2"}},
	}
	forward, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10, inputs...))
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(inputs)-1; i < j; i, j = i+1, j-1 {
		inputs[i], inputs[j] = inputs[j], inputs[i]
	}
	reverse, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10, inputs...))
	if err != nil {
		t.Fatal(err)
	}
	forwardBytes, _ := forward.MarshalCanonical()
	reverseBytes, _ := reverse.MarshalCanonical()
	if !bytes.Equal(forwardBytes, reverseBytes) {
		t.Fatal("input declaration order changed compound pipe artifact")
	}
	outputs := forward.Poset.ByName("Combined")
	if len(outputs) != 2 {
		t.Fatalf("compound pipe outputs=%d", len(outputs))
	}
	if !forward.Poset.IsCausallyBefore(outputs[0].ID, outputs[1].ID) &&
		!forward.Poset.IsCausallyBefore(outputs[1].ID, outputs[0].ID) {
		t.Fatal("compound pipe did not serialize its output matches")
	}
}

func TestCompoundConnectionValidationRejectsUnsupportedBoundaries(t *testing.T) {
	build := func(connection *Connection) error {
		architecture := NewArchitecture("invalid-compound")
		source := NewComponent("source", Interface("Source").OutAction("A").OutAction("B").Build(), nil)
		target := NewComponent("target", Interface("Target").InAction("C").Build(), nil)
		if err := architecture.AddComponent(source); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(target); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddConnection(connection); err != nil {
			t.Fatal(err)
		}
		_, err := architecture.DeterministicModelDigest()
		return err
	}
	compound := pattern.Disjoint(pattern.MatchEvent("A"), pattern.MatchEvent("B"))
	tests := []struct {
		name       string
		connection *Connection
		want       string
	}{
		{name: "basic compound", connection: Connect("source", "target").IdentifiedBy("basic").On(compound).Send("C").Build(), want: "requires a single-event trigger"},
		{name: "wildcard target", connection: Connect("source", "*").IdentifiedBy("target").On(compound).Agent().Send("C").Build(), want: "requires one explicit target component"},
		{name: "implicit action", connection: Connect("source", "target").IdentifiedBy("action").On(compound).Agent().Forward().Build(), want: "requires one explicit target action"},
		{name: "empty trigger", connection: Connect("source", "target").IdentifiedBy("empty").On(pattern.IterateZeroOrMore(pattern.MatchEvent("A"), pattern.RelationDisjoint)).Agent().Send("C").Build(), want: "can match an empty computation"},
		{name: "state guard without connection witnesses", connection: Connect("source", "target").IdentifiedBy("state").On(pattern.Where(pattern.MatchEvent("A"), pattern.StateCondition("source\x00ready", "Boolean"))).Agent().Send("C").Build(), want: "requires consistent-cut state witnesses"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := build(test.connection)
			if !errors.Is(err, ErrUnsupportedDeterministicModel) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want unsupported error containing %q", err, test.want)
			}
		})
	}
}

func TestCompoundArchitectureConnectionCanObserveMultipleSources(t *testing.T) {
	architecture := NewArchitecture("multi-source-connection")
	left := NewComponent("left", Interface("Left").OutAction("A").Build(), nil)
	right := NewComponent("right", Interface("Right").OutAction("B").Build(), nil)
	target := NewComponent("target", Interface("Target").InAction("C").Build(), nil)
	for _, component := range []*Component{left, right, target} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	trigger := pattern.Seq(
		pattern.MatchEvent("A").WhereSource("left"),
		pattern.MatchEvent("B").WhereSource("right"),
	)
	if err := architecture.AddConnection(Connect("*", "target").IdentifiedBy("multi").
		On(trigger).Agent().Send("C").Build()); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "a", Source: "left", Action: "A"},
		InputEvent{Key: "b", Source: "right", Action: "B", Causes: []string{"a"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	outputs := result.Poset.ByName("C")
	if len(outputs) != 1 || len(result.Firings) != 1 || len(result.Firings[0].MatchedEvents) != 2 {
		t.Fatalf("multi-source compound connection result=%#v", result.Firings)
	}
	for _, input := range append(result.Poset.ByName("A"), result.Poset.ByName("B")...) {
		if !result.Poset.IsCausallyBefore(input.ID, outputs[0].ID) {
			t.Fatalf("multi-source match event %s does not cause output", input.ID)
		}
	}
}
