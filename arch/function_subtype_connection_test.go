package arch

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestFunctionConnectionMaterializesProviderExtraDefault(t *testing.T) {
	architecture := NewArchitecture("provider-extra-default")
	client := NewComponent("client", Interface("Client").
		OutAction("Start", P("n", "Integer")).
		OutAction("Done", P("n", "Integer")).
		RequiresFunction("Add", "Integer", P("n", "Integer")).Build(), nil)
	provider := NewComponent("provider", Interface("Provider").
		ProvidesFunction("Sum", "Integer", P("value", "Integer"), PDefault("bonus", "Integer", 2)).Build(), nil)
	if err := client.DeclareState(StateReference("result", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	if err := provider.AddFunctionImplementation(Function("Sum", "Integer", P("value", "Integer"), P("bonus", "Integer")).
		Returns(AddValues(BoundValue("value"), BoundValue("bonus"))).Build()); err != nil {
		t.Fatal(err)
	}
	number := pattern.Var("N").WithType("Integer")
	if err := client.AddDeclarativeRule(Rule("run").
		On(pattern.MatchEvent("Start").BindParam("n", number)).
		Do(
			CallFunctionInto("add", "result", "Add", BindingParam("n", "N")),
			CallAction("done", "Done", StateParam("n", "result")),
		).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(client); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(provider); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddFunctionConnection(
		ConnectFunction("client", "Add", "provider", "Sum").IdentifiedBy("sum").Build(),
	); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 20, InputEvent{
		Key: "start", Source: "client", Action: "Start", Params: map[string]any{"n": 3},
	})
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	requiredCall := onlyNamedEvent(t, result.Poset, "Add'Call")
	providedCall := onlyNamedEvent(t, result.Poset, "Sum'Call")
	requiredReturn := onlyNamedEvent(t, result.Poset, "Add'Return")
	providedReturn := onlyNamedEvent(t, result.Poset, "Sum'Return")
	if requiredCall.ID != providedCall.ID || requiredReturn.ID != providedReturn.ID {
		t.Fatal("arity adaptation duplicated the qualified function occurrence")
	}
	if _, exists := requiredCall.Param("bonus"); exists || requiredCall.ParamInt("n") != 3 {
		t.Fatalf("required call view=%#v", requiredCall.Params)
	}
	if providedCall.ParamInt("value") != 3 || providedCall.ParamInt("bonus") != 2 {
		t.Fatalf("provided call view=%#v", providedCall.Params)
	}
	if _, exists := requiredReturn.Param("bonus"); exists || requiredReturn.ParamInt("Return") != 5 {
		t.Fatalf("required return view=%#v", requiredReturn.Params)
	}
	if providedReturn.ParamInt("bonus") != 2 || providedReturn.ParamInt("Return") != 5 {
		t.Fatalf("provided return view=%#v", providedReturn.Params)
	}
	done := onlyNamedEvent(t, result.Poset, "Done")
	if done.ParamInt("n") != 5 || !result.Poset.IsCausallyBefore(requiredReturn.ID, done.ID) {
		t.Fatalf("adapted result/call causality=%#v", done)
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("default-adapting function route did not replay byte-identically")
	}
}

func TestFunctionConnectionDropsRequirementOnlyExtraActuals(t *testing.T) {
	architecture := NewArchitecture("requirement-extra")
	client := NewComponent("client", Interface("Client").
		OutAction("Start").
		RequiresFunction("Request", "Integer", P("n", "Integer"), P("audit", "Integer")).Build(), nil)
	provider := NewComponent("provider", Interface("Provider").
		ProvidesFunction("Serve", "Integer", P("value", "Integer")).Build(), nil)
	if err := provider.AddFunctionImplementation(Function("Serve", "Integer", P("value", "Integer")).
		Returns(BoundValue("value")).Build()); err != nil {
		t.Fatal(err)
	}
	if err := client.AddDeclarativeRule(Rule("run").On(pattern.MatchEvent("Start")).
		Do(CallFunction("request", "Request", LiteralParam("n", 7), LiteralParam("audit", 99))).Build()); err != nil {
		t.Fatal(err)
	}
	for _, component := range []*Component{client, provider} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	if err := architecture.AddFunctionConnection(
		ConnectFunction("client", "Request", "provider", "Serve").IdentifiedBy("serve").Build(),
	); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20, InputEvent{
		Key: "start", Source: "client", Action: "Start",
	}))
	if err != nil {
		t.Fatal(err)
	}
	required := onlyNamedEvent(t, result.Poset, "Request'Call")
	provided := onlyNamedEvent(t, result.Poset, "Serve'Call")
	if required.ID != provided.ID || required.ParamInt("audit") != 99 || provided.ParamInt("value") != 7 {
		t.Fatalf("requirement-extra call views=%#v/%#v", required.Params, provided.Params)
	}
	if _, exists := provided.Param("audit"); exists {
		t.Fatalf("requirement-only formal leaked into provider view: %#v", provided.Params)
	}
}

func TestFunctionConnectionExtraProviderFormalRequiresDefault(t *testing.T) {
	architecture := NewArchitecture("invalid-provider-extra")
	client := NewComponent("client", Interface("Client").
		RequiresFunction("Need", "Integer", P("n", "Integer")).Build(), nil)
	provider := NewComponent("provider", Interface("Provider").
		ProvidesFunction("Give", "Integer", P("n", "Integer"), P("missing", "Integer")).Build(), nil)
	for _, component := range []*Component{client, provider} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	if err := architecture.AddFunctionConnection(
		ConnectFunction("client", "Need", "provider", "Give").IdentifiedBy("bad").Build(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := architecture.DeterministicModelDigest(); err == nil ||
		!strings.Contains(err.Error(), "0 compatible provided signatures") {
		t.Fatalf("nondefaulted provider-extra diagnostic=%v", err)
	}
}

func TestFunctionConnectionUsesPredefinedParameterAndResultVariance(t *testing.T) {
	architecture := NewArchitecture("function-predefined-variance")
	client := NewComponent("client", Interface("Client").
		OutAction("Start", P("n", "Positive")).
		OutAction("Done", P("n", "Integer")).
		RequiresFunction("Convert", "Integer", P("n", "Positive")).Build(), nil)
	provider := NewComponent("provider", Interface("Provider").
		ProvidesFunction("Widen", "Positive", P("value", "Integer")).Build(), nil)
	if err := client.DeclareState(StateReference("result", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	if err := provider.AddFunctionImplementation(Function("Widen", "Positive", P("value", "Integer")).
		Returns(LiteralValue(7)).Build()); err != nil {
		t.Fatal(err)
	}
	number := pattern.Var("N").WithType("Positive")
	if err := client.AddDeclarativeRule(Rule("run").
		On(pattern.MatchEvent("Start").BindParam("n", number)).
		Do(
			CallFunctionInto("convert", "result", "Convert", BindingParam("n", "N")),
			CallAction("done", "Done", StateParam("n", "result")),
		).Build()); err != nil {
		t.Fatal(err)
	}
	for _, component := range []*Component{client, provider} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	if err := architecture.AddFunctionConnection(
		ConnectFunction("client", "Convert", "provider", "Widen").IdentifiedBy("variance").Build(),
	); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20, InputEvent{
		Key: "start", Source: "client", Action: "Start", Params: map[string]any{"n": 3},
	}))
	if err != nil {
		t.Fatal(err)
	}
	required := onlyNamedEvent(t, result.Poset, "Convert'Call")
	provided := onlyNamedEvent(t, result.Poset, "Widen'Call")
	if required.ID != provided.ID || required.ParamInt("n") != 3 || provided.ParamInt("value") != 3 {
		t.Fatalf("variant call views=%#v/%#v", required.Params, provided.Params)
	}
	if done := onlyNamedEvent(t, result.Poset, "Done"); done.ParamInt("n") != 7 {
		t.Fatalf("covariant result=%#v", done.Params)
	}
}

func TestFunctionConnectionRejectsReversedPredefinedVariance(t *testing.T) {
	tests := []struct {
		name     string
		required FunctionDecl
		provided FunctionDecl
	}{
		{
			name:     "parameter narrows at provider",
			required: FunctionDecl{Name: "Need", Kind: RequiresFunction, Params: []ParamDecl{P("n", "Integer")}, ReturnType: "Integer"},
			provided: FunctionDecl{Name: "Give", Kind: ProvidesFunction, Params: []ParamDecl{P("n", "Positive")}, ReturnType: "Integer"},
		},
		{
			name:     "result widens at provider",
			required: FunctionDecl{Name: "Need", Kind: RequiresFunction, Params: []ParamDecl{P("n", "Integer")}, ReturnType: "Positive"},
			provided: FunctionDecl{Name: "Give", Kind: ProvidesFunction, Params: []ParamDecl{P("n", "Integer")}, ReturnType: "Integer"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			architecture := NewArchitecture("invalid-function-variance")
			for _, component := range []*Component{
				NewComponent("client", &InterfaceDecl{Name: "Client", Functions: []FunctionDecl{test.required}}, nil),
				NewComponent("provider", &InterfaceDecl{Name: "Provider", Functions: []FunctionDecl{test.provided}}, nil),
			} {
				if err := architecture.AddComponent(component); err != nil {
					t.Fatal(err)
				}
			}
			if err := architecture.AddFunctionConnection(
				ConnectFunction("client", "Need", "provider", "Give").IdentifiedBy("bad").Build(),
			); err != nil {
				t.Fatal(err)
			}
			if _, err := architecture.DeterministicModelDigest(); err == nil ||
				!strings.Contains(err.Error(), "0 compatible provided signatures") {
				t.Fatalf("reversed variance diagnostic=%v", err)
			}
		})
	}
}
