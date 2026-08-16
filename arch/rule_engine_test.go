package arch

import (
	"bytes"
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func declarativeRuleArchitecture(t *testing.T, process RuleProcessKind, reverse bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("declarative-rules")
	source := NewComponent("source",
		Interface("Source").OutAction("Input", P("n", "Integer")).Build(), nil)
	processor := NewComponent("processor",
		Interface("Processor").
			InAction("Received", P("n", "Integer")).
			OutAction("Accepted", P("n", "Integer"), P("status", "String")).
			Build(), nil)
	components := []*Component{source, processor}
	if reverse {
		components[0], components[1] = components[1], components[0]
	}
	for _, component := range components {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	ruleBuilder := Rule("processor/accept").
		On(pattern.MatchEvent("Received").BindParam("n", pattern.Var("N").WithType("Integer")))
	if process == RuleAgentProcess {
		ruleBuilder.Agent()
	} else {
		ruleBuilder.Pipe()
	}
	parameters := []RuleParameter{
		BindingParam("n", "N"),
		LiteralParam("status", "accepted"),
	}
	if reverse {
		parameters[0], parameters[1] = parameters[1], parameters[0]
	}
	ruleBuilder.Emit("Accepted", parameters...)
	if err := processor.AddDeclarativeRule(ruleBuilder.Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddConnection(Connect("source", "processor").
		IdentifiedBy("source/processor").On(pattern.MatchEvent("Input")).Send("Received").Build()); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func twoInputRuleJournal(t *testing.T, architecture *Architecture, reverse bool) ExecutionJournal {
	t.Helper()
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	inputs := []InputEvent{
		{Key: "input/one", Source: "source", Action: "Input", Params: map[string]any{"n": 1}},
		{Key: "input/two", Source: "source", Action: "Input", Params: map[string]any{"n": 2}},
	}
	if reverse {
		inputs[0], inputs[1] = inputs[1], inputs[0]
	}
	return NewExecutionJournal(digest, 20, inputs...)
}

func TestDeclarativeRuleBindsOutputAndRecordsWitness(t *testing.T) {
	architecture := declarativeRuleArchitecture(t, RuleAgentProcess, false)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10, InputEvent{
		Key: "input", Source: "source", Action: "Input", Params: map[string]any{"n": 7},
	}))
	if err != nil {
		t.Fatal(err)
	}
	accepted := result.Poset.ByName("Accepted")
	if len(accepted) != 1 {
		t.Fatalf("want one Accepted occurrence, got %d", len(accepted))
	}
	if value, ok := accepted[0].Param("n"); !ok || value != int64(7) {
		t.Fatalf("bound output value = %#v, %v; want int64(7)", value, ok)
	}
	if accepted[0].ParamString("status") != "accepted" {
		t.Fatalf("literal output status = %q", accepted[0].ParamString("status"))
	}
	if len(result.Firings) != 2 || result.Firings[1].Transition != "rule" {
		t.Fatalf("unexpected firing audit: %#v", result.Firings)
	}
	ruleFiring := result.Firings[1]
	if ruleFiring.RuleID != "processor/accept" || len(ruleFiring.MatchedEvents) != 1 || len(ruleFiring.Bindings) != 1 {
		t.Fatalf("incomplete rule witness: %#v", ruleFiring)
	}
	if !result.Poset.IsCausallyBefore(gorapide.EventID(ruleFiring.MatchedEvents[0]), accepted[0].ID) {
		t.Fatal("rule output is not caused by its complete trigger match")
	}
	if len(result.Consumption.Rules) != 1 || len(result.Consumption.Rules[0].Events) != 1 {
		t.Fatalf("missing canonical rule-consumption evidence: %#v", result.Consumption)
	}
}

func TestDeclarativeRulePipeOrdersOutputsAndAgentDoesNot(t *testing.T) {
	for _, test := range []struct {
		name    string
		process RuleProcessKind
		ordered bool
	}{
		{name: "pipe", process: RulePipeProcess, ordered: true},
		{name: "agent", process: RuleAgentProcess, ordered: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			architecture := declarativeRuleArchitecture(t, test.process, false)
			result, err := architecture.ExecuteDeterministic(twoInputRuleJournal(t, architecture, true))
			if err != nil {
				t.Fatal(err)
			}
			outputs := result.Poset.ByName("Accepted")
			if len(outputs) != 2 {
				t.Fatalf("want two outputs, got %d", len(outputs))
			}
			ordered := result.Poset.IsCausallyBefore(outputs[0].ID, outputs[1].ID) ||
				result.Poset.IsCausallyBefore(outputs[1].ID, outputs[0].ID)
			if ordered != test.ordered {
				t.Fatalf("ordered=%v, want %v", ordered, test.ordered)
			}
			if !test.ordered && !result.Poset.IsCausallyIndependent(outputs[0].ID, outputs[1].ID) {
				t.Fatal("agent rule outputs are not causally independent")
			}
		})
	}
}

func TestDeclarativeRuleConsumptionIsScopedPerRule(t *testing.T) {
	architecture := NewArchitecture("per-rule-consumption")
	component := NewComponent("component", Interface("Component").
		OutAction("Input").OutAction("First").OutAction("Second").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	for _, declaration := range []*DeclarativeRule{
		Rule("first").On(pattern.MatchEvent("Input")).Agent().Emit("First").Build(),
		Rule("second").On(pattern.MatchEvent("Input")).Agent().Emit("Second").Build(),
	} {
		if err := component.AddDeclarativeRule(declaration); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "one", Source: "component", Action: "Input"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("First")) != 1 || len(result.Poset.ByName("Second")) != 1 {
		t.Fatal("one rule made the trigger event unavailable to another rule")
	}
	if len(result.Consumption.Rules) != 2 || result.Consumption.Rules[0].Events[0] != result.Consumption.Rules[1].Events[0] {
		t.Fatalf("consumption was not recorded independently per rule: %#v", result.Consumption)
	}
}

func TestDeclarativeRuleSelectsFirstObservedMatchBeforeCanonicalRuleID(t *testing.T) {
	poset := gorapide.NewPoset()
	a := &gorapide.Event{ID: "event-a", Source: "component", Name: "A"}
	b := &gorapide.Event{ID: "event-b", Source: "component", Name: "B"}
	d := &gorapide.Event{ID: "event-d", Source: "component", Name: "D"}
	if err := poset.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(b); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(d, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	// Both matches become enabled by D. The A match is "first" because A was
	// observed before every event of the B,D match, even though its rule ID
	// sorts after the competing declaration.
	rules := []*DeclarativeRule{
		Rule("z/from-a").
			On(pattern.Seq(pattern.MatchEvent("A"), pattern.MatchEvent("D"))).
			Agent().Emit("FromA").Build(),
		Rule("a/from-b").
			On(pattern.Seq(pattern.MatchEvent("B"), pattern.MatchEvent("D"))).
			Agent().Emit("FromB").Build(),
	}
	ranks := map[string]uint64{
		observationRankKey(a): 1,
		observationRankKey(b): 2,
		observationRankKey(d): 3,
	}
	candidate, ok, err := selectDeclarativeRuleCandidate(
		"component", rules, poset, gorapide.EventSet{a, b, d}, ranks,
		NewRuleConsumption(), make(map[string]bool),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || candidate.rule.ID != "z/from-a" {
		t.Fatalf("selected rule = %v, want first-observed z/from-a before canonical-ID fallback", candidate.rule.ID)
	}
}

func TestDeclarativeRuleSelectsMaximalMatchBeforeCanonicalRuleID(t *testing.T) {
	poset := gorapide.NewPoset()
	a := &gorapide.Event{ID: "event-a", Source: "component", Name: "A"}
	b := &gorapide.Event{ID: "event-b", Source: "component", Name: "B"}
	if err := poset.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(b); err != nil {
		t.Fatal(err)
	}
	rules := []*DeclarativeRule{
		Rule("a/short").On(pattern.MatchEvent("A")).Agent().Emit("Short").Build(),
		Rule("z/maximal").On(pattern.Union(pattern.MatchEvent("A"), pattern.MatchEvent("B"))).Agent().Emit("Maximal").Build(),
	}
	ranks := map[string]uint64{observationRankKey(a): 1, observationRankKey(b): 2}
	candidate, ok, err := selectDeclarativeRuleCandidate(
		"component", rules, poset, gorapide.EventSet{a, b}, ranks,
		NewRuleConsumption(), make(map[string]bool),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || candidate.rule.ID != "z/maximal" {
		t.Fatalf("selected rule = %v, want z/maximal before canonical-ID fallback", candidate.rule.ID)
	}
}

func TestDeclarativeRuleSelectsEarliestMatchBeforeCanonicalRuleID(t *testing.T) {
	poset := gorapide.NewPoset()
	shared := &gorapide.Event{ID: "event-shared", Source: "component", Name: "Shared"}
	early := &gorapide.Event{ID: "event-early", Source: "component", Name: "Early"}
	late := &gorapide.Event{ID: "event-late", Source: "component", Name: "Late"}
	if err := poset.AddEvent(shared); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(early); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(late, early.ID); err != nil {
		t.Fatal(err)
	}
	rules := []*DeclarativeRule{
		Rule("a/later").On(pattern.Union(pattern.MatchEvent("Shared"), pattern.MatchEvent("Late"))).Agent().Emit("Later").Build(),
		Rule("z/earlier").On(pattern.Union(pattern.MatchEvent("Shared"), pattern.MatchEvent("Early"))).Agent().Emit("Earlier").Build(),
	}
	// The matches share the first-observed event, so the manual's recursive
	// earlier ordering must resolve the choice before declaration identity.
	ranks := map[string]uint64{
		observationRankKey(shared): 1,
		observationRankKey(early):  2,
		observationRankKey(late):   3,
	}
	candidate, ok, err := selectDeclarativeRuleCandidate(
		"component", rules, poset, gorapide.EventSet{shared, early, late}, ranks,
		NewRuleConsumption(), make(map[string]bool),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || candidate.rule.ID != "z/earlier" {
		t.Fatalf("selected rule = %v, want source-defined z/earlier before canonical-ID fallback", candidate.rule.ID)
	}
}

func TestDeclarativeRuleModelAndArtifactIgnoreDeclarationOrder(t *testing.T) {
	forward := declarativeRuleArchitecture(t, RuleAgentProcess, false)
	reverse := declarativeRuleArchitecture(t, RuleAgentProcess, true)
	forwardDigest, err := forward.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	reverseDigest, err := reverse.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if forwardDigest != reverseDigest {
		t.Fatalf("rule declaration order changed model digest: %s != %s", forwardDigest, reverseDigest)
	}
	left, err := forward.ExecuteDeterministic(twoInputRuleJournal(t, forward, false))
	if err != nil {
		t.Fatal(err)
	}
	right, err := reverse.ExecuteDeterministic(twoInputRuleJournal(t, reverse, true))
	if err != nil {
		t.Fatal(err)
	}
	leftBytes, err := left.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := right.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatalf("equivalent declarative rule execution differs:\nleft=%s\nright=%s", leftBytes, rightBytes)
	}
}

func TestDeclarativeRuleRejectsUnboundAndOpaqueExpressions(t *testing.T) {
	makeArchitecture := func(trigger pattern.Pattern, parameter RuleParameter) *Architecture {
		architecture := NewArchitecture("invalid-rule")
		component := NewComponent("component", Interface("Component").
			OutAction("Input", P("n", "Integer")).
			OutAction("Output", P("n", "Integer")).Build(), nil)
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
		if err := component.AddDeclarativeRule(Rule("rule").On(trigger).Agent().Emit("Output", parameter).Build()); err != nil {
			t.Fatal(err)
		}
		return architecture
	}

	unbound := makeArchitecture(pattern.MatchEvent("Input"), BindingParam("n", "N"))
	if _, err := unbound.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidDeclarativeRule) {
		t.Fatalf("unbound output: expected deterministic rule error, got %v", err)
	}
	opaque := makeArchitecture(
		pattern.MatchEvent("Input").Where(func(*gorapide.Event) bool { return true }),
		LiteralParam("n", 1),
	)
	if _, err := opaque.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidDeclarativeRule) {
		t.Fatalf("opaque trigger: expected deterministic rule error, got %v", err)
	}
}

func TestDeclarativeRuleFiringsCountTowardExplicitLimit(t *testing.T) {
	architecture := declarativeRuleArchitecture(t, RuleAgentProcess, false)
	journal := twoInputRuleJournal(t, architecture, false)
	journal.Limits.MaxFirings = 2
	if _, err := architecture.ExecuteDeterministic(journal); !errors.Is(err, ErrExecutionLimit) {
		t.Fatalf("expected ErrExecutionLimit, got %v", err)
	}
}

func TestDeclarativeRuleGeneratesFiniteBodyPoset(t *testing.T) {
	architecture := NewArchitecture("rule-body-poset")
	component := NewComponent("processor", Interface("Processor").
		OutAction("Input", P("n", "Integer")).
		OutAction("Stage", P("n", "Integer")).
		OutAction("Audit", P("n", "Integer")).
		OutAction("Done", P("n", "Integer")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	rule := Rule("process").
		On(pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer"))).
		Agent().
		Generate(
			RuleEvent("done", "Done", BindingParam("n", "N")).After("stage"),
			RuleEvent("audit", "Audit", BindingParam("n", "N")),
			RuleEvent("stage", "Stage", BindingParam("n", "N")),
		).Build()
	if err := component.AddDeclarativeRule(rule); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "processor", Action: "Input", Params: map[string]any{"n": 9}},
	))
	if err != nil {
		t.Fatal(err)
	}
	input := result.Poset.ByName("Input")[0]
	stage := result.Poset.ByName("Stage")[0]
	audit := result.Poset.ByName("Audit")[0]
	done := result.Poset.ByName("Done")[0]
	for _, output := range []*gorapide.Event{stage, audit, done} {
		if value, ok := output.Param("n"); !ok || value != int64(9) {
			t.Fatalf("%s binding = %#v, %v; want int64(9)", output.Name, value, ok)
		}
		if !result.Poset.IsCausallyBefore(input.ID, output.ID) {
			t.Fatalf("trigger does not causally precede %s", output.Name)
		}
	}
	if !result.Poset.IsCausallyBefore(stage.ID, done.ID) {
		t.Fatal("body-local Stage -> Done relationship is missing")
	}
	if !result.Poset.IsCausallyIndependent(stage.ID, audit.ID) ||
		!result.Poset.IsCausallyIndependent(done.ID, audit.ID) {
		t.Fatal("independent body branches were falsely ordered")
	}
	if len(result.Firings) != 1 || len(result.Firings[0].Generated) != 3 {
		t.Fatalf("incomplete generated-event audit: %#v", result.Firings)
	}
	wantLocalIDs := []string{"audit", "stage", "done"}
	for i, want := range wantLocalIDs {
		if result.Firings[0].Generated[i].OutputID != want {
			t.Fatalf("generated[%d].output_id=%q, want %q", i, result.Firings[0].Generated[i].OutputID, want)
		}
	}
}

func ruleBodyFrontierArchitecture(t *testing.T, process RuleProcessKind) *Architecture {
	t.Helper()
	architecture := NewArchitecture("rule-body-frontier")
	component := NewComponent("component", Interface("Component").
		OutAction("Input", P("n", "Integer")).
		OutAction("Root", P("n", "Integer")).
		OutAction("Left", P("n", "Integer")).
		OutAction("Right", P("n", "Integer")).Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	builder := Rule("branch").
		On(pattern.MatchEvent("Input").BindParam("n", pattern.Var("N").WithType("Integer")))
	if process == RuleAgentProcess {
		builder.Agent()
	} else {
		builder.Pipe()
	}
	if err := component.AddDeclarativeRule(builder.Generate(
		RuleEvent("right", "Right", BindingParam("n", "N")).After("root"),
		RuleEvent("root", "Root", BindingParam("n", "N")),
		RuleEvent("left", "Left", BindingParam("n", "N")).After("root"),
	).Build()); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func generatedEventID(firing FiringRecord, outputID string) gorapide.EventID {
	for _, generated := range firing.Generated {
		if generated.OutputID == outputID {
			return gorapide.EventID(generated.EventID)
		}
	}
	return ""
}

func TestDeclarativeRulePipeOrdersBodyFrontiersAndAgentDoesNot(t *testing.T) {
	for _, test := range []struct {
		name    string
		process RuleProcessKind
		ordered bool
	}{
		{name: "pipe", process: RulePipeProcess, ordered: true},
		{name: "agent", process: RuleAgentProcess, ordered: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			architecture := ruleBodyFrontierArchitecture(t, test.process)
			digest, err := architecture.DeterministicModelDigest()
			if err != nil {
				t.Fatal(err)
			}
			result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
				InputEvent{Key: "one", Source: "component", Action: "Input", Params: map[string]any{"n": 1}},
				InputEvent{Key: "two", Source: "component", Action: "Input", Params: map[string]any{"n": 2}},
			))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Firings) != 2 {
				t.Fatalf("rule firings=%d, want 2", len(result.Firings))
			}
			first, second := result.Firings[0], result.Firings[1]
			secondRoot := generatedEventID(second, "root")
			for _, leaf := range []string{"left", "right"} {
				firstLeaf := generatedEventID(first, leaf)
				ordered := result.Poset.IsCausallyBefore(firstLeaf, secondRoot)
				if ordered != test.ordered {
					t.Fatalf("%s first frontier -> second root=%v, want %v", leaf, ordered, test.ordered)
				}
				if !test.ordered && !result.Poset.IsCausallyIndependent(firstLeaf, secondRoot) {
					t.Fatalf("agent body occurrences falsely order %s and second root", leaf)
				}
			}
		})
	}
}

func orderedRuleBodyArchitecture(t *testing.T, reverse bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("ordered-rule-body")
	component := NewComponent("component", Interface("Component").
		OutAction("Input").OutAction("A").OutAction("B").OutAction("C").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	causes := []string{"a", "b"}
	outputs := []RuleOutput{
		RuleEvent("a", "A"),
		RuleEvent("b", "B"),
		RuleEvent("c", "C").After(causes...),
	}
	if reverse {
		outputs[0], outputs[2] = outputs[2], outputs[0]
		causes[0], causes[1] = causes[1], causes[0]
		for i := range outputs {
			if outputs[i].ID == "c" {
				outputs[i] = outputs[i].After(causes...)
			}
		}
	}
	if err := component.AddDeclarativeRule(
		Rule("generate").On(pattern.MatchEvent("Input")).Agent().Generate(outputs...).Build(),
	); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestDeclarativeRuleBodyIgnoresOutputAndCauseDeclarationOrder(t *testing.T) {
	forward := orderedRuleBodyArchitecture(t, false)
	reverse := orderedRuleBodyArchitecture(t, true)
	forwardDigest, err := forward.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	reverseDigest, err := reverse.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if forwardDigest != reverseDigest {
		t.Fatalf("equivalent body declarations changed model digest: %s != %s", forwardDigest, reverseDigest)
	}
	journal := NewExecutionJournal(forwardDigest, 10, InputEvent{Key: "input", Source: "component", Action: "Input"})
	left, err := forward.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	right, err := reverse.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	leftBytes, err := left.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := right.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatalf("equivalent body executions differ:\nleft=%s\nright=%s", leftBytes, rightBytes)
	}
}

func TestDeclarativeRuleRejectsInvalidBodyGraphs(t *testing.T) {
	tests := []struct {
		name    string
		outputs []RuleOutput
	}{
		{name: "missing cause", outputs: []RuleOutput{RuleEvent("a", "A").After("missing")}},
		{name: "cycle", outputs: []RuleOutput{RuleEvent("a", "A").After("b"), RuleEvent("b", "B").After("a")}},
		{name: "duplicate ID", outputs: []RuleOutput{RuleEvent("a", "A"), RuleEvent("a", "A")}},
		{name: "undeclared action", outputs: []RuleOutput{RuleEvent("a", "Missing")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			architecture := NewArchitecture("invalid-body")
			component := NewComponent("component", Interface("Component").
				OutAction("Input").OutAction("A").OutAction("B").Build(), nil)
			if err := architecture.AddComponent(component); err != nil {
				t.Fatal(err)
			}
			if err := component.AddDeclarativeRule(
				Rule("rule").On(pattern.MatchEvent("Input")).Agent().Generate(test.outputs...).Build(),
			); err != nil {
				t.Fatal(err)
			}
			if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) || !errors.Is(err, ErrInvalidDeclarativeRule) {
				t.Fatalf("expected deterministic invalid-body error, got %v", err)
			}
		})
	}
}

func TestDeclarativeRuleNullBodyConsumesWithoutGeneratingEvents(t *testing.T) {
	architecture := NewArchitecture("null-rule-body")
	component := NewComponent("component", Interface("Component").OutAction("Input").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeRule(
		Rule("consume").On(pattern.MatchEvent("Input")).Pipe().NoEvents().Build(),
	); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "component", Action: "Input"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.Events()) != 2 || len(result.Firings) != 1 || len(result.Firings[0].Generated) != 0 {
		t.Fatalf("unexpected null-body execution: events=%d firings=%#v", len(result.Poset.Events()), result.Firings)
	}
	if len(result.Consumption.Rules) != 1 || len(result.Consumption.Rules[0].Events) != 1 {
		t.Fatalf("null body did not consume its trigger: %#v", result.Consumption)
	}
}
