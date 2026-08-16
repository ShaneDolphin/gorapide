package arch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/constraint"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestEventPatternMapRejectsModuleSourceBindingWithoutExecutionContext(t *testing.T) {
	domainInterface := Interface("Domain").OutAction("Observed").Build()
	rangeInterface := Interface("Range").OutAction("Abstracted").Build()
	mapping := NewEventPatternMap("module-source-boundary").
		FromObject("domain", domainInterface).
		ToInterface(rangeInterface).
		AddRule(MappingRule("observed").
			On(pattern.MatchEvent("Observed").
				BindModuleSource(pattern.Var("Module").WithType("Domain"))).
			Emit("Abstracted").Build()).Build()
	_, err := mapping.DeterministicModelDigest()
	if !errors.Is(err, ErrInvalidEventPatternMap) ||
		!strings.Contains(err.Error(), "outside deterministic map v4") {
		t.Fatalf("module-source map boundary=%v", err)
	}
}

func TestEventPatternMapGeneratesSeparateRangePosetWithStrongDependency(t *testing.T) {
	domainInterface := Interface("Domain").
		OutAction("Observed", P("value", "Integer")).
		OutAction("Completed", P("value", "Integer")).Build()
	rangeInterface := Interface("Range").
		OutAction("Abstracted", P("value", "Integer")).
		OutAction("Accepted", P("value", "Integer")).Build()
	value := pattern.Var("value").WithType("Integer")
	mapping := NewEventPatternMap("audit-view").
		FromObject("domain", domainInterface).
		ToInterface(rangeInterface).
		AddRule(MappingRule("observed").
			On(pattern.MatchEvent("Observed").BindParam("value", value)).
			Emit("Abstracted", BindingParam("value", "value")).Build()).
		AddRule(MappingRule("completed").
			On(pattern.MatchEvent("Completed").BindParam("value", pattern.Var("value").WithType("Integer"))).
			Emit("Accepted", BindingParam("value", "value")).Build()).Build()

	domain := gorapide.NewPoset()
	observed := deterministicMapDomainEvent(t, "Observed", "observed", nil, map[string]any{"value": 7})
	completed := deterministicMapDomainEvent(t, "Completed", "completed", []gorapide.EventID{observed.ID}, map[string]any{"value": 7})
	if err := domain.AddEvent(observed); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEventWithCause(completed, observed.ID); err != nil {
		t.Fatal(err)
	}
	domainBefore, err := domain.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}

	result, err := mapping.ExecuteDeterministic(domain, MapExecutionLimits{MaxFirings: 8, MaxRangeEvents: 8})
	if err != nil {
		t.Fatal(err)
	}
	if result.Range == domain {
		t.Fatal("map returned the domain poset instead of a new range poset")
	}
	domainAfter, err := domain.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(domainBefore, domainAfter) {
		t.Fatal("map mutated the domain poset")
	}
	abstracted := onlyMapEvent(t, result.Range.ByName("Abstracted"))
	accepted := onlyMapEvent(t, result.Range.ByName("Accepted"))
	start := onlyMapEvent(t, result.Range.ByName("Start"))
	if !result.Range.IsCausallyBefore(start.ID, abstracted.ID) || !result.Range.IsCausallyBefore(start.ID, accepted.ID) {
		t.Fatal("map Start must precede every generated range event")
	}
	if !result.Range.IsCausallyBefore(abstracted.ID, accepted.ID) {
		t.Fatal("domain dependency did not induce the corresponding range dependency")
	}
	if result.Range.ByName("Observed") != nil || result.Range.ByName("Completed") != nil {
		t.Fatal("domain occurrences leaked into the range poset")
	}
	for _, event := range result.Range.All() {
		for _, cause := range result.Range.DirectCauses(event.ID) {
			if cause.ID == observed.ID || cause.ID == completed.ID {
				t.Fatal("domain occurrence was encoded as a range cause")
			}
		}
	}
	if len(result.Artifact.Firings) != 2 {
		t.Fatalf("got %d firing records, want 2", len(result.Artifact.Firings))
	}
	witnessed := make(map[string]bool)
	for _, firing := range result.Artifact.Firings {
		for _, eventID := range firing.Match.Events {
			witnessed[eventID] = true
		}
	}
	if !witnessed[string(observed.ID)] || !witnessed[string(completed.ID)] {
		t.Fatal("artifact did not retain domain match witnesses")
	}
}

func TestEventPatternMapPreservesBodyPosetAndAllToAllInduction(t *testing.T) {
	domainInterface := Interface("Domain").OutAction("A").OutAction("B").OutAction("C").Build()
	rangeInterface := Interface("Range").OutAction("X").OutAction("X2").OutAction("Y").Build()
	mapping := NewEventPatternMap("partial-order-view").
		FromObject("domain", domainInterface).ToInterface(rangeInterface).
		AddRule(MappingRule("pair").On(pattern.Disjoint(pattern.MatchEvent("A"), pattern.MatchEvent("C"))).
			Generate(RuleEvent("x", "X"), RuleEvent("x2", "X2").After("x")).Build()).
		AddRule(MappingRule("single").On(pattern.MatchEvent("B")).Emit("Y").Build()).Build()

	domain := gorapide.NewPoset()
	a := deterministicMapDomainEvent(t, "A", "a", nil, nil)
	c := deterministicMapDomainEvent(t, "C", "c", nil, nil)
	b := deterministicMapDomainEvent(t, "B", "b", []gorapide.EventID{a.ID}, nil)
	if err := domain.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEvent(c); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEventWithCause(b, a.ID); err != nil {
		t.Fatal(err)
	}
	result, err := mapping.ExecuteDeterministic(domain, MapExecutionLimits{MaxFirings: 8, MaxRangeEvents: 8})
	if err != nil {
		t.Fatal(err)
	}
	x := onlyMapEvent(t, result.Range.ByName("X"))
	x2 := onlyMapEvent(t, result.Range.ByName("X2"))
	y := onlyMapEvent(t, result.Range.ByName("Y"))
	if !result.Range.IsCausallyBefore(x.ID, x2.ID) {
		t.Fatal("body-local generator dependency was lost")
	}
	if !result.Range.IsCausallyIndependent(x.ID, y.ID) || !result.Range.IsCausallyIndependent(x2.ID, y.ID) {
		t.Fatal("partial domain ordering invented a strong induced range dependency")
	}
}

func TestEventPatternMapPublishedInducedDependencyRelations(t *testing.T) {
	domain := gorapide.NewPoset()
	a := deterministicMapDomainEvent(t, "A", "a", nil, nil)
	e := deterministicMapDomainEvent(t, "E", "e", nil, nil)
	b := deterministicMapDomainEvent(t, "B", "b", []gorapide.EventID{a.ID}, nil)
	c := deterministicMapDomainEvent(t, "C", "c", []gorapide.EventID{a.ID}, nil)
	d := deterministicMapDomainEvent(t, "D", "d", []gorapide.EventID{b.ID, c.ID}, nil)
	for _, event := range []*gorapide.Event{a, e} {
		if err := domain.AddEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := domain.AddEventWithCause(b, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEventWithCause(c, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEventWithCause(d, b.ID, c.ID); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		policy MapInducedDependencyPolicy
		left   gorapide.EventSet
		right  gorapide.EventSet
		want   bool
	}{
		{"none", NoneInducedDependencyPolicy, gorapide.EventSet{a}, gorapide.EventSet{b}, false},
		{"strong", StrongInducedDependencyPolicy, gorapide.EventSet{a}, gorapide.EventSet{b}, true},
		{"strong partial false", StrongInducedDependencyPolicy, gorapide.EventSet{a, b}, gorapide.EventSet{c, d}, false},
		{"maxima", MaximaInducedDependencyPolicy, gorapide.EventSet{a, b}, gorapide.EventSet{c, d}, true},
		{"dominance", DominanceInducedDependencyPolicy, gorapide.EventSet{a}, gorapide.EventSet{b, e}, true},
		{"dominance missing successor", DominanceInducedDependencyPolicy, gorapide.EventSet{a, e}, gorapide.EventSet{b}, false},
		{"overlook", OverlookInducedDependencyPolicy, gorapide.EventSet{a, e}, gorapide.EventSet{b}, true},
		{"overlook missing predecessor", OverlookInducedDependencyPolicy, gorapide.EventSet{a}, gorapide.EventSet{b, e}, false},
		{"diff overlapping triggers", DiffInducedDependencyPolicy, gorapide.EventSet{a, b}, gorapide.EventSet{b, d}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := mapMatchInduces(test.left, test.right, domain, test.policy); got != test.want {
				t.Fatalf("mapMatchInduces(%s)=%t, want %t", test.policy, got, test.want)
			}
		})
	}
}

func TestEventPatternMapInducedPolicyIsCanonicalAndExecutable(t *testing.T) {
	domainInterface := Interface("Domain").OutAction("A").OutAction("B").Build()
	rangeInterface := Interface("Range").OutAction("X").OutAction("Y").Build()
	build := func(policy MapInducedDependencyPolicy) *EventPatternMap {
		return NewEventPatternMap("policy-view").
			FromObject("domain", domainInterface).
			ToInterface(rangeInterface).
			WithInducedDependencyPolicy(policy).
			AddRule(MappingRule("a").On(pattern.MatchEvent("A")).Emit("X").Build()).
			AddRule(MappingRule("b").On(pattern.MatchEvent("B")).Emit("Y").Build()).Build()
	}
	strong := build(StrongInducedDependencyPolicy)
	none := build(NoneInducedDependencyPolicy)
	defaultStrong := build("")
	strongDigest, err := strong.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	defaultDigest, err := defaultStrong.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if strongDigest != defaultDigest {
		t.Fatal("omitted map policy did not canonicalize to strong")
	}
	noneDigest, err := none.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if noneDigest == strongDigest {
		t.Fatal("different induced policies shared one canonical model identity")
	}
	strongModel, err := strong.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(strongModel, []byte(`"dependency_policy":"rapide-strong-induced-dependency"`)) ||
		bytes.Contains(strongModel, []byte("rapide-weak-induced-dependency")) {
		t.Fatalf("canonical model retained an incorrect policy identity: %s", strongModel)
	}

	domain := gorapide.NewPoset()
	a := deterministicMapDomainEvent(t, "A", "a", nil, nil)
	b := deterministicMapDomainEvent(t, "B", "b", []gorapide.EventID{a.ID}, nil)
	if err := domain.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEventWithCause(b, a.ID); err != nil {
		t.Fatal(err)
	}
	limits := MapExecutionLimits{MaxFirings: 4, MaxRangeEvents: 4}
	strongResult, err := strong.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	noneResult, err := none.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	if strongResult.Artifact.DependencyPolicy != StrongInducedDependencyPolicy ||
		noneResult.Artifact.DependencyPolicy != NoneInducedDependencyPolicy {
		t.Fatal("artifact omitted the selected induced dependency policy")
	}
	if !strongResult.Range.IsCausallyBefore(
		onlyMapEvent(t, strongResult.Range.ByName("X")).ID,
		onlyMapEvent(t, strongResult.Range.ByName("Y")).ID,
	) {
		t.Fatal("strong policy did not induce range causality")
	}
	if !noneResult.Range.IsCausallyIndependent(
		onlyMapEvent(t, noneResult.Range.ByName("X")).ID,
		onlyMapEvent(t, noneResult.Range.ByName("Y")).ID,
	) {
		t.Fatal("none policy invented range causality")
	}
	noneArtifactDigest, err := noneResult.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := none.ReplayDeterministic(domain, limits, noneArtifactDigest); err != nil {
		t.Fatal(err)
	}

	invalid := build(MapInducedDependencyPolicy("rapide-invented"))
	if _, err := invalid.DeterministicModelDigest(); !errors.Is(err, ErrInvalidEventPatternMap) {
		t.Fatalf("invalid policy error=%v", err)
	}
}

func TestEventPatternMapHidesPrivateDomainActionAllowsPrivateRangeAndPreservesCausality(t *testing.T) {
	domainInterface := Interface("Domain").
		OutAction("Start").PrivateAction("Hidden").OutAction("Done").Build()
	rangeInterface := Interface("Range").OutAction("Opened").OutAction("Closed").Build()
	mapping := NewEventPatternMap("public-view").
		FromObject("domain", domainInterface).ToInterface(rangeInterface).
		AddRule(MappingRule("start").On(pattern.MatchEvent("Start")).Emit("Opened").Build()).
		AddRule(MappingRule("done").On(pattern.MatchEvent("Done")).Emit("Closed").Build()).Build()

	domain := gorapide.NewPoset()
	start := deterministicMapDomainEvent(t, "Start", "start", nil, nil)
	hidden := deterministicMapDomainEvent(t, "Hidden", "hidden", []gorapide.EventID{start.ID}, nil)
	done := deterministicMapDomainEvent(t, "Done", "done", []gorapide.EventID{hidden.ID}, nil)
	if err := domain.AddEvent(start); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEventWithCause(hidden, start.ID); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEventWithCause(done, hidden.ID); err != nil {
		t.Fatal(err)
	}
	result, err := mapping.ExecuteDeterministic(domain, MapExecutionLimits{MaxFirings: 8, MaxRangeEvents: 8})
	if err != nil {
		t.Fatal(err)
	}
	opened := onlyMapEvent(t, result.Range.ByName("Opened"))
	closed := onlyMapEvent(t, result.Range.ByName("Closed"))
	if !result.Range.IsCausallyBefore(opened.ID, closed.ID) {
		t.Fatal("hidden domain intermediary did not preserve induced public range causality")
	}
	for _, firing := range result.Artifact.Firings {
		for _, eventID := range firing.Match.Events {
			if eventID == string(hidden.ID) {
				t.Fatal("private domain occurrence leaked into a mapping witness")
			}
		}
	}

	privateTrigger := NewEventPatternMap("private-trigger").
		FromObject("domain", domainInterface).ToInterface(rangeInterface).
		AddRule(MappingRule("hidden").On(pattern.MatchEvent("Hidden")).Emit("Opened").Build()).Build()
	if _, err := privateTrigger.DeterministicModelDigest(); !errors.Is(err, ErrInvalidEventPatternMap) {
		t.Fatalf("private domain trigger error=%v", err)
	}
	privateRange := Interface("PrivateRange").PrivateAction("Secret").Build()
	privateOutput := NewEventPatternMap("private-output").
		FromObject("domain", domainInterface).ToInterface(privateRange).
		AddRule(MappingRule("start").On(pattern.MatchEvent("Start")).Emit("Secret").Build()).Build()
	privateResult, err := privateOutput.ExecuteDeterministic(domain, MapExecutionLimits{MaxFirings: 8, MaxRangeEvents: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(privateResult.Range.ByName("Secret")) != 1 {
		t.Fatal("map did not generate the private action allowed by its range interface")
	}
}

func TestEventPatternMapAgentConsumptionAndRuleSharing(t *testing.T) {
	domainInterface := Interface("Domain").OutAction("A", P("n", "Integer")).Build()
	rangeInterface := Interface("Range").OutAction("X", P("n", "Integer")).OutAction("Y", P("n", "Integer")).Build()
	mapping := NewEventPatternMap("sharing").FromObject("domain", domainInterface).ToInterface(rangeInterface).
		AddRule(MappingRule("x").On(pattern.MatchEvent("A").BindParam("n", pattern.Var("n").WithType("Integer"))).Emit("X", BindingParam("n", "n")).Build()).
		AddRule(MappingRule("y").On(pattern.MatchEvent("A").BindParam("n", pattern.Var("n").WithType("Integer"))).Emit("Y", BindingParam("n", "n")).Build()).Build()
	domain := gorapide.NewPoset()
	first := deterministicMapDomainEvent(t, "A", "first", nil, map[string]any{"n": 1})
	second := deterministicMapDomainEvent(t, "A", "second", nil, map[string]any{"n": 2})
	if err := domain.AddEvent(first); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEvent(second); err != nil {
		t.Fatal(err)
	}
	result, err := mapping.ExecuteDeterministic(domain, MapExecutionLimits{MaxFirings: 8, MaxRangeEvents: 8})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.Range.ByName("X")); got != 2 {
		t.Fatalf("X outputs = %d, want 2", got)
	}
	if got := len(result.Range.ByName("Y")); got != 2 {
		t.Fatalf("Y outputs = %d, want 2", got)
	}
	if len(result.Artifact.Firings) != 4 {
		t.Fatalf("firings = %d, want 4", len(result.Artifact.Firings))
	}
}

func TestEventPatternMapCanBindRapideTimingPrimitive(t *testing.T) {
	domainInterface := Interface("Domain").OutAction("A").Build()
	rangeInterface := Interface("Range").OutAction("Elapsed", P("ticks", "Integer")).Build()
	mapping := NewEventPatternMap("timed-view").FromObject("domain", domainInterface).ToInterface(rangeInterface).
		AddRule(MappingRule("elapsed").
			On(pattern.Timing(pattern.MatchEvent("A"), pattern.Var("T"), pattern.Var("D"), "mission")).
			Emit("Elapsed", BindingParam("ticks", "D")).Build()).Build()
	domain := gorapide.NewPoset()
	event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: "domain-model", Instance: "domain", Action: "A", Occurrence: "a",
		Timings: []gorapide.EventTiming{{Clock: "mission", Start: 10, Finish: 25}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	result, err := mapping.ExecuteDeterministic(domain, MapExecutionLimits{MaxFirings: 2, MaxRangeEvents: 2})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := onlyMapEvent(t, result.Range.ByName("Elapsed"))
	if got := elapsed.ParamInt("ticks"); got != 15 {
		t.Fatalf("mapped duration = %d, want 15", got)
	}
	if result.Artifact.RangePoset.Format != gorapide.CanonicalPosetFormat {
		t.Fatalf("untimed range should retain canonical v1, got %s", result.Artifact.RangePoset.Format)
	}
}

func TestEventPatternMapCanonicalReplayAndDeclarationOrder(t *testing.T) {
	domainInterface := Interface("Domain").OutAction("A").OutAction("B").Build()
	rangeInterface := Interface("Range").OutAction("X").OutAction("Y").Build()
	ruleA := MappingRule("a").On(pattern.MatchEvent("A")).Emit("X").Build()
	ruleB := MappingRule("b").On(pattern.MatchEvent("B")).Emit("Y").Build()
	left := NewEventPatternMap("canonical").FromObject("domain", domainInterface).ToInterface(rangeInterface).AddRule(ruleA).AddRule(ruleB).Build()
	right := NewEventPatternMap("canonical").FromObject("domain", domainInterface).ToInterface(rangeInterface).AddRule(ruleB).AddRule(ruleA).Build()
	leftModel, err := left.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	rightModel, err := right.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftModel, rightModel) {
		t.Fatal("mapping rule declaration order changed canonical model identity")
	}
	domain := gorapide.NewPoset()
	a := deterministicMapDomainEvent(t, "A", "a", nil, nil)
	b := deterministicMapDomainEvent(t, "B", "b", nil, nil)
	if err := domain.AddEvent(b); err != nil {
		t.Fatal(err)
	}
	if err := domain.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	limits := MapExecutionLimits{MaxFirings: 8, MaxRangeEvents: 8}
	leftResult, err := left.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	rightResult, err := right.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	leftBytes, err := leftResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCanonicalMapExecutionArtifact(leftBytes)
	if err != nil {
		t.Fatal(err)
	}
	parsedBytes, err := parsed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, parsedBytes) {
		t.Fatal("parsed artifact did not retain canonical bytes")
	}
	rightBytes, err := rightResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatal("mapping rule declaration order changed execution artifact")
	}
	shuffledDomain := gorapide.NewPoset()
	if err := shuffledDomain.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	if err := shuffledDomain.AddEvent(b); err != nil {
		t.Fatal(err)
	}
	shuffledResult, err := left.ExecuteDeterministic(shuffledDomain, limits)
	if err != nil {
		t.Fatal(err)
	}
	shuffledBytes, err := shuffledResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, shuffledBytes) {
		t.Fatal("independent domain insertion order changed map execution artifact")
	}
	digest, err := leftResult.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := left.ReplayDeterministic(domain, limits, digest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, replayedBytes) {
		t.Fatal("map replay was not byte-identical")
	}
	if _, err := left.ReplayDeterministic(domain, limits, "sha256:wrong"); !errors.Is(err, ErrMapReplayMismatch) {
		t.Fatalf("got %v, want replay mismatch", err)
	}
	tampered := leftResult.Artifact
	tampered.Firings = append([]MapFiringRecord(nil), tampered.Firings...)
	tampered.Firings[0].FiringID = "mapfire1-tampered"
	if _, err := tampered.MarshalCanonical(); !errors.Is(err, ErrInvalidEventPatternMap) {
		t.Fatalf("got %v, want tampered artifact rejection", err)
	}
}

func TestEventPatternMapEvaluatesRangeConstraints(t *testing.T) {
	domainInterface := Interface("Domain").OutAction("A").Build()
	rangeInterface := Interface("Range").OutAction("Forbidden").Build()
	set := constraint.NewConstraintSet("range-policy")
	set.Add(constraint.NewConstraint("no-forbidden").MustNever("never", pattern.MatchEvent("Forbidden"), "forbidden range event").Build())
	mapping := NewEventPatternMap("conformance").FromObject("domain", domainInterface).ToInterface(rangeInterface).
		AddRule(MappingRule("map").On(pattern.MatchEvent("A")).Emit("Forbidden").Build()).WithRangeConstraints(set).Build()
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(deterministicMapDomainEvent(t, "A", "a", nil, nil)); err != nil {
		t.Fatal(err)
	}
	result, err := mapping.ExecuteDeterministic(domain, MapExecutionLimits{MaxFirings: 4, MaxRangeEvents: 4})
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifact.Constraints == nil || result.Artifact.Constraints.Passed {
		t.Fatal("expected canonical range constraint failure")
	}
}

func TestEventPatternMapRejectsUnsupportedOrMalformedDeclarations(t *testing.T) {
	domainInterface := Interface("Domain").OutAction("A", P("n", "Integer")).Build()
	rangeInterface := Interface("Range").OutAction("X", P("n", "Integer")).Build()
	tests := []struct {
		name    string
		mapDecl *EventPatternMap
	}{
		{"pipe rule", NewEventPatternMap("bad").FromObject("domain", domainInterface).ToInterface(rangeInterface).AddRule(Rule("r").On(pattern.MatchEvent("A")).Pipe().Emit("X", LiteralParam("n", 1)).Build()).Build()},
		{"wrong domain action", NewEventPatternMap("bad").FromObject("domain", domainInterface).ToInterface(rangeInterface).AddRule(MappingRule("r").On(pattern.MatchEvent("Missing")).Emit("X", LiteralParam("n", 1)).Build()).Build()},
		{"wrong qualifier", NewEventPatternMap("bad").FromObject("domain", domainInterface).ToInterface(rangeInterface).AddRule(MappingRule("r").On(pattern.MatchEvent("A").WhereSource("other")).Emit("X", LiteralParam("n", 1)).Build()).Build()},
		{"wrong filter type", NewEventPatternMap("bad").FromObject("domain", domainInterface).ToInterface(rangeInterface).AddRule(MappingRule("r").On(pattern.MatchEvent("A").WhereParam("n", "not-an-integer")).Emit("X", LiteralParam("n", 1)).Build()).Build()},
		{"map state", NewEventPatternMap("bad").FromObject("domain", domainInterface).ToInterface(rangeInterface).AddRule(MappingRule("r").On(pattern.MatchEvent("A")).Assign(AssignState("count", LiteralValue(1))).Emit("X", LiteralParam("n", 1)).Build()).Build()},
		{"wrong range action", NewEventPatternMap("bad").FromObject("domain", domainInterface).ToInterface(rangeInterface).AddRule(MappingRule("r").On(pattern.MatchEvent("A")).Emit("Missing").Build()).Build()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.mapDecl.DeterministicModelDigest(); !errors.Is(err, ErrInvalidEventPatternMap) {
				t.Fatalf("got %v, want invalid event-pattern map", err)
			}
		})
	}
}

func TestEventPatternMapLimitsFailExplicitly(t *testing.T) {
	domainInterface := Interface("Domain").OutAction("A").Build()
	rangeInterface := Interface("Range").OutAction("X").Build()
	mapping := NewEventPatternMap("limits").FromObject("domain", domainInterface).ToInterface(rangeInterface).
		AddRule(MappingRule("r").On(pattern.MatchEvent("A")).Emit("X").Build()).Build()
	domain := gorapide.NewPoset()
	if err := domain.AddEvent(deterministicMapDomainEvent(t, "A", "a", nil, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := mapping.ExecuteDeterministic(domain, MapExecutionLimits{MaxFirings: 1, MaxRangeEvents: 1}); !errors.Is(err, ErrMapExecutionLimit) {
		t.Fatalf("got %v, want explicit range event limit", err)
	}
}

func deterministicMapDomainEvent(t *testing.T, action, occurrence string, causes []gorapide.EventID, params map[string]any) *gorapide.Event {
	t.Helper()
	event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: CompatibilityProfile, Model: "domain-model", Instance: "domain",
		Action: action, Occurrence: occurrence, Causes: causes,
	}, params)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func onlyMapEvent(t *testing.T, events gorapide.EventSet) *gorapide.Event {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	return events[0]
}
