package arch

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestClosedExpressionsUpdateStateAndGenerateTypedOutputs(t *testing.T) {
	architecture := NewArchitecture("closed-expressions")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input", P("n", "Integer")).
		OutAction("Computed",
			P("sum", "Integer"), P("negative", "Integer"),
			P("half", "Integer"), P("positive", "Boolean")).
		Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("version", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	n := BoundValue("N")
	trigger := pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))
	process := Process("calculator").StartAt("wait").States(
		AwaitState("wait", Await("calculate").On(trigger).
			Assign(AssignState("version", AddValues(ReadState("version"), LiteralValue(1)))).
			Emit("Computed",
				ExpressionParam("sum", AddValues(n, ReadState("version"))),
				ExpressionParam("negative", NegateValue(n)),
				ExpressionParam("half", DivideValues(n, LiteralValue(2))),
				ExpressionParam("positive", GreaterValues(n, LiteralValue(0))),
			).Then("wait").Build()),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20,
		InputEvent{Key: "one", Source: "worker", Action: "Input", Params: map[string]any{"n": 8}},
		InputEvent{Key: "two", Source: "worker", Action: "Input", Params: map[string]any{"n": -6}, Causes: []string{"one"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	outputs := result.Poset.ByName("Computed")
	if len(outputs) != 2 || result.State[0].Version != 2 || result.State[0].Value.Text != "2" {
		t.Fatalf("expression process result is incomplete: outputs=%d state=%#v", len(outputs), result.State)
	}
	type values struct {
		sum, negative, half int64
		positive            bool
	}
	actual := make(map[int64]values)
	for _, output := range outputs {
		sum, _ := output.Param("sum")
		negative, _ := output.Param("negative")
		half, _ := output.Param("half")
		positive, _ := output.Param("positive")
		actual[negative.(int64)] = values{
			sum: sum.(int64), negative: negative.(int64), half: half.(int64), positive: positive.(bool),
		}
	}
	if actual[-8] != (values{sum: 9, negative: -8, half: 4, positive: true}) {
		t.Fatalf("first expression result=%#v", actual[-8])
	}
	if actual[6] != (values{sum: -4, negative: 6, half: -3, positive: false}) {
		t.Fatalf("second expression result=%#v", actual[6])
	}
	for _, firing := range result.Firings {
		if len(firing.StateWrites) != 1 || len(firing.StateReads) != 2 {
			t.Fatalf("nested expression state access is not auditable: %#v", firing)
		}
	}
}

func TestClosedBooleanAndEqualityExpressions(t *testing.T) {
	architecture := NewArchitecture("boolean-expressions")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input", P("n", "Integer"), P("name", "String")).
		OutAction("Result", P("valid", "Boolean")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	trigger := pattern.MatchEvent("Input").
		BindParam("n", pattern.Var("N").WithType("Integer")).
		BindParam("name", pattern.Var("Name").WithType("String"))
	valid := AndValues(
		GreaterOrEqualValues(BoundValue("N"), LiteralValue(1)),
		AndValues(
			LessOrEqualValues(BoundValue("N"), LiteralValue(10)),
			NotValue(NotEqualValues(BoundValue("Name"), LiteralValue("allowed"))),
		),
	)
	if err := component.AddDeclarativeRule(
		Rule("validate").On(trigger).Emit("Result", ExpressionParam("valid", valid)).Build(),
	); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "worker", Action: "Input", Params: map[string]any{"n": 5, "name": "allowed"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := result.Poset.ByName("Result")[0].Param("valid"); value != true {
		t.Fatalf("nested boolean result=%#v, want true", value)
	}
}

func TestConnectionTargetExpressionsAreClosedTypedAndReplayable(t *testing.T) {
	architecture := NewArchitecture("connection-expression")
	source := NewComponent("source", Interface("Source").OutAction("Input", P("n", "Integer")).Build(), nil)
	sink := NewComponent("sink", Interface("Sink").InAction("Received", P("n", "Integer")).Build(), nil)
	if err := architecture.AddComponent(source); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(sink); err != nil {
		t.Fatal(err)
	}
	connection := Connect("source", "sink").IdentifiedBy("increment").Pipe().
		On(pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))).
		SendParameters("Received", ConnectionExpressionParam("n",
			AddValues(BoundValue("N"), LiteralValue(1)))).Build()
	if err := architecture.AddConnection(connection); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "source", Action: "Input", Params: map[string]any{"n": 4}},
	)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := result.Poset.ByName("Received")[0].Param("n"); value != int64(5) {
		t.Fatalf("connection expression value=%#v, want 5", value)
	}
	expected, _ := result.ArtifactDigest()
	if _, err := architecture.ReplayDeterministic(journal, expected); err != nil {
		t.Fatal(err)
	}
}

func TestClosedExpressionsRejectInvalidTypesOperatorsAndArithmetic(t *testing.T) {
	makeArchitecture := func(expression RuleValue) (*Architecture, *Component) {
		architecture := NewArchitecture("invalid-expression")
		component := NewComponent("worker", Interface("Worker").
			OutAction("Input").OutAction("Output", P("n", "Integer")).Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		if err := component.AddDeclarativeRule(
			Rule("compute").On(pattern.MatchEvent("Input")).
				Emit("Output", ExpressionParam("n", expression)).Build(),
		); err != nil {
			t.Fatal(err)
		}
		return architecture, component
	}

	for _, test := range []struct {
		name       string
		expression RuleValue
	}{
		{name: "wrong result type", expression: EqualValues(LiteralValue(1), LiteralValue(1))},
		{name: "wrong operand type", expression: AddValues(LiteralValue(true), LiteralValue(1))},
		{name: "unsupported operator", expression: BinaryValue("host-callback", LiteralValue(1), LiteralValue(2))},
	} {
		t.Run(test.name, func(t *testing.T) {
			architecture, _ := makeArchitecture(test.expression)
			if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidDeclarativeRule) {
				t.Fatalf("expected explicit expression validation error, got %v", err)
			}
		})
	}

	runtimeTests := []struct {
		name       string
		expression RuleValue
		want       string
	}{
		{name: "division by zero", expression: DivideValues(LiteralValue(1), LiteralValue(0)), want: "division by zero"},
		{name: "addition overflow", expression: AddValues(LiteralValue(int64(math.MaxInt64)), LiteralValue(1)), want: "integer overflow"},
		{name: "negation overflow", expression: NegateValue(LiteralValue(int64(math.MinInt64))), want: "integer overflow"},
	}
	for _, test := range runtimeTests {
		t.Run(test.name, func(t *testing.T) {
			architecture, _ := makeArchitecture(test.expression)
			digest, err := architecture.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			_, err = architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
				InputEvent{Key: "input", Source: "worker", Action: "Input"},
			))
			if !errors.Is(err, ErrInvalidStateReference) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q expression error, got %v", test.want, err)
			}
		})
	}
}

func TestExpressionOperatorChangesCanonicalModelIdentity(t *testing.T) {
	makeDigest := func(expression RuleValue) string {
		architecture := NewArchitecture("expression-identity")
		component := NewComponent("worker", Interface("Worker").
			OutAction("Input", P("n", "Integer")).OutAction("Output", P("n", "Integer")).Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		trigger := pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))
		if err := component.AddDeclarativeRule(
			Rule("compute").On(trigger).Emit("Output", ExpressionParam("n", expression)).Build(),
		); err != nil {
			t.Fatal(err)
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	add := makeDigest(AddValues(BoundValue("N"), LiteralValue(1)))
	subtract := makeDigest(SubtractValues(BoundValue("N"), LiteralValue(1)))
	if add == subtract {
		t.Fatal("different expression operators have the same model identity")
	}
}

func TestDefaultConnectionIdentityIncludesClosedTargetExpression(t *testing.T) {
	trigger := pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))
	makeConnection := func(expression RuleValue) *Connection {
		return Connect("source", "sink").Pipe().On(trigger).
			SendParameters("Received", ConnectionExpressionParam("n", expression)).Build()
	}
	add := makeConnection(AddValues(BoundValue("N"), LiteralValue(1)))
	subtract := makeConnection(SubtractValues(BoundValue("N"), LiteralValue(1)))
	if add.ID == subtract.ID {
		t.Fatal("default connection ID omitted its closed target expression")
	}
}

func TestClosedIntegerExpressionsSatisfyConstrainedMembershipByValue(t *testing.T) {
	positive := AddValues(LiteralValue(1), LiteralValue(1))
	if !ruleValueAssignableToPredefined(positive, "Integer", "Positive") {
		t.Fatal("closed value 1 + 1 did not satisfy Positive membership")
	}
	zero := SubtractValues(LiteralValue(1), LiteralValue(1))
	if ruleValueAssignableToPredefined(zero, "Integer", "Positive") {
		t.Fatal("closed value 1 - 1 incorrectly satisfied Positive membership")
	}
	if !ruleValueAssignableToPredefined(zero, "Integer", "Natural") {
		t.Fatal("closed value 1 - 1 did not satisfy Natural membership")
	}
	if ruleValueAssignableToPredefined(BoundValue("N"), "Integer", "Positive") {
		t.Fatal("open Integer expression was narrowed to Positive without a value proof")
	}
}
