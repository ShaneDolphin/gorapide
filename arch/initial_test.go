package arch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestModuleInitialClosesConnectionsBeforeAwaitElseActivation(t *testing.T) {
	architecture := NewArchitecture("initial-before-process")
	worker := NewComponent("worker", Interface("Worker").
		OutAction("Boot", P("n", "Integer")).
		InAction("Delivered", P("n", "Integer")).
		OutAction("Handled", P("n", "Integer")).
		OutAction("Fallback").Build(), nil)
	if err := worker.DeclareState(StateReference("value", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	if err := worker.SetInitialStatements(
		SetState("value", LiteralValue(5)),
		CallAction("boot", "Boot", StateParam("n", "value")),
	); err != nil {
		t.Fatal(err)
	}
	n := pattern.Var("N").WithType("Integer")
	if err := worker.AddDeclarativeProcess(Process("receiver").StartAt("start").States(
		AwaitStateWithElse("start",
			AwaitElse("fallback").Emit("Fallback").Terminate().Build(),
			Await("boot").On(pattern.MatchEvent("Delivered").BindParam("n", n)).
				Emit("Handled", BindingParam("n", "N")).Terminate().Build(),
		),
	).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(worker); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddConnection(Connect("worker", "worker").IdentifiedBy("deliver").
		On(pattern.MatchEvent("Boot")).Pipe().Send("Delivered").Build()); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 20)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	boot := result.Poset.ByName("Boot")
	delivered := result.Poset.ByName("Delivered")
	handled := result.Poset.ByName("Handled")
	if len(boot) != 1 || len(delivered) != 1 || len(handled) != 1 || len(result.Poset.ByName("Fallback")) != 0 {
		t.Fatalf("startup events boot=%d delivered=%d handled=%d fallback=%d",
			len(boot), len(delivered), len(handled), len(result.Poset.ByName("Fallback")))
	}
	if !result.Poset.IsCausallyBefore(boot[0].ID, delivered[0].ID) ||
		!result.Poset.IsCausallyBefore(delivered[0].ID, handled[0].ID) {
		t.Fatal("initial/connection/process startup causality is incomplete")
	}
	if len(result.Firings) != 3 || result.Firings[0].Transition != "initial" ||
		result.Firings[1].Transition != "connection" || result.Firings[2].Transition != "process" {
		t.Fatalf("startup phase audit=%#v", result.Firings)
	}
	if len(result.Firings[0].StateWrites) != 1 || result.Firings[0].StateWrites[0].ComponentID != "worker" ||
		len(result.Firings[0].StateReads) != 1 || result.Firings[0].StateReads[0].ComponentID != "worker" {
		t.Fatalf("initial state audit=%#v", result.Firings[0])
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("module initial replay was not byte-identical")
	}
}

func TestIndependentModuleInitialPartsStayIndependentAndCanonical(t *testing.T) {
	build := func(reverse bool) *Architecture {
		t.Helper()
		architecture := NewArchitecture("independent-initial")
		makeComponent := func(id string, value int64) *Component {
			component := NewComponent(id, Interface("Starter").OutAction("Started", P("n", "Integer")).Build(), nil)
			if err := component.SetInitialStatements(
				CallAction("started", "Started", LiteralParam("n", value)),
			); err != nil {
				t.Fatal(err)
			}
			return component
		}
		components := []*Component{makeComponent("a", 1), makeComponent("b", 2)}
		if reverse {
			components[0], components[1] = components[1], components[0]
		}
		for _, component := range components {
			if err := architecture.AddComponent(component); err != nil {
				t.Fatal(err)
			}
		}
		return architecture
	}
	leftModel, rightModel := build(false), build(true)
	leftDigest, err := leftModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := rightModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("component insertion order changed initial model identity: %s != %s", leftDigest, rightDigest)
	}
	left, err := leftModel.ExecuteDeterministic(NewExecutionJournal(leftDigest, 20))
	if err != nil {
		t.Fatal(err)
	}
	right, err := rightModel.ExecuteDeterministic(NewExecutionJournal(rightDigest, 20))
	if err != nil {
		t.Fatal(err)
	}
	events := left.Poset.ByName("Started")
	if len(events) != 2 || left.Poset.IsCausallyBefore(events[0].ID, events[1].ID) ||
		left.Poset.IsCausallyBefore(events[1].ID, events[0].ID) {
		t.Fatalf("independent initial events acquired an order: %#v", events)
	}
	leftBytes, _ := left.MarshalCanonical()
	rightBytes, _ := right.MarshalCanonical()
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatal("component insertion order changed initial execution bytes")
	}
}

func TestModuleInitialSeedsParallelProcessFrontiersWithoutOrderingThem(t *testing.T) {
	architecture := NewArchitecture("initial-frontier")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Started").OutAction("A").OutAction("B").InAction("Never").Build(), nil)
	if err := component.SetInitialStatements(CallAction("started", "Started")); err != nil {
		t.Fatal(err)
	}
	makeProcess := func(id, action string) *DeclarativeProcess {
		return Process(id).StartAt("start").States(AwaitStateWithElse("start",
			AwaitElse("run").Emit(action).Terminate().Build(),
			Await("never").On(pattern.MatchEvent("Never")).NoEvents().Terminate().Build(),
		)).Build()
	}
	if err := component.SetModuleProcessMode(ParallelProcesses); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(makeProcess("a", "A")); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(makeProcess("b", "B")); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20))
	if err != nil {
		t.Fatal(err)
	}
	started := result.Poset.ByName("Started")
	a := result.Poset.ByName("A")
	b := result.Poset.ByName("B")
	if len(started) != 1 || len(a) != 1 || len(b) != 1 {
		t.Fatalf("initial/process events started=%d a=%d b=%d", len(started), len(a), len(b))
	}
	if !result.Poset.IsCausallyBefore(started[0].ID, a[0].ID) ||
		!result.Poset.IsCausallyBefore(started[0].ID, b[0].ID) ||
		result.Poset.IsCausallyBefore(a[0].ID, b[0].ID) ||
		result.Poset.IsCausallyBefore(b[0].ID, a[0].ID) {
		t.Fatal("initial frontier was not inherited independently by parallel processes")
	}
}

func TestModuleInitialValidationFailsExplicitly(t *testing.T) {
	component := NewComponent("worker", Interface("Worker").OutAction("Done").Build(), nil)
	if err := component.SetInitialStatements(); !errors.Is(err, ErrInvalidModuleInitial) {
		t.Fatalf("empty initial error=%v", err)
	}
	if err := component.SetInitialStatements(CallAction("done", "Done")); err != nil {
		t.Fatal(err)
	}
	if err := component.SetInitialStatements(CallAction("again", "Done")); !errors.Is(err, ErrInvalidModuleInitial) {
		t.Fatalf("duplicate initial error=%v", err)
	}

	invalid := NewArchitecture("invalid-initial")
	bad := NewComponent("bad", Interface("Bad").OutAction("Done").Build(), nil)
	if err := bad.SetInitialStatements(SetState("missing", LiteralValue(1))); err != nil {
		t.Fatal(err)
	}
	if err := invalid.AddComponent(bad); err != nil {
		t.Fatal(err)
	}
	if _, err := invalid.DeterministicModelDigest(); !errors.Is(err, ErrInvalidModuleInitial) {
		t.Fatalf("invalid initial digest error=%v", err)
	}

	callModel := NewArchitecture("local-initial-call")
	caller := NewComponent("caller", Interface("Caller").ProvidesFunction("F", "").Build(), nil)
	if err := caller.AddFunctionImplementation(Function("F", "").Build()); err != nil {
		t.Fatal(err)
	}
	if err := caller.SetInitialStatements(CallFunction("f", "F")); err != nil {
		t.Fatal(err)
	}
	if err := callModel.AddComponent(caller); err != nil {
		t.Fatal(err)
	}
	digest, err := callModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := callModel.ExecuteDeterministic(NewExecutionJournal(digest, 10))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("F'Call")) != 1 || len(result.Poset.ByName("F'Return")) != 1 {
		t.Fatalf("local initial function events=%#v", result.Poset.Events())
	}

	remote := NewArchitecture("invalid-remote-initial-call")
	client := NewComponent("client", Interface("Client").RequiresFunction("Lookup", "Integer").Build(), nil)
	if err := client.SetInitialStatements(CallFunction("lookup", "Lookup")); err != nil {
		t.Fatal(err)
	}
	provider := NewComponent("provider", Interface("Provider").ProvidesFunction("Fetch", "Integer").Build(), nil)
	if err := provider.AddFunctionImplementation(Function("Fetch", "Integer").Returns(LiteralValue(1)).Build()); err != nil {
		t.Fatal(err)
	}
	for _, component := range []*Component{client, provider} {
		if err := remote.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	if err := remote.AddFunctionConnection(
		ConnectFunction("client", "Lookup", "provider", "Fetch").IdentifiedBy("lookup").Build(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.DeterministicModelDigest(); !errors.Is(err, ErrInvalidModuleInitial) ||
		!strings.Contains(err.Error(), "cross-component calls require source-grounded module creation ordering") {
		t.Fatalf("remote initial function call error=%v", err)
	}
}

func TestModuleInitialRejectsExternalInActionInterrupt(t *testing.T) {
	architecture := NewArchitecture("initial-external-interrupt")
	component := NewComponent("worker", Interface("Worker").
		InAction("Request").
		OutAction("Done").Build(), nil)
	if err := component.SetInitialStatements(HandleDo(
		[]Statement{CallAction("done", "Done")},
		ExceptionHandler{Choices: []ExceptionHandlerChoice{
			HandleInterrupt("Request", nil, CallAction("handled", "Done")),
		}},
	)); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if err == nil || !errors.Is(err, ErrInvalidModuleInitial) ||
		!strings.Contains(err.Error(), "initial external in-action interrupt choice") {
		t.Fatalf("external initial interrupt error=%v", err)
	}
}
