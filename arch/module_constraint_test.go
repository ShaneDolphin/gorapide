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

func TestModuleConstraintSeesPrivateActionWhileArchitectureConstraintDoesNot(t *testing.T) {
	architecture := NewArchitecture("module-private-constraint")
	worker := NewComponent("worker", Interface("Worker").
		OutAction("Start").PrivateAction("Hidden").OutAction("Done").Build(), nil)
	if err := worker.SetInitialStatements(
		CallAction("start", "Start"),
		CallAction("hidden", "Hidden"),
		CallAction("done", "Done"),
	); err != nil {
		t.Fatal(err)
	}
	moduleSet := constraint.NewConstraintSet("worker-local-computation")
	moduleSet.Add(constraint.NewConstraint("private-visible").
		Must("whole", pattern.Seq(
			pattern.MatchEvent("Start"),
			pattern.MatchEvent("Hidden"),
			pattern.MatchEvent("Done"),
		), "module computation must include its private action").Build())
	worker.SetModuleConstraints(moduleSet)
	if err := architecture.AddComponent(worker); err != nil {
		t.Fatal(err)
	}
	architectureSet := constraint.NewConstraintSet("architecture-public-computation")
	architectureSet.Add(constraint.NewConstraint("public-only").
		Must("whole", pattern.Seq(pattern.MatchEvent("Start"), pattern.MatchEvent("Done")), "public computation is incomplete").
		MustNever("hidden", pattern.MatchEvent("Hidden"), "private action leaked into architecture scope").Build())
	architecture.WithConstraints(architectureSet, constraint.CheckAfter)

	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10))
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || !result.Constraints.Passed {
		t.Fatalf("architecture constraint report=%#v", result.Constraints)
	}
	if len(result.ModuleConstraints) != 1 || result.ModuleConstraints[0].ComponentID != "worker" ||
		!result.ModuleConstraints[0].Report.Passed {
		t.Fatalf("module constraint reports=%#v", result.ModuleConstraints)
	}
	if result.Constraints.PosetDigest == result.ModuleConstraints[0].Report.PosetDigest {
		t.Fatal("architecture and module constraint scopes produced the same visible computation")
	}
	fullDigest, err := result.Poset.SemanticDigest()
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints.PosetDigest == fullDigest {
		t.Fatal("architecture report was evaluated over the private audit computation")
	}
	moduleView, err := moduleConstraintView("worker", result.Poset)
	if err != nil {
		t.Fatal(err)
	}
	moduleDigest, err := moduleView.(*pattern.Projection).SemanticDigest()
	if err != nil {
		t.Fatal(err)
	}
	if moduleView.Len()+1 != result.Poset.Len() || len(moduleView.ByName("Hidden")) != 1 ||
		result.ModuleConstraints[0].Report.PosetDigest != moduleDigest {
		t.Fatalf("module-visible computation len=%d/%d hidden=%d report/view=%s/%s",
			moduleView.Len(), result.Poset.Len(), len(moduleView.ByName("Hidden")),
			result.ModuleConstraints[0].Report.PosetDigest, moduleDigest)
	}
}

func TestModuleConstraintsArePerInstanceCanonicalAndReplayable(t *testing.T) {
	build := func(reverse bool) *Architecture {
		t.Helper()
		architecture := NewArchitecture("per-instance-module-constraints")
		components := []*Component{
			NewComponent("a", Interface("Worker").OutAction("Error").Build(), nil),
			NewComponent("b", Interface("Worker").OutAction("Error").Build(), nil),
		}
		if reverse {
			components[0], components[1] = components[1], components[0]
		}
		for _, component := range components {
			set := constraint.NewConstraintSet("no-local-error")
			set.Add(constraint.NewConstraint("error-forbidden").
				MustNever("error", pattern.MatchEvent("Error"), "Error must not occur in this module").Build())
			component.SetModuleConstraints(set)
			if err := architecture.AddComponent(component); err != nil {
				t.Fatal(err)
			}
		}
		return architecture
	}

	forward, reverse := build(false), build(true)
	forwardDigest, err := forward.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	reverseDigest, err := reverse.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if forwardDigest != reverseDigest {
		t.Fatalf("component insertion order changed model identity: %s != %s", forwardDigest, reverseDigest)
	}
	journal := NewExecutionJournal(forwardDigest, 5, InputEvent{Key: "error", Source: "a", Action: "Error"})
	left, err := forward.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	right, err := reverse.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(left.ModuleConstraints) != 2 || left.ModuleConstraints[0].ComponentID != "a" ||
		left.ModuleConstraints[1].ComponentID != "b" || left.ModuleConstraints[0].Report.Passed ||
		!left.ModuleConstraints[1].Report.Passed {
		t.Fatalf("per-instance canonical reports=%#v", left.ModuleConstraints)
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
		t.Fatalf("component insertion order changed artifact:\nleft=%s\nright=%s", leftBytes, rightBytes)
	}
	artifactDigest, err := left.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := forward.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, replayBytes) {
		t.Fatal("module constraint reports were not byte-identical on replay")
	}
}

func TestModuleConstraintProjectionPreservesCausalityThroughPeerEvent(t *testing.T) {
	poset := gorapide.NewPoset()
	start, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: "projection", Instance: "local", Action: "Start", Occurrence: "start",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(start); err != nil {
		t.Fatal(err)
	}
	middle, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: "projection", Instance: "peer", Action: "Middle", Occurrence: "middle",
		Causes: []gorapide.EventID{start.ID},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(middle, start.ID); err != nil {
		t.Fatal(err)
	}
	done, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: "projection", Instance: "local", Action: "Done", Occurrence: "done",
		Causes: []gorapide.EventID{middle.ID},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(done, middle.ID); err != nil {
		t.Fatal(err)
	}

	view, err := moduleConstraintView("local", poset)
	if err != nil {
		t.Fatal(err)
	}
	if view.Len() != 2 || len(view.ByName("Middle")) != 0 || !view.IsCausallyBefore(start.ID, done.ID) {
		t.Fatalf("local projection len=%d middle=%d start<done=%v", view.Len(), len(view.ByName("Middle")), view.IsCausallyBefore(start.ID, done.ID))
	}
	set := constraint.NewConstraintSet("local-order")
	set.Add(constraint.NewConstraint("start-before-done").
		Must("whole", pattern.Seq(pattern.MatchEvent("Start"), pattern.MatchEvent("Done")), "hidden peer traversal lost causality").Build())
	report, err := set.EvaluateCanonical(view)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("local projection constraint report=%#v", report)
	}
	fullDigest, err := poset.SemanticDigest()
	if err != nil {
		t.Fatal(err)
	}
	if report.PosetDigest == fullDigest {
		t.Fatal("module constraint projection included a peer-only event")
	}
}

func TestModuleConstraintChangesModelIdentityAndRejectsOpaqueChecker(t *testing.T) {
	build := func(set *constraint.ConstraintSet) *Architecture {
		t.Helper()
		architecture := NewArchitecture("module-constraint-identity")
		component := NewComponent("worker", Interface("Worker").OutAction("Event").Build(), nil)
		component.SetModuleConstraints(set)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		return architecture
	}

	withoutDigest, err := build(nil).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	closed := constraint.NewConstraintSet("closed")
	closed.Add(constraint.NewConstraint("no-event").
		MustNever("event", pattern.MatchEvent("Event"), "Event is forbidden").Build())
	withDigest, err := build(closed).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if withoutDigest == withDigest {
		t.Fatal("module constraint set did not change canonical model identity")
	}

	opaque := constraint.NewConstraintSet("opaque")
	opaque.Add(constraint.EventCount("Event", 0, 0))
	_, err = build(opaque).DeterministicModelDigest()
	if err == nil || !errors.Is(err, ErrUnsupportedDeterministicModel) ||
		!strings.Contains(err.Error(), "unsupported type *constraint.PredicateConstraint") {
		t.Fatalf("opaque module constraint error=%v", err)
	}
}
