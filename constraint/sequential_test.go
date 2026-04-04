package constraint

import (
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// --- Checker interface ---

func TestPredicateConstraintImplementsCheckable(t *testing.T) {
	var c Checkable = &PredicateConstraint{}
	_ = c
}

func TestPatternConstraintImplementsCheckable(t *testing.T) {
	c := NewConstraint("test").
		Must("c1", pattern.MatchEvent("X"), "msg").
		Build()
	var checker Checkable = c
	_ = checker
}

// --- PredicateConstraint ---

func TestPredicateConstraintBasic(t *testing.T) {
	pc := &PredicateConstraint{
		Name:     "custom",
		Desc:     "custom predicate",
		Severity: "error",
		Predicate: func(p *gorapide.Poset) []ConstraintViolation {
			if p.Len() == 0 {
				return []ConstraintViolation{{
					Constraint: "custom",
					Clause:     "non_empty",
					Kind:       MustMatch,
					Message:    "poset must not be empty",
					Severity:   "error",
				}}
			}
			return nil
		},
	}

	// Empty poset — should violate.
	p := gorapide.NewPoset()
	violations := pc.Check(p)
	if len(violations) != 1 {
		t.Fatalf("empty poset: want 1 violation, got %d", len(violations))
	}
	if violations[0].Message != "poset must not be empty" {
		t.Errorf("message: got %s", violations[0].Message)
	}

	// Non-empty poset — should pass.
	p2 := gorapide.Build().Event("X").MustDone()
	violations2 := pc.Check(p2)
	if len(violations2) != 0 {
		t.Errorf("non-empty poset: want 0 violations, got %d", len(violations2))
	}
}

func TestPredicateConstraintString(t *testing.T) {
	pc := &PredicateConstraint{
		Name:     "my_pred",
		Desc:     "a description",
		Severity: "warning",
		Predicate: func(p *gorapide.Poset) []ConstraintViolation {
			return nil
		},
	}
	s := pc.String()
	if !strings.Contains(s, "my_pred") {
		t.Errorf("String() should contain name: got %s", s)
	}
	if !strings.Contains(s, "warning") {
		t.Errorf("String() should contain severity: got %s", s)
	}
}

// --- EventCount ---

func TestEventCountExact(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		MustDone()

	c := EventCount("A", 1, 1)
	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("exact count match: want 0 violations, got %d", len(violations))
	}
}

func TestEventCountTooFew(t *testing.T) {
	p := gorapide.NewPoset()

	c := EventCount("X", 1, 5)
	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("too few: want 1 violation, got %d", len(violations))
	}
	if violations[0].Constraint != "event_count_X" {
		t.Errorf("Constraint: got %s", violations[0].Constraint)
	}
}

func TestEventCountTooMany(t *testing.T) {
	p := gorapide.Build().
		Event("X").
		Event("X").
		Event("X").
		MustDone()

	c := EventCount("X", 1, 2)
	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("too many: want 1 violation, got %d", len(violations))
	}
}

func TestEventCountZeroMin(t *testing.T) {
	p := gorapide.NewPoset()

	c := EventCount("X", 0, 5)
	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("zero min with no events: want 0 violations, got %d", len(violations))
	}
}

// --- NoUnlinkedEvents ---

func TestNoUnlinkedEventsAllLinked(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		MustDone()

	c := NoUnlinkedEvents()
	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("all linked: want 0 violations, got %d", len(violations))
	}
}

func TestNoUnlinkedEventsSingleEvent(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		MustDone()

	c := NoUnlinkedEvents()
	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("single event (root): want 0 violations, got %d", len(violations))
	}
}

func TestNoUnlinkedEventsWithOrphans(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B"). // no causal link — independent root
		MustDone()

	c := NoUnlinkedEvents()
	violations := c.Check(p)
	// Two independent roots when there are multiple events means they are unlinked.
	if len(violations) == 0 {
		t.Error("unlinked events should produce violations")
	}
}

func TestNoUnlinkedEventsEmpty(t *testing.T) {
	p := gorapide.NewPoset()
	c := NoUnlinkedEvents()
	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("empty poset: want 0 violations, got %d", len(violations))
	}
}

// --- SingleRoot ---

func TestSingleRootPasses(t *testing.T) {
	p := gorapide.Build().
		Event("Root").
		Event("Child").CausedBy("Root").
		MustDone()

	c := SingleRoot()
	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("single root: want 0 violations, got %d", len(violations))
	}
}

func TestSingleRootViolation(t *testing.T) {
	p := gorapide.Build().
		Event("Root1").
		Event("Root2").
		MustDone()

	c := SingleRoot()
	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("multiple roots: want 1 violation, got %d", len(violations))
	}
	if !strings.Contains(violations[0].Message, "2") {
		t.Errorf("message should mention root count: got %s", violations[0].Message)
	}
}

func TestSingleRootEmpty(t *testing.T) {
	p := gorapide.NewPoset()
	c := SingleRoot()
	violations := c.Check(p)
	// Empty poset has 0 roots — that's a violation (not exactly 1).
	if len(violations) != 1 {
		t.Fatalf("empty poset: want 1 violation, got %d", len(violations))
	}
}

// --- CompletesWithin ---

func TestCompletesWithinPasses(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		MustDone()

	// Causal depth is 3 (A -> B -> C), max depth 5.
	c := CompletesWithin(5)
	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("within limit: want 0 violations, got %d", len(violations))
	}
}

func TestCompletesWithinViolation(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		Event("D").CausedBy("C").
		MustDone()

	// Longest chain: A -> B -> C -> D = depth 4, limit 2.
	c := CompletesWithin(2)
	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("exceeds limit: want 1 violation, got %d", len(violations))
	}
}

func TestCompletesWithinEmpty(t *testing.T) {
	p := gorapide.NewPoset()
	c := CompletesWithin(10)
	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("empty poset: want 0 violations, got %d", len(violations))
	}
}

// --- AllComponentsEmit ---

func TestAllComponentsEmitPasses(t *testing.T) {
	p := gorapide.Build().
		Source("scanner").
		Event("ScanStart").
		Source("renderer").
		Event("Render").
		MustDone()

	c := AllComponentsEmit([]string{"scanner", "renderer"})
	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("all emit: want 0 violations, got %d", len(violations))
	}
}

func TestAllComponentsEmitMissing(t *testing.T) {
	p := gorapide.Build().
		Source("scanner").
		Event("ScanStart").
		MustDone()

	c := AllComponentsEmit([]string{"scanner", "renderer"})
	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("missing component: want 1 violation, got %d", len(violations))
	}
	if !strings.Contains(violations[0].Message, "renderer") {
		t.Errorf("message should mention missing component: got %s", violations[0].Message)
	}
}

func TestAllComponentsEmitEmpty(t *testing.T) {
	p := gorapide.NewPoset()
	c := AllComponentsEmit([]string{"scanner"})
	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("empty poset: want 1 violation, got %d", len(violations))
	}
}

// --- CausalDepthMax ---

func TestCausalDepthMaxPasses(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		MustDone()

	c := CausalDepthMax(5)
	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("within limit: want 0 violations, got %d", len(violations))
	}
}

func TestCausalDepthMaxViolation(t *testing.T) {
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		Event("D").CausedBy("C").
		Event("E").CausedBy("D").
		MustDone()

	// Longest chain: 5 events, max depth 3.
	c := CausalDepthMax(3)
	violations := c.Check(p)
	if len(violations) != 1 {
		t.Fatalf("exceeds limit: want 1 violation, got %d", len(violations))
	}
}

func TestCausalDepthMaxEmpty(t *testing.T) {
	p := gorapide.NewPoset()
	c := CausalDepthMax(10)
	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("empty poset: want 0 violations, got %d", len(violations))
	}
}

func TestCausalDepthMaxBranching(t *testing.T) {
	// Fan-out: A -> B, A -> C. Max depth is 2.
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		MustDone()

	c := CausalDepthMax(2)
	violations := c.Check(p)
	if len(violations) != 0 {
		t.Errorf("branching within limit: want 0 violations, got %d", len(violations))
	}
}

// --- ConstraintSet ---

func TestNewConstraintSet(t *testing.T) {
	cs := NewConstraintSet("test_set")
	if cs == nil {
		t.Fatal("NewConstraintSet returned nil")
	}
}

func TestConstraintSetAddAndCheck(t *testing.T) {
	p := gorapide.Build().
		Event("X").
		MustDone()

	cs := NewConstraintSet("test_set")
	cs.Add(NewConstraint("c1").
		Must("has_x", pattern.MatchEvent("X"), "X required").
		Build())

	violations := cs.Check(p)
	if len(violations) != 0 {
		t.Errorf("passing constraint: want 0 violations, got %d", len(violations))
	}
}

func TestConstraintSetMixedTypes(t *testing.T) {
	p := gorapide.Build().
		Event("X").
		MustDone()

	cs := NewConstraintSet("mixed")
	// Pattern-based constraint (passes).
	cs.Add(NewConstraint("pattern_check").
		Must("has_x", pattern.MatchEvent("X"), "X required").
		Build())
	// Predicate-based constraint (passes — count is 1, within [1,5]).
	cs.Add(EventCount("X", 1, 5))

	violations := cs.Check(p)
	if len(violations) != 0 {
		t.Errorf("all passing: want 0 violations, got %d", len(violations))
	}
}

func TestConstraintSetAggregatesViolations(t *testing.T) {
	p := gorapide.NewPoset() // empty

	cs := NewConstraintSet("multi")
	cs.Add(NewConstraint("c1").
		Must("has_x", pattern.MatchEvent("X"), "X required").
		Build())
	cs.Add(EventCount("Y", 1, 5))

	violations := cs.Check(p)
	if len(violations) != 2 {
		t.Fatalf("two failing constraints: want 2 violations, got %d", len(violations))
	}
}

func TestConstraintSetCheckAndReport(t *testing.T) {
	p := gorapide.Build().
		Event("X").
		MustDone()

	cs := NewConstraintSet("report_test")
	cs.Add(NewConstraint("c1").
		Severity("error").
		Must("has_y", pattern.MatchEvent("Y"), "Y required").
		Build())
	cs.Add(SingleRoot())

	violations, report := cs.CheckAndReport(p)
	if len(violations) != 1 {
		t.Fatalf("one failing: want 1 violation, got %d", len(violations))
	}
	if len(report) == 0 {
		t.Error("report should not be empty")
	}
	if !strings.Contains(report, "Y required") {
		t.Errorf("report should contain violation message: got %s", report)
	}
}

func TestConstraintSetCheckAndReportAllPass(t *testing.T) {
	p := gorapide.Build().
		Event("X").
		MustDone()

	cs := NewConstraintSet("all_pass")
	cs.Add(SingleRoot())
	cs.Add(EventCount("X", 1, 5))

	violations, report := cs.CheckAndReport(p)
	if len(violations) != 0 {
		t.Errorf("all pass: want 0 violations, got %d", len(violations))
	}
	if !strings.Contains(report, "pass") && !strings.Contains(report, "0 violation") {
		t.Errorf("report should indicate success: got %s", report)
	}
}

func TestConstraintSetEmpty(t *testing.T) {
	p := gorapide.NewPoset()
	cs := NewConstraintSet("empty")

	violations := cs.Check(p)
	if len(violations) != 0 {
		t.Errorf("empty set: want 0 violations, got %d", len(violations))
	}
}
