package pattern

import (
	"strings"
	"testing"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// Compile-time interface checks for composite patterns.
var (
	_ Pattern = (*seqPattern)(nil)
	_ Pattern = (*immSeqPattern)(nil)
	_ Pattern = (*joinPattern)(nil)
	_ Pattern = (*independentPattern)(nil)
	_ Pattern = (*orPattern)(nil)
	_ Pattern = (*andPattern)(nil)
	_ Pattern = (*unionPattern)(nil)
	_ Pattern = (*forEachPattern)(nil)
	_ Pattern = (*guardPattern)(nil)
	_ Pattern = (*notPattern)(nil)
	_ Pattern = (*emptyPattern)(nil)
)

// --- Sequence Pattern Tests ---

func TestSeqMatchesCausalOrder(t *testing.T) {
	// A -> B -> C -> D
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		Event("D").CausedBy("C").
		MustDone()

	results := Seq(MatchEvent("A"), MatchEvent("D")).Match(p)
	if len(results) != 1 {
		t.Fatalf("Seq(A, D): expected 1 match, got %d", len(results))
	}
	if len(results[0]) != 2 {
		t.Errorf("match should contain 2 events (A and D), got %d", len(results[0]))
	}
}

func TestSeqRejectsReverseOrder(t *testing.T) {
	// A -> B -> C -> D
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		Event("D").CausedBy("C").
		MustDone()

	results := Seq(MatchEvent("D"), MatchEvent("A")).Match(p)
	if len(results) != 0 {
		t.Errorf("Seq(D, A): should not match reverse order, got %d", len(results))
	}
}

func TestSeqAdjacentEvents(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		MustDone()

	results := Seq(MatchEvent("A"), MatchEvent("B")).Match(p)
	if len(results) != 1 {
		t.Fatalf("Seq(A, B): expected 1 match, got %d", len(results))
	}
}

func TestSeqThreePatterns(t *testing.T) {
	// A -> B -> C -> D
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		Event("D").CausedBy("C").
		MustDone()

	results := Seq(MatchEvent("A"), MatchEvent("B"), MatchEvent("D")).Match(p)
	if len(results) != 1 {
		t.Fatalf("Seq(A, B, D): expected 1 match, got %d", len(results))
	}
	if len(results[0]) != 3 {
		t.Errorf("match should contain 3 events, got %d", len(results[0]))
	}
}

func TestSeqFourPatterns(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		Event("D").CausedBy("C").
		MustDone()

	results := Seq(MatchEvent("A"), MatchEvent("B"), MatchEvent("C"), MatchEvent("D")).Match(p)
	if len(results) != 1 {
		t.Fatalf("Seq(A, B, C, D): expected 1 match, got %d", len(results))
	}
	if len(results[0]) != 4 {
		t.Errorf("match should contain 4 events, got %d", len(results[0]))
	}
}

func TestSeqNoMatch(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").
		MustDone() // no causal relation

	results := Seq(MatchEvent("A"), MatchEvent("B")).Match(p)
	// A and B are causally independent — neither direction holds,
	// so neither Seq(A,B) nor Seq(B,A) should match (unless one
	// happens to be before the other, which it isn't with no edges).
	// Actually with no causal edges, IsCausallyBefore is false for both.
	if len(results) != 0 {
		t.Errorf("Seq should not match unrelated events, got %d", len(results))
	}
}

func TestSeqPanicsOnLessThanTwo(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Seq with <2 patterns should panic")
		}
	}()
	Seq(MatchEvent("A"))
}

// --- Immediate Sequence Tests ---

func TestImmSeqDirectNeighbors(t *testing.T) {
	// A -> B -> C
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		MustDone()

	results := ImmSeq(MatchEvent("A"), MatchEvent("B")).Match(p)
	if len(results) != 1 {
		t.Fatalf("ImmSeq(A, B): expected 1 match, got %d", len(results))
	}
}

func TestImmSeqRejectsIntervening(t *testing.T) {
	// A -> B -> C
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		MustDone()

	results := ImmSeq(MatchEvent("A"), MatchEvent("C")).Match(p)
	if len(results) != 0 {
		t.Errorf("ImmSeq(A, C): should reject because B intervenes, got %d", len(results))
	}
}

func TestImmSeqDirectWithSibling(t *testing.T) {
	// A -> B, A -> C (B and C are siblings, both directly after A)
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		MustDone()

	// A ~> B should match: B is a direct successor of A.
	// Even though C also follows A, C doesn't sit between A and B.
	results := ImmSeq(MatchEvent("A"), MatchEvent("B")).Match(p)
	if len(results) != 1 {
		t.Fatalf("ImmSeq(A, B): expected 1 match with sibling C, got %d", len(results))
	}
}

func TestImmSeqRejectsReverseOrder(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		MustDone()

	results := ImmSeq(MatchEvent("B"), MatchEvent("A")).Match(p)
	if len(results) != 0 {
		t.Errorf("ImmSeq(B, A): should not match reverse, got %d", len(results))
	}
}

func TestImmSeqPanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("ImmSeq with nil should panic")
		}
	}()
	ImmSeq(nil, MatchEvent("A"))
}

// --- Join Pattern Tests ---

func TestJoinSharedAncestor(t *testing.T) {
	// Root -> A, Root -> B
	p := gorapide.Build().
		Event("Root").
		Event("A").CausedBy("Root").
		Event("B").CausedBy("Root").
		MustDone()

	results := Join(MatchEvent("A"), MatchEvent("B")).Match(p)
	if len(results) != 1 {
		t.Fatalf("Join(A, B): expected 1 match with shared ancestor Root, got %d", len(results))
	}
}

func TestJoinNoSharedAncestor(t *testing.T) {
	// A and B are completely unrelated (different roots, no shared ancestor)
	p := gorapide.Build().
		Event("A").
		Event("B").
		MustDone()

	results := Join(MatchEvent("A"), MatchEvent("B")).Match(p)
	if len(results) != 0 {
		t.Errorf("Join(A, B): should not match without shared ancestor, got %d", len(results))
	}
}

func TestJoinDeepSharedAncestor(t *testing.T) {
	// Root -> Mid -> A, Root -> B
	p := gorapide.Build().
		Event("Root").
		Event("Mid").CausedBy("Root").
		Event("A").CausedBy("Mid").
		Event("B").CausedBy("Root").
		MustDone()

	results := Join(MatchEvent("A"), MatchEvent("B")).Match(p)
	if len(results) != 1 {
		t.Fatalf("Join(A, B): expected 1 match (Root is shared ancestor), got %d", len(results))
	}
}

func TestJoinPanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Join with nil should panic")
		}
	}()
	Join(MatchEvent("A"), nil)
}

// --- Independence Pattern Tests ---

func TestIndependentUnrelated(t *testing.T) {
	// A and B with no causal relation
	p := gorapide.Build().
		Event("A").
		Event("B").
		MustDone()

	results := Independent(MatchEvent("A"), MatchEvent("B")).Match(p)
	if len(results) != 1 {
		t.Fatalf("Independent(A, B): expected 1 match for unrelated events, got %d", len(results))
	}
}

func TestIndependentRejectsCausallyRelated(t *testing.T) {
	// A -> B
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		MustDone()

	results := Independent(MatchEvent("A"), MatchEvent("B")).Match(p)
	if len(results) != 0 {
		t.Errorf("Independent(A, B): should reject causally related events, got %d", len(results))
	}
}

func TestIndependentRejectsReverseCausal(t *testing.T) {
	// A -> B
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		MustDone()

	results := Independent(MatchEvent("B"), MatchEvent("A")).Match(p)
	if len(results) != 0 {
		t.Errorf("Independent(B, A): should reject causally related events, got %d", len(results))
	}
}

func TestIndependentSiblings(t *testing.T) {
	// Root -> A, Root -> B  (A and B are siblings, causally independent)
	p := gorapide.Build().
		Event("Root").
		Event("A").CausedBy("Root").
		Event("B").CausedBy("Root").
		MustDone()

	results := Independent(MatchEvent("A"), MatchEvent("B")).Match(p)
	if len(results) != 1 {
		t.Fatalf("Independent(A, B): siblings should be independent, got %d", len(results))
	}
}

func TestIndependentPanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Independent with nil should panic")
		}
	}()
	Independent(nil, MatchEvent("A"))
}

// --- Nested Composition Tests ---

func TestNestedSeqJoin(t *testing.T) {
	// Root -> B, Root -> C, A -> Root
	// So: A -> Root -> B and A -> Root -> C
	p := gorapide.Build().
		Event("A").
		Event("Root").CausedBy("A").
		Event("B").CausedBy("Root").
		Event("C").CausedBy("Root").
		MustDone()

	// Seq(Match("A"), Join(Match("B"), Match("C")))
	// First: A matches, then Join(B, C) matches (B and C share Root as ancestor)
	// Then: A must causally precede all events in the Join match (B and C)
	pat := Seq(MatchEvent("A"), Join(MatchEvent("B"), MatchEvent("C")))
	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("Seq(A, Join(B, C)): expected 1 match, got %d", len(results))
	}
	// Result should contain A, B, C
	if len(results[0]) != 3 {
		t.Errorf("match should contain 3 events, got %d", len(results[0]))
	}
}

func TestNestedJoinIndependent(t *testing.T) {
	// Root -> A, Root -> B, C (independent)
	p := gorapide.Build().
		Event("Root").
		Event("A").CausedBy("Root").
		Event("B").CausedBy("Root").
		Event("C").
		MustDone()

	// Independent(Join(Match("A"), Match("B")), Match("C"))
	// Join(A, B) matches (shared Root ancestor)
	// C is independent of both A and B (and Root)
	pat := Independent(Join(MatchEvent("A"), MatchEvent("B")), MatchEvent("C"))
	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("Independent(Join(A,B), C): expected 1 match, got %d", len(results))
	}
	if len(results[0]) != 3 {
		t.Errorf("match should contain 3 events (A, B, C), got %d", len(results[0]))
	}
}

func TestNestedSeqImmSeq(t *testing.T) {
	// A -> B -> C
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		MustDone()

	// Seq(ImmSeq(A, B), Match(C))
	pat := Seq(ImmSeq(MatchEvent("A"), MatchEvent("B")), MatchEvent("C"))
	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("Seq(ImmSeq(A,B), C): expected 1 match, got %d", len(results))
	}
	if len(results[0]) != 3 {
		t.Errorf("match should contain 3 events, got %d", len(results[0]))
	}
}

// --- String() Tests ---

func TestCompositePatternString(t *testing.T) {
	tests := []struct {
		pat  Pattern
		want string
	}{
		{
			Seq(MatchEvent("A"), MatchEvent("B")),
			`Seq(Match("A"), Match("B"))`,
		},
		{
			ImmSeq(MatchEvent("A"), MatchEvent("B")),
			`ImmSeq(Match("A"), Match("B"))`,
		},
		{
			Join(MatchEvent("A"), MatchEvent("B")),
			`Join(Match("A"), Match("B"))`,
		},
		{
			Independent(MatchEvent("A"), MatchEvent("B")),
			`Independent(Match("A"), Match("B"))`,
		},
		{
			Seq(MatchEvent("A"), Join(MatchEvent("B"), MatchEvent("C"))),
			`Seq(Match("A"), Join(Match("B"), Match("C")))`,
		},
		{
			Seq(MatchEvent("A"), MatchEvent("B"), MatchEvent("C")),
			`Seq(Seq(Match("A"), Match("B")), Match("C"))`,
		},
	}
	for _, tt := range tests {
		got := tt.pat.String()
		if got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

// --- Edge cases ---

func TestSeqWithNoMatches(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		MustDone()

	results := Seq(MatchEvent("X"), MatchEvent("B")).Match(p)
	if len(results) != 0 {
		t.Errorf("Seq with unmatched sub-pattern should return empty, got %d", len(results))
	}
}

func TestJoinStringContainsSubpatterns(t *testing.T) {
	s := Join(MatchEvent("A"), MatchEvent("B")).String()
	if !strings.Contains(s, "Join") {
		t.Error("Join String should contain 'Join'")
	}
	if !strings.Contains(s, `Match("A")`) {
		t.Error("Join String should contain sub-pattern A")
	}
}

// --- Or Pattern Tests ---

func TestOrUnionOfMatches(t *testing.T) {
	// A -> B, A -> C
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		MustDone()

	results := Or(MatchEvent("B"), MatchEvent("C")).Match(p)
	if len(results) != 2 {
		t.Fatalf("Or(B, C): expected 2 matches, got %d", len(results))
	}
}

func TestOrDeduplicatesOverlapping(t *testing.T) {
	// Both Match("A") and MatchAny().WhereSource("default") will match event A.
	p := gorapide.Build().
		Event("A").
		MustDone()

	results := Or(MatchEvent("A"), MatchAny()).Match(p)
	// There's only one event, so both sub-patterns produce the same singleton.
	// Or should deduplicate.
	if len(results) != 1 {
		t.Errorf("Or should deduplicate overlapping matches, got %d", len(results))
	}
}

func TestOrWithNoSubpatternMatches(t *testing.T) {
	p := gorapide.Build().Event("A").MustDone()

	results := Or(MatchEvent("X"), MatchEvent("Y")).Match(p)
	if len(results) != 0 {
		t.Errorf("Or with no matching sub-patterns should return empty, got %d", len(results))
	}
}

func TestOrThreePatterns(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").
		Event("C").
		MustDone()

	results := Or(MatchEvent("A"), MatchEvent("B"), MatchEvent("C")).Match(p)
	if len(results) != 3 {
		t.Errorf("Or(A, B, C): expected 3 matches, got %d", len(results))
	}
}

func TestOrPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Or with <2 patterns should panic")
		}
	}()
	Or(MatchEvent("A"))
}

// --- And Pattern Tests ---

func TestAndIntersection(t *testing.T) {
	p := gorapide.Build().
		Source("scanner").
		Event("A").
		Event("B").
		MustDone()

	// Match("A") returns {A}, MatchAny().WhereSource("scanner") returns {A, B}.
	// Both produce singleton sets. Intersection: only {A} is in both.
	results := And(MatchEvent("A"), MatchAny().WhereSource("scanner")).Match(p)
	if len(results) != 1 {
		t.Fatalf("And: expected 1 intersecting match, got %d", len(results))
	}
	if results[0][0].Name != "A" {
		t.Errorf("And intersection should be A, got %s", results[0][0].Name)
	}
}

func TestAndDisjointReturnsEmpty(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").
		MustDone()

	results := And(MatchEvent("A"), MatchEvent("B")).Match(p)
	if len(results) != 0 {
		t.Errorf("And with disjoint match sets should return empty, got %d", len(results))
	}
}

func TestAndNoMatchReturnsEmpty(t *testing.T) {
	p := gorapide.Build().Event("A").MustDone()

	results := And(MatchEvent("X"), MatchEvent("A")).Match(p)
	if len(results) != 0 {
		t.Errorf("And where first pattern has no match should return empty, got %d", len(results))
	}
}

func TestAndPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("And with <2 patterns should panic")
		}
	}()
	And(MatchEvent("A"))
}

// --- Union Pattern Tests ---

func TestUnionCombinesSets(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").
		MustDone()

	results := Union(MatchEvent("A"), MatchEvent("B")).Match(p)
	if len(results) != 1 {
		t.Fatalf("Union(A, B): expected 1 combined set, got %d", len(results))
	}
	if len(results[0]) != 2 {
		t.Errorf("combined set should have 2 events, got %d", len(results[0]))
	}
}

func TestUnionCartesianProduct(t *testing.T) {
	// Two A events, one B event => 2 * 1 = 2 results.
	p := gorapide.Build().
		Event("A").
		Event("A").
		Event("B").
		MustDone()

	results := Union(MatchEvent("A"), MatchEvent("B")).Match(p)
	if len(results) != 2 {
		t.Errorf("Union with 2 A matches and 1 B match: expected 2 results, got %d", len(results))
	}
	for _, es := range results {
		if len(es) != 2 {
			t.Errorf("each union result should have 2 events, got %d", len(es))
		}
	}
}

func TestUnionEmptySubpattern(t *testing.T) {
	p := gorapide.Build().Event("A").MustDone()

	results := Union(MatchEvent("A"), MatchEvent("X")).Match(p)
	if len(results) != 0 {
		t.Errorf("Union with empty sub-pattern should return empty (0 * n = 0), got %d", len(results))
	}
}

func TestUnionPanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Union with nil should panic")
		}
	}()
	Union(nil, MatchEvent("A"))
}

// --- ForEach Pattern Tests ---

func TestForEachWithSeqStrings(t *testing.T) {
	// Build: ScanStart -> VulnFound(cve=CVE-1) -> Remediation(cve=CVE-1)
	//                  -> VulnFound(cve=CVE-2) -> Remediation(cve=CVE-2)
	p := gorapide.Build().
		Event("ScanStart").
		Event("VulnFound", "cve", "CVE-1").CausedBy("ScanStart").
		Event("Remediation", "cve", "CVE-1").CausedBy("VulnFound").
		MustDone()

	cves := []string{"CVE-1"}
	pat := ForEach(cves, func(a, b Pattern) Pattern { return Seq(a, b) }, func(cve string) Pattern {
		return Seq(
			MatchEvent("VulnFound").WhereParam("cve", cve),
			MatchEvent("Remediation").WhereParam("cve", cve),
		)
	})

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("ForEach with 1 CVE: expected 1 match, got %d", len(results))
	}
}

func TestForEachEmptyItems(t *testing.T) {
	p := gorapide.Build().Event("A").MustDone()

	pat := ForEach([]string{}, func(a, b Pattern) Pattern { return Seq(a, b) }, func(s string) Pattern {
		return MatchEvent(s)
	})

	results := pat.Match(p)
	if len(results) != 0 {
		t.Errorf("ForEach with empty items should return empty, got %d", len(results))
	}
}

func TestForEachSingleItem(t *testing.T) {
	p := gorapide.Build().Event("A").MustDone()

	pat := ForEach([]string{"A"}, func(a, b Pattern) Pattern { return Seq(a, b) }, func(s string) Pattern {
		return MatchEvent(s)
	})

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("ForEach with 1 item: expected 1 match, got %d", len(results))
	}
}

func TestForEachWithInts(t *testing.T) {
	p := gorapide.Build().
		Event("Step", "n", 1).
		Event("Step", "n", 2).
		Event("Step", "n", 3).
		MustDone()

	steps := []int{1, 2, 3}
	pat := ForEach(steps, func(a, b Pattern) Pattern { return Union(a, b) }, func(n int) Pattern {
		return MatchEvent("Step").WhereParam("n", n)
	})

	results := pat.Match(p)
	// Union produces cartesian product. With 3 singletons:
	// Union(Union(Step1, Step2), Step3)
	// Union(Step1, Step2) = [{Step1,Step2}]
	// Union([{Step1,Step2}], Step3) = [{Step1,Step2,Step3}]
	if len(results) != 1 {
		t.Fatalf("ForEach with ints: expected 1 combined result, got %d", len(results))
	}
	if len(results[0]) != 3 {
		t.Errorf("combined result should have 3 events, got %d", len(results[0]))
	}
}

type vuln struct {
	CVE      string
	Severity string
}

func TestForEachWithStructs(t *testing.T) {
	p := gorapide.Build().
		Event("VulnFound", "cve", "CVE-1", "severity", "HIGH").
		Event("VulnFound", "cve", "CVE-2", "severity", "LOW").
		MustDone()

	vulns := []vuln{
		{CVE: "CVE-1", Severity: "HIGH"},
		{CVE: "CVE-2", Severity: "LOW"},
	}

	pat := ForEach(vulns, func(a, b Pattern) Pattern { return Union(a, b) }, func(v vuln) Pattern {
		return MatchEvent("VulnFound").WhereParam("cve", v.CVE).WhereParam("severity", v.Severity)
	})

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("ForEach with structs: expected 1 combined result, got %d", len(results))
	}
	if len(results[0]) != 2 {
		t.Errorf("combined result should have 2 events, got %d", len(results[0]))
	}
}

func TestForEachMultipleItemsWithSeq(t *testing.T) {
	// A -> B -> C
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		MustDone()

	items := []string{"A", "B", "C"}
	// Seq(Seq(Match("A"), Match("B")), Match("C"))
	pat := ForEach(items, func(a, b Pattern) Pattern { return Seq(a, b) }, func(name string) Pattern {
		return MatchEvent(name)
	})

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("ForEach with Seq over A,B,C: expected 1 match, got %d", len(results))
	}
	if len(results[0]) != 3 {
		t.Errorf("match should have 3 events, got %d", len(results[0]))
	}
}

// --- Guard Pattern Tests ---

func TestGuardTrueCondition(t *testing.T) {
	p := gorapide.Build().Event("A").MustDone()

	pat := Guard(MatchEvent("A"), func() bool { return true })
	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("Guard(true): expected 1 match, got %d", len(results))
	}
}

func TestGuardFalseCondition(t *testing.T) {
	p := gorapide.Build().Event("A").MustDone()

	pat := Guard(MatchEvent("A"), func() bool { return false })
	results := pat.Match(p)
	if len(results) != 0 {
		t.Errorf("Guard(false): expected 0 matches, got %d", len(results))
	}
}

func TestGuardDynamicCondition(t *testing.T) {
	p := gorapide.Build().Event("A").MustDone()

	enabled := false
	pat := Guard(MatchEvent("A"), func() bool { return enabled })

	results := pat.Match(p)
	if len(results) != 0 {
		t.Error("Guard should return empty when condition is false")
	}

	enabled = true
	results = pat.Match(p)
	if len(results) != 1 {
		t.Error("Guard should return matches when condition becomes true")
	}
}

func TestGuardPanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Guard with nil should panic")
		}
	}()
	Guard(nil, func() bool { return true })
}

// --- Not Pattern Tests ---

func TestNotMatchesNothing(t *testing.T) {
	p := gorapide.Build().Event("A").MustDone()

	pat := Not(MatchEvent("A"))
	results := pat.Match(p)
	if len(results) != 0 {
		t.Errorf("Not should match nothing directly, got %d", len(results))
	}
}

func TestNotIsMarker(t *testing.T) {
	inner := MatchEvent("A")
	pat := Not(inner)

	unwrapped, ok := IsNot(pat)
	if !ok {
		t.Fatal("IsNot should return true for Not patterns")
	}
	if unwrapped.String() != inner.String() {
		t.Errorf("IsNot should return inner pattern, got %s", unwrapped.String())
	}
}

func TestIsNotReturnsFalseForNonNot(t *testing.T) {
	_, ok := IsNot(MatchEvent("A"))
	if ok {
		t.Error("IsNot should return false for non-Not patterns")
	}
}

func TestNotPanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Not with nil should panic")
		}
	}()
	Not(nil)
}

// --- String() Tests for New Patterns ---

func TestNewCompositePatternStrings(t *testing.T) {
	tests := []struct {
		pat  Pattern
		want string
	}{
		{
			Or(MatchEvent("A"), MatchEvent("B")),
			`Or(Match("A"), Match("B"))`,
		},
		{
			And(MatchEvent("A"), MatchEvent("B")),
			`And(Match("A"), Match("B"))`,
		},
		{
			Union(MatchEvent("A"), MatchEvent("B")),
			`Union(Match("A"), Match("B"))`,
		},
		{
			Guard(MatchEvent("A"), func() bool { return true }),
			`Guard(Match("A"))`,
		},
		{
			Not(MatchEvent("A")),
			`Not(Match("A"))`,
		},
	}
	for _, tt := range tests {
		got := tt.pat.String()
		if got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

// --- Complex Nesting Tests ---

func TestSeqMatchAOrMatchBC(t *testing.T) {
	// A -> B, A -> C
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		MustDone()

	// Seq(Match("A"), Or(Match("B"), Match("C")))
	// A causally before B => match {A,B}
	// A causally before C => match {A,C}
	pat := Seq(MatchEvent("A"), Or(MatchEvent("B"), MatchEvent("C")))
	results := pat.Match(p)
	if len(results) != 2 {
		t.Fatalf("Seq(A, Or(B,C)): expected 2 matches, got %d", len(results))
	}
}

func TestForEachInsideSeq(t *testing.T) {
	// Start -> Step(n=1) -> Step(n=2)
	p := gorapide.Build().
		Event("Start").
		Event("Step", "n", 1).CausedBy("Start").
		Event("Step", "n", 2).CausedBy("Step").
		MustDone()

	items := []int{1, 2}
	innerPat := ForEach(items, func(a, b Pattern) Pattern { return Seq(a, b) }, func(n int) Pattern {
		return MatchEvent("Step").WhereParam("n", n)
	})

	// Seq(Start, ForEach([1,2], Seq, fn))
	// ForEach produces Seq(Step(n=1), Step(n=2))
	// Outer Seq: Start causally before {Step1, Step2}
	pat := Seq(MatchEvent("Start"), innerPat)
	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("Seq(Start, ForEach(Seq, steps)): expected 1 match, got %d", len(results))
	}
	if len(results[0]) != 3 {
		t.Errorf("match should have 3 events, got %d", len(results[0]))
	}
}

func TestOrAndInteraction(t *testing.T) {
	// A is both from scanner source and named A.
	p := gorapide.Build().
		Source("scanner").
		Event("A").
		Event("B").
		MustDone()

	// And(Or(Match(A), Match(B)), MatchAny().WhereSource("scanner"))
	// Or returns {A}, {B}. WhereSource returns {A}, {B}.
	// And intersects: {A} and {B} both appear in both sets.
	results := And(
		Or(MatchEvent("A"), MatchEvent("B")),
		MatchAny().WhereSource("scanner"),
	).Match(p)
	if len(results) != 2 {
		t.Errorf("And(Or(A,B), WhereSource): expected 2, got %d", len(results))
	}
}
