package constraint

import (
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// --- ConstraintKind ---

func TestConstraintKindConstants(t *testing.T) {
	if MustMatch == MustNotMatch || MustMatch == MustNever || MustNotMatch == MustNever {
		t.Error("MustMatch, MustNotMatch, and MustNever must be distinct")
	}
}

func TestMustNotMatchIsExactAndDistinctFromMustNever(t *testing.T) {
	one := gorapide.Build().Event("X").MustDone()
	two := gorapide.Build().Event("X").Event("X").MustDone()
	negative := NewConstraint("negative").
		MustNotMatch("not_exactly_one", pattern.MatchEvent("X"), "must not be exactly one X").
		Build()
	never := NewConstraint("never").
		MustNever("no_x", pattern.MatchEvent("X"), "X must never occur").
		Build()

	violations, err := negative.CheckDeterministic(one)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Kind != MustNotMatch || len(violations[0].MatchedEvents) != 1 {
		t.Fatalf("negative exact-match violation=%#v", violations)
	}
	violations, err = negative.CheckDeterministic(two)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("negative exact match rejected a non-exact computation: %#v", violations)
	}
	violations, err = never.CheckDeterministic(two)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 2 {
		t.Fatalf("never did not reject both contained occurrences: %#v", violations)
	}
}

// --- Builder ---

func TestBuilderCreatesConstraint(t *testing.T) {
	c := NewConstraint("test_constraint").
		Description("a test constraint").
		Severity("error").
		Must("has_event", pattern.MatchEvent("X"), "X must exist").
		MustNever("no_bad", pattern.MatchEvent("Bad"), "Bad must not exist").
		Build()

	if c.Name != "test_constraint" {
		t.Errorf("Name: want test_constraint, got %s", c.Name)
	}
	if c.Desc != "a test constraint" {
		t.Errorf("Desc: want 'a test constraint', got %s", c.Desc)
	}
	if c.Severity != "error" {
		t.Errorf("Severity: want error, got %s", c.Severity)
	}
	if len(c.Clauses) != 2 {
		t.Fatalf("Clauses: want 2, got %d", len(c.Clauses))
	}
	if c.Clauses[0].Kind != MustMatch {
		t.Error("first clause should be MustMatch")
	}
	if c.Clauses[1].Kind != MustNever {
		t.Error("second clause should be MustNever")
	}
}

func TestBuilderWithFilter(t *testing.T) {
	c := NewConstraint("filtered").
		FilterBy(pattern.MatchEvent("X")).
		Must("has_x", pattern.MatchEvent("X"), "X required").
		Build()

	if c.Filter == nil {
		t.Error("Filter should be set")
	}
}

// --- MustMatch: passes when pattern is present ---

func TestMustMatchPassesWhenPresent(t *testing.T) {
	p := gorapide.Build().
		Event("ScanStart").
		Event("ScanComplete").CausedBy("ScanStart").
		MustDone()

	c := NewConstraint("ordering").
		Must("start_before_complete",
			pattern.Seq(pattern.MatchEvent("ScanStart"), pattern.MatchEvent("ScanComplete")),
			"ScanStart must precede ScanComplete").
		Build()

	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %d: %v", len(violations), violations)
	}
}

// --- MustMatch: violation when pattern is absent ---

func TestMustMatchViolationWhenAbsent(t *testing.T) {
	p := gorapide.Build().
		Event("ScanComplete"). // no ScanStart
		MustDone()

	c := NewConstraint("ordering").
		Severity("error").
		Must("start_before_complete",
			pattern.Seq(pattern.MatchEvent("ScanStart"), pattern.MatchEvent("ScanComplete")),
			"ScanStart must precede ScanComplete").
		Build()

	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	v := violations[0]
	if v.Constraint != "ordering" {
		t.Errorf("Constraint: want ordering, got %s", v.Constraint)
	}
	if v.Clause != "start_before_complete" {
		t.Errorf("Clause: want start_before_complete, got %s", v.Clause)
	}
	if v.Kind != MustMatch {
		t.Error("Kind should be MustMatch")
	}
	if v.Severity != "error" {
		t.Errorf("Severity: want error, got %s", v.Severity)
	}
	if v.Message != "ScanStart must precede ScanComplete" {
		t.Errorf("Message: got %s", v.Message)
	}
}

// --- MustNever: passes when forbidden pattern is absent ---

func TestMustNeverPassesWhenAbsent(t *testing.T) {
	p := gorapide.Build().
		Event("ScanStart").
		Event("ScanComplete").CausedBy("ScanStart").
		MustDone()

	c := NewConstraint("no_bad").
		MustNever("no_error_events", pattern.MatchEvent("Error"), "Error events should not exist").
		Build()

	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %d", len(violations))
	}
}

// --- MustNever: violation when forbidden pattern matches ---

func TestMustNeverViolationWhenPresent(t *testing.T) {
	p := gorapide.Build().
		Event("ScanStart").
		Event("Error", "msg", "disk full").CausedBy("ScanStart").
		MustDone()

	c := NewConstraint("no_errors").
		Severity("warning").
		MustNever("no_error_events", pattern.MatchEvent("Error"), "Error events should not exist").
		Build()

	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	v := violations[0]
	if v.Kind != MustNever {
		t.Error("Kind should be MustNever")
	}
	if v.Severity != "warning" {
		t.Errorf("Severity: want warning, got %s", v.Severity)
	}
	if len(v.MatchedEvents) != 1 {
		t.Fatalf("MatchedEvents: want 1, got %d", len(v.MatchedEvents))
	}
	if v.MatchedEvents[0].Name != "Error" {
		t.Errorf("matched event: want Error, got %s", v.MatchedEvents[0].Name)
	}
}

// --- ATO: Completeness constraint ---

func TestCompletenessConstraintPasses(t *testing.T) {
	// Complete pipeline: VulnFound → Finding → DocSection.
	p := gorapide.Build().
		Source("scanner").
		Event("ScanStart").
		Event("VulnFound", "severity", "HIGH").CausedBy("ScanStart").
		Source("aggregator").
		Event("Finding").CausedBy("VulnFound").
		Source("renderer").
		Event("DocSection").CausedBy("Finding").
		MustDone()

	c := NewConstraint("completeness").
		Severity("error").
		Must("vuln_has_doc",
			pattern.Seq(pattern.MatchEvent("VulnFound"), pattern.MatchEvent("DocSection")),
			"Every VulnFound must have a downstream DocSection").
		Build()

	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("complete pipeline should have 0 violations, got %d", len(violations))
	}
}

func TestCompletenessConstraintViolation(t *testing.T) {
	// Incomplete: VulnFound exists but no DocSection downstream.
	p := gorapide.Build().
		Source("scanner").
		Event("ScanStart").
		Event("VulnFound", "severity", "CRITICAL").CausedBy("ScanStart").
		Source("aggregator").
		Event("Finding").CausedBy("VulnFound").
		// No DocSection!
		MustDone()

	c := NewConstraint("completeness").
		Severity("error").
		Must("vuln_has_doc",
			pattern.Seq(pattern.MatchEvent("VulnFound"), pattern.MatchEvent("DocSection")),
			"Every VulnFound must have a downstream DocSection").
		Build()

	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("missing DocSection should produce 1 violation, got %d", len(violations))
	}
	if violations[0].Clause != "vuln_has_doc" {
		t.Errorf("Clause: want vuln_has_doc, got %s", violations[0].Clause)
	}
}

// --- ATO: No-orphan constraint ---

func TestNoOrphanConstraintPasses(t *testing.T) {
	// DocSection has VulnFound ancestor — not independent.
	p := gorapide.Build().
		Event("VulnFound").
		Event("DocSection").CausedBy("VulnFound").
		MustDone()

	c := NewConstraint("no_orphan").
		MustNever("orphan_doc",
			pattern.Independent(pattern.MatchEvent("DocSection"), pattern.MatchEvent("VulnFound")),
			"DocSection must not be independent of VulnFound").
		Build()

	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("linked DocSection should have 0 violations, got %d", len(violations))
	}
}

func TestNoOrphanConstraintViolation(t *testing.T) {
	// DocSection is independent of VulnFound — orphan.
	p := gorapide.Build().
		Event("VulnFound").
		Event("DocSection"). // no causal link
		MustDone()

	c := NewConstraint("no_orphan").
		Severity("error").
		MustNever("orphan_doc",
			pattern.Independent(pattern.MatchEvent("DocSection"), pattern.MatchEvent("VulnFound")),
			"DocSection must not be independent of VulnFound").
		Build()

	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("orphan DocSection should produce 1 violation, got %d", len(violations))
	}
	v := violations[0]
	if len(v.MatchedEvents) != 2 {
		t.Errorf("MatchedEvents should have 2 events, got %d", len(v.MatchedEvents))
	}
}

// --- Ordering constraint ---

func TestOrderingConstraintPasses(t *testing.T) {
	p := gorapide.Build().
		Event("ScanStart").
		Event("ScanComplete").CausedBy("ScanStart").
		MustDone()

	c := NewConstraint("ordering").
		Must("start_before_complete",
			pattern.Seq(pattern.MatchEvent("ScanStart"), pattern.MatchEvent("ScanComplete")),
			"ScanStart must precede ScanComplete").
		Build()

	if len(c.Check(p)) != 0 {
		t.Error("correct ordering should have 0 violations")
	}
}

func TestOrderingConstraintViolation(t *testing.T) {
	// ScanStart and ScanComplete exist but are independent (no causal order).
	p := gorapide.Build().
		Event("ScanStart").
		Event("ScanComplete"). // no CausedBy
		MustDone()

	c := NewConstraint("ordering").
		Must("start_before_complete",
			pattern.Seq(pattern.MatchEvent("ScanStart"), pattern.MatchEvent("ScanComplete")),
			"ScanStart must precede ScanComplete").
		Build()

	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("unordered events should produce 1 violation, got %d", len(violations))
	}
}

// --- Filter scoping ---

func TestFilterScopingIncludesMatching(t *testing.T) {
	p := gorapide.Build().
		Source("scanner").
		Event("ScanStart").
		Event("VulnFound").CausedBy("ScanStart").
		Source("other").
		Event("Unrelated").
		MustDone()

	// Filter to scanner events only.
	c := NewConstraint("scanner_check").
		FilterBy(pattern.MatchAny().WhereSource("scanner")).
		Must("has_vuln",
			pattern.MatchEvent("VulnFound"),
			"Scanner must find vulnerabilities").
		Build()

	violations := c.Check(p)
	if len(violations) != 0 {
		t.Error("scanner has VulnFound — should pass")
	}
}

func TestFilterScopingExcludesNonMatching(t *testing.T) {
	p := gorapide.Build().
		Source("scanner").
		Event("ScanStart").
		Source("other").
		Event("VulnFound"). // VulnFound from wrong source
		MustDone()

	// Filter to scanner events only.
	c := NewConstraint("scanner_check").
		FilterBy(pattern.MatchAny().WhereSource("scanner")).
		Must("has_vuln",
			pattern.MatchEvent("VulnFound"),
			"Scanner must find vulnerabilities").
		Build()

	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("VulnFound from other source should not satisfy filter, want 1 violation, got %d", len(violations))
	}
}

func TestFilterProjectionPreservesCausalityButHidesIntermediate(t *testing.T) {
	p := gorapide.Build().
		Event("VisibleStart", "visible", true).
		Event("Hidden", "visible", false).CausedBy("VisibleStart").
		Event("VisibleEnd", "visible", true).CausedBy("Hidden").
		MustDone()

	c := NewConstraint("visible_projection").
		FilterBy(pattern.MatchAny().WhereParam("visible", true)).
		Must("projected_immediate",
			pattern.ImmSeq(pattern.MatchEvent("VisibleStart"), pattern.MatchEvent("VisibleEnd")),
			"VisibleStart must immediately precede VisibleEnd in the projected poset").
		MustNever("hidden_absent", pattern.MatchEvent("Hidden"), "Hidden must not be matchable").
		Build()

	if violations := c.Check(p); len(violations) != 0 {
		t.Fatalf("projected constraint produced violations: %v", violations)
	}
}

func TestMatchConstraintRequiresWholeAssociatedComputation(t *testing.T) {
	p := gorapide.Build().Event("X").Event("X").MustDone()
	c := NewConstraint("whole").
		Must("exactly_described", pattern.MatchEvent("X"), "all X events must match").
		Build()

	violations, err := c.CheckDeterministic(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 {
		t.Fatalf("existential subset incorrectly satisfied match constraint: %d violations", len(violations))
	}
}

func TestMatchConstraintIgnoresUnassociatedEventsButRejectsExtraAssociatedEvent(t *testing.T) {
	p := gorapide.Build().
		Event("Start").
		Event("End").CausedBy("Start").
		Event("Unrelated").
		MustDone()
	c := NewConstraint("whole_sequence").
		Must("sequence", pattern.Seq(pattern.MatchEvent("Start"), pattern.MatchEvent("End")), "exact sequence required").
		Build()

	violations, err := c.CheckDeterministic(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("unassociated event affected match constraint: %v", violations)
	}

	extraEnd := gorapide.NewEvent("End", "default", nil)
	start := p.ByName("Start")[0]
	if err := p.AddEventWithCause(extraEnd, start.ID); err != nil {
		t.Fatal(err)
	}
	violations, err = c.CheckDeterministic(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 {
		t.Fatalf("extra associated event did not violate exact match: %d violations", len(violations))
	}
}

func TestCheckDeterministicRejectsOpaquePattern(t *testing.T) {
	p := gorapide.Build().Event("X").MustDone()
	c := NewConstraint("opaque").
		Must("opaque", pattern.MatchEvent("X").Where(func(*gorapide.Event) bool { return true }), "unsupported").
		Build()

	_, err := c.CheckDeterministic(p)
	if !errors.Is(err, ErrConstraintEvaluation) {
		t.Fatalf("expected ErrConstraintEvaluation, got %v", err)
	}
	legacy := c.Check(p)
	if len(legacy) != 1 || legacy[0].Clause != "evaluation" {
		t.Fatalf("legacy Check did not expose evaluation failure: %#v", legacy)
	}
}

func TestIteratedMatchConstraintRequiresAllEventsInCausalOrder(t *testing.T) {
	ordered := gorapide.Build().
		Event("WriteReturn").
		Event("WriteReturn").CausedBy("WriteReturn").
		Event("WriteReturn").CausedBy("WriteReturn").
		MustDone()
	constraint := NewConstraint("write_order").
		Must("all_ordered", pattern.IterateZeroOrMore(pattern.MatchEvent("WriteReturn"), pattern.RelationFollows), "all write returns must be ordered").
		Build()
	violations, err := constraint.CheckDeterministic(ordered)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("ordered computation violated iterated match: %v", violations)
	}

	unordered := gorapide.Build().
		Event("WriteReturn").
		Event("WriteReturn").
		Event("WriteReturn").
		MustDone()
	violations, err = constraint.CheckDeterministic(unordered)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 {
		t.Fatalf("unordered computation produced %d violations, want 1", len(violations))
	}
}

func TestDisjointRangeConstraintDetectsMissingAndDuplicateOccurrences(t *testing.T) {
	constraint := NewConstraint("exactly_two_approvals").
		Must("two", pattern.IterateRange(pattern.MatchEvent("Approval"), pattern.RelationDisjoint, 2, 2), "exactly two approvals required").
		Build()
	for count, wantViolations := range map[int]int{1: 1, 2: 0, 3: 1} {
		poset := gorapide.NewPoset()
		for i := 0; i < count; i++ {
			if err := poset.AddEvent(gorapide.NewEvent("Approval", "reviewer", map[string]any{"index": i})); err != nil {
				t.Fatal(err)
			}
		}
		violations, err := constraint.CheckDeterministic(poset)
		if err != nil {
			t.Fatal(err)
		}
		if len(violations) != wantViolations {
			t.Errorf("count %d: got %d violations, want %d", count, len(violations), wantViolations)
		}
	}
}

// --- Multiple clauses checked independently ---

func TestMultipleClausesAllPass(t *testing.T) {
	p := gorapide.Build().
		Event("ScanStart").
		Event("ScanComplete").CausedBy("ScanStart").
		MustDone()

	c := NewConstraint("multi").
		Must("has_ordering",
			pattern.Seq(pattern.MatchEvent("ScanStart"), pattern.MatchEvent("ScanComplete")),
			"ScanStart must precede ScanComplete").
		MustNever("no_error",
			pattern.MatchEvent("Error"),
			"Error events should not exist").
		Build()

	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("both clauses should pass, got %d violations", len(violations))
	}
}

func TestMultipleClausesBothViolate(t *testing.T) {
	p := gorapide.Build().
		Event("ScanStart").
		Event("ScanComplete"). // independent — violates ordering
		Event("Error").        // exists — violates never clause
		MustDone()

	c := NewConstraint("multi").
		Must("has_ordering",
			pattern.Seq(pattern.MatchEvent("ScanStart"), pattern.MatchEvent("ScanComplete")),
			"ScanStart must precede ScanComplete").
		MustNever("no_error",
			pattern.MatchEvent("Error"),
			"Error events should not exist").
		Build()

	violations := c.Check(p)
	if len(violations) != 2 {
		t.Fatalf("both clauses should violate, want 2 violations, got %d", len(violations))
	}

	// Verify each violation is for a different clause.
	names := map[string]bool{}
	for _, v := range violations {
		names[v.Clause] = true
	}
	if !names["has_ordering"] {
		t.Error("should have violation for has_ordering")
	}
	if !names["no_error"] {
		t.Error("should have violation for no_error")
	}
}

// --- Empty poset ---

func TestCheckEmptyPoset(t *testing.T) {
	p := gorapide.NewPoset()

	c := NewConstraint("empty_check").
		Must("has_something", pattern.MatchEvent("X"), "X must exist").
		Build()

	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("empty poset should violate MustMatch, got %d violations", len(violations))
	}
}

func TestCheckEmptyPosetNeverPasses(t *testing.T) {
	p := gorapide.NewPoset()

	c := NewConstraint("empty_check").
		MustNever("no_bad", pattern.MatchEvent("Bad"), "Bad must not exist").
		Build()

	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("empty poset should pass MustNever, got %d violations", len(violations))
	}
}

// --- Violation String ---

func TestConstraintViolationString(t *testing.T) {
	v := ConstraintViolation{
		Constraint: "test",
		Clause:     "clause1",
		Kind:       MustMatch,
		Message:    "something failed",
		Severity:   "error",
	}
	s := v.String()
	if len(s) == 0 {
		t.Error("String() should not be empty")
	}
	if !containsSub(s, "test") || !containsSub(s, "clause1") || !containsSub(s, "something failed") {
		t.Errorf("String() should contain constraint name, clause, and message: got %s", s)
	}
}

// --- Constraint String ---

func TestConstraintString(t *testing.T) {
	c := NewConstraint("my_constraint").
		Must("c1", pattern.MatchEvent("X"), "x required").
		MustNever("c2", pattern.MatchEvent("Y"), "y forbidden").
		Build()

	s := c.String()
	if len(s) == 0 {
		t.Error("String() should not be empty")
	}
	if !containsSub(s, "my_constraint") {
		t.Errorf("String() should contain constraint name: got %s", s)
	}
}

// --- Default severity ---

func TestDefaultSeverity(t *testing.T) {
	c := NewConstraint("test").
		Must("c1", pattern.MatchEvent("X"), "msg").
		Build()

	if c.Severity != "error" {
		t.Errorf("default severity should be error, got %s", c.Severity)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
