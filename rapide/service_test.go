package rapide

import (
	"bytes"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestNamedServiceMembersAreExecutionFacingAndReplayable(t *testing.T) {
	source := []byte(`
type Protocol is interface
  action in Request(Value : Integer);
  action out Reply(Value : Integer);
provides
  Read : function(Key : String) return Integer;
requires
  Write : function(Value : Integer);
end interface Protocol;
type ProtocolAlias is Protocol;
type Wrapped is interface service Inner : dual Protocol; end interface Wrapped;
type Gateway is interface
  action out Root();
  service
    API : ProtocolAlias;
    Socket : dual Protocol;
    Port(-1..0) : Wrapped;
end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	component, ok := model.Component("gateway")
	if !ok {
		t.Fatal("compiled gateway component is absent")
	}
	actions := make(map[string]arch.ActionDecl, len(component.Interface.Actions))
	for _, action := range component.Interface.Actions {
		actions[action.Name] = action
	}
	wantActions := map[string]arch.ActionDecl{
		"Root":                   {Name: "Root", Kind: arch.OutAction, Params: []arch.ParamDecl{}},
		"api.request":            {Name: "api.request", Kind: arch.InAction, Params: []arch.ParamDecl{arch.P("value", "Integer")}},
		"api.reply":              {Name: "api.reply", Kind: arch.OutAction, Params: []arch.ParamDecl{arch.P("value", "Integer")}},
		"socket.request":         {Name: "socket.request", Kind: arch.OutAction, Params: []arch.ParamDecl{arch.P("value", "Integer")}},
		"socket.reply":           {Name: "socket.reply", Kind: arch.InAction, Params: []arch.ParamDecl{arch.P("value", "Integer")}},
		"port(-1).inner.request": {Name: "port(-1).inner.request", Kind: arch.OutAction, Params: []arch.ParamDecl{arch.P("value", "Integer")}},
		"port(-1).inner.reply":   {Name: "port(-1).inner.reply", Kind: arch.InAction, Params: []arch.ParamDecl{arch.P("value", "Integer")}},
		"port(0).inner.request":  {Name: "port(0).inner.request", Kind: arch.OutAction, Params: []arch.ParamDecl{arch.P("value", "Integer")}},
		"port(0).inner.reply":    {Name: "port(0).inner.reply", Kind: arch.InAction, Params: []arch.ParamDecl{arch.P("value", "Integer")}},
	}
	if !reflect.DeepEqual(actions, wantActions) {
		t.Fatalf("execution-facing service actions:\nwant %#v\n got %#v", wantActions, actions)
	}

	functions := make(map[string]arch.FunctionDecl, len(component.Interface.Functions))
	for _, function := range component.Interface.Functions {
		functions[function.Name] = function
	}
	wantFunctions := map[string]arch.FunctionDecl{
		"api.read":             {Name: "api.read", Kind: arch.ProvidesFunction, Params: []arch.ParamDecl{arch.P("key", "String")}, ReturnType: "Integer"},
		"api.write":            {Name: "api.write", Kind: arch.RequiresFunction, Params: []arch.ParamDecl{arch.P("value", "Integer")}},
		"socket.read":          {Name: "socket.read", Kind: arch.RequiresFunction, Params: []arch.ParamDecl{arch.P("key", "String")}, ReturnType: "Integer"},
		"socket.write":         {Name: "socket.write", Kind: arch.ProvidesFunction, Params: []arch.ParamDecl{arch.P("value", "Integer")}},
		"port(-1).inner.read":  {Name: "port(-1).inner.read", Kind: arch.RequiresFunction, Params: []arch.ParamDecl{arch.P("key", "String")}, ReturnType: "Integer"},
		"port(-1).inner.write": {Name: "port(-1).inner.write", Kind: arch.ProvidesFunction, Params: []arch.ParamDecl{arch.P("value", "Integer")}},
		"port(0).inner.read":   {Name: "port(0).inner.read", Kind: arch.RequiresFunction, Params: []arch.ParamDecl{arch.P("key", "String")}, ReturnType: "Integer"},
		"port(0).inner.write":  {Name: "port(0).inner.write", Kind: arch.ProvidesFunction, Params: []arch.ParamDecl{arch.P("value", "Integer")}},
	}
	if !reflect.DeepEqual(functions, wantFunctions) {
		t.Fatalf("execution-facing service functions:\nwant %#v\n got %#v", wantFunctions, functions)
	}

	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 10, arch.InputEvent{
		Key: "indexed-service-output", Source: "gateway", Action: "port(-1).inner.request",
		Params: map[string]any{"value": 7},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	events := result.Poset.ByName("port(-1).inner.request")
	if len(events) != 1 || !events[0].HasObservation("gateway", "port(-1).inner.request") {
		t.Fatalf("indexed service execution events=%#v", events)
	}
	encoded, err := result.MarshalCanonical()
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
	replayBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, replayBytes) {
		t.Fatal("indexed qualified service output did not replay byte-identically")
	}
}

func TestServiceQualifiedArchitectureConnectionsExecuteAndReplay(t *testing.T) {
	source := []byte(`
type Protocol is interface action in Request(Value : Integer); end interface Protocol;
type Wrapped is interface service Inner : dual Protocol; end interface Wrapped;
type Emitter is interface service Port(-1..0) : Wrapped; end interface Emitter;
type Receiver is interface service API : Protocol; end interface Receiver;
architecture System() is
  emitter : Emitter;
  receiver : Receiver;
constraint
  observe from receiver.API.Request
    match receiver.API.Request;
  end observe;
connect
  (?N : Integer) emitter.Port(-01).Inner.Request(?N) => receiver.API.Request(?N);
end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	connection := file.Architectures[0].Connections[0]
	if connection.Source.Action != "Port(-1).Inner.Request" || connection.Target.Action != "API.Request" {
		t.Fatalf("qualified service connection AST=%#v", connection)
	}
	model, err := CompileFile(file, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 10, arch.InputEvent{
		Key: "service-source", Source: "emitter", Action: "port(-1).inner.request",
		Params: map[string]any{"value": 9},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	sources := result.Poset.ByName("port(-1).inner.request")
	targets := result.Poset.ByName("api.request")
	if len(sources) != 1 || len(targets) != 1 ||
		!result.Poset.IsCausallyBefore(sources[0].ID, targets[0].ID) ||
		!targets[0].HasObservation("receiver", "api.request") {
		t.Fatalf("qualified service connection source=%#v target=%#v", sources, targets)
	}
	if result.Constraints == nil || !result.Constraints.Passed || len(result.Constraints.Reports) != 1 {
		t.Fatalf("qualified service architecture constraint=%#v", result.Constraints)
	}
	encoded, err := result.MarshalCanonical()
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
	replayBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, replayBytes) {
		t.Fatal("qualified service connection did not replay byte-identically")
	}

	canonicalSource := bytes.ReplaceAll(source, []byte("emitter.Port(-01).Inner.Request"), []byte("emitter.port(-1).inner.request"))
	canonicalSource = bytes.ReplaceAll(canonicalSource, []byte("receiver.API.Request"), []byte("receiver.api.request"))
	canonicalModel, err := Compile(canonicalSource, "system")
	if err != nil {
		t.Fatal(err)
	}
	canonicalDigest, err := canonicalModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if canonicalDigest != digest {
		t.Fatalf("qualified service reference spelling changed model identity: %s != %s", canonicalDigest, digest)
	}
}

func TestServiceConnectionListsAreCanonicalCartesianProducts(t *testing.T) {
	declarations := `
type Protocol is interface
  action in Request(Value : Integer);
  action out Reply(Value : Integer);
end interface Protocol;
type Left is interface service API1, API2 : Protocol; end interface Left;
type Right is interface service Socket1, Socket2 : dual Protocol; end interface Right;
`
	sources := [][]byte{
		[]byte(declarations + `
architecture System() is left : Left; right : Right; connect
  left.API1, left.API2 to right.Socket1, right.Socket2;
end architecture System;
`),
		[]byte(declarations + `
architecture System() is right : Right; left : Left; connect
  LEFT.api2, left.API1 to RIGHT.socket2, right.Socket1;
end architecture System;
`),
		[]byte(declarations + `
architecture System() is left : Left; right : Right; connect
  left.API2 to right.Socket2;
  left.API1 to right.Socket1;
  left.API2 to right.Socket1;
  left.API1 to right.Socket2;
end architecture System;
`),
	}
	parsed, err := Parse(sources[0])
	if err != nil {
		t.Fatal(err)
	}
	connections := parsed.Architectures[0].Connections
	if len(connections) != 4 {
		t.Fatalf("service connection list expanded to %d rules, want 4", len(connections))
	}
	wantPairs := []string{
		"left.API1->right.Socket1", "left.API1->right.Socket2",
		"left.API2->right.Socket1", "left.API2->right.Socket2",
	}
	gotPairs := make([]string, 0, len(connections))
	for _, connection := range connections {
		gotPairs = append(gotPairs, connection.Source.Component+"."+connection.Source.Action+
			"->"+connection.Target.Component+"."+connection.Target.Action)
	}
	if !reflect.DeepEqual(gotPairs, wantPairs) {
		t.Fatalf("service connection Cartesian product:\nwant %v\n got %v", wantPairs, gotPairs)
	}

	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baselineDigest string
	var baselineArtifact []byte
	for iteration := 0; iteration < 6; iteration++ {
		if iteration < 3 {
			runtime.GOMAXPROCS(1)
		} else {
			runtime.GOMAXPROCS(4)
		}
		model, err := Compile(sources[iteration%len(sources)], "system")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
			arch.InputEvent{Key: "left-reply", Source: "left", Action: "api1.reply", Params: map[string]any{"value": 7}},
			arch.InputEvent{Key: "right-request", Source: "right", Action: "socket1.request", Params: map[string]any{"value": 9}},
		))
		if err != nil {
			t.Fatal(err)
		}
		leftReply := result.Poset.ByName("api1.reply")
		rightRequest := result.Poset.ByName("socket1.request")
		if len(leftReply) != 1 || !leftReply[0].HasObservation("right", "socket1.reply") ||
			!leftReply[0].HasObservation("right", "socket2.reply") {
			t.Fatalf("service-list fan-out reply=%#v", leftReply)
		}
		if len(rightRequest) != 1 || !rightRequest[0].HasObservation("left", "api1.request") ||
			!rightRequest[0].HasObservation("left", "api2.request") {
			t.Fatalf("service-list fan-out request=%#v", rightRequest)
		}
		artifact, err := result.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if baselineDigest == "" {
			baselineDigest, baselineArtifact = digest, artifact
			continue
		}
		if digest != baselineDigest || !bytes.Equal(artifact, baselineArtifact) {
			t.Fatalf("connection-list spelling/order/GOMAXPROCS changed semantics: digest %s/%s\nbase=%s\n got=%s",
				baselineDigest, digest, baselineArtifact, artifact)
		}
	}
}

func TestGuardedServiceConnectionListsGateEachServicePair(t *testing.T) {
	source := []byte(`
type Protocol is interface action in Request(); action out Reply(); end interface Protocol;
type Left is interface service API1, API2 : Protocol; end interface Left;
type Right is interface service Socket : dual Protocol; end interface Right;
architecture System() is left : Left; right : Right; connect
  left.API1 where false, left.API2 where true to right.Socket;
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
		arch.InputEvent{Key: "api1", Source: "left", Action: "api1.reply"},
		arch.InputEvent{Key: "api2", Source: "left", Action: "api2.reply"},
		arch.InputEvent{Key: "socket", Source: "right", Action: "socket.request"},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	api1 := result.Poset.ByName("api1.reply")
	api2 := result.Poset.ByName("api2.reply")
	socket := result.Poset.ByName("socket.request")
	if len(api1) != 1 || api1[0].HasObservation("right", "socket.reply") {
		t.Fatalf("false service guard forwarded api1=%#v", api1)
	}
	if len(api2) != 1 || !api2[0].HasObservation("right", "socket.reply") {
		t.Fatalf("true service guard did not forward api2=%#v", api2)
	}
	if len(socket) != 1 || socket[0].HasObservation("left", "api1.request") ||
		!socket[0].HasObservation("left", "api2.request") {
		t.Fatalf("per-service reciprocal guards socket=%#v", socket)
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
		t.Fatal("guarded service-list replay was not byte-identical")
	}
}

func TestServiceQualifiedFunctionConnectionsResolveCanonicalNames(t *testing.T) {
	source := []byte(`
type Protocol is interface
provides
  Store : function(Item : Integer);
requires
  Write : function(Value : Integer);
end interface Protocol;
type Client is interface service API : Protocol; end interface Client;
type Server is interface service API : Protocol; end interface Server;
architecture System() is client : Client; server : Server; connect
  client.API.WRITE to server.API.STORE;
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
	lowerSource := bytes.ReplaceAll(source, []byte("client.API.WRITE"), []byte("client.api.write"))
	lowerSource = bytes.ReplaceAll(lowerSource, []byte("server.API.STORE"), []byte("server.api.store"))
	lowerModel, err := Compile(lowerSource, "system")
	if err != nil {
		t.Fatal(err)
	}
	lowerDigest, err := lowerModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if lowerDigest != digest {
		t.Fatalf("service-qualified function reference spelling changed model identity: %s != %s", lowerDigest, digest)
	}
}

func TestArchitectureBoundaryFunctionServiceExpandsRecursiveAliasPaths(t *testing.T) {
	source := []byte(`
type Protocol is interface
provides
  Down : function(value : Integer) return Integer;
requires
  Up : function(value : Integer) return Integer;
end interface Protocol;
type Boundary is interface service Link : Protocol; end interface Boundary;
type Worker is interface service Link : Protocol; end interface Worker;
type Client is interface
  action out Start(n : Integer);
  action out Done(n : Integer);
  requires Compute : function(value : Integer) return Integer;
end interface Client;
type Server is interface
  provides Fetch : function(value : Integer) return Integer;
end interface Server;

architecture Child() return Boundary is
  worker : Worker;
connect
  Link to worker.Link;
end architecture Child;

architecture System() is
  client : Client;
  child : Boundary is Child();
  server : Server;
connect
  client.Compute to child.Link.Down;
  child.Link.Up to server.Fetch;
end architecture System;
`)

	prepare := func(source []byte) (*arch.Architecture, string) {
		model, err := Compile(source, "System")
		if err != nil {
			t.Fatal(err)
		}
		client, exists := model.Component("client")
		if !exists {
			t.Fatal("compiled service model is missing client")
		}
		if err := client.DeclareState(arch.StateReference("result", "Integer", 0)); err != nil {
			t.Fatal(err)
		}
		number := pattern.Var("N").WithType("Integer")
		if err := client.AddDeclarativeRule(arch.Rule("invoke-service").
			On(pattern.MatchEvent("Start").BindParam("n", number)).
			Do(
				arch.CallFunctionInto("compute", "result", "Compute", arch.BindingParam("value", "N")),
				arch.CallAction("done", "Done", arch.StateParam("n", "result")),
			).Build()); err != nil {
			t.Fatal(err)
		}

		workerID := arch.DeterministicArchitectureComponentID("child", "worker")
		worker, exists := model.Component(workerID)
		if !exists {
			t.Fatalf("compiled service model is missing %q", workerID)
		}
		if err := worker.DeclareState(arch.StateReference("temporary", "Integer", 0)); err != nil {
			t.Fatal(err)
		}
		if err := worker.AddFunctionImplementation(arch.Function("link.down", "Integer", arch.P("value", "Integer")).
			Do(
				arch.CallFunctionInto("external", "temporary", "link.up", arch.BindingParam("value", "value")),
			).
			Returns(arch.AddValues(arch.ReadState("temporary"), arch.LiteralValue(1))).Build()); err != nil {
			t.Fatal(err)
		}
		server, exists := model.Component("server")
		if !exists {
			t.Fatal("compiled service model is missing server")
		}
		if err := server.AddFunctionImplementation(arch.Function("Fetch", "Integer", arch.P("value", "Integer")).
			Returns(arch.MultiplyValues(arch.BoundValue("value"), arch.LiteralValue(2))).Build()); err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return model, digest
	}

	model, digest := prepare(source)
	journal := arch.NewExecutionJournal(digest, 100, arch.InputEvent{
		Key: "start", Source: "client", Action: "Start", Params: map[string]any{"n": 5},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	workerID := arch.DeterministicArchitectureComponentID("child", "worker")
	for _, alias := range []struct{ source, name string }{
		{"client", "Compute'Call"}, {"child", "link.down'Call"}, {workerID, "link.down'Call"},
		{workerID, "link.up'Call"}, {"child", "link.up'Call"}, {"server", "Fetch'Call"},
		{"client", "Compute'Return"}, {"child", "link.down'Return"}, {workerID, "link.down'Return"},
		{workerID, "link.up'Return"}, {"child", "link.up'Return"}, {"server", "Fetch'Return"},
	} {
		if events := functionBoundaryEvents(result.Poset, alias.source, alias.name); len(events) != 1 {
			t.Fatalf("service function alias %s.%s=%#v", alias.source, alias.name, events)
		}
	}
	done := functionBoundaryEvents(result.Poset, "client", "Done")
	if len(done) != 1 || done[0].ParamInt("n") != 11 {
		t.Fatalf("service boundary function result=%#v", done)
	}
	canonical, _ := result.MarshalCanonical()
	artifactDigest, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(canonical, replayedBytes) {
		t.Fatal("function-bearing boundary service replay changed bytes")
	}
	scheduledJournal := journal
	for _, resolution := range result.Choices {
		scheduledJournal.Choices = append(scheduledJournal.Choices, resolution.Decision())
	}
	explored, err := model.ExploreDeterministic(scheduledJournal, arch.ExplorationLimits{
		MaxExecutions: 32, MaxChoiceDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(scheduledJournal, arch.ExplorationLimits{
		MaxExecutions: 32, MaxChoiceDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if len(explored.Computations) != 1 || !bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("function-bearing boundary service exploration=%#v", explored)
	}

	reversedSource := bytes.ReplaceAll(source, []byte("Link to worker.Link;"), []byte("worker.Link to Link;"))
	reversed, reversedDigest := prepare(reversedSource)
	if reversedDigest != digest {
		t.Fatalf("boundary service textual direction changed model identity: %s != %s", reversedDigest, digest)
	}
	reversedResult, err := reversed.ExecuteDeterministic(arch.NewExecutionJournal(reversedDigest, 100, arch.InputEvent{
		Key: "start", Source: "client", Action: "Start", Params: map[string]any{"n": 5},
	}))
	if err != nil {
		t.Fatal(err)
	}
	reversedBytes, _ := reversedResult.MarshalCanonical()
	if !bytes.Equal(canonical, reversedBytes) {
		t.Fatal("boundary service textual direction changed execution artifact")
	}

	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for iteration, candidate := range [][]byte{source, reversedSource, source} {
			repeated, repeatedDigest := prepare(candidate)
			if repeatedDigest != digest {
				t.Fatalf("boundary function service model changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
			repeatedResult, err := repeated.ExecuteDeterministic(arch.NewExecutionJournal(
				repeatedDigest, 100, arch.InputEvent{
					Key: "start", Source: "client", Action: "Start", Params: map[string]any{"n": 5},
				},
			))
			if err != nil {
				t.Fatal(err)
			}
			repeatedBytes, _ := repeatedResult.MarshalCanonical()
			if !bytes.Equal(canonical, repeatedBytes) {
				t.Fatalf("boundary function service artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

func TestServiceConstraintsRewriteIntoQualifiedModuleScope(t *testing.T) {
	source := []byte(`
type Protocol is interface
  action in Request(Value : Integer);
constraint
  Policy: observe from Request(Value is 7)
    Forbidden: never Request(Value is 7);
  end observe;
end interface Protocol;
type Wrapped is interface service Inner : Protocol; end interface Wrapped;
type Gateway is interface service Port(-1..0) : dual Wrapped; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	lowerSource := []byte(`
type Protocol is interface
  action in request(value : integer);
constraint
  policy: observe from request(value is 7)
    forbidden: never request(value is 7);
  end observe;
end interface Protocol;
type Wrapped is interface service inner : Protocol; end interface Wrapped;
type Gateway is interface service port(-1..0) : dual Wrapped; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;
`)
	lowerModel, err := Compile(lowerSource, "system")
	if err != nil {
		t.Fatal(err)
	}
	lowerDigest, err := lowerModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if lowerDigest != digest {
		t.Fatalf("service constraint case changed model identity: %s != %s", lowerDigest, digest)
	}
	journal := arch.NewExecutionJournal(digest, 10, arch.InputEvent{
		Key: "forbidden-service-request", Source: "gateway", Action: "port(-1).inner.request",
		Params: map[string]any{"value": 7},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ModuleConstraints) != 1 || result.ModuleConstraints[0].ComponentID != "gateway" ||
		result.ModuleConstraints[0].Report.Passed || len(result.ModuleConstraints[0].Report.Reports) != 2 {
		t.Fatalf("qualified service constraint report=%#v", result.ModuleConstraints)
	}
	failed := 0
	for _, report := range result.ModuleConstraints[0].Report.Reports {
		if report.Passed {
			continue
		}
		failed++
		if len(report.Violations) != 1 ||
			!strings.Contains(report.Violations[0].Constraint, "port(-1).inner.policy") ||
			report.Violations[0].Clause != "label:port(-1).inner.forbidden" {
			t.Fatalf("qualified service violation=%#v", report.Violations)
		}
	}
	if failed != 1 {
		t.Fatalf("qualified service failed constraints=%d, want 1", failed)
	}
	encoded, err := result.MarshalCanonical()
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
	replayBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, replayBytes) {
		t.Fatal("qualified service constraint did not replay byte-identically")
	}
}

func TestServiceConnectionElaboratesBidirectionalActionsAndFunctions(t *testing.T) {
	source := []byte(`
type Protocol is interface
  action in Request(Value : Integer);
  action out Reply(Value : Integer);
provides
  Store : function(Item : Integer);
requires
  Fetch : function(Key : String) return Integer;
end interface Protocol;
type Left is interface service API : Protocol; end interface Left;
type Right is interface service Socket : dual Protocol; end interface Right;
architecture System() is left : Left; right : Right; connect
  left.API to right.Socket;
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
		arch.InputEvent{Key: "left-reply", Source: "left", Action: "api.reply", Params: map[string]any{"value": 11}},
		arch.InputEvent{Key: "right-request", Source: "right", Action: "socket.request", Params: map[string]any{"value": 22}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	leftToRight := result.Poset.ByName("api.reply")
	rightToLeft := result.Poset.ByName("socket.request")
	if len(leftToRight) != 1 || !leftToRight[0].HasObservation("right", "socket.reply") ||
		len(rightToLeft) != 1 || !rightToLeft[0].HasObservation("left", "api.request") {
		t.Fatalf("bidirectional service observations left=%#v right=%#v", leftToRight, rightToLeft)
	}
	if len(result.Poset.ByName("socket.reply")) != 1 || len(result.Poset.ByName("api.request")) != 1 {
		t.Fatal("service connection did not expose both same-named target actions")
	}
	if leftToRight[0].ID == rightToLeft[0].ID ||
		result.Poset.IsCausallyBefore(leftToRight[0].ID, rightToLeft[0].ID) ||
		result.Poset.IsCausallyBefore(rightToLeft[0].ID, leftToRight[0].ID) {
		t.Fatal("independent service action occurrences were merged or falsely ordered")
	}
	encoded, err := result.MarshalCanonical()
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
	replayBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, replayBytes) {
		t.Fatal("bidirectional service connection did not replay byte-identically")
	}

	reversed := bytes.ReplaceAll(source,
		[]byte("left.API to right.Socket;"), []byte("right.socket to left.api;"))
	reversedModel, err := Compile(reversed, "system")
	if err != nil {
		t.Fatal(err)
	}
	reversedDigest, err := reversedModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if reversedDigest != digest {
		t.Fatalf("service connection textual direction/case changed model identity: %s != %s", reversedDigest, digest)
	}

	overloaded, err := Compile([]byte(`
type Protocol is interface
  action out Exchange(Value : Integer);
provides
  Exchange : function(Value : Integer);
end interface Protocol;
type Left is interface service API : Protocol; end interface Left;
type Right is interface service Socket : dual Protocol; end interface Right;
architecture System() is left : Left; right : Right; connect left.API to right.Socket; end architecture System;
`), "System")
	if err != nil {
		t.Fatalf("same-named service action/function elaboration: %v", err)
	}
	overloadedDigest, err := overloaded.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	overloadedResult, err := overloaded.ExecuteDeterministic(arch.NewExecutionJournal(overloadedDigest, 10,
		arch.InputEvent{Key: "exchange", Source: "left", Action: "api.exchange", Params: map[string]any{"value": 4}},
	))
	if err != nil {
		t.Fatal(err)
	}
	overloadedEvents := overloadedResult.Poset.ByName("api.exchange")
	if len(overloadedEvents) != 1 || !overloadedEvents[0].HasObservation("right", "socket.exchange") {
		t.Fatalf("same-named action/function service connection events=%#v", overloadedEvents)
	}
}

func TestIndexedServiceConnectionRecursivelyElaboratesNestedMembers(t *testing.T) {
	source := []byte(`
type Protocol is interface action in Request(Value : Integer); action out Reply(Value : Integer); end interface Protocol;
type Wrapped is interface service Inner : Protocol; end interface Wrapped;
type Left is interface service Port(-1..0) : Wrapped; end interface Left;
type Right is interface service Socket(-1..0) : dual Wrapped; end interface Right;
architecture System() is left : Left; right : Right; connect
  left.Port(-01) to right.Socket(-1);
end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	connection := file.Architectures[0].Connections[0]
	if connection.SourcePattern == nil || connection.SourcePattern.Event.Name != "Port" ||
		connection.Target.Action != "Socket(-1)" {
		t.Fatalf("indexed service connection AST=%#v", connection)
	}
	model, err := CompileFile(file, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{Key: "nested", Source: "right", Action: "socket(-1).inner.request", Params: map[string]any{"value": 3}},
	))
	if err != nil {
		t.Fatal(err)
	}
	events := result.Poset.ByName("socket(-1).inner.request")
	if len(events) != 1 || !events[0].HasObservation("left", "port(-1).inner.request") {
		t.Fatalf("indexed nested service connection events=%#v", events)
	}
	canonical := bytes.ReplaceAll(source, []byte("left.Port(-01)"), []byte("left.port(-1)"))
	canonical = bytes.ReplaceAll(canonical, []byte("right.Socket(-1)"), []byte("right.socket(-1)"))
	canonicalModel, err := Compile(canonical, "system")
	if err != nil {
		t.Fatal(err)
	}
	canonicalDigest, err := canonicalModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if canonicalDigest != digest {
		t.Fatalf("indexed service connection spelling changed model identity: %s != %s", canonicalDigest, digest)
	}
}

func TestFiniteIntegerConnectionGeneratorConnectsIndexedServiceSetsCanonically(t *testing.T) {
	declarations := `
type Protocol is interface
  action in Request(value : Integer);
  action out Reply(value : Integer);
end interface Protocol;
type Wrapped is interface service Inner : Protocol; end interface Wrapped;
type Left is interface service Port(-1..1) : Wrapped; end interface Left;
type Right is interface service Socket(-1..1) : dual Wrapped; end interface Right;
`
	generatedSource := []byte(declarations + `
architecture System() is left : Left; right : Right; connect
  for I : Integer in -1..1 generate
    for J : Integer in I..I generate
      if J /= 0 generate
        left.Port(J).Inner to right.Socket(J).Inner;
      end generate;
    end generate;
  end generate for;
end architecture System;
`)
	explicitSource := []byte(declarations + `
architecture System() is right : Right; left : Left; connect
  left.Port(1).Inner to right.Socket(1).Inner;
  left.Port(-01).Inner to right.Socket(-1).Inner;
end architecture System;
`)
	file, err := Parse(generatedSource)
	if err != nil {
		t.Fatal(err)
	}
	generator := file.Architectures[0].ConnectionGenerators[0]
	if generator.Kind != ConnectionGeneratorForRange || generator.Iterator != "I" ||
		generator.IteratorType != "Integer" || len(generator.Generators) != 1 ||
		generator.Generators[0].Kind != ConnectionGeneratorForRange ||
		generator.Generators[0].Iterator != "J" || len(generator.Generators[0].Generators) != 1 {
		t.Fatalf("finite connection generator AST=%#v", generator)
	}

	generated, err := CompileFile(file, "System")
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
		t.Fatalf("finite generated service topology=%s, explicit=%s", generatedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 20,
		arch.InputEvent{Key: "negative", Source: "right", Action: "socket(-1).inner.request", Params: map[string]any{"value": 4}},
		arch.InputEvent{Key: "zero", Source: "right", Action: "socket(0).inner.request", Params: map[string]any{"value": 5}},
		arch.InputEvent{Key: "positive", Source: "right", Action: "socket(1).inner.request", Params: map[string]any{"value": 6}},
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
		t.Fatalf("finite service generator changed artifact:\ngenerated=%s\nexplicit=%s", generatedArtifact, explicitArtifact)
	}
	for _, index := range []int{-1, 1} {
		name := fmt.Sprintf("socket(%d).inner.request", index)
		events := generatedResult.Poset.ByName(name)
		if len(events) != 1 || !events[0].HasObservation("left", fmt.Sprintf("port(%d).inner.request", index)) {
			t.Fatalf("generated indexed connection %s events=%#v", name, events)
		}
	}
	zero := generatedResult.Poset.ByName("socket(0).inner.request")
	if len(zero) != 1 || zero[0].HasObservation("left", "port(0).inner.request") {
		t.Fatalf("nested false generator connected zero index: %#v", zero)
	}

	original := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(original)
	one, err := Compile(generatedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	oneDigest, _ := one.DeterministicModelDigest()
	oneResult, err := one.ExecuteDeterministic(arch.NewExecutionJournal(oneDigest, 20, journal.Inputs...))
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	four, err := Compile(generatedSource, "system")
	if err != nil {
		t.Fatal(err)
	}
	fourDigest, _ := four.DeterministicModelDigest()
	fourResult, err := four.ExecuteDeterministic(arch.NewExecutionJournal(fourDigest, 20, journal.Inputs...))
	if err != nil {
		t.Fatal(err)
	}
	oneArtifact, _ := oneResult.MarshalCanonical()
	fourArtifact, _ := fourResult.MarshalCanonical()
	if oneDigest != fourDigest || !bytes.Equal(oneArtifact, fourArtifact) {
		t.Fatal("finite service connection generator changed across GOMAXPROCS")
	}
}

func TestFiniteIntegerConnectionGeneratorDescendingRangeIsEmpty(t *testing.T) {
	declarations := `
type Protocol is interface action out Send(); end interface Protocol;
type Left is interface service Port(0..1) : Protocol; end interface Left;
type Right is interface service Socket(0..1) : dual Protocol; end interface Right;
`
	generatedSource := []byte(declarations + `
architecture System() is left : Left; right : Right; connect
  for I : Integer in 1..0 generate
    left.Port(I) to right.Socket(I);
  end for;
end architecture System;
`)
	omittedSource := []byte(declarations + `
architecture System() is right : Right; left : Left; end architecture System;
`)
	generated, err := Compile(generatedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	omitted, err := Compile(omittedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	generatedDigest, _ := generated.DeterministicModelDigest()
	omittedDigest, _ := omitted.DeterministicModelDigest()
	if generatedDigest != omittedDigest {
		t.Fatalf("descending generator topology=%s, omitted=%s", generatedDigest, omittedDigest)
	}
}

func TestConnectionSetGeneratorFansOutAcrossServiceSetMembers(t *testing.T) {
	declarations := `
type Protocol is interface action out Send(value : Integer); action in Ack(value : Integer); end interface Protocol;
type Left is interface service API : Protocol; end interface Left;
type Right is interface service Socket(0..1) : dual Protocol; end interface Right;
`
	generatedSource := []byte(declarations + `
architecture System() is left : Left; right : Right; connect
  left.API to
    for I : Integer in 0..1 generate right.Socket(I) end for;
end architecture System;
`)
	explicitSource := []byte(declarations + `
architecture System() is right : Right; left : Left; connect
  left.API to right.Socket(1);
  left.API to right.Socket(0);
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
		t.Fatalf("generated service fanout topology=%s, explicit=%s", generatedDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(generatedDigest, 10,
		arch.InputEvent{Key: "send", Source: "left", Action: "api.send", Params: map[string]any{"value": 3}},
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
		t.Fatalf("generated service fanout changed artifact:\ngenerated=%s\nexplicit=%s", generatedArtifact, explicitArtifact)
	}
	events := generatedResult.Poset.ByName("api.send")
	if len(events) != 1 || !events[0].HasObservation("right", "socket(0).send") ||
		!events[0].HasObservation("right", "socket(1).send") {
		t.Fatalf("generated service fanout observations=%#v", events)
	}
}

func TestServiceConnectionsRejectNonDualOrSilentlyOmittedConstituents(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		left     string
		right    string
		connect  string
		want     string
	}{
		{
			name: "same polarity", protocol: "action out Send();",
			left: "Protocol", right: "Protocol", connect: "left.API to right.Socket;",
			want: "requires exact dual service types",
		},
		{
			name: "object constituent", protocol: "Token : Integer; action out Send();",
			left: "Protocol", right: "dual Protocol", connect: "left.API to right.Socket;",
			want: "object constituent",
		},
		{
			name: "module generator constituent", protocol: "Spawn : module () return Empty; action out Send();",
			left: "Protocol", right: "dual Protocol", connect: "left.API to right.Socket;",
			want: "module-generator constituent",
		},
		{
			name: "pipe operator", protocol: "action out Send();",
			left: "Protocol", right: "dual Protocol", connect: "left.API => right.Socket;",
			want: "must use Stanford basic 'to' elaboration",
		},
		{
			name: "one service endpoint", protocol: "action out Send();",
			left: "Protocol", right: "dual Protocol", connect: "left.API to right.socket.send;",
			want: "requires service names on both sides",
		},
		{
			name: "duplicate list member", protocol: "action out Send();",
			left: "Protocol", right: "dual Protocol", connect: "left.API, left.API to right.Socket;",
			want: "duplicate connection",
		},
		{
			name: "guarded function object", protocol: "requires Fetch : function(Key : String) return Integer;",
			left: "Protocol", right: "dual Protocol", connect: "left.API where true to right.Socket;",
			want: "conditional object-alias semantics",
		},
		{
			name:     "generated function route collision",
			protocol: "requires Fetch : function(Key : String) return Integer;",
			left:     "Protocol", right: "dual Protocol",
			connect: "left.API to right.Socket; left.api.fetch to right.socket.fetch;",
			want:    "is connected more than once",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Empty is interface end interface Empty;
type Protocol is interface ` + test.protocol + ` end interface Protocol;
type Left is interface service API : ` + test.left + `; end interface Left;
type Right is interface service Socket : ` + test.right + `; end interface Right;
architecture System() is left : Left; right : Right; connect ` + test.connect + ` end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestServiceConnectionsAreStableAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Protocol is interface
  action in Request(Value : Integer); action out Reply(Value : Integer);
provides Store : function(Item : Integer);
requires Fetch : function(Key : String) return Integer;
end interface Protocol;
type Left is interface service API : Protocol; end interface Left;
type Right is interface service Socket : dual Protocol; end interface Right;
architecture System() is left : Left; right : Right; connect left.API to right.Socket; end architecture System;
`),
		[]byte(`
type Protocol is interface
requires fetch : function(key : string) return integer;
provides store : function(item : integer);
  action out reply(value : integer); action in request(value : integer);
end interface Protocol;
type Right is interface service socket : dual Protocol; end interface Right;
type Left is interface service api : Protocol; end interface Left;
architecture System() is right : Right; left : Left; connect right.socket to left.api; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baselineDigest string
	var baselineArtifact []byte
	for iteration := 0; iteration < 12; iteration++ {
		if iteration < 6 {
			runtime.GOMAXPROCS(1)
		} else {
			runtime.GOMAXPROCS(4)
		}
		model, err := Compile(sources[iteration%len(sources)], "system")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
			arch.InputEvent{Key: "left", Source: "left", Action: "api.reply", Params: map[string]any{"value": 1}},
			arch.InputEvent{Key: "right", Source: "right", Action: "socket.request", Params: map[string]any{"value": 2}},
		))
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := result.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if baselineDigest == "" {
			baselineDigest = digest
			baselineArtifact = artifact
			continue
		}
		if digest != baselineDigest || !bytes.Equal(artifact, baselineArtifact) {
			t.Fatalf("service connection order/case/GOMAXPROCS changed result: digest %s/%s\nbase=%s\n got=%s",
				baselineDigest, digest, baselineArtifact, artifact)
		}
	}
}

func TestParseAndCompileBasicServiceStructuralRewrite(t *testing.T) {
	source := []byte(`
type Worker is interface action out Done(); end interface Worker;
type Protocol is interface
  action in Request(Value : Integer);
  action out Reply(Value : Integer);
provides
  Read : function() return Integer;
  Spawn : module () return Worker;
requires
  Write : function(Value : Integer);
  External : module () return Worker;
end interface Protocol;
type Gateway is interface
  service
    API : Protocol;
end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Interfaces) != 3 || len(file.Interfaces[2].Services) != 1 {
		t.Fatalf("service AST=%#v", file.Interfaces)
	}
	service := file.Interfaces[2].Services[0]
	if service.Name != "API" || service.Type != "Protocol" || service.TypeExpression.Kind != TypeExpressionName {
		t.Fatalf("service declaration=%#v", service)
	}

	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	component, ok := model.Component("gateway")
	if !ok {
		t.Fatal("compiled gateway component is absent")
	}
	got, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("gateway structural descriptor is absent")
	}
	integerType := mustRootRapidePredefinedType(t, "Integer")
	valueEvent := mustRootRapideEventType(t, gorapide.RapideEventParam("Value", integerType))
	readType := mustRootRapideFunctionType(t, nil, integerType)
	writeType := mustRootRapideFunctionType(t, []gorapide.RapideFunctionParameter{
		gorapide.RapideObjectParameter("Value", integerType),
	}, gorapide.RapideType{})
	worker := mustRootRapideInterfaceType(t, gorapide.OutputRapideAction("Done", mustRootRapideEventType(t)))
	generatorType := mustRootRapideFunctionType(t, nil, worker)
	want := mustRootRapideInterfaceType(t,
		gorapide.InputRapideAction("api.request", valueEvent),
		gorapide.OutputRapideAction("api.reply", valueEvent),
		gorapide.ProvidedRapideMember("api.read", readType),
		gorapide.RequiredRapideMember("api.write", writeType),
		gorapide.ProvidedRapideModuleGenerator("api.spawn", generatorType),
		gorapide.RequiredRapideModuleGenerator("api.external", generatorType),
	)
	gotBytes, err := got.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := want.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("source service structural rewrite:\nwant %s\n got %s", wantBytes, gotBytes)
	}
}

func TestNestedAndDerivedServicesRewriteNamesRecursively(t *testing.T) {
	nestedSource := []byte(`
type Protocol is interface action in Request(); end interface Protocol;
type Wrapped is interface service Inner : Protocol; end interface Wrapped;
type Gateway is interface service API : Wrapped; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;
`)
	model, err := Compile(nestedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	component, _ := model.Component("gateway")
	got, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("nested service structural descriptor is absent")
	}
	want := mustRootRapideInterfaceType(t,
		gorapide.InputRapideAction("api.inner.request", mustRootRapideEventType(t)),
	)
	gotBytes, _ := got.MarshalCanonical()
	wantBytes, _ := want.MarshalCanonical()
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("nested service rewrite:\nwant %s\n got %s", wantBytes, gotBytes)
	}

	derivedSource := []byte(`
type Protocol is interface action in Request(); end interface Protocol;
type Base is interface service API(-1..1) : dual Protocol; end interface Base;
type Derived is include Base replace (API to External); interface end interface Derived;
architecture System() is gateway : Derived; end architecture System;
`)
	flatSource := []byte(`
type Protocol is interface action in Request(); end interface Protocol;
type Derived is interface service External(-1..1) : dual Protocol; end interface Derived;
architecture System() is gateway : Derived; end architecture System;
`)
	derived, err := Compile(derivedSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	flat, err := Compile(flatSource, "System")
	if err != nil {
		t.Fatal(err)
	}
	derivedDigest, _ := derived.DeterministicModelDigest()
	flatDigest, _ := flat.DeterministicModelDigest()
	if derivedDigest != flatDigest {
		t.Fatalf("derived service model %s != flat model %s", derivedDigest, flatDigest)
	}
}

func TestParseAndCompileIntegerRangeServiceSets(t *testing.T) {
	source := []byte(`
type Protocol is interface
  action in Take();
  action out Give();
  provides Source : Integer;
  requires Sink : Integer;
end interface Protocol;
type Gateway is interface
  service Port(-1..1) : Protocol;
  service Socket(2..2) : dual Protocol;
end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	services := file.Interfaces[1].Services
	if len(services) != 2 || !services[0].IntegerSet || services[0].FirstIndex != -1 ||
		services[0].LastIndex != 1 || services[0].Dual || !services[1].IntegerSet ||
		services[1].FirstIndex != 2 || services[1].LastIndex != 2 || !services[1].Dual {
		t.Fatalf("Integer service-set AST=%#v", services)
	}
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	component, _ := model.Component("gateway")
	got, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("Integer service-set structural descriptor is absent")
	}
	integerType := mustRootRapidePredefinedType(t, "Integer")
	eventType := mustRootRapideEventType(t)
	wantMembers := make([]gorapide.RapideInterfaceMember, 0, 16)
	for _, index := range []int{-1, 0, 1} {
		prefix := fmt.Sprintf("port(%d).", index)
		wantMembers = append(wantMembers,
			gorapide.InputRapideAction(prefix+"take", eventType),
			gorapide.OutputRapideAction(prefix+"give", eventType),
			gorapide.ProvidedRapideMember(prefix+"source", integerType),
			gorapide.RequiredRapideMember(prefix+"sink", integerType),
		)
	}
	wantMembers = append(wantMembers,
		gorapide.OutputRapideAction("socket(2).take", eventType),
		gorapide.InputRapideAction("socket(2).give", eventType),
		gorapide.RequiredRapideMember("socket(2).source", integerType),
		gorapide.ProvidedRapideMember("socket(2).sink", integerType),
	)
	want := mustRootRapideInterfaceType(t, wantMembers...)
	gotBytes, _ := got.MarshalCanonical()
	wantBytes, _ := want.MarshalCanonical()
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("source Integer service-set rewrite:\nwant %s\n got %s", wantBytes, gotBytes)
	}

	empty, err := Compile([]byte(`
type Protocol is interface action in Take(); end interface Protocol;
type Gateway is interface service None(1..0) : Protocol; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;
`), "System")
	if err != nil {
		t.Fatal(err)
	}
	emptyComponent, _ := empty.Component("gateway")
	emptyType, ok := emptyComponent.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("descending service-set structural descriptor is absent")
	}
	emptyWant := mustRootRapideInterfaceType(t)
	emptyBytes, _ := emptyType.MarshalCanonical()
	emptyWantBytes, _ := emptyWant.MarshalCanonical()
	if !bytes.Equal(emptyBytes, emptyWantBytes) {
		t.Fatalf("descending source service set is not empty:\nwant %s\n got %s", emptyWantBytes, emptyBytes)
	}
}

func TestSourceServicePreservesRecursiveObjectTypeAndRejectsContainmentCycles(t *testing.T) {
	source := []byte(`
type Protocol is interface
  Next : Protocol;
  action out Done();
end interface Protocol;
type Gateway is interface service API : Protocol; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	component, _ := model.Component("gateway")
	got, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("recursive service structural descriptor is absent")
	}
	recursive, err := gorapide.NewSelfRecursiveRapideInterfaceType(func(self gorapide.RapideType) (gorapide.RapideType, error) {
		return gorapide.NewRapideInterfaceType(
			gorapide.ProvidedRapideMember("Next", self),
			gorapide.OutputRapideAction("Done", mustRootRapideEventType(t)),
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := mustRootRapideInterfaceType(t,
		gorapide.ProvidedRapideMember("api.next", recursive),
		gorapide.OutputRapideAction("api.done", mustRootRapideEventType(t)),
	)
	gotBytes, _ := got.MarshalCanonical()
	wantBytes, _ := want.MarshalCanonical()
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("source recursive service member type:\nwant %s\n got %s", wantBytes, gotBytes)
	}

	emptySelf, err := Compile([]byte(`
type EmptySelf is interface service None(1..0) : EmptySelf; end interface EmptySelf;
architecture System() is empty : EmptySelf; end architecture System;
`), "System")
	if err != nil {
		t.Fatalf("empty self-indexed service set should have a finite rewrite: %v", err)
	}
	if _, err := emptySelf.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}

	cycles := []struct {
		source string
		want   string
	}{
		{
			source: `type Recursive is interface service Again : Recursive; end interface Recursive;
architecture System() is item : Recursive; end architecture System;`,
			want: "service expansion cycle Recursive -> Recursive has no finite service-free rewrite",
		},
		{
			source: `type A is interface service ToB : B; end interface A;
type B is interface service ToA : A; end interface B;
architecture System() is item : A; end architecture System;`,
			want: "service expansion cycle A -> B -> A has no finite service-free rewrite",
		},
		{
			source: `type B is interface service ToA : A; end interface B;
type A is interface service ToB : B; end interface A;
architecture System() is item : A; end architecture System;`,
			want: "service expansion cycle A -> B -> A has no finite service-free rewrite",
		},
		{
			source: `type AliasB is B;
type A is interface service ToB : AliasB; end interface A;
type B is interface service ToA : A; end interface B;
architecture System() is item : A; end architecture System;`,
			want: "service expansion cycle A -> B -> A has no finite service-free rewrite",
		},
	}
	for _, test := range cycles {
		_, err := Compile([]byte(test.source), "System")
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("cycle error=%v, want %q", err, test.want)
		}
	}
}

func TestParseAndCompileDualServicesAndDoubleDual(t *testing.T) {
	source := []byte(`
type Protocol is interface
  action in Take();
  action out Give();
  provides Source : Integer;
  requires Sink : Integer;
end interface Protocol;
type Socket is interface service Inner : dual Protocol; end interface Socket;
type Gateway is interface
  service Direct : dual Protocol;
  service Double : dual Socket;
end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Interfaces) != 3 || len(file.Interfaces[1].Services) != 1 ||
		!file.Interfaces[1].Services[0].Dual || len(file.Interfaces[2].Services) != 2 ||
		!file.Interfaces[2].Services[0].Dual || !file.Interfaces[2].Services[1].Dual {
		t.Fatalf("dual service AST=%#v", file.Interfaces)
	}
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	component, _ := model.Component("gateway")
	got, ok := component.Interface.StructuralRapideType()
	if !ok {
		t.Fatal("dual service structural descriptor is absent")
	}
	integerType := mustRootRapidePredefinedType(t, "Integer")
	eventType := mustRootRapideEventType(t)
	want := mustRootRapideInterfaceType(t,
		gorapide.OutputRapideAction("direct.take", eventType),
		gorapide.InputRapideAction("direct.give", eventType),
		gorapide.RequiredRapideMember("direct.source", integerType),
		gorapide.ProvidedRapideMember("direct.sink", integerType),
		gorapide.InputRapideAction("double.inner.take", eventType),
		gorapide.OutputRapideAction("double.inner.give", eventType),
		gorapide.ProvidedRapideMember("double.inner.source", integerType),
		gorapide.RequiredRapideMember("double.inner.sink", integerType),
	)
	gotBytes, _ := got.MarshalCanonical()
	wantBytes, _ := want.MarshalCanonical()
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("source dual service rewrite:\nwant %s\n got %s", wantBytes, gotBytes)
	}
}

func TestSourceServicesAreCanonicalAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Protocol is interface action in Request(Value : Integer); provides Read : function() return Integer; end interface Protocol;
type Gateway is interface service API : Protocol; Port(-1..1) : Protocol; Socket : dual Protocol; Admin : Protocol; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;
`),
		[]byte(`
type Protocol is interface provides read : function() return integer; action in request(value : integer); end interface Protocol;
type Gateway is interface service port(-1..1) : Protocol; socket : dual Protocol; admin : Protocol; api : Protocol; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 12; iteration++ {
		if iteration == 0 {
			runtime.GOMAXPROCS(1)
		} else if iteration == 6 {
			runtime.GOMAXPROCS(4)
		}
		model, err := Compile(sources[iteration%len(sources)], "system")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if baseline == "" {
			baseline = digest
		} else if digest != baseline {
			t.Fatalf("service source order/case/GOMAXPROCS changed digest: %s != %s", baseline, digest)
		}
	}
}

func TestSourceServicesRejectUnsupportedOrForbiddenForms(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "empty part",
			source: `type Gateway is interface service end interface Gateway;`,
			want:   "interface service part requires a service declaration",
		},
		{
			name: "enumeration service set",
			source: `type Protocol is interface end interface Protocol;
type Gateway is interface service API(Color) : Protocol; end interface Gateway;`,
			want: "enumeration-indexed service sets are outside the current source subset",
		},
		{
			name: "oversized service set",
			source: `type Protocol is interface end interface Protocol;
type Gateway is interface service API(0..256) : Protocol; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;`,
			want: "cardinality exceeds deterministic limit 256",
		},
		{
			name: "noninterface target",
			source: `type Gateway is interface service API : Integer; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;`,
			want: `service "API" structural rewrite: invalid Rapide type: service "API" type is not an interface`,
		},
		{
			name: "empty set still typechecks target",
			source: `type Gateway is interface service None(1..0) : Integer; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;`,
			want: `service "None" structural rewrite: invalid Rapide type: service "None" type is not an interface`,
		},
		{
			name: "private action",
			source: `type Protocol is interface private action Hidden(); end interface Protocol;
type Gateway is interface service API : Protocol; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;`,
			want: `contains a forbidden private action "Hidden"`,
		},
		{
			name: "private object",
			source: `type Protocol is interface private Secret : Integer; end interface Protocol;
type Gateway is interface service API : Protocol; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;`,
			want: "contains private object",
		},
		{
			name: "type name",
			source: `type Protocol is interface type Item; end interface Protocol;
type Gateway is interface service API : Protocol; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;`,
			want: "contains forbidden provides type name",
		},
		{
			name: "service-expanded provided function without implementation",
			source: `type Protocol is interface provides Work : function(); end interface Protocol;
type Gateway is interface service API : Protocol; end interface Gateway;
module Impl() return Gateway is end module Impl;
architecture System() is gateway : Gateway is Impl(); end architecture System;`,
			want: `provides function "api.work" with 0 matching implementations, want 1`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestServiceBehaviorIsCheckedAsSuperPosetConstraint(t *testing.T) {
	source := []byte(`
type Driver is interface action out Send(value : Integer); end interface Driver;
type Protocol is interface
  action in Request(value : Integer);
  action out Reply(value : Integer);
  behavior begin
    (?Value : Integer) Request(?Value) => Reply(?Value);;
end interface Protocol;
type Gateway is interface service API : Protocol; end interface Gateway;
architecture System() is driver : Driver; gateway : Gateway; connect
  driver.Send to gateway.API.Request;
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
	execute := func(reply *arch.InputEvent) *arch.ExecutionResult {
		inputs := []arch.InputEvent{{
			Key: "request", Source: "driver", Action: "Send",
			Params: map[string]any{"value": 7},
		}}
		if reply != nil {
			inputs = append(inputs, *reply)
		}
		result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20, inputs...))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	passing := execute(&arch.InputEvent{
		Key: "reply", Source: "gateway", Action: "api.reply",
		Params: map[string]any{"value": 7}, Causes: []string{"request"},
	})
	if len(passing.ModuleConstraints) != 1 || passing.ModuleConstraints[0].ComponentID != "gateway" ||
		!passing.ModuleConstraints[0].Report.Passed || len(passing.ModuleConstraints[0].Report.Reports) != 1 {
		t.Fatalf("passing service behavior report=%#v", passing.ModuleConstraints)
	}

	missing := execute(nil)
	if len(missing.ModuleConstraints) != 1 || missing.ModuleConstraints[0].Report.Passed ||
		len(missing.ModuleConstraints[0].Report.Reports) != 1 ||
		len(missing.ModuleConstraints[0].Report.Reports[0].Violations) != 1 {
		t.Fatalf("missing service behavior report=%#v", missing.ModuleConstraints)
	}
	violation := missing.ModuleConstraints[0].Report.Reports[0].Violations[0]
	if violation.Kind != "MustMatch" || violation.Clause != "super-poset" ||
		!strings.Contains(violation.Message, "causal-order-preserving super-poset") {
		t.Fatalf("service behavior violation=%#v", violation)
	}

	independent := execute(&arch.InputEvent{
		Key: "reply", Source: "gateway", Action: "api.reply",
		Params: map[string]any{"value": 7},
	})
	if independent.ModuleConstraints[0].Report.Passed {
		t.Fatal("causally independent reply unexpectedly conformed to Request -> Reply behavior")
	}
	wrongValue := execute(&arch.InputEvent{
		Key: "reply", Source: "gateway", Action: "api.reply",
		Params: map[string]any{"value": 8}, Causes: []string{"request"},
	})
	if wrongValue.ModuleConstraints[0].Report.Passed {
		t.Fatal("wrong-valued reply unexpectedly conformed to behavior")
	}

	artifact, err := passing.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := passing.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"value": 7}},
		arch.InputEvent{Key: "reply", Source: "gateway", Action: "api.reply", Params: map[string]any{"value": 7}, Causes: []string{"request"}},
	), artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayArtifact, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, replayArtifact) {
		t.Fatal("service behavior conformance report did not replay byte-identically")
	}
}

func TestServiceBehaviorConstraintPreservesDualRewrite(t *testing.T) {
	source := []byte(`
type Responder is interface action out Reply(value : Integer); end interface Responder;
type Protocol is interface
  action in Request(value : Integer); action out Reply(value : Integer);
  behavior begin (?Value : Integer) Request(?Value) => Reply(?Value);; end interface Protocol;
type Client is interface service API : dual Protocol; end interface Client;
architecture System() is client : Client; responder : Responder; connect
  responder.Reply to client.API.Reply;
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
		arch.InputEvent{Key: "request", Source: "client", Action: "api.request", Params: map[string]any{"value": 4}},
		arch.InputEvent{Key: "reply", Source: "responder", Action: "Reply", Params: map[string]any{"value": 4}, Causes: []string{"request"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ModuleConstraints) != 1 || result.ModuleConstraints[0].ComponentID != "client" ||
		!result.ModuleConstraints[0].Report.Passed {
		t.Fatalf("dual service behavior report=%#v", result.ModuleConstraints)
	}
}

func TestIndexedNestedServiceBehaviorConstraintIsQualifiedRecursively(t *testing.T) {
	source := []byte(`
type Driver is interface action out Send(value : Integer); end interface Driver;
type Protocol is interface
  action in Request(value : Integer); action out Reply(value : Integer);
  behavior begin (?Value : Integer) Request(?Value) => Reply(?Value);; end interface Protocol;
type Wrapped is interface service Inner : Protocol; end interface Wrapped;
type Gateway is interface service Ports(-1..0) : Wrapped; end interface Gateway;
architecture System() is driver : Driver; gateway : Gateway; connect
  driver.Send to gateway.Ports(-01).Inner.Request;
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
		arch.InputEvent{Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"value": 9}},
		arch.InputEvent{Key: "reply", Source: "gateway", Action: "ports(-1).inner.reply", Params: map[string]any{"value": 9}, Causes: []string{"request"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ModuleConstraints) != 1 || !result.ModuleConstraints[0].Report.Passed ||
		len(result.ModuleConstraints[0].Report.Reports) != 2 {
		// Ports(-1).Inner and Ports(0).Inner are two finite service instances;
		// the unused instance has an empty behavior computation and also passes.
		t.Fatalf("nested indexed behavior reports=%#v", result.ModuleConstraints)
	}
	for _, report := range result.ModuleConstraints[0].Report.Reports {
		if !report.Passed {
			t.Fatalf("nested indexed member report=%#v", report)
		}
	}
}

func TestServiceBehaviorConstraintStableAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Driver is interface action out send(value : Integer); end interface Driver;
type Protocol is interface action in Request(value : Integer); action out Reply(value : Integer);
behavior begin (?Value : Integer) Request(?Value) => Reply(?Value);; end interface Protocol;
type Gateway is interface service API : Protocol; end interface Gateway;
architecture System() is driver : Driver; gateway : Gateway; connect driver.send to gateway.API.Request; end architecture System;
`),
		[]byte(`
type Protocol is interface action out reply(value : integer); action in request(value : integer);
behavior begin (?value : integer) request(?value) => reply(?value);; end interface Protocol;
type Gateway is interface service api : protocol; end interface Gateway;
type Driver is interface action out send(value : integer); end interface Driver;
architecture System() is gateway : gateway; driver : driver; connect driver.send to gateway.api.request; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baselineDigest string
	var baselineArtifact []byte
	for iteration := 0; iteration < 12; iteration++ {
		if iteration < 6 {
			runtime.GOMAXPROCS(1)
		} else {
			runtime.GOMAXPROCS(4)
		}
		model, err := Compile(sources[iteration%len(sources)], "system")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
			arch.InputEvent{Key: "request", Source: "driver", Action: "send", Params: map[string]any{"value": 7}},
			arch.InputEvent{Key: "reply", Source: "gateway", Action: "api.reply", Params: map[string]any{"value": 7}, Causes: []string{"request"}},
		))
		if err != nil {
			t.Fatal(err)
		}
		artifact, err := result.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if baselineDigest == "" {
			baselineDigest, baselineArtifact = digest, artifact
			continue
		}
		if digest != baselineDigest || !bytes.Equal(artifact, baselineArtifact) {
			t.Fatalf("service behavior order/case/GOMAXPROCS changed result: digest %s/%s\nbase=%s\n got=%s",
				baselineDigest, digest, baselineArtifact, artifact)
		}
	}
}

func TestServiceBehaviorConstraintRejectsUnboundedOrEffectfulSubset(t *testing.T) {
	tests := []struct {
		name         string
		actions      string
		constituents string
		behavior     string
		want         string
	}{
		{name: "state", behavior: "Count : var Integer := 0; begin Request => Reply();;", want: "behavior state is outside"},
		{name: "function", constituents: "provides F : function() return Integer;", behavior: "F : function() return Integer is begin return 1; end function F; begin Request => Reply();;", want: "behavior functions are outside"},
		{name: "compound", behavior: "begin (Request ~ Request) => Reply();;", want: "compound triggers are outside"},
		{name: "statement", behavior: "begin Request => if true then Reply(); end if;;", want: "statement \"if\" is outside"},
		{name: "cycle", actions: "action out Request(); action out Reply();", behavior: "begin Request => Reply();; Reply => Request();;", want: "behavior action cycle reply -> request -> reply"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actions := test.actions
			if actions == "" {
				actions = "action in Request(); action out Reply();"
			}
			source := []byte(`
type Protocol is interface ` + actions + ` ` + test.constituents + ` behavior ` + test.behavior + ` end interface Protocol;
type Gateway is interface service API : Protocol; end interface Gateway;
architecture System() is gateway : Gateway; end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}
