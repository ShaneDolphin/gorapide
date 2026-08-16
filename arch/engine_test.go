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

func deterministicTestArchitecture(t *testing.T, reverse bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("deterministic-connections")
	components := []*Component{
		NewComponent("source", Interface("Source").OutAction("Input", P("n", "Integer")).Build(), nil, WithBufferSize(0)),
		NewComponent("basic", Interface("BasicTarget").InAction("BasicOut", P("n", "Integer")).Build(), nil, WithBufferSize(0)),
		NewComponent("pipe", Interface("PipeTarget").InAction("PipeOut", P("n", "Integer")).Build(), nil, WithBufferSize(0)),
		NewComponent("agent", Interface("AgentTarget").InAction("AgentOut", P("n", "Integer")).Build(), nil, WithBufferSize(0)),
	}
	if reverse {
		for left, right := 0, len(components)-1; left < right; left, right = left+1, right-1 {
			components[left], components[right] = components[right], components[left]
		}
	}
	for _, component := range components {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}

	connections := []*Connection{
		Connect("source", "basic").IdentifiedBy("connection/basic").On(pattern.MatchEvent("Input")).Send("BasicOut").Build(),
		Connect("source", "pipe").IdentifiedBy("connection/pipe").On(pattern.MatchEvent("Input")).Pipe().Send("PipeOut").Build(),
		Connect("source", "agent").IdentifiedBy("connection/agent").On(pattern.MatchEvent("Input")).Agent().Send("AgentOut").Build(),
	}
	if reverse {
		for left, right := 0, len(connections)-1; left < right; left, right = left+1, right-1 {
			connections[left], connections[right] = connections[right], connections[left]
		}
	}
	for _, connection := range connections {
		if err := architecture.AddConnection(connection); err != nil {
			t.Fatal(err)
		}
	}
	return architecture
}

func deterministicTestJournal(t *testing.T, architecture *Architecture, reverse bool) ExecutionJournal {
	t.Helper()
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	inputs := []InputEvent{
		{Key: "input/one", Source: "source", Action: "Input", Params: map[string]any{"n": int(1)}},
		{Key: "input/two", Source: "source", Action: "Input", Params: map[string]any{"n": int64(2)}},
	}
	if reverse {
		inputs[0], inputs[1] = inputs[1], inputs[0]
	}
	return NewExecutionJournal(digest, 100, inputs...)
}

func TestDeterministicModelDigestIgnoresDeclarationOrder(t *testing.T) {
	forward, err := deterministicTestArchitecture(t, false).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := deterministicTestArchitecture(t, true).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if forward != reverse {
		t.Fatalf("declaration order changed model digest: %s != %s", forward, reverse)
	}
}

func TestExecutionJournalCanonicalizesInputAndCauseOrder(t *testing.T) {
	architecture := deterministicTestArchitecture(t, false)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	forward := NewExecutionJournal(digest, 10,
		InputEvent{Key: "a", Source: "source", Action: "Input", Params: map[string]any{"n": int(1)}},
		InputEvent{Key: "b", Source: "source", Action: "Input", Params: map[string]any{"n": int(2)}},
		InputEvent{Key: "c", Source: "source", Action: "Input", Params: map[string]any{"n": int(3)}, Causes: []string{"b", "a"}},
	)
	reverse := NewExecutionJournal(digest, 10,
		InputEvent{Key: "c", Source: "source", Action: "Input", Params: map[string]any{"n": int64(3)}, Causes: []string{"a", "b", "a"}},
		InputEvent{Key: "b", Source: "source", Action: "Input", Params: map[string]any{"n": int64(2)}},
		InputEvent{Key: "a", Source: "source", Action: "Input", Params: map[string]any{"n": int64(1)}},
	)
	left, err := forward.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	right, err := reverse.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatalf("journal ordering changed canonical bytes:\nleft=%s\nright=%s", left, right)
	}
}

func TestExecutionJournalPreservesTypedValuesAndRoundTrips(t *testing.T) {
	architecture := deterministicTestArchitecture(t, false)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	integer := NewExecutionJournal(digest, 10, InputEvent{
		Key: "one", Source: "source", Action: "Input", Params: map[string]any{"n": int64(1)},
	})
	floating := NewExecutionJournal(digest, 10, InputEvent{
		Key: "one", Source: "source", Action: "Input", Params: map[string]any{"n": float64(1)},
	})
	integerBytes, err := integer.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	floatingBytes, err := floating.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(integerBytes, floatingBytes) {
		t.Fatal("journal canonicalization erased numeric type")
	}
	restored, err := ParseExecutionJournal(integerBytes)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := restored.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(integerBytes, roundTrip) {
		t.Fatalf("journal round trip changed bytes:\nbefore=%s\nafter=%s", integerBytes, roundTrip)
	}
	if _, ok := restored.Inputs[0].Params["n"].(int64); !ok {
		t.Fatalf("journal round trip changed integer type to %T", restored.Inputs[0].Params["n"])
	}
}

func TestExecutionJournalCanonicalizesAndRoundTripsRapideTiming(t *testing.T) {
	architecture := deterministicTestArchitecture(t, false)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	left := NewExecutionJournal(digest, 10, InputEvent{
		Key: "one", Source: "source", Action: "Input", Params: map[string]any{"n": 1},
		Timings: []gorapide.EventTiming{
			{Clock: "z", Start: 1, Finish: 2}, {Clock: "a", Start: ^uint64(0), Finish: ^uint64(0)},
		},
	})
	right := NewExecutionJournal(digest, 10, InputEvent{
		Key: "one", Source: "source", Action: "Input", Params: map[string]any{"n": int64(1)},
		Timings: []gorapide.EventTiming{
			{Clock: "a", Start: ^uint64(0), Finish: ^uint64(0)}, {Clock: "z", Start: 1, Finish: 2},
		},
	})
	leftBytes, err := left.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := right.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatalf("clock declaration order changed journal bytes:\n%s\n%s", leftBytes, rightBytes)
	}
	if !bytes.Contains(leftBytes, []byte(`"start":"18446744073709551615"`)) {
		t.Fatalf("journal did not encode maximum tick losslessly: %s", leftBytes)
	}
	parsed, err := ParseExecutionJournal(leftBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Inputs[0].Timings) != 2 || parsed.Inputs[0].Timings[0].Clock != "a" || parsed.Inputs[0].Timings[0].Start != ^uint64(0) {
		t.Fatalf("journal timing round trip changed intervals: %#v", parsed.Inputs[0].Timings)
	}
	roundTrip, err := parsed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, roundTrip) {
		t.Fatal("timed journal round trip changed canonical bytes")
	}
	noncanonical := bytes.Replace(leftBytes, []byte(`"start":"1"`), []byte(`"start":"01"`), 1)
	if _, err := ParseExecutionJournal(noncanonical); !errors.Is(err, ErrInvalidExecutionJournal) {
		t.Fatalf("got %v, want noncanonical journal timing rejection", err)
	}
}

func TestExecuteDeterministicTimedInputFiresTimingPatternAndReplays(t *testing.T) {
	architecture := NewArchitecture("timed-input")
	source := NewComponent("source", Interface("Source").OutAction("A").Build(), nil)
	target := NewComponent("target", Interface("Target").InAction("Elapsed", P("ticks", "Integer")).Build(), nil)
	if err := architecture.AddComponent(source); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(target); err != nil {
		t.Fatal(err)
	}
	connection := Connect("source", "target").IdentifiedBy("timed/elapsed").
		On(pattern.Timing(pattern.MatchEvent("A"), pattern.Var("T"), pattern.Var("D"), "mission")).
		Agent().SendParameters("Elapsed", ConnectionBindingParam("ticks", "D")).Build()
	if err := architecture.AddConnection(connection); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10, InputEvent{
		Key: "a", Source: "source", Action: "A",
		Timings: []gorapide.EventTiming{{Clock: "mission", Start: 10, Finish: 25}},
	})
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	inputs := result.Poset.ByName("A")
	if len(inputs) != 1 || !strings.HasPrefix(string(inputs[0].ID), "evt2-") {
		t.Fatalf("timed journal did not produce one evt2 input: %#v", inputs)
	}
	elapsed := result.Poset.ByName("Elapsed")
	if len(elapsed) != 1 || elapsed[0].ParamInt("ticks") != 15 {
		t.Fatalf("timing connection output = %#v, want duration 15", elapsed)
	}
	canonical, err := result.Poset.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Format != gorapide.CanonicalTimedPosetFormat {
		t.Fatalf("timed execution poset format = %s", canonical.Format)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedDigest, err := replayed.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	if replayedDigest != artifactDigest {
		t.Fatalf("timed replay digest = %s, want %s", replayedDigest, artifactDigest)
	}
}

func TestExecuteDeterministicRejectsTimedInputCausalityConflict(t *testing.T) {
	architecture := deterministicTestArchitecture(t, false)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10,
		InputEvent{Key: "a", Source: "source", Action: "Input", Params: map[string]any{"n": 1}, Timings: []gorapide.EventTiming{{Clock: "C", Start: 0, Finish: 10}}},
		InputEvent{Key: "b", Source: "source", Action: "Input", Params: map[string]any{"n": 2}, Causes: []string{"a"}, Timings: []gorapide.EventTiming{{Clock: "C", Start: 5, Finish: 5}}},
	)
	if _, err := architecture.ExecuteDeterministic(journal); !errors.Is(err, gorapide.ErrTimingCausality) {
		t.Fatalf("got %v, want timed input causality rejection", err)
	}
}

func TestParseExecutionJournalRejectsNoncanonicalBytes(t *testing.T) {
	architecture := deterministicTestArchitecture(t, false)
	journal := deterministicTestJournal(t, architecture, false)
	canonical, err := journal.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseExecutionJournal(append(canonical, '\n')); !errors.Is(err, ErrInvalidExecutionJournal) {
		t.Fatalf("expected ErrInvalidExecutionJournal, got %v", err)
	}
}

func TestExecuteDeterministicProducesByteIdenticalArtifact(t *testing.T) {
	forwardArchitecture := deterministicTestArchitecture(t, false)
	reverseArchitecture := deterministicTestArchitecture(t, true)
	forwardJournal := deterministicTestJournal(t, forwardArchitecture, false)
	reverseJournal := deterministicTestJournal(t, reverseArchitecture, true)

	forward, err := forwardArchitecture.ExecuteDeterministic(forwardJournal)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := reverseArchitecture.ExecuteDeterministic(reverseJournal)
	if err != nil {
		t.Fatal(err)
	}
	forwardBytes, err := forward.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	reverseBytes, err := reverse.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(forwardBytes, reverseBytes) {
		t.Fatalf("equivalent execution produced different artifacts:\nforward=%s\nreverse=%s", forwardBytes, reverseBytes)
	}
	if forward.Poset.Len() != 7 {
		t.Fatalf("want architecture Start + 2 input occurrences + 2 pipe + 2 agent = 7, got %d", forward.Poset.Len())
	}
	if len(forward.Poset.ByName("BasicOut")) != 2 {
		t.Fatal("basic target observations are missing")
	}
	pipeOutputs := forward.Poset.ByName("PipeOut")
	if len(pipeOutputs) != 2 {
		t.Fatalf("want 2 pipe outputs, got %d", len(pipeOutputs))
	}
	if !forward.Poset.IsCausallyBefore(pipeOutputs[0].ID, pipeOutputs[1].ID) &&
		!forward.Poset.IsCausallyBefore(pipeOutputs[1].ID, pipeOutputs[0].ID) {
		t.Fatal("pipe outputs must be causally ordered")
	}
	agentOutputs := forward.Poset.ByName("AgentOut")
	if len(agentOutputs) != 2 || !forward.Poset.IsCausallyIndependent(agentOutputs[0].ID, agentOutputs[1].ID) {
		t.Fatal("agent outputs must remain independent")
	}
}

func TestExecuteDeterministicIgnoresLegacyPosetAndChannelState(t *testing.T) {
	architecture := deterministicTestArchitecture(t, false)
	legacy := gorapide.NewEvent("Legacy", "source", nil)
	if err := architecture.Poset().AddEvent(legacy); err != nil {
		t.Fatal(err)
	}
	journal := deterministicTestJournal(t, architecture, false)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Legacy")) != 0 {
		t.Fatal("deterministic execution read the legacy architecture poset")
	}
	if architecture.Poset().Len() != 1 {
		t.Fatal("deterministic execution mutated the legacy architecture poset")
	}
}

func TestReplayDeterministicVerifiesArtifactDigest(t *testing.T) {
	architecture := deterministicTestArchitecture(t, false)
	journal := deterministicTestJournal(t, architecture, false)
	first, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, digest)
	if err != nil {
		t.Fatal(err)
	}
	replayedDigest, err := replayed.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	if replayedDigest != digest {
		t.Fatalf("replay digest changed: %s != %s", replayedDigest, digest)
	}
	if _, err := architecture.ReplayDeterministic(journal, "sha256:wrong"); !errors.Is(err, ErrReplayMismatch) {
		t.Fatalf("expected ErrReplayMismatch, got %v", err)
	}
}

func TestReplayDeterministicFromCanonicalJournalBytes(t *testing.T) {
	architecture := deterministicTestArchitecture(t, false)
	journal := deterministicTestJournal(t, architecture, true)
	journalBytes, err := journal.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	restoredJournal, err := ParseExecutionJournal(journalBytes)
	if err != nil {
		t.Fatal(err)
	}
	first, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(restoredJournal, digest)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("replay from canonical journal bytes did not reproduce the artifact")
	}
	for _, event := range replayed.Poset.Events() {
		if !strings.HasPrefix(string(event.ID), "evt1-") || !event.Clock.WallTime.IsZero() {
			t.Fatalf("deterministic result contains legacy identity or wall time: %#v", event)
		}
	}
}

func TestExecuteDeterministicRejectsOpaqueBehavior(t *testing.T) {
	architecture := deterministicTestArchitecture(t, false)
	component, _ := architecture.Component("basic")
	component.OnReceive(func(*Component, *gorapide.Event) {})
	_, err := architecture.DeterministicModelDigest()
	if !errors.Is(err, ErrUnsupportedDeterministicModel) {
		t.Fatalf("expected ErrUnsupportedDeterministicModel, got %v", err)
	}
}

func TestExecuteDeterministicRejectsOpaquePatternAndTransform(t *testing.T) {
	makeArchitecture := func(connection *Connection) *Architecture {
		a := NewArchitecture("unsupported")
		if err := a.AddComponent(NewComponent("source", Interface("S").OutAction("Input").Build(), nil)); err != nil {
			t.Fatal(err)
		}
		if err := a.AddComponent(NewComponent("target", Interface("T").InAction("Output").Build(), nil)); err != nil {
			t.Fatal(err)
		}
		if err := a.AddConnection(connection); err != nil {
			t.Fatal(err)
		}
		return a
	}

	opaquePattern := Connect("source", "target").IdentifiedBy("opaque-pattern").
		On(pattern.MatchEvent("Input").Where(func(*gorapide.Event) bool { return true })).Send("Output").Build()
	if _, err := makeArchitecture(opaquePattern).DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) {
		t.Fatalf("opaque pattern: expected ErrUnsupportedDeterministicModel, got %v", err)
	}

	opaqueTransform := Connect("source", "target").IdentifiedBy("opaque-transform").
		On(pattern.MatchEvent("Input")).SendWith("Output", func(*gorapide.Event) map[string]any { return nil }).Build()
	if _, err := makeArchitecture(opaqueTransform).DeterministicModelDigest(); !errors.Is(err, ErrUnsupportedDeterministicModel) {
		t.Fatalf("opaque transform: expected ErrUnsupportedDeterministicModel, got %v", err)
	}
}

func TestExecuteDeterministicEnforcesFiringLimit(t *testing.T) {
	architecture := deterministicTestArchitecture(t, false)
	journal := deterministicTestJournal(t, architecture, false)
	journal.Limits.MaxFirings = 1
	_, err := architecture.ExecuteDeterministic(journal)
	if !errors.Is(err, ErrExecutionLimit) {
		t.Fatalf("expected ErrExecutionLimit, got %v", err)
	}
}

func TestExecuteDeterministicEnforcesInitialRapideTypes(t *testing.T) {
	architecture := NewArchitecture("typed-input")
	if err := architecture.AddComponent(NewComponent("source",
		Interface("Source").OutAction("Input", P("n", "Natural")).Build(), nil)); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	negative := NewExecutionJournal(digest, 10, InputEvent{
		Key: "negative", Source: "source", Action: "Input", Params: map[string]any{"n": -1},
	})
	if _, err := architecture.ExecuteDeterministic(negative); !errors.Is(err, ErrActionTypeMismatch) {
		t.Fatalf("negative Natural: expected ErrActionTypeMismatch, got %v", err)
	}
	extra := NewExecutionJournal(digest, 10, InputEvent{
		Key: "extra", Source: "source", Action: "Input", Params: map[string]any{"n": 1, "extra": true},
	})
	if _, err := architecture.ExecuteDeterministic(extra); !errors.Is(err, ErrActionTypeMismatch) {
		t.Fatalf("extra parameter: expected ErrActionTypeMismatch, got %v", err)
	}
}

func TestDeterministicModelRejectsUnsupportedRapideType(t *testing.T) {
	architecture := NewArchitecture("unsupported-type")
	if err := architecture.AddComponent(NewComponent("source",
		Interface("Source").OutAction("Input", P("payload", "CustomRecord")).Build(), nil)); err != nil {
		t.Fatal(err)
	}
	_, err := architecture.DeterministicModelDigest()
	if !errors.Is(err, ErrUnsupportedRapideType) {
		t.Fatalf("expected ErrUnsupportedRapideType, got %v", err)
	}
}

func TestExecuteDeterministicChecksConnectionTargetType(t *testing.T) {
	architecture := NewArchitecture("target-type")
	if err := architecture.AddComponent(NewComponent("source",
		Interface("Source").OutAction("Input", P("n", "Integer")).Build(), nil)); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(NewComponent("target",
		Interface("Target").InAction("Output", P("n", "String")).Build(), nil)); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddConnection(Connect("source", "target").
		IdentifiedBy("type-mismatch").On(pattern.MatchEvent("Input")).Agent().Send("Output").Build()); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10, InputEvent{
		Key: "one", Source: "source", Action: "Input", Params: map[string]any{"n": 1},
	})
	if _, err := architecture.ExecuteDeterministic(journal); !errors.Is(err, ErrActionTypeMismatch) {
		t.Fatalf("expected ErrActionTypeMismatch, got %v", err)
	}
}

func TestExecuteDeterministicStableAcrossGOMAXPROCS(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	architecture := deterministicTestArchitecture(t, false)
	journal := deterministicTestJournal(t, architecture, false)
	first, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}

	runtime.GOMAXPROCS(8)
	second, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("GOMAXPROCS changed deterministic artifact")
	}
}
