package pattern

import (
	"testing"
	"time"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// Compile-time interface checks for timing patterns.
var (
	_ Pattern = (*duringPattern)(nil)
	_ Pattern = (*withinPattern)(nil)
	_ Pattern = (*afterPattern)(nil)
	_ Pattern = (*beforePattern)(nil)
)

// buildTimedPoset creates a poset with events at specific wall times.
// Returns the poset and the base time used.
func buildTimedPoset(t *testing.T) (*gorapide.Poset, time.Time) {
	t.Helper()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	p := gorapide.NewPoset()
	events := []*gorapide.Event{
		{ID: gorapide.NewEventID(), Name: "A", Source: "src", Params: map[string]any{},
			Clock: gorapide.ClockStamp{WallTime: base}},
		{ID: gorapide.NewEventID(), Name: "B", Source: "src", Params: map[string]any{},
			Clock: gorapide.ClockStamp{WallTime: base.Add(1 * time.Second)}},
		{ID: gorapide.NewEventID(), Name: "C", Source: "src", Params: map[string]any{},
			Clock: gorapide.ClockStamp{WallTime: base.Add(5 * time.Second)}},
		{ID: gorapide.NewEventID(), Name: "D", Source: "src", Params: map[string]any{},
			Clock: gorapide.ClockStamp{WallTime: base.Add(10 * time.Second)}},
	}

	for _, e := range events {
		if err := p.AddEvent(e); err != nil {
			t.Fatalf("AddEvent: %v", err)
		}
	}
	// A -> B -> C -> D
	aID := events[0].ID
	bID := events[1].ID
	cID := events[2].ID
	dID := events[3].ID
	for _, edge := range [][2]gorapide.EventID{{aID, bID}, {bID, cID}, {cID, dID}} {
		if err := p.AddCausal(edge[0], edge[1]); err != nil {
			t.Fatalf("AddCausal: %v", err)
		}
	}
	return p, base
}

// --- During Tests ---

func TestDuringFiltersToWindow(t *testing.T) {
	p, base := buildTimedPoset(t)

	// Window covers A (t+0) and B (t+1) only.
	pat := During(MatchAny(), base, base.Add(2*time.Second))
	results := pat.Match(p)
	if len(results) != 2 {
		t.Fatalf("During: expected 2 events in window, got %d", len(results))
	}
	for _, es := range results {
		name := es[0].Name
		if name != "A" && name != "B" {
			t.Errorf("unexpected event %s in window", name)
		}
	}
}

func TestDuringExcludesOutside(t *testing.T) {
	p, base := buildTimedPoset(t)

	// Window only covers C (t+5).
	pat := During(MatchAny(), base.Add(4*time.Second), base.Add(6*time.Second))
	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("During: expected 1 event, got %d", len(results))
	}
	if results[0][0].Name != "C" {
		t.Errorf("expected C, got %s", results[0][0].Name)
	}
}

func TestDuringNoMatch(t *testing.T) {
	p, base := buildTimedPoset(t)

	// Window is before all events.
	pat := During(MatchAny(), base.Add(-10*time.Second), base.Add(-5*time.Second))
	results := pat.Match(p)
	if len(results) != 0 {
		t.Errorf("During with no events in window should return empty, got %d", len(results))
	}
}

func TestDuringEmptyPoset(t *testing.T) {
	p := gorapide.NewPoset()
	now := time.Now()
	results := During(MatchAny(), now.Add(-time.Hour), now.Add(time.Hour)).Match(p)
	if len(results) != 0 {
		t.Errorf("During on empty poset should return empty, got %d", len(results))
	}
}

// --- Within Tests ---

func TestWithinAcceptsShortSpan(t *testing.T) {
	p, _ := buildTimedPoset(t)

	// Match A and B as a union. Their span is 1 second.
	pat := Within(Union(MatchEvent("A"), MatchEvent("B")), 2*time.Second)
	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("Within(2s): expected 1 match, got %d", len(results))
	}
}

func TestWithinRejectsLongSpan(t *testing.T) {
	p, _ := buildTimedPoset(t)

	// Match A and D as a union. Their span is 10 seconds.
	pat := Within(Union(MatchEvent("A"), MatchEvent("D")), 5*time.Second)
	results := pat.Match(p)
	if len(results) != 0 {
		t.Errorf("Within(5s): should reject 10s span, got %d", len(results))
	}
}

func TestWithinSingleEvent(t *testing.T) {
	p, _ := buildTimedPoset(t)

	// Single event always has span 0.
	pat := Within(MatchEvent("A"), 0)
	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("Within(0) on single event: expected 1 match, got %d", len(results))
	}
}

func TestWithinEmptyPoset(t *testing.T) {
	p := gorapide.NewPoset()
	results := Within(MatchAny(), time.Hour).Match(p)
	if len(results) != 0 {
		t.Errorf("Within on empty poset should return empty, got %d", len(results))
	}
}

// --- After Tests ---

func TestAfterFilters(t *testing.T) {
	p, base := buildTimedPoset(t)

	// After t+3 should include C (t+5) and D (t+10).
	pat := After(MatchAny(), base.Add(3*time.Second))
	results := pat.Match(p)
	if len(results) != 2 {
		t.Fatalf("After(t+3): expected 2 events, got %d", len(results))
	}
	for _, es := range results {
		name := es[0].Name
		if name != "C" && name != "D" {
			t.Errorf("unexpected event %s after t+3", name)
		}
	}
}

func TestAfterExcludesEqual(t *testing.T) {
	p, base := buildTimedPoset(t)

	// After exactly at A's time — strictly after, so A excluded.
	pat := After(MatchEvent("A"), base)
	results := pat.Match(p)
	if len(results) != 0 {
		t.Errorf("After(exact time): should exclude event at exact time, got %d", len(results))
	}
}

func TestAfterEmptyPoset(t *testing.T) {
	p := gorapide.NewPoset()
	results := After(MatchAny(), time.Now().Add(-time.Hour)).Match(p)
	if len(results) != 0 {
		t.Errorf("After on empty poset should return empty, got %d", len(results))
	}
}

// --- Before Tests ---

func TestBeforeFilters(t *testing.T) {
	p, base := buildTimedPoset(t)

	// Before t+3 should include A (t+0) and B (t+1).
	pat := Before(MatchAny(), base.Add(3*time.Second))
	results := pat.Match(p)
	if len(results) != 2 {
		t.Fatalf("Before(t+3): expected 2 events, got %d", len(results))
	}
	for _, es := range results {
		name := es[0].Name
		if name != "A" && name != "B" {
			t.Errorf("unexpected event %s before t+3", name)
		}
	}
}

func TestBeforeExcludesEqual(t *testing.T) {
	p, base := buildTimedPoset(t)

	// Before exactly at D's time — strictly before, so D excluded.
	pat := Before(MatchEvent("D"), base.Add(10*time.Second))
	results := pat.Match(p)
	if len(results) != 0 {
		t.Errorf("Before(exact time): should exclude event at exact time, got %d", len(results))
	}
}

func TestBeforeEmptyPoset(t *testing.T) {
	p := gorapide.NewPoset()
	results := Before(MatchAny(), time.Now().Add(time.Hour)).Match(p)
	if len(results) != 0 {
		t.Errorf("Before on empty poset should return empty, got %d", len(results))
	}
}

// --- Timing Composed with Causal Patterns ---

func TestSeqWithWithin(t *testing.T) {
	p, _ := buildTimedPoset(t)

	// Seq(Match("A"), Within(Match("B"), 5s))
	// B is at t+1, so singleton span is 0 <= 5s.
	// A causally before B.
	pat := Seq(MatchEvent("A"), Within(MatchEvent("B"), 5*time.Second))
	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("Seq(A, Within(B, 5s)): expected 1 match, got %d", len(results))
	}
}

func TestSeqWithAfter(t *testing.T) {
	p, base := buildTimedPoset(t)

	// Seq(Match("A"), After(Match("C"), t+4))
	// C is at t+5 which is after t+4. A causally before C.
	pat := Seq(MatchEvent("A"), After(MatchEvent("C"), base.Add(4*time.Second)))
	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("Seq(A, After(C, t+4)): expected 1 match, got %d", len(results))
	}
}

func TestSeqWithAfterFiltersOut(t *testing.T) {
	p, base := buildTimedPoset(t)

	// Seq(Match("A"), After(Match("B"), t+5))
	// B is at t+1 which is NOT after t+5. Should not match.
	pat := Seq(MatchEvent("A"), After(MatchEvent("B"), base.Add(5*time.Second)))
	results := pat.Match(p)
	if len(results) != 0 {
		t.Errorf("Seq(A, After(B, t+5)): B is at t+1 which is not after t+5, got %d", len(results))
	}
}

func TestDuringComposedWithJoin(t *testing.T) {
	p, base := buildTimedPoset(t)

	// During(MatchAny(), t+0..t+6) finds A, B, C.
	// A is causally before B and C, so B and C share ancestor A.
	// But B and C aren't independent — B->C in our chain.
	// Let's just verify During works inside Join.
	// Join(During(Match(A),..), During(Match(B),...)) — A and B share ancestor lineage.
	window := base.Add(2 * time.Second)
	pat := Join(
		During(MatchEvent("A"), base, window),
		During(MatchEvent("B"), base, window),
	)
	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("Join(During(A), During(B)): expected 1 match, got %d", len(results))
	}
}

// --- String() Tests ---

func TestTimingPatternStrings(t *testing.T) {
	ref := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		pat      Pattern
		contains string
	}{
		{During(MatchEvent("A"), ref, ref.Add(time.Hour)), "During("},
		{Within(MatchEvent("A"), 5 * time.Second), "Within("},
		{After(MatchEvent("A"), ref), "After("},
		{Before(MatchEvent("A"), ref), "Before("},
	}
	for _, tt := range tests {
		s := tt.pat.String()
		if len(s) == 0 {
			t.Error("String() should not be empty")
		}
		if !containsSubstring(s, tt.contains) {
			t.Errorf("String() %q should contain %q", s, tt.contains)
		}
	}
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
