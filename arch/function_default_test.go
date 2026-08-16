package arch

import (
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestKernelClosesFunctionDefaultsBeforeCall(t *testing.T) {
	architecture := NewArchitecture("function-default")
	component := NewComponent("calculator", Interface("Calculator").
		OutAction("Start", P("n", "Integer")).
		OutAction("Result", P("n", "Integer")).
		ProvidesFunction("Add", "Integer", P("n", "Integer"), PDefault("delta", "Integer", 2)).
		Build(), nil)
	if err := component.AddFunctionImplementation(Function("Add", "Integer", P("n", "Integer"), P("delta", "Integer")).
		Returns(AddValues(BoundValue("n"), BoundValue("delta"))).Build()); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("result", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	number := pattern.Var("N").WithType("Integer")
	if err := component.AddDeclarativeRule(Rule("run").
		On(pattern.MatchEvent("Start").BindParam("n", number)).
		Do(
			CallFunctionInto("add", "result", "Add", BindingParam("n", "N")),
			CallAction("result", "Result", StateParam("n", "result")),
		).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20, InputEvent{
		Key: "start", Source: "calculator", Action: "Start", Params: map[string]any{"n": 5},
	}))
	if err != nil {
		t.Fatal(err)
	}
	call := result.Poset.ByName("Add'Call")
	output := result.Poset.ByName("Result")
	if len(call) != 1 || call[0].ParamInt("delta") != 2 || len(output) != 1 || output[0].ParamInt("n") != 7 {
		t.Fatalf("kernel default call/output=%#v/%#v", call, output)
	}
}

func TestFunctionDefaultValidationAndIdentity(t *testing.T) {
	digest := func(value any) string {
		t.Helper()
		architecture := NewArchitecture("function-default-identity")
		component := NewComponent("c", Interface("I").
			ProvidesFunction("F", "Integer", PDefault("n", "Integer", value)).Build(), nil)
		if err := component.AddFunctionImplementation(Function("F", "Integer", P("n", "Integer")).
			Returns(BoundValue("n")).Build()); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		result, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if digest(1) == digest(2) {
		t.Fatal("function default denotation was erased from model identity")
	}

	for _, declaration := range []FunctionDecl{
		{Name: "F", Kind: ProvidesFunction, Params: []ParamDecl{PDefault("n", "Integer", true)}},
	} {
		architecture := NewArchitecture("invalid-function-default")
		if err := architecture.AddComponent(NewComponent("c", &InterfaceDecl{Name: "I", Functions: []FunctionDecl{declaration}}, nil)); err != nil {
			t.Fatal(err)
		}
		if _, err := architecture.DeterministicModelDigest(); err == nil || !strings.Contains(err.Error(), "default does not match Integer") {
			t.Fatalf("invalid default diagnostic=%v", err)
		}
	}

	architecture := NewArchitecture("invalid-action-default")
	if err := architecture.AddComponent(NewComponent("c", &InterfaceDecl{Name: "I", Actions: []ActionDecl{{
		Name: "A", Kind: OutAction, Params: []ParamDecl{PDefault("n", "Integer", 1)},
	}}}, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := architecture.DeterministicModelDigest(); err == nil || !strings.Contains(err.Error(), "cannot have a function default") {
		t.Fatalf("action default diagnostic=%v", err)
	}
}

func TestFunctionDefaultsEnforceConstrainedPredefinedMembership(t *testing.T) {
	valid := NewArchitecture("constrained-function-default")
	component := NewComponent("c", Interface("I").
		ProvidesFunction("F", "Integer", PDefault("n", "Positive", 2)).Build(), nil)
	if err := component.AddFunctionImplementation(Function("F", "Integer", P("n", "Positive")).
		Returns(BoundValue("n")).Build()); err != nil {
		t.Fatal(err)
	}
	if err := valid.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if _, err := valid.DeterministicModelDigest(); err != nil {
		t.Fatalf("valid Positive default failed: %v", err)
	}

	for _, test := range []struct {
		typeName string
		value    any
	}{
		{typeName: "Positive", value: 0},
		{typeName: "Natural", value: -1},
	} {
		invalid := NewArchitecture("invalid-constrained-function-default")
		declaration := FunctionDecl{
			Name: "F", Kind: ProvidesFunction,
			Params: []ParamDecl{PDefault("n", test.typeName, test.value)},
		}
		if err := invalid.AddComponent(NewComponent("c", &InterfaceDecl{Name: "I", Functions: []FunctionDecl{declaration}}, nil)); err != nil {
			t.Fatal(err)
		}
		if _, err := invalid.DeterministicModelDigest(); err == nil ||
			!strings.Contains(err.Error(), "default does not match "+test.typeName) {
			t.Fatalf("invalid %s default diagnostic=%v", test.typeName, err)
		}
	}
}
