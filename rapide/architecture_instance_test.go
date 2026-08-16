package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func nestedArchitectureSource(application string) []byte {
	return []byte(`
type Driver is interface action out Send(n : Integer); end interface Driver;
type Sink is interface action in Receive(n : Integer); end interface Sink;
type Child is interface
  action in Request(n : Integer);
  action out Ready(n : Integer);
end interface Child;
type Worker is interface
  action in Begin(n : Integer);
  action out Boot();
  action out Done(n : Integer);
end interface Worker;

module WorkerModule(Offset : Integer) return Worker is
  C : Clock is MakeClock();
initial
  Boot();
parallel
  when (?N : Integer) Begin(?N) do Done(?N + Offset); end when;
end module WorkerModule;

architecture ChildArchitecture(Offset : Integer is 2) return Child is
  worker : Worker is WorkerModule(Offset);
connect
  (?N : Integer) Request(?N) to worker.Begin(?N);
  (?N : Integer) worker.Done(?N) to Ready(?N);
constraint
  never worker.Boot;
end architecture ChildArchitecture;

architecture System() is
  driver : Driver;
  child : Child is ChildArchitecture(` + application + `);
  sink : Sink;
connect
  (?N : Integer) driver.Send(?N) to child.Request(?N);
  (?N : Integer) child.Ready(?N) to sink.Receive(?N);
end architecture System;
`)
}

func TestCompileNestedArchitectureGeneratorExecutesAcrossBothBoundaries(t *testing.T) {
	model, err := Compile(nestedArchitectureSource(""), "System")
	if err != nil {
		t.Fatal(err)
	}
	components := model.Components()
	workerID := nestedComponentID("child", "worker")
	if len(components) != 3 || components[0].ID != workerID ||
		components[1].ID != "driver" || components[2].ID != "sink" {
		t.Fatalf("flattened nested components=%#v", components)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 30, arch.InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 7},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	rootStarts := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, arch.ArchitectureStartAction)
	childStarts := sourceNamedEvents(result.Poset, "$architecture/child", arch.ArchitectureStartAction)
	if len(rootStarts) != 1 || len(childStarts) != 1 {
		t.Fatalf("root/child Start counts=%d/%d", len(rootStarts), len(childStarts))
	}
	if causes := result.Poset.DirectCauses(childStarts[0].ID); len(causes) != 1 || causes[0].ID != rootStarts[0].ID {
		t.Fatalf("child Start causes=%#v", causes)
	}
	boot := sourceNamedEvents(result.Poset, workerID, "Boot")
	moduleStart := sourceNamedEvents(result.Poset, "$module/"+workerID, arch.ArchitectureStartAction)
	if len(boot) != 1 {
		t.Fatalf("nested module Boot=%#v", boot)
	}
	if len(moduleStart) != 1 {
		t.Fatalf("nested module Start=%#v", moduleStart)
	}
	if causes := result.Poset.DirectCauses(moduleStart[0].ID); len(causes) != 1 || causes[0].ID != childStarts[0].ID {
		t.Fatalf("nested module Start causes=%#v", causes)
	}
	if causes := result.Poset.DirectCauses(boot[0].ID); len(causes) != 1 || causes[0].ID != moduleStart[0].ID {
		t.Fatalf("nested module initial causes=%#v", causes)
	}
	request := sourceNamedEvents(result.Poset, "driver", "Send")
	if len(request) != 1 || !request[0].HasObservation("child", "Request") ||
		!request[0].HasObservation(workerID, "Begin") {
		t.Fatalf("nested request observations=%#v", request)
	}
	done := sourceNamedEvents(result.Poset, workerID, "Done")
	if len(done) != 1 || done[0].ParamInt("n") != 9 ||
		!done[0].HasObservation("child", "Ready") || !done[0].HasObservation("sink", "Receive") {
		t.Fatalf("nested response observations=%#v", done)
	}
	if !result.Poset.IsCausallyBefore(request[0].ID, done[0].ID) {
		t.Fatal("nested module result does not depend on the parent request")
	}
	if len(result.ArchitectureConstraints) != 1 ||
		result.ArchitectureConstraints[0].ArchitectureInstance != "child" ||
		result.ArchitectureConstraints[0].Report.Passed ||
		len(result.ArchitectureConstraints[0].Report.Reports) != 1 ||
		len(result.ArchitectureConstraints[0].Report.Reports[0].Violations) != 1 {
		t.Fatalf("child architecture constraint report=%#v", result.ArchitectureConstraints)
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
		t.Fatal("source nested architecture replay changed canonical bytes")
	}
	explored, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 2, MaxChoiceDepth: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 1 {
		t.Fatalf("nested source exploration=%#v", explored)
	}
}

func TestNestedArchitectureNamedDefaultsAreCanonicalAndHostIndependent(t *testing.T) {
	applications := []string{"", "2", "Offset is 2"}
	models := make([]*arch.Architecture, len(applications))
	var expectedDigest string
	for index, application := range applications {
		model, err := Compile(nestedArchitectureSource(application), "system")
		if err != nil {
			t.Fatal(err)
		}
		models[index] = model
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			expectedDigest = digest
		} else if digest != expectedDigest {
			t.Fatalf("architecture association %q changed canonical model identity", application)
		}
	}
	journal := arch.NewExecutionJournal(expectedDigest, 30, arch.InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 4},
	})
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	first, err := models[0].ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	last, err := models[len(models)-1].ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := first.MarshalCanonical()
	lastBytes, _ := last.MarshalCanonical()
	if !bytes.Equal(firstBytes, lastBytes) {
		t.Fatal("equivalent architecture associations or GOMAXPROCS changed artifact bytes")
	}
	overridden, err := Compile(nestedArchitectureSource("Offset is 5"), "System")
	if err != nil {
		t.Fatal(err)
	}
	overriddenDigest, err := overridden.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if overriddenDigest == expectedDigest {
		t.Fatal("nested architecture argument override was omitted from model identity")
	}
	overriddenResult, err := overridden.ExecuteDeterministic(arch.NewExecutionJournal(
		overriddenDigest, 30, arch.InputEvent{
			Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 4},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	if done := sourceNamedEvents(overriddenResult.Poset, nestedComponentID("child", "worker"), "Done"); len(done) != 1 || done[0].ParamInt("n") != 9 {
		t.Fatalf("nested architecture override result=%#v", done)
	}
}

func TestNestedArchitectureSourceGatesAreExplicit(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		message string
	}{
		{
			name: "direct recursive generator",
			source: `
type Boundary is interface end interface Boundary;
architecture Root() return Boundary is again : Boundary is Root(); end architecture Root;
`,
			message: "has no finite elaboration",
		},
		{
			name: "mutual recursive generators",
			source: `
type Boundary is interface end interface Boundary;
architecture Child() return Boundary is back : Boundary is Root(); end architecture Child;
architecture Root() return Boundary is child : Boundary is Child(); end architecture Root;
`,
			message: "has no finite elaboration",
		},
		{
			name: "return mismatch",
			source: `
type A is interface end interface A;
type B is interface end interface B;
architecture Child() return A is end architecture Child;
architecture Root() is child : B is Child(); end architecture Root;
`,
			message: "does not exactly match",
		},
		{
			name: "unknown association",
			source: `
type Boundary is interface end interface Boundary;
architecture Child(Count : Integer) return Boundary is end architecture Child;
architecture Root() is child : Boundary is Child(Missing is 1); end architecture Root;
`,
			message: "no formal parameter named",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "Root")
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v, want %q", err, test.message)
			}
		})
	}
}

func recursiveArchitectureSource() []byte {
	return []byte(`
type RootBoundary is interface action out RootBoot(n : Integer); end interface RootBoundary;
type Driver is interface action out Send(n : Integer); end interface Driver;
type Sink is interface action in Receive(n : Integer); end interface Sink;
type Child is interface
  action in Request(n : Integer);
  action out Ready(n : Integer);
  action out ChildBoot(n : Integer);
end interface Child;
type Grand is interface
  action in Begin(n : Integer);
  action out Done(n : Integer);
  action out GrandBoot(n : Integer);
end interface Grand;
type Relay is interface
  action in Accept(n : Integer);
  action out Forward(n : Integer);
end interface Relay;
type Worker is interface
  action in Work(n : Integer);
  action out Result(n : Integer);
  action out Boot();
end interface Worker;

module RelayModule(Offset : Integer) return Relay is
parallel
  when (?N : Integer) Accept(?N) do Forward(?N + Offset); end when;
end module RelayModule;

module WorkerModule(Add : Integer) return Worker is
initial
  Boot();
parallel
  when (?N : Integer) Work(?N) do Result(?N + Add); end when;
end module WorkerModule;

architecture GrandArchitecture(Add : Integer) return Grand is
  worker : Worker is WorkerModule(Add);
connect
  (?N : Integer) Begin(?N) to worker.Work(?N);
  (?N : Integer) worker.Result(?N) to Done(?N);
constraint
  never worker.Boot;
initial
  GrandBoot(Add);
end architecture GrandArchitecture;

architecture ChildArchitecture(Offset : Integer) return Child is
  relay : Relay is RelayModule(Offset);
  grand : Grand is GrandArchitecture(Add is Offset + 1);
connect
  (?N : Integer) Request(?N) to relay.Accept(?N);
  (?N : Integer) relay.Forward(?N) to grand.Begin(?N);
  (?N : Integer) grand.Done(?N) to Ready(?N);
constraint
  never grand.GrandBoot;
initial
  ChildBoot(Offset);
end architecture ChildArchitecture;

architecture System() return RootBoundary is
  driver : Driver;
  child : Child is ChildArchitecture(Offset is 2);
  sink : Sink;
connect
  (?N : Integer) driver.Send(?N) to child.Request(?N);
  (?N : Integer) child.Ready(?N) to sink.Receive(?N);
constraint
  never child.ChildBoot;
initial
  RootBoot(1);
end architecture System;
`)
}

func reorderedRecursiveArchitectureSource() []byte {
	source := string(recursiveArchitectureSource())
	replacements := [][2]string{
		{
			"  relay : Relay is RelayModule(Offset);\n  grand : Grand is GrandArchitecture(Add is Offset + 1);",
			"  grand : Grand is GrandArchitecture(Add is Offset + 1);\n  relay : Relay is RelayModule(Offset);",
		},
		{
			"  (?N : Integer) Request(?N) to relay.Accept(?N);\n  (?N : Integer) relay.Forward(?N) to grand.Begin(?N);\n  (?N : Integer) grand.Done(?N) to Ready(?N);",
			"  (?N : Integer) grand.Done(?N) to Ready(?N);\n  (?N : Integer) relay.Forward(?N) to grand.Begin(?N);\n  (?N : Integer) Request(?N) to relay.Accept(?N);",
		},
		{
			"  driver : Driver;\n  child : Child is ChildArchitecture(Offset is 2);\n  sink : Sink;",
			"  sink : Sink;\n  child : Child is ChildArchitecture(Offset is 2);\n  driver : Driver;",
		},
		{
			"  (?N : Integer) driver.Send(?N) to child.Request(?N);\n  (?N : Integer) child.Ready(?N) to sink.Receive(?N);",
			"  (?N : Integer) child.Ready(?N) to sink.Receive(?N);\n  (?N : Integer) driver.Send(?N) to child.Request(?N);",
		},
	}
	for _, replacement := range replacements {
		source = strings.Replace(source, replacement[0], replacement[1], 1)
	}
	return []byte(source)
}

func TestRecursiveSourceArchitectureCallsPreserveOwnershipAndReplay(t *testing.T) {
	model, err := Compile(recursiveArchitectureSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	grandID := arch.DeterministicArchitectureInstanceID("child", "grand")
	relayID := arch.DeterministicArchitectureComponentID("child", "relay")
	workerID := arch.DeterministicArchitectureComponentID(grandID, "worker")
	components := model.Components()
	componentIDs := make([]string, len(components))
	for index, component := range components {
		componentIDs[index] = component.ID
	}
	wantComponents := []string{workerID, relayID, "driver", "sink"}
	if len(componentIDs) != len(wantComponents) {
		t.Fatalf("recursive source components=%#v", componentIDs)
	}
	for index := range wantComponents {
		if componentIDs[index] != wantComponents[index] {
			t.Fatalf("recursive source components=%#v, want %#v", componentIDs, wantComponents)
		}
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
	rootStart := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, arch.ArchitectureStartAction)
	childStart := sourceNamedEvents(result.Poset, "$architecture/child", arch.ArchitectureStartAction)
	grandStart := sourceNamedEvents(result.Poset, grandID, arch.ArchitectureStartAction)
	if len(rootStart) != 1 || len(childStart) != 1 || len(grandStart) != 1 {
		t.Fatalf("recursive Start events root=%#v child=%#v grand=%#v", rootStart, childStart, grandStart)
	}
	if causes := result.Poset.DirectCauses(childStart[0].ID); len(causes) != 1 || causes[0].ID != rootStart[0].ID {
		t.Fatalf("recursive child Start causes=%#v", causes)
	}
	if causes := result.Poset.DirectCauses(grandStart[0].ID); len(causes) != 1 || causes[0].ID != childStart[0].ID {
		t.Fatalf("recursive grandchild Start causes=%#v", causes)
	}
	workerBoot := sourceNamedEvents(result.Poset, workerID, "Boot")
	workerStart := sourceNamedEvents(result.Poset, "$module/"+workerID, arch.ArchitectureStartAction)
	if len(workerBoot) != 1 {
		t.Fatalf("recursive worker Boot=%#v", workerBoot)
	}
	if len(workerStart) != 1 {
		t.Fatalf("recursive worker Start=%#v", workerStart)
	}
	if causes := result.Poset.DirectCauses(workerStart[0].ID); len(causes) != 1 || causes[0].ID != grandStart[0].ID {
		t.Fatalf("recursive worker Start causes=%#v", causes)
	}
	if causes := result.Poset.DirectCauses(workerBoot[0].ID); len(causes) != 1 || causes[0].ID != workerStart[0].ID {
		t.Fatalf("recursive worker initial causes=%#v", causes)
	}
	grandBoot := sourceNamedEvents(result.Poset, grandID, "GrandBoot")
	childBoot := sourceNamedEvents(result.Poset, "child", "ChildBoot")
	rootBoot := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "RootBoot")
	if len(grandBoot) != 1 || len(childBoot) != 1 || len(rootBoot) != 1 ||
		grandBoot[0].ParamInt("n") != 3 || childBoot[0].ParamInt("n") != 2 || rootBoot[0].ParamInt("n") != 1 {
		t.Fatalf("recursive architecture initials grand=%#v child=%#v root=%#v", grandBoot, childBoot, rootBoot)
	}
	if causes := result.Poset.DirectCauses(grandBoot[0].ID); len(causes) != 1 || causes[0].ID != grandStart[0].ID {
		t.Fatalf("recursive grand initial causes=%#v", causes)
	}
	resultEvents := sourceNamedEvents(result.Poset, workerID, "Result")
	if len(resultEvents) != 1 || resultEvents[0].ParamInt("n") != 10 ||
		!resultEvents[0].HasObservation(grandID, "Done") ||
		!resultEvents[0].HasObservation("child", "Ready") ||
		!resultEvents[0].HasObservation("sink", "Receive") {
		t.Fatalf("recursive source result=%#v", resultEvents)
	}
	if result.Constraints == nil || result.Constraints.Passed || len(result.ArchitectureConstraints) != 2 ||
		result.ArchitectureConstraints[0].ArchitectureInstance != "child" ||
		result.ArchitectureConstraints[0].Report.Passed ||
		result.ArchitectureConstraints[1].ArchitectureInstance != grandID ||
		result.ArchitectureConstraints[1].Report.Passed {
		t.Fatalf("recursive source constraint reports root=%#v children=%#v", result.Constraints, result.ArchitectureConstraints)
	}
	initialTargets := make([]string, 0, 3)
	for _, firing := range result.Firings {
		if firing.Transition == "architecture-initial" {
			initialTargets = append(initialTargets, firing.Target)
		}
	}
	wantInitialTargets := []string{grandID, "child", arch.ArchitectureInterfaceID}
	if len(initialTargets) != len(wantInitialTargets) {
		t.Fatalf("recursive source initial order=%#v", initialTargets)
	}
	for index := range wantInitialTargets {
		if initialTargets[index] != wantInitialTargets[index] {
			t.Fatalf("recursive source initial order=%#v, want %#v", initialTargets, wantInitialTargets)
		}
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
		t.Fatal("recursive source replay changed canonical bytes")
	}
	explored, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 64, MaxChoiceDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 1 {
		t.Fatalf("recursive source exploration=%#v", explored)
	}
}

func TestRecursivelyNestedArchitectureLiteralUsesCanonicalOwnerPath(t *testing.T) {
	source := []byte(`
	type Boundary is interface
	  action out Inner(n : Integer);
	  action out Outer(n : Integer);
	end interface Boundary;
type Sink is interface action in Receive(n : Integer); end interface Sink;

architecture Child() return Boundary is
  grand : Boundary is architecture
	  connect
	  constraint
	    never Inner;
	  initial
	    Inner(2);
	  end architecture;
	connect
	  (?N : Integer) grand.Inner(?N) to Outer(?N);
end architecture Child;

architecture Root() is
  child : Boundary is Child();
	  sink : Sink;
	connect
	  (?N : Integer) child.Outer(?N) to sink.Receive(?N);
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30))
	if err != nil {
		t.Fatal(err)
	}
	grandID := arch.DeterministicArchitectureInstanceID("child", "grand")
	grandStart := sourceNamedEvents(result.Poset, grandID, arch.ArchitectureStartAction)
	grandBoot := sourceNamedEvents(result.Poset, grandID, "Inner")
	if len(grandStart) != 1 || len(grandBoot) != 1 || grandBoot[0].ParamInt("n") != 2 ||
		!grandBoot[0].HasObservation("child", "Outer") || !grandBoot[0].HasObservation("sink", "Receive") {
		t.Fatalf("recursive literal result start=%#v boot=%#v", grandStart, grandBoot)
	}
	if len(result.ArchitectureConstraints) != 1 ||
		result.ArchitectureConstraints[0].ArchitectureInstance != grandID ||
		result.ArchitectureConstraints[0].Report.Passed {
		t.Fatalf("recursive literal constraint report=%#v", result.ArchitectureConstraints)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(arch.NewExecutionJournal(digest, 30), artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("recursive literal replay changed canonical bytes")
	}
}

func TestRecursiveSourceArchitectureIsOrderAndHostIndependent(t *testing.T) {
	left, err := Compile(recursiveArchitectureSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	right, err := Compile(reorderedRecursiveArchitectureSource(), "system")
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
		t.Fatalf("recursive source declaration order changed model identity: %s != %s", leftDigest, rightDigest)
	}
	journal := arch.NewExecutionJournal(leftDigest, 80, arch.InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 5},
	})
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	leftResult, err := left.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	rightResult, err := right.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	leftBytes, _ := leftResult.MarshalCanonical()
	rightBytes, _ := rightResult.MarshalCanonical()
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatal("recursive source declaration order or GOMAXPROCS changed canonical artifact bytes")
	}
}

func TestRecursiveSourceCompoundConnectionIsOwnerScopedAndReplayable(t *testing.T) {
	source := []byte(`
type Driver is interface
  action out First(n : Integer);
  action out Second(n : Integer);
end interface Driver;
type Sink is interface action in Receive(n : Integer); end interface Sink;
type Boundary is interface
  action in First(n : Integer);
  action in Second(n : Integer);
  action out GrandCombined(n : Integer);
  action out ChildCombined(n : Integer);
end interface Boundary;
type Left is interface
  action in Trigger(n : Integer);
  action out Begin(n : Integer);
end interface Left;
type Right is interface
  action in Trigger(n : Integer);
  action out Finish(n : Integer);
end interface Right;

module LeftModule() return Left is
parallel
  when (?N : Integer) Trigger(?N) do Begin(?N); end when;
end module LeftModule;
module RightModule() return Right is
parallel
  when (?N : Integer) Trigger(?N) do Finish(?N); end when;
end module RightModule;

architecture Grand() return Boundary is
  left : Left is LeftModule();
  right : Right is RightModule();
connect
  (?N : Integer) First(?N) to left.Trigger(?N);
  (?N : Integer) Second(?N) to right.Trigger(?N);
  (?N : Integer) (left.Begin(?N) || right.Finish(?N)) ||> GrandCombined(?N + 1);
constraint
  never GrandCombined;
end architecture Grand;

architecture Child() return Boundary is
  grand : Boundary is Grand();
connect
  (?N : Integer) First(?N) to grand.First(?N);
  (?N : Integer) Second(?N) to grand.Second(?N);
  (?N : Integer) grand.GrandCombined(?N) to ChildCombined(?N);
constraint
  never grand.GrandCombined;
end architecture Child;

architecture Root() is
  driver : Driver;
  child : Boundary is Child();
  sink : Sink;
connect
  (?N : Integer) driver.First(?N) to child.First(?N);
  (?N : Integer) driver.Second(?N) to child.Second(?N);
  (?N : Integer) child.ChildCombined(?N) to sink.Receive(?N);
constraint
  never child.ChildCombined;
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 80,
		arch.InputEvent{Key: "first", Source: "driver", Action: "First", Params: map[string]any{"n": 9}},
		arch.InputEvent{Key: "second", Source: "driver", Action: "Second", Params: map[string]any{"n": 9}},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	grandID := arch.DeterministicArchitectureInstanceID("child", "grand")
	leftID := arch.DeterministicArchitectureComponentID(grandID, "left")
	rightID := arch.DeterministicArchitectureComponentID(grandID, "right")
	combined := sourceNamedEvents(result.Poset, grandID, "GrandCombined")
	begin := sourceNamedEvents(result.Poset, leftID, "Begin")
	finish := sourceNamedEvents(result.Poset, rightID, "Finish")
	if len(combined) != 1 || len(begin) != 1 || len(finish) != 1 ||
		combined[0].ParamInt("n") != 10 ||
		!combined[0].HasObservation("child", "ChildCombined") ||
		!combined[0].HasObservation("sink", "Receive") {
		t.Fatalf("recursive compound result combined=%#v begin=%#v finish=%#v", combined, begin, finish)
	}
	if !result.Poset.IsCausallyBefore(begin[0].ID, combined[0].ID) ||
		!result.Poset.IsCausallyBefore(finish[0].ID, combined[0].ID) {
		t.Fatal("recursive compound output does not depend on its complete owner-local match")
	}
	foundCompoundFiring := false
	for _, firing := range result.Firings {
		if firing.Target == grandID && len(firing.MatchedEvents) == 2 {
			foundCompoundFiring = true
			break
		}
	}
	if !foundCompoundFiring {
		t.Fatalf("recursive compound firing audit=%#v", result.Firings)
	}
	if result.Constraints == nil || result.Constraints.Passed || len(result.ArchitectureConstraints) != 2 ||
		result.ArchitectureConstraints[0].Report.Passed || result.ArchitectureConstraints[1].Report.Passed {
		t.Fatalf("recursive compound constraint reports root=%#v children=%#v", result.Constraints, result.ArchitectureConstraints)
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
		t.Fatal("recursive compound replay changed canonical bytes")
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
		t.Fatalf("recursive compound exploration=%#v", explored)
	}
	leakingSource := bytes.Replace(
		source,
		[]byte("(left.Begin(?N) || right.Finish(?N))"),
		[]byte("(driver.First(?N) || right.Finish(?N))"),
		1,
	)
	if _, err := Compile(leakingSource, "Root"); err == nil ||
		!strings.Contains(err.Error(), "component \"driver\" is not declared") {
		t.Fatalf("recursive compound parent-scope leak error=%v", err)
	}
}

func recursiveFunctionArchitectureSource(connections string) []byte {
	return []byte(`
type Driver is interface action out Send(n : Integer); end interface Driver;
type Sink is interface action in Receive(n : Integer); end interface Sink;
type Boundary is interface
  action in Request(n : Integer);
  action out Ready(n : Integer);
end interface Boundary;
type Client is interface
  action in Begin(n : Integer);
  action out Done(n : Integer);
  requires Lookup : function(value : Integer) return Integer;
  behavior
    result : var Integer := 0;
  begin
    (?N : Integer) Begin(?N) =>
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

architecture Grand() return Boundary is
  client : Client;
  server : Server;
connect
` + connections + `
end architecture Grand;

architecture Child() return Boundary is
  grand : Boundary is Grand();
connect
  (?N : Integer) Request(?N) to grand.Request(?N);
  (?N : Integer) grand.Ready(?N) to Ready(?N);
end architecture Child;

architecture Root() is
  driver : Driver;
  child : Boundary is Child();
  sink : Sink;
connect
  (?N : Integer) driver.Send(?N) to child.Request(?N);
  (?N : Integer) child.Ready(?N) to sink.Receive(?N);
end architecture Root;
`)
}

func TestRecursiveSourceFunctionConnectionIsOwnerScopedAndReplayable(t *testing.T) {
	connections := `
  client.Lookup to server.Fetch;
  (?N : Integer) Request(?N) to client.Begin(?N);
  (?N : Integer) client.Done(?N) to Ready(?N);
`
	model, err := Compile(recursiveFunctionArchitectureSource(connections), "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 80, arch.InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 3},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	grandID := arch.DeterministicArchitectureInstanceID("child", "grand")
	clientID := arch.DeterministicArchitectureComponentID(grandID, "client")
	serverID := arch.DeterministicArchitectureComponentID(grandID, "server")
	requiredCall := sourceNamedEvents(result.Poset, clientID, "Lookup'Call")
	providedCall := sourceNamedEvents(result.Poset, serverID, "Fetch'Call")
	requiredReturn := sourceNamedEvents(result.Poset, clientID, "Lookup'Return")
	providedReturn := sourceNamedEvents(result.Poset, serverID, "Fetch'Return")
	done := sourceNamedEvents(result.Poset, clientID, "Done")
	if len(requiredCall) != 1 || len(providedCall) != 1 ||
		len(requiredReturn) != 1 || len(providedReturn) != 1 || len(done) != 1 {
		t.Fatalf("recursive function events call=%#v/%#v return=%#v/%#v done=%#v",
			requiredCall, providedCall, requiredReturn, providedReturn, done)
	}
	if requiredCall[0].ID != providedCall[0].ID || requiredReturn[0].ID != providedReturn[0].ID {
		t.Fatal("recursive function connection duplicated caller/provider occurrences")
	}
	if done[0].ParamInt("n") != 7 || !done[0].HasObservation(grandID, "Ready") ||
		!done[0].HasObservation("child", "Ready") || !done[0].HasObservation("sink", "Receive") {
		t.Fatalf("recursive function result did not cross every boundary: %#v", done[0].ObservationViews())
	}
	if !result.Poset.IsCausallyBefore(requiredCall[0].ID, providedReturn[0].ID) ||
		!result.Poset.IsCausallyBefore(requiredReturn[0].ID, done[0].ID) {
		t.Fatal("recursive function connection lost synchronous causality")
	}
	if len(result.State) != 1 || result.State[0].ComponentID != clientID ||
		result.State[0].Name != "result" || result.State[0].Value.Text != "7" {
		t.Fatalf("recursive function caller state=%#v", result.State)
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
		t.Fatal("recursive function replay changed canonical bytes")
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
		t.Fatalf("recursive function exploration=%#v", explored)
	}

	reordered := `
  (?N : Integer) client.Done(?N) to Ready(?N);
  (?N : Integer) Request(?N) to client.Begin(?N);
  client.Lookup to server.Fetch;
`
	reorderedModel, err := Compile(recursiveFunctionArchitectureSource(reordered), "root")
	if err != nil {
		t.Fatal(err)
	}
	reorderedDigest, err := reorderedModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if reorderedDigest != digest {
		t.Fatalf("recursive function connection order changed model identity: %s != %s", digest, reorderedDigest)
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	one, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	two, err := reorderedModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	oneBytes, _ := one.MarshalCanonical()
	twoBytes, _ := two.MarshalCanonical()
	if !bytes.Equal(oneBytes, twoBytes) {
		t.Fatal("recursive function declaration order or GOMAXPROCS changed artifact bytes")
	}

	leaking := bytes.Replace(
		recursiveFunctionArchitectureSource(connections),
		[]byte("client.Lookup to server.Fetch"), []byte("driver.Lookup to server.Fetch"), 1,
	)
	if _, err := Compile(leaking, "Root"); err == nil ||
		!strings.Contains(err.Error(), "source component \"driver\" is not declared") {
		t.Fatalf("recursive function parent-scope leak error=%v", err)
	}
}

func TestContextTypedArchitectureLiteralUsesNestedDeterministicLowering(t *testing.T) {
	source := []byte(`
type Driver is interface action out Send(n : Integer); end interface Driver;
type Sink is interface action in Receive(n : Integer); end interface Sink;
type Child is interface
  action in Request(n : Integer);
  action out Ready(n : Integer);
end interface Child;
type Worker is interface
  action in Begin(n : Integer);
  action out Done(n : Integer);
end interface Worker;

module WorkerModule(Offset : Integer) return Worker is
parallel
  when (?N : Integer) Begin(?N) do Done(?N + Offset); end when;
end module WorkerModule;

architecture System() is
  driver : Driver;
  child : Child is architecture
    worker : Worker is WorkerModule(3);
  connect
    (?N : Integer) Request(?N) to worker.Begin(?N);
    (?N : Integer) worker.Done(?N) to Ready(?N);
  constraint
    never worker.Done;
  end architecture;
  sink : Sink;
connect
  (?N : Integer) driver.Send(?N) to child.Request(?N);
  (?N : Integer) child.Ready(?N) to sink.Receive(?N);
end architecture System;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Architectures) != 1 || len(file.Architectures[0].Components) != 3 ||
		file.Architectures[0].Components[1].ArchitectureLiteral == nil {
		t.Fatalf("architecture literal AST=%#v", file.Architectures)
	}
	literal := file.Architectures[0].Components[1].ArchitectureLiteral
	if literal.ReturnType != "Child" || literal.Name != "ArchitectureLiteral" ||
		len(literal.Components) != 1 || len(literal.Connections) != 2 {
		t.Fatalf("context-typed architecture literal=%#v", literal)
	}
	model, err := CompileFile(file, "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 30, arch.InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 6},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	done := sourceNamedEvents(result.Poset, nestedComponentID("child", "worker"), "Done")
	if len(done) != 1 || done[0].ParamInt("n") != 9 ||
		!done[0].HasObservation("child", "Ready") || !done[0].HasObservation("sink", "Receive") {
		t.Fatalf("architecture literal result=%#v", done)
	}
	if starts := sourceNamedEvents(result.Poset, "$architecture/child", arch.ArchitectureStartAction); len(starts) != 1 {
		t.Fatalf("architecture literal child Start=%#v", starts)
	}
	if len(result.ArchitectureConstraints) != 1 || result.ArchitectureConstraints[0].Report.Passed {
		t.Fatalf("architecture literal constraint report=%#v", result.ArchitectureConstraints)
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
		t.Fatal("architecture literal replay changed canonical bytes")
	}
}

func TestArchitectureInitialPartsExecuteAtOwnedStartFrontiers(t *testing.T) {
	source := []byte(`
type RootBoundary is interface action out RootBoot(n : Integer); end interface RootBoundary;
type ChildBoundary is interface action out ChildBoot(n : Integer); end interface ChildBoundary;
type Sink is interface action in Receive(n : Integer); end interface Sink;

architecture Child(Offset : Integer) return ChildBoundary is
connect
constraint
  never ChildBoot;
initial
  ChildBoot(Offset);
end architecture Child;

architecture Root() return RootBoundary is
  generated : ChildBoundary is Child(3);
  literal : ChildBoundary is architecture
  connect
  constraint
    never ChildBoot;
  initial
    ChildBoot(2);
  end architecture;
  sink : Sink;
connect
  (?N : Integer) generated.ChildBoot(?N) to sink.Receive(?N);
  (?N : Integer) literal.ChildBoot(?N) to sink.Receive(?N);
constraint
  never RootBoot;
initial
  RootBoot(1);
end architecture Root;
`)
	file, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Architectures) != 2 || len(file.Architectures[0].Initial) != 1 ||
		len(file.Architectures[1].Initial) != 1 ||
		file.Architectures[1].Components[1].ArchitectureLiteral == nil ||
		len(file.Architectures[1].Components[1].ArchitectureLiteral.Initial) != 1 {
		t.Fatalf("architecture initial AST=%#v", file.Architectures)
	}
	model, err := CompileFile(file, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 30)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	rootStart := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, arch.ArchitectureStartAction)
	generatedStart := sourceNamedEvents(result.Poset, "$architecture/generated", arch.ArchitectureStartAction)
	literalStart := sourceNamedEvents(result.Poset, "$architecture/literal", arch.ArchitectureStartAction)
	rootBoot := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "RootBoot")
	generatedBoot := sourceNamedEvents(result.Poset, "generated", "ChildBoot")
	literalBoot := sourceNamedEvents(result.Poset, "literal", "ChildBoot")
	if len(rootStart) != 1 || len(generatedStart) != 1 || len(literalStart) != 1 ||
		len(rootBoot) != 1 || len(generatedBoot) != 1 || len(literalBoot) != 1 {
		t.Fatalf("owned architecture startup events rootStart=%#v generatedStart=%#v literalStart=%#v rootBoot=%#v generatedBoot=%#v literalBoot=%#v",
			rootStart, generatedStart, literalStart, rootBoot, generatedBoot, literalBoot)
	}
	if causes := result.Poset.DirectCauses(rootBoot[0].ID); len(causes) != 1 || causes[0].ID != rootStart[0].ID {
		t.Fatalf("root initial causes=%#v, want root Start", causes)
	}
	if causes := result.Poset.DirectCauses(generatedBoot[0].ID); len(causes) != 1 || causes[0].ID != generatedStart[0].ID {
		t.Fatalf("generated child initial causes=%#v, want child Start", causes)
	}
	if causes := result.Poset.DirectCauses(literalBoot[0].ID); len(causes) != 1 || causes[0].ID != literalStart[0].ID {
		t.Fatalf("literal child initial causes=%#v, want child Start", causes)
	}
	if generatedBoot[0].ParamInt("n") != 3 || literalBoot[0].ParamInt("n") != 2 ||
		!generatedBoot[0].HasObservation("sink", "Receive") || !literalBoot[0].HasObservation("sink", "Receive") {
		t.Fatalf("child initials did not close through parent connections: generated=%#v literal=%#v",
			generatedBoot[0], literalBoot[0])
	}
	if len(result.Firings) < 3 || result.Firings[0].Transition != "architecture-initial" ||
		result.Firings[0].Target != "generated" || result.Firings[1].Transition != "architecture-initial" ||
		result.Firings[1].Target != "literal" || result.Firings[2].Transition != "architecture-initial" ||
		result.Firings[2].Target != arch.ArchitectureInterfaceID {
		t.Fatalf("architecture initial audit order=%#v", result.Firings)
	}
	if result.Constraints == nil || result.Constraints.Passed || len(result.ArchitectureConstraints) != 2 ||
		result.ArchitectureConstraints[0].ArchitectureInstance != "generated" ||
		result.ArchitectureConstraints[0].Report.Passed ||
		result.ArchitectureConstraints[1].ArchitectureInstance != "literal" ||
		result.ArchitectureConstraints[1].Report.Passed {
		t.Fatalf("architecture initial constraint reports root=%#v children=%#v",
			result.Constraints, result.ArchitectureConstraints)
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
		t.Fatal("architecture initial replay changed canonical bytes")
	}
}

func TestArchitectureInitialDeclarationBearingDoUsesOwnedExactExceptions(t *testing.T) {
	source := []byte(`
type RootBoundary is interface
  action out RootNamed(code : Integer);
  action out RootUnnamed(code : Integer);
  action out RootContinued();
  action out Wrong();
end interface RootBoundary;
type ChildBoundary is interface
  action out ChildNamed(code : Integer);
  action out ChildContinued();
  action out Wrong();
end interface ChildBoundary;

architecture Child() return ChildBoundary is
initial
  Named: declare exception Failure(code : Integer); do
    raise Named::Failure(code is 2);
  handler
    is Child::Named::Failure(code is ?Code) => ChildNamed(?Code);
  end do Named;
  ChildContinued();
end architecture Child;

architecture Root() return RootBoundary is
  child : ChildBoundary is Child();
initial
  Named: declare exception Failure(code : Integer); do
    raise Named::Failure(code is 1);
  handler
    is Root::Named::Failure(code is ?Code) => RootNamed(?Code);
  end do Named;
  declare exception Failure(code : Integer); do
    raise Failure(code is 3);
  handler
    is Failure(code is ?Code) => RootUnnamed(?Code);
  end do;
  RootContinued();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 40)
	previous := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	rootFailures := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "Failure")
	childFailures := sourceNamedEvents(result.Poset, "child", "Failure")
	rootNamed := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "RootNamed")
	rootUnnamed := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "RootUnnamed")
	rootContinued := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "RootContinued")
	childNamed := sourceNamedEvents(result.Poset, "child", "ChildNamed")
	childContinued := sourceNamedEvents(result.Poset, "child", "ChildContinued")
	if len(rootFailures) != 2 || len(childFailures) != 1 || len(rootNamed) != 1 ||
		len(rootUnnamed) != 1 || len(rootContinued) != 1 || len(childNamed) != 1 ||
		len(childContinued) != 1 || len(result.Poset.ByName("Wrong")) != 0 {
		t.Fatalf("rootFailure/childFailure/rootNamed/rootUnnamed/rootContinued/childNamed/childContinued=%d/%d/%d/%d/%d/%d/%d",
			len(rootFailures), len(childFailures), len(rootNamed), len(rootUnnamed),
			len(rootContinued), len(childNamed), len(childContinued))
	}
	byCode := make(map[int]*gorapide.Event, len(rootFailures))
	for _, failure := range rootFailures {
		byCode[failure.ParamInt("code")] = failure
	}
	if byCode[1] == nil || byCode[3] == nil || childFailures[0].ParamInt("code") != 2 ||
		rootNamed[0].ParamInt("code") != 1 || rootUnnamed[0].ParamInt("code") != 3 ||
		childNamed[0].ParamInt("code") != 2 {
		t.Fatalf("owned architecture exceptions root=%#v child=%#v", rootFailures, childFailures)
	}
	assertOnlyDirectCause(t, result.Poset, rootNamed[0], byCode[1])
	assertOnlyDirectCause(t, result.Poset, byCode[3], rootNamed[0])
	assertOnlyDirectCause(t, result.Poset, rootUnnamed[0], byCode[3])
	assertOnlyDirectCause(t, result.Poset, rootContinued[0], rootUnnamed[0])
	assertOnlyDirectCause(t, result.Poset, childNamed[0], childFailures[0])
	assertOnlyDirectCause(t, result.Poset, childContinued[0], childNamed[0])
	if !result.Poset.IsCausallyIndependent(rootNamed[0].ID, childNamed[0].ID) {
		t.Fatal("root and child architecture initial recovery gained a false causal edge")
	}
	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed owned architecture initial exception recovery")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("architecture initial exception replay changed canonical bytes")
	}
}

func TestArchitectureInitialClosedControlAndRangesAreOwnedAndReplayable(t *testing.T) {
	source := []byte(`
type Boundary is interface
  action out Seen(stage : Integer; value : Integer);
  action out After();
  action out Wrong();
end interface Boundary;

architecture Child() return Boundary is
initial
  if True then Seen(1, 10); else Wrong(); end if;
  case 2 of
    1 => Wrong();
    xor 2 => Seen(2, 20);
    default => Wrong();
  end case;
  while False do Wrong(); end while;
  Once: loop do
    Seen(3, 30);
    exit Once;
    Wrong();
  end do Once;
  for I : Integer in 1 .. 2 do Seen(4, I); end for;
  assert False;
  After();
end architecture Child;

architecture Root() return Boundary is
  child : Boundary is Child();
initial
  for I in 5 .. 6 do Seen(5, I); end for;
  After();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 80)
	previous := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	childSeen := sourceNamedEvents(result.Poset, "child", "Seen")
	rootSeen := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "Seen")
	childAfter := sourceNamedEvents(result.Poset, "child", "After")
	rootAfter := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "After")
	childInconsistent := sourceNamedEvents(result.Poset, "child", "Inconsistent")
	if len(childSeen) != 5 || len(rootSeen) != 2 || len(childAfter) != 1 ||
		len(rootAfter) != 1 || len(childInconsistent) != 1 || len(result.Poset.ByName("Wrong")) != 0 {
		t.Fatalf("child/root Seen/After/Inconsistent/Wrong=%d/%d/%d/%d/%d/%d",
			len(childSeen), len(rootSeen), len(childAfter), len(rootAfter),
			len(childInconsistent), len(result.Poset.ByName("Wrong")))
	}
	wantChild := map[[2]int]bool{
		{1, 10}: true, {2, 20}: true, {3, 30}: true, {4, 1}: true, {4, 2}: true,
	}
	for _, event := range childSeen {
		key := [2]int{event.ParamInt("stage"), event.ParamInt("value")}
		if !wantChild[key] {
			t.Fatalf("unexpected child Seen=%#v", event)
		}
		delete(wantChild, key)
	}
	if len(wantChild) != 0 {
		t.Fatalf("missing child Seen values=%#v", wantChild)
	}
	wantRoot := map[int]bool{5: true, 6: true}
	for _, event := range rootSeen {
		if event.ParamInt("stage") != 5 || !wantRoot[event.ParamInt("value")] {
			t.Fatalf("unexpected root Seen=%#v", event)
		}
		delete(wantRoot, event.ParamInt("value"))
	}
	if len(wantRoot) != 0 {
		t.Fatalf("missing root Seen values=%#v", wantRoot)
	}

	rootModule := lifecycleModuleByOccurrence(t, result, "architecture:root")
	childModule := lifecycleModuleByOccurrence(t, result, "architecture-instance:child")
	iteratorsByParent := make(map[string][]arch.ModuleLifecycleRecord)
	for _, candidate := range result.Modules {
		if candidate.Kind == "predefined-range-iterator" {
			iteratorsByParent[candidate.Parent] = append(iteratorsByParent[candidate.Parent], candidate)
		}
	}
	if len(iteratorsByParent[rootModule.ModuleID]) != 1 || len(iteratorsByParent[childModule.ModuleID]) != 1 {
		t.Fatalf("architecture iterator ownership=%#v root=%s child=%s",
			iteratorsByParent, rootModule.ModuleID, childModule.ModuleID)
	}
	for _, pair := range []struct {
		iterator arch.ModuleLifecycleRecord
		after    *gorapide.Event
	}{
		{iteratorsByParent[rootModule.ModuleID][0], rootAfter[0]},
		{iteratorsByParent[childModule.ModuleID][0], childAfter[0]},
	} {
		finish, exists := result.Poset.Get(gorapide.EventID(pair.iterator.FinishEventID))
		if pair.iterator.State != arch.ModuleFinalizedState || !exists {
			t.Fatalf("architecture initial iterator lifecycle=%#v", pair.iterator)
		}
		if !result.Poset.IsCausallyIndependent(finish.ID, pair.after.ID) {
			t.Fatal("architecture initial iterator Finish incorrectly ordered its continuation")
		}
	}
	if !result.Poset.IsCausallyIndependent(childAfter[0].ID, rootAfter[0].ID) {
		t.Fatal("bottom-up architecture initial audit order created cross-owner causality")
	}

	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed architecture initial closed control")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("architecture initial closed control replay changed canonical bytes")
	}
}

func TestArchitectureInitialSelfInterruptsAreOwnedAndReplayable(t *testing.T) {
	source := []byte(`
type Boundary is interface
  action in Request();
  action out Signal(value : Integer);
  action out Pulse();
  action out Recovered(value : Integer);
  action out AnyRecovered();
  action out After();
  action out Wrong();
end interface Boundary;

architecture Root() return Boundary is
initial
  do
    Signal(3);
    Wrong();
  handler
    is Signal(value is ?Value) => Recovered(?Value);
  end do;
  do
    Pulse();
    Wrong();
  handler
    is any => AnyRecovered();
  end do;
  After();
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 30)
	previous := runtime.GOMAXPROCS(1)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := model.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	signal := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "Signal")
	pulse := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "Pulse")
	recovered := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "Recovered")
	anyRecovered := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "AnyRecovered")
	after := sourceNamedEvents(result.Poset, arch.ArchitectureInterfaceID, "After")
	if len(signal) != 1 || len(pulse) != 1 || len(recovered) != 1 ||
		len(anyRecovered) != 1 || len(after) != 1 || len(result.Poset.ByName("Wrong")) != 0 {
		t.Fatalf("Signal/Pulse/Recovered/AnyRecovered/After/Wrong=%d/%d/%d/%d/%d/%d",
			len(signal), len(pulse), len(recovered), len(anyRecovered), len(after),
			len(result.Poset.ByName("Wrong")))
	}
	if recovered[0].ParamInt("value") != 3 {
		t.Fatalf("architecture interrupt binding=%#v", recovered[0])
	}
	root := lifecycleModuleByOccurrence(t, result, "architecture:root")
	start, exists := result.Poset.Get(gorapide.EventID(root.StartEventID))
	if !exists {
		t.Fatalf("root lifecycle has no Start: %#v", root)
	}
	assertOnlyDirectCause(t, result.Poset, signal[0], start)
	assertOnlyDirectCause(t, result.Poset, recovered[0], signal[0])
	assertOnlyDirectCause(t, result.Poset, pulse[0], recovered[0])
	assertOnlyDirectCause(t, result.Poset, anyRecovered[0], pulse[0])
	assertOnlyDirectCause(t, result.Poset, after[0], anyRecovered[0])
	if len(result.ExceptionPropagations) != 0 {
		t.Fatalf("architecture action interrupt entered exception propagation: %#v", result.ExceptionPropagations)
	}

	artifact, _ := result.MarshalCanonical()
	repeatedArtifact, _ := repeated.MarshalCanonical()
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed architecture initial self-interrupt execution")
	}
	expected, _ := result.ArtifactDigest()
	replayed, err := model.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("architecture initial self-interrupt replay changed canonical bytes")
	}
}

func TestArchitectureLiteralInitialDeclarationBearingDoUsesOwnedException(t *testing.T) {
	source := []byte(`
type Boundary is interface
  action out Recovered(code : Integer);
  action out Continued();
end interface Boundary;

architecture Root() is
  literal : Boundary is architecture
  initial
    Stable: declare exception Failure(code : Integer); do
      raise ArchitectureLiteral::Stable::Failure(code is 4);
    handler
      is Stable::Failure(code is ?Code) => Recovered(?Code);
    end do Stable;
    Continued();
  end architecture;
end architecture Root;
`)
	model, err := Compile(source, "Root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20))
	if err != nil {
		t.Fatal(err)
	}
	failures := sourceNamedEvents(result.Poset, "literal", "Failure")
	recovered := sourceNamedEvents(result.Poset, "literal", "Recovered")
	continued := sourceNamedEvents(result.Poset, "literal", "Continued")
	if len(failures) != 1 || len(recovered) != 1 || len(continued) != 1 ||
		failures[0].ParamInt("code") != 4 || recovered[0].ParamInt("code") != 4 {
		t.Fatalf("literal failure/recovered/continued=%#v/%#v/%#v", failures, recovered, continued)
	}
	assertOnlyDirectCause(t, result.Poset, recovered[0], failures[0])
	assertOnlyDirectCause(t, result.Poset, continued[0], recovered[0])
}

func TestArchitectureInitialDeclarationBearingDoScopeDoesNotLeak(t *testing.T) {
	_, err := Compile([]byte(`
type Boundary is interface action out Done(); end interface Boundary;
architecture Root() return Boundary is
initial
  declare exception LocalOnly; do null; end do;
  raise LocalOnly;
  Done();
end architecture Root;
`), "Root")
	if err == nil || !strings.Contains(err.Error(), `undeclared exception "LocalOnly"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestArchitectureInitialDeclarationBearingDoIdentityIsCanonical(t *testing.T) {
	build := func(reverse bool) []byte {
		declarations := "exception LocalA; exception LocalB;"
		choices := "is LocalA => Seen(1); is LocalB => Seen(2);"
		if reverse {
			declarations = "exception LocalB; exception LocalA;"
			choices = "is LocalB => Seen(2); is LocalA => Seen(1);"
		}
		return []byte(`
type Boundary is interface action out Seen(value : Integer); end interface Boundary;
architecture Root() return Boundary is
initial
  Stable: declare ` + declarations + ` do
    raise Stable::LocalA;
  handler ` + choices + ` end do Stable;
end architecture Root;
`)
	}
	left, err := Compile(build(false), "Root")
	if err != nil {
		t.Fatal(err)
	}
	right, err := Compile(build(true), "Root")
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
		t.Fatalf("architecture initial declaration order changed model identity: %s != %s",
			leftDigest, rightDigest)
	}
}

func TestArchitectureInitialUnsupportedScopesFailExplicitly(t *testing.T) {
	tests := []struct {
		name, body, message string
	}{
		{name: "in action", body: "Request();", message: "cannot generate in-action"},
		{name: "private action", body: "Hidden();", message: "architecture initial cannot generate private action"},
		{name: "unconnected provided function", body: "Lookup();", message: "does not match an implemented local or connected function"},
		{name: "assignment", body: "value := 1;", message: "architecture initial assignments"},
		{name: "general for", body: "for 0 in True next 0 do Boot(); end for;", message: "architecture initial general for"},
		{name: "external in-action interrupt", body: "do Boot(); handler is Request => Boot(); end do;", message: "architecture initial external in-action interrupt choice"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Boundary is interface
  action in Request();
  action out Boot();
  private action Hidden();
  provides Lookup : function() return Integer;
end interface Boundary;
architecture Root() return Boundary is
connect
initial
` + test.body + `
end architecture Root;
`)
			_, err := Compile(source, "Root")
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v, want %q", err, test.message)
			}
		})
	}
}

func TestArchitectureInitialPreConnectExampleOrderFailsExplicitly(t *testing.T) {
	source := []byte(`
type Boundary is interface action out Boot(); end interface Boundary;
architecture Root() return Boundary is
initial
  Boot();
connect
end architecture Root;
`)
	_, err := Parse(source)
	if err == nil || !strings.Contains(err.Error(), "initial before connect contradicts") {
		t.Fatalf("pre-connect architecture initial error=%v", err)
	}
}
