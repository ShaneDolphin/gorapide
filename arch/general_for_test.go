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

func generalForAssignmentArchitecture(t *testing.T) *Architecture {
	t.Helper()
	architecture := NewArchitecture("general-for-assignment")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Emit", P("value", "Integer")).Build(), nil)
	if err := component.DeclareState(StateReference("i", "Integer", 99)); err != nil {
		t.Fatal(err)
	}
	loop := ForObjectExpressions(
		ObjectAssignment("i", LiteralValue(1)),
		ObjectValue(LessOrEqualValues(ReadState("i"), LiteralValue(4))),
		ObjectAssignment("i", AddValues(ReadState("i"), LiteralValue(1))),
		NextWhen(EqualValues(ReadState("i"), LiteralValue(2))),
		CallAction("emit", "Emit", StateParam("value", "i")),
		ExitWhen(EqualValues(ReadState("i"), LiteralValue(3))),
	)
	if err := component.AddDeclarativeRule(
		Rule("run").On(pattern.MatchEvent("Start")).Do(loop).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestGeneralForAssignmentNextAndExitFollowPublishedLowering(t *testing.T) {
	architecture := generalForAssignmentArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 64},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	emitted := result.Poset.ByName("Emit")
	if len(emitted) != 2 {
		t.Fatalf("Emit count=%d, want 2", len(emitted))
	}
	values := make(map[int64]*gorapide.Event)
	for _, event := range emitted {
		value, _ := event.Param("value")
		values[value.(int64)] = event
	}
	if values[1] == nil || values[3] == nil || values[2] != nil || values[4] != nil {
		t.Fatalf("general-for emitted values=%#v, want 1 and 3", values)
	}
	if !result.Poset.IsCausallyBefore(values[1].ID, values[3].ID) {
		t.Fatal("general-for iterations lost program-order causality")
	}
	if len(result.State) != 1 || result.State[0].Name != "i" ||
		result.State[0].Value.Text != "3" || result.State[0].Version != 3 {
		t.Fatalf("final state=%#v, want i=3 at version 3", result.State)
	}
	if len(result.Firings) != 1 || len(result.Firings[0].StateWrites) != 3 {
		t.Fatalf("initializer/next state audit=%#v", result.Firings)
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
		t.Fatal("general-for replay was not byte-identical")
	}

	prior := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prior)
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for run := 0; run < 3; run++ {
			candidate, err := architecture.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := candidate.MarshalCanonical()
			if !bytes.Equal(left, encoded) {
				t.Fatalf("general-for artifact changed at GOMAXPROCS=%d run=%d", processors, run)
			}
		}
	}
}

func generalForFunctionArchitecture(t *testing.T) *Architecture {
	t.Helper()
	architecture := NewArchitecture("general-for-functions")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Emit", P("value", "Integer")).
		ProvidesFunction("Initialize", "").
		ProvidesFunction("More", "Boolean").
		ProvidesFunction("Advance", "").
		Build(), nil)
	if err := component.DeclareState(StateReference("i", "Integer", 88)); err != nil {
		t.Fatal(err)
	}
	for _, implementation := range []*FunctionImplementation{
		Function("Initialize", "").Do(SetState("i", LiteralValue(1))).Build(),
		Function("More", "Boolean").Returns(LessOrEqualValues(ReadState("i"), LiteralValue(3))).Build(),
		Function("Advance", "").Do(SetState("i", AddValues(ReadState("i"), LiteralValue(1)))).Build(),
	} {
		if err := component.AddFunctionImplementation(implementation); err != nil {
			t.Fatal(err)
		}
	}
	if err := component.AddDeclarativeRule(
		Rule("run").On(pattern.MatchEvent("Start")).Do(
			ForObjectExpressions(
				ObjectFunctionCall("initialize", "Initialize"),
				ObjectFunctionCall("more", "More"),
				ObjectFunctionCall("advance", "Advance"),
				CallAction("emit", "Emit", StateParam("value", "i")),
			),
		).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestGeneralForFunctionExpressionsRetainCallReturnCausality(t *testing.T) {
	architecture := generalForFunctionArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 30, MaxStatements: 128},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Initialize'Call")) != 1 ||
		len(result.Poset.ByName("Initialize'Return")) != 1 ||
		len(result.Poset.ByName("More'Call")) != 4 ||
		len(result.Poset.ByName("More'Return")) != 4 ||
		len(result.Poset.ByName("Advance'Call")) != 3 ||
		len(result.Poset.ByName("Advance'Return")) != 3 ||
		len(result.Poset.ByName("Emit")) != 3 {
		t.Fatalf("general-for function protocol counts init=%d/%d more=%d/%d advance=%d/%d emit=%d",
			len(result.Poset.ByName("Initialize'Call")), len(result.Poset.ByName("Initialize'Return")),
			len(result.Poset.ByName("More'Call")), len(result.Poset.ByName("More'Return")),
			len(result.Poset.ByName("Advance'Call")), len(result.Poset.ByName("Advance'Return")),
			len(result.Poset.ByName("Emit")))
	}
	initializeReturn := result.Poset.ByName("Initialize'Return")[0]
	falseMore := (*gorapide.Event)(nil)
	for _, event := range result.Poset.ByName("More'Return") {
		value, _ := event.Param("Return")
		if !value.(bool) {
			falseMore = event
		}
	}
	if falseMore == nil {
		t.Fatal("general-for test never returned false")
	}
	for _, emitted := range result.Poset.ByName("Emit") {
		if !result.Poset.IsCausallyBefore(initializeReturn.ID, emitted.ID) ||
			!result.Poset.IsCausallyBefore(emitted.ID, falseMore.ID) {
			t.Fatal("initializer/test/body/next call ordering is not represented causally")
		}
	}
	if len(result.State) != 1 || result.State[0].Value.Text != "4" || result.State[0].Version != 4 {
		t.Fatalf("function-expression final state=%#v, want i=4", result.State)
	}
}

func TestGeneralForProcessContinuationRunsNextAfterResume(t *testing.T) {
	architecture := NewArchitecture("general-for-process")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Tick", P("value", "Integer")).OutAction("Done").Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("i", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	loop := ForObjectExpressions(
		ObjectAssignment("i", LiteralValue(1)),
		ObjectValue(LessOrEqualValues(ReadState("i"), LiteralValue(3))),
		ObjectAssignment("i", AddValues(ReadState("i"), LiteralValue(1))),
		NextWhen(EqualValues(ReadState("i"), LiteralValue(2))),
		PauseFor("C", 1),
		CallAction("tick", "Tick", StateParam("value", "i")),
	)
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("start").On(pattern.MatchEvent("Start")).Do(
			loop, CallAction("done", "Done"),
		).Terminate().Build()),
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
	result, err := architecture.ExecuteDeterministic(NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 80},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[int64]bool)
	for _, event := range result.Poset.ByName("Tick") {
		value, _ := event.Param("value")
		values[value.(int64)] = true
	}
	if len(values) != 2 || !values[1] || !values[3] || len(result.Poset.ByName("Done")) != 1 ||
		len(result.ClockAdvances) != 2 {
		t.Fatalf("resumable general-for values=%#v done=%d clocks=%#v",
			values, len(result.Poset.ByName("Done")), result.ClockAdvances)
	}
	if len(result.State) != 1 || result.State[0].Value.Text != "4" || result.State[0].Version != 4 {
		t.Fatalf("resumable general-for final state=%#v", result.State)
	}
}

func TestGeneralForRejectsMalformedObjectExpressions(t *testing.T) {
	tests := []struct {
		name string
		loop Statement
		want string
	}{
		{
			name: "non-Boolean test",
			loop: ForObjectExpressions(ObjectValue(LiteralValue(0)), ObjectValue(LiteralValue(1)), ObjectValue(LiteralValue(2))),
			want: "test has type Integer, want Boolean",
		},
		{
			name: "missing initializer",
			loop: ForObjectExpressions(ExecutableObjectExpression{}, ObjectValue(LiteralValue(false)), ObjectValue(LiteralValue(0))),
			want: "object-expression kind",
		},
		{
			name: "assignment returns reference",
			loop: ForObjectExpressions(
				ObjectValue(LiteralValue(0)),
				ObjectAssignment("flag", LiteralValue(true)),
				ObjectValue(LiteralValue(0)),
			),
			want: "test has type Ref(Boolean), want Boolean",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			architecture := NewArchitecture("invalid-general-for")
			component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
			if err := component.DeclareState(StateReference("flag", "Boolean", false)); err != nil {
				t.Fatal(err)
			}
			if err := component.AddDeclarativeRule(
				Rule("run").On(pattern.MatchEvent("Start")).Do(test.loop).Build(),
			); err != nil {
				t.Fatal(err)
			}
			if err := architecture.AddComponent(component); err != nil {
				t.Fatal(err)
			}
			_, err := architecture.DeterministicModelDigest()
			if err == nil || !strings.Contains(err.Error(), test.want) || !errors.Is(err, ErrInvalidDeclarativeStatement) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}
