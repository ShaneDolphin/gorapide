package arch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func localFunctionArchitecture(t *testing.T) *Architecture {
	t.Helper()
	architecture := NewArchitecture("local-functions")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start", P("message", "String")).
		OutAction("Before", P("message", "String")).
		OutAction("Inside", P("message", "String")).
		OutAction("After", P("message", "String")).
		ProvidesFunction("Audit", "", P("message", "String")).
		Build(), nil)
	if err := component.AddFunctionImplementation(Function("Audit", "", P("message", "String")).
		Do(CallAction("inside", "Inside", BindingParam("message", "message"))).
		Build()); err != nil {
		t.Fatal(err)
	}
	message := pattern.Var("M").WithType("String")
	if err := component.AddDeclarativeRule(Rule("run").
		On(pattern.MatchEvent("Start").BindParam("message", message)).
		Do(
			CallAction("before", "Before", BindingParam("message", "M")),
			CallFunction("audit", "Audit", BindingParam("message", "M")),
			CallAction("after", "After", BindingParam("message", "M")),
		).
		Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestLocalFunctionCallGeneratesOrderedCallBodyReturnAndResumesCaller(t *testing.T) {
	architecture := localFunctionArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 20, InputEvent{
		Key: "start", Source: "worker", Action: "Start", Params: map[string]any{"message": "hello"},
	})
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"Start", "Before", "Audit'Call", "Inside", "Audit'Return", "After"}
	events := make([]*gorapide.Event, len(names))
	for index, name := range names {
		matches := result.Poset.ByName(name)
		if name == ArchitectureStartAction {
			filtered := make(gorapide.EventSet, 0, 1)
			for _, event := range matches {
				if event.Source == "worker" {
					filtered = append(filtered, event)
				}
			}
			matches = filtered
		}
		if len(matches) != 1 {
			t.Fatalf("%s occurrences=%d, want 1", name, len(matches))
		}
		events[index] = matches[0]
		if value, ok := matches[0].Param("message"); !ok || value != "hello" {
			t.Fatalf("%s message=%#v,%v", name, value, ok)
		}
	}
	for index := 0; index+1 < len(events); index++ {
		if !result.Poset.IsCausallyBefore(events[index].ID, events[index+1].ID) {
			t.Fatalf("%s is not causally before %s", names[index], names[index+1])
		}
	}
	if _, exists := events[4].Param("Return"); exists {
		t.Fatal("no-return function synthesized a Return parameter")
	}
	expectedDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, expectedDigest)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("local function replay changed canonical artifact bytes")
	}
}

func TestTypedFunctionReturnUsesPostBodyStateAndIsAudited(t *testing.T) {
	architecture := NewArchitecture("typed-function")
	component := NewComponent("calculator", Interface("Calculator").
		OutAction("Start", P("n", "Integer")).
		OutAction("Inside", P("n", "Integer")).
		ProvidesFunction("Increment", "Integer", P("n", "Integer")).
		Build(), nil)
	if err := component.DeclareState(StateReference("last", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	if err := component.AddFunctionImplementation(Function("Increment", "Integer", P("n", "Integer")).
		Do(
			SetState("last", BoundValue("n")),
			CallAction("inside", "Inside", StateParam("n", "last")),
		).
		Returns(AddValues(ReadState("last"), LiteralValue(1))).
		Build()); err != nil {
		t.Fatal(err)
	}
	number := pattern.Var("N").WithType("Integer")
	if err := component.AddDeclarativeRule(Rule("calculate").
		On(pattern.MatchEvent("Start").BindParam("n", number)).
		Do(CallFunction("increment", "Increment", BindingParam("n", "N"))).
		Build()); err != nil {
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
		Key: "start", Source: "calculator", Action: "Start", Params: map[string]any{"n": 3},
	}))
	if err != nil {
		t.Fatal(err)
	}
	call := result.Poset.ByName("Increment'Call")
	returned := result.Poset.ByName("Increment'Return")
	inside := result.Poset.ByName("Inside")
	if len(call) != 1 || len(returned) != 1 || len(inside) != 1 {
		t.Fatalf("call/body/return counts=%d/%d/%d", len(call), len(inside), len(returned))
	}
	if value, _ := returned[0].Param("Return"); value != int64(4) {
		t.Fatalf("return value=%#v, want int64(4)", value)
	}
	if value, _ := returned[0].Param("n"); value != int64(3) {
		t.Fatalf("return formal n=%#v, want int64(3)", value)
	}
	if !result.Poset.IsCausallyBefore(call[0].ID, inside[0].ID) ||
		!result.Poset.IsCausallyBefore(inside[0].ID, returned[0].ID) {
		t.Fatal("typed function call/body/return causality is incomplete")
	}
	if len(result.Firings) != 1 || len(result.Firings[0].StateWrites) != 1 || len(result.Firings[0].StateReads) < 2 {
		t.Fatalf("function state audit is incomplete: %#v", result.Firings)
	}
	augmented, err := result.AugmentedComputation()
	if err != nil {
		t.Fatal(err)
	}
	limits := ConsistentCutLimits{MaxCuts: 20, MaxOptionalOccurrences: 30}
	callCuts, err := augmented.ConsistentCutStateWitnesses([]string{string(call[0].ID)}, limits)
	if err != nil {
		t.Fatal(err)
	}
	returnCuts, err := augmented.ConsistentCutStateWitnesses([]string{string(returned[0].ID)}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if len(callCuts) != 1 || callCuts[0].State[0].Version != 0 ||
		len(returnCuts) != 1 || returnCuts[0].State[0].Version != 1 {
		t.Fatalf("function call/return cut states call=%#v return=%#v", callCuts, returnCuts)
	}
}

func TestFunctionReturnFlowsIntoStateAfterReturnEvent(t *testing.T) {
	architecture := NewArchitecture("function-result-state")
	component := NewComponent("calculator", Interface("Calculator").
		OutAction("Start", P("n", "Integer")).
		OutAction("Result", P("n", "Integer")).
		ProvidesFunction("Increment", "Integer", P("n", "Integer")).
		Build(), nil)
	if err := component.DeclareState(StateReference("result", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	if err := component.AddFunctionImplementation(Function("Increment", "Integer", P("n", "Integer")).
		Returns(AddValues(BoundValue("n"), LiteralValue(1))).Build()); err != nil {
		t.Fatal(err)
	}
	number := pattern.Var("N").WithType("Integer")
	if err := component.AddDeclarativeRule(Rule("calculate").
		On(pattern.MatchEvent("Start").BindParam("n", number)).
		Do(
			CallFunctionInto("increment", "result", "Increment", BindingParam("n", "N")),
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
	returned := result.Poset.ByName("Increment'Return")
	outputs := result.Poset.ByName("Result")
	if len(returned) != 1 || len(outputs) != 1 {
		t.Fatalf("return/result counts=%d/%d", len(returned), len(outputs))
	}
	if value, _ := outputs[0].Param("n"); value != int64(6) {
		t.Fatalf("downstream result=%#v, want int64(6)", value)
	}
	if !result.Poset.IsCausallyBefore(returned[0].ID, outputs[0].ID) {
		t.Fatal("caller consumed function result before the return occurrence")
	}
	if len(result.State) != 1 || result.State[0].Name != "result" || result.State[0].Value.Text != "6" {
		t.Fatalf("final function-result state=%#v", result.State)
	}
	if len(result.Firings) != 1 || len(result.Firings[0].StateWrites) != 1 ||
		len(result.Firings[0].StateWrites[0].Causes) != 1 ||
		result.Firings[0].StateWrites[0].Causes[0] != string(returned[0].ID) {
		t.Fatalf("function-result write audit=%#v", result.Firings)
	}
}

func TestFunctionResultContextResolvesReturnTypeOverload(t *testing.T) {
	architecture := NewArchitecture("function-result-overload")
	component := NewComponent("c", Interface("I").
		OutAction("Start").
		ProvidesFunction("Value", "Integer").
		ProvidesFunction("Value", "String").
		Build(), nil)
	if err := component.DeclareState(StateReference("number", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	if err := component.AddFunctionImplementation(Function("Value", "Integer").Returns(LiteralValue(7)).Build()); err != nil {
		t.Fatal(err)
	}
	if err := component.AddFunctionImplementation(Function("Value", "String").Returns(LiteralValue("seven")).Build()); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeRule(Rule("r").On(pattern.MatchEvent("Start")).
		Do(CallFunctionInto("value", "number", "Value")).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10, InputEvent{
		Key: "start", Source: "c", Action: "Start",
	}))
	if err != nil {
		t.Fatal(err)
	}
	returned := result.Poset.ByName("Value'Return")
	if len(returned) != 1 {
		t.Fatalf("return occurrences=%d, want 1", len(returned))
	}
	if value, _ := returned[0].Param("Return"); value != int64(7) {
		t.Fatalf("context-selected return=%#v, want int64(7)", value)
	}
}

func TestFunctionResultTargetValidation(t *testing.T) {
	tests := []struct {
		name       string
		returnType string
		returned   *RuleValue
		stateType  string
		target     string
		want       string
	}{
		{name: "undeclared target", returnType: "Integer", returned: ruleValuePointer(LiteralValue(1)), stateType: "Integer", target: "missing", want: "writes undeclared state"},
		{name: "no return object", returnType: "", stateType: "Integer", target: "result", want: "does not match an implemented"},
		{name: "incompatible return", returnType: "String", returned: ruleValuePointer(LiteralValue("x")), stateType: "Integer", target: "result", want: "does not match an implemented"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			architecture := NewArchitecture("invalid-result-target")
			component := NewComponent("c", Interface("I").OutAction("Start").
				ProvidesFunction("F", test.returnType).Build(), nil)
			if err := component.DeclareState(StateReference("result", test.stateType, zeroValueForTestType(test.stateType))); err != nil {
				t.Fatal(err)
			}
			builder := Function("F", test.returnType)
			if test.returned != nil {
				builder.Returns(*test.returned)
			}
			if err := component.AddFunctionImplementation(builder.Build()); err != nil {
				t.Fatal(err)
			}
			if err := component.AddDeclarativeRule(Rule("r").On(pattern.MatchEvent("Start")).
				Do(CallFunctionInto("f", test.target, "F")).Build()); err != nil {
				t.Fatal(err)
			}
			if err := architecture.AddComponent(component); err != nil {
				t.Fatal(err)
			}
			_, err := architecture.DeterministicModelDigest()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func ruleValuePointer(value RuleValue) *RuleValue { return &value }

func zeroValueForTestType(typeName string) any {
	if typeName == "String" {
		return ""
	}
	return 0
}

func TestLocalFunctionRecursionIsDeterministicAndStatementBounded(t *testing.T) {
	architecture := NewArchitecture("recursive-function")
	component := NewComponent("recursive", Interface("Recursive").
		OutAction("Start", P("n", "Integer")).
		ProvidesFunction("Descend", "", P("n", "Integer")).
		Build(), nil)
	recurse := IfThen(
		GreaterValues(BoundValue("n"), LiteralValue(0)),
		[]Statement{CallFunction("next", "Descend", ExpressionParam("n", SubtractValues(BoundValue("n"), LiteralValue(1))))},
		nil,
	)
	if err := component.AddFunctionImplementation(Function("Descend", "", P("n", "Integer")).Do(recurse).Build()); err != nil {
		t.Fatal(err)
	}
	number := pattern.Var("N").WithType("Integer")
	if err := component.AddDeclarativeRule(Rule("start").
		On(pattern.MatchEvent("Start").BindParam("n", number)).
		Do(CallFunction("descend", "Descend", BindingParam("n", "N"))).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournalWithLimits(digest, ExecutionLimits{MaxFirings: 20, MaxStatements: 20}, InputEvent{
		Key: "start", Source: "recursive", Action: "Start", Params: map[string]any{"n": 2},
	})
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	calls := result.Poset.ByName("Descend'Call")
	returns := result.Poset.ByName("Descend'Return")
	if len(calls) != 3 || len(returns) != 3 {
		t.Fatalf("recursive call/return counts=%d/%d, want 3/3", len(calls), len(returns))
	}
	callByN := make(map[int64]*gorapide.Event)
	returnByN := make(map[int64]*gorapide.Event)
	for _, event := range calls {
		value, _ := event.Param("n")
		callByN[value.(int64)] = event
	}
	for _, event := range returns {
		value, _ := event.Param("n")
		returnByN[value.(int64)] = event
	}
	chain := []*gorapide.Event{callByN[2], callByN[1], callByN[0], returnByN[0], returnByN[1], returnByN[2]}
	for index := 0; index+1 < len(chain); index++ {
		if !result.Poset.IsCausallyBefore(chain[index].ID, chain[index+1].ID) {
			t.Fatalf("recursive event %d is not before event %d", index, index+1)
		}
	}
	limited := journal
	limited.Limits.MaxStatements = 4
	if _, err := architecture.ExecuteDeterministic(limited); !errors.Is(err, ErrExecutionLimit) {
		t.Fatalf("got %v, want recursive statement limit", err)
	}
}

func TestReactionToFunctionCallRemainsIndependentOfFunctionReturn(t *testing.T) {
	architecture := NewArchitecture("function-call-observation")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").
		OutAction("ObservedCall").
		ProvidesFunction("Ping", "").
		Build(), nil)
	if err := component.AddFunctionImplementation(Function("Ping", "").Build()); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeRule(Rule("invoke").On(pattern.MatchEvent("Start")).
		Do(CallFunction("ping", "Ping")).Build()); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeRule(Rule("observe-call").On(pattern.MatchEvent("Ping'Call")).
		Emit("ObservedCall").Build()); err != nil {
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
		Key: "start", Source: "worker", Action: "Start",
	}))
	if err != nil {
		t.Fatal(err)
	}
	call := result.Poset.ByName("Ping'Call")
	returned := result.Poset.ByName("Ping'Return")
	observed := result.Poset.ByName("ObservedCall")
	if len(call) != 1 || len(returned) != 1 || len(observed) != 1 {
		t.Fatalf("call/return/reaction counts=%d/%d/%d", len(call), len(returned), len(observed))
	}
	if !result.Poset.IsCausallyBefore(call[0].ID, returned[0].ID) ||
		!result.Poset.IsCausallyBefore(call[0].ID, observed[0].ID) {
		t.Fatal("function call does not cause both return and call reaction")
	}
	if result.Poset.IsCausallyBefore(returned[0].ID, observed[0].ID) ||
		result.Poset.IsCausallyBefore(observed[0].ID, returned[0].ID) {
		t.Fatal("observation scheduling introduced false causality between return and call reaction")
	}
}

func TestFunctionImplementationAndCallValidation(t *testing.T) {
	tests := []struct {
		name      string
		iface     *InterfaceDecl
		function  *FunctionImplementation
		statement Statement
		want      string
	}{
		{
			name: "implementation not provided", iface: Interface("I").Build(),
			function: Function("F", "").Build(), want: "does not exactly match a provided",
		},
		{
			name: "required cannot be implemented", iface: Interface("I").RequiresFunction("F", "").Build(),
			function: Function("F", "").Build(), want: "does not exactly match a provided",
		},
		{
			name: "service function execution awaits qualification",
			iface: Interface("I").Service("S", func(service *ServiceBuilder) {
				service.ProvidesFunction("F", "")
			}).Build(),
			function: Function("F", "").Build(), want: "does not exactly match a provided",
		},
		{
			name: "typed function missing return", iface: Interface("I").ProvidesFunction("F", "Integer").Build(),
			function: Function("F", "Integer").Build(), want: "requires an explicit deterministic return",
		},
		{
			name: "no-return function has value", iface: Interface("I").ProvidesFunction("F", "").Build(),
			function: Function("F", "").Returns(LiteralValue(1)).Build(), want: "has no return type",
		},
		{
			name: "return type mismatch", iface: Interface("I").ProvidesFunction("F", "Boolean").Build(),
			function: Function("F", "Boolean").Returns(LiteralValue(1)).Build(), want: "does not match Boolean",
		},
		{
			name: "call has no implementation", iface: Interface("I").OutAction("Start").ProvidesFunction("F", "").Build(),
			statement: CallFunction("f", "F"), want: "does not match an implemented local or connected function",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			architecture := NewArchitecture("invalid-function")
			component := NewComponent("c", test.iface, nil)
			if test.function != nil {
				if err := component.AddFunctionImplementation(test.function); err != nil {
					t.Fatal(err)
				}
			}
			if test.statement.kind != "" {
				if err := component.AddDeclarativeRule(Rule("r").On(pattern.MatchEvent("Start")).Do(test.statement).Build()); err != nil {
					t.Fatal(err)
				}
			}
			if err := architecture.AddComponent(component); err != nil {
				t.Fatal(err)
			}
			_, err := architecture.DeterministicModelDigest()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestFunctionBodyAndNamedArgumentOrderAreCanonical(t *testing.T) {
	build := func(reverse bool, returned int) string {
		t.Helper()
		architecture := NewArchitecture("function-identity")
		component := NewComponent("c", Interface("I").
			OutAction("Start", P("a", "Integer"), P("b", "String")).
			ProvidesFunction("F", "Integer", P("a", "Integer"), P("b", "String")).
			Build(), nil)
		implementation := Function("F", "Integer", P("a", "Integer"), P("b", "String")).Returns(LiteralValue(returned)).Build()
		if err := component.AddFunctionImplementation(implementation); err != nil {
			t.Fatal(err)
		}
		arguments := []RuleParameter{LiteralParam("a", 1), LiteralParam("b", "x")}
		if reverse {
			arguments[0], arguments[1] = arguments[1], arguments[0]
		}
		if err := component.AddDeclarativeRule(Rule("r").On(pattern.MatchEvent("Start")).Do(CallFunction("f", "F", arguments...)).Build()); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	forward := build(false, 1)
	reverse := build(true, 1)
	if forward != reverse {
		t.Fatalf("named argument order changed model identity: %s != %s", forward, reverse)
	}
	if changed := build(false, 2); changed == forward {
		t.Fatal("function return expression did not change model identity")
	}
}

func TestFunctionOverloadAmbiguityAndDuplicateImplementationFail(t *testing.T) {
	t.Run("ambiguous assignable overload", func(t *testing.T) {
		architecture := NewArchitecture("ambiguous-overload")
		component := NewComponent("c", Interface("I").
			OutAction("Start", P("p", "Positive")).
			ProvidesFunction("F", "", P("p", "Integer")).
			ProvidesFunction("F", "", P("p", "Natural")).
			Build(), nil)
		if err := component.AddFunctionImplementation(Function("F", "", P("p", "Integer")).Build()); err != nil {
			t.Fatal(err)
		}
		if err := component.AddFunctionImplementation(Function("F", "", P("p", "Natural")).Build()); err != nil {
			t.Fatal(err)
		}
		positive := pattern.Var("P").WithType("Positive")
		if err := component.AddDeclarativeRule(Rule("r").
			On(pattern.MatchEvent("Start").BindParam("p", positive)).
			Do(CallFunction("f", "F", BindingParam("p", "P"))).Build()); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		_, err := architecture.DeterministicModelDigest()
		if err == nil || !strings.Contains(err.Error(), "ambiguous across 2 overloads") {
			t.Fatalf("got %v, want overload ambiguity", err)
		}
	})

	t.Run("duplicate implementation", func(t *testing.T) {
		architecture := NewArchitecture("duplicate-implementation")
		component := NewComponent("c", Interface("I").ProvidesFunction("F", "").Build(), nil)
		for range 2 {
			if err := component.AddFunctionImplementation(Function("F", "").Build()); err != nil {
				t.Fatal(err)
			}
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		_, err := architecture.DeterministicModelDigest()
		if err == nil || !strings.Contains(err.Error(), "duplicate implementation") {
			t.Fatalf("got %v, want duplicate implementation error", err)
		}
	})
}

func TestEarlyFunctionReturnTerminatesNestedControlAndSelectsValue(t *testing.T) {
	for _, test := range []struct {
		name string
		n    int
		want string
	}{
		{name: "negative", n: -1, want: "negative"},
		{name: "zero", n: 0, want: "zero"},
		{name: "positive fallback", n: 1, want: "positive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			architecture := NewArchitecture("early-return")
			component := NewComponent("classifier", Interface("Classifier").
				OutAction("Start", P("n", "Integer")).
				OutAction("Unreachable").
				OutAction("Classified", P("class", "String")).
				ProvidesFunction("Classify", "String", P("n", "Integer")).
				Build(), nil)
			if err := component.DeclareState(StateReference("class", "String", "")); err != nil {
				t.Fatal(err)
			}
			body := IfThen(
				LessValues(BoundValue("n"), LiteralValue(0)),
				[]Statement{
					ReturnFromFunction(LiteralValue("negative")),
					CallAction("unreachable-negative", "Unreachable"),
				},
				[]Statement{CaseOf(
					BoundValue("n"), CaseElseMode,
					CaseWhen(LiteralValue(0),
						ReturnFromFunction(LiteralValue("zero")),
						CallAction("unreachable-zero", "Unreachable"),
					),
				)},
			)
			if err := component.AddFunctionImplementation(Function("Classify", "String", P("n", "Integer")).
				Do(body).
				Returns(LiteralValue("positive")).
				Build()); err != nil {
				t.Fatal(err)
			}
			number := pattern.Var("N").WithType("Integer")
			if err := component.AddDeclarativeRule(Rule("classify").
				On(pattern.MatchEvent("Start").BindParam("n", number)).
				Do(
					CallFunctionInto("classify", "class", "Classify", BindingParam("n", "N")),
					CallAction("classified", "Classified", StateParam("class", "class")),
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
				Key: "start", Source: "classifier", Action: "Start", Params: map[string]any{"n": test.n},
			}))
			if err != nil {
				t.Fatal(err)
			}
			returned := result.Poset.ByName("Classify'Return")
			classified := result.Poset.ByName("Classified")
			if len(returned) != 1 || len(classified) != 1 || len(result.Poset.ByName("Unreachable")) != 0 {
				t.Fatalf("return/classified/unreachable counts=%d/%d/%d", len(returned), len(classified), len(result.Poset.ByName("Unreachable")))
			}
			if value, _ := returned[0].Param("Return"); value != test.want {
				t.Fatalf("return=%#v, want %q", value, test.want)
			}
			if classified[0].ParamString("class") != test.want {
				t.Fatalf("classified=%q, want %q", classified[0].ParamString("class"), test.want)
			}
		})
	}
}

func TestVoidEarlyReturnSuppressesRemainingFunctionBody(t *testing.T) {
	architecture := NewArchitecture("void-return")
	component := NewComponent("c", Interface("I").
		OutAction("Start").OutAction("Unreachable").ProvidesFunction("Stop", "").Build(), nil)
	if err := component.AddFunctionImplementation(Function("Stop", "").
		Do(ReturnFromFunctionVoid(), CallAction("unreachable", "Unreachable")).Build()); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeRule(Rule("r").On(pattern.MatchEvent("Start")).
		Do(CallFunction("stop", "Stop")).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10, InputEvent{
		Key: "start", Source: "c", Action: "Start",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Stop'Return")) != 1 || len(result.Poset.ByName("Unreachable")) != 0 {
		t.Fatalf("void return did not terminate body: %#v", result.Poset.All())
	}
}

func TestFunctionReturnStatementValidationAndCanonicalIdentity(t *testing.T) {
	invalid := []struct {
		name       string
		returnType string
		statement  Statement
		fallback   *RuleValue
		want       string
	}{
		{name: "value from void", statement: ReturnFromFunction(LiteralValue(1)), want: "no return type"},
		{name: "missing typed value", returnType: "Integer", statement: ReturnFromFunctionVoid(), fallback: ruleValuePointer(LiteralValue(0)), want: "omits the Integer return value"},
		{name: "wrong typed value", returnType: "Boolean", statement: ReturnFromFunction(LiteralValue(1)), fallback: ruleValuePointer(LiteralValue(false)), want: "does not match Boolean"},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			architecture := NewArchitecture("invalid-return")
			component := NewComponent("c", Interface("I").ProvidesFunction("F", test.returnType).Build(), nil)
			builder := Function("F", test.returnType).Do(test.statement)
			if test.fallback != nil {
				builder.Returns(*test.fallback)
			}
			if err := component.AddFunctionImplementation(builder.Build()); err != nil {
				t.Fatal(err)
			}
			if err := architecture.AddComponent(component); err != nil {
				t.Fatal(err)
			}
			_, err := architecture.DeterministicModelDigest()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}

	architecture := NewArchitecture("return-outside-function")
	component := NewComponent("c", Interface("I").OutAction("Start").Build(), nil)
	if err := component.AddDeclarativeRule(Rule("r").On(pattern.MatchEvent("Start")).
		Do(ReturnFromFunctionVoid()).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if _, err := architecture.DeterministicModelDigest(); err == nil || !strings.Contains(err.Error(), "return outside a function") {
		t.Fatalf("got %v, want return-outside-function error", err)
	}

	digest := func(returned int) string {
		t.Helper()
		model := NewArchitecture("return-identity")
		c := NewComponent("c", Interface("I").ProvidesFunction("F", "Integer").Build(), nil)
		if err := c.AddFunctionImplementation(Function("F", "Integer").
			Do(ReturnFromFunction(LiteralValue(returned))).Returns(LiteralValue(0)).Build()); err != nil {
			t.Fatal(err)
		}
		if err := model.AddComponent(c); err != nil {
			t.Fatal(err)
		}
		value, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if digest(1) == digest(2) {
		t.Fatal("early return expression did not change model identity")
	}
}
