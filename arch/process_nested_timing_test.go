package arch

import (
	"bytes"
	"errors"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func nestedTimingArchitecture(t *testing.T) *Architecture {
	t.Helper()
	architecture := NewArchitecture("nested-process-timing")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("A").OutAction("B").
		OutAction("Final").OutAction("Unreachable").Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("stage", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	nestedCase := CaseOf(LiteralValue(3), CaseOrMode,
		CaseWhen(LiteralValue(3),
			CallActionPause("a", "A", "C", 2),
		),
		CaseWhenAny([]RuleValue{LiteralValue(2), LiteralValue(3)},
			DelayFor("C", 1),
			SetState("stage", LiteralValue(2)),
			CallAction("b", "B"),
		),
	)
	body := []Statement{
		SetState("stage", LiteralValue(1)),
		IfThen(EqualValues(ReadState("stage"), LiteralValue(1)), []Statement{
			PauseFor("C", 1),
			nestedCase,
		}, []Statement{
			CallActionDelay("unreachable", "Unreachable", "C", 9),
		}),
		CallAction("final", "Final"),
	}
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("start").On(pattern.MatchEvent("Start")).Do(body...).Terminate().Build()),
	).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestNestedIfAndCaseTimingResumeExactControlFrames(t *testing.T) {
	architecture := nestedTimingArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 20,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Unreachable")) != 0 ||
		len(result.Poset.ByName("A")) != 1 || len(result.Poset.ByName("B")) != 1 ||
		len(result.Poset.ByName("Final")) != 1 {
		t.Fatalf("nested branch execution is incomplete: A=%d B=%d Final=%d Unreachable=%d",
			len(result.Poset.ByName("A")), len(result.Poset.ByName("B")),
			len(result.Poset.ByName("Final")), len(result.Poset.ByName("Unreachable")))
	}
	a := result.Poset.ByName("A")[0]
	b := result.Poset.ByName("B")[0]
	final := result.Poset.ByName("Final")[0]
	if !result.Poset.IsCausallyBefore(a.ID, b.ID) || !result.Poset.IsCausallyBefore(b.ID, final.ID) {
		t.Fatal("nested continuation lost source process order")
	}
	clock := ClockID("worker", "C")
	timing, related := a.Timing(clock)
	if !related || timing.Start != 1 || timing.Finish != 3 {
		t.Fatalf("nested pause action timing=%#v,%v, want [1,3]", timing, related)
	}
	if len(result.ClockAdvances) != 3 || result.ClockAdvances[0].To != "1" ||
		result.ClockAdvances[1].To != "3" || result.ClockAdvances[2].To != "4" {
		t.Fatalf("nested clock advances=%#v, want 1,3,4", result.ClockAdvances)
	}
	if len(result.Firings) != 1 || len(result.Firings[0].Suspensions) != 3 {
		t.Fatalf("nested suspension audit=%#v", result.Firings)
	}
	wantPaths := []string{"1/then/0", "1/then/1/case/0/0", "1/then/1/case/1/0"}
	seenIDs := make(map[string]bool)
	for index, record := range result.Firings[0].Suspensions {
		if record.Statement != wantPaths[index] {
			t.Fatalf("suspension %d path=%q, want %q", index, record.Statement, wantPaths[index])
		}
		if seenIDs[record.SuspensionID] {
			t.Fatalf("nested suspensions reused ID %q", record.SuspensionID)
		}
		seenIDs[record.SuspensionID] = true
	}
	if len(result.State) != 1 || result.State[0].Value.Text != "2" || result.State[0].Version != 2 {
		t.Fatalf("nested state did not survive yields: %#v", result.State)
	}
	if result.StatementSteps != 9 {
		t.Fatalf("nested statement steps=%d, want 9", result.StatementSteps)
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
		t.Fatal("nested if/case timing did not replay byte-identically")
	}
}

func nestedTimedLoopArchitecture(t *testing.T) *Architecture {
	t.Helper()
	architecture := NewArchitecture("nested-timed-loop")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Tick", P("count", "Integer")).OutAction("After").Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("count", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	loop := LoopDo(
		SetState("count", AddValues(ReadState("count"), LiteralValue(1))),
		PauseFor("C", 1),
		NextWhen(EqualValues(ReadState("count"), LiteralValue(2))),
		CallAction("tick", "Tick", StateParam("count", "count")),
		ExitWhen(GreaterOrEqualValues(ReadState("count"), LiteralValue(3))),
	)
	if err := component.AddDeclarativeProcess(Process("counter").StartAt("wait").States(
		AwaitState("wait", Await("start").On(pattern.MatchEvent("Start")).Do(
			loop, CallAction("after", "After"),
		).Terminate().Build()),
	).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestNestedTimedLoopPreservesIterationPathsAndLoopControl(t *testing.T) {
	architecture := nestedTimedLoopArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournalWithLimits(
		digest, ExecutionLimits{MaxFirings: 10, MaxStatements: 30},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	ticks := result.Poset.ByName("Tick")
	if len(ticks) != 2 || len(result.Poset.ByName("After")) != 1 ||
		len(result.ClockAdvances) != 3 || result.StatementSteps != 15 {
		t.Fatalf("nested loop execution: ticks=%d after=%d advances=%d steps=%d",
			len(ticks), len(result.Poset.ByName("After")), len(result.ClockAdvances), result.StatementSteps)
	}
	values := make(map[int64]bool)
	var first, last *gorapide.Event
	for _, event := range ticks {
		value, _ := event.Param("count")
		count := value.(int64)
		values[count] = true
		if count == 1 {
			first = event
		}
		if count == 3 {
			last = event
		}
	}
	if !values[1] || !values[3] || values[2] {
		t.Fatalf("timed loop next/exit semantics are incomplete: %#v", values)
	}
	records := result.Firings[0].Suspensions
	wantPaths := []string{"0/iteration/1/1", "0/iteration/2/1", "0/iteration/3/1"}
	if len(records) != len(wantPaths) {
		t.Fatalf("timed loop suspension records=%#v", records)
	}
	for index, want := range wantPaths {
		if records[index].Statement != want {
			t.Fatalf("loop suspension %d path=%q, want %q", index, records[index].Statement, want)
		}
	}
	after := result.Poset.ByName("After")[0]
	if !result.Poset.IsCausallyBefore(first.ID, last.ID) || !result.Poset.IsCausallyBefore(last.ID, after.ID) {
		t.Fatal("timed loop lost cross-iteration or post-loop causality")
	}
}

func TestNestedTimedLoopStatementBudgetSurvivesEveryResume(t *testing.T) {
	architecture := NewArchitecture("bounded-nested-timed-loop")
	component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("start").On(pattern.MatchEvent("Start")).Do(
			LoopDo(PauseFor("C", 1)),
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
	journal := NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 5, MaxStatements: 5},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	for run := 0; run < 2; run++ {
		if _, err := architecture.ExecuteDeterministic(journal); !errors.Is(err, ErrExecutionLimit) {
			t.Fatalf("run %d expected statement-bound failure across resumes, got %v", run, err)
		}
	}
}

func TestNestedTimingExecutionStableAcrossGOMAXPROCS(t *testing.T) {
	architecture := nestedTimingArchitecture(t)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 20,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
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
				t.Fatalf("nested timing artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}
