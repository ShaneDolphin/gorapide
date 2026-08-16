package rapide

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

const architectureInterfaceSource = `
type API is interface
  action in Request(n : Integer);
  action out Response(n : Integer);
end interface API;

type Worker is interface
  action in Start(n : Integer);
  action out Done(n : Integer);
end interface Worker;

architecture Boundary() return API is
  worker : Worker;
connect
  (?N : Integer) Request(?N) to worker.Start(?N);
  (?N : Integer) worker.Done(?N) to Response(?N);
end architecture Boundary;
`

func TestParseArchitectureRetainsReturnInterfaceAndDefaultsToRoot(t *testing.T) {
	file, err := Parse([]byte(`
type API is interface action in Request(); end interface API;
architecture Explicit() return API is end architecture Explicit;
architecture Implicit() is end architecture Implicit;
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Architectures) != 2 {
		t.Fatalf("architectures=%d, want 2", len(file.Architectures))
	}
	if file.Architectures[0].ReturnType != "API" || file.Architectures[0].ReturnTypeExpression.Name != "API" {
		t.Fatalf("explicit return=%+v", file.Architectures[0])
	}
	if file.Architectures[1].ReturnType != "Root" || file.Architectures[1].ReturnTypeExpression.Name != "Root" {
		t.Fatalf("implicit return=%+v, want Root", file.Architectures[1])
	}
	implicit, err := Compile([]byte(`architecture Empty() is end architecture Empty;`), "Empty")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Compile([]byte(`architecture Empty() return Root is end architecture Empty;`), "Empty")
	if err != nil {
		t.Fatal(err)
	}
	implicitDigest, err := implicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if implicitDigest != explicitDigest {
		t.Fatalf("implicit and explicit Root differ: %s != %s", implicitDigest, explicitDigest)
	}
}

func TestArchitectureInterfaceActionsExecuteAtExplicitBoundaryIdentity(t *testing.T) {
	model, err := Compile([]byte(architectureInterfaceSource), "Boundary")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := model.Component(arch.ArchitectureInterfaceID); exists {
		t.Fatal("architecture return interface was inserted as a child component")
	}
	if got := model.ReturnInterface(); got == nil || got.Name != "API" {
		t.Fatalf("return interface=%v, want API", got)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{
			Key: "request", Source: arch.ArchitectureInterfaceID, Action: "Request",
			Params: map[string]any{"n": 7},
		},
		arch.InputEvent{
			Key: "done", Source: "worker", Action: "Done",
			Params: map[string]any{"n": 9}, Causes: []string{"request"},
		},
	)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	request := result.Poset.ByName("Request")
	start := sourceNamedEvents(result.Poset, "worker", "Start")
	done := result.Poset.ByName("Done")
	response := result.Poset.ByName("Response")
	if len(request) != 1 || len(start) != 1 || len(done) != 1 || len(response) != 1 {
		t.Fatalf("views request/start/done/response=%d/%d/%d/%d", len(request), len(start), len(done), len(response))
	}
	if request[0].Source != arch.ArchitectureInterfaceID || response[0].Source != arch.ArchitectureInterfaceID {
		t.Fatalf("boundary sources request=%q response=%q", request[0].Source, response[0].Source)
	}
	if request[0].ID != start[0].ID {
		t.Fatalf("basic input connection split occurrence identity: %s != %s", request[0].ID, start[0].ID)
	}
	if done[0].ID != response[0].ID {
		t.Fatalf("basic output connection split occurrence identity: %s != %s", done[0].ID, response[0].ID)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedDigest, err := replayed.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	if artifactDigest != replayedDigest {
		t.Fatalf("replay digest=%s, want %s", replayedDigest, artifactDigest)
	}
}

func TestArchitectureInterfacePipeAndAgentConnectionsPreserveCausality(t *testing.T) {
	source := strings.Replace(architectureInterfaceSource,
		"Request(?N) to worker.Start(?N)", "Request(?N) => worker.Start(?N)", 1)
	source = strings.Replace(source,
		"worker.Done(?N) to Response(?N)", "worker.Done(?N) ||> Response(?N)", 1)
	model, err := Compile([]byte(source), "Boundary")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 30,
		arch.InputEvent{Key: "request-one", Source: arch.ArchitectureInterfaceID, Action: "Request", Params: map[string]any{"n": 1}},
		arch.InputEvent{Key: "request-two", Source: arch.ArchitectureInterfaceID, Action: "Request", Params: map[string]any{"n": 2}},
		arch.InputEvent{Key: "done-one", Source: "worker", Action: "Done", Params: map[string]any{"n": 1}},
		arch.InputEvent{Key: "done-two", Source: "worker", Action: "Done", Params: map[string]any{"n": 2}},
	))
	if err != nil {
		t.Fatal(err)
	}
	starts := sourceNamedEvents(result.Poset, "worker", "Start")
	if len(starts) != 2 {
		t.Fatalf("pipe starts=%d, want 2", len(starts))
	}
	if !result.Poset.IsCausallyBefore(starts[0].ID, starts[1].ID) &&
		!result.Poset.IsCausallyBefore(starts[1].ID, starts[0].ID) {
		t.Fatal("architecture-input pipe outputs are not causally ordered")
	}
	responses := result.Poset.ByName("Response")
	if len(responses) != 2 {
		t.Fatalf("agent responses=%d, want 2", len(responses))
	}
	if responses[0].Source != arch.ArchitectureInterfaceID || responses[1].Source != arch.ArchitectureInterfaceID {
		t.Fatalf("agent response sources=%q/%q", responses[0].Source, responses[1].Source)
	}
	if !result.Poset.IsCausallyIndependent(responses[0].ID, responses[1].ID) {
		t.Fatal("architecture-output agent firings falsely ordered independent results")
	}
}

func sourceNamedEvents(poset *gorapide.Poset, source, name string) gorapide.EventSet {
	result := make(gorapide.EventSet, 0)
	for _, event := range poset.ByName(name) {
		if event.HasObservation(source, name) {
			result = append(result, event)
		}
	}
	return result
}

func eventsWithoutArchitectureStart(poset *gorapide.Poset) gorapide.EventSet {
	result := make(gorapide.EventSet, 0, poset.Len())
	for _, event := range poset.Events() {
		if event.Name == arch.ArchitectureStartAction &&
			(event.Source == arch.ArchitectureInterfaceID || strings.HasPrefix(event.Source, "$module/")) {
			continue
		}
		result = append(result, event)
	}
	return result
}

func TestCompoundConnectionCanObserveAndGenerateArchitectureInterfaceActions(t *testing.T) {
	source := strings.Replace(architectureInterfaceSource,
		"  (?N : Integer) Request(?N) to worker.Start(?N);\n  (?N : Integer) worker.Done(?N) to Response(?N);",
		"  (?N : Integer) (Request(?N) -> worker.Done(?N)) ||> Response(?N);", 1)
	model, err := Compile([]byte(source), "Boundary")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{
			Key: "request", Source: arch.ArchitectureInterfaceID, Action: "Request",
			Params: map[string]any{"n": 7},
		},
		arch.InputEvent{
			Key: "done", Source: "worker", Action: "Done", Params: map[string]any{"n": 7},
			Causes: []string{"request"},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	requests := result.Poset.ByName("Request")
	done := result.Poset.ByName("Done")
	responses := result.Poset.ByName("Response")
	if len(requests) != 1 || len(done) != 1 || len(responses) != 1 {
		t.Fatalf("compound boundary request/done/response=%d/%d/%d", len(requests), len(done), len(responses))
	}
	if responses[0].Source != arch.ArchitectureInterfaceID ||
		!result.Poset.IsCausallyBefore(requests[0].ID, responses[0].ID) ||
		!result.Poset.IsCausallyBefore(done[0].ID, responses[0].ID) {
		t.Fatalf("compound boundary result=%#v", responses[0])
	}
}

func TestArchitectureInterfaceServiceConnectionUsesStanfordSameTypeException(t *testing.T) {
	const source = `
type Protocol is interface
  action in Request(value : Integer);
  action out Response(value : Integer);
end interface Protocol;
type API is interface service Link : Protocol; end interface API;
type Worker is interface service Link : Protocol; end interface Worker;
architecture Boundary() return API is
  worker : Worker;
connect
  Link to worker.Link;
end architecture Boundary;
`
	model, err := Compile([]byte(source), "Boundary")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{
			Key: "request", Source: arch.ArchitectureInterfaceID, Action: "link.request",
			Params: map[string]any{"value": 3},
		},
		arch.InputEvent{
			Key: "response", Source: "worker", Action: "link.response",
			Params: map[string]any{"value": 5},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	requests := result.Poset.ByName("link.request")
	responses := result.Poset.ByName("link.response")
	if len(requests) != 2 || requests[0].ID != requests[1].ID ||
		!eventSetHasSources(requests, arch.ArchitectureInterfaceID, "worker") {
		t.Fatalf("same-type root service request=%#v", requests)
	}
	if len(responses) != 2 || responses[0].ID != responses[1].ID ||
		!eventSetHasSources(responses, arch.ArchitectureInterfaceID, "worker") {
		t.Fatalf("same-type root service response=%#v", responses)
	}

	dualWorker := strings.Replace(source,
		"type Worker is interface service Link : Protocol;",
		"type Worker is interface service Link : dual Protocol;", 1)
	_, err = Compile([]byte(dualWorker), "Boundary")
	if err == nil || !strings.Contains(err.Error(), "requires exact same service types") {
		t.Fatalf("root-to-dual service connection: got %v, want exact-same-type rejection", err)
	}
}

func TestArchitectureConstraintObservesUnqualifiedReturnInterfaceAction(t *testing.T) {
	source := strings.Replace(architectureInterfaceSource,
		"  worker : Worker;\nconnect",
		"  worker : Worker;\nconstraint\n  never Request;\nconnect", 1)
	model, err := Compile([]byte(source), "Boundary")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10,
		arch.InputEvent{
			Key: "request", Source: arch.ArchitectureInterfaceID, Action: "Request",
			Params: map[string]any{"n": 1},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.Constraints == nil || result.Constraints.Passed || len(result.Constraints.Reports) != 1 {
		t.Fatalf("architecture-interface constraint report=%#v", result.Constraints)
	}
}

func eventSetHasSources(events gorapide.EventSet, sources ...string) bool {
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		seen[event.Source] = true
	}
	for _, source := range sources {
		if !seen[source] {
			return false
		}
	}
	return true
}

func TestArchitectureInterfaceDirectionAndJournalPolarityAreEnforced(t *testing.T) {
	badConnections := []struct {
		name, rule, want string
	}{
		{name: "root out trigger", rule: "Response() to worker.Start();", want: "not an in action"},
		{name: "root in body", rule: "worker.Done() to Request();", want: "not an out action"},
		{name: "component in trigger", rule: "worker.Start() to Response();", want: "not an out action"},
		{name: "component out body", rule: "Request() to worker.Done();", want: "not an in action"},
	}
	for _, test := range badConnections {
		t.Run(test.name, func(t *testing.T) {
			source := strings.Replace(architectureInterfaceSource,
				"  (?N : Integer) Request(?N) to worker.Start(?N);\n  (?N : Integer) worker.Done(?N) to Response(?N);",
				"  "+test.rule, 1)
			_, err := Compile([]byte(source), "Boundary")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}

	model, err := Compile([]byte(architectureInterfaceSource), "Boundary")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	_, err = model.ExecuteDeterministic(arch.NewExecutionJournal(digest, 10, arch.InputEvent{
		Key: "invalid", Source: arch.ArchitectureInterfaceID, Action: "Response", Params: map[string]any{"n": 1},
	}))
	if !errors.Is(err, arch.ErrActionTypeMismatch) {
		t.Fatalf("injecting architecture out action: got %v, want ErrActionTypeMismatch", err)
	}
}

func TestArchitectureInterfaceCanonicalModelAndExecutionIgnoreDeclarationAndSchedulingOrder(t *testing.T) {
	reordered := strings.Replace(architectureInterfaceSource,
		"  action in Request(n : Integer);\n  action out Response(n : Integer);",
		"  action out Response(n : Integer);\n  action in Request(n : Integer);", 1)
	left, err := Compile([]byte(architectureInterfaceSource), "Boundary")
	if err != nil {
		t.Fatal(err)
	}
	right, err := Compile([]byte(reordered), "Boundary")
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
		t.Fatalf("return-interface declaration order changed model digest: %s != %s", leftDigest, rightDigest)
	}
	journal := arch.NewExecutionJournal(leftDigest, 20,
		arch.InputEvent{Key: "request", Source: arch.ArchitectureInterfaceID, Action: "Request", Params: map[string]any{"n": 1}},
		arch.InputEvent{Key: "done", Source: "worker", Action: "Done", Params: map[string]any{"n": 2}},
	)
	original := runtime.GOMAXPROCS(1)
	first, err := left.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(original)
	if err != nil {
		t.Fatal(err)
	}
	original = runtime.GOMAXPROCS(8)
	second, err := right.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(original)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatal("architecture-interface artifacts changed with declaration order or GOMAXPROCS")
	}
}
