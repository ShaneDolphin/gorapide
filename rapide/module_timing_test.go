package rapide

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

const rangedModuleSource = `
type Driver is interface
  action out Start();
end interface Driver;

type Worker is interface
  action in Start();
  action out Ranged();
end interface Worker;

module TimedWorker() return Worker is
  C : Clock is MakeClock();
parallel
  when Start do
    Ranged() in C.Ticks range 0..2;
  end when;
end module TimedWorker;

architecture Timed() is
  driver : Driver;
  worker : Worker is TimedWorker();
connect
  driver.Start => worker.Start;
end architecture Timed;
`

func moduleJournal(t *testing.T, model *arch.Architecture) arch.ExecutionJournal {
	t.Helper()
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	return arch.NewExecutionJournal(digest, 20,
		arch.InputEvent{Key: "start", Source: "driver", Action: "Start"},
	)
}

func TestCompileModuleClockAndTimingRangeExecutesExploresAndReplays(t *testing.T) {
	model, err := Compile([]byte(rangedModuleSource), "Timed")
	if err != nil {
		t.Fatal(err)
	}
	journal := moduleJournal(t, model)
	canonical, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical.Choices) != 1 || !strings.HasPrefix(canonical.Choices[0].Domain, "timing-object:") ||
		len(canonical.Choices[0].Options) != 3 {
		t.Fatalf("source timing range choices=%#v", canonical.Choices)
	}
	clock := arch.ClockID("worker", "C")
	timing, related := canonical.Poset.ByName("Ranged")[0].Timing(clock)
	if !related || timing.Start != 0 || timing.Finish != 0 {
		t.Fatalf("canonical source range timing=%#v,%v", timing, related)
	}

	journal.Choices = []arch.ChoiceDecision{{
		Point: canonical.Choices[0].Point, Selection: canonical.Choices[0].Options[2],
	}}
	selected, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	timing, related = selected.Poset.ByName("Ranged")[0].Timing(clock)
	if !related || timing.Start != 2 || timing.Finish != 2 {
		t.Fatalf("selected source range timing=%#v,%v", timing, related)
	}
	digest, err := selected.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, digest)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := selected.MarshalCanonical()
	right, _ := replayed.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("source timing range replay was not byte-identical")
	}

	explored, err := model.ExploreDeterministic(moduleJournal(t, model), arch.ExplorationLimits{
		MaxExecutions: 8, MaxChoiceDepth: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 3 {
		t.Fatalf("source timing exploration=%d complete=%v, want three complete computations", len(explored.Computations), explored.Complete)
	}
	finishes := make(map[uint64]bool)
	for _, computation := range explored.Computations {
		event := computation.Result.Poset.ByName("Ranged")[0]
		timing, _ := event.Timing(clock)
		finishes[timing.Finish] = true
	}
	if !finishes[0] || !finishes[1] || !finishes[2] {
		t.Fatalf("source timing exploration missed members: %#v", finishes)
	}
}

func TestPublishedMakeClockSpellingCanonicalizesWithCompatibilityAlias(t *testing.T) {
	publishedSource := strings.ReplaceAll(rangedModuleSource, "MakeClock", "Make_Clock")
	published, err := Compile([]byte(publishedSource), "Timed")
	if err != nil {
		t.Fatal(err)
	}
	alias, err := Compile([]byte(rangedModuleSource), "Timed")
	if err != nil {
		t.Fatal(err)
	}
	publishedDigest, err := published.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	aliasDigest, err := alias.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if publishedDigest != aliasDigest {
		t.Fatalf("published Make_Clock and compatibility MakeClock spellings differ: %s != %s",
			publishedDigest, aliasDigest)
	}
	journal := moduleJournal(t, published)
	publishedResult, err := published.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	aliasResult, err := alias.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	publishedArtifact, _ := publishedResult.MarshalCanonical()
	aliasArtifact, _ := aliasResult.MarshalCanonical()
	if !bytes.Equal(publishedArtifact, aliasArtifact) {
		t.Fatal("published Make_Clock and compatibility MakeClock spellings produced different artifacts")
	}
	caseVariant := strings.ReplaceAll(rangedModuleSource, "MakeClock", "make_clock")
	variant, err := Compile([]byte(caseVariant), "Timed")
	if err != nil {
		t.Fatal(err)
	}
	variantDigest, err := variant.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if variantDigest != publishedDigest {
		t.Fatalf("case-insensitive published clock constructor changed model identity: %s != %s",
			variantDigest, publishedDigest)
	}
}

const fixedTimingModuleSource = `
type Driver is interface action out Start(); end interface Driver;
type Worker is interface
  action in Start();
  action out Zero(); action out Scheduled(); action out Paused();
  action out Delayed(); action out Finished();
end interface Worker;
module TimedWorker() return Worker is
  C : Clock is MakeClock();
serial
  when Start do
    Zero() in C.Ticks(0);
    Paused() pause C.Ticks(3);
    pause C.Ticks(1);
    Delayed() delay C.Ticks(2);
    delay C.Ticks(1);
    Finished();
    Scheduled() in C.Ticks(2);
  end when;
end module TimedWorker;
architecture Timed() is driver : Driver; worker : Worker is TimedWorker(); connect
  driver.Start to worker.Start;
end architecture Timed;
`

func TestCompileFixedTicksTimingObjectsMatchSingletonSubtypesAndReplay(t *testing.T) {
	fixed, err := Compile([]byte(fixedTimingModuleSource), "Timed")
	if err != nil {
		t.Fatal(err)
	}
	rangeSource := strings.NewReplacer(
		"C.Ticks(0)", "C.Ticks range 0..0",
		"C.Ticks(1)", "C.Ticks range 1..1",
		"C.Ticks(2)", "C.Ticks range 2..2",
		"C.Ticks(3)", "C.Ticks range 3..3",
	).Replace(fixedTimingModuleSource)
	ranged, err := Compile([]byte(rangeSource), "Timed")
	if err != nil {
		t.Fatal(err)
	}
	fixedDigest, err := fixed.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rangeDigest, err := ranged.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if fixedDigest != rangeDigest {
		t.Fatalf("fixed Ticks object and singleton subtype differ: %s != %s", fixedDigest, rangeDigest)
	}
	journal := arch.NewExecutionJournal(fixedDigest, 80,
		arch.InputEvent{Key: "start", Source: "driver", Action: "Start"},
	)
	result, err := fixed.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Choices) != 0 {
		t.Fatalf("fixed timing objects introduced a language choice: %#v", result.Choices)
	}
	clock := arch.ClockID("worker", "C")
	checks := []struct {
		name          string
		start, finish uint64
	}{
		{name: "Zero", start: 0, finish: 0},
		{name: "Paused", start: 0, finish: 3},
		{name: "Delayed", start: 4, finish: 6},
		{name: "Finished", start: 7, finish: 7},
		{name: "Scheduled", start: 9, finish: 9},
	}
	for _, check := range checks {
		events := result.Poset.ByName(check.name)
		if len(events) != 1 {
			t.Fatalf("%s events=%d, want one", check.name, len(events))
		}
		timing, related := events[0].Timing(clock)
		if !related || timing.Start != check.start || timing.Finish != check.finish {
			t.Fatalf("%s timing=%#v,%v, want [%d,%d]", check.name, timing, related, check.start, check.finish)
		}
	}
	if len(result.ClockAdvances) != 5 || result.ClockAdvances[0].To != "3" ||
		result.ClockAdvances[1].To != "4" || result.ClockAdvances[2].To != "6" ||
		result.ClockAdvances[3].To != "7" || result.ClockAdvances[4].To != "9" {
		t.Fatalf("fixed timing clock advances=%#v", result.ClockAdvances)
	}
	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	rangeResult, err := ranged.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	rangeArtifact, err := rangeResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact, rangeArtifact) {
		t.Fatal("fixed Ticks object and singleton subtype produced different artifacts")
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := fixed.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayArtifact) {
		t.Fatal("fixed Ticks timing did not replay byte-identically")
	}
	explored, err := fixed.ExploreDeterministic(journal, arch.ExplorationLimits{MaxExecutions: 8, MaxChoiceDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 1 {
		t.Fatalf("fixed timing exploration=%#v, want one complete computation", explored)
	}

	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for iteration := 0; iteration < 3; iteration++ {
			repeated, err := Compile([]byte(fixedTimingModuleSource), "Timed")
			if err != nil {
				t.Fatal(err)
			}
			repeatedResult, err := repeated.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			repeatedArtifact, _ := repeatedResult.MarshalCanonical()
			if !bytes.Equal(artifact, repeatedArtifact) {
				t.Fatalf("fixed timing artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

func TestCompileClosedTicksExpressionsUseModuleConstantsAndCanonicalValues(t *testing.T) {
	source := `
type Driver is interface action out Start(); end interface Driver;
type Worker is interface
  action in Start();
  action out Paused(); action out Finished();
end interface Worker;
module TimedWorker(Base : Integer) return Worker is
  Delay : Integer is Base + 1;
  C : Clock is MakeClock();
serial
  when Start do
    Paused() pause C.Ticks(Delay);
    pause C.Ticks(Delay - Base);
    Finished() in C.Ticks(Base + Delay);
  end when;
end module TimedWorker;
architecture Timed() is
  driver : Driver;
  worker : Worker is TimedWorker(1);
connect driver.Start to worker.Start;
end architecture Timed;
`
	expressionModel, err := Compile([]byte(source), "Timed")
	if err != nil {
		t.Fatal(err)
	}
	literalSource := strings.NewReplacer(
		"C.Ticks(Delay)", "C.Ticks(2)",
		"C.Ticks(Delay - Base)", "C.Ticks(1)",
		"C.Ticks(Base + Delay)", "C.Ticks(3)",
	).Replace(source)
	literalModel, err := Compile([]byte(literalSource), "Timed")
	if err != nil {
		t.Fatal(err)
	}
	expressionDigest, err := expressionModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	literalDigest, err := literalModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if expressionDigest != literalDigest {
		t.Fatalf("closed and literal Ticks objects differ: %s != %s", expressionDigest, literalDigest)
	}

	journal := arch.NewExecutionJournal(expressionDigest, 40,
		arch.InputEvent{Key: "start", Source: "driver", Action: "Start"},
	)
	expressionResult, err := expressionModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(expressionResult.Choices) != 0 {
		t.Fatalf("closed timing expressions introduced a language choice: %#v", expressionResult.Choices)
	}
	clock := arch.ClockID("worker", "C")
	checks := []struct {
		name          string
		start, finish uint64
	}{
		{name: "Paused", start: 0, finish: 2},
		{name: "Finished", start: 6, finish: 6},
	}
	for _, check := range checks {
		events := expressionResult.Poset.ByName(check.name)
		if len(events) != 1 {
			t.Fatalf("%s events=%d, want one", check.name, len(events))
		}
		timing, related := events[0].Timing(clock)
		if !related || timing.Start != check.start || timing.Finish != check.finish {
			t.Fatalf("%s timing=%#v,%v, want [%d,%d]", check.name, timing, related, check.start, check.finish)
		}
	}
	expressionArtifact, err := expressionResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	literalResult, err := literalModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	literalArtifact, err := literalResult.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expressionArtifact, literalArtifact) {
		t.Fatal("closed and literal Ticks objects produced different artifacts")
	}
	artifactDigest, err := expressionResult.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := expressionModel.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(expressionArtifact, replayArtifact) {
		t.Fatal("closed timing expression did not replay byte-identically")
	}
	explored, err := expressionModel.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 8, MaxChoiceDepth: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 1 {
		t.Fatalf("closed timing exploration=%#v, want one complete computation", explored)
	}

	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for iteration := 0; iteration < 3; iteration++ {
			repeated, err := Compile([]byte(source), "Timed")
			if err != nil {
				t.Fatal(err)
			}
			repeatedResult, err := repeated.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			repeatedArtifact, _ := repeatedResult.MarshalCanonical()
			if !bytes.Equal(expressionArtifact, repeatedArtifact) {
				t.Fatalf("closed timing artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

func TestCompileClosedTicksRangeBoundsUseModuleConstantsAndCanonicalDomains(t *testing.T) {
	source := `
type Driver is interface action out Start(); end interface Driver;
type Worker is interface action in Start(); action out Ranged(); end interface Worker;
module TimedWorker(Base : Integer) return Worker is
  Limit : Integer is Base + 2;
  C : Clock is MakeClock();
parallel
  when Start do Ranged() in C.Ticks range Base..Limit; end when;
end module TimedWorker;
architecture Timed() is
  driver : Driver;
  worker : Worker is TimedWorker(1);
connect driver.Start to worker.Start;
end architecture Timed;
`
	expressionModel, err := Compile([]byte(source), "Timed")
	if err != nil {
		t.Fatal(err)
	}
	literalSource := strings.Replace(source, "C.Ticks range Base..Limit", "C.Ticks range 1..3", 1)
	literalModel, err := Compile([]byte(literalSource), "Timed")
	if err != nil {
		t.Fatal(err)
	}
	expressionDigest, err := expressionModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	literalDigest, err := literalModel.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if expressionDigest != literalDigest {
		t.Fatalf("closed and literal Ticks ranges differ: %s != %s", expressionDigest, literalDigest)
	}

	journal := arch.NewExecutionJournal(expressionDigest, 30,
		arch.InputEvent{Key: "start", Source: "driver", Action: "Start"},
	)
	canonical, err := expressionModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical.Choices) != 1 || len(canonical.Choices[0].Options) != 3 ||
		!strings.HasPrefix(canonical.Choices[0].Domain, "timing-object:") {
		t.Fatalf("closed timing range choices=%#v, want three objects", canonical.Choices)
	}
	canonicalArtifact, err := canonical.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	literal, err := literalModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	literalArtifact, err := literal.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonicalArtifact, literalArtifact) {
		t.Fatal("closed and literal Ticks ranges produced different canonical computations")
	}

	journal.Choices = []arch.ChoiceDecision{{
		Point: canonical.Choices[0].Point, Selection: canonical.Choices[0].Options[2],
	}}
	selected, err := expressionModel.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	timing, related := selected.Poset.ByName("Ranged")[0].Timing(arch.ClockID("worker", "C"))
	if !related || timing.Start != 3 || timing.Finish != 3 {
		t.Fatalf("selected closed range timing=%#v,%v, want [3,3]", timing, related)
	}
	artifactDigest, err := selected.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := expressionModel.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	selectedArtifact, _ := selected.MarshalCanonical()
	replayArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(selectedArtifact, replayArtifact) {
		t.Fatal("selected closed timing range did not replay byte-identically")
	}

	explored, err := expressionModel.ExploreDeterministic(
		arch.NewExecutionJournal(expressionDigest, 30,
			arch.InputEvent{Key: "start", Source: "driver", Action: "Start"}),
		arch.ExplorationLimits{MaxExecutions: 8, MaxChoiceDepth: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 3 {
		t.Fatalf("closed timing range exploration=%d complete=%v, want three", len(explored.Computations), explored.Complete)
	}

	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for iteration := 0; iteration < 3; iteration++ {
			repeated, err := Compile([]byte(source), "Timed")
			if err != nil {
				t.Fatal(err)
			}
			repeatedResult, err := repeated.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			repeatedArtifact, _ := repeatedResult.MarshalCanonical()
			if !bytes.Equal(selectedArtifact, repeatedArtifact) {
				t.Fatalf("closed timing range changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

func TestNamedDependentTicksObjectLowersToExactOwningClockAndFixedValue(t *testing.T) {
	source := `
type Driver is interface action out Start(); end interface Driver;
type Worker is interface
  action in Start(); action out Paused(); action out Delayed(); action out Finished();
end interface Worker;
module TimedWorker(Base : Integer) return Worker is
  C : Clock is Make_Clock();
  Two : C.Ticks is Base + 1;
serial
  when Start do
    Paused() pause Two;
    pause Two;
    Delayed() delay Two;
    Finished() in Two;
  end when;
end module TimedWorker;
architecture Timed() is
  driver : Driver;
  worker : Worker is TimedWorker(1);
connect driver.Start to worker.Start;
end architecture Timed;
`
	named, err := Compile([]byte(source), "Timed")
	if err != nil {
		t.Fatal(err)
	}
	literalSource := strings.Replace(source, "  Two : C.Ticks is Base + 1;\n", "", 1)
	literalSource = strings.ReplaceAll(literalSource, " Two;", " C.Ticks(2);")
	literal, err := Compile([]byte(literalSource), "Timed")
	if err != nil {
		t.Fatal(err)
	}
	namedDigest, err := named.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	literalDigest, err := literal.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if namedDigest != literalDigest {
		t.Fatalf("named dependent Ticks object and fixed literal differ: %s != %s", namedDigest, literalDigest)
	}
	journal := arch.NewExecutionJournal(namedDigest, 50,
		arch.InputEvent{Key: "start", Source: "driver", Action: "Start"},
	)
	result, err := named.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Choices) != 0 {
		t.Fatalf("named fixed Ticks object introduced a choice: %#v", result.Choices)
	}
	clock := arch.ClockID("worker", "C")
	checks := []struct {
		name          string
		start, finish uint64
	}{
		{name: "Paused", start: 0, finish: 2},
		{name: "Delayed", start: 4, finish: 6},
		{name: "Finished", start: 8, finish: 8},
	}
	for _, check := range checks {
		events := result.Poset.ByName(check.name)
		if len(events) != 1 {
			t.Fatalf("%s events=%d, want one", check.name, len(events))
		}
		timing, related := events[0].Timing(clock)
		if !related || timing.Start != check.start || timing.Finish != check.finish {
			t.Fatalf("%s timing=%#v,%v, want [%d,%d]", check.name, timing, related, check.start, check.finish)
		}
	}
	namedArtifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	literalResult, err := literal.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	literalArtifact, _ := literalResult.MarshalCanonical()
	if !bytes.Equal(namedArtifact, literalArtifact) {
		t.Fatal("named dependent Ticks object and fixed literal produced different artifacts")
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := named.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(namedArtifact, replayArtifact) {
		t.Fatal("named dependent Ticks object did not replay byte-identically")
	}
	explored, err := named.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 8, MaxChoiceDepth: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 1 {
		t.Fatalf("named dependent Ticks exploration=%#v, want one computation", explored)
	}

	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for iteration := 0; iteration < 3; iteration++ {
			repeated, err := Compile([]byte(source), "Timed")
			if err != nil {
				t.Fatal(err)
			}
			repeatedResult, err := repeated.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			repeatedArtifact, _ := repeatedResult.MarshalCanonical()
			if !bytes.Equal(namedArtifact, repeatedArtifact) {
				t.Fatalf("named dependent Ticks artifact changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

func TestOrdinaryModuleProcessEntersOnceSuspendsTerminatesAndFeedsWhen(t *testing.T) {
	source := []byte(`
type Worker is interface
  action out Started(); action out Finished(); action out Seen();
end interface Worker;
module TimedWorker() return Worker is
  C : Clock is Make_Clock();
  Two : C.Ticks is 2;
parallel
  Started();
  Finished() pause Two;
||
  when Finished do Seen(); end when;
end module TimedWorker;
architecture Timed() is worker : Worker is TimedWorker(); end architecture Timed;
`)
	model, err := Compile(source, "Timed")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 30)
	result, err := model.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	started := result.Poset.ByName("Started")
	finished := result.Poset.ByName("Finished")
	seen := result.Poset.ByName("Seen")
	if len(started) != 1 || len(finished) != 1 || len(seen) != 1 {
		t.Fatalf("ordinary process events Started=%d Finished=%d Seen=%d", len(started), len(finished), len(seen))
	}
	if !result.Poset.IsCausallyBefore(started[0].ID, finished[0].ID) ||
		!result.Poset.IsCausallyBefore(finished[0].ID, seen[0].ID) {
		t.Fatal("ordinary entry process or following when lost source-defined causality")
	}
	timing, related := finished[0].Timing(arch.ClockID("worker", "C"))
	if !related || timing.Start != 0 || timing.Finish != 2 {
		t.Fatalf("ordinary timed process event=%#v,%v, want [0,2]", timing, related)
	}
	terminated := 0
	suspended := 0
	for _, process := range result.Processes {
		if process.Terminated {
			terminated++
		} else if process.State == "when" {
			suspended++
		}
	}
	if len(result.Processes) != 2 || terminated != 1 || suspended != 1 {
		t.Fatalf("ordinary/reactive final process states=%#v", result.Processes)
	}
	if len(result.Choices) != 0 {
		t.Fatalf("one-shot ordinary process introduced a language choice: %#v", result.Choices)
	}
	artifact, err := result.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := model.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayArtifact, _ := replayed.MarshalCanonical()
	if !bytes.Equal(artifact, replayArtifact) {
		t.Fatal("ordinary module process did not replay byte-identically")
	}
	explored, err := model.ExploreDeterministic(journal, arch.ExplorationLimits{
		MaxExecutions: 8, MaxChoiceDepth: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !explored.Complete || len(explored.Computations) != 1 {
		t.Fatalf("ordinary module process exploration=%#v, want one computation", explored)
	}

	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for iteration := 0; iteration < 3; iteration++ {
			repeated, err := Compile(source, "Timed")
			if err != nil {
				t.Fatal(err)
			}
			repeatedResult, err := repeated.ExecuteDeterministic(journal)
			if err != nil {
				t.Fatal(err)
			}
			repeatedArtifact, _ := repeatedResult.MarshalCanonical()
			if !bytes.Equal(artifact, repeatedArtifact) {
				t.Fatalf("ordinary module process changed at GOMAXPROCS=%d iteration=%d", processors, iteration)
			}
		}
	}
}

func TestCompileModulePauseDelayAndTimedStatementResumeExactIntervals(t *testing.T) {
	source := []byte(`
type Driver is interface
  action out Start();
end interface Driver;

type Worker is interface
  action in Start();
  action out Interval();
  action out Final();
end interface Worker;

module TimedWorker() return Worker is
  C : Clock is MakeClock();
serial
  when Start do
    Interval() pause c.Ticks range 2..2;
    pause C.Ticks range 1..1;
    Final() delay C.Ticks range 3..3;
  end when;
end module TimedWorker;

architecture Timed() is
  driver : Driver;
  worker : Worker is TimedWorker();
connect
  driver.Start => worker.Start;
end architecture Timed;
`)
	model, err := Compile(source, "Timed")
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.ExecuteDeterministic(moduleJournal(t, model))
	if err != nil {
		t.Fatal(err)
	}
	clock := arch.ClockID("worker", "C")
	checks := []struct {
		name          string
		start, finish uint64
	}{
		{name: "Interval", start: 0, finish: 2},
		{name: "Final", start: 3, finish: 6},
	}
	for _, check := range checks {
		events := result.Poset.ByName(check.name)
		if len(events) != 1 {
			t.Fatalf("%s events=%d, want one", check.name, len(events))
		}
		timing, related := events[0].Timing(clock)
		if !related || timing.Start != check.start || timing.Finish != check.finish {
			t.Fatalf("%s timing=%#v,%v, want [%d,%d]", check.name, timing, related, check.start, check.finish)
		}
	}
	var processFiring *arch.FiringRecord
	for index := range result.Firings {
		if result.Firings[index].Transition == "process" {
			processFiring = &result.Firings[index]
		}
	}
	if processFiring == nil || len(processFiring.Suspensions) != 3 ||
		len(result.ClockAdvances) != 3 || result.ClockAdvances[0].To != "2" ||
		result.ClockAdvances[1].To != "3" || result.ClockAdvances[2].To != "6" {
		t.Fatalf("source pause/delay audit: firings=%#v advances=%#v", result.Firings, result.ClockAdvances)
	}
}

func TestModuleProcessAndClockDeclarationOrderDoesNotChangeModel(t *testing.T) {
	build := func(clockOrder, processOrder string) string {
		return `
type Worker is interface
  action in StartA(); action in StartB();
  action out A(); action out B();
end interface Worker;
module M() return Worker is
` + clockOrder + `
parallel
` + processOrder + `
end module M;
architecture System() is worker : Worker is M(); end architecture System;
`
	}
	left, err := Compile([]byte(build(
		"  C : Clock is MakeClock();\n  D : Clock is MakeClock();",
		"  when StartA do A() in C.Ticks range 1..1; end when;\n||\n  when StartB do B() in D.Ticks range 1..1; end when;",
	)), "System")
	if err != nil {
		t.Fatal(err)
	}
	right, err := Compile([]byte(build(
		"  D : Clock is MakeClock();\n  C : Clock is MakeClock();",
		"  when StartB do B() in D.Ticks range 1..1; end when;\n||\n  when StartA do A() in C.Ticks range 1..1; end when;",
	)), "System")
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("module declaration order changed model identity: %s != %s", leftDigest, rightDigest)
	}
}

func TestModuleTimingSourceRejectsUnsupportedOrInvalidFormsExplicitly(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "undeclared clock",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is parallel when Start do A() in Missing.Ticks range 1..2; end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: `has no basic clock "Missing"`,
		},
		{
			name: "empty range",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is C : Clock is MakeClock(); parallel when Start do A() in C.Ticks range 1 + 1..1; end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "requires Timing_Error support",
		},
		{
			name: "Boolean range bound",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is C : Clock is MakeClock(); parallel when Start do A() in C.Ticks range true..2; end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "timing range lower-bound expression has type Boolean",
		},
		{
			name: "negative range bound",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is C : Clock is MakeClock(); parallel when Start do A() in C.Ticks range 0..1 - 2; end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "timing range upper-bound expression evaluates to -1",
		},
		{
			name: "bare Ticks type pending",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is C : Clock is MakeClock(); parallel when Start do A() in C.Ticks; end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "named and general subtype timing expressions",
		},
		{
			name: "undeclared named timing object",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is C : Clock is Make_Clock(); parallel when Start do A() in Missing; end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: `named timing object "Missing" is not declared`,
		},
		{
			name: "ordinary Integer is not dependent Ticks",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is Delay : Integer is 2; C : Clock is Make_Clock(); parallel when Start do A() in Delay; end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: `timing expression name "Delay" has type Integer`,
		},
		{
			name: "timing object undeclared clock",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is Two : Missing.Ticks is 2; parallel when Start do A() in Two; end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: `module timing object "Two" refers to undeclared clock "Missing"`,
		},
		{
			name: "timing object Boolean initializer",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is C : Clock is Make_Clock(); Two : C.Ticks is true; parallel when Start do A() in Two; end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "module timing object Two expression has type Boolean",
		},
		{
			name: "timing object negative initializer",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is C : Clock is Make_Clock(); Two : C.Ticks is 1 - 2; parallel when Start do A() in Two; end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "module timing object Two expression evaluates to -1",
		},
		{
			name: "fixed Boolean expression",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is C : Clock is MakeClock(); parallel when Start do A() in C.Ticks(true); end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "fixed timing expression has type Boolean",
		},
		{
			name: "negative fixed expression",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is C : Clock is MakeClock(); parallel when Start do A() in C.Ticks(1 - 2); end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "want a nonnegative Integer",
		},
		{
			name: "open fixed expression",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is C : Clock is MakeClock(); parallel when Start do A() in C.Ticks(Missing); end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: `expression name "Missing" is not declared`,
		},
		{
			name: "mutable fixed expression",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is Delay : var Integer := 2; C : Clock is MakeClock(); parallel when Start do A() in C.Ticks($Delay); end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "behavior state $Delay is not declared",
		},
		{
			name: "fixed tick overflow",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is C : Clock is MakeClock(); parallel when Start do A() in C.Ticks(18446744073709551616); end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "fixed timing tick is outside C.Ticks",
		},
		{
			name: "module actual missing",
			source: `type W is interface action in Start(); end interface W;
module M(n : Integer) return W is end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "expects 1 actual parameters but component \"w\" supplies 0",
		},
		{
			name: "module return mismatch",
			source: `type A is interface end interface A; type B is interface end interface B;
module M() return A is end module M;
architecture X() is b : B is M(); end architecture X;`,
			want: "does not exactly match",
		},
		{
			name: "range bound",
			source: `type W is interface action in Start(); action out A(); end interface W;
module M() return W is C : Clock is MakeClock(); parallel when Start do A() in C.Ticks range 0..128 + 128; end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "exceeds supported cardinality 256",
		},
		{
			name: "empty when body",
			source: `type W is interface action in Start(); end interface W;
module M() return W is parallel when Start do end when; end module M;
architecture X() is w : W is M(); end architecture X;`,
			want: "module process statement",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "X")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want diagnostic containing %q", err, test.want)
			}
			var syntax *SyntaxError
			var typeFailure *TypeError
			if !errors.As(err, &syntax) && !errors.As(err, &typeFailure) {
				t.Fatalf("diagnostic %T is not a typed source error", err)
			}
		})
	}
}

func TestSourceModuleTimingStableAcrossGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for _, processors := range []int{1, 8} {
		runtime.GOMAXPROCS(processors)
		for run := 0; run < 10; run++ {
			model, err := Compile([]byte(rangedModuleSource), "Timed")
			if err != nil {
				t.Fatal(err)
			}
			result, err := model.ExecuteDeterministic(moduleJournal(t, model))
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
				t.Fatalf("source module timing changed under GOMAXPROCS=%d run=%d", processors, run)
			}
		}
	}
}
