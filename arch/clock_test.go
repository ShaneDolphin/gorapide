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

func inClauseArchitecture(t *testing.T, twoClocks bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("in-clause")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").
		OutAction("A", P("n", "Integer")).Build(), nil)
	if err := component.AddBasicClock("C1"); err != nil {
		t.Fatal(err)
	}
	if twoClocks {
		if err := component.AddBasicClock("C2"); err != nil {
			t.Fatal(err)
		}
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func eventWithInteger(t *testing.T, events gorapide.EventSet, value int64) *gorapide.Event {
	t.Helper()
	for _, event := range events {
		actual, ok := event.Param("n")
		if ok && actual == value {
			return event
		}
	}
	t.Fatalf("event with n=%d not found in %#v", value, events)
	return nil
}

func TestInClauseUsesDeadlineAndGenerationTimeCausality(t *testing.T) {
	architecture := inClauseArchitecture(t, false)
	component, _ := architecture.Component("worker")
	process := Process("p").StartAt("wait").States(
		AwaitState("wait", Await("run").On(pattern.MatchEvent("Start")).Do(
			CallActionIn("a1", "A", "C1", 1, LiteralParam("n", 1)),
			CallActionIn("a2", "A", "C1", 1, LiteralParam("n", 2)),
			CallAction("a3", "A", LiteralParam("n", 3)),
		).Terminate().Build()),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
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
	events := result.Poset.ByName("A")
	if len(events) != 3 {
		t.Fatalf("A events=%d, want 3", len(events))
	}
	a1 := eventWithInteger(t, events, 1)
	a2 := eventWithInteger(t, events, 2)
	a3 := eventWithInteger(t, events, 3)
	clock := ClockID("worker", "C1")
	for _, check := range []struct {
		event *gorapide.Event
		tick  uint64
	}{{a1, 1}, {a2, 1}, {a3, 0}} {
		timing, ok := check.event.Timing(clock)
		if !ok || timing.Start != check.tick || timing.Finish != check.tick {
			t.Fatalf("event %s timing=%#v,%v, want %s=%d", check.event.ID, timing, ok, clock, check.tick)
		}
	}
	if !result.Poset.IsCausallyBefore(a3.ID, a1.ID) || !result.Poset.IsCausallyBefore(a1.ID, a2.ID) {
		t.Fatal("in-clause calls were ordered by call evaluation instead of actual generation time")
	}
	if result.Poset.IsCausallyBefore(a1.ID, a3.ID) || result.Poset.IsCausallyBefore(a2.ID, a3.ID) {
		t.Fatal("a future scheduled event incorrectly precedes the immediate event")
	}
	if len(result.Firings) != 1 || len(result.Firings[0].Generated) != 1 || len(result.Firings[0].Scheduled) != 2 {
		t.Fatalf("firing schedule audit=%#v", result.Firings)
	}
	if len(result.ClockAdvances) != 1 || result.ClockAdvances[0].From != "0" || result.ClockAdvances[0].To != "1" ||
		len(result.ScheduledEvents) != 2 || len(result.Clocks) != 1 || result.Clocks[0].Now != "1" {
		t.Fatalf("clock audit is incomplete: clocks=%#v advances=%#v scheduled=%#v", result.Clocks, result.ClockAdvances, result.ScheduledEvents)
	}
	if result.ClockPolicy != ClockAdvancePolicy {
		t.Fatalf("clock policy=%q", result.ClockPolicy)
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
		t.Fatal("deadline execution did not replay byte-identically")
	}
}

func TestIndependentBasicClockAdvanceIsExplicitAndExplorable(t *testing.T) {
	architecture := inClauseArchitecture(t, true)
	component, _ := architecture.Component("worker")
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("run").On(pattern.MatchEvent("Start")).Do(
			CallActionIn("c1", "A", "C1", 1, LiteralParam("n", 1)),
			CallActionIn("c2", "A", "C2", 1, LiteralParam("n", 2)),
		).Terminate().Build()),
	).Build()); err != nil {
		t.Fatal(err)
	}
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
	if len(result.Choices) != 1 || result.Choices[0].Domain != "clock-advance" || result.Choices[0].Scheduled {
		t.Fatalf("independent clock choice is not audited: %#v", result.Choices)
	}
	if len(result.Choices[0].Options) != 2 || !strings.Contains(result.Choices[0].Selected, "worker.C1@1") {
		t.Fatalf("canonical clock choice=%#v", result.Choices[0])
	}
	explored, err := architecture.ExploreDeterministic(journal, ExplorationLimits{MaxExecutions: 8, MaxChoiceDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 2 {
		t.Fatalf("independent clocks yielded %d computations, complete=%v", len(explored.Computations), explored.Complete)
	}
	for _, computation := range explored.Computations {
		if len(computation.Schedule) != 1 {
			t.Fatalf("clock computation schedule=%#v", computation.Schedule)
		}
	}
}

func TestTimedInputIsNotObservableBeforeItsClockDeadline(t *testing.T) {
	architecture := NewArchitecture("timed-input-deadline")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Done").Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("run").On(pattern.MatchEvent("Start")).Do(
			CallAction("done", "Done"),
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
	clock := ClockID("worker", "C")
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "start", Source: "worker", Action: "Start",
			Timings: []gorapide.EventTiming{{Clock: clock, Start: 5, Finish: 5}}},
	))
	if err != nil {
		t.Fatal(err)
	}
	done := result.Poset.ByName("Done")
	if len(done) != 1 {
		t.Fatalf("Done events=%d", len(done))
	}
	timing, ok := done[0].Timing(clock)
	if !ok || timing.Start != 5 || timing.Finish != 5 {
		t.Fatalf("Done timing=%#v,%v, want instant at 5", timing, ok)
	}
	if len(result.ClockAdvances) != 1 || result.ClockAdvances[0].Released[0][:6] != "input:" {
		t.Fatalf("input deadline audit=%#v", result.ClockAdvances)
	}
}

func TestCausallyDependentInputInheritsOwningClockFrontier(t *testing.T) {
	architecture := NewArchitecture("input-clock-frontier")
	component := NewComponent("worker", Interface("Worker").
		OutAction("A").OutAction("B").Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	clock := ClockID("worker", "C")
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
		InputEvent{Key: "a", Source: "worker", Action: "A",
			Timings: []gorapide.EventTiming{{Clock: clock, Start: 5, Finish: 5}}},
		InputEvent{Key: "b", Source: "worker", Action: "B", Causes: []string{"a"}},
	))
	if err != nil {
		t.Fatal(err)
	}
	b := result.Poset.ByName("B")
	if len(b) != 1 {
		t.Fatalf("B events=%d", len(b))
	}
	timing, ok := b[0].Timing(clock)
	if !ok || timing.Start != 5 || timing.Finish != 5 {
		t.Fatalf("dependent input timing=%#v,%v, want instant at predecessor finish 5", timing, ok)
	}
	if len(result.ClockAdvances) != 1 || len(result.ClockAdvances[0].Released) != 2 {
		t.Fatalf("dependent input release audit=%#v", result.ClockAdvances)
	}
}

func TestInZeroCanonicalizesToUntimedActionCall(t *testing.T) {
	build := func(zeroClause bool) *Architecture {
		architecture := NewArchitecture("in-zero")
		component := NewComponent("worker", Interface("Worker").
			OutAction("Start").OutAction("Done").Build(), nil)
		_ = component.AddBasicClock("C")
		statement := CallAction("done", "Done")
		if zeroClause {
			statement = CallActionIn("done", "Done", "C", 0)
		}
		_ = component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
			AwaitState("wait", Await("run").On(pattern.MatchEvent("Start")).Do(statement).Terminate().Build()),
		).Build())
		_ = architecture.AddComponent(component)
		return architecture
	}
	left, err := build(false).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	right, err := build(true).DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("in 0 changed canonical semantics: %s != %s", left, right)
	}
}

func TestBasicClockAndTimingClauseRejectMalformedModels(t *testing.T) {
	t.Run("duplicate clock", func(t *testing.T) {
		architecture := inClauseArchitecture(t, false)
		component, _ := architecture.Component("worker")
		if err := component.AddBasicClock("C1"); err != nil {
			t.Fatal(err)
		}
		if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrInvalidBasicClock) {
			t.Fatalf("got %v, want ErrInvalidBasicClock", err)
		}
	})

	t.Run("missing named clock", func(t *testing.T) {
		architecture := inClauseArchitecture(t, false)
		component, _ := architecture.Component("worker")
		_ = component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
			AwaitState("wait", Await("run").On(pattern.MatchEvent("Start")).Do(
				CallActionIn("missing", "A", "Missing", 1, LiteralParam("n", 1)),
			).Terminate().Build()),
		).Build())
		if _, err := architecture.DeterministicModelDigest(); !errors.Is(err, ErrInvalidDeclarativeStatement) {
			t.Fatalf("got %v, want ErrInvalidDeclarativeStatement", err)
		}
	})

	t.Run("deadline overflow", func(t *testing.T) {
		architecture := inClauseArchitecture(t, false)
		component, _ := architecture.Component("worker")
		_ = component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
			AwaitState("wait", Await("run").On(pattern.MatchEvent("Start")).Do(
				CallActionIn("overflow", "A", "C1", 1, LiteralParam("n", 1)),
			).Terminate().Build()),
		).Build())
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		_, err = architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
			InputEvent{Key: "start", Source: "worker", Action: "Start",
				Timings: []gorapide.EventTiming{{Clock: ClockID("worker", "C1"), Start: ^uint64(0), Finish: ^uint64(0)}}},
		))
		if !errors.Is(err, ErrClockDeadlineOverflow) {
			t.Fatalf("got %v, want ErrClockDeadlineOverflow", err)
		}
	})
}

func TestBasicConnectionEstablishesTargetClockRelationship(t *testing.T) {
	build := func(t *testing.T) *Architecture {
		t.Helper()
		architecture := NewArchitecture("timed-basic-connection")
		source := NewComponent("source", Interface("Source").OutAction("Ping").Build(), nil)
		target := NewComponent("target", Interface("Target").InAction("Pong").Build(), nil)
		if err := target.AddBasicClock("C"); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(source); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(target); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddConnection(Connect("source", "target").IdentifiedBy("basic").Send("Pong").Build()); err != nil {
			t.Fatal(err)
		}
		return architecture
	}

	t.Run("observation establishes relationship", func(t *testing.T) {
		architecture := build(t)
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
			InputEvent{Key: "ping", Source: "source", Action: "Ping"},
		))
		if err != nil {
			t.Fatal(err)
		}
		pong := result.Poset.ByName("Pong")
		if len(pong) != 1 {
			t.Fatalf("Pong events=%d", len(pong))
		}
		timing, ok := pong[0].Timing(ClockID("target", "C"))
		if !ok || timing.Start != 0 || timing.Finish != 0 {
			t.Fatalf("basic observation timing=%#v,%v", timing, ok)
		}
	})

	t.Run("explicit relationship preserves basic identity", func(t *testing.T) {
		architecture := build(t)
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		clock := ClockID("target", "C")
		result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
			InputEvent{Key: "ping", Source: "source", Action: "Ping",
				Timings: []gorapide.EventTiming{{Clock: clock, Start: 2, Finish: 2}}},
		))
		if err != nil {
			t.Fatal(err)
		}
		ping := result.Poset.ByName("Ping")
		pong := result.Poset.ByName("Pong")
		if len(ping) != 1 || len(pong) != 1 || ping[0].ID != pong[0].ID {
			t.Fatalf("basic occurrence identity changed: ping=%#v pong=%#v", ping, pong)
		}
		if len(result.ClockAdvances) != 1 || result.ClockAdvances[0].To != "2" {
			t.Fatalf("target clock was not advanced to observation time: %#v", result.ClockAdvances)
		}
	})

	t.Run("existing duration is preserved", func(t *testing.T) {
		architecture := build(t)
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		clock := ClockID("target", "C")
		result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 10,
			InputEvent{Key: "ping", Source: "source", Action: "Ping",
				Timings: []gorapide.EventTiming{{Clock: clock, Start: 2, Finish: 5}}},
		))
		if err != nil {
			t.Fatal(err)
		}
		pong := result.Poset.ByName("Pong")
		timing, ok := pong[0].Timing(clock)
		if !ok || timing.Start != 2 || timing.Finish != 5 {
			t.Fatalf("basic observation changed duration: %#v,%v", timing, ok)
		}
	})
}

func TestClockExecutionStableAcrossGOMAXPROCS(t *testing.T) {
	architecture := inClauseArchitecture(t, true)
	component, _ := architecture.Component("worker")
	_ = component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("run").On(pattern.MatchEvent("Start")).Do(
			CallAction("now", "A", LiteralParam("n", 0)),
			CallActionIn("c1", "A", "C1", 2, LiteralParam("n", 1)),
			CallActionIn("c2", "A", "C2", 3, LiteralParam("n", 2)),
		).Terminate().Build()),
	).Build())
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 20,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	journal.ClockAdvances = []ClockAdvanceDirective{
		{Clock: ClockID("worker", "C2"), To: 1},
		{Clock: ClockID("worker", "C1"), To: 1},
	}
	var baseline []byte
	prior := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(prior)
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for iteration := 0; iteration < 20; iteration++ {
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
				t.Fatalf("clock artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

func idleClockArchitecture(t *testing.T, clocks ...string) *Architecture {
	t.Helper()
	architecture := NewArchitecture("explicit-idle-clocks")
	component := NewComponent("worker", Interface("Worker").Build(), nil)
	for _, clock := range clocks {
		if err := component.AddBasicClock(clock); err != nil {
			t.Fatal(err)
		}
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestExplicitClockAdvancesReplayFiniteIdleHistory(t *testing.T) {
	architecture := idleClockArchitecture(t, "C")
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 10)
	journal.ClockAdvances = []ClockAdvanceDirective{
		{Clock: ClockID("worker", "C"), To: 5},
		{Clock: ClockID("worker", "C"), To: ^uint64(0)},
	}
	encoded, err := journal.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"clock_advances":[{"clock":"worker.C","to":"5"},{"clock":"worker.C","to":"18446744073709551615"}]`)) {
		t.Fatalf("clock directives are not lossless ordered decimal inputs: %s", encoded)
	}
	parsed, err := ParseExecutionJournal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.ClockAdvances) != 2 || parsed.ClockAdvances[0].To != 5 || parsed.ClockAdvances[1].To != ^uint64(0) {
		t.Fatalf("clock directive round trip=%#v", parsed.ClockAdvances)
	}
	result, err := architecture.ExecuteDeterministic(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Clocks) != 1 || result.Clocks[0].Now != "18446744073709551615" || len(result.ClockAdvances) != 2 {
		t.Fatalf("explicit idle history clocks=%#v advances=%#v", result.Clocks, result.ClockAdvances)
	}
	for index, advance := range result.ClockAdvances {
		if advance.Reason != "explicit" || len(advance.Released) != 0 || advance.Sequence != uint64(index+1) {
			t.Fatalf("explicit idle audit[%d]=%#v", index, advance)
		}
	}
	if events := result.Poset.Events(); len(events) != 1 ||
		events[0].Source != ArchitectureInterfaceID || events[0].Name != ArchitectureStartAction ||
		len(result.Choices) != 0 {
		t.Fatalf("idle clock history invented semantic work: events=%#v choices=%#v", result.Poset.Events(), result.Choices)
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replay, err := architecture.ReplayDeterministic(parsed, expected)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replay.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("explicit idle history did not replay byte-identically")
	}
	noncanonical := bytes.Replace(encoded, []byte(`"to":"5"`), []byte(`"to":"05"`), 1)
	if _, err := ParseExecutionJournal(noncanonical); !errors.Is(err, ErrInvalidExecutionJournal) {
		t.Fatalf("noncanonical clock target: got %v, want ErrInvalidExecutionJournal", err)
	}
}

func TestExplicitClockAdvanceStopsBeforeThenReleasesDeadline(t *testing.T) {
	architecture := inClauseArchitecture(t, false)
	component, _ := architecture.Component("worker")
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("run").On(pattern.MatchEvent("Start")).Do(
			CallActionIn("future", "A", "C1", 5, LiteralParam("n", 5)),
		).Terminate().Build()),
	).Build()); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 20,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	journal.ClockAdvances = []ClockAdvanceDirective{{Clock: ClockID("worker", "C1"), To: 3}}
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ClockAdvances) != 2 || result.ClockAdvances[0].To != "3" ||
		result.ClockAdvances[0].Reason != "explicit" || len(result.ClockAdvances[0].Released) != 0 ||
		result.ClockAdvances[1].To != "5" || result.ClockAdvances[1].Reason != "deadline" ||
		len(result.ClockAdvances[1].Released) != 1 {
		t.Fatalf("explicit/deadline audit=%#v", result.ClockAdvances)
	}
	events := result.Poset.ByName("A")
	if len(events) != 1 {
		t.Fatalf("A events=%d", len(events))
	}
	timing, related := events[0].Timing(ClockID("worker", "C1"))
	if !related || timing.Start != 5 || timing.Finish != 5 {
		t.Fatalf("deadline event timing=%#v,%v", timing, related)
	}
}

func TestExplicitClockAdvanceAtDeadlinePerformsRelease(t *testing.T) {
	architecture := NewArchitecture("explicit-input-deadline")
	component := NewComponent("worker", Interface("Worker").OutAction("Input").Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	clock := ClockID("worker", "C")
	journal := NewExecutionJournal(digest, 10, InputEvent{
		Key: "input", Source: "worker", Action: "Input",
		Timings: []gorapide.EventTiming{{Clock: clock, Start: 4, Finish: 4}},
	})
	journal.ClockAdvances = []ClockAdvanceDirective{{Clock: clock, To: 4}}
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ClockAdvances) != 1 || result.ClockAdvances[0].Reason != "explicit" ||
		len(result.ClockAdvances[0].Released) != 1 || !strings.HasPrefix(result.ClockAdvances[0].Released[0], "input:") {
		t.Fatalf("explicit deadline release=%#v", result.ClockAdvances)
	}
}

func TestExplicitClockAdvanceAtSuspensionDeadlineResumesProcess(t *testing.T) {
	architecture := timedProcessArchitecture(t, "explicit-suspension-deadline",
		PauseFor("C", 5),
		CallAction("done", "Final", LiteralParam("n", 1)),
	)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	clock := ClockID("worker", "C")
	journal := NewExecutionJournal(digest, 20,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	journal.ClockAdvances = []ClockAdvanceDirective{{Clock: clock, To: 5}}
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ClockAdvances) != 1 || result.ClockAdvances[0].Reason != "explicit" ||
		len(result.ClockAdvances[0].Released) != 1 || !strings.HasPrefix(result.ClockAdvances[0].Released[0], "resume:") {
		t.Fatalf("explicit suspension release=%#v", result.ClockAdvances)
	}
	done := result.Poset.ByName("Final")
	if len(done) != 1 {
		t.Fatalf("Final events=%d", len(done))
	}
	timing, related := done[0].Timing(clock)
	if !related || timing.Start != 5 || timing.Finish != 5 {
		t.Fatalf("resumed process event timing=%#v,%v", timing, related)
	}
}

func TestExplicitClockAdvanceOrderIsSemanticAndSuppressesClockChoice(t *testing.T) {
	architecture := idleClockArchitecture(t, "C1", "C2")
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	left := NewExecutionJournal(digest, 10)
	left.ClockAdvances = []ClockAdvanceDirective{
		{Clock: ClockID("worker", "C1"), To: 1},
		{Clock: ClockID("worker", "C2"), To: 1},
	}
	right := NewExecutionJournal(digest, 10)
	right.ClockAdvances = []ClockAdvanceDirective{
		{Clock: ClockID("worker", "C2"), To: 1},
		{Clock: ClockID("worker", "C1"), To: 1},
	}
	leftBytes, err := left.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := right.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(leftBytes, rightBytes) {
		t.Fatal("clock directive order was incorrectly canonicalized away")
	}
	for _, journal := range []ExecutionJournal{left, right} {
		result, err := architecture.ExecuteDeterministic(journal)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Choices) != 0 || len(result.ClockAdvances) != 2 {
			t.Fatalf("explicit clock order leaked into choice resolution: choices=%#v advances=%#v", result.Choices, result.ClockAdvances)
		}
	}
}

func TestExplicitClockAdvanceRejectsMalformedOrImpossibleDirectives(t *testing.T) {
	architecture := idleClockArchitecture(t, "C")
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		directives []ClockAdvanceDirective
		marshal    bool
	}{
		{name: "empty clock", directives: []ClockAdvanceDirective{{To: 1}}, marshal: true},
		{name: "zero target", directives: []ClockAdvanceDirective{{Clock: ClockID("worker", "C")}}, marshal: true},
		{name: "undeclared clock", directives: []ClockAdvanceDirective{{Clock: "worker.Missing", To: 1}}},
		{name: "nonfuture target", directives: []ClockAdvanceDirective{
			{Clock: ClockID("worker", "C"), To: 2}, {Clock: ClockID("worker", "C"), To: 2},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			journal := NewExecutionJournal(digest, 10)
			journal.ClockAdvances = test.directives
			if test.marshal {
				if _, err := journal.MarshalCanonical(); !errors.Is(err, ErrInvalidExecutionJournal) ||
					!errors.Is(err, ErrClockAdvanceDirective) {
					t.Fatalf("got %v, want journal/directive errors", err)
				}
				return
			}
			if _, err := architecture.ExecuteDeterministic(journal); !errors.Is(err, ErrInvalidExecutionJournal) ||
				!errors.Is(err, ErrClockAdvanceDirective) {
				t.Fatalf("got %v, want journal/directive errors", err)
			}
		})
	}

	deadlineArchitecture := inClauseArchitecture(t, false)
	component, _ := deadlineArchitecture.Component("worker")
	_ = component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("run").On(pattern.MatchEvent("Start")).Do(
			CallActionIn("future", "A", "C1", 5, LiteralParam("n", 5)),
		).Terminate().Build()),
	).Build())
	deadlineDigest, err := deadlineArchitecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(deadlineDigest, 20,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	journal.ClockAdvances = []ClockAdvanceDirective{{Clock: ClockID("worker", "C1"), To: 6}}
	if _, err := deadlineArchitecture.ExecuteDeterministic(journal); !errors.Is(err, ErrClockAdvanceDirective) {
		t.Fatalf("past-deadline directive: got %v, want ErrClockAdvanceDirective", err)
	}
}
