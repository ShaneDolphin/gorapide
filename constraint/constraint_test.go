package constraint

import (
	"testing"

	"github.com/beautiful-majestic-dolphin/gorapide"
	"github.com/beautiful-majestic-dolphin/gorapide/pattern"
)

// --- ConstraintKind ---

func TestConstraintKindConstants(t *testing.T) {
	if MustMatch == MustNever {
		t.Error("MustMatch and MustNever must be distinct")
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
