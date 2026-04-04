package pattern

import (
	"testing"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// Compile-time check that BasicPattern implements Pattern.
var _ Pattern = (*BasicPattern)(nil)

// testPoset builds a realistic poset for pattern matching tests.
//
//	scanner: ScanStart -> VulnFound(cve=CVE-2024-1234, severity=HIGH)
//	                   -> VulnFound(cve=CVE-2024-5678, severity=LOW)
//	                   -> ScanComplete
//	renderer: DocGenerated(section=POAM) <- VulnFound(HIGH)
func testPoset() *gorapide.Poset {
	return gorapide.Build().
		Source("scanner").
		Event("ScanStart").
		Event("VulnFound", "cve", "CVE-2024-1234", "severity", "HIGH").CausedBy("ScanStart").
		Event("VulnFound", "cve", "CVE-2024-5678", "severity", "LOW").CausedBy("ScanStart").
		Event("ScanComplete").CausedBy("ScanStart").
		Source("renderer").
		Event("DocGenerated", "section", "POAM").CausedBy("ScanComplete").
		MustDone()
}

func TestMatchByName(t *testing.T) {
	p := testPoset()
	pat := MatchEvent("VulnFound")

	results := pat.Match(p)
	if len(results) != 2 {
		t.Fatalf("expected 2 VulnFound matches, got %d", len(results))
	}
	for _, es := range results {
		if len(es) != 1 {
			t.Error("each match should be a singleton EventSet")
		}
		if es[0].Name != "VulnFound" {
			t.Errorf("expected VulnFound, got %s", es[0].Name)
		}
	}
}

func TestMatchByNameSingle(t *testing.T) {
	p := testPoset()
	results := MatchEvent("ScanStart").Match(p)
	if len(results) != 1 {
		t.Fatalf("expected 1 ScanStart match, got %d", len(results))
	}
	if results[0][0].Name != "ScanStart" {
		t.Errorf("expected ScanStart, got %s", results[0][0].Name)
	}
}

func TestMatchAny(t *testing.T) {
	p := testPoset()
	results := MatchAny().Match(p)
	if len(results) != 5 {
		t.Errorf("expected 5 matches (all events), got %d", len(results))
	}
	for _, es := range results {
		if len(es) != 1 {
			t.Error("each match should be a singleton EventSet")
		}
	}
}

func TestWhereCustomPredicate(t *testing.T) {
	p := testPoset()
	pat := MatchEvent("VulnFound").Where(func(e *gorapide.Event) bool {
		return e.ParamString("severity") == "HIGH"
	})

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("expected 1 HIGH VulnFound, got %d", len(results))
	}
	if results[0][0].ParamString("cve") != "CVE-2024-1234" {
		t.Errorf("expected CVE-2024-1234, got %s", results[0][0].ParamString("cve"))
	}
}

func TestWhereParam(t *testing.T) {
	p := testPoset()
	pat := MatchEvent("VulnFound").WhereParam("severity", "LOW")

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("expected 1 LOW VulnFound, got %d", len(results))
	}
	if results[0][0].ParamString("cve") != "CVE-2024-5678" {
		t.Errorf("expected CVE-2024-5678, got %s", results[0][0].ParamString("cve"))
	}
}

func TestWhereParamNoMatch(t *testing.T) {
	p := testPoset()
	pat := MatchEvent("VulnFound").WhereParam("severity", "CRITICAL")

	results := pat.Match(p)
	if len(results) != 0 {
		t.Errorf("expected 0 matches, got %d", len(results))
	}
}

func TestWhereSource(t *testing.T) {
	p := testPoset()
	pat := MatchAny().WhereSource("renderer")

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("expected 1 renderer event, got %d", len(results))
	}
	if results[0][0].Name != "DocGenerated" {
		t.Errorf("expected DocGenerated, got %s", results[0][0].Name)
	}
}

func TestWhereSourceMultiple(t *testing.T) {
	p := testPoset()
	pat := MatchAny().WhereSource("scanner")

	results := pat.Match(p)
	if len(results) != 4 {
		t.Errorf("expected 4 scanner events, got %d", len(results))
	}
}

func TestChainedWhereAndWhereParam(t *testing.T) {
	p := testPoset()
	pat := MatchEvent("VulnFound").
		WhereParam("severity", "HIGH").
		Where(func(e *gorapide.Event) bool {
			return e.Source == "scanner"
		})

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("expected 1 match with chained filters, got %d", len(results))
	}
	if results[0][0].ParamString("cve") != "CVE-2024-1234" {
		t.Error("chained filter should match HIGH severity from scanner")
	}
}

func TestChainedWhereSourceAndWhereParam(t *testing.T) {
	p := testPoset()
	pat := MatchAny().
		WhereSource("scanner").
		WhereParam("severity", "LOW")

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("expected 1 match, got %d", len(results))
	}
	if results[0][0].ParamString("cve") != "CVE-2024-5678" {
		t.Error("should match LOW severity from scanner")
	}
}

func TestNoMatchReturnsEmptySlice(t *testing.T) {
	p := testPoset()
	pat := MatchEvent("NonExistentEvent")

	results := pat.Match(p)
	if results == nil {
		t.Error("no matches should return empty slice, not nil")
	}
	if len(results) != 0 {
		t.Errorf("expected 0 matches, got %d", len(results))
	}
}

func TestEmptyPosetReturnsEmpty(t *testing.T) {
	p := gorapide.NewPoset()

	results := MatchAny().Match(p)
	if results == nil {
		t.Error("empty poset should return empty slice, not nil")
	}
	if len(results) != 0 {
		t.Errorf("expected 0 matches for empty poset, got %d", len(results))
	}

	results = MatchEvent("Anything").Match(p)
	if len(results) != 0 {
		t.Errorf("expected 0 matches for empty poset, got %d", len(results))
	}
}

func TestPatternString(t *testing.T) {
	tests := []struct {
		pat  Pattern
		want string
	}{
		{MatchEvent("VulnFound"), `Match("VulnFound")`},
		{MatchAny(), "MatchAny()"},
		{MatchEvent("X").WhereSource("s"), `Match("X").WhereSource("s")`},
		{MatchAny().WhereParam("k", "v"), `MatchAny().WhereParam("k", v)`},
		{MatchEvent("E").Where(func(*gorapide.Event) bool { return true }), `Match("E").Where(<fn>)`},
	}
	for _, tt := range tests {
		got := tt.pat.String()
		if got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestPlaceholderVar(t *testing.T) {
	v := Var("X")
	if v.Name() != "X" {
		t.Errorf("Name: want X, got %s", v.Name())
	}
	if v.String() != "?X" {
		t.Errorf("String: want ?X, got %s", v.String())
	}
	if v.Type() != "" {
		t.Errorf("Type should be empty, got %s", v.Type())
	}
}

func TestPlaceholderWithType(t *testing.T) {
	v := Var("Severity").WithType("string")
	if v.String() != "?Severity:string" {
		t.Errorf("String: want ?Severity:string, got %s", v.String())
	}
	if v.Type() != "string" {
		t.Errorf("Type: want string, got %s", v.Type())
	}
}

func TestMatchEventStringChained(t *testing.T) {
	pat := MatchEvent("E").
		WhereParam("a", 1).
		WhereSource("src").
		Where(func(*gorapide.Event) bool { return true })

	s := pat.String()
	if s != `Match("E").WhereParam("a", 1).WhereSource("src").Where(<fn>)` {
		t.Errorf("chained String() = %q", s)
	}
}
