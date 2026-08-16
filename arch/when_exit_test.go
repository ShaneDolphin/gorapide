package arch

import (
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestExitEnclosingWhenTerminatesImplicitReactiveLoop(t *testing.T) {
	architecture := NewArchitecture("exit-when")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input").OutAction("Before").OutAction("After").Build(), nil)
	body := StatementBody(
		CallAction("before", "Before"),
		LoopDo(ExitLoop()),
		ExitEnclosingWhen(),
		CallAction("after", "After"),
	)
	if err := component.AddDeclarativeProcess(Process("p").StartAt("watch").States(
		WhenState("watch", pattern.MatchEvent("Input"), body),
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
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "first", Source: "worker", Action: "Input"},
		InputEvent{Key: "second", Source: "worker", Action: "Input", Causes: []string{"first"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	before, after := result.Poset.ByName("Before"), result.Poset.ByName("After")
	if len(before) != 1 || len(after) != 0 || len(result.Firings) != 1 {
		t.Fatalf("exit when did not suppress later/repeated body execution: %#v", result.Firings)
	}
	input := result.Poset.ByName("Input")
	if len(input) != 2 || !result.Poset.IsCausallyBefore(input[0].ID, before[0].ID) &&
		!result.Poset.IsCausallyBefore(input[1].ID, before[0].ID) {
		t.Fatal("exit-when body lost its selected trigger causality")
	}
	if len(result.Processes) != 1 || !result.Processes[0].Terminated || result.Processes[0].State != "" {
		t.Fatalf("exit when final process state=%#v", result.Processes)
	}
}

func TestConditionalExitEnclosingWhenUsesCurrentBindings(t *testing.T) {
	architecture := NewArchitecture("conditional-exit-when")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Input", P("n", "Integer")).
		OutAction("Seen", P("n", "Integer")).Build(), nil)
	number := pattern.Var("N").WithType("Integer")
	trigger := pattern.MatchEvent("Input").BindParam("n", number)
	body := StatementBody(
		ExitEnclosingWhenWhere(EqualValues(BoundValue("N"), LiteralValue(int64(0)))),
		CallAction("seen", "Seen", BindingParam("n", "N")),
	)
	if err := component.AddDeclarativeProcess(Process("p").StartAt("watch").States(
		WhenState("watch", trigger, body),
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
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "one", Source: "worker", Action: "Input", Params: map[string]any{"n": 1}},
		InputEvent{Key: "zero", Source: "worker", Action: "Input", Params: map[string]any{"n": 0}, Causes: []string{"one"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	seen := result.Poset.ByName("Seen")
	if len(seen) != 1 {
		t.Fatalf("conditional exit when generated %d Seen events", len(seen))
	}
	value, _ := seen[0].Param("n")
	if value != int64(1) || len(result.Firings) != 2 || !result.Processes[0].Terminated {
		t.Fatalf("conditional exit when result value=%#v firings=%#v process=%#v", value, result.Firings, result.Processes)
	}
}

func TestExitEnclosingWhenValidationIsLexicalAndTyped(t *testing.T) {
	t.Run("ordinary await", func(t *testing.T) {
		architecture := NewArchitecture("bad-await-exit")
		component := NewComponent("worker", Interface("Worker").OutAction("Input").Build(), nil)
		process := Process("p").StartAt("wait").States(
			AwaitState("wait", Await("a").On(pattern.MatchEvent("Input")).
				Do(ExitEnclosingWhen()).Terminate().Build()),
		).Build()
		if err := component.AddDeclarativeProcess(process); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		_, err := architecture.DeterministicModelDigest()
		if !errors.Is(err, ErrUnsupportedDeterministicModel) || !strings.Contains(err.Error(), "outside a source-equivalent when") {
			t.Fatalf("got %v, want lexical exit-when error", err)
		}
	})

	t.Run("ordinary rule", func(t *testing.T) {
		architecture := NewArchitecture("bad-rule-exit")
		component := NewComponent("worker", Interface("Worker").OutAction("Input").Build(), nil)
		if err := component.AddDeclarativeRule(Rule("r").On(pattern.MatchEvent("Input")).
			Do(ExitEnclosingWhen()).Build()); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		_, err := architecture.DeterministicModelDigest()
		if !errors.Is(err, ErrUnsupportedDeterministicModel) || !strings.Contains(err.Error(), "outside a source-equivalent when") {
			t.Fatalf("got %v, want lexical exit-when error", err)
		}
	})

	t.Run("non boolean", func(t *testing.T) {
		architecture := NewArchitecture("bad-when-condition")
		component := NewComponent("worker", Interface("Worker").OutAction("Input").Build(), nil)
		process := Process("p").StartAt("watch").States(
			WhenState("watch", pattern.MatchEvent("Input"), StatementBody(
				ExitEnclosingWhenWhere(LiteralValue(int64(1))),
			)),
		).Build()
		if err := component.AddDeclarativeProcess(process); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		_, err := architecture.DeterministicModelDigest()
		if !errors.Is(err, ErrUnsupportedDeterministicModel) || !strings.Contains(err.Error(), "condition has type Integer, want Boolean") {
			t.Fatalf("got %v, want typed exit-when error", err)
		}
	})
}

func TestOrdinaryNamedDoControlUsesSourceWhenBoundary(t *testing.T) {
	build := func(label string) *Architecture {
		architecture := NewArchitecture("named-when-boundary")
		component := NewComponent("worker", Interface("Worker").
			OutAction("Input", P("n", "Integer")).
			OutAction("After", P("n", "Integer")).
			OutAction("Wrong").Build(), nil)
		number := pattern.Var("N").WithType("Integer")
		state := WhenState("watch", pattern.MatchEvent("Input").BindParam("n", number), StatementBody(
			NextNamedWhen(label, EqualValues(BoundValue("N"), LiteralValue(int64(1)))),
			CallAction("after", "After", BindingParam("n", "N")),
			ExitNamed(label),
			CallAction("wrong", "Wrong"),
		))
		state = NameWhenState(label, state)
		if err := component.AddDeclarativeProcess(Process("p").StartAt("watch").States(state).Build()); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		return architecture
	}
	lower, upper := build("cycle"), build("CYCLE")
	lowerDigest, err := lower.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	upperDigest, err := upper.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if lowerDigest != upperDigest {
		t.Fatalf("when do-label case changed model identity: %s != %s", lowerDigest, upperDigest)
	}
	result, err := lower.ExecuteDeterministic(NewExecutionJournal(lowerDigest, 20,
		InputEvent{Key: "one", Source: "worker", Action: "Input", Params: map[string]any{"n": 1}},
		InputEvent{Key: "two", Source: "worker", Action: "Input", Params: map[string]any{"n": 2}, Causes: []string{"one"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	after := result.Poset.ByName("After")
	if len(after) != 1 || len(result.Poset.ByName("Wrong")) != 0 || !result.Processes[0].Terminated {
		t.Fatalf("named when boundary result After/Wrong=%d/%d process=%#v", len(after), len(result.Poset.ByName("Wrong")), result.Processes)
	}
	value, _ := after[0].Param("n")
	if value != int64(2) {
		t.Fatalf("After value=%#v, want 2", value)
	}
}

func TestNamedWhenStateAndControlValidationIsLexical(t *testing.T) {
	t.Run("named ordinary await", func(t *testing.T) {
		architecture := NewArchitecture("named-ordinary-await")
		component := NewComponent("worker", Interface("Worker").OutAction("Input").Build(), nil)
		state := NameWhenState("cycle", AwaitState("wait",
			Await("one").On(pattern.MatchEvent("Input")).Do(NullStatement()).Terminate().Build(),
		))
		if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(state).Build()); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		_, err := architecture.DeterministicModelDigest()
		if !errors.Is(err, ErrUnsupportedDeterministicModel) || !strings.Contains(err.Error(), "not a source-equivalent when") {
			t.Fatalf("got %v, want named ordinary-await rejection", err)
		}
	})

	t.Run("non-enclosing process do", func(t *testing.T) {
		architecture := NewArchitecture("missing-process-do")
		component := NewComponent("worker", Interface("Worker").OutAction("Input").Build(), nil)
		state := NameWhenState("cycle", WhenState("watch", pattern.MatchEvent("Input"), StatementBody(
			ExitNamed("missing"),
		)))
		if err := component.AddDeclarativeProcess(Process("p").StartAt("watch").States(state).Build()); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		_, err := architecture.DeterministicModelDigest()
		if !errors.Is(err, ErrUnsupportedDeterministicModel) || !strings.Contains(err.Error(), "names non-enclosing do") {
			t.Fatalf("got %v, want non-enclosing process-do rejection", err)
		}
	})
}
