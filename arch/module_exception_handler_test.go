package arch

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestModuleExceptionHandlerRequiresGeneratedMembership(t *testing.T) {
	architecture := NewArchitecture("module-handler-requires-membership")
	component := NewComponent("worker", Interface("Worker").Build(), nil)
	if err := component.SetModuleExceptionHandler(ExceptionHandler{
		Else: []Statement{NullStatement()},
	}); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}

	_, err := architecture.DeterministicModelDigest()
	if err == nil ||
		!errors.Is(err, ErrUnsupportedDeterministicModel) ||
		!errors.Is(err, ErrInvalidExceptionHandler) ||
		!strings.Contains(err.Error(), "module handler requires generated-module membership") {
		t.Fatalf("non-module handler error=%v", err)
	}
}

func TestModuleExceptionHandlerGeneratedIteratorFinalizesAndReplays(t *testing.T) {
	integerType, err := gorapide.RapidePredefinedType("Integer")
	if err != nil {
		t.Fatal(err)
	}
	generator, err := NewFiniteIteratorGenerator(
		"Recovery_Items", integerType, int64(4), int64(5),
	)
	if err != nil {
		t.Fatal(err)
	}
	architecture := NewArchitecture("module-handler-generated-iterator")
	if err := architecture.AddFiniteIteratorGenerator(generator); err != nil {
		t.Fatal(err)
	}
	component := NewComponent("worker", Interface("Worker").
		OutAction("Go").
		OutAction("Emit", P("value", "Integer")).
		OutAction("Recovered").Build(), nil)
	if err := component.SetModuleMembership("WorkerModule"); err != nil {
		t.Fatal(err)
	}
	if err := component.AddExceptionDeclaration(Exception("Failure")); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(Process("raiser").StartAt("wait").States(
		AwaitState("wait", Await("go").On(pattern.MatchEvent("Go")).Do(
			RaiseException("failure", "Failure"),
		).Terminate().Build()),
	).Build()); err != nil {
		t.Fatal(err)
	}
	if err := component.SetModuleExceptionHandler(ExceptionHandler{
		Choices: []ExceptionHandlerChoice{HandleException("Failure", nil,
			ForEachGeneratedIterator("I", generator,
				CallAction("emit", "Emit", BindingParam("value", "I"))),
			CallAction("recovered", "Recovered"),
		)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 50},
		InputEvent{Key: "go", Source: "worker", Action: "Go"},
	)
	previous := runtime.GOMAXPROCS(1)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	repeated, repeatedErr := architecture.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if repeatedErr != nil {
		t.Fatal(repeatedErr)
	}

	failures := result.Poset.ByName("Failure")
	emitted := result.Poset.ByName("Emit")
	recovered := result.Poset.ByName("Recovered")
	if len(failures) != 1 || len(emitted) != 2 || len(recovered) != 1 {
		t.Fatalf("Failure/Emit/Recovered=%d/%d/%d", len(failures), len(emitted), len(recovered))
	}
	emittedByValue := make(map[int]*gorapide.Event, len(emitted))
	for _, event := range emitted {
		emittedByValue[event.ParamInt("value")] = event
	}
	if emittedByValue[4] == nil || emittedByValue[5] == nil ||
		!result.Poset.IsCausallyBefore(emittedByValue[4].ID, emittedByValue[5].ID) {
		t.Fatalf("module-handler generated iteration=%#v", emitted)
	}
	if len(result.Iterators) != 1 || result.Iterators[0].Next != "2" || !result.Iterators[0].Exhausted {
		t.Fatalf("module-handler generated iterator state=%#v", result.Iterators)
	}
	iteratorModuleID := result.Iterators[0].Module
	iteratorFinalized := false
	for _, module := range result.Modules {
		if module.ModuleID == iteratorModuleID {
			if module.State != ModuleFinalizedState || module.FinishEventID == "" {
				t.Fatalf("module-handler iterator lifecycle=%#v", module)
			}
			iteratorFinalized = true
		}
		if module.Occurrence == "component:worker" && module.State != ModuleCompletedState {
			t.Fatalf("recovered worker lifecycle=%#v", module)
		}
	}
	if !iteratorFinalized || len(distinctIteratorEvents(result.Poset.ByName("More'Call"))) != 3 ||
		len(distinctIteratorEvents(result.Poset.ByName("Item'Call"))) != 2 {
		t.Fatalf("module-handler iterator protocol/finalization missing: iterators=%#v", result.Iterators)
	}

	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	repeatedArtifact, err := repeated.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, repeatedArtifact) {
		t.Fatal("GOMAXPROCS changed module-handler generated iterator")
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	replayedArtifact, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, replayedArtifact) {
		t.Fatal("module-handler generated iterator replay changed canonical bytes")
	}
}

func TestModuleExceptionHandlerRejectsPreallocatedIterator(t *testing.T) {
	integerType, err := gorapide.RapidePredefinedType("Integer")
	if err != nil {
		t.Fatal(err)
	}
	iterator := testFiniteIteratorModule(t, "module-handler-shared", integerType, int64(1))
	architecture := NewArchitecture("module-handler-preallocated-iterator-boundary")
	if err := architecture.AddFiniteIteratorModule(iterator); err != nil {
		t.Fatal(err)
	}
	component := NewComponent("worker", Interface("Worker").Build(), nil)
	if err := component.SetModuleMembership("WorkerModule"); err != nil {
		t.Fatal(err)
	}
	if err := component.AddExceptionDeclaration(Exception("Failure")); err != nil {
		t.Fatal(err)
	}
	if err := component.SetModuleExceptionHandler(ExceptionHandler{
		Choices: []ExceptionHandlerChoice{HandleException("Failure", nil,
			ForEachIterator("I", iterator, NullStatement()),
		)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err = architecture.DeterministicModelDigest()
	if err == nil || !errors.Is(err, ErrInvalidExceptionHandler) ||
		!strings.Contains(err.Error(), `module handler iterator kind "module" is outside the immediate recovery subset`) {
		t.Fatalf("preallocated module-handler iterator boundary=%v", err)
	}
}
