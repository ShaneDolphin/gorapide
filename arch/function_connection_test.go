package arch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func remoteFunctionArchitecture(t *testing.T, reverseComponents, reverseConnections bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("remote-functions")
	client := NewComponent("client", Interface("Client").
		OutAction("Start", P("key", "String")).
		OutAction("Done", P("value", "Integer")).
		OutAction("SawReturn", P("value", "Integer")).
		RequiresFunction("Lookup", "Integer", P("key", "String")).
		RequiresFunction("Health", "Boolean").
		Build(), nil)
	provider := NewComponent("provider", Interface("Provider").
		OutAction("ProviderBody", P("key", "String"), P("count", "Integer")).
		OutAction("SawCall", P("key", "String")).
		ProvidesFunction("Fetch", "Integer", P("item", "String")).
		ProvidesFunction("Status", "Boolean").
		Build(), nil)
	if err := client.DeclareState(StateReference("result", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	if err := provider.DeclareState(StateReference("count", "Integer", 1)); err != nil {
		t.Fatal(err)
	}
	if err := provider.AddFunctionImplementation(Function("Fetch", "Integer", P("item", "String")).
		Do(
			SetState("count", AddValues(ReadState("count"), LiteralValue(1))),
			CallAction("body", "ProviderBody",
				BindingParam("key", "item"), StateParam("count", "count")),
		).
		Returns(AddValues(ReadState("count"), LiteralValue(40))).
		Build()); err != nil {
		t.Fatal(err)
	}
	if err := provider.AddFunctionImplementation(Function("Status", "Boolean").
		Returns(LiteralValue(true)).Build()); err != nil {
		t.Fatal(err)
	}
	key := pattern.Var("K").WithType("String")
	if err := client.AddDeclarativeRule(Rule("invoke").
		On(pattern.MatchEvent("Start").BindParam("key", key)).
		Do(
			CallFunctionInto("lookup", "result", "Lookup", BindingParam("key", "K")),
			CallAction("done", "Done", StateParam("value", "result")),
		).Build()); err != nil {
		t.Fatal(err)
	}
	returned := pattern.Var("R").WithType("Integer")
	if err := client.AddDeclarativeRule(Rule("observe-return").
		On(pattern.MatchEvent("Lookup'Return").BindParam("Return", returned)).
		Do(CallAction("saw-return", "SawReturn", BindingParam("value", "R"))).
		Build()); err != nil {
		t.Fatal(err)
	}
	calledKey := pattern.Var("CalledKey").WithType("String")
	if err := provider.AddDeclarativeRule(Rule("observe-call").
		On(pattern.MatchEvent("Fetch'Call").BindParam("item", calledKey)).
		Do(CallAction("saw-call", "SawCall", BindingParam("key", "CalledKey"))).
		Build()); err != nil {
		t.Fatal(err)
	}
	components := []*Component{client, provider}
	if reverseComponents {
		components[0], components[1] = components[1], components[0]
	}
	for _, component := range components {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	connections := []*FunctionConnection{
		ConnectFunction("client", "Lookup", "provider", "Fetch").IdentifiedBy("lookup-route").Build(),
		ConnectFunction("client", "Health", "provider", "Status").IdentifiedBy("health-route").Build(),
	}
	if reverseConnections {
		connections[0], connections[1] = connections[1], connections[0]
	}
	for _, connection := range connections {
		if err := architecture.AddFunctionConnection(connection); err != nil {
			t.Fatal(err)
		}
	}
	return architecture
}

func TestRemoteFunctionConnectionSharesOccurrencesAndExecutesProvider(t *testing.T) {
	architecture := remoteFunctionArchitecture(t, false, false)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 20, InputEvent{
		Key: "start", Source: "client", Action: "Start", Params: map[string]any{"key": "alpha"},
	})
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}

	start := onlySourceNamedEvent(t, result.Poset, "client", "Start")
	requiredCall := onlyNamedEvent(t, result.Poset, "Lookup'Call")
	providedCall := onlyNamedEvent(t, result.Poset, "Fetch'Call")
	body := onlyNamedEvent(t, result.Poset, "ProviderBody")
	providedReturn := onlyNamedEvent(t, result.Poset, "Fetch'Return")
	requiredReturn := onlyNamedEvent(t, result.Poset, "Lookup'Return")
	done := onlyNamedEvent(t, result.Poset, "Done")
	sawCall := onlyNamedEvent(t, result.Poset, "SawCall")
	sawReturn := onlyNamedEvent(t, result.Poset, "SawReturn")

	if requiredCall.ID != providedCall.ID {
		t.Fatalf("call views have different occurrence IDs: %s != %s", requiredCall.ID, providedCall.ID)
	}
	if providedReturn.ID != requiredReturn.ID {
		t.Fatalf("return views have different occurrence IDs: %s != %s", providedReturn.ID, requiredReturn.ID)
	}
	if result.Poset.Len() != 8 {
		t.Fatalf("poset has %d occurrences, want architecture Start plus 7; qualified function views must not duplicate occurrences", result.Poset.Len())
	}
	if requiredCall.Source != "client" || providedCall.Source != "provider" ||
		providedReturn.Source != "provider" || requiredReturn.Source != "client" {
		t.Fatalf("qualified function sources call=%s/%s return=%s/%s",
			requiredCall.Source, providedCall.Source, providedReturn.Source, requiredReturn.Source)
	}
	for _, event := range []*gorapide.Event{requiredCall, requiredReturn} {
		if event.ParamString("key") != "alpha" {
			t.Fatalf("%s key=%q, want alpha", event.Name, event.ParamString("key"))
		}
	}
	for _, event := range []*gorapide.Event{providedCall, providedReturn} {
		if event.ParamString("item") != "alpha" {
			t.Fatalf("%s item=%q, want alpha", event.Name, event.ParamString("item"))
		}
	}
	for _, event := range []*gorapide.Event{providedReturn, requiredReturn, done, sawReturn} {
		value, ok := event.Param(map[string]string{
			"Fetch'Return": "Return", "Lookup'Return": "Return", "Done": "value", "SawReturn": "value",
		}[event.Name])
		if !ok || value != int64(42) {
			t.Fatalf("%s result=%#v,%v, want int64(42)", event.Name, value, ok)
		}
	}
	if sawCall.ParamString("key") != "alpha" {
		t.Fatalf("provider did not observe the provided call view: %#v", sawCall.Params)
	}
	for _, edge := range [][2]*gorapide.Event{
		{start, requiredCall}, {providedCall, body}, {body, providedReturn},
		{requiredReturn, done}, {providedCall, sawCall}, {requiredReturn, sawReturn},
	} {
		if !result.Poset.IsCausallyBefore(edge[0].ID, edge[1].ID) {
			t.Fatalf("%s is not causally before %s", edge[0].Name, edge[1].Name)
		}
	}
	if !result.Poset.IsCausallyIndependent(body.ID, sawCall.ID) {
		t.Fatal("observing the provided Call introduced a false edge to the function body")
	}
	if !result.Poset.IsCausallyIndependent(done.ID, sawReturn.ID) {
		t.Fatal("observing the required Return introduced a false edge to caller continuation")
	}

	var providerWrite, callerWrite bool
	for _, firing := range result.Firings {
		for _, read := range firing.StateReads {
			if read.ComponentID == "" {
				t.Fatalf("unqualified state read in firing %#v", firing)
			}
		}
		for _, write := range firing.StateWrites {
			switch {
			case write.ComponentID == "provider" && write.Name == "count" && write.Value.Text == "2":
				providerWrite = true
			case write.ComponentID == "client" && write.Name == "result" && write.Value.Text == "42" &&
				len(write.Causes) == 1 && write.Causes[0] == string(requiredReturn.ID):
				callerWrite = true
			}
		}
	}
	if !providerWrite || !callerWrite {
		t.Fatalf("cross-component state audit missing provider/caller writes: %#v", result.Firings)
	}
	if len(result.State) != 2 ||
		result.State[0].ComponentID != "client" || result.State[0].Value.Text != "42" ||
		result.State[1].ComponentID != "provider" || result.State[1].Value.Text != "2" {
		t.Fatalf("final remote-function state=%#v", result.State)
	}

	expectedDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, expectedDigest)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("remote function replay changed canonical artifact bytes")
	}
}

func TestFunctionConnectionScopeIsCanonicalAndModuleScopeRejectsCrossComponent(t *testing.T) {
	build := func(module bool) *Architecture {
		t.Helper()
		architecture := NewArchitecture("function-connection-scope")
		component := NewComponent("worker", Interface("Worker").
			RequiresFunction("Need", "Integer", P("value", "Integer")).
			ProvidesFunction("Offer", "Integer", P("value", "Integer")).Build(), nil)
		if err := component.AddFunctionImplementation(Function("Offer", "Integer", P("value", "Integer")).
			Returns(BoundValue("value")).Build()); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		builder := ConnectFunction("worker", "Need", "worker", "Offer").IdentifiedBy("route")
		if module {
			builder.WithinModule()
		}
		if err := architecture.AddFunctionConnection(builder.Build()); err != nil {
			t.Fatal(err)
		}
		return architecture
	}
	architectureDigest, err := build(false).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	moduleDigest, err := build(true).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if architectureDigest == moduleDigest {
		t.Fatal("function connection scope is absent from canonical model identity")
	}

	cross := NewArchitecture("cross-module-function-route")
	client := NewComponent("client", Interface("Client").RequiresFunction("Need", "").Build(), nil)
	provider := NewComponent("provider", Interface("Provider").ProvidesFunction("Offer", "").Build(), nil)
	if err := provider.AddFunctionImplementation(Function("Offer", "").Build()); err != nil {
		t.Fatal(err)
	}
	for _, component := range []*Component{client, provider} {
		if err := cross.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	if err := cross.AddFunctionConnection(ConnectFunction("client", "Need", "provider", "Offer").
		WithinModule().IdentifiedBy("cross").Build()); err != nil {
		t.Fatal(err)
	}
	if _, err := cross.DeterministicModelDigest(); err == nil ||
		!strings.Contains(err.Error(), "identical component source and target") {
		t.Fatalf("cross-component module function connection error=%v", err)
	}
}

func onlyNamedEvent(t *testing.T, poset *gorapide.Poset, name string) *gorapide.Event {
	t.Helper()
	events := poset.ByName(name)
	if len(events) != 1 {
		t.Fatalf("%s occurrences=%d, want 1", name, len(events))
	}
	return events[0]
}

func onlySourceNamedEvent(t *testing.T, poset *gorapide.Poset, source, name string) *gorapide.Event {
	t.Helper()
	matches := poset.ByName(name)
	selected := make(gorapide.EventSet, 0, 1)
	for _, event := range matches {
		if event.HasObservation(source, name) {
			selected = append(selected, event)
		}
	}
	if len(selected) != 1 {
		t.Fatalf("%s.%s occurrences=%d, want 1", source, name, len(selected))
	}
	return selected[0]
}

func TestRemoteFunctionConnectionsAreDeclarationOrderIndependent(t *testing.T) {
	left := remoteFunctionArchitecture(t, false, false)
	right := remoteFunctionArchitecture(t, true, true)
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("component/route order changed model identity: %s != %s", leftDigest, rightDigest)
	}
	leftResult, err := left.ExecuteDeterministic(NewExecutionJournal(leftDigest, 20, InputEvent{
		Key: "start", Source: "client", Action: "Start", Params: map[string]any{"key": "alpha"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	rightResult, err := right.ExecuteDeterministic(NewExecutionJournal(rightDigest, 20, InputEvent{
		Key: "start", Source: "client", Action: "Start", Params: map[string]any{"key": "alpha"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	leftBytes, _ := leftResult.MarshalCanonical()
	rightBytes, _ := rightResult.MarshalCanonical()
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatal("component/route order changed remote-function artifact bytes")
	}
}

func TestNestedFunctionConnectionScopeIsCanonicalAndClosed(t *testing.T) {
	childInterface := Interface("Child").Build()
	newArchitecture := func(t *testing.T) (*Architecture, string, string) {
		t.Helper()
		architecture := NewArchitecture("nested-function-scope")
		if err := architecture.AddDeterministicArchitectureInstance(ArchitectureInstance(
			"child", "ChildArchitecture", childInterface,
		)); err != nil {
			t.Fatal(err)
		}
		clientID := DeterministicArchitectureComponentID("child", "client")
		providerID := DeterministicArchitectureComponentID("child", "provider")
		client := NewComponent(clientID, Interface("Client").
			RequiresFunction("Lookup", "Integer", P("n", "Integer")).Build(), nil)
		provider := NewComponent(providerID, Interface("Provider").
			ProvidesFunction("Fetch", "Integer", P("value", "Integer")).Build(), nil)
		for _, component := range []*Component{provider, client} {
			if err := architecture.AddComponentInArchitecture(component, "child"); err != nil {
				t.Fatal(err)
			}
		}
		return architecture, clientID, providerID
	}

	architecture, clientID, providerID := newArchitecture(t)
	connection := ConnectFunction(clientID, "Lookup", providerID, "Fetch").
		WithinArchitecture("child").IdentifiedBy("child/lookup").Build()
	if err := architecture.AddFunctionConnection(connection); err != nil {
		t.Fatal(err)
	}
	if _, err := architecture.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}

	rootLeak, clientID, providerID := newArchitecture(t)
	if err := rootLeak.AddFunctionConnection(
		ConnectFunction(clientID, "Lookup", providerID, "Fetch").IdentifiedBy("root/leak").Build(),
	); err == nil || !strings.Contains(err.Error(), "not visible") {
		t.Fatalf("root-to-child function scope leak error=%v", err)
	}
	missingOwner, clientID, providerID := newArchitecture(t)
	if err := missingOwner.AddFunctionConnectionInArchitecture(
		ConnectFunction(clientID, "Lookup", providerID, "Fetch").IdentifiedBy("missing").Build(), "missing",
	); err == nil || !strings.Contains(err.Error(), "undeclared architecture") {
		t.Fatalf("missing function owner error=%v", err)
	}
	mismatchedOwner, clientID, providerID := newArchitecture(t)
	if err := mismatchedOwner.AddFunctionConnectionInArchitecture(
		ConnectFunction(clientID, "Lookup", providerID, "Fetch").
			WithinArchitecture("child").IdentifiedBy("mismatch").Build(), ArchitectureInterfaceID,
	); err == nil || !strings.Contains(err.Error(), "declares architecture") {
		t.Fatalf("mismatched function owner error=%v", err)
	}

	canonicalGuard, clientID, providerID := newArchitecture(t)
	canonicalGuard.functionConnections = append(canonicalGuard.functionConnections,
		ConnectFunction(clientID, "Lookup", providerID, "Fetch").IdentifiedBy("bypass").Build())
	if _, err := canonicalGuard.DeterministicModelDigest(); err == nil || !strings.Contains(err.Error(), "not visible") {
		t.Fatalf("canonical function visibility guard error=%v", err)
	}
}

func TestArchitectureBoundaryFunctionConnectionPolarityIsCanonical(t *testing.T) {
	build := func() *Architecture {
		architecture := NewArchitecture("function-boundary-polarity")
		if err := architecture.SetReturnInterface(Interface("Boundary").
			ProvidesFunction("Exported", "Integer", P("value", "Integer")).
			RequiresFunction("Imported", "Integer", P("value", "Integer")).
			Build()); err != nil {
			t.Fatal(err)
		}
		worker := NewComponent("worker", Interface("Worker").
			RequiresFunction("Need", "Integer", P("operand", "Integer")).
			ProvidesFunction("Offer", "Integer", P("operand", "Integer")).
			Build(), nil)
		if err := architecture.AddComponent(worker); err != nil {
			t.Fatal(err)
		}
		return architecture
	}

	valid := build()
	for _, connection := range []*FunctionConnection{
		ConnectFunction(ArchitectureInterfaceID, "Exported", "worker", "Offer").
			IdentifiedBy("boundary-provides-in").Build(),
		ConnectFunction("worker", "Need", ArchitectureInterfaceID, "Imported").
			IdentifiedBy("boundary-requires-out").Build(),
	} {
		if err := valid.AddFunctionConnection(connection); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := valid.DeterministicModelDigest(); err != nil {
		t.Fatal(err)
	}

	wrongSource := build()
	if err := wrongSource.AddFunctionConnection(
		ConnectFunction(ArchitectureInterfaceID, "Imported", "worker", "Offer").
			IdentifiedBy("wrong-source").Build(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := wrongSource.DeterministicModelDigest(); err == nil ||
		!strings.Contains(err.Error(), "not a direct provided function") {
		t.Fatalf("architecture-boundary source polarity error=%v", err)
	}

	wrongTarget := build()
	if err := wrongTarget.AddFunctionConnection(
		ConnectFunction("worker", "Need", ArchitectureInterfaceID, "Exported").
			IdentifiedBy("wrong-target").Build(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := wrongTarget.DeterministicModelDigest(); err == nil ||
		!strings.Contains(err.Error(), "not a direct required function") {
		t.Fatalf("architecture-boundary target polarity error=%v", err)
	}
}

func TestConnectedFunctionBodyCanCallAnotherConnectedFunction(t *testing.T) {
	architecture := NewArchitecture("connected-function-chain")
	client := NewComponent("client", Interface("Client").
		OutAction("Start", P("n", "Integer")).
		OutAction("Done", P("n", "Integer")).
		RequiresFunction("Top", "Integer", P("n", "Integer")).Build(), nil)
	middle := NewComponent("middle", Interface("Middle").
		ProvidesFunction("Compute", "Integer", P("input", "Integer")).
		RequiresFunction("Double", "Integer", P("value", "Integer")).Build(), nil)
	leaf := NewComponent("leaf", Interface("Leaf").
		ProvidesFunction("Twice", "Integer", P("operand", "Integer")).Build(), nil)
	if err := client.DeclareState(StateReference("answer", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	if err := middle.DeclareState(StateReference("doubled", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	if err := leaf.AddFunctionImplementation(Function("Twice", "Integer", P("operand", "Integer")).
		Returns(MultiplyValues(BoundValue("operand"), LiteralValue(2))).Build()); err != nil {
		t.Fatal(err)
	}
	if err := middle.AddFunctionImplementation(Function("Compute", "Integer", P("input", "Integer")).
		Do(CallFunctionInto("double", "doubled", "Double", BindingParam("value", "input"))).
		Returns(AddValues(ReadState("doubled"), LiteralValue(1))).Build()); err != nil {
		t.Fatal(err)
	}
	input := pattern.Var("N").WithType("Integer")
	if err := client.AddDeclarativeRule(Rule("compute").
		On(pattern.MatchEvent("Start").BindParam("n", input)).
		Do(
			CallFunctionInto("top", "answer", "Top", BindingParam("n", "N")),
			CallAction("done", "Done", StateParam("n", "answer")),
		).Build()); err != nil {
		t.Fatal(err)
	}
	for _, component := range []*Component{leaf, client, middle} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	for _, route := range []*FunctionConnection{
		ConnectFunction("middle", "Double", "leaf", "Twice").IdentifiedBy("double-route").Build(),
		ConnectFunction("client", "Top", "middle", "Compute").IdentifiedBy("top-route").Build(),
	} {
		if err := architecture.AddFunctionConnection(route); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20, InputEvent{
		Key: "start", Source: "client", Action: "Start", Params: map[string]any{"n": 3},
	}))
	if err != nil {
		t.Fatal(err)
	}
	topCall := onlyNamedEvent(t, result.Poset, "Top'Call")
	computeCall := onlyNamedEvent(t, result.Poset, "Compute'Call")
	doubleCall := onlyNamedEvent(t, result.Poset, "Double'Call")
	twiceCall := onlyNamedEvent(t, result.Poset, "Twice'Call")
	twiceReturn := onlyNamedEvent(t, result.Poset, "Twice'Return")
	doubleReturn := onlyNamedEvent(t, result.Poset, "Double'Return")
	computeReturn := onlyNamedEvent(t, result.Poset, "Compute'Return")
	topReturn := onlyNamedEvent(t, result.Poset, "Top'Return")
	done := onlyNamedEvent(t, result.Poset, "Done")
	if topCall.ID != computeCall.ID || doubleCall.ID != twiceCall.ID ||
		twiceReturn.ID != doubleReturn.ID || computeReturn.ID != topReturn.ID {
		t.Fatal("connected function chain duplicated a qualified call or return occurrence")
	}
	for _, edge := range [][2]*gorapide.Event{
		{topCall, doubleCall}, {doubleCall, twiceReturn},
		{doubleReturn, computeReturn}, {topReturn, done},
	} {
		if !result.Poset.IsCausallyBefore(edge[0].ID, edge[1].ID) {
			t.Fatalf("%s is not causally before %s", edge[0].Name, edge[1].Name)
		}
	}
	if value, _ := done.Param("n"); value != int64(7) {
		t.Fatalf("connected function chain result=%#v, want int64(7)", value)
	}
	owners := make(map[string]bool)
	for _, firing := range result.Firings {
		for _, write := range firing.StateWrites {
			owners[write.ComponentID+"."+write.Name] = true
		}
	}
	if !owners["middle.doubled"] || !owners["client.answer"] {
		t.Fatalf("connected function chain audit owners=%v", owners)
	}
}

func TestCrossComponentFunctionRecursionIsDeterministicAndBounded(t *testing.T) {
	architecture := NewArchitecture("cross-component-recursion")
	a := NewComponent("a", Interface("A").
		OutAction("Start", P("n", "Integer")).
		ProvidesFunction("StepA", "", P("n", "Integer")).
		RequiresFunction("NeedB", "", P("n", "Integer")).Build(), nil)
	b := NewComponent("b", Interface("B").
		ProvidesFunction("StepB", "", P("n", "Integer")).
		RequiresFunction("NeedA", "", P("n", "Integer")).Build(), nil)
	if err := a.AddFunctionImplementation(Function("StepA", "", P("n", "Integer")).
		Do(IfThen(
			GreaterValues(BoundValue("n"), LiteralValue(0)),
			[]Statement{CallFunction("step-b", "NeedB", ExpressionParam("n", SubtractValues(BoundValue("n"), LiteralValue(1))))},
			nil,
		)).Build()); err != nil {
		t.Fatal(err)
	}
	if err := b.AddFunctionImplementation(Function("StepB", "", P("n", "Integer")).
		Do(IfThen(
			GreaterValues(BoundValue("n"), LiteralValue(0)),
			[]Statement{CallFunction("step-a", "NeedA", ExpressionParam("n", SubtractValues(BoundValue("n"), LiteralValue(1))))},
			nil,
		)).Build()); err != nil {
		t.Fatal(err)
	}
	number := pattern.Var("N").WithType("Integer")
	if err := a.AddDeclarativeRule(Rule("start").
		On(pattern.MatchEvent("Start").BindParam("n", number)).
		Do(CallFunction("step-a", "StepA", BindingParam("n", "N"))).Build()); err != nil {
		t.Fatal(err)
	}
	for _, component := range []*Component{b, a} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	for _, route := range []*FunctionConnection{
		ConnectFunction("b", "NeedA", "a", "StepA").IdentifiedBy("route-a").Build(),
		ConnectFunction("a", "NeedB", "b", "StepB").IdentifiedBy("route-b").Build(),
	} {
		if err := architecture.AddFunctionConnection(route); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournalWithLimits(digest, ExecutionLimits{MaxFirings: 10, MaxStatements: 20}, InputEvent{
		Key: "start", Source: "a", Action: "Start", Params: map[string]any{"n": 2},
	})
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Poset.Len() != 8 || len(result.Poset.ByName("NeedB'Call")) != 1 ||
		len(result.Poset.ByName("NeedA'Call")) != 1 || len(result.Poset.ByName("StepB'Return")) != 1 {
		t.Fatalf("cross-component recursion poset=%#v", result.Poset.All())
	}
	limited := journal
	limited.Inputs[0].Params = map[string]any{"n": 20}
	limited.Limits.MaxStatements = 8
	if _, err := architecture.ExecuteDeterministic(limited); !errors.Is(err, ErrExecutionLimit) {
		t.Fatalf("got %v, want cross-component recursive statement limit", err)
	}
}

func TestFunctionConnectionValidationIsDeterministic(t *testing.T) {
	base := func(required, provided FunctionKind, requiredType, providedType string, implement bool) *Architecture {
		architecture := NewArchitecture("invalid-function-route")
		callerInterface := Interface("Caller").OutAction("Start")
		providerInterface := Interface("Provider")
		if required == RequiresFunction {
			callerInterface.RequiresFunction("Need", requiredType, P("n", "Integer"))
		} else {
			callerInterface.ProvidesFunction("Need", requiredType, P("n", "Integer"))
		}
		if provided == ProvidesFunction {
			providerInterface.ProvidesFunction("Offer", providedType, P("n", "Integer"))
		} else {
			providerInterface.RequiresFunction("Offer", providedType, P("n", "Integer"))
		}
		caller := NewComponent("caller", callerInterface.Build(), nil)
		providerComponent := NewComponent("provider", providerInterface.Build(), nil)
		if implement {
			builder := Function("Offer", providedType, P("n", "Integer"))
			if providedType != "" {
				returned := any(1)
				if providedType == "Boolean" {
					returned = true
				}
				builder.Returns(LiteralValue(returned))
			}
			if err := providerComponent.AddFunctionImplementation(builder.Build()); err != nil {
				t.Fatal(err)
			}
		}
		for _, component := range []*Component{caller, providerComponent} {
			if err := architecture.AddComponent(component); err != nil {
				t.Fatal(err)
			}
		}
		if err := architecture.AddFunctionConnection(ConnectFunction("caller", "Need", "provider", "Offer").
			IdentifiedBy("route").Build()); err != nil {
			t.Fatal(err)
		}
		return architecture
	}
	tests := []struct {
		name                       string
		required, provided         FunctionKind
		requiredType, providedType string
		implement                  bool
		want                       string
	}{
		{name: "source is not required", required: ProvidesFunction, provided: ProvidesFunction, requiredType: "Integer", providedType: "Integer", implement: true, want: "not a direct required function"},
		{name: "target is not provided", required: RequiresFunction, provided: RequiresFunction, requiredType: "Integer", providedType: "Integer", want: "not a direct provided function"},
		{name: "incompatible return", required: RequiresFunction, provided: ProvidesFunction, requiredType: "Integer", providedType: "Boolean", implement: true, want: "0 compatible provided signatures"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := base(test.required, test.provided, test.requiredType, test.providedType, test.implement).DeterministicModelDigest()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestFunctionConnectionDeclarationDoesNotRequireAttachedProviderBody(t *testing.T) {
	architecture := NewArchitecture("function-route-declaration")
	caller := NewComponent("caller", Interface("Caller").
		RequiresFunction("Need", "Integer", P("request", "String")).Build(), nil)
	provider := NewComponent("provider", Interface("Provider").
		ProvidesFunction("Offer", "Integer", P("value", "String")).Build(), nil)
	for _, component := range []*Component{caller, provider} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	if err := architecture.AddFunctionConnection(ConnectFunction("caller", "Need", "provider", "Offer").
		IdentifiedBy("route").Build()); err != nil {
		t.Fatal(err)
	}
	if _, err := architecture.DeterministicModelDigest(); err != nil {
		t.Fatalf("an architecture-level function alias should not require a module body until called: %v", err)
	}
}

func TestFunctionConnectionRejectsDuplicateIdentityAndRoute(t *testing.T) {
	for _, test := range []struct {
		name, secondID, want string
	}{
		{name: "duplicate identity", secondID: "route-a", want: "duplicate function connection ID"},
		{name: "duplicate route", secondID: "route-b", want: "connected more than once"},
	} {
		t.Run(test.name, func(t *testing.T) {
			architecture := NewArchitecture("duplicate-function-route")
			caller := NewComponent("caller", Interface("Caller").
				RequiresFunction("Need", "Integer").Build(), nil)
			provider := NewComponent("provider", Interface("Provider").
				ProvidesFunction("Offer", "Integer").Build(), nil)
			if err := provider.AddFunctionImplementation(Function("Offer", "Integer").
				Returns(LiteralValue(1)).Build()); err != nil {
				t.Fatal(err)
			}
			for _, component := range []*Component{caller, provider} {
				if err := architecture.AddComponent(component); err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range []string{"route-a", test.secondID} {
				if err := architecture.AddFunctionConnection(ConnectFunction("caller", "Need", "provider", "Offer").
					IdentifiedBy(id).Build()); err != nil {
					t.Fatal(err)
				}
			}
			_, err := architecture.DeterministicModelDigest()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestInvalidOverloadedFunctionRouteDiagnosticsAreOrderIndependent(t *testing.T) {
	build := func(reverse bool) error {
		architecture := NewArchitecture("invalid-overloaded-function-route")
		requirements := []FunctionDecl{
			{Name: "Need", Kind: RequiresFunction, Params: []ParamDecl{P("n", "Integer")}, ReturnType: "Boolean"},
			{Name: "Need", Kind: RequiresFunction, Params: []ParamDecl{P("n", "Integer")}, ReturnType: "Integer"},
		}
		if reverse {
			requirements[0], requirements[1] = requirements[1], requirements[0]
		}
		callerInterface := Interface("Caller")
		for _, declaration := range requirements {
			callerInterface.RequiresFunction(declaration.Name, declaration.ReturnType, declaration.Params...)
		}
		caller := NewComponent("caller", callerInterface.Build(), nil)
		provider := NewComponent("provider", Interface("Provider").
			ProvidesFunction("Offer", "Integer", P("n", "Integer")).Build(), nil)
		if err := provider.AddFunctionImplementation(Function("Offer", "Integer", P("n", "Integer")).
			Returns(LiteralValue(1)).Build()); err != nil {
			t.Fatal(err)
		}
		for _, component := range []*Component{caller, provider} {
			if err := architecture.AddComponent(component); err != nil {
				t.Fatal(err)
			}
		}
		if err := architecture.AddFunctionConnection(ConnectFunction("caller", "Need", "provider", "Offer").
			IdentifiedBy("route").Build()); err != nil {
			t.Fatal(err)
		}
		_, err := architecture.DeterministicModelDigest()
		return err
	}
	left, right := build(false), build(true)
	if left == nil || right == nil || left.Error() != right.Error() {
		t.Fatalf("declaration order changed invalid-route diagnostic:\nleft=%v\nright=%v", left, right)
	}
}

func TestUnconnectedRequiredFunctionCallFailsStaticResolution(t *testing.T) {
	architecture := NewArchitecture("unconnected-required")
	client := NewComponent("client", Interface("Client").
		OutAction("Start").RequiresFunction("Lookup", "Integer").Build(), nil)
	if err := client.AddDeclarativeRule(Rule("invoke").On(pattern.MatchEvent("Start")).
		Do(CallFunction("lookup", "Lookup")).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(client); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if err == nil || !strings.Contains(err.Error(), "implemented local or connected function") {
		t.Fatalf("got %v, want unconnected required-function resolution error", err)
	}
}
