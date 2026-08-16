package arch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func ambiguousRuleArchitecture(t *testing.T) *Architecture {
	t.Helper()
	architecture := NewArchitecture("choice-schedule")
	component := NewComponent("component", Interface("Component").
		OutAction("Input").OutAction("A").OutAction("Z").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	for _, rule := range []*DeclarativeRule{
		Rule("a").On(pattern.MatchEvent("Input")).Agent().Emit("A").Build(),
		Rule("z").On(pattern.MatchEvent("Input")).Agent().Emit("Z").Build(),
	} {
		if err := component.AddDeclarativeRule(rule); err != nil {
			t.Fatal(err)
		}
	}
	return architecture
}

func TestExplicitChoiceScheduleSelectsPermittedRuleOrder(t *testing.T) {
	architecture := ambiguousRuleArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "component", Action: "Input"},
	)
	defaultResult, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultResult.Choices) < 1 || len(defaultResult.Choices[0].Options) != 2 || defaultResult.Choices[0].Domain != "declarative-rule:component" {
		t.Fatalf("missing arbitrary-choice witness: %#v", defaultResult.Choices)
	}
	if defaultResult.Choices[0].Scheduled || defaultResult.Firings[0].RuleID != "a" {
		t.Fatalf("default policy did not choose canonical alternative: choices=%#v firings=%#v", defaultResult.Choices, defaultResult.Firings)
	}
	var selectZ string
	for _, option := range defaultResult.Choices[0].Options {
		if strings.Contains(option, "/z@") {
			selectZ = option
		}
	}
	if selectZ == "" {
		t.Fatalf("choice options do not identify rule z: %#v", defaultResult.Choices[0].Options)
	}
	journal.Choices = []ChoiceDecision{{
		Point: defaultResult.Choices[0].Point, Selection: selectZ,
	}}
	scheduledResult, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if scheduledResult.Firings[0].RuleID != "z" || !scheduledResult.Choices[0].Scheduled ||
		scheduledResult.Choices[0].Selected != selectZ {
		t.Fatalf("explicit schedule was not honored: choices=%#v firings=%#v", scheduledResult.Choices, scheduledResult.Firings)
	}
	if len(scheduledResult.Poset.ByName("A")) != 1 || len(scheduledResult.Poset.ByName("Z")) != 1 {
		t.Fatal("choice scheduling removed a permitted rule instead of selecting execution order")
	}
}

func TestExplicitChoiceScheduleRejectsUnavailableAndUnusedDecisions(t *testing.T) {
	architecture := ambiguousRuleArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	base := NewExecutionJournal(digest, 10,
		InputEvent{Key: "input", Source: "component", Action: "Input"},
	)
	result, err := architecture.ExecuteDeterministic(base)
	if err != nil {
		t.Fatal(err)
	}

	unavailable := base
	unavailable.Choices = []ChoiceDecision{{Point: result.Choices[0].Point, Selection: "not-an-option"}}
	if _, err := architecture.ExecuteDeterministic(unavailable); !errors.Is(err, ErrChoiceScheduleMismatch) {
		t.Fatalf("unavailable selection: expected ErrChoiceScheduleMismatch, got %v", err)
	}

	unused := base
	unused.Choices = []ChoiceDecision{{Point: "unused", Selection: "also-unused"}}
	if _, err := architecture.ExecuteDeterministic(unused); !errors.Is(err, ErrChoiceScheduleMismatch) {
		t.Fatalf("unused selection: expected ErrChoiceScheduleMismatch, got %v", err)
	}
}

func TestChoiceScheduleCanonicalOrderAndRoundTrip(t *testing.T) {
	journal := NewExecutionJournal("model", 1)
	journal.Choices = []ChoiceDecision{
		{Point: "z", Selection: "last"},
		{Point: "a", Selection: "first"},
	}
	encoded, err := journal.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseExecutionJournal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Choices) != 2 || parsed.Choices[0].Point != "a" || parsed.Choices[1].Point != "z" {
		t.Fatalf("choice schedule is not canonical: %#v", parsed.Choices)
	}
	roundTrip, err := parsed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, roundTrip) {
		t.Fatalf("choice schedule round trip differs:\nfirst=%s\nsecond=%s", encoded, roundTrip)
	}
}
