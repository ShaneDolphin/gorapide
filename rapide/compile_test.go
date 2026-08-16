package rapide

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
	"github.com/ShaneDolphin/gorapide/pattern"
)

const pipeSource = `
type Source is interface
  action out Input(n : Integer);
end interface Source;

type Sink is interface
  action in Received(n : Integer);
end interface Sink;

architecture Flow() is
  source : Source;
  sink : Sink;
connect
  source.Input => sink.Received;
end architecture Flow;
`

const pipeSourceReordered = `
-- Declaration and constituent order are intentionally reversed.
type Sink is interface
  action in Received(n : Integer);
end interface Sink;

type Source is interface
  action out Input(n : Integer);
end interface Source;

architecture Flow() is
  sink : Sink;
  source : Source;
connect
  source.Input() => sink.Received();
end architecture Flow;
`

func TestCompileRapideArchitectureExecutesPipeConnectionDeterministically(t *testing.T) {
	model, err := Compile([]byte(pipeSource), "Flow")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "one", Source: "source", Action: "Input", Params: map[string]any{"n": 1}},
		arch.InputEvent{Key: "two", Source: "source", Action: "Input", Params: map[string]any{"n": 2}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	outputs := result.Poset.ByName("Received")
	if len(outputs) != 2 {
		t.Fatalf("parsed pipe outputs=%d, want 2", len(outputs))
	}
	ordered := result.Poset.IsCausallyBefore(outputs[0].ID, outputs[1].ID) ||
		result.Poset.IsCausallyBefore(outputs[1].ID, outputs[0].ID)
	if !ordered {
		t.Fatal("parsed '=>' connection did not preserve pipe causality")
	}
}

func TestCompileRapideIgnoresDeclarationOrderAndEmptyActionParentheses(t *testing.T) {
	forward, err := Compile([]byte(pipeSource), "flow")
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := Compile([]byte(pipeSourceReordered), "FLOW")
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, err := forward.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := reverse.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("source declaration order changed model digest: %s != %s", leftDigest, rightDigest)
	}
	journal := arch.NewExecutionJournal(leftDigest, 10,
		arch.InputEvent{Key: "input", Source: "source", Action: "Input", Params: map[string]any{"n": 4}},
	)
	left, err := forward.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	right, err := reverse.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
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
		t.Fatalf("equivalent parsed execution differs:\nleft=%s\nright=%s", leftBytes, rightBytes)
	}
}

func TestParseAllThreeRapideConnectionOperators(t *testing.T) {
	source := []byte(`
type A is interface action out X(); end interface A;
type B is interface action in Y(); end interface B;
architecture Ops() is a : A; b : B; connect
  a.X to b.Y;
  a.X => b.Y;
  a.X ||> b.Y;
end architecture Ops;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	connections := file.Architectures[0].Connections
	if len(connections) != 3 || connections[0].Connector != ConnectBasic ||
		connections[1].Connector != ConnectPipe || connections[2].Connector != ConnectAgent {
		t.Fatalf("connection operators parsed incorrectly: %#v", connections)
	}
}

func TestPatternConnectionListsAreCanonicalCartesianProducts(t *testing.T) {
	declarations := `
type Emitter is interface
  action out A(value : Integer);
  action out B(value : Integer);
end interface Emitter;
type Receiver is interface
  action in C(value : Integer);
  action in D(value : Integer);
end interface Receiver;
`
	listSource := []byte(declarations + `
architecture Flow() is emitter : Emitter; receiver : Receiver; connect
  (?V : Integer) emitter.A(?V), emitter.B(?V) => receiver.C(?V), receiver.D(?V);
end architecture Flow;
`)
	explicitSource := []byte(declarations + `
architecture Flow() is receiver : Receiver; emitter : Emitter; connect
  (?V : Integer) emitter.B(?V) => receiver.D(?V);
  (?V : Integer) emitter.A(?V) => receiver.C(?V);
  (?V : Integer) emitter.B(?V) => receiver.C(?V);
  (?V : Integer) emitter.A(?V) => receiver.D(?V);
end architecture Flow;
`)
	file, err := Parse(listSource)
	if err != nil {
		t.Fatal(err)
	}
	connections := file.Architectures[0].Connections
	if len(connections) != 4 {
		t.Fatalf("pattern connection list expanded to %d rules, want 4", len(connections))
	}
	wantPairs := []string{"A->C", "A->D", "B->C", "B->D"}
	gotPairs := make([]string, 0, len(connections))
	for _, connection := range connections {
		gotPairs = append(gotPairs, connection.Source.Action+"->"+connection.Target.Action)
	}
	if !reflect.DeepEqual(gotPairs, wantPairs) {
		t.Fatalf("pattern connection Cartesian product:\nwant %v\n got %v", wantPairs, gotPairs)
	}

	listModel, err := Compile(listSource, "Flow")
	if err != nil {
		t.Fatal(err)
	}
	explicitModel, err := Compile(explicitSource, "flow")
	if err != nil {
		t.Fatal(err)
	}
	listDigest, err := listModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicitModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if listDigest != explicitDigest {
		t.Fatalf("connection-list shorthand changed model identity: %s != %s", listDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(listDigest, 20,
		arch.InputEvent{Key: "a", Source: "emitter", Action: "A", Params: map[string]any{"value": 1}},
		arch.InputEvent{Key: "b", Source: "emitter", Action: "B", Params: map[string]any{"value": 2}},
	)
	listResult, err := listModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	explicitResult, err := explicitModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"C", "D"} {
		events := listResult.Poset.ByName(name)
		if len(events) != 2 {
			t.Fatalf("connection list generated %d %s events, want 2", len(events), name)
		}
		values := make(map[int64]bool, len(events))
		for _, event := range events {
			value, ok := event.Param("value")
			if !ok {
				t.Fatalf("generated %s event has no value parameter", name)
			}
			values[value.(int64)] = true
		}
		if !values[1] || !values[2] {
			t.Fatalf("connection list generated %s values %v", name, values)
		}
	}
	listArtifact, err := listResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	explicitArtifact, err := explicitResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(listArtifact, explicitArtifact) {
		t.Fatalf("connection-list shorthand changed execution artifact:\nlist=%s\nfull=%s", listArtifact, explicitArtifact)
	}
}

func TestClosedIfConnectionGeneratorIsCanonicalElaboration(t *testing.T) {
	declarations := `
type Emitter is interface
  action out A(value : Integer);
  action out B(value : Integer);
end interface Emitter;
type Receiver is interface
  action in C(value : Integer);
  action in D(value : Integer);
end interface Receiver;
`
	generatedSource := []byte(declarations + `
architecture Flow() is emitter : Emitter; receiver : Receiver; connect
  if (2 * 3 = 6) and not False generate
    (?V : Integer) emitter.A(?V), emitter.B(?V) => receiver.C(?V), receiver.D(?V);
  end generate if;
end architecture Flow;
`)
	explicitSource := []byte(declarations + `
architecture Flow() is receiver : Receiver; emitter : Emitter; connect
  (?V : Integer) emitter.B(?V) => receiver.D(?V);
  (?V : Integer) emitter.A(?V) => receiver.C(?V);
  (?V : Integer) emitter.B(?V) => receiver.C(?V);
  (?V : Integer) emitter.A(?V) => receiver.D(?V);
end architecture Flow;
`)
	file, err := Parse(generatedSource)
	if err != nil {
		t.Fatal(err)
	}
	architecture := file.Architectures[0]
	if len(architecture.Connections) != 0 || len(architecture.ConnectionGenerators) != 1 {
		t.Fatalf("connection generator AST=%#v", architecture)
	}
	generator := architecture.ConnectionGenerators[0]
	if generator.Kind != ConnectionGeneratorIf || len(generator.Connections) != 4 || len(generator.Generators) != 0 {
		t.Fatalf("connection generator body=%#v", generator)
	}

	generated, err := Compile(generatedSource, "Flow")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(explicitSource, "flow")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, err := generated.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if generatedDigest != explicitDigest {
		t.Fatalf("generated topology digest=%s, explicit=%s", generatedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 30,
		arch.InputEvent{Key: "a", Source: "emitter", Action: "A", Params: map[string]any{"value": 1}},
		arch.InputEvent{Key: "b", Source: "emitter", Action: "B", Params: map[string]any{"value": 2}},
	)
	generatedResult, err := generated.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	explicitResult, err := explicit.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	generatedArtifact, _ := generatedResult.MarshalCanonical()
	explicitArtifact, _ := explicitResult.MarshalCanonical()
	if !bytes.Equal(generatedArtifact, explicitArtifact) {
		t.Fatalf("connection generator changed execution artifact:\ngenerated=%s\nexplicit=%s", generatedArtifact, explicitArtifact)
	}
	if len(generatedResult.Poset.ByName("C")) != 2 || len(generatedResult.Poset.ByName("D")) != 2 {
		t.Fatalf("generated Cartesian outputs C/D=%d/%d, want 2/2",
			len(generatedResult.Poset.ByName("C")), len(generatedResult.Poset.ByName("D")))
	}
}

func TestClosedIfConnectionGeneratorsNestAndFalseGeneratesNothing(t *testing.T) {
	declarations := `
type Emitter is interface action out A(); action out B(); end interface Emitter;
type Receiver is interface action in C(); action in D(); end interface Receiver;
`
	generatedSource := []byte(declarations + `
architecture Flow() is emitter : Emitter; receiver : Receiver; connect
  if True generate
    if False generate
      emitter.A to receiver.C;
    end;
    emitter.B to receiver.D;
  end if;
end architecture Flow;
`)
	explicitSource := []byte(declarations + `
architecture Flow() is receiver : Receiver; emitter : Emitter; connect
  emitter.B to receiver.D;
end architecture Flow;
`)
	generated, err := Compile(generatedSource, "Flow")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(explicitSource, "Flow")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, _ := generated.DeterministicModelDigest()
	explicitDigest, _ := explicit.DeterministicModelDigest()
	if generatedDigest != explicitDigest {
		t.Fatalf("nested false generator changed topology: %s != %s", generatedDigest, explicitDigest)
	}
	result, err := generated.ExecuteDeterministic(arch.NewExecutionJournal(generatedDigest, 10,
		arch.InputEvent{Key: "a", Source: "emitter", Action: "A"},
		arch.InputEvent{Key: "b", Source: "emitter", Action: "B"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("C")) != 0 || len(result.Poset.ByName("D")) != 1 {
		t.Fatalf("nested generator outputs C/D=%d/%d, want 0/1",
			len(result.Poset.ByName("C")), len(result.Poset.ByName("D")))
	}
}

func TestConnectionGeneratorsRejectOpenIllTypedOrUnboundedSchemes(t *testing.T) {
	tests := []struct {
		name   string
		scheme string
		want   string
	}{
		{name: "integer condition", scheme: "if 1 generate emitter.A to receiver.C; end generate;", want: "condition has type Integer, want Boolean"},
		{name: "open name", scheme: "if Enabled generate emitter.A to receiver.C; end generate;", want: `condition must be a closed deterministic Boolean expression`},
		{name: "state name", scheme: "if $Enabled generate emitter.A to receiver.C; end generate;", want: `behavior state $Enabled is not declared`},
		{name: "checked failure", scheme: "if (1 / 0) = 0 generate emitter.A to receiver.C; end generate;", want: `division by zero`},
		{name: "wrong iterator type", scheme: "for I : Boolean in 1..2 generate emitter.A to receiver.C; end generate;", want: `iterator "I" has type Boolean, want Integer`},
		{name: "open lower bound", scheme: "for I : Integer in First..2 generate emitter.A to receiver.C; end generate;", want: `lower bound must be a closed deterministic Integer expression`},
		{name: "Boolean upper bound", scheme: "for I : Integer in 1..True generate emitter.A to receiver.C; end generate;", want: `upper bound has type Boolean, want Integer`},
		{name: "oversize range", scheme: "for I : Integer in 0..256 generate emitter.A to receiver.C; end generate;", want: `exceeds deterministic cardinality limit 256`},
		{name: "iterator shadow", scheme: "for I : Integer in 0..0 generate for I : Integer in 0..0 generate emitter.A to receiver.C; end generate; end generate;", want: `iterator "I" conflicts with an enclosing object or iterator`},
		{name: "general iterator form", scheme: "for 1 in True next 2 generate emitter.A to receiver.C; end generate;", want: `requires 'for I : Integer in First..Last'`},
		{name: "empty", scheme: "if True generate end generate;", want: `connection generator requires at least one connection`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Emitter is interface action out A(); end interface Emitter;
type Receiver is interface action in C(); end interface Receiver;
architecture Flow() is emitter : Emitter; receiver : Receiver; connect
` + test.scheme + `
end architecture Flow;
`)
			_, err := Compile(source, "Flow")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestConnectionSetGeneratorIsCanonicalFanoutElaboration(t *testing.T) {
	declarations := `
type Protocol is interface action in Receive(value : Integer); end interface Protocol;
type Emitter is interface action out Send(value : Integer); end interface Emitter;
type Receiver is interface
  action in Direct(value : Integer);
  service Port(-1..1) : Protocol;
end interface Receiver;
`
	generatedSource := []byte(declarations + `
architecture Flow() is emitter : Emitter; receiver : Receiver; connect
  (?V : Integer) emitter.Send(?V) to
    receiver.Direct(?V),
    for I : Integer in -1..1 generate
      if I /= 0 generate
        receiver.Port(I).Receive(?V)
      end if
    end for;
end architecture Flow;
`)
	explicitSource := []byte(declarations + `
architecture Flow() is receiver : Receiver; emitter : Emitter; connect
  (?V : Integer) emitter.Send(?V) to receiver.Port(1).Receive(?V);
  (?V : Integer) emitter.Send(?V) to receiver.Direct(?V);
  (?V : Integer) emitter.Send(?V) to receiver.Port(-01).Receive(?V);
end architecture Flow;
`)
	file, err := Parse(generatedSource)
	if err != nil {
		t.Fatal(err)
	}
	connections := file.Architectures[0].Connections
	if len(connections) != 2 || connections[0].Target.Action != "Direct" ||
		connections[1].TargetGenerator == nil ||
		connections[1].TargetGenerator.Kind != ConnectionGeneratorForRange ||
		len(connections[1].TargetGenerator.Generators) != 1 {
		t.Fatalf("generated connection-set AST=%#v", connections)
	}
	generated, err := CompileFile(file, "Flow")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(explicitSource, "flow")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, _ := generated.DeterministicModelDigest()
	explicitDigest, _ := explicit.DeterministicModelDigest()
	if generatedDigest != explicitDigest {
		t.Fatalf("connection-set fanout topology=%s, explicit=%s", generatedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 10,
		arch.InputEvent{Key: "send", Source: "emitter", Action: "Send", Params: map[string]any{"value": 7}},
	)
	generatedResult, err := generated.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	explicitResult, err := explicit.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	generatedArtifact, _ := generatedResult.MarshalCanonical()
	explicitArtifact, _ := explicitResult.MarshalCanonical()
	if !bytes.Equal(generatedArtifact, explicitArtifact) {
		t.Fatalf("connection-set fanout changed artifact:\ngenerated=%s\nexplicit=%s", generatedArtifact, explicitArtifact)
	}
	events := eventsWithoutArchitectureStart(generatedResult.Poset)
	if len(events) != 1 || !events[0].HasObservation("receiver", "Direct") ||
		!events[0].HasObservation("receiver", "port(-1).receive") ||
		!events[0].HasObservation("receiver", "port(1).receive") ||
		events[0].HasObservation("receiver", "port(0).receive") {
		t.Fatalf("connection-set basic fanout observations=%#v", events)
	}
}

func TestConnectionSetGeneratorSubstitutesIteratorInTargetExpression(t *testing.T) {
	declarations := `
type Protocol is interface action in Receive(value : Integer); end interface Protocol;
type Emitter is interface action out Tick(); end interface Emitter;
type Receiver is interface service Port(0..1) : Protocol; end interface Receiver;
`
	generatedSource := []byte(declarations + `
architecture Flow() is emitter : Emitter; receiver : Receiver; connect
  emitter.Tick =>
    for I : Integer in 0..1 generate receiver.Port(I).Receive(I + 10) end generate;
end architecture Flow;
`)
	explicitSource := []byte(declarations + `
architecture Flow() is receiver : Receiver; emitter : Emitter; connect
  emitter.Tick => receiver.Port(1).Receive(1 + 10);
  emitter.Tick => receiver.Port(0).Receive(0 + 10);
end architecture Flow;
`)
	generated, err := Compile(generatedSource, "Flow")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(explicitSource, "flow")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, _ := generated.DeterministicModelDigest()
	explicitDigest, _ := explicit.DeterministicModelDigest()
	if generatedDigest != explicitDigest {
		t.Fatalf("target expression generator topology=%s, explicit=%s", generatedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 10,
		arch.InputEvent{Key: "tick", Source: "emitter", Action: "Tick"},
	)
	generatedResult, err := generated.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	explicitResult, err := explicit.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	generatedArtifact, _ := generatedResult.MarshalCanonical()
	explicitArtifact, _ := explicitResult.MarshalCanonical()
	if !bytes.Equal(generatedArtifact, explicitArtifact) {
		t.Fatalf("target expression generator changed artifact:\ngenerated=%s\nexplicit=%s", generatedArtifact, explicitArtifact)
	}
	for index := 0; index <= 1; index++ {
		outputs := generatedResult.Poset.ByName(fmt.Sprintf("port(%d).receive", index))
		if len(outputs) != 1 || outputs[0].ParamInt("value") != index+10 {
			t.Fatalf("target expression output index %d=%#v", index, outputs)
		}
	}
}

func TestConnectionSetGeneratorsRejectOpenIllTypedOrEmptySchemes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "integer condition", body: "if 1 generate receiver.C end generate", want: "condition has type Integer, want Boolean"},
		{name: "open bound", body: "for I : Integer in First..1 generate receiver.C end generate", want: "lower bound must be a closed deterministic Integer expression"},
		{name: "wrong iterator", body: "for I : Boolean in 0..1 generate receiver.C end generate", want: `iterator "I" has type Boolean, want Integer`},
		{name: "oversize", body: "for I : Integer in 0..256 generate receiver.C end generate", want: "exceeds deterministic cardinality limit 256"},
		{name: "empty", body: "if True generate end generate", want: "connection-set generator requires at least one target"},
		{name: "general form", body: "for 1 in True next 2 generate receiver.C end generate", want: "requires 'for I : Integer in First..Last'"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Emitter is interface action out A(); end interface Emitter;
type Receiver is interface action in C(); end interface Receiver;
architecture Flow() is emitter : Emitter; receiver : Receiver; connect
  emitter.A to ` + test.body + `;
end architecture Flow;
`)
			_, err := Compile(source, "Flow")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestFiniteConnectionGeneratorSubstitutesIteratorInRuleGuard(t *testing.T) {
	declarations := `
type Emitter is interface action out Send(value : Integer); end interface Emitter;
type Receiver is interface action in Receive(value : Integer); end interface Receiver;
`
	generatedSource := []byte(declarations + `
architecture Flow() is emitter : Emitter; receiver : Receiver; connect
  for I : Integer in 0..1 generate
    (?V : Integer) emitter.Send(?V) where ?V = I => receiver.Receive(?V);
  end generate;
end architecture Flow;
`)
	explicitSource := []byte(declarations + `
architecture Flow() is receiver : Receiver; emitter : Emitter; connect
  (?V : Integer) emitter.Send(?V) where ?V = 1 => receiver.Receive(?V);
  (?V : Integer) emitter.Send(?V) where ?V = 0 => receiver.Receive(?V);
end architecture Flow;
`)
	generated, err := Compile(generatedSource, "Flow")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(explicitSource, "flow")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, _ := generated.DeterministicModelDigest()
	explicitDigest, _ := explicit.DeterministicModelDigest()
	if generatedDigest != explicitDigest {
		t.Fatalf("guard iterator substitution topology=%s, explicit=%s", generatedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 20,
		arch.InputEvent{Key: "zero", Source: "emitter", Action: "Send", Params: map[string]any{"value": 0}},
		arch.InputEvent{Key: "one", Source: "emitter", Action: "Send", Params: map[string]any{"value": 1}},
		arch.InputEvent{Key: "two", Source: "emitter", Action: "Send", Params: map[string]any{"value": 2}},
	)
	generatedResult, err := generated.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	explicitResult, err := explicit.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	generatedArtifact, _ := generatedResult.MarshalCanonical()
	explicitArtifact, _ := explicitResult.MarshalCanonical()
	if !bytes.Equal(generatedArtifact, explicitArtifact) || len(generatedResult.Poset.ByName("Receive")) != 2 {
		t.Fatalf("guard iterator substitution result generated=%s explicit=%s", generatedArtifact, explicitArtifact)
	}
}

func TestGuardedConnectionListItemsUseClosedMatchBindings(t *testing.T) {
	declarations := `
type Emitter is interface
  action out A(value : Integer);
  action out B(value : Integer);
end interface Emitter;
type Receiver is interface
  action in C(value : Integer);
  action in D(value : Integer);
end interface Receiver;
`
	listSource := []byte(declarations + `
architecture Flow() is emitter : Emitter; receiver : Receiver; connect
  (?V : Integer) emitter.A(?V) where ?V > 0,
                 emitter.B(?V) where ?V < 0
    => receiver.C(?V), receiver.D(?V);
end architecture Flow;
`)
	explicitSource := []byte(declarations + `
architecture Flow() is receiver : Receiver; emitter : Emitter; connect
  (?V : Integer) emitter.B(?V) where ?V < 0 => receiver.D(?V);
  (?V : Integer) emitter.A(?V) where ?V > 0 => receiver.C(?V);
  (?V : Integer) emitter.B(?V) where ?V < 0 => receiver.C(?V);
  (?V : Integer) emitter.A(?V) where ?V > 0 => receiver.D(?V);
end architecture Flow;
`)
	file, err := Parse(listSource)
	if err != nil {
		t.Fatal(err)
	}
	connections := file.Architectures[0].Connections
	if len(connections) != 4 || connections[0].Guard == nil || connections[1].Guard == nil ||
		connections[2].Guard == nil || connections[3].Guard == nil ||
		connections[0].Guard.Operator != ">" || connections[1].Guard.Operator != ">" ||
		connections[2].Guard.Operator != "<" || connections[3].Guard.Operator != "<" {
		t.Fatalf("per-trigger connection-list guards=%#v", connections)
	}

	listed, err := Compile(listSource, "Flow")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(explicitSource, "flow")
	if err != nil {
		t.Fatal(err)
	}
	listedDigest, err := listed.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if listedDigest != explicitDigest {
		t.Fatalf("guarded connection list changed model identity: %s != %s", listedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(listedDigest, 30,
		arch.InputEvent{Key: "a-pass", Source: "emitter", Action: "A", Params: map[string]any{"value": 1}},
		arch.InputEvent{Key: "a-block", Source: "emitter", Action: "A", Params: map[string]any{"value": -1}},
		arch.InputEvent{Key: "b-pass", Source: "emitter", Action: "B", Params: map[string]any{"value": -2}},
		arch.InputEvent{Key: "b-block", Source: "emitter", Action: "B", Params: map[string]any{"value": 2}},
	)
	listedResult, err := listed.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	explicitResult, err := explicit.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"C", "D"} {
		events := listedResult.Poset.ByName(name)
		if len(events) != 2 {
			t.Fatalf("guarded list generated %d %s events, want 2", len(events), name)
		}
		values := make(map[int]bool, len(events))
		for _, event := range events {
			values[event.ParamInt("value")] = true
		}
		if !values[1] || !values[-2] {
			t.Fatalf("guarded list generated %s values %v", name, values)
		}
	}
	listedArtifact, err := listedResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	explicitArtifact, err := explicitResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(listedArtifact, explicitArtifact) {
		t.Fatalf("guarded list changed execution artifact:\nlist=%s\nfull=%s", listedArtifact, explicitArtifact)
	}
}

func TestConnectionGuardsRejectOpenOrIllTypedForms(t *testing.T) {
	tests := []struct {
		name, guard, want string
	}{
		{"non Boolean", "1", "pattern guard has type Integer, want Boolean"},
		{"open name", "Missing", `pattern guard name "Missing" is not declared`},
		{"state without owner", "$enabled", "state dereference $enabled is not declared"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Emitter is interface action out A(value : Integer); end interface Emitter;
type Receiver is interface action in B(value : Integer); end interface Receiver;
architecture Flow() is emitter : Emitter; receiver : Receiver; connect
  (?V : Integer) emitter.A(?V) where ` + test.guard + ` => receiver.B(?V);
end architecture Flow;
`)
			_, err := Compile(source, "Flow")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}

	distinct := []byte(`
type Emitter is interface action out A(); end interface Emitter;
type Receiver is interface action in B(); end interface Receiver;
architecture Flow() is emitter : Emitter; receiver : Receiver; connect
  emitter.A where true to receiver.B;
  emitter.A where false to receiver.B;
end architecture Flow;
`)
	if _, err := Compile(distinct, "Flow"); err != nil {
		t.Fatalf("distinct guarded connections collided semantically: %v", err)
	}
}

func TestGuardedCompoundConnectionUsesCompleteMatch(t *testing.T) {
	source := []byte(`
type Left is interface action out A(value : Integer); end interface Left;
type Right is interface action out B(value : Integer); end interface Right;
type Target is interface action in C(value : Integer); end interface Target;
architecture Flow() is left : Left; right : Right; target : Target; connect
  (?V : Integer) (left.A(?V) -> right.B(?V)) where ?V > 0 => target.C(?V);
end architecture Flow;
`)
	model, err := Compile(source, "Flow")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "a-positive", Source: "left", Action: "A", Params: map[string]any{"value": 2}},
		arch.InputEvent{Key: "b-positive", Source: "right", Action: "B", Params: map[string]any{"value": 2}, Causes: []string{"a-positive"}},
		arch.InputEvent{Key: "a-negative", Source: "left", Action: "A", Params: map[string]any{"value": -2}},
		arch.InputEvent{Key: "b-negative", Source: "right", Action: "B", Params: map[string]any{"value": -2}, Causes: []string{"a-negative"}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	outputs := result.Poset.ByName("C")
	if len(outputs) != 1 || outputs[0].ParamInt("value") != 2 {
		t.Fatalf("guarded compound outputs=%#v", outputs)
	}
	aEvents := result.Poset.ByName("A")
	bEvents := result.Poset.ByName("B")
	if len(aEvents) != 2 || len(bEvents) != 2 {
		t.Fatalf("guarded compound inputs A=%#v B=%#v", aEvents, bEvents)
	}
	positiveA, positiveB := aEvents[0], bEvents[0]
	if positiveA.ParamInt("value") != 2 {
		positiveA = aEvents[1]
	}
	if positiveB.ParamInt("value") != 2 {
		positiveB = bEvents[1]
	}
	if !result.Poset.IsCausallyBefore(positiveA.ID, outputs[0].ID) ||
		!result.Poset.IsCausallyBefore(positiveB.ID, outputs[0].ID) {
		t.Fatal("guarded compound output lost complete-match causality")
	}
}

func TestParseReportsStableSourceCoordinate(t *testing.T) {
	_, err := Parse([]byte("type Broken is interface\n  action sideways X();\nend interface Broken;"))
	var syntaxError *SyntaxError
	if !errors.As(err, &syntaxError) {
		t.Fatalf("expected SyntaxError, got %v", err)
	}
	if syntaxError.Position.Line != 2 || syntaxError.Position.Column != 10 ||
		syntaxError.Message != "expected action mode 'in' or 'out'" {
		t.Fatalf("unstable syntax diagnostic: %#v", syntaxError)
	}
}

func TestCompileRejectsUnsupportedOrIllTypedRapideSource(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "unsupported type",
			source: `type A is interface action out X(v : Duration); end interface A;
architecture M() is a : A; end architecture M;`,
			want: `type "Duration" is outside the current deterministic Rapide type subset`,
		},
		{
			name:   "unknown interface",
			source: `architecture M() is a : Missing; end architecture M;`,
			want:   `component "a" uses undeclared interface type "Missing"`,
		},
		{
			name: "wrong direction",
			source: `type A is interface action in X(); end interface A;
type B is interface action in Y(); end interface B;
architecture M() is a : A; b : B; connect a.X => b.Y; end architecture M;`,
			want: `connection source a.X is not an out action`,
		},
		{
			name: "parameter mismatch",
			source: `type A is interface action out X(left : Integer); end interface A;
type B is interface action in Y(right : Integer); end interface B;
architecture M() is a : A; b : B; connect a.X => b.Y; end architecture M;`,
			want: `requires identical parameter names and types`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "M")
			var typeErrorValue *TypeError
			if !errors.As(err, &typeErrorValue) {
				t.Fatalf("expected TypeError, got %v", err)
			}
			if !bytes.Contains([]byte(typeErrorValue.Message), []byte(test.want)) {
				t.Fatalf("diagnostic %q does not contain %q", typeErrorValue.Message, test.want)
			}
		})
	}
}

func TestCompileTypedConnectionPlaceholdersRenameParameters(t *testing.T) {
	source := []byte(`
type A is interface action out X(left : Integer); end interface A;
type B is interface action in Y(right : Integer); end interface B;
architecture M() is a : A; b : B; connect
  (?N : Integer) a.X(?N) => b.Y(?N);
end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 5,
		arch.InputEvent{Key: "input", Source: "a", Action: "X", Params: map[string]any{"left": 12}},
	))
	if err != nil {
		t.Fatal(err)
	}
	output := result.Poset.ByName("Y")
	if len(output) != 1 {
		t.Fatalf("mapped outputs=%d, want 1", len(output))
	}
	value, ok := output[0].Param("right")
	if !ok || value != int64(12) {
		t.Fatalf("mapped target parameter=%#v,%v; want int64(12)", value, ok)
	}
}

func TestCompileConnectionPlaceholderDeclarationOrderIsNotSemantic(t *testing.T) {
	template := func(declarations string) []byte {
		return []byte(`
type A is interface action out X(n : Integer, s : String); end interface A;
type B is interface action in Y(s : String, n : Integer); end interface B;
architecture M() is a : A; b : B; connect
  (` + declarations + `) a.X(?N, ?S) => b.Y(?S, ?N);
end architecture M;
`)
	}
	forward, err := Compile(template("?N : Integer; ?S : String"), "M")
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := Compile(template("?S : String; ?N : Integer"), "M")
	if err != nil {
		t.Fatal(err)
	}
	left, err := forward.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	right, err := reverse.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("placeholder declaration order changed model identity: %s != %s", left, right)
	}
}

func TestConnectionTargetsEvaluateClosedExpressions(t *testing.T) {
	source := []byte(`
type A is interface action out X(n : Integer, flag : Boolean); end interface A;
type B is interface action in Y(adjusted : Integer, copied : Integer, ready : Boolean); end interface B;
architecture M() is a : A; b : B; connect
  (?N : Integer; ?F : Boolean) a.X(?N, ?F) => b.Y(?N + 1, 7, not ?F);
end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "input", Source: "a", Action: "X", Params: map[string]any{"n": 4, "flag": false}},
	))
	if err != nil {
		t.Fatal(err)
	}
	outputs := result.Poset.ByName("Y")
	if len(outputs) != 1 || outputs[0].ParamInt("adjusted") != 5 ||
		outputs[0].ParamInt("copied") != 7 {
		t.Fatalf("connection expression output=%#v", outputs)
	}
	ready, ok := outputs[0].Param("ready")
	if !ok || ready != true {
		t.Fatalf("connection Boolean expression ready=%#v,%v", ready, ok)
	}
}

func TestNamedTargetAssociationsAreCanonicalAndMapByFormal(t *testing.T) {
	declarations := `
type A is interface action out X(n : Integer, flag : Boolean); end interface A;
type B is interface action in Y(first : Integer, ready : Boolean, last : Integer); end interface B;
`
	forwardSource := []byte(declarations + `
architecture M() is a : A; b : B; connect
  (?N : Integer; ?F : Boolean) a.X(?N, ?F) =>
    b.Y(?N, last is ?N + 2, ready is not ?F);
end architecture M;
`)
	reverseSource := []byte(declarations + `
architecture M() is b : B; a : A; connect
  (?F : Boolean; ?N : Integer) a.X(?N, ?F) =>
    b.Y(?N, READY is not ?F, LAST is ?N + 2);
end architecture M;
`)
	forward, err := Compile(forwardSource, "M")
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := Compile(reverseSource, "m")
	if err != nil {
		t.Fatal(err)
	}
	forwardDigest, _ := forward.DeterministicModelDigest()
	reverseDigest, _ := reverse.DeterministicModelDigest()
	if forwardDigest != reverseDigest {
		t.Fatalf("named association order/case changed topology: %s != %s", forwardDigest, reverseDigest)
	}
	journal := arch.NewExecutionJournal(forwardDigest, 10,
		arch.InputEvent{Key: "input", Source: "a", Action: "X", Params: map[string]any{"n": 5, "flag": false}},
	)
	left, err := forward.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	right, err := reverse.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	leftArtifact, _ := left.MarshalCanonical()
	rightArtifact, _ := right.MarshalCanonical()
	if !bytes.Equal(leftArtifact, rightArtifact) {
		t.Fatalf("named association order changed artifact:\nleft=%s\nright=%s", leftArtifact, rightArtifact)
	}
	output := left.Poset.ByName("Y")
	if len(output) != 1 || output[0].ParamInt("first") != 5 || output[0].ParamInt("last") != 7 {
		t.Fatalf("named association output=%#v", output)
	}
	if ready, _ := output[0].Param("ready"); ready != true {
		t.Fatalf("named association ready=%#v", ready)
	}
}

func TestNamedTargetAssociationsRejectMalformedBindings(t *testing.T) {
	tests := []struct {
		arguments string
		want      string
	}{
		{arguments: "missing is ?N, second is ?N", want: `names undeclared formal parameter "missing"`},
		{arguments: "first is ?N, first is ?N", want: `supplies target parameter "first" more than once`},
		{arguments: "first is ?N, ?N", want: `positional target arguments must precede named associations`},
	}
	for _, test := range tests {
		source := []byte(`
type A is interface action out X(n : Integer); end interface A;
type B is interface action in Y(first : Integer, second : Integer); end interface B;
architecture M() is a : A; b : B; connect
  (?N : Integer) a.X(?N) => b.Y(` + test.arguments + `);
end architecture M;
`)
		_, err := Compile(source, "M")
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("arguments %q got %v, want %q", test.arguments, err, test.want)
		}
	}
}

func TestSingleLiteralTargetDisambiguatesFromIndexedService(t *testing.T) {
	source := []byte(`
type A is interface action out X(); end interface A;
type B is interface action in Y(n : Integer); end interface B;
architecture M() is a : A; b : B; connect a.X => b.Y(7); end architecture M;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	parsed := file.Architectures[0].Connections[0].Target
	if parsed.Action != "Y(7)" || len(parsed.Path) != 1 || parsed.Path[0].Index == nil {
		t.Fatalf("ambiguous target was not preserved structurally: %#v", parsed)
	}
	model, err := CompileFile(file, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "input", Source: "a", Action: "X"},
	))
	if err != nil {
		t.Fatal(err)
	}
	outputs := result.Poset.ByName("Y")
	if len(outputs) != 1 || outputs[0].ParamInt("n") != 7 {
		t.Fatalf("single literal target output=%#v", outputs)
	}
}

func TestConnectionTargetExpressionsRejectOpenIllTypedOrFailingValues(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{name: "open name", expression: "Missing", want: `behavior expression name "Missing" is not declared`},
		{name: "state read", expression: "$Count", want: `behavior state $Count is not declared`},
		{name: "wrong type", expression: "True", want: `has type Boolean but parameter n has type Integer`},
		{name: "unbound placeholder", expression: "?Missing", want: `behavior expression name "Missing" is not declared`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type A is interface action out X(value : Integer); end interface A;
type B is interface action in Y(n : Integer); end interface B;
architecture M() is a : A; b : B; connect
  (?V : Integer) a.X(?V) => b.Y(` + test.expression + `);
end architecture M;
`)
			_, err := Compile(source, "M")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}

	failing := []byte(`
type A is interface action out X(); end interface A;
type B is interface action in Y(n : Integer); end interface B;
architecture M() is a : A; b : B; connect a.X => b.Y(1 / 0); end architecture M;
`)
	model, err := Compile(failing, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	_, err = model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "input", Source: "a", Action: "X"},
	))
	if err == nil || !strings.Contains(err.Error(), "division by zero") {
		t.Fatalf("failing connection expression got %v", err)
	}
}

func TestIndexedServiceAndLiteralActionTargetAmbiguityIsRejected(t *testing.T) {
	source := []byte(`
type Protocol is interface action in Receive(); end interface Protocol;
type A is interface action out X(); end interface A;
type B is interface
  action in Port(n : Integer);
  service Port(0..1) : Protocol;
end interface B;
architecture M() is a : A; b : B; connect a.X to b.Port(0); end architecture M;
`)
	_, err := Compile(source, "M")
	if err == nil || !strings.Contains(err.Error(), "ambiguous between a service and an action or function") {
		t.Fatalf("indexed service/action ambiguity got %v", err)
	}
}

func TestCompileCompoundArchitectureConnectionMatchesWholeComputation(t *testing.T) {
	source := []byte(`
type Left is interface action out Begin(n : Integer); end interface Left;
type Right is interface action out Finish(n : Integer); end interface Right;
type Sink is interface action in Combined(n : Integer); end interface Sink;
architecture M() is left : Left; right : Right; sink : Sink;
connect
  (?N : Integer) (left.Begin(?N) -> right.Finish(?N)) ||> sink.Combined(?N + 1);
end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "begin", Source: "left", Action: "Begin", Params: map[string]any{"n": 9}},
		arch.InputEvent{Key: "finish", Source: "right", Action: "Finish", Params: map[string]any{"n": 9}, Causes: []string{"begin"}},
	)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("parsed compound connection is not byte-identical")
	}
	combined := first.Poset.ByName("Combined")
	if len(combined) != 1 || len(first.Firings) != 1 || len(first.Firings[0].MatchedEvents) != 2 {
		t.Fatalf("parsed compound connection result=%#v", first.Firings)
	}
	value, _ := combined[0].Param("n")
	if value != int64(10) {
		t.Fatalf("parsed compound connection binding=%#v", value)
	}
	for _, name := range []string{"Begin", "Finish"} {
		input := first.Poset.ByName(name)
		if len(input) != 1 || !first.Poset.IsCausallyBefore(input[0].ID, combined[0].ID) {
			t.Fatalf("parsed compound connection lost %s causality", name)
		}
	}
}

func TestCompoundConnectionCommutativePatternOrderIsCanonical(t *testing.T) {
	compile := func(patternSource string) string {
		t.Helper()
		source := []byte(`
type Left is interface action out A(); end interface Left;
type Right is interface action out B(); end interface Right;
type Sink is interface action in C(); end interface Sink;
architecture M() is left : Left; right : Right; sink : Sink;
connect (` + patternSource + `) ||> sink.C();
end architecture M;
`)
		model, err := Compile(source, "M")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	if left, right := compile("left.A || right.B"), compile("right.B || left.A"); left != right {
		t.Fatalf("commutative compound connection order changed identity: %s != %s", left, right)
	}
	if forward, reverse := compile("left.A -> right.B"), compile("right.B -> left.A"); forward == reverse {
		t.Fatal("causal compound connection direction did not change identity")
	}
}

func TestCompileRejectsMalformedCompoundConnections(t *testing.T) {
	tests := []struct {
		name, pattern, connector, target, want string
	}{
		{name: "undeclared architecture-interface source", pattern: "(A -> right.B)", connector: "||>", target: "sink.C()", want: "connection pattern action .A is not declared"},
		{name: "incoming source", pattern: "(sink.C -> right.B)", connector: "||>", target: "sink.C()", want: "is not an out action"},
		{name: "basic compound", pattern: "(left.A -> right.B)", connector: "to", target: "sink.C()", want: "compound source patterns require"},
		{name: "empty source", pattern: "[* rel ~] left.A", connector: "||>", target: "sink.C()", want: "can match an empty computation"},
		{name: "unknown component", pattern: "(missing.A -> right.B)", connector: "||>", target: "sink.C()", want: "component \"missing\" is not declared"},
		{name: "unknown action", pattern: "(left.Missing -> right.B)", connector: "||>", target: "sink.C()", want: "action left.Missing is not declared"},
		{name: "out target", pattern: "(left.A -> right.B)", connector: "||>", target: "left.A()", want: "is not an in action"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Left is interface action out A(); end interface Left;
type Right is interface action out B(); end interface Right;
type Sink is interface action in C(); end interface Sink;
architecture M() is left : Left; right : Right; sink : Sink;
connect ` + test.pattern + ` ` + test.connector + ` ` + test.target + `;
end architecture M;
`)
			_, err := Compile(source, "M")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestCompileProvidesAndRequiresFunctionDeclarations(t *testing.T) {
	source := []byte(`
type Store is interface
  provides
    Read : function(key : String) return Integer;
    Notify : function(message : String);
  requires
    Write : function(key : String; value : Integer);
end interface Store;

architecture Functions() is
  store : Store;
end architecture Functions;
`)
	parsed, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	functions := parsed.Interfaces[0].Functions
	if len(functions) != 3 || functions[0].Mode != FunctionProvides ||
		functions[0].ReturnType != "Integer" || functions[1].ReturnType != "" ||
		functions[2].Mode != FunctionRequires || len(functions[2].Parameters) != 2 {
		t.Fatalf("function parts parsed incorrectly: %#v", functions)
	}
	compiled, err := Compile(source, "Functions")
	if err != nil {
		t.Fatal(err)
	}
	expected := arch.NewArchitecture("Functions")
	iface := arch.Interface("Store").
		ProvidesFunction("Read", "Integer", arch.P("key", "String")).
		ProvidesFunction("Notify", "", arch.P("message", "String")).
		RequiresFunction("Write", "", arch.P("key", "String"), arch.P("value", "Integer")).
		Build()
	if err := expected.AddComponent(arch.NewComponent("store", iface, nil)); err != nil {
		t.Fatal(err)
	}
	want, err := expected.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	got, err := compiled.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("parsed function model digest=%s, builder digest=%s", got, want)
	}
}

func TestFunctionPartDeclarationOrderIsNotSemantic(t *testing.T) {
	compile := func(body string) string {
		t.Helper()
		source := []byte("type API is interface\n" + body + "\nend interface API;\n" +
			"architecture M() is api : API; end architecture M;")
		model, err := Compile(source, "M")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	forward := compile("provides A : function() return Integer; B : function(v : String); requires C : function();")
	reverse := compile("requires C : function(); provides B : function(v : String); A : function() return Integer;")
	if forward != reverse {
		t.Fatalf("function part order changed model identity: %s != %s", forward, reverse)
	}
}

func TestCompileRejectsMalformedFunctionDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		want  string
		typed bool
	}{
		{name: "unsupported parameter", body: "provides F : function(v : Duration);", want: `type "Duration" is outside`, typed: true},
		{name: "unsupported return", body: "provides F : function() return Duration;", want: `return type "Duration": type "Duration" is outside`, typed: true},
		{name: "duplicate parameter", body: "provides F : function(v : Integer; V : Integer);", want: `duplicate parameter "V"`, typed: true},
		{name: "duplicate signature", body: "provides F : function(v : Integer); F : function(v : Integer);", want: `duplicate provides function signature "F"`, typed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type API is interface " + test.body + " end interface API; architecture M() is api : API; end architecture M;")
			_, err := Compile(source, "M")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
			if test.typed {
				var typeErr *TypeError
				if !errors.As(err, &typeErr) {
					t.Fatalf("got %T, want TypeError", err)
				}
			}
		})
	}
}

func TestCompileStaticFunctionConnectionAndProviderBehaviorExecute(t *testing.T) {
	source := []byte(`
type Client is interface
  action out Start(value : Integer);
  action out Done(value : Integer);
  requires Write : function(request : Integer);
  behavior
  begin
    (?V : Integer) Start(?V) =>
      Write(?V);
      Done(?V);
    ;
end interface Client;
type Server is interface
  action out Applied(value : Integer);
  provides Store : function(item : Integer);
  behavior
	stored : var Integer := 0;
    Store : function(item : Integer) is
      begin
		stored := item;
		Applied($stored);
      end function Store;
  begin
end interface Server;
architecture M() is
  client : Client;
  server : Server;
connect
  client.Write to server.Store;
end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10, arch.InputEvent{
		Key: "start", Source: "client", Action: "Start", Params: map[string]any{"value": 7},
	}))
	if err != nil {
		t.Fatal(err)
	}
	requiredCall := result.Poset.ByName("Write'Call")
	providedCall := result.Poset.ByName("Store'Call")
	requiredReturn := result.Poset.ByName("Write'Return")
	providedReturn := result.Poset.ByName("Store'Return")
	applied := result.Poset.ByName("Applied")
	done := result.Poset.ByName("Done")
	if len(requiredCall) != 1 || len(providedCall) != 1 || len(requiredReturn) != 1 ||
		len(providedReturn) != 1 || len(applied) != 1 || len(done) != 1 {
		t.Fatalf("parsed function route event counts call=%d/%d return=%d/%d applied=%d done=%d",
			len(requiredCall), len(providedCall), len(requiredReturn), len(providedReturn), len(applied), len(done))
	}
	if requiredCall[0].ID != providedCall[0].ID || requiredReturn[0].ID != providedReturn[0].ID {
		t.Fatal("parsed function route duplicated caller/provider occurrences")
	}
	if request, _ := requiredCall[0].Param("request"); request != int64(7) {
		t.Fatalf("required formal value=%#v", request)
	}
	if item, _ := providedCall[0].Param("item"); item != int64(7) {
		t.Fatalf("provided formal value=%#v", item)
	}
	if !result.Poset.IsCausallyBefore(providedCall[0].ID, applied[0].ID) ||
		!result.Poset.IsCausallyBefore(applied[0].ID, providedReturn[0].ID) ||
		!result.Poset.IsCausallyBefore(requiredReturn[0].ID, done[0].ID) {
		t.Fatal("parsed function route lost synchronous causality")
	}
	if len(result.State) != 1 || result.State[0].ComponentID != "server" ||
		result.State[0].Name != "stored" || result.State[0].Value.Text != "7" {
		t.Fatalf("parsed provider state=%#v", result.State)
	}
	if len(result.Firings) != 1 || len(result.Firings[0].StateWrites) != 1 ||
		result.Firings[0].StateWrites[0].ComponentID != "server" ||
		len(result.Firings[0].StateReads) != 1 || result.Firings[0].StateReads[0].ComponentID != "server" {
		t.Fatalf("parsed provider state audit=%#v", result.Firings)
	}
}

func TestCompileTypedBehaviorFunctionReturnExpression(t *testing.T) {
	source := []byte(`
type Client is interface
  action out Start(n : Integer);
  action out Done(n : Integer);
  requires Lookup : function(value : Integer) return Integer;
	behavior
	  result : var Integer := 0;
	begin
	  (?N : Integer) Start(?N) =>
		result := Lookup(?N);
		Done($result);
	  ;
end interface Client;
type Server is interface
  provides Fetch : function(operand : Integer) return Integer;
  behavior
    Fetch : function(operand : Integer) return Integer is
      begin
        return operand * 2 + 1;
      end function Fetch;
  begin
end interface Server;
architecture M() is client : Client; server : Server;
connect client.Lookup to server.Fetch;
end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10, arch.InputEvent{
		Key: "start", Source: "client", Action: "Start", Params: map[string]any{"n": 3},
	}))
	if err != nil {
		t.Fatal(err)
	}
	done := result.Poset.ByName("Done")
	returned := result.Poset.ByName("Fetch'Return")
	if len(done) != 1 || len(returned) != 1 {
		t.Fatalf("typed behavior done/return counts=%d/%d", len(done), len(returned))
	}
	if value, _ := done[0].Param("n"); value != int64(7) {
		t.Fatalf("typed behavior result=%#v, want int64(7)", value)
	}
	if len(result.State) != 1 || result.State[0].ComponentID != "client" || result.State[0].Value.Text != "7" {
		t.Fatalf("typed behavior result state=%#v", result.State)
	}
}

func TestCompiledBehaviorFunctionCanCallConnectedFunction(t *testing.T) {
	source := []byte(`
type Client is interface
  action out Start(n : Integer);
  requires Top : function(n : Integer);
end interface Client;
type Middle is interface
  provides Run : function(input : Integer);
  requires Next : function(value : Integer);
  behavior
    Run : function(input : Integer) is
      begin
        Next(input);
      end function Run;
  begin
end interface Middle;
type Leaf is interface
  action out Reached(n : Integer);
  provides Finish : function(operand : Integer);
  behavior
    Finish : function(operand : Integer) is
      begin
        Reached(operand);
      end function Finish;
  begin
end interface Leaf;
architecture M() is client : Client; middle : Middle; leaf : Leaf;
connect
  middle.Next to leaf.Finish;
  client.Top to middle.Run;
end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	client, _ := model.Component("client")
	number := pattern.Var("N").WithType("Integer")
	if err := client.AddDeclarativeRule(arch.Rule("start").
		On(pattern.MatchEvent("Start").BindParam("n", number)).
		Do(arch.CallFunction("top", "Top", arch.BindingParam("n", "N"))).Build()); err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10, arch.InputEvent{
		Key: "start", Source: "client", Action: "Start", Params: map[string]any{"n": 9},
	}))
	if err != nil {
		t.Fatal(err)
	}
	top := result.Poset.ByName("Top'Call")
	next := result.Poset.ByName("Next'Call")
	reached := result.Poset.ByName("Reached")
	topReturn := result.Poset.ByName("Top'Return")
	if len(top) != 1 || len(next) != 1 || len(reached) != 1 || len(topReturn) != 1 {
		t.Fatalf("compiled connected behavior chain counts=%d/%d/%d/%d", len(top), len(next), len(reached), len(topReturn))
	}
	if value, _ := reached[0].Param("n"); value != int64(9) {
		t.Fatalf("compiled connected behavior value=%#v", value)
	}
	if !result.Poset.IsCausallyBefore(top[0].ID, next[0].ID) ||
		!result.Poset.IsCausallyBefore(next[0].ID, reached[0].ID) ||
		!result.Poset.IsCausallyBefore(reached[0].ID, topReturn[0].ID) {
		t.Fatal("compiled connected behavior lost nested synchronous causality")
	}
}

func TestBehaviorFunctionDeclarationOrderIsNotSemantic(t *testing.T) {
	compile := func(functions string) string {
		t.Helper()
		source := []byte(`
type API is interface
  provides A : function(n : Integer) return Integer;
  provides B : function(n : Integer) return Integer;
  behavior ` + functions + ` begin
end interface API;
architecture M() is api : API; end architecture M;
`)
		model, err := Compile(source, "M")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	a := "A : function(n : Integer) return Integer is begin return n + 1; end function A;"
	b := "B : function(n : Integer) return Integer is begin return n * 2; end function B;"
	if left, right := compile(a+b), compile(b+a); left != right {
		t.Fatalf("behavior function declaration order changed model identity: %s != %s", left, right)
	}
}

func TestCompileRejectsMalformedBehaviorFunctions(t *testing.T) {
	tests := []struct {
		name, provides, behavior, want string
	}{
		{name: "not provided", provides: "provides F : function(n : Integer) return Integer;", behavior: "G : function(n : Integer) return Integer is begin return n; end function G;", want: `matches 0 provided interface signatures`},
		{name: "missing typed return", provides: "provides F : function(n : Integer) return Integer;", behavior: "F : function(n : Integer) return Integer is begin end function F;", want: `requires a final return expression`},
		{name: "void returns value", provides: "provides F : function(n : Integer);", behavior: "F : function(n : Integer) is begin return n; end function F;", want: `cannot return a value`},
		{name: "unknown expression name", provides: "provides F : function(n : Integer) return Integer;", behavior: "F : function(n : Integer) return Integer is begin return missing + 1; end function F;", want: `is not declared in this body`},
		{name: "unknown call", provides: "provides F : function(n : Integer);", behavior: "F : function(n : Integer) is begin Missing(n); end function F;", want: `is not a declared action or function`},
		{name: "in action call", provides: "action in Input(n : Integer); provides F : function(n : Integer);", behavior: "F : function(n : Integer) is begin Input(n); end function F;", want: `cannot generate in-action`},
		{name: "missing fallthrough after early return", provides: "action out Output(n : Integer); provides F : function(n : Integer) return Integer;", behavior: "F : function(n : Integer) return Integer is begin return n; Output(n); end function F;", want: `requires a final return expression`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type API is interface " + test.provides + " behavior " + test.behavior +
				" begin end interface API; architecture M() is api : API; end architecture M;")
			_, err := Compile(source, "M")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestCompileBehaviorStateAssignmentsAndDereferencesExecute(t *testing.T) {
	source := []byte(`
type Counter is interface
  action out Start(value : Integer);
  action out Seen(value : Integer);
  provides Store : function(next : Integer);
  behavior
    total : var Integer := 1 + 1;
    Store : function(next : Integer) is
      begin
        total := next;
      end function Store;
  begin
    (?N : Integer) Start(?N) =>
      Store(?N);
      Seen($total);
    ;
end interface Counter;
architecture M() is counter : Counter; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 10, arch.InputEvent{
		Key: "start", Source: "counter", Action: "Start", Params: map[string]any{"value": 7},
	})
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := model.ExecuteDeterministic(journal)
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
		t.Fatal("parsed behavior state execution did not replay byte-identically")
	}
	seen := first.Poset.ByName("Seen")
	storeCall := first.Poset.ByName("Store'Call")
	if len(seen) != 1 || len(storeCall) != 1 {
		t.Fatalf("state behavior event counts seen/store=%d/%d", len(seen), len(storeCall))
	}
	if value, _ := seen[0].Param("value"); value != int64(7) {
		t.Fatalf("state-backed output=%#v, want int64(7)", value)
	}
	if !first.Poset.IsCausallyBefore(storeCall[0].ID, seen[0].ID) {
		t.Fatal("state-backed output lost function/write/read causality")
	}
	if len(first.State) != 1 || first.State[0].ComponentID != "counter" ||
		first.State[0].Name != "total" || first.State[0].Version != 1 || first.State[0].Value.Text != "7" {
		t.Fatalf("compiled behavior final state=%#v", first.State)
	}
	if len(first.Firings) != 1 || len(first.Firings[0].StateWrites) != 1 || len(first.Firings[0].StateReads) != 1 ||
		first.Firings[0].StateWrites[0].ComponentID != "counter" || first.Firings[0].StateReads[0].ComponentID != "counter" {
		t.Fatalf("compiled behavior state audit=%#v", first.Firings)
	}
}

func TestBehaviorStateDeclarationOrderIsNotSemantic(t *testing.T) {
	compile := func(states string) string {
		t.Helper()
		source := []byte(`
type Counter is interface
  provides Total : function() return Integer;
  behavior ` + states + `
    Total : function() return Integer is begin return $a + $b; end function Total;
  begin
end interface Counter;
architecture M() is counter : Counter; end architecture M;
`)
		model, err := Compile(source, "M")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	a := "a : var Integer := 1;"
	b := "b : var Integer := 2;"
	if left, right := compile(a+b), compile(b+a); left != right {
		t.Fatalf("behavior state declaration order changed model identity: %s != %s", left, right)
	}
}

func TestCompileRejectsMalformedBehaviorState(t *testing.T) {
	tests := []struct {
		name, declarations, statement, want string
	}{
		{name: "missing initializer", declarations: "value : var Integer;", want: `requires an explicit initializer`},
		{name: "duplicate", declarations: "value : var Integer := 0; VALUE : var Integer := 1;", want: `duplicate behavior state`},
		{name: "initializer type mismatch", declarations: "value : var String := 0;", want: `initializer has type Integer, want String`},
		{name: "wrong initializer type", declarations: "value : var Integer := False;", want: `initializer has type Boolean, want Integer`},
		{name: "initializer division by zero", declarations: "value : var Integer := 1 / 0;", want: `division by zero`},
		{name: "initializer overflow", declarations: "value : var Integer := 9223372036854775807 + 1;", want: `integer overflow`},
		{name: "initializer state read", declarations: "other : var Integer := 0; value : var Integer := $other;", want: `behavior state $other is not declared`},
		{name: "unknown assignment", declarations: "value : var Integer := 0;", statement: "missing := 1;", want: `assignment targets undeclared state`},
		{name: "wrong assignment type", declarations: "value : var Integer := 0;", statement: "value := True;", want: `assignment to "value" has type Boolean, want Integer`},
		{name: "bare dereference", declarations: "value : var Integer := 0;", statement: "Output(value);", want: `must be dereferenced with '$'`},
		{name: "unknown dereference", declarations: "value : var Integer := 0;", statement: "Output($missing);", want: `state $missing is not declared`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type API is interface action out Input(); action out Output(value : Integer); behavior " +
				test.declarations + " begin Input => " + test.statement + "; end interface API; " +
				"architecture M() is api : API; end architecture M;")
			_, err := Compile(source, "M")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestCompileRejectsInvalidBehaviorFunctionAssignments(t *testing.T) {
	tests := []struct {
		name, target, call, want string
	}{
		{name: "action result", target: "integerResult", call: "Output(1)", want: `action "Output" cannot supply a function assignment result`},
		{name: "unknown function", target: "integerResult", call: "Missing(1)", want: `calls undeclared function "Missing"`},
		{name: "void function", target: "integerResult", call: "Void(1)", want: `matches 0 compatible typed signatures`},
		{name: "wrong return type", target: "integerResult", call: "Flag(1)", want: `matches 0 compatible typed signatures`},
		{name: "wrong argument type", target: "integerResult", call: "Number(True)", want: `matches 0 compatible typed signatures`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type API is interface
  action out Input();
  action out Output(value : Integer);
  provides
    Void : function(value : Integer);
    Flag : function(value : Integer) return Boolean;
    Number : function(value : Integer) return Integer;
  behavior
    integerResult : var Integer := 0;
  begin
    Input => ` + test.target + ` := ` + test.call + `;;
end interface API;
architecture M() is api : API; end architecture M;
`)
			_, err := Compile(source, "M")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestCompileBehaviorGuardUsesGenerationTimeState(t *testing.T) {
	source := []byte(`
type Gate is interface
  action out Start();
  action out Check();
  action out Accepted();
  action out Blocked();
  behavior
    enabled : var Boolean := True;
  begin
    Start =>
      enabled := False;
      Check();
    ;
    Check where $enabled => Accepted();;
    Check where not $enabled => Blocked();;
end interface Gate;
architecture M() is gate : Gate; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "start", Source: "gate", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Accepted")) != 0 || len(result.Poset.ByName("Blocked")) != 1 {
		t.Fatalf("state-guard outputs accepted/blocked=%d/%d", len(result.Poset.ByName("Accepted")), len(result.Poset.ByName("Blocked")))
	}
	if len(result.State) != 1 || result.State[0].Version != 1 || result.State[0].Value.Bool {
		t.Fatalf("state-guard final state=%#v", result.State)
	}
	if len(result.Firings) != 2 || len(result.Firings[0].StateWrites) != 1 ||
		len(result.Firings[1].StateReads) != 1 || result.Firings[1].StateReads[0].Version != 1 ||
		result.Firings[1].StateReads[0].Value.Bool {
		t.Fatalf("state-guard audit=%#v", result.Firings)
	}
}

func TestCompileBehaviorGuardComparisonsAndBooleanPrecedence(t *testing.T) {
	source := []byte(`
type Gate is interface
  action out Evaluate(value : Integer);
  action out Accepted(value : Integer);
  behavior
  begin
    (?N : Integer) Evaluate(?N) where ?N >= 10 and ?N < 20 => Accepted(?N);;
end interface Gate;
architecture M() is gate : Gate; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "five", Source: "gate", Action: "Evaluate", Params: map[string]any{"value": 5}},
		arch.InputEvent{Key: "ten", Source: "gate", Action: "Evaluate", Params: map[string]any{"value": 10}},
		arch.InputEvent{Key: "nineteen", Source: "gate", Action: "Evaluate", Params: map[string]any{"value": 19}},
		arch.InputEvent{Key: "twenty", Source: "gate", Action: "Evaluate", Params: map[string]any{"value": 20}},
	))
	if err != nil {
		t.Fatal(err)
	}
	accepted := result.Poset.ByName("Accepted")
	if len(accepted) != 2 {
		t.Fatalf("guard accepted %d events, want 2", len(accepted))
	}
	values := map[int64]bool{}
	for _, event := range accepted {
		value, _ := event.Param("value")
		values[value.(int64)] = true
	}
	if !values[10] || !values[19] || values[5] || values[20] {
		t.Fatalf("guard accepted values=%#v", values)
	}
}

func TestCompileRejectsMalformedBehaviorGuards(t *testing.T) {
	tests := []struct{ name, guard, want string }{
		{name: "non boolean", guard: "?N + 1", want: `guard has type Integer, want Boolean`},
		{name: "wrong comparison types", guard: "?N < True", want: `operator "<" is not defined for Integer and Boolean`},
		{name: "unknown placeholder", guard: "?Missing = 1", want: `name "Missing" is not declared`},
		{name: "unknown state", guard: "$missing", want: `state $missing is not declared`},
		{name: "bare state", guard: "enabled", want: `must be dereferenced with '$'`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Gate is interface
  action out Evaluate(value : Integer);
  action out Accepted();
  behavior enabled : var Boolean := True;
  begin
    (?N : Integer) Evaluate(?N) where ` + test.guard + ` => Accepted();;
end interface Gate;
architecture M() is gate : Gate; end architecture M;
`)
			_, err := Compile(source, "M")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestCompileBehaviorIfElsePreservesOrderedStateAndEvents(t *testing.T) {
	source := []byte(`
type Gate is interface
  action out Input(value : Integer);
  action out Accepted(value : Integer);
  action out Rejected(value : Integer);
  behavior
    last : var Integer := 0;
  begin
    (?N : Integer) Input(?N) =>
      if ?N >= 0 then
        last := ?N;
        Accepted($last);
      else
        Rejected(?N);
      end if;
    ;
end interface Gate;
architecture M() is gate : Gate; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "negative", Source: "gate", Action: "Input", Params: map[string]any{"value": -1}},
		arch.InputEvent{Key: "positive", Source: "gate", Action: "Input", Params: map[string]any{"value": 5}},
	))
	if err != nil {
		t.Fatal(err)
	}
	accepted, rejected := result.Poset.ByName("Accepted"), result.Poset.ByName("Rejected")
	if len(accepted) != 1 || len(rejected) != 1 {
		t.Fatalf("if/else outputs accepted/rejected=%d/%d", len(accepted), len(rejected))
	}
	acceptedValue, _ := accepted[0].Param("value")
	rejectedValue, _ := rejected[0].Param("value")
	if acceptedValue != int64(5) || rejectedValue != int64(-1) {
		t.Fatalf("if/else values accepted/rejected=%#v/%#v", acceptedValue, rejectedValue)
	}
	if len(result.State) != 1 || result.State[0].Value.Text != "5" || result.State[0].Version != 1 || result.StatementSteps != 5 {
		t.Fatalf("if/else state/steps=%#v/%d", result.State, result.StatementSteps)
	}
}

func TestCompileBehaviorLoopExitAndNextExecuteDeterministically(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Start(value : Integer);
  action out Tick(value : Integer);
  action out Done(value : Integer);
  behavior
    remaining : var Integer := 0;
  begin
    (?N : Integer) Start(?N) =>
      remaining := ?N;
      loop do
        remaining := $remaining - 1;
        next where $remaining = 1;
        Tick($remaining);
        exit where $remaining = 0;
      end do;
      Done($remaining);
    ;
end interface Worker;
architecture M() is worker : Worker; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournalWithLimits(digest,
		arch.ExecutionLimits{MaxFirings: 10, MaxStatements: 30},
		arch.InputEvent{Key: "start", Source: "worker", Action: "Start", Params: map[string]any{"value": 3}},
	)
	first, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := model.ExecuteDeterministic(journal)
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
		t.Fatal("parsed loop execution is not byte-identical")
	}
	ticks, done := first.Poset.ByName("Tick"), first.Poset.ByName("Done")
	if len(ticks) != 2 || len(done) != 1 {
		t.Fatalf("parsed loop tick/done counts=%d/%d", len(ticks), len(done))
	}
	firstEvent, secondEvent := ticks[0], ticks[1]
	if first.Poset.IsCausallyBefore(secondEvent.ID, firstEvent.ID) {
		firstEvent, secondEvent = secondEvent, firstEvent
	} else if !first.Poset.IsCausallyBefore(firstEvent.ID, secondEvent.ID) {
		t.Fatal("parsed loop ticks are not causally ordered")
	}
	firstTick, _ := firstEvent.Param("value")
	secondTick, _ := secondEvent.Param("value")
	doneValue, _ := done[0].Param("value")
	if firstTick != int64(2) || secondTick != int64(0) || doneValue != int64(0) {
		t.Fatalf("parsed loop values=%#v/%#v/%#v", firstTick, secondTick, doneValue)
	}
	if !first.Poset.IsCausallyBefore(secondEvent.ID, done[0].ID) {
		t.Fatal("parsed loop lost sequential process causality")
	}
	if len(first.State) != 1 || first.State[0].Value.Text != "0" || first.State[0].Version != 4 || first.StatementSteps != 13 {
		t.Fatalf("parsed loop state/steps=%#v/%d", first.State, first.StatementSteps)
	}
}

func TestCompileBehaviorWhileUsesTopTestedRapideSemantics(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Start(); action out Tick(value : Integer); action out Done();
  behavior remaining : var Integer := 0;
  begin
    Start =>
      remaining := 2;
      while $remaining > 0 do
        Tick($remaining);
        remaining := $remaining - 1;
      end while;
      Done();
    ;
end interface Worker;
architecture M() is worker : Worker; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournalWithLimits(digest,
		arch.ExecutionLimits{MaxFirings: 10, MaxStatements: 20},
		arch.InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	ticks, done := result.Poset.ByName("Tick"), result.Poset.ByName("Done")
	if len(ticks) != 2 || len(done) != 1 || result.StatementSteps != 10 {
		t.Fatalf("parsed while events/steps=%d/%d/%d", len(ticks), len(done), result.StatementSteps)
	}
	first, _ := ticks[0].Param("value")
	second, _ := ticks[1].Param("value")
	if !((first == int64(2) && second == int64(1)) || (first == int64(1) && second == int64(2))) {
		t.Fatalf("parsed while values=%#v/%#v", first, second)
	}
	var two, one gorapide.EventID
	for _, tick := range ticks {
		value, _ := tick.Param("value")
		if value == int64(2) {
			two = tick.ID
		} else if value == int64(1) {
			one = tick.ID
		}
	}
	if !result.Poset.IsCausallyBefore(two, one) {
		t.Fatal("top-tested while iterations lost source process order")
	}
}

func TestCompileBehaviorFunctionEarlyReturnStopsNestedBody(t *testing.T) {
	source := []byte(`
type API is interface
  action out Start(value : Integer); action out Result(value : Integer);
  provides Abs : function(value : Integer) return Integer;
  behavior
    result : var Integer := 0;
    Abs : function(value : Integer) return Integer is
      begin
        if value < 0 then
          return -value;
        end if;
        return value;
      end function Abs;
  begin
    (?N : Integer) Start(?N) => result := Abs(?N); Result($result);;
end interface API;
architecture M() is api : API; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "start", Source: "api", Action: "Start", Params: map[string]any{"value": -5}},
	))
	if err != nil {
		t.Fatal(err)
	}
	returned, output := result.Poset.ByName("Abs'Return"), result.Poset.ByName("Result")
	if len(returned) != 1 || len(output) != 1 {
		t.Fatalf("early return/output counts=%d/%d", len(returned), len(output))
	}
	returnValue, _ := returned[0].Param("Return")
	outputValue, _ := output[0].Param("value")
	if returnValue != int64(5) || outputValue != int64(5) {
		t.Fatalf("early return values=%#v/%#v", returnValue, outputValue)
	}
	if !result.Poset.IsCausallyBefore(returned[0].ID, output[0].ID) {
		t.Fatal("caller resumed before the parsed early return event")
	}
}

func TestCompileBehaviorCaseValueRangeAndDefaultSelection(t *testing.T) {
	source := []byte(`
type Selector is interface
  action out Choose(value : Integer); action out Selected(bucket : Integer);
  behavior current : var Integer := 0;
  begin
    (?N : Integer) Choose(?N) =>
      current := ?N;
      case $current of
        1, 2 => Selected(10);
        xor 3 .. 5 => Selected(20);
        default => Selected(0);
      end case;
    ;
end interface Selector;
architecture M() is selector : Selector; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		input, want int64
	}{{input: 2, want: 10}, {input: 4, want: 20}, {input: 9, want: 0}} {
		result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
			arch.InputEvent{Key: "choose", Source: "selector", Action: "Choose", Params: map[string]any{"value": test.input}},
		))
		if err != nil {
			t.Fatal(err)
		}
		selected := result.Poset.ByName("Selected")
		if len(selected) != 1 {
			t.Fatalf("case input %d selected count=%d", test.input, len(selected))
		}
		bucket, _ := selected[0].Param("bucket")
		if bucket != test.want {
			t.Fatalf("case input %d selected=%#v, want %d", test.input, bucket, test.want)
		}
		if result.StatementSteps != 3 || len(result.Firings) != 1 || len(result.Firings[0].StateReads) != 1 {
			t.Fatalf("case selector was not evaluated once for input %d: steps=%d firing=%#v", test.input, result.StatementSteps, result.Firings)
		}
	}
}

func TestCompileBehaviorCaseOrAndElsePreserveAlternativeSemantics(t *testing.T) {
	compile := func(separator string) *arch.Architecture {
		t.Helper()
		source := []byte(`
type Selector is interface
  action out Choose(value : Integer); action out First(); action out Second(); action out Fallback();
  behavior begin
    (?N : Integer) Choose(?N) =>
      case ?N of
        1 .. 3 => First();
        ` + separator + ` 2 .. 4 => Second();
        default => Fallback();
      end case;
    ;
end interface Selector;
architecture M() is selector : Selector; end architecture M;
`)
		model, err := Compile(source, "M")
		if err != nil {
			t.Fatal(err)
		}
		return model
	}
	execute := func(model *arch.Architecture) *arch.ExecutionResult {
		t.Helper()
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
			arch.InputEvent{Key: "choose", Source: "selector", Action: "Choose", Params: map[string]any{"value": 2}},
		))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	orResult := execute(compile("or"))
	first, second := orResult.Poset.ByName("First"), orResult.Poset.ByName("Second")
	if len(first) != 1 || len(second) != 1 || len(orResult.Poset.ByName("Fallback")) != 0 {
		t.Fatalf("or case events first/second/default=%d/%d/%d", len(first), len(second), len(orResult.Poset.ByName("Fallback")))
	}
	if !orResult.Poset.IsCausallyBefore(first[0].ID, second[0].ID) {
		t.Fatal("or case alternatives did not execute in source order")
	}
	elseResult := execute(compile("else"))
	if len(elseResult.Poset.ByName("First")) != 1 || len(elseResult.Poset.ByName("Second")) != 0 || len(elseResult.Poset.ByName("Fallback")) != 0 {
		t.Fatalf("else case did not execute only its first eligible alternative")
	}
}

func TestCompileBehaviorCaseCanReturnFromFunction(t *testing.T) {
	source := []byte(`
type API is interface
  action out Start(value : Integer); action out Result(value : Integer);
  provides Sign : function(value : Integer) return Integer;
  behavior
    result : var Integer := 0;
    Sign : function(value : Integer) return Integer is
      begin
        case value of
          -5 .. -1 => return -1;
          xor 0 => return 0;
          default => null;
        end case;
        return 1;
      end function Sign;
  begin
    (?N : Integer) Start(?N) => result := Sign(?N); Result($result);;
end interface API;
architecture M() is api : API; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "start", Source: "api", Action: "Start", Params: map[string]any{"value": -3}},
	))
	if err != nil {
		t.Fatal(err)
	}
	output := result.Poset.ByName("Result")
	if len(output) != 1 {
		t.Fatalf("case-return output count=%d", len(output))
	}
	value, _ := output[0].Param("value")
	if value != int64(-1) {
		t.Fatalf("case-return output=%#v", value)
	}
}

func TestCompileRejectsMalformedBehaviorCase(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{name: "mixed separators", body: "case ?N of 1 => null; xor 2 => null; or 3 => null; end case;", want: "cannot mix"},
		{name: "choice type mismatch", body: "case ?N of True => null; end case;", want: "case choice has type Boolean, want Integer"},
		{name: "non Integer range", body: "case True of False .. True => null; end case;", want: "case ranges require an Integer selector"},
		{name: "type choice", body: "case ?N of Integer => null; end case;", want: "case type choices are outside the current source subset"},
		{name: "missing arrow", body: "case ?N of 1 null; end case;", want: "expected '=>'"},
		{name: "empty alternative", body: "case ?N of 1 => xor 2 => null; end case;", want: "expected case statement"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type API is interface action out Start(value : Integer); behavior begin (?N : Integer) Start(?N) => " + test.body + "; end interface API; architecture M() is api : API; end architecture M;")
			_, err := Compile(source, "M")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestCompileRejectsUnsupportedOrIllTypedBehaviorLoopControl(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{name: "non Boolean while", body: "while 1 do null; end do;", want: "behavior while condition has type Integer, want Boolean"},
		{name: "non Boolean exit", body: "loop do exit where 1; end do;", want: "behavior exit condition has type Integer, want Boolean"},
		{name: "exit outside do", body: "exit;", want: "outside a do statement"},
		{name: "next outside do", body: "next;", want: "outside a do statement"},
		{name: "return outside function", body: "return;", want: "behavior return is only allowed in a function body"},
		{name: "non-enclosing named exit", body: "loop do exit Outer; end do;", want: "names non-enclosing do \"outer\""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type API is interface action out Start(); behavior begin Start => " + test.body + "; end interface API; architecture M() is api : API; end architecture M;")
			_, err := Compile(source, "M")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestCompileRejectsNonBooleanBehaviorIf(t *testing.T) {
	source := []byte(`
type Gate is interface
  action out Input(value : Integer);
  action out Accepted();
  behavior
  begin
    (?N : Integer) Input(?N) =>
      if ?N + 1 then Accepted(); end if;
    ;
end interface Gate;
architecture M() is gate : Gate; end architecture M;
`)
	_, err := Compile(source, "M")
	if err == nil || !strings.Contains(err.Error(), "behavior if condition has type Integer, want Boolean") {
		t.Fatalf("got %v, want non-Boolean if diagnostic", err)
	}
}

func TestCompileBehaviorElsifAndEndifSelectOneBranch(t *testing.T) {
	source := []byte(`
type Classifier is interface
  action out Input(value : Integer);
  action out Negative(); action out Zero(); action out Positive();
  behavior begin
    (?N : Integer) Input(?N) =>
      if ?N < 0 then
        Negative();
      elsif ?N = 0 then
        Zero();
      else
        Positive();
      endif;
    ;
end interface Classifier;
architecture M() is classifier : Classifier; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		input int64
		want  string
	}{{input: -1, want: "Negative"}, {input: 0, want: "Zero"}, {input: 1, want: "Positive"}} {
		result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
			arch.InputEvent{Key: "input", Source: "classifier", Action: "Input", Params: map[string]any{"value": test.input}},
		))
		if err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"Negative", "Zero", "Positive"} {
			want := 0
			if action == test.want {
				want = 1
			}
			if got := len(result.Poset.ByName(action)); got != want {
				t.Fatalf("elsif input %d action %s count=%d, want %d", test.input, action, got, want)
			}
		}
	}
}

func TestCompileRejectsNonBooleanBehaviorElsif(t *testing.T) {
	source := []byte(`
type Classifier is interface
  action out Input(value : Integer); action out Done();
  behavior begin
    (?N : Integer) Input(?N) =>
      if False then null; elsif ?N + 1 then Done(); endif;
    ;
end interface Classifier;
architecture M() is classifier : Classifier; end architecture M;
`)
	_, err := Compile(source, "M")
	if err == nil || !strings.Contains(err.Error(), "behavior if condition has type Integer, want Boolean") {
		t.Fatalf("got %v, want non-Boolean elsif diagnostic", err)
	}
}

func TestCompileBehaviorAssertAndNullAreAuditable(t *testing.T) {
	source := []byte(`
type Checker is interface
  action out Check(value : Integer);
  action out Completed(value : Integer);
  behavior
    minimum : var Integer := 0;
  begin
    (?N : Integer) Check(?N) =>
      null;
      assert ?N >= $minimum;
      Completed(?N);
    ;
end interface Checker;
architecture M() is checker : Checker; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "negative", Source: "checker", Action: "Check", Params: map[string]any{"value": -1}},
	))
	if err != nil {
		t.Fatal(err)
	}
	inconsistent, completed := result.Poset.ByName("Inconsistent"), result.Poset.ByName("Completed")
	if len(inconsistent) != 1 || len(completed) != 1 || result.StatementSteps != 3 {
		t.Fatalf("assert/null events or steps inconsistent/completed/steps=%d/%d/%d", len(inconsistent), len(completed), result.StatementSteps)
	}
	if !result.Poset.IsCausallyBefore(inconsistent[0].ID, completed[0].ID) {
		t.Fatal("failed assertion did not become the sequential cause of the following statement")
	}
	if len(result.Firings) != 1 || len(result.Firings[0].StateReads) != 1 ||
		result.Firings[0].StateReads[0].Name != "minimum" ||
		len(result.Firings[0].Generated) != 2 {
		t.Fatalf("assert/null firing audit=%#v", result.Firings)
	}
}

func TestCompileRejectsNonBooleanBehaviorAssert(t *testing.T) {
	source := []byte(`
type Checker is interface
  action out Check(value : Integer);
  behavior begin
    (?N : Integer) Check(?N) => assert ?N + 1;;
end interface Checker;
architecture M() is checker : Checker; end architecture M;
`)
	_, err := Compile(source, "M")
	if err == nil || !strings.Contains(err.Error(), "behavior assertion has type Integer, want Boolean") {
		t.Fatalf("got %v, want non-Boolean assertion diagnostic", err)
	}
}

func TestCompileCompoundBehaviorPatternsPreserveCausalityAndConcurrency(t *testing.T) {
	source := []byte(`
type Flow is interface
  action out Seed(); action out A(); action out B(); action out Missing();
  action out Causal(); action out Immediate(); action out Concurrent();
  action out Distinct(); action out Together(); action out Either(); action out Equivalent();
  behavior begin
    Seed => A(); B();;
    (A -> B) => Causal();;
    (A |> B) => Immediate();;
    (A || B) => Concurrent();;
    (A ~ B) => Distinct();;
    (A and B) => Together();;
    (A or Missing) => Either();;
    (A <=> A) => Equivalent();;
end interface Flow;
architecture M() is flow : Flow; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "seed", Source: "flow", Action: "Seed"},
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Causal", "Immediate", "Distinct", "Together", "Either", "Equivalent"} {
		if count := len(result.Poset.ByName(name)); count != 1 {
			t.Fatalf("compound causal pattern output %s count=%d, want 1", name, count)
		}
	}
	if count := len(result.Poset.ByName("Concurrent")); count != 0 {
		t.Fatalf("causally ordered A/B incorrectly matched independence %d times", count)
	}

	independent, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "a", Source: "flow", Action: "A"},
		arch.InputEvent{Key: "b", Source: "flow", Action: "B"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if count := len(independent.Poset.ByName("Concurrent")); count != 1 {
		t.Fatalf("independent A/B matched independence %d times, want 1", count)
	}
	if len(independent.Poset.ByName("Causal")) != 0 || len(independent.Poset.ByName("Immediate")) != 0 {
		t.Fatal("independent A/B incorrectly matched causal succession")
	}
}

func TestCompileCompoundBehaviorPatternUnifiesPlaceholders(t *testing.T) {
	source := []byte(`
type Pairer is interface
  action out A(value : Integer); action out B(value : Integer); action out Paired(value : Integer);
  behavior begin
    (?N : Integer) (A(?N) ~ B(?N)) => Paired(?N);;
end interface Pairer;
architecture M() is pairer : Pairer; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "a-one", Source: "pairer", Action: "A", Params: map[string]any{"value": 1}},
		arch.InputEvent{Key: "b-two", Source: "pairer", Action: "B", Params: map[string]any{"value": 2}},
		arch.InputEvent{Key: "b-one", Source: "pairer", Action: "B", Params: map[string]any{"value": 1}},
	))
	if err != nil {
		t.Fatal(err)
	}
	paired := result.Poset.ByName("Paired")
	if len(paired) != 1 {
		t.Fatalf("compound placeholder pairing count=%d, want 1", len(paired))
	}
	if value, _ := paired[0].Param("value"); value != int64(1) {
		t.Fatalf("compound placeholder pairing value=%#v", value)
	}
	if len(result.Firings) != 1 || len(result.Firings[0].MatchedEvents) != 2 || len(result.Firings[0].Bindings) != 1 {
		t.Fatalf("compound placeholder firing audit=%#v", result.Firings)
	}
}

func TestCompileRejectsAmbiguousOrMalformedCompoundBehaviorPatterns(t *testing.T) {
	tests := []struct{ name, pattern, want string }{
		{name: "unparenthesized chain", pattern: "A -> B -> C", want: `chained behavior pattern operators require explicit parentheses`},
		{name: "unknown operand", pattern: "A -> Missing", want: `trigger action "Missing" is not declared`},
		{name: "wrong operand arity", pattern: "A(?N) ~ B", want: `pattern action "A" has 0 parameters but supplies positional association 1`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Flow is interface
  action out A(); action out B(); action out C(); action out Output();
  behavior begin
    (?N : Integer) ` + test.pattern + ` => Output();;
end interface Flow;
architecture M() is flow : Flow; end architecture M;
`)
			_, err := Compile(source, "M")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestCompoundBehaviorPatternCommutativityIsCanonical(t *testing.T) {
	compile := func(trigger string) string {
		t.Helper()
		source := []byte(`
type Flow is interface
  action out A(); action out B(); action out Output();
  behavior begin (` + trigger + `) => Output();; end interface Flow;
architecture M() is flow : Flow; end architecture M;
`)
		model, err := Compile(source, "M")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	for _, operator := range []string{"||", "~", "and", "or", "<=>"} {
		if left, right := compile("A "+operator+" B"), compile("B "+operator+" A"); left != right {
			t.Fatalf("commutative pattern %s changed model identity: %s != %s", operator, left, right)
		}
	}
	if left, right := compile("A -> B"), compile("B -> A"); left == right {
		t.Fatal("directional causal pattern lost operand order")
	}
}

func TestCompileBehaviorIterationSelectsFixedDisjointMatch(t *testing.T) {
	source := []byte(`
type Batch is interface
  action out Item(); action out BatchReady();
  behavior begin [2 rel ~] Item => BatchReady();; end interface Batch;
architecture M() is batch : Batch; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "one", Source: "batch", Action: "Item"},
		arch.InputEvent{Key: "two", Source: "batch", Action: "Item"},
		arch.InputEvent{Key: "three", Source: "batch", Action: "Item"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("BatchReady")) != 1 || len(result.Firings) != 1 || len(result.Firings[0].MatchedEvents) != 2 {
		t.Fatalf("fixed disjoint iteration firing=%#v", result.Firings)
	}
}

func TestCompileBehaviorIterationSupportsStarPlusAndSharedBindings(t *testing.T) {
	source := []byte(`
type Batch is interface
  action out Seed(); action out Tick(); action out Value(n : Integer);
  action out Chain(); action out Pair(n : Integer);
  behavior begin
    Seed => Tick(); Tick(); Tick();;
    [+ rel ->] Tick => Chain();;
    (?N : Integer) [2 rel ~] Value(?N) => Pair(?N);;
end interface Batch;
architecture M() is batch : Batch; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "seed", Source: "batch", Action: "Seed"},
		arch.InputEvent{Key: "one-a", Source: "batch", Action: "Value", Params: map[string]any{"n": 1}},
		arch.InputEvent{Key: "two", Source: "batch", Action: "Value", Params: map[string]any{"n": 2}},
		arch.InputEvent{Key: "one-b", Source: "batch", Action: "Value", Params: map[string]any{"n": 1}},
	))
	if err != nil {
		t.Fatal(err)
	}
	chain, pair := result.Poset.ByName("Chain"), result.Poset.ByName("Pair")
	if len(chain) != 3 || len(pair) != 1 {
		t.Fatalf("iteration outputs chain/pair=%d/%d, want 3/1", len(chain), len(pair))
	}
	if value, _ := pair[0].Param("n"); value != int64(1) {
		t.Fatalf("iteration shared binding value=%#v", value)
	}
	singleTicks, foundTwo := 0, false
	for _, firing := range result.Firings {
		if len(firing.MatchedEvents) == 1 && len(firing.Bindings) == 0 {
			singleTicks++
		}
		if len(firing.MatchedEvents) == 2 && len(firing.Bindings) == 1 {
			foundTwo = true
		}
	}
	if singleTicks != 4 || !foundTwo {
		// The four singleton firings are Seed plus the three earliest Tick
		// matches; behavior selection gives `first` priority over maximality.
		t.Fatalf("iteration first-selection/binding audit=%#v", result.Firings)
	}

	starSource := []byte(`
type EmptyBatch is interface action out Item(); behavior begin [* rel ~] Item => ; end interface EmptyBatch;
architecture E() is batch : EmptyBatch; end architecture E;
`)
	if _, err := Compile(starSource, "E"); err != nil {
		t.Fatalf("zero-or-more source iteration did not compile: %v", err)
	}
}

func TestCompileRejectsMalformedBehaviorIteration(t *testing.T) {
	tests := []struct{ pattern, want string }{
		{pattern: "[-1 rel ~] Item", want: `expected iteration cardinality`},
		{pattern: "[* ~] Item", want: `expected 'rel'`},
		{pattern: "[* rel =>] Item", want: `expected iteration relation`},
		{pattern: "[* rel ~ Item", want: `expected ']'`},
	}
	for _, test := range tests {
		source := []byte(`
type Batch is interface action out Item(); behavior begin ` + test.pattern + ` => ; end interface Batch;
architecture M() is batch : Batch; end architecture M;
`)
		_, err := Compile(source, "M")
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("pattern %q got %v, want error containing %q", test.pattern, err, test.want)
		}
	}
}

func TestCompileArchitectureConstraintsProduceCanonicalAudit(t *testing.T) {
	source := []byte(`
type Flow is interface
  action out Start(); action out Done(); action out Error(code : Integer);
  behavior begin Start => Done();; end interface Flow;
architecture M() is flow : Flow;
constraint
  match (flow.Start -> flow.Done);
  never (?Code : Integer) flow.Error(?Code);
end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	cleanJournal := arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "start", Source: "flow", Action: "Start"},
	)
	first, err := model.ExecuteDeterministic(cleanJournal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := model.ExecuteDeterministic(cleanJournal)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("parsed constraint report did not replay byte-identically")
	}
	if first.Constraints == nil || !first.Constraints.Passed || len(first.Constraints.Reports) != 2 {
		t.Fatalf("clean parsed constraint report=%#v", first.Constraints)
	}

	violating, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "start", Source: "flow", Action: "Start"},
		arch.InputEvent{Key: "error", Source: "flow", Action: "Error", Params: map[string]any{"code": 7}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if violating.Constraints == nil || violating.Constraints.Passed {
		t.Fatalf("violating parsed constraint report=%#v", violating.Constraints)
	}
	violations := 0
	for _, report := range violating.Constraints.Reports {
		for _, violation := range report.Violations {
			violations++
			if violation.Kind != "MustNever" || len(violation.Events) != 1 || len(violation.Bindings) != 1 {
				t.Fatalf("parsed never violation=%#v", violation)
			}
		}
	}
	if violations != 1 {
		t.Fatalf("parsed constraint violation count=%d, want 1", violations)
	}
}

func TestParsedMatchConstraintRequiresWholeAssociatedComputation(t *testing.T) {
	source := []byte(`
type Flow is interface action out Start(); action out Done();
  behavior begin Start => Done();; end interface Flow;
architecture M() is flow : Flow;
constraint match (flow.Start -> flow.Done);
end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "start", Source: "flow", Action: "Start"},
		arch.InputEvent{Key: "extra-done", Source: "flow", Action: "Done"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || result.Constraints.Passed || len(result.Constraints.Reports) != 1 ||
		len(result.Constraints.Reports[0].Violations) != 1 || result.Constraints.Reports[0].Violations[0].Kind != "MustMatch" {
		t.Fatalf("whole-computation source match report=%#v", result.Constraints)
	}
}

func TestArchitectureConstraintDeclarationOrderIsNotSemantic(t *testing.T) {
	compile := func(clauses string) string {
		t.Helper()
		source := []byte(`
type Flow is interface action out Start(); action out Done(); action out Error(); end interface Flow;
architecture M() is flow : Flow; constraint ` + clauses + ` end architecture M;
`)
		model, err := Compile(source, "M")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	match := "match (flow.Start -> flow.Done);"
	never := "never flow.Error;"
	if left, right := compile(match+never), compile(never+match); left != right {
		t.Fatalf("constraint declaration order changed model identity: %s != %s", left, right)
	}
}

func TestCompileRejectsMalformedArchitectureConstraints(t *testing.T) {
	tests := []struct{ clause, want string }{
		{clause: "forbid flow.Error;", want: `expected constraint kind 'match' or 'never'`},
		{clause: "never Error;", want: `architecture constraint action .Error is not declared`},
		{clause: "never missing.Error;", want: `constraint component "missing" is not declared`},
		{clause: "never flow.Missing;", want: `constraint action flow.Missing is not declared`},
		{clause: "never (?N : Duration) flow.Error(?N);", want: `unsupported type "Duration"`},
		{clause: "never (?N : Integer) flow.Error;", want: `placeholder ?N is never bound`},
		{clause: "never flow.Error; never flow.Error;", want: `duplicate architecture constraint`},
	}
	for _, test := range tests {
		source := []byte(`
type Flow is interface action out Error(value : Integer); end interface Flow;
architecture M() is flow : Flow; constraint ` + test.clause + ` end architecture M;
`)
		_, err := Compile(source, "M")
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("clause %q got %v, want error containing %q", test.clause, err, test.want)
		}
	}
}

func TestCompileInterfaceConstraintsApplyPerComponentInstance(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Start(); action out Done(); action out Error(code : Integer);
  constraint
    match (Start -> Done);
    never (?Code : Integer) Error(?Code);
  behavior begin Start => Done();; end interface Worker;
architecture M() is a : Worker; b : Worker; end architecture M;
`)
	model, err := Compile(source, "M")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	clean, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "a-start", Source: "a", Action: "Start"},
		arch.InputEvent{Key: "b-start", Source: "b", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if clean.Constraints != nil || len(clean.ModuleConstraints) != 2 {
		t.Fatalf("per-instance clean architecture/module reports=%#v/%#v", clean.Constraints, clean.ModuleConstraints)
	}
	for _, record := range clean.ModuleConstraints {
		if !record.Report.Passed || len(record.Report.Reports) != 2 {
			t.Fatalf("component %s clean constraint report=%#v", record.ComponentID, record.Report)
		}
	}

	violating, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "a-start", Source: "a", Action: "Start"},
		arch.InputEvent{Key: "a-error", Source: "a", Action: "Error", Params: map[string]any{"code": 9}},
		arch.InputEvent{Key: "b-start", Source: "b", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	violations := 0
	for _, record := range violating.ModuleConstraints {
		for _, report := range record.Report.Reports {
			for _, violation := range report.Violations {
				violations++
				if record.ComponentID != "a" || violation.Kind != "MustNever" || len(violation.Events) != 1 || len(violation.Bindings) != 1 {
					t.Fatalf("per-instance violation component=%s value=%#v", record.ComponentID, violation)
				}
				event, ok := violating.Poset.Get(gorapide.EventID(violation.Events[0]))
				if !ok || event.Source != "a" {
					t.Fatalf("interface constraint escaped component projection: %#v", event)
				}
			}
		}
	}
	if violations != 1 || len(violating.ModuleConstraints) != 2 ||
		violating.ModuleConstraints[0].Report.Passed || !violating.ModuleConstraints[1].Report.Passed {
		t.Fatalf("per-instance violating reports=%#v", violating.ModuleConstraints)
	}
}

func TestInterfaceConstraintDeclarationOrderIsNotSemantic(t *testing.T) {
	compile := func(clauses string) string {
		t.Helper()
		source := []byte(`
type Worker is interface action out Start(); action out Done(); action out Error();
  constraint ` + clauses + ` end interface Worker;
architecture M() is worker : Worker; end architecture M;
`)
		model, err := Compile(source, "M")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	match := "match (Start -> Done);"
	never := "never Error;"
	if left, right := compile(match+never), compile(never+match); left != right {
		t.Fatalf("interface constraint order changed model identity: %s != %s", left, right)
	}
}

func TestCompileRejectsMalformedInterfaceConstraints(t *testing.T) {
	tests := []struct{ clause, want string }{
		{clause: "constraint never worker.Error;", want: `cannot be component-qualified`},
		{clause: "constraint never Missing;", want: `interface constraint action "Missing" is not declared`},
		{clause: "constraint never (?N : Duration) Error(?N);", want: `unsupported type "Duration"`},
		{clause: "constraint never (?N : Integer) Error;", want: `placeholder ?N is never bound`},
		{clause: "constraint never Error; never Error;", want: `duplicate interface constraint`},
	}
	for _, test := range tests {
		source := []byte(`
type Worker is interface action out Error(value : Integer); ` + test.clause + ` end interface Worker;
architecture M() is worker : Worker; end architecture M;
`)
		_, err := Compile(source, "M")
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("clause %q got %v, want error containing %q", test.clause, err, test.want)
		}
	}
}

func TestBehaviorRuleDeclarationOrderIsNotSemantic(t *testing.T) {
	compile := func(rules string) string {
		t.Helper()
		source := []byte(`
type API is interface
  action out Input();
  action out A();
  action out B();
  behavior begin ` + rules + ` end interface API;
architecture M() is api : API; end architecture M;
`)
		model, err := Compile(source, "M")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	pipe := "Input => A();;"
	agent := "Input ||> B();;"
	if left, right := compile(pipe+agent), compile(agent+pipe); left != right {
		t.Fatalf("behavior rule declaration order changed model identity: %s != %s", left, right)
	}
}

func TestCompileRejectsMalformedBehaviorRules(t *testing.T) {
	tests := []struct {
		name, actions, rule, want string
	}{
		{name: "unknown trigger", actions: "action out Output();", rule: "Missing => Output();;", want: `trigger action "Missing" is not declared`},
		{name: "undeclared trigger placeholder", actions: "action out Input(n : Integer);", rule: "Input(?N) => ;", want: `placeholder ?N is not declared`},
		{name: "unbound declaration", actions: "action out Input();", rule: "(?N : Integer) Input => ;", want: `placeholder ?N is never bound`},
		{name: "wrong trigger arity", actions: "action out Input(a : Integer);", rule: "(?N : Integer) Input(?N, ?N) => ;", want: `supplies positional association 2`},
		{name: "wrong trigger type", actions: "action out Input(flag : Boolean);", rule: "(?N : Integer) Input(?N) => ;", want: `has type Integer but action parameter flag has type Boolean`},
		{name: "placeholder marker required in body", actions: "action out Input(n : Integer); action out Output(n : Integer);", rule: "(?N : Integer) Input(?N) => Output(N);;", want: `placeholder "N" must be referenced with '?'`},
		{name: "duplicate rule", actions: "action out Input();", rule: "Input => ; Input => ;", want: `duplicate behavior rule`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type API is interface " + test.actions + " behavior begin " + test.rule +
				" end interface API; architecture M() is api : API; end architecture M;")
			_, err := Compile(source, "M")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestCompiledFunctionConnectionsAreDeclarationOrderIndependent(t *testing.T) {
	compile := func(connections string) string {
		t.Helper()
		source := []byte(`
type Client is interface
  requires Write : function(request : Integer); Read : function(key : String) return Integer;
end interface Client;
type Server is interface
  provides Store : function(item : Integer); Fetch : function(name : String) return Integer;
end interface Server;
architecture M() is client : Client; server : Server; connect ` + connections + ` end architecture M;
`)
		model, err := Compile(source, "M")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	left := compile("client.Write to server.Store; client.Read to server.Fetch;")
	right := compile("client.Read to server.Fetch; client.Write to server.Store;")
	if left != right {
		t.Fatalf("function connection source order changed model identity: %s != %s", left, right)
	}
}

func TestCompileRejectsUnsupportedOrIllTypedFunctionConnections(t *testing.T) {
	tests := []struct {
		name, clientPart, serverPart, connection, want string
	}{
		{name: "pipe connector", clientPart: "requires F : function(v : Integer);", serverPart: "provides G : function(x : Integer);", connection: "client.F => server.G;", want: "current static subset requires 'to'"},
		{name: "pattern arguments", clientPart: "requires F : function(v : Integer);", serverPart: "provides G : function(x : Integer);", connection: "(?v : Integer) client.F(?v) to server.G(?v);", want: "do not accept pattern placeholders"},
		{name: "source is provided", clientPart: "provides F : function(v : Integer);", serverPart: "provides G : function(x : Integer);", connection: "client.F to server.G;", want: "is not a requires function"},
		{name: "target is required", clientPart: "requires F : function(v : Integer);", serverPart: "requires G : function(x : Integer);", connection: "client.F to server.G;", want: "is not a provides function"},
		{name: "incompatible result", clientPart: "requires F : function(v : Integer) return Integer;", serverPart: "provides G : function(x : Integer) return Boolean;", connection: "client.F to server.G;", want: "0 type-compatible provided signatures"},
		{name: "mixed constituents", clientPart: "action out F(v : Integer);", serverPart: "provides G : function(x : Integer);", connection: "client.F to server.G;", want: "function connection source client.F is not declared"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("type Client is interface " + test.clientPart + " end interface Client;" +
				"type Server is interface " + test.serverPart + " end interface Server;" +
				"architecture M() is client : Client; server : Server; connect " + test.connection + " end architecture M;")
			_, err := Compile(source, "M")
			var typeErr *TypeError
			if !errors.As(err, &typeErr) || !strings.Contains(typeErr.Message, test.want) {
				t.Fatalf("got %v, want TypeError containing %q", err, test.want)
			}
		})
	}
}
