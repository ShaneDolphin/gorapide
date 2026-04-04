package pattern

import (
	"strings"
	"sync"
	"testing"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

func TestNewMacroRegistryHasBuiltins(t *testing.T) {
	r := NewMacroRegistry()
	for _, name := range []string{"causal_chain", "fan_in", "fan_out"} {
		if _, ok := r.Get(name); !ok {
			t.Errorf("built-in macro %q should be registered", name)
		}
	}
}

func TestMacroRegistryRegisterAndGet(t *testing.T) {
	r := NewMacroRegistry()
	r.Register("custom", "a custom macro", func(args ...any) Pattern {
		return MatchEvent(args[0].(string))
	})

	m, ok := r.Get("custom")
	if !ok {
		t.Fatal("custom macro should be found")
	}
	if m.Name != "custom" {
		t.Errorf("Name: want custom, got %s", m.Name)
	}
	if m.Desc != "a custom macro" {
		t.Errorf("Desc: want 'a custom macro', got %s", m.Desc)
	}
}

func TestMacroRegistryGetMissing(t *testing.T) {
	r := NewMacroRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("nonexistent macro should not be found")
	}
}

func TestMacroRegistryApply(t *testing.T) {
	r := NewMacroRegistry()
	r.Register("echo", "returns Match(name)", func(args ...any) Pattern {
		return MatchEvent(args[0].(string))
	})

	pat, err := r.Apply("echo", "TestEvent")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if pat.String() != `Match("TestEvent")` {
		t.Errorf("unexpected pattern: %s", pat.String())
	}
}

func TestMacroRegistryApplyMissing(t *testing.T) {
	r := NewMacroRegistry()
	_, err := r.Apply("nonexistent")
	if err == nil {
		t.Error("Apply for missing macro should return error")
	}
}

func TestMacroRegistryNames(t *testing.T) {
	r := NewMacroRegistry()
	names := r.Names()
	if len(names) < 3 {
		t.Errorf("expected at least 3 built-in macros, got %d", len(names))
	}
}

// --- Built-in Macro Tests ---

func TestCausalChainMacro(t *testing.T) {
	r := NewMacroRegistry()
	pat, err := r.Apply("causal_chain", "A", "B", "C")
	if err != nil {
		t.Fatal(err)
	}

	// Should produce Seq(Seq(Match("A"), Match("B")), Match("C"))
	s := pat.String()
	if !strings.Contains(s, "Seq") {
		t.Errorf("causal_chain should produce Seq pattern, got %s", s)
	}

	// Test against an actual poset.
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		MustDone()

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("causal_chain(A,B,C): expected 1 match, got %d", len(results))
	}
	if len(results[0]) != 3 {
		t.Errorf("match should have 3 events, got %d", len(results[0]))
	}
}

func TestCausalChainMacroRejectsReverse(t *testing.T) {
	r := NewMacroRegistry()
	pat, err := r.Apply("causal_chain", "C", "B", "A")
	if err != nil {
		t.Fatal(err)
	}

	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		MustDone()

	results := pat.Match(p)
	if len(results) != 0 {
		t.Errorf("causal_chain(C,B,A) should not match forward chain, got %d", len(results))
	}
}

func TestFanInMacro(t *testing.T) {
	r := NewMacroRegistry()
	// fan_in: target="D", sources="B","C"
	pat, err := r.Apply("fan_in", "D", "B", "C")
	if err != nil {
		t.Fatal(err)
	}

	// Root -> B, Root -> C, B -> D, C -> D
	p := gorapide.Build().
		Event("Root").
		Event("B").CausedBy("Root").
		Event("C").CausedBy("Root").
		Event("D").CausedBy("B", "C").
		MustDone()

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("fan_in(D, B, C): expected 1 match, got %d", len(results))
	}
}

func TestFanOutMacro(t *testing.T) {
	r := NewMacroRegistry()
	// fan_out: source="A", targets="B","C"
	pat, err := r.Apply("fan_out", "A", "B", "C")
	if err != nil {
		t.Fatal(err)
	}

	// A -> B, A -> C (B and C are independent)
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("A").
		MustDone()

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("fan_out(A, B, C): expected 1 match, got %d", len(results))
	}
}

func TestFanOutMacroRejectsDependent(t *testing.T) {
	r := NewMacroRegistry()
	pat, err := r.Apply("fan_out", "A", "B", "C")
	if err != nil {
		t.Fatal(err)
	}

	// A -> B -> C (B and C are NOT independent)
	p := gorapide.Build().
		Event("A").
		Event("B").CausedBy("A").
		Event("C").CausedBy("B").
		MustDone()

	results := pat.Match(p)
	if len(results) != 0 {
		t.Errorf("fan_out should reject dependent targets, got %d", len(results))
	}
}

// --- Concurrent safety ---

func TestMacroRegistryConcurrent(t *testing.T) {
	r := NewMacroRegistry()
	var wg sync.WaitGroup
	const goroutines = 50

	// Concurrent reads.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Get("causal_chain")
			r.Names()
		}()
	}

	// Concurrent writes.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := strings.Repeat("x", idx+1)
			r.Register(name, "test", func(args ...any) Pattern {
				return MatchEvent("X")
			})
		}(i)
	}

	// Concurrent applies.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Apply("causal_chain", "A", "B")
		}()
	}

	wg.Wait()
}

// --- Custom macro test ---

func TestCustomMacroApplied(t *testing.T) {
	r := NewMacroRegistry()
	r.Register("severity_scan", "Match VulnFound with given severity", func(args ...any) Pattern {
		severity := args[0].(string)
		return Seq(
			MatchEvent("ScanStart"),
			MatchEvent("VulnFound").WhereParam("severity", severity),
		)
	})

	pat, err := r.Apply("severity_scan", "HIGH")
	if err != nil {
		t.Fatal(err)
	}

	p := gorapide.Build().
		Event("ScanStart").
		Event("VulnFound", "severity", "HIGH").CausedBy("ScanStart").
		Event("VulnFound", "severity", "LOW").CausedBy("ScanStart").
		MustDone()

	results := pat.Match(p)
	if len(results) != 1 {
		t.Fatalf("severity_scan(HIGH): expected 1 match, got %d", len(results))
	}
}
