package gorapide

import (
	"strings"
	"testing"
)

// --- DOT output tests ---

func TestDOTContainsGraphStructure(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		Event("D").CausedBy("B", "C").
		MustDone()

	dot := p.DOT()

	if !strings.Contains(dot, "digraph poset {") {
		t.Error("DOT output should start with 'digraph poset {'")
	}
	if !strings.HasSuffix(strings.TrimSpace(dot), "}") {
		t.Error("DOT output should end with '}'")
	}
	if !strings.Contains(dot, "rankdir=TB") {
		t.Error("DOT output should contain rankdir")
	}
}

func TestDOTContainsNodesAndEdges(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		MustDone()

	dot := p.DOT()

	// Node labels use Graphviz \n for line breaks within labels.
	if !strings.Contains(dot, `"A\n`) {
		t.Errorf("DOT output should contain node labeled A, got:\n%s", dot)
	}
	if !strings.Contains(dot, `"B\n`) {
		t.Errorf("DOT output should contain node labeled B, got:\n%s", dot)
	}
	// Should have an edge (->).
	if !strings.Contains(dot, " -> ") {
		t.Errorf("DOT output should contain directed edge (->), got:\n%s", dot)
	}
}

func TestDOTEmptyPoset(t *testing.T) {
	p := NewPoset()
	dot := p.DOT()
	if !strings.Contains(dot, "digraph poset {") {
		t.Error("empty poset DOT should still be valid")
	}
}

func TestDOTIsValidSyntax(t *testing.T) {
	p := Build().
		Source("scanner").
		Event("ScanStart").
		Event("VulnFound", "cve", "CVE-2024-1234").CausedBy("ScanStart").
		Event("ScanComplete").CausedBy("VulnFound").
		MustDone()

	dot := p.DOT()

	// Verify basic DOT structural requirements.
	if strings.Count(dot, "{") != strings.Count(dot, "}") {
		t.Error("DOT has mismatched braces")
	}
	lines := strings.Split(strings.TrimSpace(dot), "\n")
	if len(lines) < 3 {
		t.Errorf("DOT too short, expected at least header + nodes + footer, got %d lines", len(lines))
	}
	// Every edge line should have ->
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "->") {
			if !strings.HasSuffix(line, ";") {
				t.Errorf("DOT edge line should end with semicolon: %q", line)
			}
		}
	}
}

// --- String() tests ---

func TestPosetString(t *testing.T) {
	p := Build().
		Source("scanner").
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		Event("D").CausedBy("B", "C").
		MustDone()

	s := p.String()

	if !strings.Contains(s, "4 events") {
		t.Errorf("String() should show event count, got:\n%s", s)
	}
	if !strings.Contains(s, "4 causal edges") {
		t.Errorf("String() should show edge count, got:\n%s", s)
	}
	if !strings.Contains(s, "Roots:") {
		t.Error("String() should show roots")
	}
	if !strings.Contains(s, "Leaves:") {
		t.Error("String() should show leaves")
	}
	if !strings.Contains(s, "(root)") {
		t.Error("String() should mark root events")
	}
	if !strings.Contains(s, "<-") {
		t.Error("String() should show causal predecessors")
	}
}

// --- Validate tests ---

func TestValidateCleanPoset(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		MustDone()

	errs := p.Validate()
	if len(errs) != 0 {
		t.Errorf("clean poset should have no validation errors, got: %v", errs)
	}
}

func TestValidateCatchesUnfrozenEvent(t *testing.T) {
	p := NewPoset()
	e := NewEvent("Bad", "src", nil)
	// Bypass normal AddEvent to inject unfrozen event.
	p.mu.Lock()
	p.lamportCounter++
	e.Clock.Lamport = p.lamportCounter
	// Deliberately skip e.Freeze()
	p.events[e.ID] = e
	p.causalEdges[e.ID] = make(map[EventID]bool)
	p.reverseCausal[e.ID] = make(map[EventID]bool)
	p.mu.Unlock()

	errs := p.Validate()
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "not frozen") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Validate should catch unfrozen event, got: %v", errs)
	}
}

func TestValidateCatchesLamportInconsistency(t *testing.T) {
	p := NewPoset()
	a := NewEvent("A", "src", nil)
	b := NewEvent("B", "src", nil)
	if err := p.AddEvent(a); err != nil {
		t.Fatal(err)
	}
	if err := p.AddEvent(b); err != nil {
		t.Fatal(err)
	}
	if err := p.AddCausal(a.ID, b.ID); err != nil {
		t.Fatal(err)
	}

	// Corrupt Lamport: make B's timestamp less than A's.
	p.mu.Lock()
	b.Clock.Lamport = 0
	p.mu.Unlock()

	errs := p.Validate()
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "Lamport violation") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Validate should catch Lamport inconsistency, got: %v", errs)
	}
}

func TestValidateCatchesDanglingEdge(t *testing.T) {
	p := NewPoset()
	a := NewEvent("A", "src", nil)
	if err := p.AddEvent(a); err != nil {
		t.Fatal(err)
	}

	// Inject a dangling edge reference.
	p.mu.Lock()
	phantom := EventID("phantom-id-does-not-exist")
	p.causalEdges[a.ID][phantom] = true
	p.mu.Unlock()

	errs := p.Validate()
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "non-existent") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Validate should catch dangling edge, got: %v", errs)
	}
}

// --- Stats tests ---

func TestStatsDiamond(t *testing.T) {
	p := Build().
		Source("scanner").
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		Event("D").CausedBy("B", "C").
		MustDone()

	s := p.Stats()
	if s.EventCount != 4 {
		t.Errorf("EventCount: want 4, got %d", s.EventCount)
	}
	if s.EdgeCount != 4 {
		t.Errorf("EdgeCount: want 4, got %d", s.EdgeCount)
	}
	if s.RootCount != 1 {
		t.Errorf("RootCount: want 1, got %d", s.RootCount)
	}
	if s.LeafCount != 1 {
		t.Errorf("LeafCount: want 1, got %d", s.LeafCount)
	}
	if s.MaxDepth != 3 {
		t.Errorf("MaxDepth: want 3 (A->B->D or A->C->D), got %d", s.MaxDepth)
	}
	// AvgFanOut = 4 edges / 4 events = 1.0
	if s.AvgFanOut != 1.0 {
		t.Errorf("AvgFanOut: want 1.0, got %f", s.AvgFanOut)
	}
	if s.ComponentCount != 1 {
		t.Errorf("ComponentCount: want 1, got %d", s.ComponentCount)
	}
}

func TestStatsMultipleComponents(t *testing.T) {
	p := Build().
		Source("scanner").
		Event("ScanStart").
		Event("VulnFound").CausedBy("ScanStart").
		Source("renderer").
		Event("DocGenerated").CausedBy("VulnFound").
		MustDone()

	s := p.Stats()
	if s.ComponentCount != 2 {
		t.Errorf("ComponentCount: want 2, got %d", s.ComponentCount)
	}
}

func TestStatsLinearChain(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		Event("D").CausedBy("C").
		MustDone()

	s := p.Stats()
	if s.MaxDepth != 4 {
		t.Errorf("MaxDepth: want 4 (A->B->C->D), got %d", s.MaxDepth)
	}
	if s.EdgeCount != 3 {
		t.Errorf("EdgeCount: want 3, got %d", s.EdgeCount)
	}
}

func TestStatsEmptyPoset(t *testing.T) {
	p := NewPoset()
	s := p.Stats()
	if s.EventCount != 0 || s.EdgeCount != 0 || s.MaxDepth != 0 {
		t.Errorf("empty poset stats should be all zeros, got %+v", s)
	}
}

// --- PosetBuilder tests ---

func TestBuilderDiamond(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		Event("D").CausedBy("B", "C").
		MustDone()

	if p.Len() != 4 {
		t.Fatalf("diamond should have 4 events, got %d", p.Len())
	}
	roots := p.Roots()
	if len(roots) != 1 || roots[0].Name != "A" {
		t.Error("diamond root should be A")
	}
	leaves := p.Leaves()
	if len(leaves) != 1 || leaves[0].Name != "D" {
		t.Error("diamond leaf should be D")
	}
	// B and C should be causally independent.
	bEvents := p.EventsByName("B")
	cEvents := p.EventsByName("C")
	if !p.IsCausallyIndependent(bEvents[0].ID, cEvents[0].ID) {
		t.Error("B and C should be causally independent in diamond")
	}
}

func TestBuilderLinearChain(t *testing.T) {
	p := Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		Event("D").CausedBy("C").
		MustDone()

	if p.Len() != 4 {
		t.Fatalf("chain should have 4 events, got %d", p.Len())
	}
	aEvents := p.EventsByName("A")
	dEvents := p.EventsByName("D")
	if !p.IsCausallyBefore(aEvents[0].ID, dEvents[0].ID) {
		t.Error("A should be transitively before D")
	}
}

func TestBuilderWithParams(t *testing.T) {
	p := Build().
		Source("scanner").
		Event("ScanStart").
		Event("VulnFound", "cve", "CVE-2024-1234", "severity", "HIGH").CausedBy("ScanStart").
		Event("ScanComplete").CausedBy("VulnFound").
		Source("renderer").
		Event("DocGenerated", "section", "POAM").CausedBy("VulnFound").
		MustDone()

	if p.Len() != 4 {
		t.Fatalf("expected 4 events, got %d", p.Len())
	}

	vulns := p.EventsByName("VulnFound")
	if len(vulns) != 1 {
		t.Fatal("expected 1 VulnFound event")
	}
	if vulns[0].ParamString("cve") != "CVE-2024-1234" {
		t.Errorf("expected cve=CVE-2024-1234, got %q", vulns[0].ParamString("cve"))
	}
	if vulns[0].ParamString("severity") != "HIGH" {
		t.Errorf("expected severity=HIGH, got %q", vulns[0].ParamString("severity"))
	}
	if vulns[0].Source != "scanner" {
		t.Errorf("VulnFound source should be scanner, got %q", vulns[0].Source)
	}

	docs := p.EventsByName("DocGenerated")
	if len(docs) != 1 {
		t.Fatal("expected 1 DocGenerated event")
	}
	if docs[0].Source != "renderer" {
		t.Errorf("DocGenerated source should be renderer, got %q", docs[0].Source)
	}
	if docs[0].ParamString("section") != "POAM" {
		t.Errorf("expected section=POAM, got %q", docs[0].ParamString("section"))
	}
}

func TestBuilderCausedByLinksCorrectly(t *testing.T) {
	p := Build().
		Event("Root").
		Event("Child1").CausedBy("Root").
		Event("Child2").CausedBy("Root").
		Event("Grandchild").CausedBy("Child1", "Child2").
		MustDone()

	gc := p.EventsByName("Grandchild")[0]
	causes := p.DirectCauses(gc.ID)
	if len(causes) != 2 {
		t.Fatalf("Grandchild should have 2 direct causes, got %d", len(causes))
	}
	names := causes.Names()
	hasChild1, hasChild2 := false, false
	for _, n := range names {
		if n == "Child1" {
			hasChild1 = true
		}
		if n == "Child2" {
			hasChild2 = true
		}
	}
	if !hasChild1 || !hasChild2 {
		t.Errorf("Grandchild causes should be Child1 and Child2, got %v", names)
	}
}

func TestBuilderCausedByUsesLatestEvent(t *testing.T) {
	// When two events share a name, CausedBy should use the most recent one.
	p := Build().
		Event("Step").
		Event("Step").
		Event("Final").CausedBy("Step").
		MustDone()

	steps := p.EventsByName("Step")
	final := p.EventsByName("Final")[0]
	causes := p.DirectCauses(final.ID)
	if len(causes) != 1 {
		t.Fatalf("Final should have 1 direct cause, got %d", len(causes))
	}
	// It should be the second Step (most recent).
	if causes[0].ID == steps[0].ID && steps[0].Clock.Lamport < steps[1].Clock.Lamport {
		// causes[0] is the first step, which has the lower lamport — wrong one.
		t.Error("CausedBy should use the most recently added event when names collide")
	}
}

func TestBuilderMustDonePanicsOnError(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MustDone should panic on invalid input")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", r, r)
		}
		if !strings.Contains(msg, "NonExistent") {
			t.Errorf("panic message should reference the missing event, got: %s", msg)
		}
	}()

	Build().
		Event("A").
		Event("B").CausedBy("NonExistent").
		MustDone()
}

func TestBuilderDoneReturnsError(t *testing.T) {
	_, err := Build().
		Event("A").
		Event("B").CausedBy("NonExistent").
		Done()

	if err == nil {
		t.Fatal("Done should return error for non-existent cause")
	}
	if !strings.Contains(err.Error(), "NonExistent") {
		t.Errorf("error should mention the missing event name, got: %v", err)
	}
}

func TestBuilderOddParams(t *testing.T) {
	_, err := Build().
		Event("Bad", "key_without_value").
		Done()

	if err == nil {
		t.Fatal("odd number of params should produce an error")
	}
}

func TestBuilderNonStringParamKey(t *testing.T) {
	_, err := Build().
		Event("Bad", 42, "value").
		Done()

	if err == nil {
		t.Fatal("non-string param key should produce an error")
	}
}

func TestBuilderCausedByBeforeEvent(t *testing.T) {
	_, err := Build().
		CausedBy("A").
		Done()

	if err == nil {
		t.Fatal("CausedBy before any Event should produce an error")
	}
}

func TestBuilderSourcePersists(t *testing.T) {
	p := Build().
		Source("alpha").
		Event("E1").
		Event("E2").
		Source("beta").
		Event("E3").
		MustDone()

	e1 := p.EventsByName("E1")[0]
	e2 := p.EventsByName("E2")[0]
	e3 := p.EventsByName("E3")[0]

	if e1.Source != "alpha" {
		t.Errorf("E1 source should be alpha, got %q", e1.Source)
	}
	if e2.Source != "alpha" {
		t.Errorf("E2 source should be alpha, got %q", e2.Source)
	}
	if e3.Source != "beta" {
		t.Errorf("E3 source should be beta, got %q", e3.Source)
	}
}

// --- Validate on builder-built posets ---

func TestValidateBuilderPoset(t *testing.T) {
	p := Build().
		Source("scanner").
		Event("ScanStart").
		Event("VulnFound", "cve", "CVE-2024-1234", "severity", "HIGH").CausedBy("ScanStart").
		Event("ScanComplete").CausedBy("VulnFound").
		Source("renderer").
		Event("DocGenerated", "section", "POAM").CausedBy("VulnFound").
		MustDone()

	errs := p.Validate()
	if len(errs) != 0 {
		t.Errorf("builder-constructed poset should validate cleanly, got: %v", errs)
	}
}
