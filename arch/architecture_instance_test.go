package arch

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/constraint"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func deterministicNestedArchitecture(t *testing.T, reverse bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("nested-boundary")
	childInterface := Interface("Child").
		InAction("Request", P("n", "Integer")).
		OutAction("Ready", P("n", "Integer")).Build()
	if err := architecture.AddDeterministicArchitectureInstance(ArchitectureInstance(
		"child", "ChildArchitecture", childInterface,
		ArchitectureArgument("Enabled", "Boolean", true),
		ArchitectureArgument("Limit", "Integer", 2),
	)); err != nil {
		t.Fatal(err)
	}

	driver := NewComponent("driver", Interface("Driver").
		OutAction("Send", P("n", "Integer")).Build(), nil)
	worker := NewComponent("child_worker", Interface("Worker").
		InAction("Begin", P("n", "Integer")).
		OutAction("Boot").
		OutAction("Done", P("n", "Integer")).Build(), nil)
	sink := NewComponent("sink", Interface("Sink").
		InAction("Receive", P("n", "Integer")).Build(), nil)
	if err := worker.SetInitialStatements(CallAction("boot", "Boot")); err != nil {
		t.Fatal(err)
	}
	if err := worker.AddDeclarativeRule(Rule("finish").
		On(pattern.MatchEvent("Begin").BindParam("n", pattern.Var("N").WithType("Integer"))).
		Emit("Done", BindingParam("n", "N")).Build()); err != nil {
		t.Fatal(err)
	}

	type ownedComponent struct {
		component *Component
		owner     string
	}
	components := []ownedComponent{
		{component: driver, owner: ArchitectureInterfaceID},
		{component: worker, owner: "child"},
		{component: sink, owner: ArchitectureInterfaceID},
	}
	if reverse {
		components[0], components[2] = components[2], components[0]
	}
	for _, declaration := range components {
		if err := architecture.AddComponentInArchitecture(declaration.component, declaration.owner); err != nil {
			t.Fatal(err)
		}
	}

	connections := []*Connection{
		Connect("driver", "child").IdentifiedBy("parent/request").
			On(pattern.MatchEvent("Send")).Send("Request").Build(),
		Connect("child", "child_worker").WithinArchitecture("child").IdentifiedBy("child/request").
			On(pattern.MatchEvent("Request")).Send("Begin").Build(),
		Connect("child_worker", "child").WithinArchitecture("child").IdentifiedBy("child/ready").
			On(pattern.MatchEvent("Done")).Send("Ready").Build(),
		Connect("child", "sink").IdentifiedBy("parent/ready").
			On(pattern.MatchEvent("Ready")).Send("Receive").Build(),
	}
	if reverse {
		for left, right := 0, len(connections)-1; left < right; left, right = left+1, right-1 {
			connections[left], connections[right] = connections[right], connections[left]
		}
	}
	for _, connection := range connections {
		if err := architecture.AddConnection(connection); err != nil {
			t.Fatal(err)
		}
	}

	set := constraint.NewConstraintSet("hide-system-starts")
	set.Add(constraint.NewConstraint("no-public-start").
		MustNever("start", pattern.MatchEvent(ArchitectureStartAction), "system Start is not in the public architecture alphabet").Build())
	set.Add(constraint.NewConstraint("no-child-internals").
		MustNever("boot", pattern.MatchEvent("Boot"), "child internals are not in the parent architecture alphabet").Build())
	architecture.WithConstraints(set, constraint.CheckOnEvent)
	childSet := constraint.NewConstraintSet("child-visibility")
	childSet.Add(constraint.NewConstraint("no-parent-internals").
		MustNever("send", pattern.MatchEvent("Send"), "parent components are not in the child architecture alphabet").Build())
	if err := architecture.SetDeterministicArchitectureInstanceConstraints("child", childSet); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestDeterministicNestedArchitectureBoundaryAndStartCausality(t *testing.T) {
	architecture := deterministicNestedArchitecture(t, false)
	if err := architecture.components["child_worker"].SetModuleMembership("WorkerModule"); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 30, InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 7},
	})
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}

	rootStart := onlySourceNamedEvent(t, result.Poset, ArchitectureInterfaceID, ArchitectureStartAction)
	childStart := onlySourceNamedEvent(t, result.Poset, architectureInstanceAuditID("child"), ArchitectureStartAction)
	rootLifecycle := lifecycleRecordByNameID(t, result, "architecture-name:"+ArchitectureInterfaceID)
	childLifecycle := lifecycleRecordByNameID(t, result, "architecture-name:"+architectureInstanceAuditID("child"))
	if !strings.HasPrefix(rootLifecycle.ModuleID, "mod1-") ||
		!strings.HasPrefix(childLifecycle.ModuleID, "mod1-") ||
		rootLifecycle.ModuleID == childLifecycle.ModuleID ||
		rootLifecycle.Parent != moduleEnvironmentRoot || childLifecycle.Parent != rootLifecycle.ModuleID ||
		rootLifecycle.StartEventID != string(rootStart.ID) || childLifecycle.StartEventID != string(childStart.ID) {
		t.Fatalf("root/child architecture allocation graph=%#v/%#v", rootLifecycle, childLifecycle)
	}
	if causes := result.Poset.DirectCauses(childStart.ID); len(causes) != 1 || causes[0].ID != rootStart.ID {
		t.Fatalf("child Start causes=%#v, want root Start", causes)
	}
	workerStart := onlySourceNamedEvent(t, result.Poset, staticModuleAuditID("child_worker"), ArchitectureStartAction)
	workerLifecycle := lifecycleRecordByNameID(t, result, "component-name:child_worker")
	if workerLifecycle.Parent != childLifecycle.ModuleID || workerLifecycle.StartEventID != string(workerStart.ID) {
		t.Fatalf("child module allocation graph=%#v, child=%#v", workerLifecycle, childLifecycle)
	}
	if causes := result.Poset.DirectCauses(workerStart.ID); len(causes) != 1 || causes[0].ID != childStart.ID {
		t.Fatalf("child module Start causes=%#v, want child architecture Start", causes)
	}
	boot := onlySourceNamedEvent(t, result.Poset, "child_worker", "Boot")
	if causes := result.Poset.DirectCauses(boot.ID); len(causes) != 1 || causes[0].ID != workerStart.ID {
		t.Fatalf("child module initial causes=%#v, want module Start", causes)
	}
	request := onlySourceNamedEvent(t, result.Poset, "driver", "Send")
	if !request.HasObservation("child", "Request") || !request.HasObservation("child_worker", "Begin") {
		t.Fatalf("request did not cross parent and child input boundaries: %#v", request.ObservationViews())
	}
	if causes := result.Poset.DirectCauses(request.ID); len(causes) != 1 || causes[0].ID != rootStart.ID {
		t.Fatalf("root input causes=%#v, want root Start", causes)
	}
	done := onlySourceNamedEvent(t, result.Poset, "child_worker", "Done")
	if !done.HasObservation("child", "Ready") || !done.HasObservation("sink", "Receive") {
		t.Fatalf("result did not cross child output and parent boundaries: %#v", done.ObservationViews())
	}
	if !result.Poset.IsCausallyBefore(request.ID, done.ID) {
		t.Fatal("request is not causally before the child result")
	}
	if result.Constraints == nil || !result.Constraints.Passed {
		t.Fatalf("system Start leaked into returned-interface constraints: %#v", result.Constraints)
	}
	if len(result.ArchitectureConstraints) != 1 ||
		result.ArchitectureConstraints[0].ArchitectureInstance != "child" ||
		!result.ArchitectureConstraints[0].Report.Passed {
		t.Fatalf("parent observation leaked into child constraints: %#v", result.ArchitectureConstraints)
	}

	encoded, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, replayedBytes) {
		t.Fatal("nested architecture replay changed canonical artifact bytes")
	}
	explored, err := architecture.ExploreDeterministic(journal, ExplorationLimits{
		MaxExecutions: 2, MaxChoiceDepth: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 1 {
		t.Fatalf("nested exploration=%#v", explored)
	}
}

func TestNestedArchitectureCanonicalizationIgnoresDeclarationOrderAndHostParallelism(t *testing.T) {
	left := deterministicNestedArchitecture(t, false)
	right := deterministicNestedArchitecture(t, true)
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("nested declaration order changed model digest: %s != %s", leftDigest, rightDigest)
	}
	journal := NewExecutionJournal(leftDigest, 30, InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 7},
	})
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	one, err := left.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	two, err := right.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	oneBytes, _ := one.MarshalCanonical()
	twoBytes, _ := two.MarshalCanonical()
	if !bytes.Equal(oneBytes, twoBytes) {
		t.Fatal("nested artifact changed with declaration order or GOMAXPROCS")
	}
}

func TestDeterministicNestedArchitectureRejectsInvalidOwnership(t *testing.T) {
	childInterface := Interface("Child").InAction("Request").OutAction("Ready").Build()
	constraints := constraint.NewConstraintSet("child")
	var nilArchitecture *Architecture
	if err := nilArchitecture.SetDeterministicArchitectureInstanceConstraints("child", constraints); err == nil || !strings.Contains(err.Error(), "architecture is nil") {
		t.Fatalf("nil architecture constraint attachment error=%v", err)
	}
	if err := nilArchitecture.SetDeterministicArchitectureInitialStatements(
		ArchitectureInterfaceID, CallAction("ready", "Ready"),
	); err == nil || !strings.Contains(err.Error(), "architecture is nil") {
		t.Fatalf("nil architecture initial attachment error=%v", err)
	}
	architecture := NewArchitecture("invalid-nesting")
	if err := architecture.SetDeterministicArchitectureInstanceConstraints("missing", constraints); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("missing architecture constraint attachment error=%v", err)
	}
	if err := architecture.SetDeterministicArchitectureInitialStatements(
		"missing", CallAction("ready", "Ready"),
	); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("missing architecture initial attachment error=%v", err)
	}
	badParent := ArchitectureInstance("grandchild", "ChildArchitecture", childInterface)
	badParent.Parent = "child"
	if err := architecture.AddDeterministicArchitectureInstance(badParent); err == nil || !strings.Contains(err.Error(), "non-canonical ID") {
		t.Fatalf("non-canonical recursive identity error=%v", err)
	}
	if err := architecture.AddDeterministicArchitectureInstance(ArchitectureInstance(
		"child", "ChildArchitecture", childInterface,
	)); err != nil {
		t.Fatal(err)
	}
	if err := architecture.SetDeterministicArchitectureInstanceConstraints("child", nil); err == nil || !strings.Contains(err.Error(), "constraint set is nil") {
		t.Fatalf("nil child constraint set error=%v", err)
	}
	if err := architecture.SetDeterministicArchitectureInstanceConstraints("child", constraints); err != nil {
		t.Fatal(err)
	}
	if err := architecture.SetDeterministicArchitectureInstanceConstraints("child", constraints); err == nil || !strings.Contains(err.Error(), "already has constraints") {
		t.Fatalf("duplicate child constraint set error=%v", err)
	}
	if err := architecture.SetDeterministicArchitectureInitialStatements("child"); err == nil || !strings.Contains(err.Error(), "initial part is empty") {
		t.Fatalf("empty child architecture initial error=%v", err)
	}
	if err := architecture.SetDeterministicArchitectureInitialStatements(
		"child", CallAction("ready", "Ready"),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.SetDeterministicArchitectureInitialStatements(
		"child", CallAction("ready", "Ready"),
	); err == nil || !strings.Contains(err.Error(), "already has an initial part") {
		t.Fatalf("duplicate child architecture initial error=%v", err)
	}
	internal := NewComponent("child_worker", Interface("Worker").InAction("Begin").Build(), nil)
	if err := architecture.AddComponentInArchitecture(internal, "child"); err != nil {
		t.Fatal(err)
	}
	root := NewComponent("root", Interface("RootComponent").OutAction("Send").Build(), nil)
	if err := architecture.AddComponent(root); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddConnection(Connect("root", "child_worker").IdentifiedBy("leak-in").Send("Begin").Build()); err == nil || !strings.Contains(err.Error(), "not visible") {
		t.Fatalf("parent-to-internal connection error=%v", err)
	}
	if err := architecture.AddConnectionInArchitecture(
		Connect("child_worker", "root").IdentifiedBy("leak-out").Send("Send").Build(), "child",
	); err == nil || !strings.Contains(err.Error(), "not visible") {
		t.Fatalf("child-to-parent-internal connection error=%v", err)
	}
	if err := architecture.AddConnectionInArchitecture(
		Connect("child", "child_worker").IdentifiedBy("missing-owner").Send("Begin").Build(), "missing",
	); err == nil || !strings.Contains(err.Error(), "undeclared architecture") {
		t.Fatalf("missing connection owner error=%v", err)
	}
	if err := architecture.AddComponent(NewComponent("child", childInterface, nil)); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("component/architecture identity conflict error=%v", err)
	}

	reverseConflict := NewArchitecture("reverse-conflict")
	if err := reverseConflict.AddComponent(NewComponent("child", childInterface, nil)); err != nil {
		t.Fatal(err)
	}
	if err := reverseConflict.AddDeterministicArchitectureInstance(ArchitectureInstance(
		"child", "ChildArchitecture", childInterface,
	)); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("architecture/component identity conflict error=%v", err)
	}

	running := NewArchitecture("running")
	if err := running.AddDeterministicArchitectureInstance(ArchitectureInstance(
		"child", "ChildArchitecture", childInterface,
	)); err != nil {
		t.Fatal(err)
	}
	running.running = true
	if err := running.SetDeterministicArchitectureInstanceConstraints("child", constraints); err == nil || !strings.Contains(err.Error(), "is running") {
		t.Fatalf("running architecture constraint attachment error=%v", err)
	}
	if err := running.SetDeterministicArchitectureInitialStatements(
		"child", CallAction("ready", "Ready"),
	); err == nil || !strings.Contains(err.Error(), "is running") {
		t.Fatalf("running architecture initial attachment error=%v", err)
	}
}

func deterministicRecursiveArchitecture(t *testing.T, reverse bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("recursive-boundaries")
	rootInterface := Interface("Root").OutAction("RootBoot").Build()
	childInterface := Interface("Child").
		InAction("Request", P("n", "Integer")).
		OutAction("Ready", P("n", "Integer")).
		OutAction("ChildBoot").Build()
	grandInterface := Interface("Grand").
		InAction("Begin", P("n", "Integer")).
		OutAction("Done", P("n", "Integer")).
		OutAction("GrandBoot").Build()
	if err := architecture.SetReturnInterface(rootInterface); err != nil {
		t.Fatal(err)
	}
	grandID := DeterministicArchitectureInstanceID("child", "grand")
	declarations := []ArchitectureInstanceDeclaration{
		ArchitectureInstance("child", "ChildArchitecture", childInterface),
		ArchitectureInstanceWithin("child", "grand", "GrandArchitecture", grandInterface),
	}
	if reverse {
		declarations[0], declarations[1] = declarations[1], declarations[0]
	}
	for _, declaration := range declarations {
		if err := architecture.AddDeterministicArchitectureInstance(declaration); err != nil {
			t.Fatal(err)
		}
	}

	driver := NewComponent("driver", Interface("Driver").
		OutAction("Send", P("n", "Integer")).Build(), nil)
	relayID := DeterministicArchitectureComponentID("child", "relay")
	relay := NewComponent(relayID, Interface("Relay").
		InAction("Accept", P("n", "Integer")).
		OutAction("Forward", P("n", "Integer")).Build(), nil)
	workerID := DeterministicArchitectureComponentID(grandID, "worker")
	worker := NewComponent(workerID, Interface("Worker").
		InAction("Work", P("n", "Integer")).
		OutAction("Result", P("n", "Integer")).
		OutAction("Boot").Build(), nil)
	sink := NewComponent("sink", Interface("Sink").
		InAction("Receive", P("n", "Integer")).Build(), nil)
	if err := relay.AddDeclarativeRule(Rule("forward").
		On(pattern.MatchEvent("Accept").BindParam("n", pattern.Var("N").WithType("Integer"))).
		Emit("Forward", BindingParam("n", "N")).Build()); err != nil {
		t.Fatal(err)
	}
	if err := worker.SetInitialStatements(CallAction("boot", "Boot")); err != nil {
		t.Fatal(err)
	}
	if err := worker.AddDeclarativeRule(Rule("result").
		On(pattern.MatchEvent("Work").BindParam("n", pattern.Var("N").WithType("Integer"))).
		Emit("Result", BindingParam("n", "N")).Build()); err != nil {
		t.Fatal(err)
	}
	components := []struct {
		component *Component
		owner     string
	}{
		{driver, ArchitectureInterfaceID},
		{relay, "child"},
		{worker, grandID},
		{sink, ArchitectureInterfaceID},
	}
	if reverse {
		for left, right := 0, len(components)-1; left < right; left, right = left+1, right-1 {
			components[left], components[right] = components[right], components[left]
		}
	}
	for _, declaration := range components {
		if err := architecture.AddComponentInArchitecture(declaration.component, declaration.owner); err != nil {
			t.Fatal(err)
		}
	}

	connections := []*Connection{
		Connect("driver", "child").IdentifiedBy("root/request").
			On(pattern.MatchEvent("Send")).Send("Request").Build(),
		Connect("child", relayID).WithinArchitecture("child").IdentifiedBy("child/accept").
			On(pattern.MatchEvent("Request")).Send("Accept").Build(),
		Connect(relayID, grandID).WithinArchitecture("child").IdentifiedBy("child/begin").
			On(pattern.MatchEvent("Forward")).Send("Begin").Build(),
		Connect(grandID, workerID).WithinArchitecture(grandID).IdentifiedBy("grand/work").
			On(pattern.MatchEvent("Begin")).Send("Work").Build(),
		Connect(workerID, grandID).WithinArchitecture(grandID).IdentifiedBy("grand/done").
			On(pattern.MatchEvent("Result")).Send("Done").Build(),
		Connect(grandID, "child").WithinArchitecture("child").IdentifiedBy("child/ready").
			On(pattern.MatchEvent("Done")).Send("Ready").Build(),
		Connect("child", "sink").IdentifiedBy("root/receive").
			On(pattern.MatchEvent("Ready")).Send("Receive").Build(),
	}
	if reverse {
		for left, right := 0, len(connections)-1; left < right; left, right = left+1, right-1 {
			connections[left], connections[right] = connections[right], connections[left]
		}
	}
	for _, connection := range connections {
		if err := architecture.AddConnection(connection); err != nil {
			t.Fatal(err)
		}
	}

	if err := architecture.SetDeterministicArchitectureInitialStatements(
		grandID, CallAction("grand-boot", "GrandBoot"),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.SetDeterministicArchitectureInitialStatements(
		"child", CallAction("child-boot", "ChildBoot"),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.SetDeterministicArchitectureInitialStatements(
		ArchitectureInterfaceID, CallAction("root-boot", "RootBoot"),
	); err != nil {
		t.Fatal(err)
	}

	rootConstraints := constraint.NewConstraintSet("root-recursive-visibility")
	rootConstraints.Add(constraint.NewConstraint("hide-grand-boundary").
		MustNever("grand-boot", pattern.MatchEvent("GrandBoot"), "grandchild boundary is not a direct root constituent").Build())
	architecture.WithConstraints(rootConstraints, constraint.CheckOnEvent)
	childConstraints := constraint.NewConstraintSet("child-recursive-visibility")
	childConstraints.Add(constraint.NewConstraint("hide-grand-internals").
		MustNever("work", pattern.MatchEvent("Work"), "grandchild internals are hidden from the parent").Build())
	if err := architecture.SetDeterministicArchitectureInstanceConstraints("child", childConstraints); err != nil {
		t.Fatal(err)
	}
	grandConstraints := constraint.NewConstraintSet("grand-recursive-visibility")
	grandConstraints.Add(constraint.NewConstraint("hide-root-components").
		MustNever("send", pattern.MatchEvent("Send"), "root components are hidden from the grandchild").Build())
	if err := architecture.SetDeterministicArchitectureInstanceConstraints(grandID, grandConstraints); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestDeterministicRecursiveArchitectureCausalityVisibilityAndReplay(t *testing.T) {
	architecture := deterministicRecursiveArchitecture(t, false)
	grandID := DeterministicArchitectureInstanceID("child", "grand")
	if err := architecture.AddConnection(
		Connect("driver", grandID).IdentifiedBy("root/grand-leak").Send("Begin").Build(),
	); err == nil || !strings.Contains(err.Error(), "not visible") {
		t.Fatalf("root-to-grandchild scope leak error=%v", err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	workerID := DeterministicArchitectureComponentID(grandID, "worker")
	journal := NewExecutionJournal(digest, 80, InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 11},
	})
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	rootStart := onlySourceNamedEvent(t, result.Poset, ArchitectureInterfaceID, ArchitectureStartAction)
	childStart := onlySourceNamedEvent(t, result.Poset, architectureInstanceAuditID("child"), ArchitectureStartAction)
	grandStart := onlySourceNamedEvent(t, result.Poset, architectureInstanceAuditID(grandID), ArchitectureStartAction)
	if causes := result.Poset.DirectCauses(childStart.ID); len(causes) != 1 || causes[0].ID != rootStart.ID {
		t.Fatalf("child Start causes=%#v, want root Start", causes)
	}
	if causes := result.Poset.DirectCauses(grandStart.ID); len(causes) != 1 || causes[0].ID != childStart.ID {
		t.Fatalf("grandchild Start causes=%#v, want child Start", causes)
	}
	grandBoot := onlySourceNamedEvent(t, result.Poset, grandID, "GrandBoot")
	if causes := result.Poset.DirectCauses(grandBoot.ID); len(causes) != 1 || causes[0].ID != grandStart.ID {
		t.Fatalf("grandchild architecture initial causes=%#v, want grandchild Start", causes)
	}
	childBoot := onlySourceNamedEvent(t, result.Poset, "child", "ChildBoot")
	if causes := result.Poset.DirectCauses(childBoot.ID); len(causes) != 1 || causes[0].ID != childStart.ID {
		t.Fatalf("child architecture initial causes=%#v, want child Start", causes)
	}
	rootBoot := onlySourceNamedEvent(t, result.Poset, ArchitectureInterfaceID, "RootBoot")
	if causes := result.Poset.DirectCauses(rootBoot.ID); len(causes) != 1 || causes[0].ID != rootStart.ID {
		t.Fatalf("root architecture initial causes=%#v, want root Start", causes)
	}
	boot := onlySourceNamedEvent(t, result.Poset, workerID, "Boot")
	if causes := result.Poset.DirectCauses(boot.ID); len(causes) != 1 || causes[0].ID != grandStart.ID {
		t.Fatalf("grandchild module initial causes=%#v, want grandchild Start", causes)
	}
	resultEvent := onlySourceNamedEvent(t, result.Poset, workerID, "Result")
	if resultEvent.ParamInt("n") != 11 || !resultEvent.HasObservation(grandID, "Done") ||
		!resultEvent.HasObservation("child", "Ready") || !resultEvent.HasObservation("sink", "Receive") {
		t.Fatalf("recursive result did not cross every boundary: %#v", resultEvent.ObservationViews())
	}
	if result.Constraints == nil || !result.Constraints.Passed || len(result.ArchitectureConstraints) != 2 {
		t.Fatalf("recursive constraint reports=%#v root=%#v", result.ArchitectureConstraints, result.Constraints)
	}
	for _, report := range result.ArchitectureConstraints {
		if !report.Report.Passed {
			t.Fatalf("architecture %q visibility constraint failed: %#v", report.ArchitectureInstance, report.Report)
		}
	}
	initialTargets := make([]string, 0, 3)
	for _, firing := range result.Firings {
		if firing.Transition == "architecture-initial" {
			initialTargets = append(initialTargets, firing.Target)
		}
	}
	wantInitialTargets := []string{grandID, "child", ArchitectureInterfaceID}
	if len(initialTargets) != len(wantInitialTargets) {
		t.Fatalf("architecture initial targets=%#v", initialTargets)
	}
	for index := range wantInitialTargets {
		if initialTargets[index] != wantInitialTargets[index] {
			t.Fatalf("architecture initial targets=%#v, want %#v", initialTargets, wantInitialTargets)
		}
	}
	encoded, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, replayedBytes) {
		t.Fatal("recursive architecture replay changed canonical artifact bytes")
	}
	explored, err := architecture.ExploreDeterministic(journal, ExplorationLimits{
		MaxExecutions: 64, MaxChoiceDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 1 {
		t.Fatalf("recursive architecture exploration=%#v", explored)
	}
}

func TestRecursiveArchitectureCanonicalizationIgnoresDeclarationOrderAndHostParallelism(t *testing.T) {
	left := deterministicRecursiveArchitecture(t, false)
	right := deterministicRecursiveArchitecture(t, true)
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("recursive declaration order changed model digest: %s != %s", leftDigest, rightDigest)
	}
	journal := NewExecutionJournal(leftDigest, 80, InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 11},
	})
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	one, err := left.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	two, err := right.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	oneBytes, _ := one.MarshalCanonical()
	twoBytes, _ := two.MarshalCanonical()
	if !bytes.Equal(oneBytes, twoBytes) {
		t.Fatal("recursive artifact changed with declaration order or GOMAXPROCS")
	}

	missing := NewArchitecture("missing-recursive-parent")
	if err := missing.AddDeterministicArchitectureInstance(ArchitectureInstanceWithin(
		"missing", "grand", "GrandArchitecture", Interface("Grand").Build(),
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := missing.DeterministicModelDigest(); err == nil || !strings.Contains(err.Error(), "missing parent") {
		t.Fatalf("missing recursive parent validation error=%v", err)
	}
}
