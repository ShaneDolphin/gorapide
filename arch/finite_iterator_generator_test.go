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

func testFiniteIteratorGenerator(
	t *testing.T,
	name string,
	itemType gorapide.RapideType,
	items ...any,
) *FiniteIteratorGenerator {
	t.Helper()
	generator, err := NewFiniteIteratorGenerator(name, itemType, items...)
	if err != nil {
		t.Fatal(err)
	}
	return generator
}

func finiteIteratorGeneratorArchitecture(
	t *testing.T,
	items ...any,
) (*Architecture, *FiniteIteratorGenerator) {
	t.Helper()
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	generator := testFiniteIteratorGenerator(t, "Fresh_Items", integerType, items...)
	architecture := NewArchitecture("finite-iterator-generators")
	if err := architecture.AddFiniteIteratorGenerator(generator); err != nil {
		t.Fatal(err)
	}
	component := NewComponent("worker", Interface("Worker").
		OutAction("Go").
		OutAction("Emit", P("pass", "String"), P("value", "Integer")).
		OutAction("Done").Build(), nil)
	first := ForEachGeneratedIterator("I", generator,
		CallAction("first", "Emit", LiteralParam("pass", "first"), BindingParam("value", "I")),
		ExitLoop(),
	)
	second := ForEachGeneratedIterator("J", generator,
		CallAction("second", "Emit", LiteralParam("pass", "second"), BindingParam("value", "J")),
	)
	if err := component.AddDeclarativeRule(
		Rule("iterate").On(pattern.MatchEvent("Go")).Do(
			first, second, CallAction("done", "Done"),
		).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture, generator
}

func TestFiniteIteratorGeneratorAllocatesFreshModulesAndReplays(t *testing.T) {
	architecture, generator := finiteIteratorGeneratorArchitecture(t, int64(4), int64(5))
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 40},
		InputEvent{Key: "go", Source: "worker", Action: "Go"},
	)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	emitted := result.Poset.ByName("Emit")
	if len(emitted) != 3 {
		t.Fatalf("emitted=%d, want 3", len(emitted))
	}
	passes := map[string]map[int64]*gorapide.Event{}
	for _, event := range emitted {
		pass, _ := event.Param("pass")
		value, _ := event.Param("value")
		if passes[pass.(string)] == nil {
			passes[pass.(string)] = make(map[int64]*gorapide.Event)
		}
		passes[pass.(string)][value.(int64)] = event
	}
	if len(passes["first"]) != 1 || passes["first"][4] == nil ||
		len(passes["second"]) != 2 || passes["second"][4] == nil || passes["second"][5] == nil ||
		!result.Poset.IsCausallyBefore(passes["second"][4].ID, passes["second"][5].ID) {
		t.Fatalf("fresh generator values=%#v", passes)
	}

	moduleStarts := make(map[string]*gorapide.Event)
	for _, event := range result.Poset.ByName("Start") {
		if strings.HasPrefix(event.Source, "mod1-") {
			moduleStarts[event.Source] = event
		}
	}
	if len(moduleStarts) != 2 || len(result.Iterators) != 2 {
		t.Fatalf("module starts/iterator states=%d/%#v", len(moduleStarts), result.Iterators)
	}
	nextCounts := map[string]int{}
	for _, state := range result.Iterators {
		if moduleStarts[state.Module] == nil || state.ItemType != "Integer" || state.Cardinality != "2" {
			t.Fatalf("generated iterator state=%#v", state)
		}
		nextCounts[state.Next]++
		if state.Next == "1" && state.Exhausted {
			t.Fatalf("early-exit iterator marked exhausted: %#v", state)
		}
		if state.Next == "2" && !state.Exhausted {
			t.Fatalf("completed iterator not exhausted: %#v", state)
		}
	}
	if nextCounts["1"] != 1 || nextCounts["2"] != 1 {
		t.Fatalf("fresh iterator cursors=%#v", nextCounts)
	}
	for _, event := range distinctIteratorEvents(result.Poset.ByName("More'Call")) {
		provider := ""
		for _, observation := range event.ObservationViews() {
			if moduleStarts[observation.Source] != nil {
				provider = observation.Source
			}
		}
		if provider == "" {
			t.Fatalf("More call has no generated-module provider: %#v", event.ObservationViews())
		}
		start := moduleStarts[provider]
		if !result.Poset.IsCausallyBefore(start.ID, event.ID) {
			t.Fatalf("module Start does not precede More call for %s", start.Source)
		}
	}

	expected, err := result.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replay, err := architecture.ReplayDeterministic(journal, expected)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := result.MarshalCanonical()
	right, _ := replay.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatal("finite iterator-generator replay was not byte-identical")
	}

	prior := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prior)
	single, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	multi, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	singleBytes, _ := single.MarshalCanonical()
	multiBytes, _ := multi.MarshalCanonical()
	if !bytes.Equal(left, singleBytes) || !bytes.Equal(singleBytes, multiBytes) {
		t.Fatal("finite iterator-generator artifact changed with GOMAXPROCS")
	}
	if generator.Name() != "fresh_items" {
		t.Fatalf("normalized generator name=%q", generator.Name())
	}
}

func TestFiniteIteratorGeneratorCursorSurvivesProcessSuspension(t *testing.T) {
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	generator := testFiniteIteratorGenerator(t, "Resumable", integerType, int64(7), int64(8))
	architecture := NewArchitecture("resumable-iterator-generator")
	if err := architecture.AddFiniteIteratorGenerator(generator); err != nil {
		t.Fatal(err)
	}
	component := NewComponent("worker", Interface("Worker").
		OutAction("Go").OutAction("Tick", P("value", "Integer")).Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("go").On(pattern.MatchEvent("Go")).Do(
			ForEachGeneratedIterator("I", generator,
				PauseFor("C", 1),
				CallAction("tick", "Tick", BindingParam("value", "I")),
			),
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
	result, err := architecture.ExecuteDeterministic(NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 40},
		InputEvent{Key: "go", Source: "worker", Action: "Go"},
	))
	if err != nil {
		t.Fatal(err)
	}
	starts := result.Poset.ByName("Start")
	iteratorStarts := 0
	for _, event := range starts {
		if event.Source != ArchitectureInterfaceID {
			iteratorStarts++
		}
	}
	if len(result.Poset.ByName("Tick")) != 2 || iteratorStarts != 1 ||
		len(result.Iterators) != 1 || result.Iterators[0].Next != "2" || !result.Iterators[0].Exhausted {
		t.Fatalf("resumable generated iterator events/state=%d/%d/%#v",
			len(result.Poset.ByName("Tick")), iteratorStarts, result.Iterators)
	}
}

func TestFiniteIteratorGeneratorModelIsCanonical(t *testing.T) {
	left, _ := finiteIteratorGeneratorArchitecture(t, int64(4), int64(5))
	right, _ := finiteIteratorGeneratorArchitecture(t, int64(5), int64(4))
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest == rightDigest {
		t.Fatal("generator item order did not affect canonical model")
	}

	integerType, _ := gorapide.RapidePredefinedType("Integer")
	a := testFiniteIteratorGenerator(t, "A", integerType, int64(1))
	b := testFiniteIteratorGenerator(t, "B", integerType, int64(2))
	prior := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prior)
	var baseline string
	for iteration := 0; iteration < 12; iteration++ {
		if iteration == 6 {
			runtime.GOMAXPROCS(4)
		}
		architecture := NewArchitecture("generator-registration")
		generators := []*FiniteIteratorGenerator{a, b}
		if iteration%2 != 0 {
			generators = []*FiniteIteratorGenerator{b, a}
		}
		for _, generator := range generators {
			if err := architecture.AddFiniteIteratorGenerator(generator); err != nil {
				t.Fatal(err)
			}
		}
		digest, err := architecture.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if baseline == "" {
			baseline = digest
		} else if baseline != digest {
			t.Fatalf("registration order/GOMAXPROCS changed model: %s != %s", baseline, digest)
		}
	}
}

func TestFiniteIteratorGeneratorRejectsMalformedModels(t *testing.T) {
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	booleanType, _ := gorapide.RapidePredefinedType("Boolean")
	if _, err := NewFiniteIteratorGenerator("", integerType); !errors.Is(err, ErrInvalidFiniteIteratorGenerator) {
		t.Fatalf("empty name error=%v", err)
	}
	if _, err := NewFiniteIteratorGenerator("Wrong", integerType, true); !errors.Is(err, ErrInvalidFiniteIteratorGenerator) {
		t.Fatalf("wrong item error=%v", err)
	}
	structural, _ := gorapide.RapideIteratorType(integerType)
	if _, err := NewFiniteIteratorGenerator("Structural", structural); !errors.Is(err, ErrInvalidFiniteIteratorGenerator) {
		t.Fatalf("structural item type error=%v", err)
	}
	tooMany := make([]any, MaxFiniteRangeIteratorCardinality+1)
	for index := range tooMany {
		tooMany[index] = int64(index)
	}
	if _, err := NewFiniteIteratorGenerator("Large", integerType, tooMany...); !errors.Is(err, ErrInvalidFiniteIteratorGenerator) {
		t.Fatalf("cardinality error=%v", err)
	}

	integerGenerator := testFiniteIteratorGenerator(t, "Duplicate", integerType, int64(1))
	architecture := NewArchitecture("duplicate-generator")
	if err := architecture.AddFiniteIteratorGenerator(integerGenerator); err != nil {
		t.Fatal(err)
	}
	caseDuplicate := testFiniteIteratorGenerator(t, "DUPLICATE", integerType, int64(1))
	if err := architecture.AddFiniteIteratorGenerator(caseDuplicate); !errors.Is(err, ErrInvalidFiniteIteratorGenerator) {
		t.Fatalf("duplicate generator error=%v", err)
	}

	missing := NewArchitecture("missing-generator")
	component := NewComponent("worker", Interface("Worker").OutAction("Go").Build(), nil)
	if err := component.AddDeclarativeRule(
		Rule("iterate").On(pattern.MatchEvent("Go")).Do(
			ForEachGeneratedIterator("I", integerGenerator, NullStatement()),
		).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := missing.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err := missing.DeterministicModelDigest()
	if !errors.Is(err, ErrUnsupportedDeterministicModel) ||
		!strings.Contains(err.Error(), "has no declared implementation") {
		t.Fatalf("missing generator implementation error=%v", err)
	}

	booleanGenerator := testFiniteIteratorGenerator(t, "Duplicate", booleanType, true)
	mismatch := NewArchitecture("mismatched-generator")
	if err := mismatch.AddFiniteIteratorGenerator(booleanGenerator); err != nil {
		t.Fatal(err)
	}
	component = NewComponent("worker", Interface("Worker").OutAction("Go").Build(), nil)
	if err := component.AddDeclarativeRule(
		Rule("iterate").On(pattern.MatchEvent("Go")).Do(
			ForEachGeneratedIterator("I", integerGenerator, NullStatement()),
		).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := mismatch.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err = mismatch.DeterministicModelDigest()
	if !errors.Is(err, ErrUnsupportedDeterministicModel) ||
		!strings.Contains(err.Error(), "supplies Boolean items") {
		t.Fatalf("generator item-type mismatch error=%v", err)
	}
}

func TestProceduralIteratorFormsAllowOmittedIdentifier(t *testing.T) {
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	module := testFiniteIteratorModule(t, "anonymous", integerType, int64(1))
	generator := testFiniteIteratorGenerator(t, "Anonymous", integerType, int64(2))
	architecture := NewArchitecture("anonymous-iterators")
	if err := architecture.AddFiniteIteratorModule(module); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddFiniteIteratorGenerator(generator); err != nil {
		t.Fatal(err)
	}
	component := NewComponent("worker", Interface("Worker").
		OutAction("Go").OutAction("Tick", P("kind", "String")).Build(), nil)
	if err := component.AddDeclarativeRule(
		Rule("iterate").On(pattern.MatchEvent("Go")).Do(
			ForEachIntegerRange("", LiteralValue(1), LiteralValue(1),
				CallAction("range", "Tick", LiteralParam("kind", "range"))),
			ForEachIterator("", module,
				CallAction("module", "Tick", LiteralParam("kind", "module"))),
			ForEachGeneratedIterator("", generator,
				CallAction("generator", "Tick", LiteralParam("kind", "generator"))),
		).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := architecture.ExecuteDeterministic(NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 40},
		InputEvent{Key: "go", Source: "worker", Action: "Go"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Poset.ByName("Tick")) != 3 || len(result.Iterators) != 3 {
		t.Fatalf("anonymous iterator Tick/state=%d/%#v", len(result.Poset.ByName("Tick")), result.Iterators)
	}
}
