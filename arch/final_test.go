package arch

import (
	"errors"
	"testing"
)

func TestModuleFinalDeclarationValidation(t *testing.T) {
	component := NewComponent("worker", Interface("Worker").OutAction("Close", P("n", "Integer")).Build(), nil)
	if err := component.SetFinalStatements(CallAction("close", "Close", LiteralParam("n", 1))); err != nil {
		t.Fatal(err)
	}
	if err := component.SetFinalStatements(CallAction("again", "Close", LiteralParam("n", 2))); err == nil || !errors.Is(err, ErrInvalidModuleFinal) {
		t.Fatalf("repeated final declaration error=%v", err)
	}
	architecture := NewArchitecture("final-requires-module")
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if err == nil || !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidModuleFinal) {
		t.Fatalf("non-module final error=%v", err)
	}
}

func TestModuleFinalRejectsOpenActual(t *testing.T) {
	component := NewComponent("worker", Interface("Worker").OutAction("Close", P("n", "Integer")).Build(), nil)
	if err := component.SetModuleMembership("WorkerModule"); err != nil {
		t.Fatal(err)
	}
	if err := component.SetFinalStatements(CallAction("close", "Close", StateParam("n", "state"))); err != nil {
		t.Fatal(err)
	}
	architecture := NewArchitecture("final-open-actual")
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if err == nil || !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidModuleFinal) {
		t.Fatalf("open final actual error=%v", err)
	}
}

func TestModuleFinalRejectsExternalInActionInterrupt(t *testing.T) {
	component := NewComponent("worker", Interface("Worker").
		InAction("Request").
		OutAction("Close").Build(), nil)
	if err := component.SetModuleMembership("WorkerModule"); err != nil {
		t.Fatal(err)
	}
	if err := component.SetFinalStatements(HandleDo(
		[]Statement{CallAction("close", "Close")},
		ExceptionHandler{Choices: []ExceptionHandlerChoice{
			HandleInterrupt("Request", nil, CallAction("handled", "Close")),
		}},
	)); err != nil {
		t.Fatal(err)
	}
	architecture := NewArchitecture("final-external-interrupt")
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if err == nil || !errors.Is(err, ErrUnsupportedDeterministicModel) ||
		!errors.Is(err, ErrInvalidModuleFinal) {
		t.Fatalf("external final interrupt error=%v", err)
	}
}

func TestModuleFinalClosedIfCaseAndAssertAreCanonicalModelData(t *testing.T) {
	build := func(condition bool) *Architecture {
		component := NewComponent("worker", Interface("Worker").
			OutAction("Selected", P("n", "Integer")).
			OutAction("Wrong").Build(), nil)
		if err := component.SetModuleMembership("WorkerModule"); err != nil {
			t.Fatal(err)
		}
		if err := component.SetFinalStatements(
			IfThen(LiteralValue(condition),
				[]Statement{CallAction("selected", "Selected", LiteralParam("n", 1))},
				[]Statement{CallAction("wrong", "Wrong")},
			),
			CaseOfDefault(LiteralValue(2), CaseXorMode,
				[]Statement{CallAction("default", "Wrong")},
				CaseWhen(LiteralValue(2), CallAction("case", "Selected", LiteralParam("n", 2))),
			),
			AssertThat(LiteralValue(false)),
			NameDo("cycle", LoopDo(
				CallAction("loop", "Selected", LiteralParam("n", 3)),
				ExitNamed("cycle"),
				CallAction("loop-wrong", "Wrong"),
			)),
			ForEachIntegerRange("I", LiteralValue(1), LiteralValue(2),
				CallAction("range", "Selected", BindingParam("n", "I")),
			),
		); err != nil {
			t.Fatal(err)
		}
		architecture := NewArchitecture("final-closed-control")
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		return architecture
	}
	trueDigest, err := build(true).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	falseDigest, err := build(false).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if trueDigest == falseDigest {
		t.Fatal("closed final if condition is absent from canonical model identity")
	}
}

func TestModuleFinalRejectsOpenRangeEndpoint(t *testing.T) {
	component := NewComponent("worker", Interface("Worker").OutAction("Close", P("n", "Integer")).Build(), nil)
	if err := component.SetModuleMembership("WorkerModule"); err != nil {
		t.Fatal(err)
	}
	if err := component.SetFinalStatements(ForEachIntegerRange(
		"I", ReadState("open"), LiteralValue(2),
		CallAction("close", "Close", BindingParam("n", "I")),
	)); err != nil {
		t.Fatal(err)
	}
	architecture := NewArchitecture("final-open-range")
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if err == nil || !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidModuleFinal) {
		t.Fatalf("open final range error=%v", err)
	}
}

func TestModuleFinalRejectsEscapingLoopControl(t *testing.T) {
	component := NewComponent("worker", Interface("Worker").OutAction("Close").Build(), nil)
	if err := component.SetModuleMembership("WorkerModule"); err != nil {
		t.Fatal(err)
	}
	if err := component.SetFinalStatements(ExitLoop()); err != nil {
		t.Fatal(err)
	}
	architecture := NewArchitecture("final-escaping-control")
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if err == nil || !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidModuleFinal) {
		t.Fatalf("escaping final control error=%v", err)
	}
}

func TestModuleFinalRejectsOpenControlCondition(t *testing.T) {
	conditions := []struct {
		name      string
		condition RuleValue
	}{
		{name: "state", condition: ReadState("open")},
		{name: "nonscalar", condition: LiteralValue([]any{int64(1)})},
	}
	for _, test := range conditions {
		t.Run(test.name, func(t *testing.T) {
			component := NewComponent("worker", Interface("Worker").OutAction("Close").Build(), nil)
			if err := component.SetModuleMembership("WorkerModule"); err != nil {
				t.Fatal(err)
			}
			if err := component.SetFinalStatements(IfThen(
				test.condition, []Statement{CallAction("close", "Close")}, nil,
			)); err != nil {
				t.Fatal(err)
			}
			architecture := NewArchitecture("final-open-control")
			if err := architecture.AddComponent(component); err != nil {
				t.Fatal(err)
			}
			_, err := architecture.DeterministicModelDigest()
			if err == nil || !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidModuleFinal) {
				t.Fatalf("open final control error=%v", err)
			}
		})
	}
}
