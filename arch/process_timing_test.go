package arch

import (
	"bytes"
	"errors"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func timedProcessArchitecture(t *testing.T, name string, statements ...Statement) *Architecture {
	t.Helper()
	architecture := NewArchitecture(name)
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").
		OutAction("Interval", P("n", "Integer")).
		OutAction("Final", P("n", "Integer")).Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("value", "Integer", 0)); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("run").On(pattern.MatchEvent("Start")).Do(statements...).Terminate().Build()),
	).Build()); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestPauseDelayContinuationUsesClosedClockIntervals(t *testing.T) {
	architecture := timedProcessArchitecture(t, "pause-delay-continuation",
		SetState("value", LiteralValue(1)),
		CallActionPause("pause-action", "Interval", "C", 2, StateParam("n", "value")),
		SetState("value", LiteralValue(2)),
		PauseFor("C", 1),
		CallActionDelay("delay-action", "Final", "C", 3, StateParam("n", "value")),
	)
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
	clock := ClockID("worker", "C")
	intervals := result.Poset.ByName("Interval")
	finals := result.Poset.ByName("Final")
	if len(intervals) != 1 || len(finals) != 1 {
		t.Fatalf("timed events missing: Interval=%d Final=%d", len(intervals), len(finals))
	}
	for _, check := range []struct {
		event         *gorapide.Event
		start, finish uint64
		value         int64
	}{
		{event: intervals[0], start: 0, finish: 2, value: 1},
		{event: finals[0], start: 3, finish: 6, value: 2},
	} {
		timing, related := check.event.Timing(clock)
		if !related || timing.Start != check.start || timing.Finish != check.finish {
			t.Fatalf("event %s timing=%#v,%v, want [%d,%d]", check.event.Name, timing, related, check.start, check.finish)
		}
		if value, _ := check.event.Param("n"); value != check.value {
			t.Fatalf("event %s captured n=%#v, want %d", check.event.Name, value, check.value)
		}
	}
	start := result.Poset.ByName("Start")[0]
	if !result.Poset.IsCausallyBefore(start.ID, intervals[0].ID) ||
		!result.Poset.IsCausallyBefore(intervals[0].ID, finals[0].ID) {
		t.Fatal("suspended continuation lost sequential process causality")
	}
	if len(result.ClockAdvances) != 3 ||
		result.ClockAdvances[0].To != "2" || result.ClockAdvances[1].To != "3" || result.ClockAdvances[2].To != "6" {
		t.Fatalf("clock advances=%#v, want deadlines 2, 3, 6", result.ClockAdvances)
	}
	if len(result.Firings) != 1 || len(result.Firings[0].Suspensions) != 3 ||
		len(result.Firings[0].Generated) != 2 || len(result.Firings[0].StateWrites) != 2 {
		t.Fatalf("timed firing audit=%#v", result.Firings)
	}
	if result.Firings[0].Suspensions[0].Kind != PauseTimingClause ||
		result.Firings[0].Suspensions[0].EventID == "" ||
		result.Firings[0].Suspensions[1].Kind != PauseTimingClause ||
		result.Firings[0].Suspensions[1].EventID != "" ||
		result.Firings[0].Suspensions[2].Kind != DelayTimingClause ||
		result.Firings[0].Suspensions[2].EventID == "" {
		t.Fatalf("action/timed-statement suspension distinction=%#v", result.Firings[0].Suspensions)
	}
	if len(result.State) != 1 || result.State[0].Value.Text != "2" || result.State[0].Version != 2 {
		t.Fatalf("state did not survive suspension: %#v", result.State)
	}
	augmented, err := result.AugmentedComputation()
	if err != nil {
		t.Fatal(err)
	}
	limits := ConsistentCutLimits{MaxCuts: 20, MaxOptionalOccurrences: 40}
	intervalCuts, err := augmented.ConsistentCutStateWitnesses([]string{string(intervals[0].ID)}, limits)
	if err != nil {
		t.Fatal(err)
	}
	finalCuts, err := augmented.ConsistentCutStateWitnesses([]string{string(finals[0].ID)}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if len(intervalCuts) != 1 || intervalCuts[0].State[0].Version != 1 ||
		len(finalCuts) != 1 || finalCuts[0].State[0].Version != 2 {
		t.Fatalf("timed program-point cut states interval=%#v final=%#v", intervalCuts, finalCuts)
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
		t.Fatal("pause/delay continuation did not replay byte-identically")
	}
}

func processDelayWindowArchitecture(t *testing.T, kind TimingClauseKind, timedStatement bool) *Architecture {
	t.Helper()
	architecture := NewArchitecture("delay-window-" + string(kind))
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("X").OutAction("Interval").
		OutAction("After").OutAction("Seen").OutAction("OtherSeen").Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := component.SetModuleProcessMode(ParallelProcesses); err != nil {
		t.Fatal(err)
	}
	var body []Statement
	if timedStatement {
		if kind == PauseTimingClause {
			body = []Statement{PauseFor("C", 2), CallAction("after", "After")}
		} else {
			body = []Statement{DelayFor("C", 2), CallAction("after", "After")}
		}
	} else if kind == PauseTimingClause {
		body = []Statement{CallActionPause("interval", "Interval", "C", 2)}
	} else {
		body = []Statement{CallActionDelay("interval", "Interval", "C", 2)}
	}
	process := Process("p").StartAt("run").States(
		AwaitState("run", Await("start").On(pattern.MatchEvent("Start")).Do(body...).Then("wait-x").Build()),
		AwaitState("wait-x", Await("x").On(pattern.MatchEvent("X")).Do(
			CallAction("seen", "Seen"),
		).Terminate().Build()),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
	observer := Process("observer").StartAt("wait-x").States(
		AwaitState("wait-x", Await("x").On(pattern.MatchEvent("X")).Do(
			CallAction("other-seen", "OtherSeen"),
		).Terminate().Build()),
	).Build()
	if err := component.AddDeclarativeProcess(observer); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture
}

func TestDelayMakesClosedIntervalEventsUnavailableOnlyToOwningProcess(t *testing.T) {
	for _, timedStatement := range []bool{false, true} {
		for _, kind := range []TimingClauseKind{PauseTimingClause, DelayTimingClause} {
			name := string(kind)
			if timedStatement {
				name += "-statement"
			} else {
				name += "-action"
			}
			t.Run(name, func(t *testing.T) {
				architecture := processDelayWindowArchitecture(t, kind, timedStatement)
				digest, err := architecture.DeterministicModelDigest()
				if err != nil {
					t.Fatal(err)
				}
				clock := ClockID("worker", "C")
				journal := NewExecutionJournal(digest, 20,
					InputEvent{Key: "start", Source: "worker", Action: "Start"},
					InputEvent{Key: "x", Source: "worker", Action: "X",
						Timings: []gorapide.EventTiming{{Clock: clock, Start: 1, Finish: 1}}},
				)
				result, err := architecture.ExecuteDeterministic(journal)
				if err != nil {
					t.Fatal(err)
				}
				seen := result.Poset.ByName("Seen")
				if kind == PauseTimingClause && len(seen) != 1 {
					t.Fatalf("pause removed an interval event from the process: Seen=%d", len(seen))
				}
				if kind == DelayTimingClause && len(seen) != 0 {
					t.Fatalf("delay left an interval event available to the process: Seen=%d", len(seen))
				}
				if len(result.Poset.ByName("X")) != 1 {
					t.Fatal("delay removed the event occurrence from the computation instead of process-local availability")
				}
				if len(result.Poset.ByName("OtherSeen")) != 1 {
					t.Fatal("delay leaked from the owning process and hid the event from another process")
				}
				if timedStatement {
					after := result.Poset.ByName("After")
					if len(after) != 1 {
						t.Fatal("timed statement failed to resume its following statement")
					}
					timing, related := after[0].Timing(clock)
					if !related || timing.Start != 2 || timing.Finish != 2 {
						t.Fatalf("post-suspension event timing=%#v,%v, want 2", timing, related)
					}
					if result.Firings[0].Suspensions[0].EventID != "" {
						t.Fatalf("timed statement generated an event: %#v", result.Firings[0].Suspensions[0])
					}
				}
			})
		}
	}
}

func TestPauseActionCapturesParametersBeforeSuspension(t *testing.T) {
	architecture := NewArchitecture("pause-parameter-capture")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("X").
		OutAction("Interval", P("n", "Integer")).Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := component.DeclareState(StateReference("value", "Integer", 1)); err != nil {
		t.Fatal(err)
	}
	if err := component.SetModuleProcessMode(ParallelProcesses); err != nil {
		t.Fatal(err)
	}
	pauser := Process("pauser").StartAt("start").States(
		AwaitState("start", Await("start").On(pattern.MatchEvent("Start")).Do(
			CallActionPause("interval", "Interval", "C", 2, StateParam("n", "value")),
		).Terminate().Build()),
	).Build()
	updater := Process("updater").StartAt("x").States(
		AwaitState("x", Await("x").On(pattern.MatchEvent("X")).Do(
			SetState("value", LiteralValue(9)),
		).Terminate().Build()),
	).Build()
	for _, process := range []*DeclarativeProcess{pauser, updater} {
		if err := component.AddDeclarativeProcess(process); err != nil {
			t.Fatal(err)
		}
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	clock := ClockID("worker", "C")
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
		InputEvent{Key: "x", Source: "worker", Action: "X",
			Timings: []gorapide.EventTiming{{Clock: clock, Start: 1, Finish: 1}}},
	))
	if err != nil {
		t.Fatal(err)
	}
	interval := result.Poset.ByName("Interval")
	if len(interval) != 1 {
		t.Fatalf("Interval events=%d", len(interval))
	}
	if value, _ := interval[0].Param("n"); value != int64(1) {
		t.Fatalf("pause action parameter=%#v, want call-time value 1", value)
	}
	if len(result.State) != 1 || result.State[0].Value.Text != "9" {
		t.Fatalf("parallel state update did not occur during suspension: %#v", result.State)
	}
}

func TestInGeneratedDuringPauseUsesActualProcessFrontier(t *testing.T) {
	architecture := NewArchitecture("in-during-pause")
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("InEvent").OutAction("PauseEvent").Build(), nil)
	for _, clock := range []string{"C1", "C2"} {
		if err := component.AddBasicClock(clock); err != nil {
			t.Fatal(err)
		}
	}
	if err := component.AddDeclarativeProcess(Process("p").StartAt("start").States(
		AwaitState("start", Await("start").On(pattern.MatchEvent("Start")).Do(
			CallActionIn("in", "InEvent", "C1", 1),
			CallActionPause("pause", "PauseEvent", "C2", 1),
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
	journal := NewExecutionJournal(digest, 20,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	inEvent := result.Poset.ByName("InEvent")[0]
	pauseEvent := result.Poset.ByName("PauseEvent")[0]
	if !result.Poset.IsCausallyBefore(inEvent.ID, pauseEvent.ID) {
		t.Fatal("canonical C1-first schedule did not refresh the paused process frontier from the generated in event")
	}
	explored, err := architecture.ExploreDeterministic(journal, ExplorationLimits{
		MaxExecutions: 8, MaxChoiceDepth: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 2 {
		t.Fatalf("independent in/pause deadlines yielded %d computations, complete=%v; want both actual generation orders", len(explored.Computations), explored.Complete)
	}
}

func TestIndependentProcessSuspensionClockChoicesAreExplorable(t *testing.T) {
	architecture := NewArchitecture("independent-process-suspensions")
	for _, componentID := range []string{"a", "b"} {
		component := NewComponent(componentID, Interface("Worker").OutAction("Never").OutAction("Done").Build(), nil)
		if err := component.AddBasicClock("C"); err != nil {
			t.Fatal(err)
		}
		if err := component.AddDeclarativeProcess(Process("p").StartAt("start").States(
			AwaitStateWithElse("start", AwaitElse("begin").Do(
				CallActionPause("done", "Done", "C", 1),
			).Terminate().Build(), Await("never").On(pattern.MatchEvent("Never")).NoEvents().Terminate().Build()),
		).Build()); err != nil {
			t.Fatal(err)
		}
		if err := architecture.AddComponent(component); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 20)
	defaultResult, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultResult.Choices) != 1 || defaultResult.Choices[0].Domain != "clock-advance" {
		t.Fatalf("independent suspension choice audit=%#v", defaultResult.Choices)
	}
	explored, err := architecture.ExploreDeterministic(journal, ExplorationLimits{
		MaxExecutions: 8, MaxChoiceDepth: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 1 {
		t.Fatalf("independent suspension exploration=%d computations, complete=%v; opposite clock schedules must collapse to one poset", len(explored.Computations), explored.Complete)
	}
	for _, computation := range explored.Computations {
		if len(computation.Schedule) != 1 {
			t.Fatalf("suspension exploration schedule=%#v", computation.Schedule)
		}
	}
}

func TestPauseDelayRejectUnsupportedOwners(t *testing.T) {
	build := func(t *testing.T, body Statement, asRule bool) *Architecture {
		t.Helper()
		architecture := NewArchitecture("bad-pause-delay")
		component := NewComponent("worker", Interface("Worker").
			OutAction("Start").OutAction("Interval").Build(), nil)
		_ = component.AddBasicClock("C")
		if asRule {
			_ = component.AddDeclarativeRule(Rule("r").On(pattern.MatchEvent("Start")).Do(body).Build())
		} else {
			_ = component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
				AwaitState("wait", Await("run").On(pattern.MatchEvent("Start")).Do(body).Terminate().Build()),
			).Build())
		}
		_ = architecture.AddComponent(component)
		return architecture
	}
	for _, test := range []struct {
		name   string
		body   Statement
		asRule bool
	}{
		{name: "ordinary rule", body: CallActionPause("interval", "Interval", "C", 1), asRule: true},
		{name: "nested ordinary rule", body: IfThen(LiteralValue(true), []Statement{
			CallActionDelay("interval", "Interval", "C", 1),
		}, nil), asRule: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := build(t, test.body, test.asRule).DeterministicModelDigest(); !errors.Is(err, ErrInvalidDeclarativeStatement) {
				t.Fatalf("got %v, want ErrInvalidDeclarativeStatement", err)
			}
		})
	}
}

func TestZeroDurationPauseDelayCompletesWithoutClockAdvance(t *testing.T) {
	architecture := timedProcessArchitecture(t, "zero-pause-delay",
		CallAction("before", "Final", LiteralParam("n", 0)),
		CallActionPause("pause", "Interval", "C", 0, LiteralParam("n", 1)),
		PauseFor("C", 0),
		CallActionDelay("delay", "Interval", "C", 0, LiteralParam("n", 2)),
		DelayFor("C", 0),
		CallAction("after", "Final", LiteralParam("n", 3)),
	)
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
	if len(result.ClockAdvances) != 0 || len(result.Clocks) != 1 || result.Clocks[0].Now != "0" {
		t.Fatalf("zero timing advanced the clock: clocks=%#v advances=%#v", result.Clocks, result.ClockAdvances)
	}
	clock := ClockID("worker", "C")
	intervals := result.Poset.ByName("Interval")
	if len(intervals) != 2 {
		t.Fatalf("zero-duration action events=%d, want 2", len(intervals))
	}
	for _, event := range intervals {
		timing, related := event.Timing(clock)
		if !related || timing.Start != 0 || timing.Finish != 0 {
			t.Fatalf("zero-duration event timing=%#v,%v", timing, related)
		}
	}
	finals := result.Poset.ByName("Final")
	before := eventWithInteger(t, finals, 0)
	pause := eventWithInteger(t, intervals, 1)
	delay := eventWithInteger(t, intervals, 2)
	after := eventWithInteger(t, finals, 3)
	if !result.Poset.IsCausallyBefore(before.ID, pause.ID) ||
		!result.Poset.IsCausallyBefore(pause.ID, delay.ID) ||
		!result.Poset.IsCausallyBefore(delay.ID, after.ID) {
		t.Fatal("zero-duration timing forms yielded or lost sequential causality")
	}
	if len(result.Firings) != 1 || len(result.Firings[0].Suspensions) != 4 ||
		len(result.Firings[0].Generated) != 4 {
		t.Fatalf("zero-duration audit=%#v", result.Firings)
	}
	for index, record := range result.Firings[0].Suspensions {
		if record.Start != "0" || record.Finish != "0" {
			t.Fatalf("suspension %d=%#v, want [0,0]", index, record)
		}
	}
	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := architecture.ReplayDeterministic(journal, expected); err != nil {
		t.Fatal(err)
	}
}

func TestZeroDelayWindowIsInclusiveAtCurrentTick(t *testing.T) {
	architecture := processDelayWindowArchitecture(t, DelayTimingClause, true)
	component, _ := architecture.Component("worker")
	// Replace the nonzero timed delay with a canonical zero-duration form in
	// the owning process while retaining the independent observer process.
	component.mu.Lock()
	for _, process := range component.processes {
		if process.ID == "p" {
			process.States[0].Alternatives[0].Body = &RuleBody{
				Statements: []Statement{DelayFor("C", 0), CallAction("after", "After")},
			}
		}
	}
	component.mu.Unlock()
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	clock := ClockID("worker", "C")
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
		InputEvent{Key: "x", Source: "worker", Action: "X",
			Timings: []gorapide.EventTiming{{Clock: clock, Start: 0, Finish: 0}}},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Seen")) != 0 {
		t.Fatal("zero-delay closed interval left a same-tick event available to its owning process")
	}
	if len(result.Poset.ByName("OtherSeen")) != 1 || len(result.Poset.ByName("X")) != 1 {
		t.Fatal("zero delay changed shared computation visibility")
	}
}

func TestRepeatedSameTickZeroSuspensionsHaveUniqueAuditIDs(t *testing.T) {
	architecture := NewArchitecture("repeated-zero-suspensions")
	component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	process := Process("p").StartAt("watch").States(
		WhenState("watch", pattern.MatchEvent("Start"), StatementBody(PauseFor("C", 0))),
	).Build()
	if err := component.AddDeclarativeProcess(process); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournal(digest, 20,
		InputEvent{Key: "start-1", Source: "worker", Action: "Start"},
		InputEvent{Key: "start-2", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Firings) != 2 || len(result.Firings[0].Suspensions) != 1 ||
		len(result.Firings[1].Suspensions) != 1 {
		t.Fatalf("repeated suspension audit=%#v", result.Firings)
	}
	first := result.Firings[0].Suspensions[0].SuspensionID
	second := result.Firings[1].Suspensions[0].SuspensionID
	if first == second {
		t.Fatalf("distinct same-tick activations reused suspension ID %q", first)
	}
	if len(result.ClockAdvances) != 0 {
		t.Fatalf("zero-duration repetition advanced a clock: %#v", result.ClockAdvances)
	}
}

func TestPauseDelayExecutionStableAcrossGOMAXPROCS(t *testing.T) {
	architecture := timedProcessArchitecture(t, "pause-delay-stability",
		CallActionPause("first", "Interval", "C", 2, LiteralParam("n", 1)),
		PauseFor("C", 0),
		CallActionDelay("instant", "Interval", "C", 0, LiteralParam("n", 0)),
		DelayFor("C", 1),
		CallActionPause("last", "Final", "C", 3, LiteralParam("n", 2)),
	)
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournal(digest, 20,
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
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
				t.Fatalf("pause/delay artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}
