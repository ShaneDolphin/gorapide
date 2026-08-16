package arch

import (
	"bytes"
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func finiteChoiceMap(t *testing.T) (*EventPatternMap, *gorapide.Poset) {
	t.Helper()
	domainInterface := Interface("Domain").OutAction("Trigger").Build()
	rangeInterface := Interface("Range").OutAction("Left").OutAction("Right").Build()
	rule := MappingRule("join").On(pattern.MatchEvent("Trigger")).GenerateOneOf(
		[]RuleOutput{RuleEvent("left", "Left"), RuleEvent("right", "Right")},
		[]RuleOutput{RuleEvent("left", "Left"), RuleEvent("right", "Right").After("left")},
		[]RuleOutput{RuleEvent("left", "Left").After("right"), RuleEvent("right", "Right")},
	).Build()
	mapping := NewEventPatternMap("choice-map").FromObject("domain", domainInterface).
		ToInterface(rangeInterface).AddRule(rule).Build()
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(deterministicMapDomainEvent(t, "Trigger", "trigger", nil, nil)); err != nil {
		t.Fatal(err)
	}
	return mapping, domain
}

func TestEventPatternMapExplicitGeneratorAlternativesExploreDeterministically(t *testing.T) {
	mapping, domain := finiteChoiceMap(t)
	prepared, err := mapping.PrepareDeterministic()
	if err != nil {
		t.Fatal(err)
	}
	request := MapExecutionRequest{Limits: MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 4}}
	limits := ExplorationLimits{MaxExecutions: 8, MaxChoiceDepth: 1}
	first, err := prepared.ExploreDeterministic(domain, request, limits)
	if err != nil {
		t.Fatal(err)
	}
	mapping.Rules[0].Body.Alternatives[0][0].Action = "Mutated"
	second, err := prepared.ExploreDeterministic(domain, request, limits)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Complete || first.Executions != 4 || len(first.Computations) != 3 {
		t.Fatalf("map exploration=%#v", first)
	}
	firstBytes, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("repeated map exploration differs:\nfirst=%s\nsecond=%s", firstBytes, secondBytes)
	}
	for _, computation := range first.Computations {
		if computation.Result == nil || len(computation.Schedule) != 1 {
			t.Fatalf("missing map exploration replay witness: %#v", computation)
		}
		replayRequest := request
		replayRequest.Choices = computation.Schedule
		replayed, err := prepared.ReplayDeterministicRequest(
			domain, replayRequest, computation.ArtifactDigest,
		)
		if err != nil {
			t.Fatal(err)
		}
		replayedBytes, _ := replayed.MarshalCanonical()
		resultBytes, _ := computation.Result.MarshalCanonical()
		if !bytes.Equal(replayedBytes, resultBytes) {
			t.Fatal("map exploration replay was not byte-identical")
		}
	}
}

func TestEventPatternMapExplorationReportsExplicitBounds(t *testing.T) {
	mapping, domain := finiteChoiceMap(t)
	request := MapExecutionRequest{Limits: MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 4}}
	truncated, err := mapping.ExploreDeterministic(domain, request,
		ExplorationLimits{MaxExecutions: 1, MaxChoiceDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if truncated.Complete || truncated.Executions != 1 {
		t.Fatalf("map exploration did not report truncation: %#v", truncated)
	}
	if _, err := mapping.ExploreDeterministic(domain, request, ExplorationLimits{}); !errors.Is(err, ErrInvalidExplorationLimits) {
		t.Fatalf("expected ErrInvalidExplorationLimits, got %v", err)
	}
}

func TestArchitectureRuleRejectsMapOnlyGeneratorAlternatives(t *testing.T) {
	architecture := NewArchitecture("invalid-alternatives")
	component := NewComponent("component", Interface("Component").OutAction("Trigger").OutAction("Left").OutAction("Right").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	rule := Rule("choice").On(pattern.MatchEvent("Trigger")).GenerateOneOf(
		[]RuleOutput{RuleEvent("left", "Left")},
		[]RuleOutput{RuleEvent("right", "Right")},
	).Build()
	if err := component.AddDeclarativeRule(rule); err != nil {
		t.Fatal(err)
	}
	if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrInvalidDeclarativeRule) {
		t.Fatalf("expected ErrInvalidDeclarativeRule, got %v", err)
	}
}

func TestEventPatternMapCausalEquivalenceIsCanonicalAndReplayable(t *testing.T) {
	build := func(reverse bool) *EventPatternMap {
		domainInterface := Interface("Domain").OutAction("Trigger").Build()
		rangeInterface := Interface("Range").OutAction("Left").OutAction("Right").Build()
		outputs := []RuleOutput{
			RuleEvent("left", "Left").EquivalentTo("right"),
			RuleEvent("right", "Right"),
		}
		if reverse {
			outputs = []RuleOutput{
				RuleEvent("right", "Right").EquivalentTo("left"),
				RuleEvent("left", "Left"),
			}
		}
		return NewEventPatternMap("equivalent-map").FromObject("domain", domainInterface).
			ToInterface(rangeInterface).
			AddRule(MappingRule("equivalent").On(pattern.MatchEvent("Trigger")).Generate(outputs...).Build()).Build()
	}
	left, right := build(false), build(true)
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("equivalence declaration direction/order changed map identity: %s != %s", leftDigest, rightDigest)
	}
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(deterministicMapDomainEvent(t, "Trigger", "trigger", nil, nil)); err != nil {
		t.Fatal(err)
	}
	limits := MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 4}
	result, err := left.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	leftEvent := onlyMapEvent(t, result.Range.ByName("Left"))
	rightEvent := onlyMapEvent(t, result.Range.ByName("Right"))
	if !result.Range.IsCausallyEquivalent(leftEvent.ID, rightEvent.ID) ||
		result.Range.IsCausallyBefore(leftEvent.ID, rightEvent.ID) ||
		result.Range.IsCausallyIndependent(leftEvent.ID, rightEvent.ID) {
		t.Fatal("map range did not retain the declared causal-equivalence class")
	}
	canonical, err := result.Range.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Format != gorapide.CanonicalCausalPreorderFormat || len(canonical.CausalEquivalences) != 1 {
		t.Fatalf("range canonical preorder=%#v", canonical)
	}
	digest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := right.ReplayDeterministic(domain, limits, digest)
	if err != nil {
		t.Fatal(err)
	}
	resultBytes, _ := result.MarshalCanonical()
	replayedBytes, _ := replayed.MarshalCanonical()
	if !bytes.Equal(resultBytes, replayedBytes) {
		t.Fatalf("equivalent map replay differs:\n%s\n%s", resultBytes, replayedBytes)
	}
}

func TestMapCausalEquivalenceValidationRejectsMissingAndStrictPeers(t *testing.T) {
	build := func(outputs ...RuleOutput) *EventPatternMap {
		return NewEventPatternMap("invalid-equivalence").
			FromObject("domain", Interface("Domain").OutAction("Trigger").Build()).
			ToInterface(Interface("Range").OutAction("Left").OutAction("Right").Build()).
			AddRule(MappingRule("invalid").On(pattern.MatchEvent("Trigger")).Generate(outputs...).Build()).Build()
	}
	for name, mapping := range map[string]*EventPatternMap{
		"missing": build(RuleEvent("left", "Left").EquivalentTo("missing")),
		"strict": build(
			RuleEvent("left", "Left").EquivalentTo("right"),
			RuleEvent("right", "Right").After("left"),
		),
	} {
		if _, err := mapping.DeterministicModelDigest(); !errors.Is(err, ErrInvalidEventPatternMap) {
			t.Fatalf("%s equivalence validation=%v", name, err)
		}
	}
}

func TestArchitectureRuleRejectsMapOnlyCausalEquivalence(t *testing.T) {
	architecture := NewArchitecture("invalid-equivalence")
	component := NewComponent("component", Interface("Component").OutAction("Trigger").OutAction("Left").OutAction("Right").Build(), nil)
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	rule := Rule("equivalent").On(pattern.MatchEvent("Trigger")).Generate(
		RuleEvent("left", "Left").EquivalentTo("right"),
		RuleEvent("right", "Right"),
	).Build()
	if err := component.AddDeclarativeRule(rule); err != nil {
		t.Fatal(err)
	}
	if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrInvalidDeclarativeRule) {
		t.Fatalf("expected ErrInvalidDeclarativeRule, got %v", err)
	}
}
