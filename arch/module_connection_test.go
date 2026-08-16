package arch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestModuleConnectionAliasesOneOccurrenceThroughModuleAndArchitecture(t *testing.T) {
	architecture := NewArchitecture("module-connection-chain")
	driver := NewComponent("driver", Interface("Driver").
		OutAction("Send", P("n", "Integer")).Build(), nil)
	relay := NewComponent("relay", Interface("Relay").
		InAction("Request", P("n", "Integer")).
		OutAction("Response", P("n", "Integer")).Build(), nil)
	sink := NewComponent("sink", Interface("Sink").
		InAction("Delivered", P("n", "Integer")).Build(), nil)
	for _, component := range []*Component{driver, relay, sink} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	n := pattern.Var("N").WithType("Integer")
	connections := []*Connection{
		Connect("driver", "relay").IdentifiedBy("driver-to-relay").
			On(pattern.MatchEvent("Send").BindParam("n", n)).
			SendParameters("Request", ConnectionBindingParam("n", "N")).Build(),
		Connect("relay", "relay").WithinModule().IdentifiedBy("relay-local").
			On(pattern.MatchEvent("Request").BindParam("n", n)).
			SendParameters("Response", ConnectionBindingParam("n", "N")).Build(),
		Connect("relay", "sink").IdentifiedBy("relay-to-sink").
			On(pattern.MatchEvent("Response").BindParam("n", n)).
			SendParameters("Delivered", ConnectionBindingParam("n", "N")).Build(),
	}
	for _, connection := range connections {
		if err := architecture.AddConnection(connection); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10, InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 7},
	})
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Send", "Request", "Response", "Delivered"} {
		events := result.Poset.ByName(name)
		if len(events) != 1 {
			t.Fatalf("%s observations=%d, want 1", name, len(events))
		}
		if events[0].ID != result.Poset.ByName("Send")[0].ID {
			t.Fatalf("basic connection chain changed occurrence identity at %s", name)
		}
		if value, _ := events[0].Param("n"); value != int64(7) {
			t.Fatalf("%s parameter=%#v, want 7", name, value)
		}
	}
	if len(result.Firings) != 3 ||
		result.Firings[0].ConnectionScope != ArchitectureConnectionScope.String() ||
		result.Firings[1].ConnectionScope != ModuleConnectionScope.String() ||
		result.Firings[2].ConnectionScope != ArchitectureConnectionScope.String() {
		t.Fatalf("connection scope audit=%#v", result.Firings)
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
		t.Fatal("module-local connection replay was not byte-identical")
	}
}

func TestModuleConnectionScopeIsCanonicalAndBuilderIdentityDiffers(t *testing.T) {
	build := func(module bool) (*Architecture, string) {
		t.Helper()
		architecture := NewArchitecture("connection-scope")
		component := NewComponent("worker", Interface("Worker").
			InAction("Alias").OutAction("Alias").Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		builder := Connect("worker", "worker").On(pattern.MatchEvent("Alias")).Send("Alias")
		if module {
			builder.WithinModule()
		}
		connection := builder.Build()
		if err := architecture.AddConnection(connection); err != nil {
			t.Fatal(err)
		}
		return architecture, connection.ID
	}
	architectureConnection, architectureID := build(false)
	moduleConnection, moduleID := build(true)
	if architectureID == moduleID {
		t.Fatal("builder identity omitted connection scope")
	}
	left, err := architectureConnection.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	right, err := moduleConnection.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatal("module-local and architecture connection scopes have one model identity")
	}
}

func TestModuleConnectionValidationRejectsCrossComponentAndInActionTargets(t *testing.T) {
	cross := NewArchitecture("invalid-module-route")
	for _, id := range []string{"a", "b"} {
		if err := cross.AddComponent(NewComponent(id, Interface("I").OutAction("X").Build(), nil)); err != nil {
			t.Fatal(err)
		}
	}
	if err := cross.AddConnection(Connect("a", "b").WithinModule().IdentifiedBy("cross").
		On(pattern.MatchEvent("X")).Send("X").Build()); err != nil {
		t.Fatal(err)
	}
	if _, err := cross.DeterministicModelDigest(); err == nil || !strings.Contains(err.Error(), "identical explicit source and target") {
		t.Fatalf("cross-component module connection error=%v", err)
	}

	wrongDirection := NewArchitecture("invalid-module-target")
	component := NewComponent("worker", Interface("Worker").OutAction("Start").InAction("Again").Build(), nil)
	if err := component.SetInitialStatements(CallAction("start", "Start")); err != nil {
		t.Fatal(err)
	}
	if err := wrongDirection.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := wrongDirection.AddConnection(Connect("worker", "worker").WithinModule().IdentifiedBy("wrong-target").
		On(pattern.MatchEvent("Start")).Send("Again").Build()); err != nil {
		t.Fatal(err)
	}
	digest, err := wrongDirection.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 3)
	_, err = wrongDirection.ExecuteDeterministic(journal)
	if err == nil || !errors.Is(err, ErrActionTypeMismatch) || !strings.Contains(err.Error(), "out-action") {
		t.Fatalf("module in-action target error=%v", err)
	}
}
