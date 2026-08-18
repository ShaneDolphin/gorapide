package gorapide

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// This file pins the classMembers incremental index added in v0.2.4
// (perf/causal-class-successor-lookup) against the reference O(poset size)
// implementations it replaced in causalClassMembersLocked,
// causalClassRepresentativesLocked, causalClassSuccessorsLocked, and
// causalClassPredecessorsLocked (causal_equivalence.go). The reference
// functions below are literal copies of the pre-v0.2.4 code: they derive
// every answer by scanning p.events/p.causalEdges/p.reverseCausal in full,
// exactly as the library did before this round, and never read
// p.classMembers. Any divergence between "new" and "reference" here is a
// correctness bug in the new index, not a difference of opinion about
// what the right answer is.

func referenceCausalClassMembersLocked(p *Poset, id EventID) []EventID {
	representative := p.causalRepresentativeLocked(id)
	result := make([]EventID, 0)
	for candidate := range p.events {
		if p.causalRepresentativeLocked(candidate) == representative {
			result = append(result, candidate)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func referenceCausalClassRepresentativesLocked(p *Poset) []EventID {
	seen := make(map[EventID]bool, len(p.events))
	for id := range p.events {
		seen[p.causalRepresentativeLocked(id)] = true
	}
	result := make([]EventID, 0, len(seen))
	for representative := range seen {
		result = append(result, representative)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func referenceCausalClassSuccessorsLocked(p *Poset, representative EventID) []EventID {
	seen := make(map[EventID]bool)
	for from, successors := range p.causalEdges {
		if p.causalRepresentativeLocked(from) != representative {
			continue
		}
		for to := range successors {
			candidate := p.causalRepresentativeLocked(to)
			if candidate != representative {
				seen[candidate] = true
			}
		}
	}
	result := make([]EventID, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func referenceCausalClassPredecessorsLocked(p *Poset, representative EventID) []EventID {
	seen := make(map[EventID]bool)
	for to, predecessors := range p.reverseCausal {
		if p.causalRepresentativeLocked(to) != representative {
			continue
		}
		for from := range predecessors {
			candidate := p.causalRepresentativeLocked(from)
			if candidate != representative {
				seen[candidate] = true
			}
		}
	}
	result := make([]EventID, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// assertCausalClassIndexMatchesReference compares the new, index-backed
// implementations against the reference full-scan implementations for
// EVERY event and EVERY current representative in the poset. Must be
// called with p.mu held for reading by the caller's own access pattern;
// since this is an internal (same-package) test, it reaches into the
// locked-suffix helpers directly rather than through the public API, so it
// takes the lock itself.
func assertCausalClassIndexMatchesReference(t *testing.T, label string, p *Poset) {
	t.Helper()
	p.mu.RLock()
	defer p.mu.RUnlock()

	gotRepresentatives := p.causalClassRepresentativesLocked()
	wantRepresentatives := referenceCausalClassRepresentativesLocked(p)
	if !reflect.DeepEqual(gotRepresentatives, wantRepresentatives) {
		t.Fatalf("%s: causalClassRepresentativesLocked = %v, want %v", label, gotRepresentatives, wantRepresentatives)
	}

	for id := range p.events {
		gotMembers := p.causalClassMembersLocked(id)
		wantMembers := referenceCausalClassMembersLocked(p, id)
		if !reflect.DeepEqual(gotMembers, wantMembers) {
			t.Fatalf("%s: causalClassMembersLocked(%s) = %v, want %v", label, id, gotMembers, wantMembers)
		}
	}

	for _, representative := range wantRepresentatives {
		gotSucc := p.causalClassSuccessorsLocked(representative)
		wantSucc := referenceCausalClassSuccessorsLocked(p, representative)
		if !reflect.DeepEqual(gotSucc, wantSucc) {
			t.Fatalf("%s: causalClassSuccessorsLocked(%s) = %v, want %v", label, representative, gotSucc, wantSucc)
		}
		gotPred := p.causalClassPredecessorsLocked(representative)
		wantPred := referenceCausalClassPredecessorsLocked(p, representative)
		if !reflect.DeepEqual(gotPred, wantPred) {
			t.Fatalf("%s: causalClassPredecessorsLocked(%s) = %v, want %v", label, representative, gotPred, wantPred)
		}
	}
}

// TestCausalClassIndexMatchesReferenceUnderRandomMutation runs a seeded,
// reproducible sequence of AddEvent/AddCausal/AddCausalEquivalenceClass
// operations (including chained/transitive equivalence merges and merges
// of already-nontrivial classes) and, after every single mutation,
// compares every index-backed query against the reference O(poset size)
// implementation. This is the property-style differential test requested
// for the classMembers index: rather than hand-enumerating cases, it
// exercises many random mutation orderings and merge shapes against a
// known-correct oracle.
func TestCausalClassIndexMatchesReferenceUnderRandomMutation(t *testing.T) {
	const eventCount = 60
	const operationCount = 400
	rng := rand.New(rand.NewSource(20260818))

	poset := NewPoset()
	ids := make([]EventID, eventCount)
	for i := 0; i < eventCount; i++ {
		id := EventID(fmt.Sprintf("evt-%03d", i))
		ids[i] = id
		if err := poset.AddEvent(&Event{ID: id, Name: "Event", Source: "component"}); err != nil {
			t.Fatalf("AddEvent(%s): %v", id, err)
		}
	}
	assertCausalClassIndexMatchesReference(t, "after initial inserts", poset)

	for op := 0; op < operationCount; op++ {
		a := ids[rng.Intn(eventCount)]
		b := ids[rng.Intn(eventCount)]
		if a == b {
			continue
		}
		label := fmt.Sprintf("operation %d", op)
		if rng.Intn(2) == 0 {
			// Attempt a strict causal edge. Cycles and self-equivalence are
			// expected and simply skipped; only genuine failures fail the test.
			if err := poset.AddCausal(a, b); err != nil {
				continue
			}
		} else {
			// Attempt an equivalence merge, occasionally across 3 events to
			// exercise multi-representative (chained) merges in one call.
			c := ids[rng.Intn(eventCount)]
			var err error
			if c != a && c != b {
				err = poset.AddCausalEquivalenceClass(a, b, c)
			} else {
				err = poset.AddCausalEquivalenceClass(a, b)
			}
			if err != nil {
				continue
			}
		}
		assertCausalClassIndexMatchesReference(t, label, poset)
	}
}

// TestClassMembersAfterAddEventIsTrivial pins the simplest mutation path:
// a freshly-added event is its own one-member class, in both directions
// (causalClassMembersLocked and the public CausalEquivalenceClass).
func TestClassMembersAfterAddEventIsTrivial(t *testing.T) {
	poset := NewPoset()
	event := &Event{ID: "solo", Name: "Solo", Source: "component"}
	if err := poset.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	class := poset.CausalEquivalenceClass("solo")
	if len(class) != 1 || class[0].ID != "solo" {
		t.Fatalf("CausalEquivalenceClass(solo) = %v, want [solo]", class.IDs())
	}
	assertCausalClassIndexMatchesReference(t, "after single AddEvent", poset)
}

// TestClassMembersAfterEquivalenceMergeIsQueryableFromEveryMember pins
// AddCausalEquivalent/AddCausalEquivalenceClass: after merging, EVERY
// member of the class -- not just the one chosen as representative --
// must resolve CausalEquivalenceClass to the full merged set, and the
// old, now-absorbed representative(s) must no longer head their own
// independent one-member class in causalClassRepresentativesLocked.
func TestClassMembersAfterEquivalenceMergeIsQueryableFromEveryMember(t *testing.T) {
	poset := NewPoset()
	addCausalEquivalenceTestEvents(t, poset, "m1", "m2", "m3")
	if err := poset.AddCausalEquivalenceClass("m2", "m3", "m1"); err != nil {
		t.Fatal(err)
	}
	want := []EventID{"m1", "m2", "m3"}
	for _, id := range want {
		class := poset.CausalEquivalenceClass(id)
		if !reflect.DeepEqual(class.IDs(), want) {
			t.Fatalf("CausalEquivalenceClass(%s) = %v, want %v", id, class.IDs(), want)
		}
	}
	assertCausalClassIndexMatchesReference(t, "after 3-way equivalence merge", poset)
}

// TestClassMembersAfterChainedMergesConsolidatesTransitively pins the
// trickiest membership-index mutation: merging class {A} into {B} and
// THEN merging the resulting class into {C} (two separate
// AddCausalEquivalenceClass calls, not one call with all three ids) must
// still produce one class containing all of A, B, and C, discoverable
// from any member, with no stale index entry left behind for the
// intermediate {A,B} representative if it was absorbed into C's.
func TestClassMembersAfterChainedMergesConsolidatesTransitively(t *testing.T) {
	poset := NewPoset()
	addCausalEquivalenceTestEvents(t, poset, "A", "B", "C", "D")
	if err := poset.AddCausalEquivalenceClass("B", "A"); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddCausalEquivalenceClass("C", "B"); err != nil {
		t.Fatal(err)
	}
	want := []EventID{"A", "B", "C"}
	for _, id := range want {
		class := poset.CausalEquivalenceClass(id)
		if !reflect.DeepEqual(class.IDs(), want) {
			t.Fatalf("CausalEquivalenceClass(%s) = %v, want %v", id, class.IDs(), want)
		}
	}
	// D was never merged: it must remain its own trivial class, and the
	// representative set must contain exactly D and the merged class's
	// representative -- no leftover entries for A, B, or C individually.
	dClass := poset.CausalEquivalenceClass("D")
	if !reflect.DeepEqual(dClass.IDs(), []EventID{"D"}) {
		t.Fatalf("CausalEquivalenceClass(D) = %v, want [D]", dClass.IDs())
	}
	assertCausalClassIndexMatchesReference(t, "after chained equivalence merges", poset)
}

// TestClassMembersIndexRebuildsOnUnmarshalJSONStateReplace mirrors
// TestUnmarshalJSONRecomputesTimedEventCounterOnStateReplace's style for
// the timedEvents counter: reuse ONE live *Poset object (which already has
// its own equivalence classes and thus its own classMembers index
// content), then UnmarshalJSON a DIFFERENT poset's export into it, and
// confirm the index reflects only the new state -- no member from the
// discarded poset leaks into a query against the replacement.
func TestClassMembersIndexRebuildsOnUnmarshalJSONStateReplace(t *testing.T) {
	reused := NewPoset()
	addCausalEquivalenceTestEvents(t, reused, "old1", "old2", "old3")
	if err := reused.AddCausalEquivalenceClass("old1", "old2"); err != nil {
		t.Fatal(err)
	}
	assertCausalClassIndexMatchesReference(t, "reused poset before replace", reused)

	replacement := NewPoset()
	addCausalEquivalenceTestEvents(t, replacement, "new1", "new2", "new3")
	if err := replacement.AddCausalEquivalenceClass("new2", "new3"); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(replacement)
	if err != nil {
		t.Fatal(err)
	}

	if err := reused.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	// The discarded classes must be entirely gone: old1/old2/old3 are not
	// even events any more, so CausalEquivalenceClass on them returns
	// empty, and the new classes are queryable correctly from any member.
	for _, stale := range []EventID{"old1", "old2", "old3"} {
		if class := reused.CausalEquivalenceClass(stale); len(class) != 0 {
			t.Fatalf("CausalEquivalenceClass(%s) after replace = %v, want empty (event no longer exists)", stale, class.IDs())
		}
	}
	want := []EventID{"new2", "new3"}
	for _, id := range want {
		class := reused.CausalEquivalenceClass(id)
		if !reflect.DeepEqual(class.IDs(), want) {
			t.Fatalf("CausalEquivalenceClass(%s) after replace = %v, want %v", id, class.IDs(), want)
		}
	}
	assertCausalClassIndexMatchesReference(t, "reused poset after UnmarshalJSON replace", reused)
}

// TestClassMembersIndexAfterMergeSnapshotAddsEquivalences pins the
// MergeSnapshot mutation path: merging a snapshot that declares causal-
// equivalence classes into an existing poset must leave the classMembers
// index correct for both the pre-existing and newly-merged classes.
func TestClassMembersIndexAfterMergeSnapshotAddsEquivalences(t *testing.T) {
	remote := NewPoset()
	addCausalEquivalenceTestEvents(t, remote, "r1", "r2", "r3")
	if err := remote.AddCausalEquivalenceClass("r1", "r2"); err != nil {
		t.Fatal(err)
	}
	snapshot := remote.CreateSnapshot("remote")

	local := NewPoset()
	addCausalEquivalenceTestEvents(t, local, "l1")
	if _, err := local.MergeSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	want := []EventID{"r1", "r2"}
	for _, id := range want {
		class := local.CausalEquivalenceClass(id)
		if !reflect.DeepEqual(class.IDs(), want) {
			t.Fatalf("CausalEquivalenceClass(%s) after merge = %v, want %v", id, class.IDs(), want)
		}
	}
	if class := local.CausalEquivalenceClass("l1"); !reflect.DeepEqual(class.IDs(), []EventID{"l1"}) {
		t.Fatalf("CausalEquivalenceClass(l1) after merge = %v, want [l1]", class.IDs())
	}
	if class := local.CausalEquivalenceClass("r3"); !reflect.DeepEqual(class.IDs(), []EventID{"r3"}) {
		t.Fatalf("CausalEquivalenceClass(r3) after merge = %v, want [r3]", class.IDs())
	}
	assertCausalClassIndexMatchesReference(t, "local poset after MergeSnapshot", local)
}

// TestClassMembersIndexAfterParseCanonicalPosetWithEquivalences pins the
// ParseCanonicalPoset mutation path (builds entirely via the public
// AddEvent/AddCausalEquivalenceClass/AddCausal API, but worth pinning
// directly since it is the trusted-import path, not just an aggregate of
// the others).
func TestClassMembersIndexAfterParseCanonicalPosetWithEquivalences(t *testing.T) {
	built := NewPoset()
	addCausalEquivalenceTestEvents(t, built, "c1", "c2", "c3")
	if err := built.AddCausalEquivalenceClass("c1", "c2"); err != nil {
		t.Fatal(err)
	}
	if err := built.AddCausal("c1", "c3"); err != nil {
		t.Fatal(err)
	}
	data, err := built.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCanonicalPoset(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []EventID{"c1", "c2"}
	for _, id := range want {
		class := parsed.CausalEquivalenceClass(id)
		if !reflect.DeepEqual(class.IDs(), want) {
			t.Fatalf("CausalEquivalenceClass(%s) after parse = %v, want %v", id, class.IDs(), want)
		}
	}
	assertCausalClassIndexMatchesReference(t, "poset after ParseCanonicalPoset", parsed)
}
