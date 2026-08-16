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

func TestPreparedEntryPointsRejectNilOrIncompleteSnapshots(t *testing.T) {
	architectureCalls := []struct {
		name string
		call func(*PreparedArchitecture) error
	}{
		{name: "model bytes", call: func(prepared *PreparedArchitecture) error { _, err := prepared.MarshalCanonicalModel(); return err }},
		{name: "digest", call: func(prepared *PreparedArchitecture) error { _, err := prepared.DeterministicModelDigest(); return err }},
		{name: "execute", call: func(prepared *PreparedArchitecture) error {
			_, err := prepared.ExecuteDeterministic(ExecutionJournal{})
			return err
		}},
		{name: "replay", call: func(prepared *PreparedArchitecture) error {
			_, err := prepared.ReplayDeterministic(ExecutionJournal{}, "")
			return err
		}},
		{name: "explore", call: func(prepared *PreparedArchitecture) error {
			_, err := prepared.ExploreDeterministic(ExecutionJournal{}, ExplorationLimits{})
			return err
		}},
	}
	for _, prepared := range []*PreparedArchitecture{nil, {}} {
		for _, test := range architectureCalls {
			if err := test.call(prepared); !errors.Is(err, ErrInvalidPreparedArchitecture) {
				t.Fatalf("architecture %s error=%v, want ErrInvalidPreparedArchitecture", test.name, err)
			}
		}
	}

	mapCalls := []struct {
		name string
		call func(*PreparedEventPatternMap) error
	}{
		{name: "model bytes", call: func(prepared *PreparedEventPatternMap) error { _, err := prepared.MarshalCanonicalModel(); return err }},
		{name: "digest", call: func(prepared *PreparedEventPatternMap) error {
			_, err := prepared.DeterministicModelDigest()
			return err
		}},
		{name: "execute", call: func(prepared *PreparedEventPatternMap) error {
			_, err := prepared.ExecuteDeterministic(nil, MapExecutionLimits{})
			return err
		}},
		{name: "execute request", call: func(prepared *PreparedEventPatternMap) error {
			_, err := prepared.ExecuteDeterministicRequest(nil, MapExecutionRequest{})
			return err
		}},
		{name: "replay", call: func(prepared *PreparedEventPatternMap) error {
			_, err := prepared.ReplayDeterministic(nil, MapExecutionLimits{}, "")
			return err
		}},
		{name: "replay request", call: func(prepared *PreparedEventPatternMap) error {
			_, err := prepared.ReplayDeterministicRequest(nil, MapExecutionRequest{}, "")
			return err
		}},
		{name: "explore", call: func(prepared *PreparedEventPatternMap) error {
			_, err := prepared.ExploreDeterministic(nil, MapExecutionRequest{}, ExplorationLimits{})
			return err
		}},
	}
	for _, prepared := range []*PreparedEventPatternMap{nil, {}} {
		for _, test := range mapCalls {
			if err := test.call(prepared); !errors.Is(err, ErrInvalidPreparedEventPatternMap) {
				t.Fatalf("map %s error=%v, want ErrInvalidPreparedEventPatternMap", test.name, err)
			}
		}
	}
}

func TestPreparedArchitectureOwnsExecutableModelState(t *testing.T) {
	architecture := deterministicTestArchitecture(t, false)
	source := architecture.components["source"]
	source.Interface.Actions = append(source.Interface.Actions, ActionDecl{Name: "Derived", Kind: OutAction})
	ruleTrigger := pattern.MatchEvent("Input")
	if err := source.AddDeclarativeRule(Rule("derive").On(ruleTrigger).Emit("Derived").Build()); err != nil {
		t.Fatal(err)
	}
	forbidden := pattern.MatchEvent("Forbidden")
	declaration := constraint.NewConstraint("forbidden").
		MustNever("absent", forbidden, "Forbidden must be absent").Build()
	set := constraint.NewConstraintSet("sealed-policy")
	set.Add(declaration)
	architecture.WithConstraints(set, constraint.CheckOnEvent)

	prepared, err := architecture.PrepareDeterministic()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := prepared.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	modelBytes, err := prepared.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	expectedModelBytes := append([]byte(nil), modelBytes...)
	modelBytes[0] = '!'
	freshModelBytes, err := prepared.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(freshModelBytes, expectedModelBytes) {
		t.Fatal("mutating returned architecture model bytes changed the prepared snapshot")
	}
	delegatedModelBytes, err := architecture.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(delegatedModelBytes, expectedModelBytes) {
		t.Fatal("architecture convenience entry point did not delegate to equivalent prepared model bytes")
	}
	journal := NewExecutionJournal(digest, 100,
		InputEvent{Key: "one", Source: "source", Action: "Input", Params: map[string]any{"n": 1}},
		InputEvent{Key: "two", Source: "source", Action: "Input", Params: map[string]any{"n": 2}},
	)
	before, err := prepared.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := before.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	delegated, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	delegatedBytes, err := delegated.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(delegatedBytes, beforeBytes) {
		t.Fatal("architecture convenience execution did not match prepared execution")
	}
	artifactDigest, err := before.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	limits := ExplorationLimits{MaxExecutions: 8, MaxChoiceDepth: 4}
	beforeExploration, err := prepared.ExploreDeterministic(journal, limits)
	if err != nil {
		t.Fatal(err)
	}
	beforeExplorationBytes, err := beforeExploration.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	// Results are fresh per execution. Mutating a previously returned result
	// must not reach back into the prepared model or a later execution.
	before.ModelDigest = "mutated-result-model"
	if len(before.Firings) != 0 {
		before.Firings[0].Transition = "mutated-result-firing"
	}

	// Mutate every caller-owned category exercised by this model after sealing:
	// architecture metadata, boundary and component interfaces, a connection
	// trigger and output, and the constraint declaration and set.
	architecture.Name = "mutated-architecture"
	architecture.returnInterface.Name = "MutatedRoot"
	source.Interface.Actions[0].Name = "MutatedInput"
	architecture.connections[0].ActionName = "MutatedOutput"
	architecture.connections[0].Trigger.(*pattern.BasicPattern).WhereSource("other")
	source.transitions[0].ID = "mutated-rule"
	source.transitions[0].Body.Outputs[0].Action = "MutatedDerived"
	ruleTrigger.WhereSource("other")
	set.Name = "mutated-policy"
	declaration.Name = "mutated-constraint"
	forbidden.WhereSource("other")

	stableDigest, err := prepared.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if stableDigest != digest {
		t.Fatalf("prepared digest changed after source mutation: %s != %s", stableDigest, digest)
	}
	stableModelBytes, err := prepared.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stableModelBytes, expectedModelBytes) {
		t.Fatalf("prepared architecture model bytes changed after source mutation:\nbefore=%s\nafter=%s", expectedModelBytes, stableModelBytes)
	}
	after, err := prepared.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, err := after.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("prepared execution changed after source mutation:\nbefore=%s\nafter=%s", beforeBytes, afterBytes)
	}
	if _, err := prepared.ReplayDeterministic(journal, artifactDigest); err != nil {
		t.Fatalf("prepared replay changed after source mutation: %v", err)
	}
	afterExploration, err := prepared.ExploreDeterministic(journal, limits)
	if err != nil {
		t.Fatal(err)
	}
	afterExplorationBytes, err := afterExploration.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeExplorationBytes, afterExplorationBytes) {
		t.Fatalf("prepared exploration changed after source mutation:\nbefore=%s\nafter=%s", beforeExplorationBytes, afterExplorationBytes)
	}
	if sourceDigest, sourceErr := architecture.DeterministicModelDigest(); sourceErr == nil && sourceDigest == digest {
		t.Fatal("source mutation was not observable at the source preparation boundary")
	}

	assertConcurrentPreparedArchitectureBytes(t, prepared, journal, beforeBytes)
}

func assertConcurrentPreparedArchitectureBytes(
	t *testing.T,
	prepared *PreparedArchitecture,
	journal ExecutionJournal,
	want []byte,
) {
	t.Helper()
	type outcome struct {
		data []byte
		err  error
	}
	const executions = 24
	results := make(chan outcome, executions)
	for index := 0; index < executions; index++ {
		go func() {
			result, err := prepared.ExecuteDeterministic(journal)
			if err != nil {
				results <- outcome{err: err}
				return
			}
			data, err := result.MarshalCanonical()
			results <- outcome{data: data, err: err}
		}()
	}
	for index := 0; index < executions; index++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent execution %d: %v", index, result.err)
		}
		if !bytes.Equal(result.data, want) {
			t.Fatalf("concurrent execution %d produced different artifact", index)
		}
	}
}

func TestPreparedEventPatternMapOwnsExecutableModelState(t *testing.T) {
	domainInterface := Interface("Domain").OutAction("Observed", P("n", "Integer")).Build()
	rangeInterface := Interface("Range").OutAction("Abstracted", P("n", "Integer")).Build()
	value := pattern.Var("n").WithType("Integer")
	trigger := pattern.MatchEvent("Observed").WhereSource("domain").BindParam("n", value)
	rule := MappingRule("observed").On(trigger).
		Emit("Abstracted", BindingParam("n", "n")).Build()
	forbidden := pattern.MatchEvent("Forbidden")
	declaration := constraint.NewConstraint("range-policy").
		MustNever("absent", forbidden, "Forbidden must be absent").Build()
	set := constraint.NewConstraintSet("range-constraints")
	set.Add(declaration)
	mapping := NewEventPatternMap("sealed-map").
		FromObject("domain", domainInterface).
		ToInterface(rangeInterface).
		AddRule(rule).
		WithRangeConstraints(set).
		Build()

	prepared, err := mapping.PrepareDeterministic()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := prepared.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	modelBytes, err := prepared.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	expectedModelBytes := append([]byte(nil), modelBytes...)
	modelBytes[0] = '!'
	freshModelBytes, err := prepared.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(freshModelBytes, expectedModelBytes) {
		t.Fatal("mutating returned map model bytes changed the prepared snapshot")
	}
	delegatedModelBytes, err := mapping.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(delegatedModelBytes, expectedModelBytes) {
		t.Fatal("map convenience entry point did not delegate to equivalent prepared model bytes")
	}
	domain := gorapide.NewPoset()
	event := deterministicMapDomainEvent(t, "Observed", "observed", nil, map[string]any{"n": 7})
	if err := domain.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	limits := MapExecutionLimits{MaxFirings: 8, MaxRangeEvents: 8}
	before, err := prepared.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := before.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	delegated, err := mapping.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	delegatedBytes, err := delegated.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(delegatedBytes, beforeBytes) {
		t.Fatal("map convenience execution did not match prepared execution")
	}
	artifactDigest, err := before.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	before.Artifact.ModelDigest = "mutated-result-model"
	if len(before.Artifact.Firings) != 0 {
		before.Artifact.Firings[0].RuleID = "mutated-result-firing"
	}

	mapping.Name = "mutated-map"
	mapping.DomainID = "other"
	domainInterface.Actions[0].Name = "MutatedObserved"
	rangeInterface.Actions[0].Name = "MutatedAbstracted"
	mapping.Rules[0].ID = "mutated-rule"
	mapping.Rules[0].Body.Outputs[0].Action = "MutatedAbstracted"
	trigger.WhereSource("other")
	set.Name = "mutated-range-constraints"
	declaration.Name = "mutated-range-policy"
	forbidden.WhereSource("other")

	stableDigest, err := prepared.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if stableDigest != digest {
		t.Fatalf("prepared map digest changed after source mutation: %s != %s", stableDigest, digest)
	}
	stableModelBytes, err := prepared.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stableModelBytes, expectedModelBytes) {
		t.Fatalf("prepared map model bytes changed after source mutation:\nbefore=%s\nafter=%s", expectedModelBytes, stableModelBytes)
	}
	after, err := prepared.ExecuteDeterministic(domain, limits)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, err := after.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("prepared map execution changed after source mutation:\nbefore=%s\nafter=%s", beforeBytes, afterBytes)
	}
	if _, err := prepared.ReplayDeterministic(domain, limits, artifactDigest); err != nil {
		t.Fatalf("prepared map replay changed after source mutation: %v", err)
	}
	if sourceDigest, sourceErr := mapping.DeterministicModelDigest(); sourceErr == nil && sourceDigest == digest {
		t.Fatal("map source mutation was not observable at the source preparation boundary")
	}

	assertConcurrentPreparedMapBytes(t, prepared, domain, limits, beforeBytes)
}

func TestClonedBehaviorConformanceOwnsPreparedShadowArchitecture(t *testing.T) {
	iface := Interface("Protocol").InAction("Request").OutAction("Response").Build()
	trigger := pattern.MatchEvent("Request")
	checker, err := NewBehaviorConformanceConstraint(
		"protocol-behavior", "service.protocol", iface,
		[]*DeclarativeRule{Rule("reply").On(trigger).Emit("Response").Build()},
		"sha256:0000000000000000000000000000000000000000000000000000000000000001",
	)
	if err != nil {
		t.Fatal(err)
	}
	cloned, err := cloneBehaviorConformanceConstraint(checker)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := cloned.DeterministicDigest()
	if err != nil {
		t.Fatal(err)
	}
	shadowDigest, err := cloned.preparedSpecification.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	shadowBytes, err := cloned.preparedSpecification.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(shadowDigest, 16,
		InputEvent{Key: "request", Source: behaviorEnvironmentComponent, Action: "Request"},
	)
	before, err := cloned.preparedSpecification.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := before.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}

	checker.Name = "mutated-behavior"
	checker.Actions[0].Qualified = "mutated.path"
	checker.specification.Name = "mutated-shadow"
	subject := checker.specification.components[behaviorSubjectComponent]
	subject.Interface.Actions[0].Name = "MutatedRequest"
	subject.transitions[0].Body.Outputs[0].Action = "MutatedResponse"
	trigger.WhereSource("other")

	stableDigest, err := cloned.DeterministicDigest()
	if err != nil {
		t.Fatal(err)
	}
	if stableDigest != digest {
		t.Fatalf("cloned behavior constraint digest changed after source mutation: %s != %s", stableDigest, digest)
	}
	stableShadowBytes, err := cloned.preparedSpecification.MarshalCanonicalModel()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stableShadowBytes, shadowBytes) {
		t.Fatalf("prepared behavior shadow changed after source mutation:\nbefore=%s\nafter=%s", shadowBytes, stableShadowBytes)
	}
	after, err := cloned.preparedSpecification.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, err := after.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterBytes, beforeBytes) {
		t.Fatalf("prepared behavior shadow execution changed after source mutation:\nbefore=%s\nafter=%s", beforeBytes, afterBytes)
	}
}

func assertConcurrentPreparedMapBytes(
	t *testing.T,
	prepared *PreparedEventPatternMap,
	domain *gorapide.Poset,
	limits MapExecutionLimits,
	want []byte,
) {
	t.Helper()
	type outcome struct {
		data []byte
		err  error
	}
	const executions = 24
	results := make(chan outcome, executions)
	for index := 0; index < executions; index++ {
		go func() {
			result, err := prepared.ExecuteDeterministic(domain, limits)
			if err != nil {
				results <- outcome{err: err}
				return
			}
			data, err := result.MarshalCanonical()
			results <- outcome{data: data, err: err}
		}()
	}
	for index := 0; index < executions; index++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent map execution %d: %v", index, result.err)
		}
		if !bytes.Equal(result.data, want) {
			t.Fatalf("concurrent map execution %d produced different artifact", index)
		}
	}
}

type unsupportedCanonicalChecker struct{}

func (unsupportedCanonicalChecker) Check(*gorapide.Poset) []constraint.ConstraintViolation {
	return nil
}

func (unsupportedCanonicalChecker) CanonicalName() string { return "unsupported" }

func (unsupportedCanonicalChecker) DeterministicDigest() (string, error) {
	return "sha256:0000000000000000000000000000000000000000000000000000000000000000", nil
}

func (unsupportedCanonicalChecker) EvaluateCanonical(
	pattern.PosetReader,
) (constraint.CanonicalConstraintReport, error) {
	return constraint.CanonicalConstraintReport{}, nil
}

func TestPrepareDeterministicRejectsUncloneableCanonicalChecker(t *testing.T) {
	set := constraint.NewConstraintSet("unsupported")
	set.Add(unsupportedCanonicalChecker{})
	architecture := NewArchitecture("unsupported-checker").WithConstraints(set, constraint.CheckOnEvent)
	_, err := architecture.PrepareDeterministic()
	if err == nil || !strings.Contains(err.Error(), "unsupported canonical checker type") {
		t.Fatalf("error=%v, want explicit unsupported canonical checker boundary", err)
	}
}
