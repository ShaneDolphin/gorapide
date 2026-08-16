package arch

import (
	"bytes"
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func mappedConnectionArchitecture(t *testing.T, reverse bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("mapped-connection")
	source := NewComponent("source", Interface("Source").
		OutAction("Input", P("left", "Integer")).Build(), nil)
	target := NewComponent("target", Interface("Target").
		InAction("Received", P("right", "Integer"), P("status", "String")).Build(), nil)
	for _, component := range []*Component{source, target} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	parameters := []ConnectionParameter{
		ConnectionBindingParam("right", "N"),
		ConnectionLiteralParam("status", "mapped"),
	}
	if reverse {
		parameters[0], parameters[1] = parameters[1], parameters[0]
	}
	if err := architecture.AddConnection(Connect("source", "target").
		IdentifiedBy("source/target").
		On(pattern.MatchEvent("Input").BindParam("left", pattern.Var("N").WithType("Integer"))).
		Agent().SendParameters("Received", parameters...).Build()); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestClosedConnectionParametersBindAndSerializeDeterministically(t *testing.T) {
	forward := mappedConnectionArchitecture(t, false)
	reverse := mappedConnectionArchitecture(t, true)
	forwardDigest, err := forward.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	reverseDigest, err := reverse.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if forwardDigest != reverseDigest {
		t.Fatalf("connection parameter order changed model identity: %s != %s", forwardDigest, reverseDigest)
	}
	journal := NewExecutionJournal(forwardDigest, 5,
		InputEvent{Key: "input", Source: "source", Action: "Input", Params: map[string]any{"left": 6}},
	)
	left, err := forward.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	right, err := reverse.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	output := left.Poset.ByName("Received")
	if len(output) != 1 {
		t.Fatalf("outputs=%d, want 1", len(output))
	}
	if value, ok := output[0].Param("right"); !ok || value != int64(6) {
		t.Fatalf("bound parameter=%#v,%v; want int64(6)", value, ok)
	}
	if output[0].ParamString("status") != "mapped" {
		t.Fatalf("literal parameter=%q", output[0].ParamString("status"))
	}
	leftBytes, err := left.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := right.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatal("equivalent mapped connection execution is not byte-identical")
	}
}

func TestClosedConnectionParametersRejectUnboundOrDuplicateTargets(t *testing.T) {
	for _, test := range []struct {
		name       string
		parameters []ConnectionParameter
	}{
		{name: "unbound", parameters: []ConnectionParameter{ConnectionBindingParam("n", "Missing")}},
		{name: "duplicate", parameters: []ConnectionParameter{
			ConnectionBindingParam("n", "N"), ConnectionBindingParam("n", "N"),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			architecture := NewArchitecture("invalid-mapping")
			source := NewComponent("source", Interface("Source").OutAction("Input", P("n", "Integer")).Build(), nil)
			target := NewComponent("target", Interface("Target").InAction("Received", P("n", "Integer")).Build(), nil)
			for _, component := range []*Component{source, target} {
				if err := architecture.AddComponent(component); err != nil {
					t.Fatal(err)
				}
			}
			if err := architecture.AddConnection(Connect("source", "target").IdentifiedBy("mapping").
				On(pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))).
				SendParameters("Received", test.parameters...).Build()); err != nil {
				t.Fatal(err)
			}
			if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) {
				t.Fatalf("expected deterministic model rejection, got %v", err)
			}
		})
	}
}
