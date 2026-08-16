package rapide

import (
	"bytes"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

const moduleConnectionSource = `
type Driver is interface action out Send(n : Integer); end interface Driver;
type Relay is interface
  action in Request(request : Integer);
  action out Response(value : Integer);
  action out Handled(value : Integer);
end interface Relay;
type Sink is interface action in Delivered(value : Integer); end interface Sink;

module RelayModule() return Relay is
connect
  (?N : Integer) Request(?N) to Response(?N);
parallel
  when (?N : Integer) Response(?N) do Handled(?N); end when;
end module RelayModule;

architecture System() is
  driver : Driver;
  relay : Relay is RelayModule();
  sink : Sink;
connect
  (?N : Integer) driver.Send(?N) => relay.Request(?N);
  (?N : Integer) relay.Response(?N) to sink.Delivered(?N);
end architecture System;
`

func TestCompileModuleBasicConnectionPreservesIdentityCausalityAndReplay(t *testing.T) {
	model, err := Compile([]byte(moduleConnectionSource), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 20, arch.InputEvent{
		Key: "send", Source: "driver", Action: "Send", Params: map[string]any{"n": 9},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	request := result.Poset.ByName("Request")
	response := result.Poset.ByName("Response")
	delivered := result.Poset.ByName("Delivered")
	handled := result.Poset.ByName("Handled")
	if len(request) != 1 || len(response) != 1 || len(delivered) != 1 || len(handled) != 1 {
		t.Fatalf("module connection events request=%d response=%d delivered=%d handled=%d",
			len(request), len(response), len(delivered), len(handled))
	}
	if request[0].ID != response[0].ID || response[0].ID != delivered[0].ID {
		t.Fatal("module or downstream basic connection changed occurrence identity")
	}
	for _, event := range []*struct {
		name  string
		value any
	}{{"Request", request[0].ParamInt("request")}, {"Response", response[0].ParamInt("value")}, {"Delivered", delivered[0].ParamInt("value")}} {
		if event.value != 9 {
			t.Fatalf("%s value=%#v, want 9", event.name, event.value)
		}
	}
	if !result.Poset.IsCausallyBefore(request[0].ID, handled[0].ID) {
		t.Fatal("module process output does not depend on the locally connected occurrence")
	}
	var scopes []string
	for _, firing := range result.Firings {
		if firing.Transition == "connection" {
			scopes = append(scopes, firing.ConnectionScope)
		}
	}
	if strings.Join(scopes, ",") != "architecture,module,architecture" {
		t.Fatalf("connection scope audit=%v", scopes)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("source module connection replay was not byte-identical")
	}
}

func TestModuleConnectionClosesDuringInitialStartupBeforeProcessEntry(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Boot(); action out Ready(); action out Handled();
end interface Worker;
module M() return Worker is
connect
  Boot to Ready;
initial
  Boot();
parallel
  when Ready do Handled(); end when;
end module M;
architecture System() is worker : Worker is M(); end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10))
	if err != nil {
		t.Fatal(err)
	}
	boot, ready, handled := result.Poset.ByName("Boot"), result.Poset.ByName("Ready"), result.Poset.ByName("Handled")
	if len(boot) != 1 || len(ready) != 1 || len(handled) != 1 || boot[0].ID != ready[0].ID {
		t.Fatalf("startup module connection boot=%#v ready=%#v handled=%#v", boot, ready, handled)
	}
	if !result.Poset.IsCausallyBefore(ready[0].ID, handled[0].ID) {
		t.Fatal("process did not inherit the closed module-connection startup occurrence")
	}
	if len(result.Firings) != 3 || result.Firings[0].Transition != "initial" ||
		result.Firings[1].ConnectionScope != arch.ModuleConnectionScope.String() ||
		result.Firings[2].Transition != "process" {
		t.Fatalf("startup module connection audit=%#v", result.Firings)
	}
}

func TestModuleConnectionGuardUsesClosedMatchBinding(t *testing.T) {
	source := []byte(`
type Driver is interface action out Send(n : Integer); end interface Driver;
type Relay is interface action in Request(n : Integer); action out Response(n : Integer); end interface Relay;
module M() return Relay is connect
  (?N : Integer) Request(?N) where ?N > 0 to Response(?N);
end module M;
architecture System() is driver : Driver; relay : Relay is M(); connect
  driver.Send => relay.Request;
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "pass", Source: "driver", Action: "Send", Params: map[string]any{"n": 3}},
		arch.InputEvent{Key: "block", Source: "driver", Action: "Send", Params: map[string]any{"n": -1}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	responses := result.Poset.ByName("Response")
	if len(responses) != 1 || responses[0].ParamInt("n") != 3 {
		t.Fatalf("guarded module connection responses=%#v", responses)
	}
	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayArtifact, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, replayArtifact) {
		t.Fatal("guarded module connection replay was not byte-identical")
	}
}

func TestModuleConnectionsInstantiateIndependentlyPerGeneratorCall(t *testing.T) {
	source := []byte(`
type Driver is interface action out SendA(n : Integer); action out SendB(n : Integer); end interface Driver;
type Relay is interface action in Request(n : Integer); action out Response(n : Integer); end interface Relay;
module RelayModule() return Relay is
connect
  Request to Response;
end module RelayModule;
architecture System() is
  driver : Driver;
  a : Relay is RelayModule();
  b : Relay is RelayModule();
connect
  driver.SendA to a.Request;
  driver.SendB to b.Request;
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "a", Source: "driver", Action: "SendA", Params: map[string]any{"n": 1}},
		arch.InputEvent{Key: "b", Source: "driver", Action: "SendB", Params: map[string]any{"n": 2}},
	))
	if err != nil {
		t.Fatal(err)
	}
	responses := result.Poset.ByName("Response")
	if len(responses) != 2 || responses[0].Source == responses[1].Source {
		t.Fatalf("per-instance module responses=%#v", responses)
	}
	values := map[string]int{}
	for _, event := range responses {
		values[event.Source] = event.ParamInt("n")
	}
	if values["a"] != 1 || values["b"] != 2 {
		t.Fatalf("per-instance module connection values=%v", values)
	}
	if !result.Poset.IsCausallyIndependent(responses[0].ID, responses[1].ID) {
		t.Fatal("independent module-connection instances acquired a traversal-order causal edge")
	}
	moduleIDs := map[string]bool{}
	for _, firing := range result.Firings {
		if firing.ConnectionScope == arch.ModuleConnectionScope.String() {
			moduleIDs[firing.ConnectionID] = true
		}
	}
	if len(moduleIDs) != 2 {
		t.Fatalf("module connection instance identities=%v", moduleIDs)
	}
}

func TestModuleConnectionDeclarationOrderIsNotSemantic(t *testing.T) {
	compile := func(connections string) string {
		t.Helper()
		source := []byte(`
type Relay is interface
  action in A(n : Integer); action in B(n : Integer);
  action out X(n : Integer); action out Y(n : Integer);
end interface Relay;
module M() return Relay is connect ` + connections + ` end module M;
architecture System() is relay : Relay is M(); end architecture System;
`)
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	first := compile("A to X; B to Y;")
	second := compile("B to Y; A to X;")
	if first != second {
		t.Fatal("module connection declaration order changed canonical model identity")
	}
	listed := compile("A, B to X, Y;")
	expanded := compile("B to Y; A to X; B to X; A to Y;")
	if listed != expanded {
		t.Fatal("module connection-list shorthand changed canonical model identity")
	}
}

func TestModuleClosedIfConnectionGeneratorIsCanonicalElaboration(t *testing.T) {
	declarations := `
type Driver is interface
  action out Send(value : Integer);
end interface Driver;
type Loopback is interface
  action in Input(value : Integer);
  action out Output(value : Integer);
end interface Loopback;
`
	generatedSource := []byte(declarations + `
module LoopbackModule() return Loopback is
connect
  if 4 >= 2 generate
    (?Value : Integer) Input(?Value) to Output(?Value);
  end generate;
end module LoopbackModule;
architecture System() is driver : Driver; worker : Loopback is LoopbackModule(); connect
  (?Value : Integer) driver.Send(?Value) => worker.Input(?Value);
end architecture System;
`)
	explicitSource := []byte(declarations + `
module LoopbackModule() return Loopback is
connect
  (?Value : Integer) Input(?Value) to Output(?Value);
end module LoopbackModule;
architecture System() is worker : Loopback is LoopbackModule(); driver : Driver; connect
  (?Value : Integer) driver.Send(?Value) => worker.Input(?Value);
end architecture System;
`)
	generated, err := Compile(generatedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(explicitSource, "system")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, _ := generated.DeterministicModelDigest()
	explicitDigest, _ := explicit.DeterministicModelDigest()
	if generatedDigest != explicitDigest {
		t.Fatalf("module generated topology digest=%s, explicit=%s", generatedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 10,
		arch.InputEvent{Key: "input", Source: "driver", Action: "Send", Params: map[string]any{"value": 9}},
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
		t.Fatalf("module connection generator changed execution artifact:\ngenerated=%s\nexplicit=%s", generatedArtifact, explicitArtifact)
	}
	outputs := generatedResult.Poset.ByName("Output")
	if len(outputs) != 1 {
		t.Fatalf("module generated output count=%d, want 1", len(outputs))
	}
	if value, _ := outputs[0].Param("value"); value != int64(9) {
		t.Fatalf("module generated output value=%#v", value)
	}
}

func TestModuleFiniteIntegerConnectionGeneratorSubstitutesPatternValues(t *testing.T) {
	declarations := `
type Driver is interface action out Send(value : Integer); end interface Driver;
type Loopback is interface
  action in Input(value : Integer);
  action out Output(value : Integer);
end interface Loopback;
`
	generatedSource := []byte(declarations + `
module LoopbackModule() return Loopback is
connect
  for I : Integer in 0..1 generate
    Input(I) to Output;
  end generate;
end module LoopbackModule;
architecture System() is driver : Driver; worker : Loopback is LoopbackModule(); connect
  (?Value : Integer) driver.Send(?Value) => worker.Input(?Value);
end architecture System;
`)
	explicitSource := []byte(declarations + `
module LoopbackModule() return Loopback is
connect
  Input(1) to Output;
  Input(0) to Output;
end module LoopbackModule;
architecture System() is worker : Loopback is LoopbackModule(); driver : Driver; connect
  (?Value : Integer) driver.Send(?Value) => worker.Input(?Value);
end architecture System;
`)
	generated, err := Compile(generatedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(explicitSource, "system")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, _ := generated.DeterministicModelDigest()
	explicitDigest, _ := explicit.DeterministicModelDigest()
	if generatedDigest != explicitDigest {
		t.Fatalf("module finite generator topology=%s, explicit=%s", generatedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 20,
		arch.InputEvent{Key: "zero", Source: "driver", Action: "Send", Params: map[string]any{"value": 0}},
		arch.InputEvent{Key: "one", Source: "driver", Action: "Send", Params: map[string]any{"value": 1}},
		arch.InputEvent{Key: "two", Source: "driver", Action: "Send", Params: map[string]any{"value": 2}},
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
		t.Fatalf("module finite generator changed artifact:\ngenerated=%s\nexplicit=%s", generatedArtifact, explicitArtifact)
	}
	outputs := generatedResult.Poset.ByName("Output")
	if len(outputs) != 2 {
		t.Fatalf("module finite generated outputs=%d, want 2", len(outputs))
	}
	values := []int{outputs[0].ParamInt("value"), outputs[1].ParamInt("value")}
	sort.Ints(values)
	if !reflect.DeepEqual(values, []int{0, 1}) {
		t.Fatalf("module finite generated values=%v, want [0 1]", values)
	}
}

func TestModuleConnectionGeneratorsUseImmutableObjectConstants(t *testing.T) {
	declarations := `
type Driver is interface action out Send(value : Integer); end interface Driver;
type Loopback is interface
  action in Input(value : Integer);
  action out Output(value : Integer);
end interface Loopback;
`
	generatedSource := []byte(declarations + `
module LoopbackModule() return Loopback is
  Enabled : Boolean is True;
  Disabled : Boolean is False;
  First, Last : Integer is 0;
  Offset : Integer is 10;
connect
  if Enabled generate
    for I : Integer in First..Last + 1 generate
      Input(I) to Output(I + Offset);
    end generate;
  end generate;
  if Disabled generate
    Input(2) to Output;
  end generate;
end module LoopbackModule;
architecture System() is driver : Driver; worker : Loopback is LoopbackModule(); connect
  (?Value : Integer) driver.Send(?Value) => worker.Input(?Value);
end architecture System;
`)
	explicitSource := []byte(declarations + `
module LoopbackModule() return Loopback is
  Last, First : Integer is 0;
  Offset : Integer is 10;
  Disabled : Boolean is False;
  Enabled : Boolean is True;
connect
  Input(1) to Output(1 + Offset);
  Input(0) to Output(0 + Offset);
end module LoopbackModule;
architecture System() is worker : Loopback is LoopbackModule(); driver : Driver; connect
  (?Value : Integer) driver.Send(?Value) => worker.Input(?Value);
end architecture System;
`)
	generated, err := Compile(generatedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(explicitSource, "system")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, _ := generated.DeterministicModelDigest()
	explicitDigest, _ := explicit.DeterministicModelDigest()
	if generatedDigest != explicitDigest {
		t.Fatalf("object-bound module generator topology=%s, explicit=%s", generatedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 20,
		arch.InputEvent{Key: "zero", Source: "driver", Action: "Send", Params: map[string]any{"value": 0}},
		arch.InputEvent{Key: "one", Source: "driver", Action: "Send", Params: map[string]any{"value": 1}},
		arch.InputEvent{Key: "two", Source: "driver", Action: "Send", Params: map[string]any{"value": 2}},
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
		t.Fatalf("object-bound module generator changed artifact:\ngenerated=%s\nexplicit=%s", generatedArtifact, explicitArtifact)
	}
	outputs := generatedResult.Poset.ByName("Output")
	values := map[int]bool{}
	for _, output := range outputs {
		values[output.ParamInt("value")] = true
	}
	if len(outputs) != 2 || !values[10] || !values[11] {
		t.Fatalf("object-bound module generator outputs=%#v, want values 10 and 11", outputs)
	}
}

func TestModuleConnectionSetIfGeneratorIsCanonicalElaboration(t *testing.T) {
	declarations := `
type Driver is interface action out Send(value : Integer); end interface Driver;
type Loopback is interface
  action in Input(value : Integer);
  action out Output(value : Integer);
end interface Loopback;
`
	generatedSource := []byte(declarations + `
module LoopbackModule() return Loopback is
  Enabled : Boolean is True;
connect
  (?Value : Integer) Input(?Value) to
    if Enabled generate Output(?Value) end generate if;
end module LoopbackModule;
architecture System() is driver : Driver; worker : Loopback is LoopbackModule(); connect
  (?Value : Integer) driver.Send(?Value) => worker.Input(?Value);
end architecture System;
`)
	explicitSource := []byte(declarations + `
module LoopbackModule() return Loopback is
  Enabled : Boolean is True;
connect
  (?Value : Integer) Input(?Value) to Output(?Value);
end module LoopbackModule;
architecture System() is worker : Loopback is LoopbackModule(); driver : Driver; connect
  (?Value : Integer) driver.Send(?Value) => worker.Input(?Value);
end architecture System;
`)
	generated, err := Compile(generatedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile(explicitSource, "system")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, _ := generated.DeterministicModelDigest()
	explicitDigest, _ := explicit.DeterministicModelDigest()
	if generatedDigest != explicitDigest {
		t.Fatalf("module result-set generator topology=%s, explicit=%s", generatedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 10,
		arch.InputEvent{Key: "value", Source: "driver", Action: "Send", Params: map[string]any{"value": 8}},
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
		t.Fatalf("module result-set generator changed artifact:\ngenerated=%s\nexplicit=%s", generatedArtifact, explicitArtifact)
	}
}

func TestModuleFunctionConnectionIsPerInstanceOwnerScopedAndReplayable(t *testing.T) {
	source := []byte(`
type Driver is interface action out Send(n : Integer); end interface Driver;
type Sink is interface action in Receive(n : Integer); end interface Sink;
type Boundary is interface
  action in Request(n : Integer);
  action out Ready(n : Integer);
end interface Boundary;
type Worker is interface
  action in Begin(n : Integer);
  action out Done(n : Integer);
  requires Lookup : function(value : Integer) return Integer;
  provides Fetch : function(operand : Integer) return Integer;
end interface Worker;

module WorkerModule(Offset : Integer) return Worker is
  result : var Integer := 0;
  Fetch : function(operand : Integer) return Integer is
    begin
      return operand + Offset;
    end function Fetch;
connect
  Lookup to Fetch;
parallel
  when (?N : Integer) Begin(?N) do
    result := Lookup(?N);
    Done($result);
  end when;
end module WorkerModule;

architecture Grand(Offset : Integer) return Boundary is
  worker : Worker is WorkerModule(Offset);
connect
  (?N : Integer) Request(?N) to worker.Begin(?N);
  (?N : Integer) worker.Done(?N) to Ready(?N);
end architecture Grand;

architecture Child(Offset : Integer) return Boundary is
  grand : Boundary is Grand(Offset);
connect
  (?N : Integer) Request(?N) to grand.Request(?N);
  (?N : Integer) grand.Ready(?N) to Ready(?N);
end architecture Child;

architecture System() is
  driver : Driver;
  child : Boundary is Child(2);
  sink : Sink;
connect
  (?N : Integer) driver.Send(?N) to child.Request(?N);
  (?N : Integer) child.Ready(?N) to sink.Receive(?N);
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 80, arch.InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 5},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	grandID := arch.DeterministicArchitectureInstanceID("child", "grand")
	workerID := arch.DeterministicArchitectureComponentID(grandID, "worker")
	requiredCall := sourceNamedEvents(result.Poset, workerID, "Lookup'Call")
	providedCall := sourceNamedEvents(result.Poset, workerID, "Fetch'Call")
	requiredReturn := sourceNamedEvents(result.Poset, workerID, "Lookup'Return")
	providedReturn := sourceNamedEvents(result.Poset, workerID, "Fetch'Return")
	done := sourceNamedEvents(result.Poset, workerID, "Done")
	if len(requiredCall) != 1 || len(providedCall) != 1 ||
		len(requiredReturn) != 1 || len(providedReturn) != 1 || len(done) != 1 {
		t.Fatalf("module function events call=%#v/%#v return=%#v/%#v done=%#v",
			requiredCall, providedCall, requiredReturn, providedReturn, done)
	}
	if requiredCall[0].ID != providedCall[0].ID || requiredReturn[0].ID != providedReturn[0].ID {
		t.Fatal("module function connection duplicated its self call or return occurrence")
	}
	if done[0].ParamInt("n") != 7 || !done[0].HasObservation(grandID, "Ready") ||
		!done[0].HasObservation("child", "Ready") || !done[0].HasObservation("sink", "Receive") {
		t.Fatalf("module function result did not cross hierarchy: %#v", done[0].ObservationViews())
	}
	if !result.Poset.IsCausallyBefore(requiredCall[0].ID, providedReturn[0].ID) ||
		!result.Poset.IsCausallyBefore(requiredReturn[0].ID, done[0].ID) {
		t.Fatal("module function connection lost synchronous causality")
	}
	if len(result.State) != 1 || result.State[0].ComponentID != workerID ||
		result.State[0].Name != "result" || result.State[0].Value.Text != "7" {
		t.Fatalf("module function instance state=%#v", result.State)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	leftBytes, _ := result.MarshalCanonical()
	rightBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatal("module function replay changed canonical bytes")
	}
	explored, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 64, MaxChoiceDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 64, MaxChoiceDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if len(explored.Computations) != 1 || !bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("module function exploration=%#v", explored)
	}

	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for iteration := 0; iteration < 3; iteration++ {
			repeatedModel, err := Compile(source, "System")
			if err != nil {
				t.Fatal(err)
			}
			repeatedDigest, err := repeatedModel.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			if repeatedDigest != digest {
				t.Fatalf("module function model digest changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
			repeatedJournal := arch.NewExecutionJournal(repeatedDigest, 80, arch.InputEvent{
				Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 5},
			})
			repeated, err := repeatedModel.ExecuteDeterministic(repeatedJournal)
			if err != nil {
				t.Fatal(err)
			}
			repeatedBytes, err := repeated.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(leftBytes, repeatedBytes) {
				t.Fatalf("module function artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

func TestModuleFunctionConnectionDiagnosticsAreExplicit(t *testing.T) {
	tests := []struct {
		name, functions, implementations, connection, want string
	}{
		{
			name:      "source is provided",
			functions: "provides Source : function(v : Integer); Target : function(v : Integer);",
			implementations: `
Source : function(v : Integer) is begin end function Source;
Target : function(v : Integer) is begin end function Target;`,
			connection: "Source to Target;", want: "not a requires function",
		},
		{
			name:       "target is required",
			functions:  "requires Source : function(v : Integer); Target : function(v : Integer);",
			connection: "Source to Target;", want: "not a provides function",
		},
		{
			name:      "incompatible",
			functions: "requires Source : function(v : Integer) return Integer; provides Target : function(v : Integer) return Boolean;",
			implementations: `
Target : function(v : Integer) return Boolean is begin return false; end function Target;`,
			connection: "Source to Target;", want: "0 type-compatible provided signatures",
		},
		{
			name:      "arguments",
			functions: "requires Source : function(v : Integer); provides Target : function(v : Integer);",
			implementations: `
Target : function(v : Integer) is begin end function Target;`,
			connection: "(?N : Integer) Source(?N) to Target(?N);", want: "do not accept pattern placeholders",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type I is interface ` + test.functions + ` end interface I;
module M() return I is ` + test.implementations + ` connect ` + test.connection + ` end module M;
architecture System() is worker : I is M(); end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("module function diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

const moduleCompoundConnectionSource = `
type Driver is interface
  action out Begin(n : Integer); action out End(n : Integer);
end interface Driver;
type Worker is interface
  action in Begin(n : Integer); action in End(n : Integer);
  action out Published(n : Integer);
  private action Left(n : Integer); private action Right(n : Integer); private action Combined(n : Integer);
end interface Worker;
type Sink is interface action in Receive(n : Integer); end interface Sink;

module WorkerModule() return Worker is
connect
  (?N : Integer) Begin(?N) to Left(?N);
  (?N : Integer) End(?N) to Right(?N);
  (?N : Integer) (Left(?N) and Right(?N)) CONNECTOR Combined(?N);
  (?N : Integer) Combined(?N) to Published(?N);
end module WorkerModule;

architecture System() is
  driver : Driver;
  worker : Worker is WorkerModule();
  sink : Sink;
connect
  driver.Begin to worker.Begin;
  driver.End to worker.End;
  worker.Published to sink.Receive;
end architecture System;
`

func TestModuleCompoundPipeAndAgentConnectionsAreScopedReplayableAndDeterministic(t *testing.T) {
	tests := []struct {
		name      string
		connector string
		kind      arch.ConnectionKind
		ordered   bool
	}{
		{name: "pipe", connector: "=>", kind: arch.PipeConnection, ordered: true},
		{name: "agent", connector: "||>", kind: arch.AgentConnection, ordered: false},
	}
	modelDigests := make(map[string]string, len(tests))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(strings.Replace(moduleCompoundConnectionSource, "CONNECTOR", test.connector, 1))
			model, err := Compile(source, "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			modelDigests[test.name] = digest
			journal := arch.NewExecutionJournal(digest, 80,
				arch.InputEvent{Key: "begin-1", Source: "driver", Action: "Begin", Params: map[string]any{"n": 1}},
				arch.InputEvent{Key: "end-2", Source: "driver", Action: "End", Params: map[string]any{"n": 2}},
				arch.InputEvent{Key: "begin-2", Source: "driver", Action: "Begin", Params: map[string]any{"n": 2}},
				arch.InputEvent{Key: "end-1", Source: "driver", Action: "End", Params: map[string]any{"n": 1}},
			)
			result, err := model.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			combined := result.Poset.ByName("Combined")
			published := result.Poset.ByName("Published")
			received := result.Poset.ByName("Receive")
			if len(combined) != 2 || len(published) != 2 || len(received) != 2 {
				t.Fatalf("compound/public/sink observations=%d/%d/%d, want 2/2/2",
					len(combined), len(published), len(received))
			}
			byValue := func(events []*gorapide.Event) map[int]*gorapide.Event {
				indexed := make(map[int]*gorapide.Event, len(events))
				for _, event := range events {
					indexed[event.ParamInt("n")] = event
				}
				return indexed
			}
			left, right, output := byValue(result.Poset.ByName("Left")), byValue(result.Poset.ByName("Right")), byValue(combined)
			for _, value := range []int{1, 2} {
				if left[value] == nil || right[value] == nil || output[value] == nil {
					t.Fatalf("missing value %d in left=%v right=%v output=%v", value, left, right, output)
				}
				if !result.Poset.IsCausallyBefore(left[value].ID, output[value].ID) ||
					!result.Poset.IsCausallyBefore(right[value].ID, output[value].ID) {
					t.Fatalf("compound output %d does not depend on both matched module events", value)
				}
				if output[value].ID != byValue(published)[value].ID || output[value].ID != byValue(received)[value].ID {
					t.Fatalf("basic publication changed compound occurrence identity for value %d", value)
				}
			}
			if test.ordered {
				if !result.Poset.IsCausallyBefore(output[1].ID, output[2].ID) &&
					!result.Poset.IsCausallyBefore(output[2].ID, output[1].ID) {
					t.Fatal("module compound pipe did not serialize distinct outputs")
				}
			} else if !result.Poset.IsCausallyIndependent(output[1].ID, output[2].ID) {
				t.Fatal("module compound agent added a connection-local output ordering")
			}
			compoundFirings := 0
			for _, firing := range result.Firings {
				if firing.ConnectionScope != arch.ModuleConnectionScope.String() ||
					firing.ConnectionKind != test.kind.String() {
					continue
				}
				compoundFirings++
				if firing.Target != "worker" || len(firing.MatchedEvents) != 2 || len(firing.Bindings) != 1 ||
					firing.Bindings[0].Placeholder != "N" {
					t.Fatalf("compound module firing audit=%#v", firing)
				}
			}
			if compoundFirings != 2 {
				t.Fatalf("compound module firings=%d, want 2; audit=%#v", compoundFirings, result.Firings)
			}
			artifact, err := result.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			artifactDigest, err := result.ArtifactDigest()
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := model.ReplayDeterministic(journal, artifactDigest)
			if err != nil {
				t.Fatal(err)
			}
			replayArtifact, err := replayed.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(artifact, replayArtifact) {
				t.Fatal("module compound connection replay was not byte-identical")
			}
			explorationJournal := journal
			explorationJournal.Choices = make([]arch.ChoiceDecision, len(result.Choices))
			for index, choice := range result.Choices {
				explorationJournal.Choices[index] = choice.Decision()
			}
			limits := arch.ExplorationLimits{MaxExecutions: 64, MaxChoiceDepth: 16}
			firstExploration, err := model.ExploreDeterministic(explorationJournal, limits)
			if err != nil {
				t.Fatal(err)
			}
			secondExploration, err := model.ExploreDeterministic(explorationJournal, limits)
			if err != nil {
				t.Fatal(err)
			}
			firstBytes, _ := firstExploration.MarshalCanonical()
			secondBytes, _ := secondExploration.MarshalCanonical()
			if len(firstExploration.Computations) != 1 || !bytes.Equal(firstBytes, secondBytes) {
				t.Fatalf("module compound exploration=%#v", firstExploration)
			}

			previous := runtime.GOMAXPROCS(0)
			defer runtime.GOMAXPROCS(previous)
			for _, processors := range []int{1, 8} {
				runtime.GOMAXPROCS(processors)
				for iteration := 0; iteration < 3; iteration++ {
					repeatedModel, err := Compile(source, "System")
					if err != nil {
						t.Fatal(err)
					}
					repeatedDigest, err := repeatedModel.DeterministicModelDigest()
					if err != nil {
						t.Fatal(err)
					}
					if repeatedDigest != digest {
						t.Fatalf("module compound model digest changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
					}
					repeated, err := repeatedModel.ExecuteDeterministic(journal)
					if err != nil {
						t.Fatal(err)
					}
					repeatedArtifact, err := repeated.MarshalCanonical()
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(artifact, repeatedArtifact) {
						t.Fatalf("module compound artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
					}
				}
			}
		})
	}
	if modelDigests["pipe"] != "" && modelDigests["pipe"] == modelDigests["agent"] {
		t.Fatal("module compound pipe and agent semantics have one model identity")
	}
	canonicalPipe := strings.Replace(moduleCompoundConnectionSource, "CONNECTOR", "=>", 1)
	reorderedPipe := strings.Replace(canonicalPipe, `
  (?N : Integer) Begin(?N) to Left(?N);
  (?N : Integer) End(?N) to Right(?N);
  (?N : Integer) (Left(?N) and Right(?N)) => Combined(?N);
  (?N : Integer) Combined(?N) to Published(?N);`, `
  (?N : Integer) Combined(?N) to Published(?N);
  (?N : Integer) (Right(?N) and Left(?N)) => Combined(?N);
  (?N : Integer) End(?N) to Right(?N);
  (?N : Integer) Begin(?N) to Left(?N);`, 1)
	reorderedModel, err := Compile([]byte(reorderedPipe), "System")
	if err != nil {
		t.Fatal(err)
	}
	reorderedDigest, err := reorderedModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if canonicalPipe == reorderedPipe || reorderedDigest != modelDigests["pipe"] {
		t.Fatal("module connection declaration or union-operand order changed canonical model identity")
	}
}

func TestNestedModuleCompoundConnectionRetainsOwnerScopeAndKind(t *testing.T) {
	source := []byte(`
type Driver is interface action out Begin(n : Integer); action out End(n : Integer); end interface Driver;
type Sink is interface action in Receive(n : Integer); end interface Sink;
type Boundary is interface
  action in Begin(n : Integer); action in End(n : Integer); action out Published(n : Integer);
end interface Boundary;
type Worker is interface
  action in Begin(n : Integer); action in End(n : Integer); action out Published(n : Integer);
  private action Left(n : Integer); private action Right(n : Integer); private action Combined(n : Integer);
end interface Worker;
module WorkerModule() return Worker is connect
  (?N : Integer) Begin(?N) to Left(?N);
  (?N : Integer) End(?N) to Right(?N);
  (?N : Integer) (Left(?N) and Right(?N)) ||> Combined(?N);
  (?N : Integer) Combined(?N) to Published(?N);
end module WorkerModule;
architecture Child() return Boundary is
  worker : Worker is WorkerModule();
connect
  (?N : Integer) Begin(?N) to worker.Begin(?N);
  (?N : Integer) End(?N) to worker.End(?N);
  (?N : Integer) worker.Published(?N) to Published(?N);
end architecture Child;
architecture System() is
  driver : Driver; child : Boundary is Child(); sink : Sink;
connect
  (?N : Integer) driver.Begin(?N) to child.Begin(?N);
  (?N : Integer) driver.End(?N) to child.End(?N);
  (?N : Integer) child.Published(?N) to sink.Receive(?N);
end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 80,
		arch.InputEvent{Key: "begin", Source: "driver", Action: "Begin", Params: map[string]any{"n": 7}},
		arch.InputEvent{Key: "end", Source: "driver", Action: "End", Params: map[string]any{"n": 7}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	workerID := arch.DeterministicArchitectureComponentID("child", "worker")
	combined := sourceNamedEvents(result.Poset, workerID, "Combined")
	if len(combined) != 1 || combined[0].ParamInt("n") != 7 ||
		!combined[0].HasObservation(workerID, "Published") ||
		!combined[0].HasObservation("child", "Published") ||
		!combined[0].HasObservation("sink", "Receive") {
		t.Fatalf("nested module compound output=%#v", combined)
	}
	found := false
	for _, firing := range result.Firings {
		if firing.Target == workerID && firing.ConnectionScope == arch.ModuleConnectionScope.String() &&
			firing.ConnectionKind == arch.AgentConnection.String() && len(firing.MatchedEvents) == 2 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("nested module compound firing audit=%#v", result.Firings)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("nested module compound replay was not byte-identical")
	}
}

func TestCompileRejectsMalformedOrUnsupportedModuleConnections(t *testing.T) {
	tests := []struct {
		name, interfaceBody, connection, want string
	}{
		{"empty", "action in A(); action out B();", "", "requires a connection"},
		{"compound-basic", "action in A(); action in B(); action out C();", "(A and B) to C;", "require '=>' or '||>'"},
		{"compound-target-arguments", "action in A(n : Integer); action in B(n : Integer); action out C(n : Integer);", "(?N : Integer) (A(?N) and B(?N)) => C;", "result supplies 0 arguments"},
		{"universal-compound", "action in A(n : Integer); action out B(n : Integer);", "(!N : Integer range 1..2 by ->) A(!N) => B(!N);", "universal module-connection sources"},
		{"function-pattern-leaf", "requires F : function(); action in A(); action out B();", "(F and A) ||> B;", "function call/return events"},
		{"target-in", "action in A(); action in B();", "A to B;", "not an out action"},
		{"unknown-source", "action out B();", "Missing to B;", "source action"},
		{"unknown-target", "action in A();", "A to Missing;", "target action"},
		{"qualified-source", "action in A(); action out B();", "self.A to B;", "must be an unqualified action"},
		{"qualified-compound-source", "action in A(); action in B(); action out C();", "(self.A and B) => C;", "must be unqualified"},
		{"duplicate", "action in A(); action out B();", "A to B; A to B;", "duplicate module connection"},
		{"shape", "action in A(n : Integer); action out B(value : Integer);", "A to B;", "identical parameter names"},
		{"unbound", "action in A(n : Integer); action out B(n : Integer);", "(?N : Integer; ?U : Integer) A(?N) to B(?N);", "never bound"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type I is interface ` + test.interfaceBody + ` end interface I;
module M() return I is connect ` + test.connection + ` end module M;
architecture System() is worker : I is M(); end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestSourceModuleConnectionsStableAcrossGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for iteration := 0; iteration < 20; iteration++ {
			model, err := Compile([]byte(moduleConnectionSource), "System")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := model.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
				arch.InputEvent{Key: "send", Source: "driver", Action: "Send", Params: map[string]any{"n": 9}},
			))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := result.MarshalCanonical()
			if err != nil {
				t.Fatal(err)
			}
			if baseline == nil {
				baseline = encoded
			} else if !bytes.Equal(baseline, encoded) {
				t.Fatalf("module connection artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}
