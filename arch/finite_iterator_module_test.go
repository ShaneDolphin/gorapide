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

func testFiniteIteratorModule(
	t *testing.T,
	occurrence string,
	itemType gorapide.RapideType,
	items ...any,
) *FiniteIteratorModule {
	t.Helper()
	module, err := gorapide.NewRapideModuleValue(gorapide.ModuleAllocationProvenance{
		Profile: CompatibilityProfile, Model: "finite-iterator-modules", Parent: "root",
		Generator: "FiniteIterator", Occurrence: occurrence,
	})
	if err != nil {
		t.Fatal(err)
	}
	iterator, err := NewFiniteIteratorModule(module, itemType, items...)
	if err != nil {
		t.Fatal(err)
	}
	return iterator
}

func finiteIteratorModuleArchitecture(t *testing.T, items ...any) (*Architecture, *FiniteIteratorModule) {
	t.Helper()
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	iterator := testFiniteIteratorModule(t, "shared", integerType, items...)
	architecture := NewArchitecture("finite-iterator-modules")
	if err := architecture.AddFiniteIteratorModule(iterator); err != nil {
		t.Fatal(err)
	}
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Emit", P("value", "Integer")).OutAction("Done").Build(), nil)
	first := ForEachIterator("I", iterator,
		CallAction("first", "Emit", BindingParam("value", "I")),
		ExitWhen(EqualValues(BoundValue("I"), LiteralValue(3))),
	)
	rest := ForEachIterator("J", iterator,
		CallAction("rest", "Emit", BindingParam("value", "J")),
	)
	if err := component.AddDeclarativeRule(
		Rule("iterate").On(pattern.MatchEvent("Start")).Do(
			first, rest, CallAction("done", "Done"),
		).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := architecture.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	return architecture, iterator
}

func TestFiniteIteratorModuleExecutesSharedCursorAndReplays(t *testing.T) {
	architecture, iterator := finiteIteratorModuleArchitecture(t, int64(3), int64(1), int64(2))
	digest, err := architecture.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := NewExecutionJournalWithLimits(digest,
		ExecutionLimits{MaxFirings: 20, MaxStatements: 32},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	)
	result, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	if result.StatementSteps != 14 || len(result.Iterators) != 1 {
		t.Fatalf("statement/iterator audit=%d/%#v", result.StatementSteps, result.Iterators)
	}
	state := result.Iterators[0]
	if state.Module != iterator.Module().Identity() || state.ItemType != "Integer" ||
		state.Cardinality != "3" || state.Next != "3" || !state.Exhausted {
		t.Fatalf("final iterator state=%#v", state)
	}
	for _, lifecycle := range result.Modules {
		if lifecycle.ModuleID == iterator.Module().Identity() {
			t.Fatalf("externally allocated iterator acquired fabricated execution lifecycle=%#v", lifecycle)
		}
	}
	emitted := result.Poset.ByName("Emit")
	if len(emitted) != 3 || len(distinctIteratorEvents(result.Poset.ByName("More'Call"))) != 4 ||
		len(distinctIteratorEvents(result.Poset.ByName("Item'Call"))) != 3 {
		t.Fatalf("emit/more/item counts=%d/%d/%d", len(emitted),
			len(distinctIteratorEvents(result.Poset.ByName("More'Call"))),
			len(distinctIteratorEvents(result.Poset.ByName("Item'Call"))))
	}
	byValue := make(map[int64]*gorapide.Event)
	for _, event := range emitted {
		value, _ := event.Param("value")
		byValue[value.(int64)] = event
	}
	if byValue[3] == nil || byValue[1] == nil || byValue[2] == nil ||
		!result.Poset.IsCausallyBefore(byValue[3].ID, byValue[1].ID) ||
		!result.Poset.IsCausallyBefore(byValue[1].ID, byValue[2].ID) {
		t.Fatal("shared iterator did not retain declared order across exit and second for statement")
	}
	for _, event := range distinctIteratorEvents(result.Poset.ByName("Item'Return")) {
		if event.Source != iterator.Module().Identity() {
			t.Fatalf("Item provider=%q, want %q", event.Source, iterator.Module().Identity())
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
		t.Fatal("shared finite Iterator(T) replay was not byte-identical")
	}

	prior := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prior)
	singleProcessor, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	multiProcessor, err := architecture.ExecuteDeterministic(journal)
	if err != nil {
		t.Fatal(err)
	}
	singleBytes, _ := singleProcessor.MarshalCanonical()
	multiBytes, _ := multiProcessor.MarshalCanonical()
	if !bytes.Equal(left, singleBytes) || !bytes.Equal(singleBytes, multiBytes) {
		t.Fatal("shared finite Iterator(T) artifact changed with GOMAXPROCS")
	}
}

func TestFiniteIteratorModuleCursorSurvivesProcessSuspension(t *testing.T) {
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	iterator := testFiniteIteratorModule(t, "resumable", integerType, int64(4), int64(5))
	architecture := NewArchitecture("resumable-iterator-module")
	if err := architecture.AddFiniteIteratorModule(iterator); err != nil {
		t.Fatal(err)
	}
	component := NewComponent("worker", Interface("Worker").
		OutAction("Start").OutAction("Tick", P("value", "Integer")).Build(), nil)
	if err := component.AddBasicClock("C"); err != nil {
		t.Fatal(err)
	}
	if err := component.AddDeclarativeProcess(Process("p").StartAt("wait").States(
		AwaitState("wait", Await("start").On(pattern.MatchEvent("Start")).Do(
			ForEachIterator("I", iterator,
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
		ExecutionLimits{MaxFirings: 20, MaxStatements: 32},
		InputEvent{Key: "start", Source: "worker", Action: "Start"},
	))
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[int64]bool)
	for _, event := range result.Poset.ByName("Tick") {
		value, _ := event.Param("value")
		values[value.(int64)] = true
	}
	if !values[4] || !values[5] || len(result.ClockAdvances) != 2 ||
		len(result.Iterators) != 1 || result.Iterators[0].Next != "2" || !result.Iterators[0].Exhausted {
		t.Fatalf("resumable iterator state values/clocks/iterator=%#v/%#v/%#v",
			values, result.ClockAdvances, result.Iterators)
	}
}

func TestFiniteIteratorModuleModelIncludesItemOrderAndRegistrationIsCanonical(t *testing.T) {
	left, _ := finiteIteratorModuleArchitecture(t, int64(3), int64(1), int64(2))
	right, _ := finiteIteratorModuleArchitecture(t, int64(3), int64(2), int64(1))
	leftDigest, err := left.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest == rightDigest {
		t.Fatal("finite iterator item order did not affect canonical model identity")
	}

	integerType, _ := gorapide.RapidePredefinedType("Integer")
	a := testFiniteIteratorModule(t, "a", integerType, int64(1))
	b := testFiniteIteratorModule(t, "b", integerType, int64(2))
	prior := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prior)
	var baseline string
	for iteration := 0; iteration < 12; iteration++ {
		if iteration == 6 {
			runtime.GOMAXPROCS(4)
		}
		architecture := NewArchitecture("iterator-registration")
		modules := []*FiniteIteratorModule{a, b}
		if iteration%2 != 0 {
			modules = []*FiniteIteratorModule{b, a}
		}
		for _, module := range modules {
			if err := architecture.AddFiniteIteratorModule(module); err != nil {
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

func TestFiniteIteratorModuleRejectsMalformedModels(t *testing.T) {
	integerType, _ := gorapide.RapidePredefinedType("Integer")
	booleanType, _ := gorapide.RapidePredefinedType("Boolean")
	if _, err := NewFiniteIteratorModule(gorapide.RapideModuleValue{}, integerType); !errors.Is(err, ErrInvalidFiniteIteratorModule) {
		t.Fatalf("zero module error=%v", err)
	}
	valid := testFiniteIteratorModule(t, "invalid-items", integerType, int64(1))
	if _, err := NewFiniteIteratorModule(valid.Module(), integerType, true); !errors.Is(err, ErrInvalidFiniteIteratorModule) {
		t.Fatalf("wrong item error=%v", err)
	}
	structural, _ := gorapide.RapideIteratorType(integerType)
	if _, err := NewFiniteIteratorModule(valid.Module(), structural); !errors.Is(err, ErrInvalidFiniteIteratorModule) {
		t.Fatalf("structural item type error=%v", err)
	}
	tooMany := make([]any, MaxFiniteRangeIteratorCardinality+1)
	for index := range tooMany {
		tooMany[index] = int64(index)
	}
	if _, err := NewFiniteIteratorModule(valid.Module(), integerType, tooMany...); !errors.Is(err, ErrInvalidFiniteIteratorModule) {
		t.Fatalf("cardinality error=%v", err)
	}

	architecture, iterator := finiteIteratorModuleArchitecture(t, int64(3))
	if err := architecture.AddFiniteIteratorModule(iterator); !errors.Is(err, ErrInvalidFiniteIteratorModule) {
		t.Fatalf("duplicate module error=%v", err)
	}

	missing := NewArchitecture("missing-iterator")
	component := NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
	if err := component.AddDeclarativeRule(
		Rule("iterate").On(pattern.MatchEvent("Start")).Do(
			ForEachIterator("I", iterator, NullStatement()),
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
		t.Fatalf("missing iterator implementation error=%v", err)
	}

	integerView := testFiniteIteratorModule(t, "mismatch", integerType, int64(1))
	booleanImplementation, err := NewFiniteIteratorModule(integerView.Module(), booleanType, true)
	if err != nil {
		t.Fatal(err)
	}
	mismatch := NewArchitecture("mismatch")
	if err := mismatch.AddFiniteIteratorModule(booleanImplementation); err != nil {
		t.Fatal(err)
	}
	component = NewComponent("worker", Interface("Worker").OutAction("Start").Build(), nil)
	if err := component.AddDeclarativeRule(
		Rule("iterate").On(pattern.MatchEvent("Start")).Do(
			ForEachIterator("I", integerView, NullStatement()),
		).Build(),
	); err != nil {
		t.Fatal(err)
	}
	if err := mismatch.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	_, err = mismatch.DeterministicModelDigest()
	if !errors.Is(err, ErrUnsupportedDeterministicModel) ||
		!strings.Contains(err.Error(), "supplies Boolean items, statement expects Integer") {
		t.Fatalf("iterator type mismatch error=%v", err)
	}
}
