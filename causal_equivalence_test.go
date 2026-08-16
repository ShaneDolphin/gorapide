package gorapide

import (
	"bytes"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

func causalEquivalenceTestEvent(id EventID) *Event {
	return &Event{ID: id, Name: "Event" + string(id), Source: "component", Params: map[string]any{"id": string(id)}}
}

func addCausalEquivalenceTestEvents(t *testing.T, poset *Poset, ids ...EventID) {
	t.Helper()
	for _, id := range ids {
		if err := poset.AddEvent(causalEquivalenceTestEvent(id)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCausalEquivalenceQuotientQueriesPreserveSubstitutivity(t *testing.T) {
	poset := NewPoset()
	addCausalEquivalenceTestEvents(t, poset, "before", "left", "right", "after")
	if err := poset.AddCausalEquivalent("right", "left"); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddCausal("before", "right"); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddCausal("left", "after"); err != nil {
		t.Fatal(err)
	}

	if !poset.IsCausallyEquivalent("left", "right") ||
		poset.IsCausallyBefore("left", "right") || poset.IsCausallyBefore("right", "left") ||
		poset.IsCausallyIndependent("left", "right") {
		t.Fatal("causal equivalence was confused with strict order or independence")
	}
	for _, member := range []EventID{"left", "right"} {
		if !poset.IsCausallyBefore("before", member) || !poset.IsCausallyBefore(member, "after") {
			t.Fatalf("strict order did not substitute through equivalent member %s", member)
		}
		causes := poset.DirectCauses(member)
		if len(causes) != 1 || causes[0].ID != "before" {
			t.Fatalf("DirectCauses(%s)=%v", member, causes.IDs())
		}
		effects := poset.DirectEffects(member)
		if len(effects) != 1 || effects[0].ID != "after" {
			t.Fatalf("DirectEffects(%s)=%v", member, effects.IDs())
		}
	}
	class := poset.CausalEquivalenceClass("right")
	if len(class) != 2 || class[0].ID != "left" || class[1].ID != "right" {
		t.Fatalf("equivalence class=%v", class.IDs())
	}
	if roots := poset.Roots(); len(roots) != 1 || roots[0].ID != "before" {
		t.Fatalf("roots=%v", roots.IDs())
	}
	if leaves := poset.Leaves(); len(leaves) != 1 || leaves[0].ID != "after" {
		t.Fatalf("leaves=%v", leaves.IDs())
	}
	chain, err := poset.CausalChain("before", "after")
	if err != nil || len(chain) != 4 {
		t.Fatalf("chain=%v err=%v", chain.IDs(), err)
	}
	if validation := poset.Validate(); len(validation) != 0 {
		t.Fatalf("validation=%v", validation)
	}
	stats := poset.Stats()
	if stats.CausalEquivalenceClassCount != 1 || stats.RootCount != 1 || stats.LeafCount != 1 || stats.MaxDepth != 3 {
		t.Fatalf("stats=%#v", stats)
	}
}

func TestCanonicalCausalPreorderIsOrderInvariantAndRoundTrips(t *testing.T) {
	build := func(reverse bool) *Poset {
		poset := NewPoset()
		ids := []EventID{"a", "b", "c", "d"}
		if reverse {
			ids = []EventID{"d", "c", "b", "a"}
		}
		addCausalEquivalenceTestEvents(t, poset, ids...)
		if reverse {
			if err := poset.AddCausalEquivalenceClass("c", "b"); err != nil {
				t.Fatal(err)
			}
			if err := poset.AddCausal("c", "d"); err != nil {
				t.Fatal(err)
			}
			if err := poset.AddCausal("a", "c"); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := poset.AddCausalEquivalenceClass("b", "c"); err != nil {
				t.Fatal(err)
			}
			if err := poset.AddCausal("a", "b"); err != nil {
				t.Fatal(err)
			}
			if err := poset.AddCausal("b", "d"); err != nil {
				t.Fatal(err)
			}
		}
		return poset
	}

	originalProcs := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(originalProcs)
	var expected []byte
	for run := 0; run < 40; run++ {
		runtime.GOMAXPROCS(1 + 7*(run%2))
		encoded, err := build(run%2 == 1).MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			expected = encoded
		} else if !bytes.Equal(encoded, expected) {
			t.Fatalf("run %d changed canonical preorder bytes:\n%s\n%s", run, expected, encoded)
		}
	}
	var canonical CanonicalPoset
	if err := json.Unmarshal(expected, &canonical); err != nil {
		t.Fatal(err)
	}
	if canonical.Format != CanonicalCausalPreorderFormat || len(canonical.CausalEquivalences) != 1 ||
		len(canonical.CausalEquivalences[0].Members) != 2 ||
		canonical.CausalEquivalences[0].Members[0] != "b" || canonical.CausalEquivalences[0].Members[1] != "c" {
		t.Fatalf("canonical preorder=%#v", canonical)
	}
	if len(canonical.Edges) != 2 || canonical.Edges[0] != (CanonicalEdge{From: "a", To: "b"}) ||
		canonical.Edges[1] != (CanonicalEdge{From: "b", To: "d"}) {
		t.Fatalf("canonical quotient edges=%#v", canonical.Edges)
	}
	if canonical.Events[1].CausalDepth != canonical.Events[2].CausalDepth {
		t.Fatalf("equivalent depths differ: %#v", canonical.Events)
	}
	restored, err := ParseCanonicalPoset(expected)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := restored.MarshalCanonical()
	if err != nil || !bytes.Equal(roundTrip, expected) {
		t.Fatalf("round trip err=%v\n%s\n%s", err, expected, roundTrip)
	}
}

func TestCausalEquivalenceRejectsStrictOrderConflicts(t *testing.T) {
	strict := NewPoset()
	addCausalEquivalenceTestEvents(t, strict, "a", "b")
	if err := strict.AddCausal("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := strict.AddCausalEquivalent("a", "b"); !errors.Is(err, ErrCausalEquivalenceConflict) {
		t.Fatalf("strict-to-equivalent conflict=%v", err)
	}

	equivalent := NewPoset()
	addCausalEquivalenceTestEvents(t, equivalent, "a", "b")
	if err := equivalent.AddCausalEquivalent("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := equivalent.AddCausal("a", "b"); !errors.Is(err, ErrSelfCausal) {
		t.Fatalf("equivalent-to-strict conflict=%v", err)
	}
	if err := equivalent.AddCausalEquivalenceClass("a", "missing"); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("missing member=%v", err)
	}

	timed := NewPoset()
	before := causalEquivalenceTestEvent("before")
	left := causalEquivalenceTestEvent("left")
	right := causalEquivalenceTestEvent("right")
	before.Timings = []EventTiming{{Clock: "clock", Start: 5, Finish: 6}}
	left.Timings = []EventTiming{{Clock: "clock", Start: 7, Finish: 8}}
	right.Timings = []EventTiming{{Clock: "clock", Start: 1, Finish: 2}}
	for _, event := range []*Event{before, left, right} {
		if err := timed.AddEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := timed.AddCausal(before.ID, left.ID); err != nil {
		t.Fatal(err)
	}
	if err := timed.AddCausalEquivalent(left.ID, right.ID); !errors.Is(err, ErrTimingCausality) {
		t.Fatalf("substitutive timing conflict=%v", err)
	}
	if timed.IsCausallyEquivalent(left.ID, right.ID) || !timed.IsCausallyBefore(before.ID, left.ID) ||
		timed.IsCausallyBefore(before.ID, right.ID) {
		t.Fatal("failed equivalence timing preflight partially mutated the preorder")
	}
}

func TestTimedCausalPreorderUsesVersionedCanonicalFormat(t *testing.T) {
	poset := NewPoset()
	left := causalEquivalenceTestEvent("left")
	right := causalEquivalenceTestEvent("right")
	left.Timings = []EventTiming{{Clock: "clock", Start: 1, Finish: 2}}
	right.Timings = []EventTiming{{Clock: "clock", Start: 3, Finish: 4}}
	if err := poset.AddEvent(left); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(right); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddCausalEquivalent("left", "right"); err != nil {
		t.Fatal(err)
	}
	encoded, err := poset.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	var canonical CanonicalPoset
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		t.Fatal(err)
	}
	if canonical.Format != CanonicalTimedCausalPreorderFormat {
		t.Fatalf("format=%q", canonical.Format)
	}
	if _, err := ParseCanonicalPoset(encoded); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalCausalPreorderRejectsMalformedClassesAndEdges(t *testing.T) {
	poset := NewPoset()
	addCausalEquivalenceTestEvents(t, poset, "a", "b", "c")
	if err := poset.AddCausalEquivalent("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddCausal("a", "c"); err != nil {
		t.Fatal(err)
	}
	encoded, err := poset.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	mutations := [][]byte{
		bytes.Replace(encoded, []byte(`"members":["a","b"]`), []byte(`"members":["b","a"]`), 1),
		bytes.Replace(encoded, []byte(`"members":["a","b"]`), []byte(`"members":["a"]`), 1),
		bytes.Replace(encoded, []byte(`"from":"a"`), []byte(`"from":"b"`), 1),
		bytes.Replace(encoded, []byte(CanonicalCausalPreorderFormat), []byte(CanonicalPosetFormat), 1),
	}
	for index, mutation := range mutations {
		if _, err := ParseCanonicalPoset(mutation); !errors.Is(err, ErrInvalidCanonicalPoset) {
			t.Fatalf("mutation %d err=%v\n%s", index, err, mutation)
		}
	}
}

func TestCausalEquivalenceSnapshotsCloneMergeAndReplayLosslessly(t *testing.T) {
	remote := NewPoset()
	addCausalEquivalenceTestEvents(t, remote, "before", "left", "right", "after")
	if err := remote.AddCausalEquivalent("right", "left"); err != nil {
		t.Fatal(err)
	}
	if err := remote.AddCausal("before", "right"); err != nil {
		t.Fatal(err)
	}
	if err := remote.AddCausal("left", "after"); err != nil {
		t.Fatal(err)
	}
	for _, event := range remote.events {
		event.Clock.WallTime = time.Unix(0, 0).UTC()
	}
	snapshot := remote.CreateSnapshot("remote")
	cloned, err := CloneSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(cloned.CausalEquivalences) != 1 || len(cloned.CausalEquivalences[0]) != 2 {
		t.Fatalf("snapshot equivalences=%#v", cloned.CausalEquivalences)
	}
	snapshot.CausalEquivalences[0][0] = "mutated"
	if cloned.CausalEquivalences[0][0] == "mutated" {
		t.Fatal("CloneSnapshot retained caller-owned equivalence data")
	}

	local := NewPoset()
	result, err := local.MergeSnapshot(cloned)
	if err != nil {
		t.Fatal(err)
	}
	if result.EventsAdded != 4 || result.EquivalencesAdded != 1 || result.EdgesAdded != 2 {
		t.Fatalf("merge result=%#v", result)
	}
	left, _ := remote.MarshalCanonical()
	right, _ := local.MarshalCanonical()
	if !bytes.Equal(left, right) {
		t.Fatalf("snapshot merge changed preorder:\n%s\n%s", left, right)
	}
	second, err := local.MergeSnapshot(cloned)
	if err != nil || second.EquivalencesSkipped != 1 || second.EquivalencesAdded != 0 {
		t.Fatalf("idempotent merge=%#v err=%v", second, err)
	}

	legacyJSON, err := json.Marshal(remote)
	if err != nil {
		t.Fatal(err)
	}
	var restored Poset
	if err := json.Unmarshal(legacyJSON, &restored); err != nil {
		t.Fatal(err)
	}
	restoredCanonical, err := restored.MarshalCanonical()
	if err != nil || !bytes.Equal(restoredCanonical, left) {
		t.Fatalf("legacy JSON round trip err=%v\n%s\n%s", err, left, restoredCanonical)
	}
}

func TestCausalEquivalenceExportsRemainExplicitAndDeterministic(t *testing.T) {
	poset := NewPoset()
	left := &Event{ID: "export-left", Name: "Left", Source: "export"}
	right := &Event{ID: "export-right", Name: "Right", Source: "export"}
	after := &Event{ID: "export-after", Name: "After", Source: "export"}
	for _, event := range []*Event{after, right, left} {
		if err := poset.AddEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := poset.AddCausalEquivalent(right.ID, left.ID); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddCausal(left.ID, after.ID); err != nil {
		t.Fatal(err)
	}

	for name, rendered := range map[string]string{
		"dot":         poset.DOT(),
		"dot-options": poset.DOTWithOptions(DOTOptions{}),
		"mermaid":     poset.Mermaid(),
	} {
		if !strings.Contains(rendered, "=c") {
			t.Fatalf("%s omitted causal equivalence:\n%s", name, rendered)
		}
	}
	if first, second := poset.DOT(), poset.DOT(); first != second {
		t.Fatal("DOT export changed without a semantic mutation")
	}
	if first, second := poset.Mermaid(), poset.Mermaid(); first != second {
		t.Fatal("Mermaid export changed without a semantic mutation")
	}
	if summary := poset.String(); !strings.Contains(summary, "1 nontrivial causal-equivalence classes") {
		t.Fatalf("summary omitted causal-equivalence count: %s", summary)
	}
}
