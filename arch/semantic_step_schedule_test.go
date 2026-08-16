package arch

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func sameDeadlineSemanticStepArchitecture(t *testing.T) *Architecture {
	t.Helper()
	architecture := NewArchitecture("same-deadline-semantic-step")
	worker := NewComponent("A", Interface("Worker").
		OutAction("Start").InAction("Arrived").
		OutAction("Resumed").OutAction("Seen").Build(), nil)
	if err := worker.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := worker.DeclareState(StateReference("flag", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	if err := worker.SetModuleProcessMode(ParallelProcesses); err != nil {
		t.Fatal(err)
	}
	pauser := Process("pauser").StartAt("wait").States(
		AwaitState("wait", Await("start").On(pattern.MatchEvent("Start")).Do(
			PauseFor("C", 1),
			SetState("flag", LiteralValue(1)),
			CallAction("resumed", "Resumed"),
		).Terminate().Build()),
	).Build()
	observer := Process("observer").StartAt("wait").States(
		AwaitState("wait", Await("arrived").On(pattern.MatchEvent("Arrived")).
			Where(EqualValues(ReadState("flag"), LiteralValue(1))).
			Do(CallAction("seen", "Seen")).Terminate().Build()),
	).Build()
	for _, process := range []*DeclarativeProcess{pauser, observer} {
		if err := worker.AddDeclarativeProcess(process); err != nil {
			t.Fatal(err)
		}
	}
	sender := NewComponent("B", Interface("Sender").OutAction("Tick").Build(), nil)
	for _, component := range []*Component{worker, sender} {
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	if err := architecture.AddConnection(
		Connect("B", "A").IdentifiedBy("tick-to-arrived").
			On(pattern.MatchEvent("Tick")).Agent().Send("Arrived").Build(),
	); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func sameDeadlineSemanticStepJournal(t *testing.T, architecture *Architecture) ExecutionJournal {
	t.Helper()
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	clock := ClockID("A", "C")
	return NewExecutionJournal(digest, 30,
		InputEvent{Key: "start", Source: "A", Action: "Start"},
		InputEvent{Key: "tick", Source: "B", Action: "Tick",
			Timings: []gorapide.EventTiming{{Clock: clock, Start: 1, Finish: 1}}},
	)
}

func scheduledFirstSemanticResume(t *testing.T, architecture *Architecture, journal ExecutionJournal) ExecutionJournal {
	t.Helper()
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Choices) == 0 || result.Choices[0].Domain != "semantic-step" {
		t.Fatalf("same-deadline alternatives lack a semantic-step witness: %#v", result.Choices)
	}
	journal.Choices = []ChoiceDecision{{
		Point: result.Choices[0].Point, Selection: "resume",
	}}
	return journal
}

func TestSameDeadlineObservationAndContinuationAreReplayableSemanticAlternatives(t *testing.T) {
	architecture := sameDeadlineSemanticStepArchitecture(t)
	journal := sameDeadlineSemanticStepJournal(t, architecture)
	observedFirst, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(observedFirst.Poset.ByName("Seen")) != 0 {
		t.Fatal("canonical observe-first schedule did not preserve Arrived's flag=0 generation snapshot")
	}
	resumeJournal := scheduledFirstSemanticResume(t, architecture, journal)
	resumedFirst, err := architecture.ExecuteDeterministic(resumeJournal)
	if err != nil {
		t.Fatal(err)
	}
	if resumedFirst.SemanticStepPolicy != SemanticStepPolicy {
		t.Fatalf("semantic-step policy=%q, want %q", resumedFirst.SemanticStepPolicy, SemanticStepPolicy)
	}
	if len(resumedFirst.Poset.ByName("Seen")) != 1 {
		t.Fatal("scheduled resume-first execution did not expose the flag=1 Arrived snapshot")
	}
	tick := resumedFirst.Poset.ByName("Tick")[0]
	resumed := resumedFirst.Poset.ByName("Resumed")[0]
	if resumedFirst.Poset.IsCausallyBefore(tick.ID, resumed.ID) ||
		resumedFirst.Poset.IsCausallyBefore(resumed.ID, tick.ID) {
		t.Fatal("semantic scheduling introduced false causality between same-deadline independent events")
	}
	expected, err := resumedFirst.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(resumeJournal, expected)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := resumedFirst.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("same-deadline resume schedule did not replay byte-identically")
	}
}

func TestExplorationEnumeratesSameDeadlineObservationAndContinuationOutcomes(t *testing.T) {
	architecture := sameDeadlineSemanticStepArchitecture(t)
	journal := sameDeadlineSemanticStepJournal(t, architecture)
	result, err := architecture.ExploreDeterministic(journal, ExplorationLimits{
		MaxExecutions: 32, MaxChoiceDepth: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || len(result.Computations) != 2 {
		t.Fatalf("same-deadline exploration produced %d computations, complete=%v; want two", len(result.Computations), result.Complete)
	}
	seenCounts := make(map[int]bool)
	for _, computation := range result.Computations {
		seenCounts[len(computation.Result.Poset.ByName("Seen"))] = true
	}
	if !seenCounts[0] || !seenCounts[1] {
		t.Fatalf("same-deadline exploration missed observe-first or resume-first outcome: %#v", seenCounts)
	}
}

func TestSameDeadlineSemanticSchedulingStableAcrossGOMAXPROCS(t *testing.T) {
	architecture := sameDeadlineSemanticStepArchitecture(t)
	base := sameDeadlineSemanticStepJournal(t, architecture)
	resume := scheduledFirstSemanticResume(t, architecture, base)
	prior := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(prior)
	for name, journal := range map[string]ExecutionJournal{"observe": base, "resume": resume} {
		var baseline []byte
		for _, processors := range []int{1, 8} {
			runtime.GOMAXPROCS(processors)
			for iteration := 0; iteration < 10; iteration++ {
				result, err := architecture.ExecuteDeterministic(journal)
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := result.MarshalCanonical()
				if err != nil {
					t.Fatal(err)
				}
				if baseline == nil {
					baseline = encoded
				} else if !bytes.Equal(baseline, encoded) {
					t.Fatalf("%s-first artifact changed at GOMAXPROCS=%d iteration=%d", name, processors, iteration)
				}
			}
		}
	}
}
