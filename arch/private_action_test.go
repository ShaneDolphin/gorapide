package arch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/constraint"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestPrivateActionAliasesThroughModuleConnectionWithoutChangingOccurrence(t *testing.T) {
	architecture := NewArchitecture("private-module-alias")
	worker := NewComponent("worker", Interface("Worker").
		PrivateAction("Hidden", P("n", "Integer")).
		OutAction("Published", P("n", "Integer")).Build(), nil)
	sink := NewComponent("sink", Interface("Sink").
		InAction("Delivered", P("n", "Integer")).Build(), nil)
	if err := worker.SetInitialStatements(
		CallAction("hidden", "Hidden", LiteralParam("n", 7)),
	); err != nil {
		t.Fatal(err)
	}
	for _, component := range []*Component{worker, sink} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	number := pattern.Var("N").WithType("Integer")
	connections := []*Connection{
		Connect("worker", "worker").WithinModule().IdentifiedBy("publish-hidden").
			On(pattern.MatchEvent("Hidden").BindParam("n", number)).
			SendParameters("Published", ConnectionBindingParam("n", "N")).Build(),
		Connect("worker", "sink").IdentifiedBy("deliver-published").
			On(pattern.MatchEvent("Published").BindParam("n", number)).
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
	journal := NewExecutionJournal(digest, 10)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Poset.Len() != 2 {
		t.Fatalf("occurrences=%d, want architecture Start plus one basic-connected occurrence", result.Poset.Len())
	}
	hidden := result.Poset.ByName("Hidden")
	published := result.Poset.ByName("Published")
	delivered := result.Poset.ByName("Delivered")
	if len(hidden) != 1 || len(published) != 1 || len(delivered) != 1 ||
		hidden[0].ID != published[0].ID || published[0].ID != delivered[0].ID {
		t.Fatalf("private/public identity hidden=%#v published=%#v delivered=%#v", hidden, published, delivered)
	}
	if len(result.Firings) != 3 || result.Firings[0].Transition != "initial" ||
		result.Firings[1].ConnectionScope != ModuleConnectionScope.String() ||
		result.Firings[2].ConnectionScope != ArchitectureConnectionScope.String() {
		t.Fatalf("private connection audit=%#v", result.Firings)
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
		t.Fatal("private-action replay was not byte-identical")
	}
}

func TestArchitectureCompoundConnectionCannotObservePrivateAction(t *testing.T) {
	architecture := NewArchitecture("private-compound-visibility")
	source := NewComponent("source", Interface("Source").
		PrivateAction("Hidden").OutAction("Public").Build(), nil)
	target := NewComponent("target", Interface("Target").InAction("Combined").Build(), nil)
	if err := source.SetInitialStatements(
		CallAction("hidden", "Hidden"),
		CallAction("public", "Public"),
	); err != nil {
		t.Fatal(err)
	}
	for _, component := range []*Component{source, target} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	if err := architecture.AddConnection(Connect("source", "target").IdentifiedBy("must-not-fire").
		On(pattern.Seq(pattern.MatchEvent("Hidden"), pattern.MatchEvent("Public"))).
		Pipe().Send("Combined").Build()); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Hidden")) != 1 || len(result.Poset.ByName("Public")) != 1 {
		t.Fatal("module-local execution did not retain its private and public events")
	}
	if len(result.Poset.ByName("Combined")) != 0 {
		t.Fatal("architecture compound connection observed a private action")
	}
}

func TestExecutionJournalCannotInjectPrivateAction(t *testing.T) {
	architecture := NewArchitecture("private-input")
	if err := architecture.AddComponent(NewComponent("worker", Interface("Worker").
		PrivateAction("Hidden").OutAction("Public").Build(), nil)); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	_, err = architecture.ExecuteDeterministic(NewExecutionJournal(digest, 2,
		InputEvent{Key: "hidden", Source: "worker", Action: "Hidden"},
	))
	if err == nil || !errors.Is(err, ErrInvalidExecutionJournal) || !errors.Is(err, ErrActionTypeMismatch) {
		t.Fatalf("private journal input error=%v", err)
	}
}

func TestArchitectureConstraintsHidePrivateActionsAndPreserveCausality(t *testing.T) {
	architecture := NewArchitecture("private-constraint-projection")
	worker := NewComponent("worker", Interface("Worker").
		OutAction("Start").PrivateAction("Hidden").OutAction("Done").Build(), nil)
	if err := worker.SetInitialStatements(
		CallAction("start", "Start"),
		CallAction("hidden", "Hidden"),
		CallAction("done", "Done"),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(worker); err != nil {
		t.Fatal(err)
	}
	set := constraint.NewConstraintSet("public-computation")
	set.Add(constraint.NewConstraint("public-order").
		Must("start-before-done", pattern.Seq(pattern.MatchEvent("Start"), pattern.MatchEvent("Done")), "public order missing").
		MustNever("hidden", pattern.MatchEvent("Hidden"), "private action leaked").Build())
	architecture.WithConstraints(set, constraint.CheckAfter)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10))
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || !result.Constraints.Passed {
		t.Fatalf("public constraint projection=%#v", result.Constraints)
	}
	start := gorapide.EventSet{onlySourceNamedEvent(t, result.Poset, "worker", "Start")}
	hidden, done := result.Poset.ByName("Hidden"), result.Poset.ByName("Done")
	if len(start) != 1 || len(hidden) != 1 || len(done) != 1 ||
		!result.Poset.IsCausallyBefore(start[0].ID, hidden[0].ID) ||
		!result.Poset.IsCausallyBefore(hidden[0].ID, done[0].ID) {
		t.Fatal("full audit poset lost private occurrence or its causality")
	}
	fullDigest, err := result.Poset.SemanticDigest()
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints.PosetDigest == fullDigest {
		t.Fatal("constraint report was bound to the unprojected private computation")
	}
}

func TestPrivateActionChangesCanonicalModelIdentity(t *testing.T) {
	build := func(private bool) *Architecture {
		architecture := NewArchitecture("action-visibility")
		builder := Interface("Worker")
		if private {
			builder.PrivateAction("A")
		} else {
			builder.OutAction("A")
		}
		if err := architecture.AddComponent(NewComponent("worker", builder.Build(), nil)); err != nil {
			t.Fatal(err)
		}
		return architecture
	}
	publicDigest, err := build(false).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	privateModel := build(true)
	privateDigest, err := privateModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if publicDigest == privateDigest {
		t.Fatal("private and public action declarations have identical model identity")
	}
	if !strings.Contains(privateModel.components["worker"].Interface.String(), "private:A") {
		t.Fatalf("private interface rendering=%q", privateModel.components["worker"].Interface.String())
	}
}
