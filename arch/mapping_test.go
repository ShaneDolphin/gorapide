package arch

import (
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

// --- MapBuilder Tests ---

func TestNewMapBuilder(t *testing.T) {
	src := Interface("Source").OutAction("X").Build()
	tgt := Interface("Target").InAction("Y").Build()

	m := NewMap("myMap").
		From(src).
		To(tgt).
		Translate("X", "Y").
		Build()

	if m.Name != "myMap" {
		t.Errorf("Name: want myMap, got %s", m.Name)
	}
	if m.SourceInterface != src {
		t.Error("SourceInterface should match")
	}
	if m.TargetInterface != tgt {
		t.Error("TargetInterface should match")
	}
	if len(m.Translations) != 1 {
		t.Fatalf("Translations: want 1, got %d", len(m.Translations))
	}
	tr := m.Translations[0]
	if tr.SourceAction != "X" {
		t.Errorf("SourceAction: want X, got %s", tr.SourceAction)
	}
	if tr.TargetAction != "Y" {
		t.Errorf("TargetAction: want Y, got %s", tr.TargetAction)
	}
	if tr.Transform != nil {
		t.Error("Transform should be nil for simple Translate")
	}
	if tr.Guard != nil {
		t.Error("Guard should be nil for simple Translate")
	}
}

func TestMapTranslateWithTransform(t *testing.T) {
	xform := func(e *gorapide.Event) map[string]any {
		return map[string]any{"doubled": e.ParamInt("val") * 2}
	}

	m := NewMap("xformMap").
		TranslateWith("A", "B", xform).
		Build()

	if len(m.Translations) != 1 {
		t.Fatalf("Translations: want 1, got %d", len(m.Translations))
	}
	if m.Translations[0].Transform == nil {
		t.Error("Transform should be set")
	}
}

func TestMapTranslateGuarded(t *testing.T) {
	guard := func(e *gorapide.Event) bool {
		return e.ParamString("level") == "HIGH"
	}

	m := NewMap("guardedMap").
		TranslateGuarded("A", "B", guard, nil).
		Build()

	if len(m.Translations) != 1 {
		t.Fatalf("Translations: want 1, got %d", len(m.Translations))
	}
	if m.Translations[0].Guard == nil {
		t.Error("Guard should be set")
	}
}

// --- MapEvent Tests ---

func TestMapEventSimpleTranslation(t *testing.T) {
	m := NewMap("simple").
		Translate("X", "Y").
		Build()

	src := gorapide.NewEvent("X", "test", map[string]any{"key": "val"})
	results, err := m.MapEvent(src)
	if err != nil {
		t.Fatalf("MapEvent: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results: want 1, got %d", len(results))
	}
	r := results[0]
	if r.Name != "Y" {
		t.Errorf("Name: want Y, got %s", r.Name)
	}
	if r.ParamString("key") != "val" {
		t.Errorf("Param key: want val, got %s", r.ParamString("key"))
	}
	// Ensure the params are a copy, not the same map.
	src.Params["key"] = "changed"
	if r.ParamString("key") == "changed" {
		t.Error("params should be a deep copy, not shared")
	}
}

func TestMapEventNoMatch(t *testing.T) {
	m := NewMap("noMatch").
		Translate("X", "Y").
		Build()

	src := gorapide.NewEvent("Z", "test", nil)
	results, err := m.MapEvent(src)
	if err != nil {
		t.Fatalf("MapEvent: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results: want 0, got %d", len(results))
	}
}

func TestMapEventWithGuard(t *testing.T) {
	guard := func(e *gorapide.Event) bool {
		return e.ParamString("level") == "HIGH"
	}

	m := NewMap("guarded").
		TranslateGuarded("Alert", "Alarm", guard, nil).
		Build()

	// HIGH passes the guard.
	high := gorapide.NewEvent("Alert", "test", map[string]any{"level": "HIGH"})
	results, err := m.MapEvent(high)
	if err != nil {
		t.Fatalf("MapEvent HIGH: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("HIGH results: want 1, got %d", len(results))
	}
	if results[0].Name != "Alarm" {
		t.Errorf("Name: want Alarm, got %s", results[0].Name)
	}

	// LOW does not pass the guard.
	low := gorapide.NewEvent("Alert", "test", map[string]any{"level": "LOW"})
	results, err = m.MapEvent(low)
	if err != nil {
		t.Fatalf("MapEvent LOW: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("LOW results: want 0, got %d", len(results))
	}
}

func TestMapEventWithTransform(t *testing.T) {
	xform := func(e *gorapide.Event) map[string]any {
		return map[string]any{"doubled": e.ParamInt("val") * 2}
	}

	m := NewMap("xform").
		TranslateWith("Compute", "Result", xform).
		Build()

	src := gorapide.NewEvent("Compute", "test", map[string]any{"val": 21})
	results, err := m.MapEvent(src)
	if err != nil {
		t.Fatalf("MapEvent: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results: want 1, got %d", len(results))
	}
	if results[0].ParamInt("doubled") != 42 {
		t.Errorf("doubled: want 42, got %d", results[0].ParamInt("doubled"))
	}
}

func TestMapEventMultipleTranslations(t *testing.T) {
	m := NewMap("multi").
		Translate("X", "A").
		Translate("X", "B").
		Build()

	src := gorapide.NewEvent("X", "test", map[string]any{"k": "v"})
	results, err := m.MapEvent(src)
	if err != nil {
		t.Fatalf("MapEvent: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results: want 2, got %d", len(results))
	}
	names := map[string]bool{}
	for _, r := range results {
		names[r.Name] = true
	}
	if !names["A"] || !names["B"] {
		t.Errorf("expected events A and B, got %v", names)
	}
}

// --- Validate Tests ---

func TestMapValidateSuccess(t *testing.T) {
	src := Interface("Src").OutAction("X").Build()
	tgt := Interface("Tgt").InAction("Y").Build()

	m := NewMap("valid").
		From(src).
		To(tgt).
		Translate("X", "Y").
		Build()

	if err := m.Validate(); err != nil {
		t.Errorf("Validate should pass, got: %v", err)
	}
}

func TestMapValidateBadSource(t *testing.T) {
	src := Interface("Src").OutAction("X").Build()
	tgt := Interface("Tgt").InAction("Y").Build()

	m := NewMap("badSrc").
		From(src).
		To(tgt).
		Translate("Missing", "Y").
		Build()

	err := m.Validate()
	if err == nil {
		t.Fatal("Validate should fail for missing source action")
	}
	if !strings.Contains(err.Error(), "Missing") {
		t.Errorf("error should mention 'Missing', got: %v", err)
	}
}

func TestMapValidateBadTarget(t *testing.T) {
	src := Interface("Src").OutAction("X").Build()
	tgt := Interface("Tgt").InAction("Y").Build()

	m := NewMap("badTgt").
		From(src).
		To(tgt).
		Translate("X", "Missing").
		Build()

	err := m.Validate()
	if err == nil {
		t.Fatal("Validate should fail for missing target action")
	}
	if !strings.Contains(err.Error(), "Missing") {
		t.Errorf("error should mention 'Missing', got: %v", err)
	}
}

func TestMapValidateNilInterfaces(t *testing.T) {
	m := NewMap("noIfaces").
		Translate("X", "Y").
		Build()

	// With nil interfaces, Validate should skip checks and pass.
	if err := m.Validate(); err != nil {
		t.Errorf("Validate with nil interfaces should pass, got: %v", err)
	}
}

func TestMapValidateServiceActions(t *testing.T) {
	src := Interface("Src").OutAction("X").Build()
	tgt := Interface("Tgt").
		Service("Svc", func(s *ServiceBuilder) {
			s.InAction("SvcAction")
		}).
		Build()

	m := NewMap("svcTarget").
		From(src).
		To(tgt).
		Translate("X", "SvcAction").
		Build()

	if err := m.Validate(); err != nil {
		t.Errorf("Validate should pass for service action target, got: %v", err)
	}
}

// --- String Test ---

func TestMapString(t *testing.T) {
	m := NewMap("testMap").
		Translate("X", "Y").
		Build()

	s := m.String()
	if len(s) == 0 {
		t.Error("String() should not be empty")
	}
	if !strings.Contains(s, "testMap") {
		t.Errorf("String() should contain map name, got: %s", s)
	}
}
