package arch

import (
	"bytes"
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func timingRangeProcessArchitecture(t *testing.T, name string, statements ...Statement) *Architecture {
	t.Helper()
	architecture := NewArchitecture(name)
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Ranged").OutAction("Final").Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("start").On(pattern.MatchEvent("Start")).Do(statements...).Terminate().Build()),
	).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func timingRangeJournal(t *testing.T, architecture *Architecture) ExecutionJournal {
	t.Helper()
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	return NewExecutionJournal(digest, 20,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
}

func TestInTimingRangeIsAnExplicitReplayableObjectChoice(t *testing.T) {
	architecture := timingRangeProcessArchitecture(t, "in-timing-range",
		CallActionInRange("ranged", "Ranged", "C", 0, 2),
		CallAction("final", "Final"),
	)
	journal := timingRangeJournal(t, architecture)
	canonical, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical.Choices) == 0 || !strings.HasPrefix(canonical.Choices[0].Domain, "timing-object:") ||
		canonical.Choices[0].Selected != timingTickOption(0) || len(canonical.Choices[0].Options) != 3 {
		t.Fatalf("timing range choice audit=%#v", canonical.Choices)
	}
	if len(canonical.ClockAdvances) != 0 {
		t.Fatalf("canonical zero member advanced a clock: %#v", canonical.ClockAdvances)
	}
	ranged := canonical.Poset.ByName("Ranged")[0]
	zeroTiming, related := ranged.Timing(ClockID("worker", "C"))
	if !related || zeroTiming.Start != 0 || zeroTiming.Finish != 0 {
		t.Fatalf("selected in-range zero did not use ordinary same-tick generation: %#v,%v", zeroTiming, related)
	}

	journal.Choices = []ChoiceDecision{{
		Point: canonical.Choices[0].Point, Selection: timingTickOption(2),
	}}
	selected, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	ranged = selected.Poset.ByName("Ranged")[0]
	timing, related := ranged.Timing(ClockID("worker", "C"))
	if !related || timing.Start != 2 || timing.Finish != 2 ||
		len(selected.ClockAdvances) != 1 || selected.ClockAdvances[0].To != "2" {
		t.Fatalf("selected in-range member: timing=%#v,%v advances=%#v", timing, related, selected.ClockAdvances)
	}
	expected, err := selected.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := architecture.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := selected.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("selected in-range member did not replay byte-identically")
	}

	explored, err := architecture.ExploreDeterministic(timingRangeJournal(t, architecture), ExplorationLimits{
		MaxExecutions: 16, MaxChoiceDepth: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 3 {
		t.Fatalf("in timing range exploration=%d computations, complete=%v; want three", len(explored.Computations), explored.Complete)
	}
	selectedTicks := make(map[uint64]bool)
	for _, computation := range explored.Computations {
		event := computation.Result.Poset.ByName("Ranged")[0]
		if timing, related := event.Timing(ClockID("worker", "C")); related {
			selectedTicks[timing.Finish] = true
		} else {
			selectedTicks[0] = true
		}
	}
	if !selectedTicks[0] || !selectedTicks[1] || !selectedTicks[2] {
		t.Fatalf("exploration missed timing range members: %#v", selectedTicks)
	}
}

func TestPauseDelayTimingRangesUseSelectedIntervalsAndExploreEveryMember(t *testing.T) {
	tests := []struct {
		name        string
		statement   Statement
		actionEvent bool
		kind        TimingClauseKind
	}{
		{name: "pause-action", statement: CallActionPauseRange("ranged", "Ranged", "C", 1, 2), actionEvent: true, kind: PauseTimingClause},
		{name: "delay-action", statement: CallActionDelayRange("ranged", "Ranged", "C", 1, 2), actionEvent: true, kind: DelayTimingClause},
		{name: "pause-statement", statement: PauseForRange("C", 1, 2), kind: PauseTimingClause},
		{name: "delay-statement", statement: DelayForRange("C", 1, 2), kind: DelayTimingClause},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			architecture := timingRangeProcessArchitecture(t, "timing-range-"+test.name,
				test.statement, CallAction("final", "Final"),
			)
			journal := timingRangeJournal(t, architecture)
			canonical, err := architecture.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			if len(canonical.Choices) == 0 || canonical.Choices[0].Selected != timingTickOption(1) ||
				len(canonical.Firings) != 1 || len(canonical.Firings[0].Suspensions) != 1 {
				t.Fatalf("range suspension audit: choices=%#v firings=%#v", canonical.Choices, canonical.Firings)
			}
			record := canonical.Firings[0].Suspensions[0]
			if record.Kind != test.kind || record.Start != "0" || record.Finish != "1" {
				t.Fatalf("selected range suspension=%#v", record)
			}
			final := canonical.Poset.ByName("Final")[0]
			finalTiming, related := final.Timing(ClockID("worker", "C"))
			if !related || finalTiming.Start != 1 || finalTiming.Finish != 1 {
				t.Fatalf("post-range final timing=%#v,%v", finalTiming, related)
			}
			if test.actionEvent {
				event := canonical.Poset.ByName("Ranged")[0]
				timing, related := event.Timing(ClockID("worker", "C"))
				if !related || timing.Start != 0 || timing.Finish != 1 || record.EventID != string(event.ID) {
					t.Fatalf("range action timing=%#v,%v record=%#v", timing, related, record)
				}
			} else if len(canonical.Poset.ByName("Ranged")) != 0 || record.EventID != "" {
				t.Fatal("timed range statement generated an action occurrence")
			}

			explored, err := architecture.ExploreDeterministic(journal, ExplorationLimits{
				MaxExecutions: 12, MaxChoiceDepth: 4,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !explored.Complete || len(explored.Computations) != 2 {
				t.Fatalf("%s range exploration=%d computations, complete=%v", test.name, len(explored.Computations), explored.Complete)
			}
			finishes := make(map[uint64]bool)
			for _, computation := range explored.Computations {
				timing, _ := computation.Result.Poset.ByName("Final")[0].Timing(ClockID("worker", "C"))
				finishes[timing.Finish] = true
			}
			if !finishes[1] || !finishes[2] {
				t.Fatalf("%s exploration missed range members: %#v", test.name, finishes)
			}
		})
	}
}

func timingRangeDigest(t *testing.T, statement Statement) (string, error) {
	t.Helper()
	architecture := NewArchitecture("timing-range-identity")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Ranged").Build(), nil)
	_ = component.AddBasicClock("C")
	_ = component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("start").On(pattern.MatchEvent("Start")).Do(statement).Terminate().Build()),
	).Build())
	_ = architecture.AddComponent(component)
	return architecture.DeterministicModelDigest()
}

func TestTimingRangeCanonicalIdentityAndValidation(t *testing.T) {
	ordinaryZero, err := timingRangeDigest(t, CallAction("ranged", "Ranged"))
	if err != nil {
		t.Fatal(err)
	}
	rangeZero, err := timingRangeDigest(t, CallActionInRange("ranged", "Ranged", "C", 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if ordinaryZero != rangeZero {
		t.Fatal("singleton in-range zero did not canonicalize to an ordinary call")
	}
	fixed, err := timingRangeDigest(t, CallActionPause("ranged", "Ranged", "C", 3))
	if err != nil {
		t.Fatal(err)
	}
	singleton, err := timingRangeDigest(t, CallActionPauseRange("ranged", "Ranged", "C", 3, 3))
	if err != nil {
		t.Fatal(err)
	}
	if fixed != singleton {
		t.Fatal("singleton Ticks range did not canonicalize to its one object")
	}
	first, err := timingRangeDigest(t, CallActionPauseRange("ranged", "Ranged", "C", 1, 2))
	if err != nil {
		t.Fatal(err)
	}
	second, err := timingRangeDigest(t, CallActionPauseRange("ranged", "Ranged", "C", 1, 3))
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first == fixed {
		t.Fatal("finite Ticks range endpoints did not affect canonical model identity")
	}

	invalid := []Statement{
		CallActionPauseRange("ranged", "Ranged", "C", 2, 1),
		CallActionPauseRange("ranged", "Ranged", "C", 0, MaxTimingRangeCardinality),
		{kind: EventCallStatement, output: RuleEvent("ranged", "Ranged"), timing: &ActionTimingClause{
			Kind: PauseTimingClause, Clock: "C", Ticks: 1, Range: &TimingTickRange{First: 1, Last: 2},
		}},
	}
	for index, statement := range invalid {
		if _, err := timingRangeDigest(t, statement); !errors.Is(err, ErrInvalidTimingRange) ||
			!errors.Is(err, ErrInvalidDeclarativeStatement) {
			t.Fatalf("invalid timing range %d was not rejected explicitly: %v", index, err)
		}
	}
}

func TestTimingRangePreservesCompleteUint64TicksDomain(t *testing.T) {
	architecture := timingRangeProcessArchitecture(t, "timing-range-uint64",
		PauseForRange("C", math.MaxUint64-1, math.MaxUint64), CallAction("final", "Final"),
	)
	journal := timingRangeJournal(t, architecture)
	canonical, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical.Choices) == 0 || canonical.Choices[0].Options[1] != timingTickOption(math.MaxUint64) {
		t.Fatalf("uint64 timing options=%#v", canonical.Choices)
	}
	journal.Choices = []ChoiceDecision{{
		Point: canonical.Choices[0].Point, Selection: timingTickOption(math.MaxUint64),
	}}
	selected, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.ClockAdvances) != 1 || selected.ClockAdvances[0].To != "18446744073709551615" {
		t.Fatalf("maximum Ticks selection lost precision: %#v", selected.ClockAdvances)
	}
	timing, related := selected.Poset.ByName("Final")[0].Timing(ClockID("worker", "C"))
	if !related || timing.Finish != math.MaxUint64 {
		t.Fatalf("maximum Ticks final timing=%#v,%v", timing, related)
	}
}

func TestTimingRangeExecutionStableAcrossGOMAXPROCS(t *testing.T) {
	architecture := timingRangeProcessArchitecture(t, "timing-range-stability",
		PauseForRange("C", 0, 2), CallAction("final", "Final"),
	)
	journal := timingRangeJournal(t, architecture)
	canonical, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	journal.Choices = []ChoiceDecision{{
		Point: canonical.Choices[0].Point, Selection: timingTickOption(2),
	}}
	prior := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(prior)
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
				t.Fatalf("timing range artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}
