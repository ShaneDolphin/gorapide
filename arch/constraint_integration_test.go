package arch

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/constraint"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// --- CheckAfter: constraints checked on Stop ---

func TestArchCheckAfterMode(t *testing.T) {
	cs := constraint.NewConstraintSet("after_test")
	cs.Add(constraint.NewConstraint("completeness").
		Must("has_scan",
			pattern.MatchEvent("ScanStart"),
			"ScanStart required").
		Build())

	a := NewArchitecture("test_after")
	a.WithConstraints(cs, constraint.CheckAfter)

	prod := NewComponent("producer", Interface("P").OutAction("Other").Build(), nil)
	a.AddComponent(prod)

	a.Start(context.Background())

	// Emit an event that isn't ScanStart — constraint will fail.
	prod.Emit("Other", nil)
	time.Sleep(50 * time.Millisecond)

	a.Stop()
	a.Wait()

	violations := a.CheckConstraints()
	if len(violations) != 1 {
		t.Fatalf("CheckAfter: want 1 violation, got %d", len(violations))
	}
	if violations[0].Message != "ScanStart required" {
		t.Errorf("message: got %s", violations[0].Message)
	}
}

func TestArchCheckAfterPassesClean(t *testing.T) {
	cs := constraint.NewConstraintSet("after_clean")
	cs.Add(constraint.NewConstraint("has_x").
		Must("x_exists", pattern.MatchEvent("X"), "X required").
		Build())

	a := NewArchitecture("test_clean")
	a.WithConstraints(cs, constraint.CheckAfter)

	prod := NewComponent("producer", Interface("P").OutAction("X").Build(), nil)
	a.AddComponent(prod)

	a.Start(context.Background())
	prod.Emit("X", nil)
	time.Sleep(50 * time.Millisecond)

	a.Stop()
	a.Wait()

	violations := a.CheckConstraints()
	if len(violations) != 0 {
		t.Errorf("clean: want 0 violations, got %d", len(violations))
	}
}

// --- CheckOnEvent: triggers after N events ---

func TestArchCheckOnEventMode(t *testing.T) {
	cs := constraint.NewConstraintSet("on_event_test")
	cs.Add(constraint.EventCount("Bad", 0, 0)) // "Bad" must not exist

	var mu sync.Mutex
	var violations []constraint.ConstraintViolation

	a := NewArchitecture("test_on_event")
	a.WithConstraintsOpts(cs, constraint.CheckOnEvent, func(ch *constraint.Checker) {
		ch.SetBatchSize(2)
		ch.OnViolation(func(v constraint.ConstraintViolation) {
			mu.Lock()
			violations = append(violations, v)
			mu.Unlock()
		})
	})

	prod := NewComponent("producer", Interface("P").OutAction("Bad").Build(), nil)
	a.AddComponent(prod)

	a.Start(context.Background())

	// Emit 2 events to trigger batch check.
	prod.Emit("Bad", nil)
	prod.Emit("Bad", nil)

	time.Sleep(100 * time.Millisecond)

	a.Stop()
	a.Wait()

	mu.Lock()
	count := len(violations)
	mu.Unlock()
	if count == 0 {
		t.Error("CheckOnEvent should catch Bad events during execution")
	}
}

// --- OnViolation callback fires with correct data ---

func TestArchOnViolationCallback(t *testing.T) {
	cs := constraint.NewConstraintSet("callback_test")
	cs.Add(constraint.NewConstraint("c1").
		Severity("error").
		Must("has_x", pattern.MatchEvent("X"), "X required").
		Build())

	var received []constraint.ConstraintViolation
	var mu sync.Mutex

	a := NewArchitecture("test_callback")
	a.WithConstraintsOpts(cs, constraint.CheckAfter, func(ch *constraint.Checker) {
		ch.OnViolation(func(v constraint.ConstraintViolation) {
			mu.Lock()
			received = append(received, v)
			mu.Unlock()
		})
	})

	prod := NewComponent("producer", Interface("P").Build(), nil)
	a.AddComponent(prod)

	a.Start(context.Background())
	a.Stop()
	a.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("callback: want 1, got %d", len(received))
	}
	if received[0].Message != "X required" {
		t.Errorf("message: got %s", received[0].Message)
	}
}

// --- ConstraintReport ---

func TestArchConstraintReport(t *testing.T) {
	cs := constraint.NewConstraintSet("report")
	cs.Add(constraint.NewConstraint("c1").
		Must("has_x", pattern.MatchEvent("X"), "X required").
		Build())

	a := NewArchitecture("test_report")
	a.WithConstraints(cs, constraint.CheckAfter)

	prod := NewComponent("producer", Interface("P").Build(), nil)
	a.AddComponent(prod)

	a.Start(context.Background())
	a.Stop()
	a.Wait()

	report := a.ConstraintReport()
	if !strings.Contains(report, "X required") {
		t.Errorf("report should contain violation: got %s", report)
	}
}

// --- CheckConstraints manual (no checker configured) ---

func TestArchCheckConstraintsManual(t *testing.T) {
	cs := constraint.NewConstraintSet("manual")
	cs.Add(constraint.SingleRoot())

	a := NewArchitecture("test_manual")
	a.WithConstraints(cs, constraint.CheckAfter)

	prod := NewComponent("producer", Interface("P").Build(), nil)
	a.AddComponent(prod)

	// Manual check before start — empty poset.
	violations := a.CheckConstraints()
	if len(violations) != 1 {
		t.Fatalf("empty poset should violate single_root, got %d", len(violations))
	}
}

// --- Clean shutdown of checker goroutine ---

func TestArchCheckerCleanShutdown(t *testing.T) {
	cs := constraint.NewConstraintSet("shutdown")
	cs.Add(constraint.EventCount("X", 0, 100))

	a := NewArchitecture("test_shutdown")
	a.WithConstraintsOpts(cs, constraint.CheckPeriodic, func(ch *constraint.Checker) {
		ch.SetInterval(10 * time.Millisecond)
	})

	prod := NewComponent("producer", Interface("P").Build(), nil)
	a.AddComponent(prod)

	a.Start(context.Background())
	time.Sleep(50 * time.Millisecond)
	a.Stop()

	done := make(chan struct{})
	go func() {
		a.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("architecture with checker did not shut down cleanly")
	}
}

// --- Full ATO scenario ---

func TestATOScenarioFullPipeline(t *testing.T) {
	// Build constraint set: ATO-relevant checks.
	cs := constraint.NewConstraintSet("ato_checks")

	// a. Completeness (predicate): every scanner VulnFound must have a
	//    downstream DocSection. This catches dropped vulns.
	cs.Add(&constraint.PredicateConstraint{
		Name:     "completeness",
		Desc:     "every scanner VulnFound must produce a DocSection",
		Severity: "error",
		Predicate: func(p *gorapide.Poset) []constraint.ConstraintViolation {
			var violations []constraint.ConstraintViolation
			for _, vuln := range p.EventsByName("VulnFound") {
				if vuln.Source != "scanner" {
					continue
				}
				hasDoc := false
				for _, desc := range p.CausalDescendants(vuln.ID) {
					if desc.Name == "DocSection" {
						hasDoc = true
						break
					}
				}
				if !hasDoc {
					violations = append(violations, constraint.ConstraintViolation{
						Constraint:    "completeness",
						Clause:        "vuln_has_doc",
						Kind:          constraint.MustMatch,
						Message:       "VulnFound " + vuln.ParamString("cve") + " has no downstream DocSection",
						MatchedEvents: gorapide.EventSet{vuln},
						Severity:      "error",
					})
				}
			}
			return violations
		},
	})

	// b. No orphan DocSections (predicate): every DocSection must have a
	//    VulnFound ancestor.
	cs.Add(&constraint.PredicateConstraint{
		Name:     "no_orphans",
		Desc:     "every DocSection must have a VulnFound ancestor",
		Severity: "error",
		Predicate: func(p *gorapide.Poset) []constraint.ConstraintViolation {
			var violations []constraint.ConstraintViolation
			for _, doc := range p.EventsByName("DocSection") {
				hasVuln := false
				for _, anc := range p.CausalAncestors(doc.ID) {
					if anc.Name == "VulnFound" {
						hasVuln = true
						break
					}
				}
				if !hasVuln {
					violations = append(violations, constraint.ConstraintViolation{
						Constraint:    "no_orphans",
						Clause:        "doc_has_vuln",
						Kind:          constraint.MustMatch,
						Message:       "DocSection has no VulnFound ancestor",
						MatchedEvents: gorapide.EventSet{doc},
						Severity:      "error",
					})
				}
			}
			return violations
		},
	})

	// c. Processing depth constraint (proxy for timing).
	cs.Add(constraint.CausalDepthMax(20))

	var mu sync.Mutex
	var callbackViolations []constraint.ConstraintViolation

	a := NewArchitecture("ato_pipeline")
	a.WithConstraintsOpts(cs, constraint.CheckAfter, func(ch *constraint.Checker) {
		ch.OnViolation(func(v constraint.ConstraintViolation) {
			mu.Lock()
			callbackViolations = append(callbackViolations, v)
			mu.Unlock()
		})
	})

	// --- Components ---
	scanner := NewComponent("scanner",
		Interface("Scanner").OutAction("VulnFound").Build(), nil)
	aggregator := NewComponent("aggregator",
		Interface("Aggregator").
			InAction("VulnFound").
			OutAction("Finding").Build(),
		nil, WithBufferSize(16))
	renderer := NewComponent("renderer",
		Interface("Renderer").
			InAction("Finding").
			OutAction("DocSection").Build(),
		nil, WithBufferSize(16))

	a.AddComponent(scanner)
	a.AddComponent(aggregator)
	a.AddComponent(renderer)

	// --- Connections ---
	a.AddConnection(Connect("scanner", "aggregator").
		On(pattern.MatchEvent("VulnFound")).Pipe().Send("VulnFound").Build())
	a.AddConnection(Connect("aggregator", "renderer").
		On(pattern.MatchEvent("Finding")).Pipe().Send("Finding").Build())

	// --- Behaviors ---
	// Aggregator: VulnFound → Finding, BUT drops "LOW" severity vulns.
	aggregator.OnEvent("VulnFound", func(ctx BehaviorContext) {
		sev := ctx.ParamFrom("VulnFound", "severity")
		if sev == "LOW" {
			return // Deliberately drop — simulates a processing gap.
		}
		ctx.Emit("Finding", map[string]any{
			"severity": sev,
			"cve":      ctx.ParamFrom("VulnFound", "cve"),
		})
	})

	// Renderer: Finding → DocSection.
	var docCount int
	var docMu sync.Mutex
	allDone := make(chan struct{})
	renderer.OnEvent("Finding", func(ctx BehaviorContext) {
		ctx.Emit("DocSection", map[string]any{
			"title": "Vulnerability Report",
			"cve":   ctx.ParamFrom("Finding", "cve"),
		})
		docMu.Lock()
		docCount++
		if docCount == 2 {
			close(allDone)
		}
		docMu.Unlock()
	})

	// --- Execute ---
	a.Start(context.Background())

	// Three vulns: HIGH and CRITICAL get processed, LOW gets dropped.
	scanner.Emit("VulnFound", map[string]any{
		"severity": "HIGH",
		"cve":      "CVE-2026-0001",
	})
	scanner.Emit("VulnFound", map[string]any{
		"severity": "CRITICAL",
		"cve":      "CVE-2026-0002",
	})
	scanner.Emit("VulnFound", map[string]any{
		"severity": "LOW",
		"cve":      "CVE-2026-0003",
	})

	select {
	case <-allDone:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for pipeline to process vulns")
	}

	// Give router time to finish cascading.
	time.Sleep(100 * time.Millisecond)

	a.Stop()
	a.Wait()

	// --- Verify constraints ---
	violations := a.CheckConstraints()
	report := a.ConstraintReport()
	t.Logf("ATO Constraint Report:\n%s", report)

	// Completeness: the dropped LOW vuln should cause exactly 1 violation.
	var completenessViolations []constraint.ConstraintViolation
	for _, v := range violations {
		if v.Constraint == "completeness" {
			completenessViolations = append(completenessViolations, v)
		}
	}
	if len(completenessViolations) != 1 {
		t.Fatalf("completeness: want 1 violation (dropped LOW), got %d", len(completenessViolations))
	}
	if !strings.Contains(completenessViolations[0].Message, "CVE-2026-0003") {
		t.Errorf("completeness violation should mention CVE-2026-0003: got %s", completenessViolations[0].Message)
	}

	// No orphans: all DocSections have VulnFound ancestors — no violations.
	for _, v := range violations {
		if v.Constraint == "no_orphans" {
			t.Error("no_orphans should not fire — all DocSections have VulnFound ancestors")
		}
	}

	// Causal depth: should be well within limit.
	for _, v := range violations {
		if v.Constraint == "causal_depth_max" {
			t.Error("causal depth should be within limit")
		}
	}

	// Report is non-empty and actionable.
	if len(report) == 0 {
		t.Error("report should not be empty")
	}
	if !strings.Contains(report, "completeness") {
		t.Error("report should mention the completeness constraint")
	}
	if !strings.Contains(report, "CVE-2026-0003") {
		t.Error("report should identify the dropped vuln by CVE")
	}

	// Callback was called for violations.
	mu.Lock()
	cbCount := len(callbackViolations)
	mu.Unlock()
	if cbCount != len(violations) {
		t.Errorf("callback count (%d) should match violations (%d)", cbCount, len(violations))
	}

	// Verify events in poset are correct.
	vulns := a.Poset().EventsByName("VulnFound")
	docs := a.Poset().EventsByName("DocSection")
	findings := a.Poset().EventsByName("Finding")

	if len(vulns) < 3 {
		t.Errorf("expected at least 3 VulnFound events, got %d", len(vulns))
	}
	aggFindings := findings.Filter(func(e *gorapide.Event) bool {
		return e.Source == "aggregator"
	})
	if len(aggFindings) != 2 {
		t.Errorf("expected 2 aggregator Findings, got %d", len(aggFindings))
	}
	if len(docs) < 2 {
		t.Errorf("expected at least 2 DocSection events, got %d", len(docs))
	}

	// Verify causal chains for properly processed vulns.
	for _, doc := range docs {
		if doc.Source != "renderer" {
			continue
		}
		hasVulnAncestor := false
		for _, anc := range a.Poset().CausalAncestors(doc.ID) {
			if anc.Name == "VulnFound" {
				hasVulnAncestor = true
				break
			}
		}
		if !hasVulnAncestor {
			t.Errorf("DocSection %s should have a VulnFound ancestor", doc.ID.Short())
		}
	}
}

// --- ATO scenario with CheckOnEvent mid-execution detection ---

func TestATOCheckOnEventMidExecution(t *testing.T) {
	cs := constraint.NewConstraintSet("ato_live")
	cs.Add(constraint.EventCount("Error", 0, 0)) // errors must not exist

	var mu sync.Mutex
	var liveViolations []constraint.ConstraintViolation

	a := NewArchitecture("ato_live")
	a.WithConstraintsOpts(cs, constraint.CheckOnEvent, func(ch *constraint.Checker) {
		ch.SetBatchSize(2)
		ch.OnViolation(func(v constraint.ConstraintViolation) {
			mu.Lock()
			liveViolations = append(liveViolations, v)
			mu.Unlock()
		})
	})

	scanner := NewComponent("scanner",
		Interface("Scanner").OutAction("VulnFound").OutAction("Error").Build(), nil)
	a.AddComponent(scanner)

	a.Start(context.Background())

	// Emit some events including an Error.
	scanner.Emit("VulnFound", nil)
	scanner.Emit("Error", map[string]any{"msg": "disk full"})

	time.Sleep(100 * time.Millisecond)

	a.Stop()
	a.Wait()

	mu.Lock()
	count := len(liveViolations)
	mu.Unlock()
	if count == 0 {
		t.Error("CheckOnEvent should have caught Error during execution")
	}
}
