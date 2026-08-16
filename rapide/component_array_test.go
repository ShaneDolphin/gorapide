package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

const finiteComponentArrayDeclarations = `
type Button is interface
  action out Move(value : Integer);
end interface Button;

type Sensor is interface
  action in Activate(value : Integer);
  action out Seen(value : Integer);
  behavior
    last : var Integer := 0;
  begin
    (?Value : Integer) Activate(?Value) =>
      last := ?Value;
      Seen($last);
    ;
end interface Sensor;
`

const generatedFiniteComponentArraySource = finiteComponentArrayDeclarations + `
architecture Lift_Panel() is
  Sensors : array[-1..1] of Sensor;
  Buttons : array[-1..1] of Button;
connect
  for I : Integer in -1..1 generate
    (?Value : Integer) Buttons[I].Move(?Value) to Sensors[I].Activate(?Value);
  end generate;
end architecture Lift_Panel;
`

func TestParseFiniteComponentArraysRetainsBracketSelection(t *testing.T) {
	file, err := Parse([]byte(generatedFiniteComponentArraySource))
	if err != nil {
		t.Fatal(err)
	}
	declaration := file.Architectures[0]
	if len(declaration.Components) != 2 {
		t.Fatalf("components=%d, want two array declarations", len(declaration.Components))
	}
	for _, component := range declaration.Components {
		if !component.IntegerArray || component.FirstIndex != -1 || component.LastIndex != 1 {
			t.Fatalf("component array AST=%#v", component)
		}
	}
	if len(declaration.ConnectionGenerators) != 1 ||
		len(declaration.ConnectionGenerators[0].Connections) != 1 {
		t.Fatalf("connection generator AST=%#v", declaration.ConnectionGenerators)
	}
	connection := declaration.ConnectionGenerators[0].Connections[0]
	if connection.Source.Component != "Buttons" || connection.Source.ComponentIndex == nil ||
		connection.Source.ComponentIndex.Kind != ExpressionName || connection.Source.ComponentIndex.Name != "I" {
		t.Fatalf("source component selection=%#v", connection.Source)
	}
	if connection.Target.Component != "Sensors" || connection.Target.ComponentIndex == nil ||
		connection.Target.ComponentIndex.Kind != ExpressionName || connection.Target.ComponentIndex.Name != "I" {
		t.Fatalf("target component selection=%#v", connection.Target)
	}
}

func TestParseNamedFiniteRangeComponentArrayRetainsDomain(t *testing.T) {
	file, err := Parse([]byte(`
type Count is range -1..1;
type Button is interface action out Move(); end interface Button;
architecture Lift_Panel() is
  Buttons : array[Count] of Button;
end architecture Lift_Panel;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.TypeAliases) != 1 {
		t.Fatalf("type declarations=%d, want one", len(file.TypeAliases))
	}
	domain := file.TypeAliases[0]
	if !domain.IntegerRange || domain.Name != "Count" || domain.FirstIndex != -1 || domain.LastIndex != 1 {
		t.Fatalf("finite range AST=%#v", domain)
	}
	component := file.Architectures[0].Components[0]
	if !component.IntegerArray || component.IndexType != "Count" {
		t.Fatalf("named component-array AST=%#v", component)
	}
}

func TestNamedFiniteRangeComponentArraysMatchLiteralElaborationAndReplay(t *testing.T) {
	named := finiteComponentArrayDeclarations + `
type Count is range -1..1;
architecture Lift_Panel() is
  Sensors : array[count] of Sensor;
  Buttons : array[COUNT] of Button;
connect
  for I : cOuNt in -1..1 generate
    (?Value : Integer) Buttons[I].Move(?Value) to Sensors[I].Activate(?Value);
  end generate;
end architecture Lift_Panel;
`
	namedModel, err := Compile([]byte(named), "Lift_Panel")
	if err != nil {
		t.Fatal(err)
	}
	literalModel, err := Compile([]byte(generatedFiniteComponentArraySource), "Lift_Panel")
	if err != nil {
		t.Fatal(err)
	}
	namedDigest, _ := namedModel.DeterministicModelDigest()
	literalDigest, _ := literalModel.DeterministicModelDigest()
	if namedDigest != literalDigest {
		t.Fatalf("named range model digest=%s, literal=%s", namedDigest, literalDigest)
	}
	journal := arch.NewExecutionJournal(namedDigest, 30,
		arch.InputEvent{Key: "left", Source: "Buttons[-1]", Action: "Move", Params: map[string]any{"value": -8}},
		arch.InputEvent{Key: "right", Source: "Buttons[1]", Action: "Move", Params: map[string]any{"value": 13}},
	)
	namedResult, err := namedModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	literalResult, err := literalModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	namedBytes, _ := namedResult.MarshalCanonical()
	literalBytes, _ := literalResult.MarshalCanonical()
	if !bytes.Equal(namedBytes, literalBytes) {
		t.Fatal("named finite range changed canonical execution bytes")
	}
	artifactDigest, _ := namedResult.ArtifactDigest()
	replayed, err := namedModel.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(namedBytes, replayedBytes) {
		t.Fatal("named finite range replay changed canonical artifact bytes")
	}
}

func TestStanfordLiftPanelNamedRangeFormElaborates(t *testing.T) {
	model, err := Compile([]byte(`
type Count is range 1..50;
type Button is interface action out Move(); end interface Button;
type Sensor is interface action in Activate(); end interface Sensor;
architecture Lift_Panel() is
  Buttons : array[Count] of Button;
  Sensors : array[Count] of Sensor;
connect
  for I : Count in 1..50 generate
    Buttons[I].Move to Sensors[I].Activate;
  end generate;
end architecture Lift_Panel;
`), "Lift_Panel")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(model.Components()); got != 100 {
		t.Fatalf("Lift_Panel components=%d, want 100", got)
	}
	if _, ok := model.Component("Buttons[50]"); !ok {
		t.Fatal("Lift_Panel is missing Buttons[50]")
	}
	if _, ok := model.Component("Sensors[1]"); !ok {
		t.Fatal("Lift_Panel is missing Sensors[1]")
	}
}

func TestNamedFiniteRangeWorksInModuleAndResultSetGenerators(t *testing.T) {
	model, err := Compile([]byte(`
type Count is range 1..2;
type Driver is interface action out Send(); end interface Driver;
type Relay is interface action in Request(); action out Response(); end interface Relay;
type Sink is interface action in Receive(); end interface Sink;
module Relay_Module() return Relay is
connect
  Request to for I : Count in 1..1 generate Response end for;
end module Relay_Module;
architecture System() is
  driver : Driver;
  relay : Relay is Relay_Module();
  Sinks : array[Count] of Sink;
connect
  driver.Send to relay.Request;
  relay.Response to
    for I : Count in 1..2 generate Sinks[I].Receive end for;
end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "send", Source: "driver", Action: "Send"},
	))
	if err != nil {
		t.Fatal(err)
	}
	events := eventsWithoutArchitectureStart(result.Poset)
	if len(events) != 1 || !events[0].HasObservation("Sinks[1]", "Receive") ||
		!events[0].HasObservation("Sinks[2]", "Receive") {
		t.Fatalf("named module/result-set generator observations=%#v", events)
	}
}

func TestFiniteComponentArraysExecuteWithPerElementStateAndReplay(t *testing.T) {
	model, err := Compile([]byte(generatedFiniteComponentArraySource), "Lift_Panel")
	if err != nil {
		t.Fatal(err)
	}
	wantComponents := []string{
		"Buttons[-1]", "Buttons[0]", "Buttons[1]",
		"Sensors[-1]", "Sensors[0]", "Sensors[1]",
	}
	components := model.Components()
	if len(components) != len(wantComponents) {
		t.Fatalf("component count=%d, want %d", len(components), len(wantComponents))
	}
	for index, want := range wantComponents {
		if components[index].ID != want {
			t.Fatalf("component[%d]=%q, want %q", index, components[index].ID, want)
		}
	}
	if left, _ := model.Component("Sensors[-1]"); left == nil {
		t.Fatal("missing Sensors[-1]")
	} else if right, _ := model.Component("Sensors[1]"); left == right {
		t.Fatal("component-array elements share one module instance")
	}

	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 30,
		arch.InputEvent{Key: "negative", Source: "Buttons[-1]", Action: "Move", Params: map[string]any{"value": -7}},
		arch.InputEvent{Key: "positive", Source: "Buttons[1]", Action: "Move", Params: map[string]any{"value": 9}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	seen := result.Poset.ByName("Seen")
	seenSources := make(map[string]bool, len(seen))
	for _, event := range seen {
		seenSources[event.Source] = true
	}
	if len(seen) != 2 || !seenSources["Sensors[-1]"] || !seenSources["Sensors[1]"] {
		t.Fatalf("array element outputs=%#v", seen)
	}
	state := make(map[string]string, len(result.State))
	for _, record := range result.State {
		state[record.ComponentID] = record.Value.Text
	}
	if state["Sensors[-1]"] != "-7" || state["Sensors[0]"] != "0" || state["Sensors[1]"] != "9" {
		t.Fatalf("per-element final state=%#v", state)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := result.MarshalCanonical()
	second, _ := replayed.MarshalCanonical()
	if !bytes.Equal(first, second) {
		t.Fatal("finite component-array replay changed canonical artifact bytes")
	}
}

func TestArchitectureGeneratorArgumentsElaborateArraysConnectionsAndConstraints(t *testing.T) {
	source := finiteComponentArrayDeclarations + `
architecture Scaled(NumRMs : Integer; Enabled : Boolean) is
  Buttons : array[1..NumRMs] of Button;
  Sensors : array[1..NumRMs] of Sensor;
constraint
  never Sensors[NumRMs].Seen;
connect
  if Enabled generate
    for I : Integer in 1..NumRMs generate
      (?Value : Integer) Buttons[I].Move(?Value) to Sensors[I].Activate(?Value);
    end generate;
  end generate if;
end architecture Scaled;
`
	file, err := Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	declaration := file.Architectures[0]
	if len(declaration.Parameters) != 2 || declaration.Parameters[0].Name != "NumRMs" ||
		declaration.Parameters[1].Name != "Enabled" {
		t.Fatalf("architecture parameters=%#v", declaration.Parameters)
	}
	array := declaration.Components[0]
	if array.RangeFirst.Kind != ExpressionInteger || array.RangeFirst.Integer != 1 ||
		array.RangeLast.Kind != ExpressionName || array.RangeLast.Name != "NumRMs" {
		t.Fatalf("architecture-parameter array range=%#v..%#v", array.RangeFirst, array.RangeLast)
	}

	left, err := CompileWithArguments([]byte(source), "scaled", map[string]any{
		"ENABLED": true, "numrms": int32(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := CompileWithArguments([]byte(source), "SCALED", map[string]any{
		"NumRMs": int64(2), "Enabled": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("argument name case, map order, or Go integer width changed model identity: %s != %s", leftDigest, rightDigest)
	}
	if len(left.Components()) != 4 {
		t.Fatalf("parameterized component count=%d, want 4", len(left.Components()))
	}
	journal := arch.NewExecutionJournal(leftDigest, 30,
		arch.InputEvent{Key: "one", Source: "Buttons[1]", Action: "Move", Params: map[string]any{"value": 7}},
		arch.InputEvent{Key: "two", Source: "Buttons[2]", Action: "Move", Params: map[string]any{"value": 9}},
	)
	prior := runtime.GOMAXPROCS(1)
	first, err := left.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(8)
	second, secondErr := right.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if err != nil {
		t.Fatal(err)
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	firstBytes, _ := first.MarshalCanonical()
	secondBytes, _ := second.MarshalCanonical()
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("architecture-argument execution changed across map order, numeric width, case, or GOMAXPROCS")
	}
	if len(first.Poset.ByName("Seen")) != 2 || first.Constraints == nil || first.Constraints.Passed {
		t.Fatalf("parameterized outputs/constraint report=%d/%#v", len(first.Poset.ByName("Seen")), first.Constraints)
	}
	artifactDigest, _ := first.ArtifactDigest()
	replayed, err := left.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("architecture-argument replay changed canonical artifact bytes")
	}

	disabled, err := CompileWithArguments([]byte(source), "Scaled", map[string]any{
		"NumRMs": int64(2), "Enabled": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	disabledDigest, _ := disabled.DeterministicModelDigest()
	if disabledDigest == leftDigest {
		t.Fatal("an explicit architecture argument did not affect canonical model identity")
	}
	disabledResult, err := disabled.ExecuteDeterministic(arch.NewExecutionJournal(disabledDigest, 10,
		arch.InputEvent{Key: "one", Source: "Buttons[1]", Action: "Move", Params: map[string]any{"value": 7}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(disabledResult.Poset.ByName("Seen")) != 0 {
		t.Fatal("false architecture generator argument unexpectedly elaborated connections")
	}
}

func TestArchitectureGeneratorArgumentsRejectMissingExtraDuplicateAndIllTypedActuals(t *testing.T) {
	source := []byte(`
type Item is interface action out Ready(); end interface Item;
architecture Scaled(Count : Integer; Enabled : Boolean) is
  Items : array[1..Count] of Item;
end architecture Scaled;
`)
	tests := []struct {
		name      string
		arguments map[string]any
		want      string
	}{
		{name: "missing", arguments: map[string]any{"Count": 2}, want: `parameter "Enabled" requires an explicit Boolean argument`},
		{name: "extra", arguments: map[string]any{"Count": 2, "Enabled": true, "Other": 1}, want: `undeclared argument "Other"`},
		{name: "case duplicate", arguments: map[string]any{"Count": 2, "count": 2, "Enabled": true}, want: "differ only by case"},
		{name: "wrong type", arguments: map[string]any{"Count": true, "Enabled": true}, want: `argument "Count" does not match Integer`},
		{name: "noncanonical", arguments: map[string]any{"Count": make(chan int), "Enabled": true}, want: "not a canonical deterministic value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompileWithArguments(source, "Scaled", test.arguments)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
	if _, err := Compile(source, "Scaled"); err == nil || !strings.Contains(err.Error(), "requires an explicit Integer argument") {
		t.Fatalf("Compile without required generator actuals got %v", err)
	}
	if _, err := CompileWithArguments([]byte(`architecture Empty() is end architecture Empty;`), "Empty", map[string]any{"Extra": 1}); err == nil || !strings.Contains(err.Error(), `undeclared argument "Extra"`) {
		t.Fatalf("zero-parameter architecture extra actual got %v", err)
	}
}

func TestUnusedArchitectureGeneratorArgumentRemainsAuditableModelData(t *testing.T) {
	source := []byte(`architecture Tagged(Audit_Tag : Integer) is end architecture Tagged;`)
	first, err := CompileWithArguments(source, "Tagged", map[string]any{"Audit_Tag": int8(1)})
	if err != nil {
		t.Fatal(err)
	}
	same, err := CompileWithArguments(source, "tagged", map[string]any{"audit_tag": int64(1)})
	if err != nil {
		t.Fatal(err)
	}
	different, err := CompileWithArguments(source, "TAGGED", map[string]any{"AUDIT_TAG": int64(2)})
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, _ := first.DeterministicModelDigest()
	sameDigest, _ := same.DeterministicModelDigest()
	differentDigest, _ := different.DeterministicModelDigest()
	if firstDigest != sameDigest {
		t.Fatal("equivalent explicit architecture arguments changed model identity")
	}
	if firstDigest == differentDigest {
		t.Fatal("unused explicit architecture argument was omitted from canonical model identity")
	}
}

func TestStanfordParameterizedResourceArrayAndGeneratedConnectionShapeElaborates(t *testing.T) {
	source := []byte(`
type Resource_Manager is interface action out Ready(); end interface Resource_Manager;
type Transaction_Manager is interface action in Attach(); end interface Transaction_Manager;
architecture System(NumRMs : Integer) is
  TM : Transaction_Manager;
  RMs : array[1..NumRMs] of Resource_Manager;
connect
  for I : Integer in 1..NumRMs generate
    RMs[I].Ready to TM.Attach;
  end generate;
end architecture System;
`)
	model, err := CompileWithArguments(source, "System", map[string]any{"NumRMs": 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Components()) != 4 {
		t.Fatalf("Stanford parameterized resource shape components=%d, want 4", len(model.Components()))
	}
	for index := int64(1); index <= 3; index++ {
		if _, exists := model.Component(componentArrayElementSpelling("RMs", index)); !exists {
			t.Fatalf("missing RMs[%d]", index)
		}
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "ready", Source: "RMs[3]", Action: "Ready"},
	))
	if err != nil {
		t.Fatal(err)
	}
	attach := result.Poset.ByName("Attach")
	if len(attach) != 1 || attach[0].Source != "TM" {
		t.Fatalf("Stanford parameterized generated connection result=%#v", attach)
	}
}

func TestFiniteComponentArrayGeneratorMatchesExplicitUnrollingAndScheduling(t *testing.T) {
	explicit := finiteComponentArrayDeclarations + `
architecture Lift_Panel() is
  Buttons : array[-1..1] of Button;
  Sensors : array[-1..1] of Sensor;
connect
  (?Value : Integer) buttons[1].move(?Value) to sensors[1].activate(?Value);
  (?Value : Integer) buttons[0].move(?Value) to sensors[0].activate(?Value);
  (?Value : Integer) buttons[-1].move(?Value) to sensors[-1].activate(?Value);
end architecture Lift_Panel;
`
	generated, err := Compile([]byte(generatedFiniteComponentArraySource), "lift_panel")
	if err != nil {
		t.Fatal(err)
	}
	unrolled, err := Compile([]byte(explicit), "LIFT_PANEL")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, _ := generated.DeterministicModelDigest()
	unrolledDigest, _ := unrolled.DeterministicModelDigest()
	if generatedDigest != unrolledDigest {
		t.Fatalf("component-array generator digest=%s, explicit=%s", generatedDigest, unrolledDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 30,
		arch.InputEvent{Key: "negative", Source: "Buttons[-1]", Action: "Move", Params: map[string]any{"value": -1}},
		arch.InputEvent{Key: "zero", Source: "Buttons[0]", Action: "Move", Params: map[string]any{"value": 0}},
		arch.InputEvent{Key: "positive", Source: "Buttons[1]", Action: "Move", Params: map[string]any{"value": 1}},
	)
	prior := runtime.GOMAXPROCS(1)
	generatedResult, err := generated.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(8)
	unrolledResult, secondErr := unrolled.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(prior)
	if err != nil {
		t.Fatal(err)
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	generatedBytes, _ := generatedResult.MarshalCanonical()
	unrolledBytes, _ := unrolledResult.MarshalCanonical()
	if !bytes.Equal(generatedBytes, unrolledBytes) {
		t.Fatal("component declaration/reference order, case, or GOMAXPROCS changed array execution")
	}
}

func TestFiniteComponentArraySelectionsParticipateInArchitectureConstraints(t *testing.T) {
	source := finiteComponentArrayDeclarations + `
architecture Monitored() is
  Buttons : array[1..2] of Button;
  Sensors : array[1..2] of Sensor;
constraint
  never Sensors[1].Seen;
  never Sensors[2].Seen;
connect
  for I : Integer in 1..2 generate
    (?Value : Integer) Buttons[I].Move(?Value) to Sensors[I].Activate(?Value);
  end generate;
end architecture Monitored;
`
	model, err := Compile([]byte(source), "Monitored")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "one", Source: "Buttons[1]", Action: "Move", Params: map[string]any{"value": 4}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || result.Constraints.Passed || len(result.Constraints.Reports) != 2 {
		t.Fatalf("selected-component constraints=%#v", result.Constraints)
	}
	violations := 0
	for _, report := range result.Constraints.Reports {
		violations += len(report.Violations)
	}
	if violations != 1 {
		t.Fatalf("selected-component constraint violations=%d, want 1", violations)
	}
}

func TestFiniteComponentArrayIteratorSubstitutesEveryCompoundPatternLeaf(t *testing.T) {
	source := `
type Emitter is interface
  action out First(value : Integer);
  action out Second(value : Integer);
end interface Emitter;
type Receiver is interface action in Joined(value : Integer); end interface Receiver;
architecture Compound() is
  Emitters : array[1..2] of Emitter;
  receiver : Receiver;
connect
  for I : Integer in 1..2 generate
    (?Value : Integer) (Emitters[I].First(?Value) -> Emitters[I].Second(?Value))
      ||> receiver.Joined(?Value);
  end generate;
end architecture Compound;
`
	model, err := Compile([]byte(source), "Compound")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30,
		arch.InputEvent{Key: "first-1", Source: "Emitters[1]", Action: "First", Params: map[string]any{"value": 1}},
		arch.InputEvent{Key: "first-2", Source: "Emitters[2]", Action: "First", Params: map[string]any{"value": 2}},
		arch.InputEvent{Key: "second-1", Source: "Emitters[1]", Action: "Second", Params: map[string]any{"value": 1}, Causes: []string{"first-1"}},
		arch.InputEvent{Key: "second-2", Source: "Emitters[2]", Action: "Second", Params: map[string]any{"value": 2}, Causes: []string{"first-2"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	joined := result.Poset.ByName("Joined")
	if len(joined) != 2 {
		t.Fatalf("compound selected-component outputs=%d, want 2", len(joined))
	}
	for _, event := range joined {
		value, _ := event.Param("value")
		if value != int64(1) && value != int64(2) {
			t.Fatalf("compound selected-component value=%#v", value)
		}
	}
}

func TestFiniteComponentArrayServiceConnectionsKeepBracketAndParenthesisIndicesDistinct(t *testing.T) {
	source := `
type Protocol is interface
  action in Request(value : Integer);
  action out Reply(value : Integer);
end interface Protocol;
type Resource is interface service Port : Protocol; end interface Resource;
type Manager is interface service Resources(1..2) : dual Protocol; end interface Manager;
architecture System() is
  RMs : array[1..2] of Resource;
  TM : Manager;
connect
  for I : Integer in 1..2 generate
    RMs[I].Port to TM.Resources(I);
  end generate;
end architecture System;
`
	model, err := Compile([]byte(source), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := model.DeterministicModelDigest()
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "reply", Source: "RMs[1]", Action: "port.reply", Params: map[string]any{"value": 4}},
		arch.InputEvent{Key: "request", Source: "TM", Action: "resources(2).request", Params: map[string]any{"value": 8}},
	))
	if err != nil {
		t.Fatal(err)
	}
	replies := result.Poset.ByName("port.reply")
	if len(replies) != 1 || replies[0].Source != "RMs[1]" {
		t.Fatalf("component-array service reply=%#v", replies)
	}
	managerReplies := result.Poset.ByName("resources(1).reply")
	if len(managerReplies) != 1 || managerReplies[0].ID != replies[0].ID || managerReplies[0].Source != "TM" {
		t.Fatalf("indexed manager reply=%#v, source=%#v", managerReplies, replies)
	}
	managerRequests := result.Poset.ByName("resources(2).request")
	resourceRequests := result.Poset.ByName("port.request")
	if len(managerRequests) != 1 || len(resourceRequests) != 1 ||
		managerRequests[0].ID != resourceRequests[0].ID || resourceRequests[0].Source != "RMs[2]" {
		t.Fatalf("array/service-set request manager=%#v resource=%#v", managerRequests, resourceRequests)
	}
}

func TestFiniteComponentArraysRejectUnsupportedOrInvalidForms(t *testing.T) {
	base := finiteComponentArrayDeclarations
	tests := []struct {
		name         string
		architecture string
		want         string
	}{
		{
			name: "unbounded index type",
			architecture: `architecture Bad() is
  Buttons : array[Integer] of Button;
end architecture Bad;`,
			want: `index type "Integer" is not a declared closed Integer range`,
		},
		{
			name: "array denotation",
			architecture: `architecture Bad() is
  Buttons : array[1..2] of Button is Make_Buttons();
end architecture Bad;`,
			want: "component-array denotation expressions are outside",
		},
		{
			name: "cardinality bound",
			architecture: `architecture Bad() is
  Buttons : array[0..256] of Button;
end architecture Bad;`,
			want: "exceeds deterministic cardinality limit 256",
		},
		{
			name: "out of range selection",
			architecture: `architecture Bad() is
  Buttons : array[1..1] of Button;
  Sensors : array[1..1] of Sensor;
connect
  Buttons[1].Move to Sensors[2].Activate;
end architecture Bad;`,
			want: `connection target component "Sensors[2]" is not declared`,
		},
		{
			name: "duplicate array base",
			architecture: `architecture Bad() is
  Buttons : array[1..1] of Button;
  BUTTONS : array[2..2] of Button;
end architecture Bad;`,
			want: "duplicate component or component array",
		},
		{
			name: "named range cardinality bound",
			architecture: `type Count is range 0..256;
architecture Bad() is
  Buttons : array[Count] of Button;
end architecture Bad;`,
			want: "exceeds deterministic cardinality limit 256",
		},
		{
			name: "named generator outside declared range",
			architecture: `type Count is range 1..2;
architecture Bad() is
  Buttons : array[Count] of Button;
  Sensors : array[Count] of Sensor;
connect
  for I : Count in 0..2 generate
    Buttons[I].Move to Sensors[I].Activate;
  end generate;
end architecture Bad;`,
			want: "range 0..2 is outside declared type Count range 1..2",
		},
		{
			name: "finite range in arbitrary value slot remains gated",
			architecture: `type Count is range 1..2;
type Uses_Count is interface action out Seen(value : Count); end interface Uses_Count;
architecture Bad() is
  value : Uses_Count;
end architecture Bad;`,
			want: "finite Integer range type \"Count\" is currently supported only",
		},
		{
			name: "finite range alias remains gated",
			architecture: `type Count is range 1..2;
type Count_Alias is Count;
architecture Bad() is
  Buttons : array[Count_Alias] of Button;
end architecture Bad;`,
			want: "finite Integer range type \"Count\" is currently supported only",
		},
		{
			name: "module local finite range remains gated",
			architecture: `module Bad_Module() return Button is
  type Local_Count is range 1..2;
end module Bad_Module;
architecture Bad() is
  button : Button is Bad_Module();
end architecture Bad;`,
			want: "module-local finite range declarations are outside",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(base+test.architecture), "Bad")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}

	empty, err := Compile([]byte(base+`architecture Empty() is
  Buttons : array[3..1] of Button;
end architecture Empty;`), "Empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Components()) != 0 {
		t.Fatalf("descending component array created %d elements, want zero", len(empty.Components()))
	}

	namedEmpty, err := Compile([]byte(base+`
type Empty_Count is range 3..1;
architecture Empty() is
  Buttons : array[Empty_Count] of Button;
connect
  for I : Empty_Count in 3..1 generate
    Buttons[I].Move to Buttons[I].Move;
  end generate;
end architecture Empty;`), "Empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(namedEmpty.Components()) != 0 {
		t.Fatalf("descending named component array created %d elements, want zero", len(namedEmpty.Components()))
	}
}
