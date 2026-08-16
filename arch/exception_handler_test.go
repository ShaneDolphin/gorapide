package arch

import (
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestExactExceptionDeclarationsPermitHiddenSameSpellingAndRejectAmbiguousLegacyLookup(t *testing.T) {
	build := func(name string, exactHandler bool) *Architecture {
		architecture := NewArchitecture(name)
		component := NewComponent("worker", Interface("Worker").
			OutAction("Trigger").
			OutAction("InterfaceHandled").
			OutAction("ModuleHandled").
			Build(), nil)
		if err := component.AddExceptionDeclaration(DeclaredException(
			"rapide:interface:worker:provides:exception:failure", "Failure",
		)); err != nil {
			t.Fatal(err)
		}
		if err := component.AddExceptionDeclaration(DeclaredException(
			"rapide:module:worker:exception:failure", "Failure",
		)); err != nil {
			t.Fatal(err)
		}
		interfaceChoice := HandleException("Failure", nil, CallAction("interface-handled", "InterfaceHandled"))
		if exactHandler {
			interfaceChoice = HandleDeclaredException(
				"rapide:interface:worker:provides:exception:failure", "Failure", nil,
				CallAction("interface-handled", "InterfaceHandled"),
			)
		}
		protected := HandleExceptions([]Statement{
			RaiseDeclaredException(
				"raise-interface", "rapide:interface:worker:provides:exception:failure", "Failure",
			),
		}, ExceptionHandler{Choices: []ExceptionHandlerChoice{
			interfaceChoice,
			HandleDeclaredException(
				"rapide:module:worker:exception:failure", "Failure", nil,
				CallAction("module-handled", "ModuleHandled"),
			),
		}})
		if err := component.AddDeclarativeRule(
			Rule("handle").On(pattern.MatchEvent("Trigger")).Do(protected).Build(),
		); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		return architecture
	}

	exact := build("exact-hidden-exception", true)
	digest, err := exact.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := exact.ExecuteDeterministic(NewExecutionJournal(
		digest, 10, InputEvent{Key: "trigger", Source: "worker", Action: "Trigger"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.Poset.ByName("InterfaceHandled")); got != 1 {
		t.Fatalf("InterfaceHandled=%d, want 1", got)
	}
	if got := len(result.Poset.ByName("ModuleHandled")); got != 0 {
		t.Fatalf("ModuleHandled=%d, want 0", got)
	}

	ambiguous := build("ambiguous-hidden-exception", false)
	_, err = ambiguous.DeterministicModelDigest()
	if err == nil || !errors.Is(err, ErrInvalidExceptionHandler) ||
		!strings.Contains(err.Error(), "names missing exception declaration") {
		t.Fatalf("ambiguous legacy exception lookup error=%v", err)
	}
}

func TestUnnamedReraiseRequiresActiveHandler(t *testing.T) {
	architecture := NewArchitecture("reraise-requires-handler")
	component := NewComponent("worker", Interface("Worker").InAction("Input").Build(), nil)
	if err := component.AddDeclarativeRule(
		Rule("invalid-reraise").On(pattern.MatchEvent("Input")).Do(ReraiseException()).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if err == nil ||
		!errors.Is(err, ErrUnsupportedDeterministicModel) ||
		!errors.Is(err, ErrInvalidDeclarativeStatement) ||
		!strings.Contains(err.Error(), "requires an active handler") {
		t.Fatalf("unscoped unnamed re-raise error=%v", err)
	}
}

func TestModuleInterruptHandlerRequiresProceduralBlock(t *testing.T) {
	architecture := NewArchitecture("module-interrupt-handler-boundary")
	component := NewComponent("worker", Interface("Worker").InAction("Stop").Build(), nil)
	if err := component.SetModuleMembership("WorkerModule"); err != nil {
		t.Fatal(err)
	}
	if err := component.SetModuleExceptionHandler(ExceptionHandler{
		Choices: []ExceptionHandlerChoice{HandleInterrupt("Stop", nil, NullStatement())},
	}); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if err == nil || !errors.Is(err, ErrInvalidExceptionHandler) ||
		!strings.Contains(err.Error(), "requires an enclosing active procedural block") {
		t.Fatalf("module action-handler boundary error=%v", err)
	}
}

func TestNonProcessInterruptHandlerRejectsProtectedModuleAllocation(t *testing.T) {
	architecture := NewArchitecture("interrupt-handler-allocation-boundary")
	component := NewComponent("worker", Interface("Worker").
		InAction("Trigger").
		OutAction("Pulse").
		OutAction("Allocated", P("value", "Integer")).
		Build(), nil)
	protected := HandleExceptions(
		[]Statement{
			CallAction("allocate", "Allocated",
				ExpressionParam("value", ModuleNewValue("Integer"))),
		},
		ExceptionHandler{Choices: []ExceptionHandlerChoice{
			HandleInterrupt("Pulse", nil, NullStatement()),
		}},
	)
	if err := component.AddDeclarativeRule(
		Rule("allocate").On(pattern.MatchEvent("Trigger")).Do(protected).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if err == nil || !errors.Is(err, ErrInvalidExceptionHandler) ||
		!strings.Contains(err.Error(), "module allocation while a non-process interrupt handler is active requires owner-lifetime semantics") {
		t.Fatalf("protected module-allocation boundary error=%v", err)
	}
}

func TestAnyHandlerPatternRejectsCanonicalAmbiguity(t *testing.T) {
	processModel := func(name string, handler ExceptionHandler) *Architecture {
		architecture := NewArchitecture(name)
		component := NewComponent("worker", Interface("Worker").
			InAction("Trigger").OutAction("Pulse").Build(), nil)
		protected := HandleExceptions([]Statement{CallAction("pulse", "Pulse")}, handler)
		if err := component.AddDeclarativeRule(
			Rule("handle").On(pattern.MatchEvent("Trigger")).Do(protected).Build(),
		); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		return architecture
	}

	t.Run("overlapping choice", func(t *testing.T) {
		model := processModel("any-overlap", ExceptionHandler{Choices: []ExceptionHandlerChoice{
			HandleAnyEvent(NullStatement()),
			HandleInterrupt("Pulse", nil, NullStatement()),
		}})
		_, err := model.DeterministicModelDigest()
		if err == nil || !errors.Is(err, ErrInvalidExceptionHandler) ||
			!strings.Contains(err.Error(), "must be the handler's sole choice") {
			t.Fatalf("overlapping any-handler boundary=%v", err)
		}
	})

	t.Run("binding", func(t *testing.T) {
		choice := HandleAnyEvent(NullStatement())
		choice.Bindings = []ExceptionHandlerBinding{{Formal: "value", Placeholder: "V", Type: "Integer"}}
		model := processModel("any-binding", ExceptionHandler{Choices: []ExceptionHandlerChoice{choice}})
		_, err := model.DeterministicModelDigest()
		if err == nil || !errors.Is(err, ErrInvalidExceptionHandler) ||
			!strings.Contains(err.Error(), "cannot bind parameters") {
			t.Fatalf("parameterized any-handler boundary=%v", err)
		}
	})

	t.Run("module activation", func(t *testing.T) {
		architecture := NewArchitecture("module-any-handler-boundary")
		component := NewComponent("worker", Interface("Worker").InAction("Trigger").Build(), nil)
		if err := component.SetModuleMembership("WorkerModule"); err != nil {
			t.Fatal(err)
		}
		if err := component.SetModuleExceptionHandler(ExceptionHandler{
			Choices: []ExceptionHandlerChoice{HandleAnyEvent(NullStatement())},
		}); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		_, err := architecture.DeterministicModelDigest()
		if err == nil || !errors.Is(err, ErrInvalidExceptionHandler) ||
			!strings.Contains(err.Error(), "requires an enclosing active procedural block") {
			t.Fatalf("module any-handler boundary=%v", err)
		}
	})
}
