package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func TestRecursiveArchitectureFunctionBoundaryAliasesExecuteAndReplay(t *testing.T) {
	source := []byte(`
type Driver is interface action out Send(n : Integer); end interface Driver;
type Sink is interface action in Receive(n : Integer); end interface Sink;
type Client is interface
  action in Begin(n : Integer);
  action out Done(n : Integer);
  requires Compute : function(value : Integer) return Integer;
end interface Client;
type Server is interface
  provides Fetch : function(operand : Integer) return Integer;
end interface Server;
type Boundary is interface
  provides Down : function(value : Integer) return Integer;
  requires Up : function(value : Integer) return Integer;
end interface Boundary;
type Worker is interface
  provides Internal : function(operand : Integer) return Integer;
  requires External : function(value : Integer) return Integer;
end interface Worker;

module ClientModule() return Client is
  result : var Integer := 0;
parallel
  when (?N : Integer) Begin(?N) do
    result := Compute(?N);
    Done($result);
  end when;
end module ClientModule;

module ServerModule() return Server is
  Fetch : function(operand : Integer) return Integer is
    begin
      return operand * 2;
    end function Fetch;
end module ServerModule;

module WorkerModule() return Worker is
  temporary : var Integer := 0;
  Internal : function(operand : Integer) return Integer is
    begin
      temporary := External(operand);
      return $temporary + 1;
    end function Internal;
end module WorkerModule;

architecture Grand() return Boundary is
  worker : Worker is WorkerModule();
connect
  Down to worker.Internal;
  worker.External to Up;
end architecture Grand;

architecture Child() return Boundary is
  grand : Boundary is Grand();
connect
  Down to grand.Down;
  grand.Up to Up;
end architecture Child;

architecture System() is
  driver : Driver;
  client : Client is ClientModule();
  child : Boundary is Child();
  server : Server is ServerModule();
  sink : Sink;
connect
  (?N : Integer) driver.Send(?N) to client.Begin(?N);
  client.Compute to child.Down;
  child.Up to server.Fetch;
  (?N : Integer) client.Done(?N) to sink.Receive(?N);
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
	journal := arch.NewExecutionJournal(digest, 100, arch.InputEvent{
		Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 5},
	})
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	grandID := arch.DeterministicArchitectureInstanceID("child", "grand")
	workerID := arch.DeterministicArchitectureComponentID(grandID, "worker")

	outerCalls := [][]*gorapide.Event{
		functionBoundaryEvents(result.Poset, "client", "Compute'Call"),
		functionBoundaryEvents(result.Poset, "child", "Down'Call"),
		functionBoundaryEvents(result.Poset, grandID, "Down'Call"),
		functionBoundaryEvents(result.Poset, workerID, "Internal'Call"),
	}
	assertOneSharedAliasOccurrence(t, "outer call", outerCalls)
	innerCalls := [][]*gorapide.Event{
		functionBoundaryEvents(result.Poset, workerID, "External'Call"),
		functionBoundaryEvents(result.Poset, grandID, "Up'Call"),
		functionBoundaryEvents(result.Poset, "child", "Up'Call"),
		functionBoundaryEvents(result.Poset, "server", "Fetch'Call"),
	}
	assertOneSharedAliasOccurrence(t, "inner call", innerCalls)
	outerReturns := [][]*gorapide.Event{
		functionBoundaryEvents(result.Poset, "client", "Compute'Return"),
		functionBoundaryEvents(result.Poset, "child", "Down'Return"),
		functionBoundaryEvents(result.Poset, grandID, "Down'Return"),
		functionBoundaryEvents(result.Poset, workerID, "Internal'Return"),
	}
	assertOneSharedAliasOccurrence(t, "outer return", outerReturns)
	innerReturns := [][]*gorapide.Event{
		functionBoundaryEvents(result.Poset, workerID, "External'Return"),
		functionBoundaryEvents(result.Poset, grandID, "Up'Return"),
		functionBoundaryEvents(result.Poset, "child", "Up'Return"),
		functionBoundaryEvents(result.Poset, "server", "Fetch'Return"),
	}
	assertOneSharedAliasOccurrence(t, "inner return", innerReturns)

	done := sourceNamedEvents(result.Poset, "client", "Done")
	if len(done) != 1 || done[0].ParamInt("n") != 11 || !done[0].HasObservation("sink", "Receive") {
		t.Fatalf("boundary function result=%#v", done)
	}
	outerCallID := functionBoundaryEvents(result.Poset, "client", "Compute'Call")[0].ID
	innerCallID := functionBoundaryEvents(result.Poset, workerID, "External'Call")[0].ID
	innerReturnID := functionBoundaryEvents(result.Poset, "server", "Fetch'Return")[0].ID
	outerReturnID := functionBoundaryEvents(result.Poset, workerID, "Internal'Return")[0].ID
	if !result.Poset.IsCausallyBefore(outerCallID, innerCallID) ||
		!result.Poset.IsCausallyBefore(innerCallID, innerReturnID) ||
		!result.Poset.IsCausallyBefore(innerReturnID, outerReturnID) ||
		!result.Poset.IsCausallyBefore(outerReturnID, done[0].ID) {
		t.Fatal("boundary function alias path lost synchronous causality")
	}

	canonical, err := result.MarshalCanonical()
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
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(canonical, replayedBytes) {
		t.Fatal("boundary function replay changed canonical bytes")
	}
	scheduledJournal := journal
	for _, resolution := range result.Choices {
		scheduledJournal.Choices = append(scheduledJournal.Choices, resolution.Decision())
	}
	explored, err := model.ExploreDeterministic(scheduledJournal, arch.ExplorationLimits{
		MaxExecutions: 64, MaxChoiceDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	exploredAgain, err := model.ExploreDeterministic(scheduledJournal, arch.ExplorationLimits{
		MaxExecutions: 64, MaxChoiceDepth: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	exploredBytes, _ := explored.MarshalCanonical()
	exploredAgainBytes, _ := exploredAgain.MarshalCanonical()
	if len(explored.Computations) != 1 || !bytes.Equal(exploredBytes, exploredAgainBytes) {
		t.Fatalf("boundary function exploration=%#v", explored)
	}

	reversedSource := append([]byte(nil), source...)
	for _, replacement := range []struct{ old, new string }{
		{"Down to worker.Internal;\n  worker.External to Up;", "worker.External to Up;\n  Down to worker.Internal;"},
		{"Down to grand.Down;\n  grand.Up to Up;", "grand.Up to Up;\n  Down to grand.Down;"},
		{"client.Compute to child.Down;\n  child.Up to server.Fetch;", "child.Up to server.Fetch;\n  client.Compute to child.Down;"},
	} {
		updated := bytes.ReplaceAll(reversedSource, []byte(replacement.old), []byte(replacement.new))
		if bytes.Equal(updated, reversedSource) {
			t.Fatalf("test did not reorder function connections containing %q", replacement.old)
		}
		reversedSource = updated
	}
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for iteration, candidate := range [][]byte{source, reversedSource, source} {
			repeatedModel, err := Compile(candidate, "System")
			if err != nil {
				t.Fatal(err)
			}
			repeatedDigest, err := repeatedModel.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			if repeatedDigest != digest {
				t.Fatalf("boundary function model changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
			repeatedResult, err := repeatedModel.ExecuteDeterministic(arch.NewExecutionJournal(
				repeatedDigest, 100, arch.InputEvent{
					Key: "request", Source: "driver", Action: "Send", Params: map[string]any{"n": 5},
				},
			))
			if err != nil {
				t.Fatal(err)
			}
			repeatedBytes, _ := repeatedResult.MarshalCanonical()
			if !bytes.Equal(canonical, repeatedBytes) {
				t.Fatalf("boundary function artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

func TestArchitectureFunctionBoundaryDiagnosticsAreExplicit(t *testing.T) {
	tests := []struct {
		name, boundary, worker, connection, want string
	}{
		{
			name:       "boundary source must provide",
			boundary:   "requires Imported : function(value : Integer) return Integer;",
			worker:     "provides Offer : function(value : Integer) return Integer;",
			connection: "Imported to worker.Offer;",
			want:       "not a provides function",
		},
		{
			name:       "boundary target must require",
			boundary:   "provides Exported : function(value : Integer) return Integer;",
			worker:     "requires Need : function(value : Integer) return Integer;",
			connection: "worker.Need to Exported;",
			want:       "not a requires function",
		},
		{
			name:       "boundary signatures must agree",
			boundary:   "provides Exported : function(value : Integer) return Integer;",
			worker:     "provides Offer : function(value : Integer) return Boolean;",
			connection: "Exported to worker.Offer;",
			want:       "0 type-compatible provided signatures",
		},
		{
			name:       "boundary calls are aliases not patterns",
			boundary:   "provides Exported : function(value : Integer) return Integer;",
			worker:     "provides Offer : function(value : Integer) return Integer;",
			connection: "(?N : Integer) Exported(?N) to worker.Offer(?N);",
			want:       "do not accept pattern placeholders",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`
type Boundary is interface ` + test.boundary + ` end interface Boundary;
type Worker is interface ` + test.worker + ` end interface Worker;
architecture System() return Boundary is
  worker : Worker;
connect
  ` + test.connection + `
end architecture System;
`)
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("architecture function boundary diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

func functionBoundaryEvents(poset *gorapide.Poset, source, name string) gorapide.EventSet {
	result := make(gorapide.EventSet, 0)
	for _, event := range poset.Events() {
		if event.HasObservation(source, name) {
			result = append(result, event)
		}
	}
	return result
}

func assertOneSharedAliasOccurrence(t *testing.T, context string, groups [][]*gorapide.Event) {
	t.Helper()
	var identity gorapide.EventID
	for _, group := range groups {
		if len(group) != 1 {
			t.Fatalf("%s aliases=%#v", context, groups)
		}
		if identity == "" {
			identity = group[0].ID
		} else if group[0].ID != identity {
			t.Fatalf("%s aliases do not share one occurrence: %#v", context, groups)
		}
	}
}
