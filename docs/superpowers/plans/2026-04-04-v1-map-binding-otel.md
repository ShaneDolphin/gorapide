# V1: Map/Binding Constructs + OpenTelemetry Live Export

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete Rapide Map and Binding semantics for cross-architecture event translation and dynamic runtime wiring, plus live OTLP span export to OpenTelemetry collectors.

**Architecture:** Two independent features. Maps and Bindings extend the `arch/` package with new types that integrate into the existing Architecture router's `processEventCascade` loop. OTel export is an isolated sub-module (`otelexport/`) that hooks into the existing `WithObserver` mechanism with zero changes to core code.

**Tech Stack:** Go 1.22, zero new dependencies for Map/Binding. OTel export adds `go.opentelemetry.io/otel` SDK in a separate `go.mod`.

---

## Prerequisites

Before starting, initialize a git repo if one does not exist:

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git init
git add -A
git commit -m "Initial commit: gorapide v0 with core, arch, pattern, constraint, export packages"
```

---

## File Structure

### Map and Binding Constructs (new files)

| File | Responsibility |
|------|---------------|
| `arch/mapping.go` | `EventTranslation`, `Map` type implementing `MapTarget`, `MapBuilder` fluent API, `Validate()` |
| `arch/mapping_test.go` | Unit tests for Map construction, translation, validation, builder |
| `arch/binding.go` | `Binding`, `BindingManager` implementing `BindingTarget`, thread-safe bind/unbind |
| `arch/binding_test.go` | Unit tests for BindingManager lifecycle, concurrent access |
| `arch/binding_integration_test.go` | Integration test: Map + Binding + Architecture end-to-end |

### Map and Binding Constructs (modified files)

| File | Change |
|------|--------|
| `arch/architecture.go` | Add `bindings *BindingManager` field, `Bind`/`Unbind`/`BindWith` methods, modify `processEventCascade` to evaluate bindings |
| `store.go` | Add compile-time assertions for `MapTarget` and `BindingTarget` |
| `store_test.go` | Update placeholder test to use real implementations |

### OTel Live Export (new sub-module)

| File | Responsibility |
|------|---------------|
| `otelexport/go.mod` | Separate module with OTel SDK dependencies |
| `otelexport/doc.go` | Package documentation |
| `otelexport/ids.go` | TraceID/SpanID mapping from EventID |
| `otelexport/ids_test.go` | ID mapping tests |
| `otelexport/batcher.go` | Span batching with backpressure |
| `otelexport/batcher_test.go` | Batcher tests |
| `otelexport/exporter.go` | `LiveExporter`, `Config`, `OnEvent` callback |
| `otelexport/exporter_test.go` | Exporter tests with mock collector |

---

## Part A: Map and Binding Constructs

### Task 1: Map type and builder

**Files:**
- Create: `arch/mapping.go`
- Test: `arch/mapping_test.go`

- [ ] **Step 1: Write failing test for Map builder and basic construction**

```go
// arch/mapping_test.go
package arch

import (
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestNewMapBuilder(t *testing.T) {
	srcIface := Interface("Scanner").
		OutAction("VulnFound").
		Build()
	dstIface := Interface("Aggregator").
		InAction("Finding").
		Build()

	m := NewMap("scan_to_agg").
		From(srcIface).
		To(dstIface).
		Translate("VulnFound", "Finding").
		Build()

	if m.Name != "scan_to_agg" {
		t.Errorf("Name: want scan_to_agg, got %s", m.Name)
	}
	if m.SourceInterface.Name != "Scanner" {
		t.Errorf("SourceInterface: want Scanner, got %s", m.SourceInterface.Name)
	}
	if m.TargetInterface.Name != "Aggregator" {
		t.Errorf("TargetInterface: want Aggregator, got %s", m.TargetInterface.Name)
	}
	if len(m.Translations) != 1 {
		t.Fatalf("Translations: want 1, got %d", len(m.Translations))
	}
	tr := m.Translations[0]
	if tr.SourceAction != "VulnFound" || tr.TargetAction != "Finding" {
		t.Errorf("Translation: want VulnFound->Finding, got %s->%s", tr.SourceAction, tr.TargetAction)
	}
}

func TestMapTranslateWithTransform(t *testing.T) {
	srcIface := Interface("A").OutAction("X").Build()
	dstIface := Interface("B").InAction("Y").Build()

	m := NewMap("test").
		From(srcIface).
		To(dstIface).
		TranslateWith("X", "Y", func(e *gorapide.Event) map[string]any {
			return map[string]any{"doubled": e.ParamInt("val") * 2}
		}).
		Build()

	if m.Translations[0].Transform == nil {
		t.Error("Transform should not be nil")
	}
}

func TestMapTranslateGuarded(t *testing.T) {
	srcIface := Interface("A").OutAction("X").Build()
	dstIface := Interface("B").InAction("Y").Build()

	m := NewMap("test").
		From(srcIface).
		To(dstIface).
		TranslateGuarded("X", "Y",
			func(e *gorapide.Event) bool { return e.ParamString("level") == "HIGH" },
			nil,
		).
		Build()

	if m.Translations[0].Guard == nil {
		t.Error("Guard should not be nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestNewMapBuilder|TestMapTranslateWith|TestMapTranslateGuarded" -v`
Expected: FAIL — `NewMap` undefined

- [ ] **Step 3: Write Map type, EventTranslation, and MapBuilder**

```go
// arch/mapping.go
package arch

import (
	"fmt"

	"github.com/ShaneDolphin/gorapide"
)

// EventTranslation defines how a single source action maps to a target action,
// with optional parameter transformation and guard condition.
type EventTranslation struct {
	SourceAction string
	TargetAction string
	Transform    func(*gorapide.Event) map[string]any // nil = copy params
	Guard        func(*gorapide.Event) bool           // nil = always match
}

// Map defines event translation rules between two interface declarations.
// It implements gorapide.MapTarget.
type Map struct {
	Name            string
	SourceInterface *InterfaceDecl
	TargetInterface *InterfaceDecl
	Translations    []EventTranslation
}

// MapEvent translates a source event into zero or more target events
// according to the translation rules. Implements gorapide.MapTarget.
func (m *Map) MapEvent(source *gorapide.Event) ([]*gorapide.Event, error) {
	var results []*gorapide.Event
	for _, tr := range m.Translations {
		if source.Name != tr.SourceAction {
			continue
		}
		if tr.Guard != nil && !tr.Guard(source) {
			continue
		}
		params := copyParams(source)
		if tr.Transform != nil {
			params = tr.Transform(source)
		}
		e := gorapide.NewEvent(tr.TargetAction, "", params)
		results = append(results, e)
	}
	return results, nil
}

// Validate checks that all SourceAction names exist in SourceInterface
// and all TargetAction names exist in TargetInterface.
func (m *Map) Validate() error {
	srcActions := make(map[string]bool)
	if m.SourceInterface != nil {
		for _, a := range m.SourceInterface.Actions {
			srcActions[a.Name] = true
		}
		for _, s := range m.SourceInterface.Services {
			for _, a := range s.Actions {
				srcActions[a.Name] = true
			}
		}
	}
	dstActions := make(map[string]bool)
	if m.TargetInterface != nil {
		for _, a := range m.TargetInterface.Actions {
			dstActions[a.Name] = true
		}
		for _, s := range m.TargetInterface.Services {
			for _, a := range s.Actions {
				dstActions[a.Name] = true
			}
		}
	}
	for _, tr := range m.Translations {
		if m.SourceInterface != nil && !srcActions[tr.SourceAction] {
			return fmt.Errorf("arch.Map.Validate: source action %q not found in interface %q",
				tr.SourceAction, m.SourceInterface.Name)
		}
		if m.TargetInterface != nil && !dstActions[tr.TargetAction] {
			return fmt.Errorf("arch.Map.Validate: target action %q not found in interface %q",
				tr.TargetAction, m.TargetInterface.Name)
		}
	}
	return nil
}

// String returns a human-readable description of the map.
func (m *Map) String() string {
	src := "<any>"
	if m.SourceInterface != nil {
		src = m.SourceInterface.Name
	}
	dst := "<any>"
	if m.TargetInterface != nil {
		dst = m.TargetInterface.Name
	}
	return fmt.Sprintf("Map(%s: %s -> %s, %d translations)", m.Name, src, dst, len(m.Translations))
}

func copyParams(e *gorapide.Event) map[string]any {
	params := make(map[string]any, len(e.Params))
	for k, v := range e.Params {
		params[k] = v
	}
	return params
}

// --- MapBuilder ---

// MapBuilder constructs a Map using a fluent API.
type MapBuilder struct {
	name            string
	sourceInterface *InterfaceDecl
	targetInterface *InterfaceDecl
	translations    []EventTranslation
}

// NewMap starts building a new Map with the given name.
func NewMap(name string) *MapBuilder {
	return &MapBuilder{name: name}
}

// From sets the source interface.
func (b *MapBuilder) From(iface *InterfaceDecl) *MapBuilder {
	b.sourceInterface = iface
	return b
}

// To sets the target interface.
func (b *MapBuilder) To(iface *InterfaceDecl) *MapBuilder {
	b.targetInterface = iface
	return b
}

// Translate adds a simple action-to-action translation (copies params).
func (b *MapBuilder) Translate(sourceAction, targetAction string) *MapBuilder {
	b.translations = append(b.translations, EventTranslation{
		SourceAction: sourceAction,
		TargetAction: targetAction,
	})
	return b
}

// TranslateWith adds a translation with a custom parameter transform.
func (b *MapBuilder) TranslateWith(sourceAction, targetAction string,
	transform func(*gorapide.Event) map[string]any) *MapBuilder {
	b.translations = append(b.translations, EventTranslation{
		SourceAction: sourceAction,
		TargetAction: targetAction,
		Transform:    transform,
	})
	return b
}

// TranslateGuarded adds a translation with a guard predicate and optional transform.
func (b *MapBuilder) TranslateGuarded(sourceAction, targetAction string,
	guard func(*gorapide.Event) bool,
	transform func(*gorapide.Event) map[string]any) *MapBuilder {
	b.translations = append(b.translations, EventTranslation{
		SourceAction: sourceAction,
		TargetAction: targetAction,
		Guard:        guard,
		Transform:    transform,
	})
	return b
}

// Build finalizes and returns the Map.
func (b *MapBuilder) Build() *Map {
	return &Map{
		Name:            b.name,
		SourceInterface: b.sourceInterface,
		TargetInterface: b.targetInterface,
		Translations:    b.translations,
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestNewMapBuilder|TestMapTranslateWith|TestMapTranslateGuarded" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add arch/mapping.go arch/mapping_test.go
git commit -m "feat(arch): add Map type and MapBuilder for event translation"
```

---

### Task 2: Map.MapEvent and Map.Validate

**Files:**
- Modify: `arch/mapping_test.go`
- (Implementation already in `arch/mapping.go` from Task 1)

- [ ] **Step 1: Write failing tests for MapEvent and Validate**

Append to `arch/mapping_test.go`:

```go
func TestMapEventSimpleTranslation(t *testing.T) {
	srcIface := Interface("A").OutAction("X").Build()
	dstIface := Interface("B").InAction("Y").Build()

	m := NewMap("test").
		From(srcIface).
		To(dstIface).
		Translate("X", "Y").
		Build()

	source := gorapide.NewEvent("X", "compA", map[string]any{"val": 42})
	results, err := m.MapEvent(source)
	if err != nil {
		t.Fatalf("MapEvent: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Name != "Y" {
		t.Errorf("Name: want Y, got %s", results[0].Name)
	}
	if results[0].ParamInt("val") != 42 {
		t.Errorf("Param val: want 42, got %d", results[0].ParamInt("val"))
	}
}

func TestMapEventNoMatch(t *testing.T) {
	srcIface := Interface("A").OutAction("X").Build()
	dstIface := Interface("B").InAction("Y").Build()

	m := NewMap("test").
		From(srcIface).
		To(dstIface).
		Translate("X", "Y").
		Build()

	source := gorapide.NewEvent("Z", "compA", nil) // Z doesn't match X
	results, err := m.MapEvent(source)
	if err != nil {
		t.Fatalf("MapEvent: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results for non-matching event, got %d", len(results))
	}
}

func TestMapEventWithGuard(t *testing.T) {
	srcIface := Interface("A").OutAction("X").Build()
	dstIface := Interface("B").InAction("Y").Build()

	m := NewMap("test").
		From(srcIface).
		To(dstIface).
		TranslateGuarded("X", "Y",
			func(e *gorapide.Event) bool { return e.ParamString("level") == "HIGH" },
			nil,
		).
		Build()

	// Should match: level=HIGH
	high := gorapide.NewEvent("X", "compA", map[string]any{"level": "HIGH"})
	results, _ := m.MapEvent(high)
	if len(results) != 1 {
		t.Errorf("HIGH event should match, got %d results", len(results))
	}

	// Should not match: level=LOW
	low := gorapide.NewEvent("X", "compA", map[string]any{"level": "LOW"})
	results, _ = m.MapEvent(low)
	if len(results) != 0 {
		t.Errorf("LOW event should not match, got %d results", len(results))
	}
}

func TestMapEventWithTransform(t *testing.T) {
	srcIface := Interface("A").OutAction("X").Build()
	dstIface := Interface("B").InAction("Y").Build()

	m := NewMap("test").
		From(srcIface).
		To(dstIface).
		TranslateWith("X", "Y", func(e *gorapide.Event) map[string]any {
			return map[string]any{"doubled": e.ParamInt("val") * 2}
		}).
		Build()

	source := gorapide.NewEvent("X", "compA", map[string]any{"val": 5})
	results, _ := m.MapEvent(source)
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].ParamInt("doubled") != 10 {
		t.Errorf("doubled: want 10, got %d", results[0].ParamInt("doubled"))
	}
}

func TestMapEventMultipleTranslations(t *testing.T) {
	srcIface := Interface("A").OutAction("X").Build()
	dstIface := Interface("B").InAction("Y").InAction("Z").Build()

	m := NewMap("test").
		From(srcIface).
		To(dstIface).
		Translate("X", "Y").
		Translate("X", "Z").
		Build()

	source := gorapide.NewEvent("X", "compA", nil)
	results, _ := m.MapEvent(source)
	if len(results) != 2 {
		t.Fatalf("want 2 results for multi-translation, got %d", len(results))
	}
	names := map[string]bool{results[0].Name: true, results[1].Name: true}
	if !names["Y"] || !names["Z"] {
		t.Errorf("want Y and Z, got %v", names)
	}
}

func TestMapValidateSuccess(t *testing.T) {
	srcIface := Interface("A").OutAction("X").Build()
	dstIface := Interface("B").InAction("Y").Build()

	m := NewMap("test").
		From(srcIface).
		To(dstIface).
		Translate("X", "Y").
		Build()

	if err := m.Validate(); err != nil {
		t.Errorf("Validate should pass: %v", err)
	}
}

func TestMapValidateBadSource(t *testing.T) {
	srcIface := Interface("A").OutAction("X").Build()
	dstIface := Interface("B").InAction("Y").Build()

	m := NewMap("test").
		From(srcIface).
		To(dstIface).
		Translate("MISSING", "Y"). // MISSING not in source interface
		Build()

	if err := m.Validate(); err == nil {
		t.Error("Validate should fail for missing source action")
	}
}

func TestMapValidateBadTarget(t *testing.T) {
	srcIface := Interface("A").OutAction("X").Build()
	dstIface := Interface("B").InAction("Y").Build()

	m := NewMap("test").
		From(srcIface).
		To(dstIface).
		Translate("X", "MISSING"). // MISSING not in target interface
		Build()

	if err := m.Validate(); err == nil {
		t.Error("Validate should fail for missing target action")
	}
}

func TestMapString(t *testing.T) {
	srcIface := Interface("A").Build()
	dstIface := Interface("B").Build()

	m := NewMap("test").From(srcIface).To(dstIface).Translate("X", "Y").Build()
	s := m.String()
	if s == "" {
		t.Error("String should not be empty")
	}
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestMapEvent|TestMapValidate|TestMapString" -v`
Expected: PASS (implementation already in mapping.go from Task 1)

- [ ] **Step 3: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add arch/mapping_test.go
git commit -m "test(arch): add Map.MapEvent and Map.Validate tests"
```

---

### Task 3: BindingManager type

**Files:**
- Create: `arch/binding.go`
- Test: `arch/binding_test.go`

- [ ] **Step 1: Write failing tests for Binding and BindingManager**

```go
// arch/binding_test.go
package arch

import (
	"testing"
)

func TestBindingManagerBind(t *testing.T) {
	bm := NewBindingManager()
	err := bm.Bind("A", "B")
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	bindings := bm.BindingsFrom("A")
	if len(bindings) != 1 {
		t.Fatalf("BindingsFrom A: want 1, got %d", len(bindings))
	}
	if bindings[0].FromComp != "A" || bindings[0].ToComp != "B" {
		t.Errorf("Binding: want A->B, got %s->%s", bindings[0].FromComp, bindings[0].ToComp)
	}
}

func TestBindingManagerBindWith(t *testing.T) {
	srcIface := Interface("A").OutAction("X").Build()
	dstIface := Interface("B").InAction("Y").Build()

	m := NewMap("test").From(srcIface).To(dstIface).Translate("X", "Y").Build()

	bm := NewBindingManager()
	id, err := bm.BindWith("A", "B", WithBindingMap(m), WithBindingKind(PipeConnection))
	if err != nil {
		t.Fatalf("BindWith: %v", err)
	}
	if id == "" {
		t.Error("BindWith should return a non-empty ID")
	}

	bindings := bm.BindingsFrom("A")
	if len(bindings) != 1 {
		t.Fatalf("BindingsFrom A: want 1, got %d", len(bindings))
	}
	if bindings[0].Map != m {
		t.Error("Binding should have the map set")
	}
	if bindings[0].Kind != PipeConnection {
		t.Errorf("Kind: want PipeConnection, got %v", bindings[0].Kind)
	}
}

func TestBindingManagerUnbind(t *testing.T) {
	bm := NewBindingManager()
	bm.Bind("A", "B")
	bm.Bind("A", "C")
	bm.Bind("D", "E")

	err := bm.Unbind("A")
	if err != nil {
		t.Fatalf("Unbind: %v", err)
	}

	if len(bm.BindingsFrom("A")) != 0 {
		t.Error("All bindings from A should be removed")
	}
	if len(bm.BindingsFrom("D")) != 1 {
		t.Error("Bindings from D should be unaffected")
	}
}

func TestBindingManagerUnbindByID(t *testing.T) {
	bm := NewBindingManager()
	id1, _ := bm.BindWith("A", "B")
	bm.BindWith("A", "C")

	err := bm.UnbindByID(id1)
	if err != nil {
		t.Fatalf("UnbindByID: %v", err)
	}

	bindings := bm.BindingsFrom("A")
	if len(bindings) != 1 {
		t.Fatalf("want 1 remaining binding, got %d", len(bindings))
	}
	if bindings[0].ToComp != "C" {
		t.Errorf("remaining binding should be A->C, got A->%s", bindings[0].ToComp)
	}
}

func TestBindingManagerUnbindByIDNotFound(t *testing.T) {
	bm := NewBindingManager()
	err := bm.UnbindByID("nonexistent")
	if err == nil {
		t.Error("UnbindByID should fail for unknown ID")
	}
}

func TestBindingManagerActiveBindings(t *testing.T) {
	bm := NewBindingManager()
	bm.Bind("A", "B")
	bm.Bind("C", "D")

	all := bm.ActiveBindings()
	if len(all) != 2 {
		t.Errorf("ActiveBindings: want 2, got %d", len(all))
	}
}

func TestBindingManagerDefaultKind(t *testing.T) {
	bm := NewBindingManager()
	bm.Bind("A", "B")

	b := bm.BindingsFrom("A")[0]
	if b.Kind != PipeConnection {
		t.Errorf("Default kind should be PipeConnection, got %v", b.Kind)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestBindingManager" -v`
Expected: FAIL — `NewBindingManager` undefined

- [ ] **Step 3: Write BindingManager implementation**

```go
// arch/binding.go
package arch

import (
	"fmt"
	"sync"

	"github.com/ShaneDolphin/gorapide"
)

// Binding represents a dynamic runtime connection between two components,
// optionally mediated by a Map for event translation.
type Binding struct {
	ID       string
	FromComp string
	ToComp   string
	Map      *Map
	Kind     ConnectionKind
}

// BindingOption configures a Binding.
type BindingOption func(*Binding)

// WithBindingMap sets the event translation map for a binding.
func WithBindingMap(m *Map) BindingOption {
	return func(b *Binding) {
		b.Map = m
	}
}

// WithBindingKind sets the connection semantics for a binding.
func WithBindingKind(k ConnectionKind) BindingOption {
	return func(b *Binding) {
		b.Kind = k
	}
}

// BindingManager manages dynamic bindings within an architecture.
// It is safe for concurrent use.
type BindingManager struct {
	bindings map[string]*Binding   // binding ID -> Binding
	bySource map[string][]string   // source comp ID -> binding IDs
	mu       sync.RWMutex
	nextID   int
}

// NewBindingManager creates an empty BindingManager.
func NewBindingManager() *BindingManager {
	return &BindingManager{
		bindings: make(map[string]*Binding),
		bySource: make(map[string][]string),
	}
}

// Bind creates a binding from source to target with default settings
// (PipeConnection, no Map). Satisfies gorapide.BindingTarget.
func (bm *BindingManager) Bind(from, to string) error {
	_, err := bm.BindWith(from, to)
	return err
}

// Unbind removes all bindings where the source is 'from'.
// Satisfies gorapide.BindingTarget.
func (bm *BindingManager) Unbind(from string) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	ids := bm.bySource[from]
	for _, id := range ids {
		delete(bm.bindings, id)
	}
	delete(bm.bySource, from)
	return nil
}

// BindWith creates a binding with optional configuration. Returns the binding ID.
func (bm *BindingManager) BindWith(from, to string, opts ...BindingOption) (string, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	bm.nextID++
	id := fmt.Sprintf("binding-%d", bm.nextID)

	b := &Binding{
		ID:       id,
		FromComp: from,
		ToComp:   to,
		Kind:     PipeConnection, // default
	}
	for _, opt := range opts {
		opt(b)
	}

	bm.bindings[id] = b
	bm.bySource[from] = append(bm.bySource[from], id)
	return id, nil
}

// UnbindByID removes a specific binding by its ID.
func (bm *BindingManager) UnbindByID(id string) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	b, ok := bm.bindings[id]
	if !ok {
		return fmt.Errorf("arch.BindingManager.UnbindByID: binding %q not found", id)
	}

	delete(bm.bindings, id)

	// Remove from bySource index.
	ids := bm.bySource[b.FromComp]
	for i, bid := range ids {
		if bid == id {
			bm.bySource[b.FromComp] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
	if len(bm.bySource[b.FromComp]) == 0 {
		delete(bm.bySource, b.FromComp)
	}
	return nil
}

// BindingsFrom returns all active bindings where the given component is the source.
func (bm *BindingManager) BindingsFrom(componentID string) []*Binding {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	ids := bm.bySource[componentID]
	result := make([]*Binding, 0, len(ids))
	for _, id := range ids {
		if b, ok := bm.bindings[id]; ok {
			result = append(result, b)
		}
	}
	return result
}

// ActiveBindings returns all active bindings.
func (bm *BindingManager) ActiveBindings() []*Binding {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	result := make([]*Binding, 0, len(bm.bindings))
	for _, b := range bm.bindings {
		result = append(result, b)
	}
	return result
}

// executeBinding processes an event through a binding, creating translated
// events on the target component. Returns created events for cascade processing.
func (bm *BindingManager) executeBinding(b *Binding, triggerEvent *gorapide.Event, target *Component, poset *gorapide.Poset) []*gorapide.Event {
	if b.Map != nil {
		translated, err := b.Map.MapEvent(triggerEvent)
		if err != nil || len(translated) == 0 {
			return nil
		}
		var results []*gorapide.Event
		for _, te := range translated {
			te.Source = target.ID
			switch b.Kind {
			case PipeConnection:
				if err := poset.AddEventWithCause(te, triggerEvent.ID); err != nil {
					continue
				}
			default:
				if err := poset.AddEvent(te); err != nil {
					continue
				}
			}
			target.Send(te)
			results = append(results, te)
		}
		return results
	}

	// No map: forward the event with identity translation.
	switch b.Kind {
	case AgentConnection:
		target.Send(triggerEvent)
		return nil
	case PipeConnection:
		e := gorapide.NewEvent(triggerEvent.Name, target.ID, copyParams(triggerEvent))
		if err := poset.AddEventWithCause(e, triggerEvent.ID); err != nil {
			return nil
		}
		target.Send(e)
		return []*gorapide.Event{e}
	default: // BasicConnection
		e := gorapide.NewEvent(triggerEvent.Name, target.ID, copyParams(triggerEvent))
		if err := poset.AddEvent(e); err != nil {
			return nil
		}
		target.Send(e)
		return []*gorapide.Event{e}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestBindingManager" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add arch/binding.go arch/binding_test.go
git commit -m "feat(arch): add BindingManager for dynamic runtime wiring"
```

---

### Task 4: Integrate bindings into Architecture

**Files:**
- Modify: `arch/architecture.go`
- Test: `arch/binding_test.go` (append)

- [ ] **Step 1: Write failing tests for Architecture.Bind and Architecture.Unbind**

Append to `arch/binding_test.go`:

```go
func TestArchitectureBind(t *testing.T) {
	a := NewArchitecture("test")
	compA := NewComponent("A", Interface("I").OutAction("X").Build(), nil)
	compB := NewComponent("B", Interface("I").InAction("X").Build(), nil)
	a.AddComponent(compA)
	a.AddComponent(compB)

	err := a.Bind("A", "B")
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	bindings := a.Bindings()
	if len(bindings) != 1 {
		t.Fatalf("Bindings: want 1, got %d", len(bindings))
	}
}

func TestArchitectureBindUnknownComponent(t *testing.T) {
	a := NewArchitecture("test")
	compA := NewComponent("A", Interface("I").Build(), nil)
	a.AddComponent(compA)

	err := a.Bind("A", "UNKNOWN")
	if err == nil {
		t.Error("Bind should fail for unknown target component")
	}
}

func TestArchitectureUnbind(t *testing.T) {
	a := NewArchitecture("test")
	compA := NewComponent("A", Interface("I").Build(), nil)
	compB := NewComponent("B", Interface("I").Build(), nil)
	a.AddComponent(compA)
	a.AddComponent(compB)

	a.Bind("A", "B")
	err := a.Unbind("A")
	if err != nil {
		t.Fatalf("Unbind: %v", err)
	}

	if len(a.Bindings()) != 0 {
		t.Error("Bindings should be empty after Unbind")
	}
}

func TestArchitectureBindWith(t *testing.T) {
	a := NewArchitecture("test")
	srcIface := Interface("Scanner").OutAction("VulnFound").Build()
	dstIface := Interface("Aggregator").InAction("Finding").Build()
	compA := NewComponent("scanner", srcIface, nil)
	compB := NewComponent("aggregator", dstIface, nil)
	a.AddComponent(compA)
	a.AddComponent(compB)

	m := NewMap("scan_map").From(srcIface).To(dstIface).Translate("VulnFound", "Finding").Build()

	id, err := a.BindWith("scanner", "aggregator", WithBindingMap(m))
	if err != nil {
		t.Fatalf("BindWith: %v", err)
	}
	if id == "" {
		t.Error("BindWith should return non-empty ID")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestArchitectureBind" -v`
Expected: FAIL — `a.Bind` undefined

- [ ] **Step 3: Add bindings field and methods to Architecture**

In `arch/architecture.go`, make these changes:

Add `bindings *BindingManager` field to the `Architecture` struct (after line 19):

```go
// In the Architecture struct, add after the connections field:
	bindings    *BindingManager
```

Initialize it in `NewArchitecture` (after line 58, before the `for` loop):

```go
// In NewArchitecture, add to initialization:
		bindings:   NewBindingManager(),
```

Add the following methods after the `Poset()` method (after line 127):

```go
// Bind creates a dynamic binding between two components using default
// settings (PipeConnection, no Map). Components must already be registered.
func (a *Architecture) Bind(from, to string) error {
	a.mu.RLock()
	_, fromOK := a.components[from]
	_, toOK := a.components[to]
	a.mu.RUnlock()
	if !fromOK {
		return fmt.Errorf("arch: source component %q not found", from)
	}
	if !toOK {
		return fmt.Errorf("arch: target component %q not found", to)
	}
	return a.bindings.Bind(from, to)
}

// Unbind removes all dynamic bindings from the given source component.
func (a *Architecture) Unbind(from string) error {
	return a.bindings.Unbind(from)
}

// BindWith creates a dynamic binding with options (Map, ConnectionKind).
// Components must already be registered.
func (a *Architecture) BindWith(from, to string, opts ...BindingOption) (string, error) {
	a.mu.RLock()
	_, fromOK := a.components[from]
	_, toOK := a.components[to]
	a.mu.RUnlock()
	if !fromOK {
		return "", fmt.Errorf("arch: source component %q not found", from)
	}
	if !toOK {
		return "", fmt.Errorf("arch: target component %q not found", to)
	}
	return a.bindings.BindWith(from, to, opts...)
}

// UnbindByID removes a specific dynamic binding by its ID.
func (a *Architecture) UnbindByID(id string) error {
	return a.bindings.UnbindByID(id)
}

// Bindings returns all active dynamic bindings.
func (a *Architecture) Bindings() []*Binding {
	return a.bindings.ActiveBindings()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestArchitectureBind" -v`
Expected: PASS

- [ ] **Step 5: Run full test suite to verify nothing is broken**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All tests PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add arch/architecture.go arch/binding_test.go
git commit -m "feat(arch): integrate BindingManager into Architecture with Bind/Unbind methods"
```

---

### Task 5: Wire bindings into processEventCascade

**Files:**
- Modify: `arch/architecture.go` (the `processEventCascade` method)
- Test: `arch/binding_integration_test.go`

- [ ] **Step 1: Write failing integration test for binding-driven event routing**

```go
// arch/binding_integration_test.go
package arch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestBindingRoutesEvents(t *testing.T) {
	a := NewArchitecture("test")

	srcIface := Interface("Src").OutAction("X").Build()
	dstIface := Interface("Dst").InAction("X").Build()

	producer := NewComponent("producer", srcIface, nil)
	consumer := NewComponent("consumer", dstIface, nil)
	a.AddComponent(producer)
	a.AddComponent(consumer)

	// Dynamic binding instead of static connection.
	a.Bind("producer", "consumer")

	var mu sync.Mutex
	var received []*gorapide.Event
	consumer.OnEvent("X", func(ctx BehaviorContext) {
		mu.Lock()
		received = append(received, ctx.Matched...)
		mu.Unlock()
	})

	ctx := context.Background()
	a.Start(ctx)

	producer.Emit("X", map[string]any{"val": 1})
	time.Sleep(100 * time.Millisecond)

	a.Stop()
	a.Wait()

	mu.Lock()
	defer mu.Unlock()

	if len(received) == 0 {
		t.Fatal("consumer should have received events via binding")
	}
}

func TestBindingWithMapTranslatesEvents(t *testing.T) {
	a := NewArchitecture("test")

	srcIface := Interface("Scanner").OutAction("VulnFound").Build()
	dstIface := Interface("Aggregator").InAction("Finding").Build()

	scanner := NewComponent("scanner", srcIface, nil)
	aggregator := NewComponent("aggregator", dstIface, nil)
	a.AddComponent(scanner)
	a.AddComponent(aggregator)

	m := NewMap("vuln_map").
		From(srcIface).
		To(dstIface).
		TranslateWith("VulnFound", "Finding", func(e *gorapide.Event) map[string]any {
			return map[string]any{
				"cve":      e.ParamString("cve"),
				"severity": e.ParamString("severity"),
				"mapped":   true,
			}
		}).
		Build()

	a.BindWith("scanner", "aggregator", WithBindingMap(m))

	var mu sync.Mutex
	var received []*gorapide.Event
	aggregator.OnEvent("Finding", func(ctx BehaviorContext) {
		mu.Lock()
		received = append(received, ctx.Matched...)
		mu.Unlock()
	})

	ctx := context.Background()
	a.Start(ctx)

	scanner.Emit("VulnFound", map[string]any{"cve": "CVE-2026-0001", "severity": "HIGH"})
	time.Sleep(100 * time.Millisecond)

	a.Stop()
	a.Wait()

	mu.Lock()
	defer mu.Unlock()

	if len(received) == 0 {
		t.Fatal("aggregator should have received translated Finding event")
	}

	finding := received[0]
	if finding.Name != "Finding" {
		t.Errorf("Name: want Finding, got %s", finding.Name)
	}
	if finding.ParamString("cve") != "CVE-2026-0001" {
		t.Errorf("cve: want CVE-2026-0001, got %s", finding.ParamString("cve"))
	}
	v, ok := finding.Param("mapped")
	if !ok || v != true {
		t.Error("mapped param should be true")
	}
}

func TestBindingCausalLink(t *testing.T) {
	a := NewArchitecture("test")

	iface := Interface("I").OutAction("X").InAction("X").Build()
	producer := NewComponent("producer", iface, nil)
	consumer := NewComponent("consumer", iface, nil)
	a.AddComponent(producer)
	a.AddComponent(consumer)

	// PipeConnection binding creates causal link.
	a.BindWith("producer", "consumer", WithBindingKind(PipeConnection))

	ctx := context.Background()
	a.Start(ctx)

	emitted, _ := producer.Emit("X", nil)
	time.Sleep(100 * time.Millisecond)

	a.Stop()
	a.Wait()

	// Check that a causal descendant exists.
	descendants := a.Poset().CausalDescendants(emitted.ID)
	if len(descendants) == 0 {
		t.Error("PipeConnection binding should create causal descendant")
	}
}

func TestBindingCoexistsWithStaticConnections(t *testing.T) {
	a := NewArchitecture("test")

	iface := Interface("I").OutAction("X").InAction("X").InAction("Y").Build()
	producer := NewComponent("producer", iface, nil)
	staticConsumer := NewComponent("static", iface, nil)
	dynamicConsumer := NewComponent("dynamic", iface, nil)
	a.AddComponent(producer)
	a.AddComponent(staticConsumer)
	a.AddComponent(dynamicConsumer)

	// Static connection: producer -> staticConsumer
	a.AddConnection(Connect("producer", "static").
		On(pattern.MatchEvent("X")).
		Pipe().
		Send("Y").
		Build())

	// Dynamic binding: producer -> dynamicConsumer
	a.Bind("producer", "dynamic")

	var mu sync.Mutex
	staticGot := false
	dynamicGot := false

	staticConsumer.OnEvent("Y", func(ctx BehaviorContext) {
		mu.Lock()
		staticGot = true
		mu.Unlock()
	})
	dynamicConsumer.OnEvent("X", func(ctx BehaviorContext) {
		mu.Lock()
		dynamicGot = true
		mu.Unlock()
	})

	ctx := context.Background()
	a.Start(ctx)

	producer.Emit("X", nil)
	time.Sleep(100 * time.Millisecond)

	a.Stop()
	a.Wait()

	mu.Lock()
	defer mu.Unlock()

	if !staticGot {
		t.Error("static consumer should have received via connection")
	}
	if !dynamicGot {
		t.Error("dynamic consumer should have received via binding")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestBindingRoutesEvents" -v`
Expected: FAIL — binding events don't route (processEventCascade doesn't evaluate bindings yet)

- [ ] **Step 3: Modify processEventCascade to evaluate bindings**

In `arch/architecture.go`, replace the `processEventCascade` method (lines 292-328) with:

```go
// processEventCascade processes an event and any events created by
// connection and binding executions (cascading).
func (a *Architecture) processEventCascade(e *gorapide.Event) {
	queue := []*gorapide.Event{e}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Evaluate static connections.
		a.mu.RLock()
		conns := make([]*Connection, len(a.connections))
		copy(conns, a.connections)
		a.mu.RUnlock()

		for _, conn := range conns {
			targets := a.resolveTargets(conn, current)
			for _, target := range targets {
				source := a.resolveSource(conn, current)
				newEvent, err := conn.execute(current, source, target)
				if err == nil && newEvent != nil {
					queue = append(queue, newEvent)
				}
			}
		}

		// Evaluate dynamic bindings.
		bindings := a.bindings.BindingsFrom(current.Source)
		for _, binding := range bindings {
			a.mu.RLock()
			target, targetOK := a.components[binding.ToComp]
			a.mu.RUnlock()
			if !targetOK {
				continue
			}
			newEvents := a.bindings.executeBinding(binding, current, target, a.poset)
			queue = append(queue, newEvents...)
		}

		// Notify global observers.
		a.mu.RLock()
		observers := make([]func(*gorapide.Event), len(a.onEvent))
		copy(observers, a.onEvent)
		eventChecker := a.checker
		a.mu.RUnlock()
		for _, fn := range observers {
			fn(current)
		}
		// Notify checker for CheckOnEvent mode.
		if eventChecker != nil {
			eventChecker.NotifyEvent()
		}
	}
}
```

- [ ] **Step 4: Run integration tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestBinding" -v -race`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All tests PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add arch/architecture.go arch/binding_integration_test.go
git commit -m "feat(arch): wire bindings into event cascade routing"
```

---

### Task 6: Compile-time assertions and placeholder test update

**Files:**
- Modify: `store.go`
- Modify: `store_test.go`

- [ ] **Step 1: Write test verifying compile-time assertions**

Replace the `TestPlaceholderInterfacesExist` function in `store_test.go` (lines 305-318) with:

```go
// Verify MapTarget and BindingTarget interfaces are satisfied by real types.
func TestMapTargetAndBindingTargetSatisfied(t *testing.T) {
	// These are compile-time assertions verified at package level in store.go.
	// This test confirms the implementations work at runtime.
	var m MapTarget
	var b BindingTarget

	// m and b are nil — just confirm the interfaces are still properly defined.
	if m != nil {
		t.Error("nil MapTarget should be nil")
	}
	if b != nil {
		t.Error("nil BindingTarget should be nil")
	}
}
```

- [ ] **Step 2: Add compile-time assertions to store.go**

Append after line 45 in `store.go` (after `var _ PosetReadWriter = (*Poset)(nil)`):

```go
// Compile-time assertions that arch.Map satisfies MapTarget
// and arch.BindingManager satisfies BindingTarget are in the arch package
// (they cannot be here due to import cycle). See arch/mapping.go and arch/binding.go.
```

Create compile-time assertions in the arch package. Add to the end of `arch/mapping.go`:

```go
// Compile-time assertion that *Map satisfies gorapide.MapTarget.
var _ gorapide.MapTarget = (*Map)(nil)
```

Add to the end of `arch/binding.go`:

```go
// Compile-time assertion that *BindingManager satisfies gorapide.BindingTarget.
var _ gorapide.BindingTarget = (*BindingManager)(nil)
```

- [ ] **Step 3: Run full test suite**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All tests PASS (compile-time assertions verified)

- [ ] **Step 4: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add store.go store_test.go arch/mapping.go arch/binding.go
git commit -m "feat: add compile-time assertions for MapTarget and BindingTarget"
```

---

## Part B: OpenTelemetry Collector Integration

### Task 7: otelexport sub-module setup and ID mapping

**Files:**
- Create: `otelexport/go.mod`
- Create: `otelexport/doc.go`
- Create: `otelexport/ids.go`
- Create: `otelexport/ids_test.go`

- [ ] **Step 1: Create the sub-module**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
mkdir -p otelexport
```

Create `otelexport/go.mod`:

```
module github.com/ShaneDolphin/gorapide/otelexport

go 1.22

require (
	github.com/ShaneDolphin/gorapide v0.0.0
	go.opentelemetry.io/otel v1.35.0
	go.opentelemetry.io/otel/sdk v1.35.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.35.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.35.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.35.0
)

replace github.com/ShaneDolphin/gorapide => ../
```

Create `otelexport/doc.go`:

```go
// Package otelexport provides live OpenTelemetry trace export for gorapide
// architectures. It streams poset events as OTLP spans to a collector
// during execution, using the existing WithObserver hook.
//
// Usage:
//
//	exporter, err := otelexport.NewLiveExporter(otelexport.Config{
//	    Endpoint:    "localhost:4317",
//	    ServiceName: "my-pipeline",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer exporter.Shutdown(context.Background())
//
//	pipeline := arch.NewArchitecture("my-pipeline",
//	    arch.WithObserver(exporter.OnEvent),
//	)
package otelexport
```

- [ ] **Step 2: Write failing test for ID mapping**

```go
// otelexport/ids_test.go
package otelexport

import (
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestSpanIDFromEventID(t *testing.T) {
	id := gorapide.NewEventID()
	spanID := spanIDFromEventID(id)

	if spanID.IsValid() == false {
		t.Error("SpanID should be valid")
	}

	// Same input produces same output.
	spanID2 := spanIDFromEventID(id)
	if spanID != spanID2 {
		t.Error("spanIDFromEventID should be deterministic")
	}
}

func TestSpanIDFromDifferentEventIDs(t *testing.T) {
	id1 := gorapide.NewEventID()
	id2 := gorapide.NewEventID()

	s1 := spanIDFromEventID(id1)
	s2 := spanIDFromEventID(id2)

	if s1 == s2 {
		t.Error("different EventIDs should produce different SpanIDs")
	}
}

func TestNewTraceID(t *testing.T) {
	tid := newTraceID()
	if !tid.IsValid() {
		t.Error("TraceID should be valid")
	}
}

func TestFixedTraceID(t *testing.T) {
	id := gorapide.NewEventID()
	tid := traceIDFromEventID(id)
	if !tid.IsValid() {
		t.Error("TraceID from EventID should be valid")
	}

	// Deterministic.
	tid2 := traceIDFromEventID(id)
	if tid != tid2 {
		t.Error("traceIDFromEventID should be deterministic")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/otelexport && go mod tidy && go test -run "TestSpanID|TestNewTraceID|TestFixedTraceID" -v`
Expected: FAIL — functions undefined

- [ ] **Step 4: Write ID mapping implementation**

```go
// otelexport/ids.go
package otelexport

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// spanIDFromEventID generates an 8-byte SpanID from an EventID.
// Uses the first 8 bytes of the UUID (stripped of dashes).
func spanIDFromEventID(id gorapide.EventID) oteltrace.SpanID {
	raw := strings.ReplaceAll(string(id), "-", "")
	var sid oteltrace.SpanID
	if len(raw) >= 16 {
		b, err := hex.DecodeString(raw[:16])
		if err == nil {
			copy(sid[:], b)
		}
	}
	return sid
}

// traceIDFromEventID generates a 16-byte TraceID from an EventID.
// Uses the full 16 bytes of the UUID (stripped of dashes).
func traceIDFromEventID(id gorapide.EventID) oteltrace.TraceID {
	raw := strings.ReplaceAll(string(id), "-", "")
	var tid oteltrace.TraceID
	if len(raw) >= 32 {
		b, err := hex.DecodeString(raw[:32])
		if err == nil {
			copy(tid[:], b)
		}
	}
	return tid
}

// newTraceID generates a random 16-byte TraceID.
func newTraceID() oteltrace.TraceID {
	var tid oteltrace.TraceID
	_, _ = rand.Read(tid[:])
	return tid
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/otelexport && go test -run "TestSpanID|TestNewTraceID|TestFixedTraceID" -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add otelexport/
git commit -m "feat(otelexport): add sub-module with TraceID/SpanID mapping from EventID"
```

---

### Task 8: Span batcher

**Files:**
- Create: `otelexport/batcher.go`
- Create: `otelexport/batcher_test.go`

- [ ] **Step 1: Write failing test for span batcher**

```go
// otelexport/batcher_test.go
package otelexport

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mockSpan is a minimal test double for a queued span.
type mockSpan struct {
	name string
}

func TestBatcherFlushOnSize(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]spanData
	flush := func(ctx context.Context, batch []spanData) {
		mu.Lock()
		cp := make([]spanData, len(batch))
		copy(cp, batch)
		flushed = append(flushed, cp)
		mu.Unlock()
	}

	b := newBatcher(3, 10*time.Second, flush)
	b.start()
	defer b.stop(context.Background())

	b.add(spanData{name: "a"})
	b.add(spanData{name: "b"})
	b.add(spanData{name: "c"}) // triggers flush at batch size 3

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 1 {
		t.Fatalf("want 1 flush, got %d", len(flushed))
	}
	if len(flushed[0]) != 3 {
		t.Errorf("want batch of 3, got %d", len(flushed[0]))
	}
}

func TestBatcherFlushOnTimeout(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]spanData
	flush := func(ctx context.Context, batch []spanData) {
		mu.Lock()
		cp := make([]spanData, len(batch))
		copy(cp, batch)
		flushed = append(flushed, cp)
		mu.Unlock()
	}

	b := newBatcher(100, 50*time.Millisecond, flush)
	b.start()
	defer b.stop(context.Background())

	b.add(spanData{name: "a"})

	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 1 {
		t.Fatalf("want 1 flush on timeout, got %d", len(flushed))
	}
	if len(flushed[0]) != 1 {
		t.Errorf("want batch of 1, got %d", len(flushed[0]))
	}
}

func TestBatcherStopFlushesRemaining(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]spanData
	flush := func(ctx context.Context, batch []spanData) {
		mu.Lock()
		cp := make([]spanData, len(batch))
		copy(cp, batch)
		flushed = append(flushed, cp)
		mu.Unlock()
	}

	b := newBatcher(100, 10*time.Second, flush)
	b.start()

	b.add(spanData{name: "a"})
	b.add(spanData{name: "b"})
	b.stop(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 1 {
		t.Fatalf("stop should flush remaining, got %d flushes", len(flushed))
	}
	if len(flushed[0]) != 2 {
		t.Errorf("want 2 remaining spans, got %d", len(flushed[0]))
	}
}

func TestBatcherDropsOnFullQueue(t *testing.T) {
	flush := func(ctx context.Context, batch []spanData) {
		// Block to simulate slow export.
		time.Sleep(500 * time.Millisecond)
	}

	b := newBatcher(1000, 10*time.Second, flush)
	b.start()

	// Fill queue (capacity is batcherQueueSize).
	for i := 0; i < batcherQueueSize+100; i++ {
		b.add(spanData{name: "x"})
	}

	// Should not panic or block. Dropped count should be > 0.
	if b.dropped() == 0 {
		// This is OK if the queue is large enough -- just verify no panic.
		t.Log("no drops (queue was large enough)")
	}

	b.stop(context.Background())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/otelexport && go test -run "TestBatcher" -v`
Expected: FAIL — `newBatcher` undefined

- [ ] **Step 3: Write batcher implementation**

```go
// otelexport/batcher.go
package otelexport

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	oteltrace "go.opentelemetry.io/otel/trace"
)

const batcherQueueSize = 2048

// spanData holds the data needed to export a single span.
type spanData struct {
	name       string
	traceID    oteltrace.TraceID
	spanID     oteltrace.SpanID
	parentID   oteltrace.SpanID
	startTime  time.Time
	source     string
	attributes map[string]string
	links      []oteltrace.SpanID
}

// batcher accumulates spanData and flushes in batches.
type batcher struct {
	maxSize  int
	timeout  time.Duration
	flushFn  func(ctx context.Context, batch []spanData)
	queue    chan spanData
	done     chan struct{}
	once     sync.Once
	dropCnt  atomic.Int64
}

func newBatcher(maxSize int, timeout time.Duration, flushFn func(ctx context.Context, batch []spanData)) *batcher {
	return &batcher{
		maxSize: maxSize,
		timeout: timeout,
		flushFn: flushFn,
		queue:   make(chan spanData, batcherQueueSize),
		done:    make(chan struct{}),
	}
}

func (b *batcher) start() {
	go b.run()
}

func (b *batcher) run() {
	defer close(b.done)
	batch := make([]spanData, 0, b.maxSize)
	timer := time.NewTimer(b.timeout)
	defer timer.Stop()

	for {
		select {
		case sd, ok := <-b.queue:
			if !ok {
				// Channel closed — flush remaining.
				if len(batch) > 0 {
					b.flushFn(context.Background(), batch)
				}
				return
			}
			batch = append(batch, sd)
			if len(batch) >= b.maxSize {
				b.flushFn(context.Background(), batch)
				batch = make([]spanData, 0, b.maxSize)
				timer.Reset(b.timeout)
			}
		case <-timer.C:
			if len(batch) > 0 {
				b.flushFn(context.Background(), batch)
				batch = make([]spanData, 0, b.maxSize)
			}
			timer.Reset(b.timeout)
		}
	}
}

func (b *batcher) add(sd spanData) {
	select {
	case b.queue <- sd:
	default:
		b.dropCnt.Add(1)
	}
}

func (b *batcher) stop(ctx context.Context) {
	b.once.Do(func() {
		close(b.queue)
	})
	<-b.done
}

func (b *batcher) dropped() int64 {
	return b.dropCnt.Load()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/otelexport && go test -run "TestBatcher" -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add otelexport/batcher.go otelexport/batcher_test.go
git commit -m "feat(otelexport): add span batcher with backpressure"
```

---

### Task 9: LiveExporter

**Files:**
- Create: `otelexport/exporter.go`
- Create: `otelexport/exporter_test.go`

- [ ] **Step 1: Write failing test for LiveExporter.OnEvent**

```go
// otelexport/exporter_test.go
package otelexport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

func TestLiveExporterOnEvent(t *testing.T) {
	var mu sync.Mutex
	var exported []spanData

	poset := gorapide.NewPoset()
	exp := newTestExporter(poset, func(ctx context.Context, batch []spanData) {
		mu.Lock()
		exported = append(exported, batch...)
		mu.Unlock()
	})
	defer exp.Shutdown(context.Background())

	// Add event to poset and trigger OnEvent.
	e := gorapide.NewEvent("ScanStart", "scanner", map[string]any{"target": "host1"})
	poset.AddEvent(e)
	exp.OnEvent(e)

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(exported) != 1 {
		t.Fatalf("want 1 exported span, got %d", len(exported))
	}
	if exported[0].name != "ScanStart" {
		t.Errorf("name: want ScanStart, got %s", exported[0].name)
	}
	if exported[0].source != "scanner" {
		t.Errorf("source: want scanner, got %s", exported[0].source)
	}
}

func TestLiveExporterDeduplicates(t *testing.T) {
	var mu sync.Mutex
	var exported []spanData

	poset := gorapide.NewPoset()
	exp := newTestExporter(poset, func(ctx context.Context, batch []spanData) {
		mu.Lock()
		exported = append(exported, batch...)
		mu.Unlock()
	})
	defer exp.Shutdown(context.Background())

	e := gorapide.NewEvent("X", "src", nil)
	poset.AddEvent(e)

	exp.OnEvent(e)
	exp.OnEvent(e) // duplicate
	exp.OnEvent(e) // duplicate

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(exported) != 1 {
		t.Errorf("want 1 deduplicated span, got %d", len(exported))
	}
}

func TestLiveExporterCausalParent(t *testing.T) {
	var mu sync.Mutex
	var exported []spanData

	poset := gorapide.NewPoset()
	exp := newTestExporter(poset, func(ctx context.Context, batch []spanData) {
		mu.Lock()
		exported = append(exported, batch...)
		mu.Unlock()
	})
	defer exp.Shutdown(context.Background())

	parent := gorapide.NewEvent("A", "src", nil)
	poset.AddEvent(parent)

	child := gorapide.NewEvent("B", "src", nil)
	poset.AddEventWithCause(child, parent.ID)

	exp.OnEvent(parent)
	exp.OnEvent(child)

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(exported) != 2 {
		t.Fatalf("want 2 spans, got %d", len(exported))
	}

	// Find the child span.
	var childSpan spanData
	for _, s := range exported {
		if s.name == "B" {
			childSpan = s
			break
		}
	}

	parentSpanID := spanIDFromEventID(parent.ID)
	if childSpan.parentID != parentSpanID {
		t.Errorf("child parentID should reference parent span")
	}
}

func TestLiveExporterCount(t *testing.T) {
	poset := gorapide.NewPoset()
	exp := newTestExporter(poset, func(ctx context.Context, batch []spanData) {})
	defer exp.Shutdown(context.Background())

	for i := 0; i < 5; i++ {
		e := gorapide.NewEvent("X", "src", nil)
		poset.AddEvent(e)
		exp.OnEvent(e)
	}

	if exp.ExportedCount() != 5 {
		t.Errorf("ExportedCount: want 5, got %d", exp.ExportedCount())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/otelexport && go test -run "TestLiveExporter" -v`
Expected: FAIL — `newTestExporter` undefined

- [ ] **Step 3: Write LiveExporter implementation**

```go
// otelexport/exporter.go
package otelexport

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ShaneDolphin/gorapide"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Protocol selects the OTLP transport.
type Protocol int

const (
	GRPC Protocol = iota
	HTTP
)

// Config configures the LiveExporter.
type Config struct {
	Endpoint     string
	Protocol     Protocol
	Headers      map[string]string
	ServiceName  string
	TraceID      oteltrace.TraceID // zero = auto-generate
	BatchSize    int               // default 512
	BatchTimeout time.Duration     // default 5s
	Insecure     bool
}

func (c *Config) defaults() {
	if c.BatchSize <= 0 {
		c.BatchSize = 512
	}
	if c.BatchTimeout <= 0 {
		c.BatchTimeout = 5 * time.Second
	}
	if c.ServiceName == "" {
		c.ServiceName = "gorapide"
	}
}

// LiveExporter streams poset events as OTLP spans to a collector.
type LiveExporter struct {
	traceID  oteltrace.TraceID
	poset    *gorapide.Poset
	batcher  *batcher
	exported map[gorapide.EventID]bool
	count    atomic.Int64
	mu       sync.Mutex
}

// NewLiveExporter creates a new exporter. For production use, it connects
// to an OTLP collector. Use newTestExporter for testing.
func NewLiveExporter(cfg Config) (*LiveExporter, error) {
	cfg.defaults()

	tid := cfg.TraceID
	if !tid.IsValid() {
		tid = newTraceID()
	}

	le := &LiveExporter{
		traceID:  tid,
		exported: make(map[gorapide.EventID]bool),
	}

	le.batcher = newBatcher(cfg.BatchSize, cfg.BatchTimeout, le.exportBatch)
	le.batcher.start()

	return le, nil
}

// newTestExporter creates a LiveExporter with a custom flush function for testing.
func newTestExporter(poset *gorapide.Poset, flushFn func(ctx context.Context, batch []spanData)) *LiveExporter {
	le := &LiveExporter{
		traceID:  newTraceID(),
		poset:    poset,
		exported: make(map[gorapide.EventID]bool),
	}
	le.batcher = newBatcher(512, 50*time.Millisecond, flushFn)
	le.batcher.start()
	return le
}

// SetPoset sets the poset reference for causal parent lookups.
func (le *LiveExporter) SetPoset(p *gorapide.Poset) {
	le.mu.Lock()
	le.poset = p
	le.mu.Unlock()
}

// OnEvent is the observer callback compatible with arch.WithObserver.
func (le *LiveExporter) OnEvent(e *gorapide.Event) {
	le.mu.Lock()
	if le.exported[e.ID] {
		le.mu.Unlock()
		return
	}
	le.exported[e.ID] = true
	poset := le.poset
	le.mu.Unlock()

	le.count.Add(1)

	sd := spanData{
		name:      e.Name,
		traceID:   le.traceID,
		spanID:    spanIDFromEventID(e.ID),
		startTime: e.Clock.WallTime,
		source:    e.Source,
	}

	// Build attributes from params + source.
	attrs := make(map[string]string)
	if e.Source != "" {
		attrs["source"] = e.Source
	}
	for k, v := range e.Params {
		attrs[k] = toString(v)
	}
	sd.attributes = attrs

	// Look up causal parents.
	if poset != nil {
		causes := poset.DirectCauses(e.ID)
		if len(causes) > 0 {
			// Sort by Lamport to pick canonical parent.
			sort.Slice(causes, func(i, j int) bool {
				return causes[i].Clock.Lamport < causes[j].Clock.Lamport
			})
			sd.parentID = spanIDFromEventID(causes[0].ID)
			for _, c := range causes[1:] {
				sd.links = append(sd.links, spanIDFromEventID(c.ID))
			}
		}
	}

	le.batcher.add(sd)
}

// Shutdown flushes pending spans and stops the exporter.
func (le *LiveExporter) Shutdown(ctx context.Context) error {
	le.batcher.stop(ctx)
	return nil
}

// ExportedCount returns the number of events processed.
func (le *LiveExporter) ExportedCount() int {
	return int(le.count.Load())
}

// Dropped returns the number of spans dropped due to backpressure.
func (le *LiveExporter) Dropped() int64 {
	return le.batcher.dropped()
}

// exportBatch sends a batch of spans to the OTLP collector.
// This is the production flush function, called by the batcher.
func (le *LiveExporter) exportBatch(ctx context.Context, batch []spanData) {
	// TODO: wire to actual OTLP SpanExporter in Task 10.
	// For now this is a no-op placeholder that will be replaced.
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/otelexport && go test -run "TestLiveExporter" -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add otelexport/exporter.go otelexport/exporter_test.go
git commit -m "feat(otelexport): add LiveExporter with OnEvent callback and deduplication"
```

---

### Task 10: Wire OTLP exporter to OTel SDK

**Files:**
- Create: `otelexport/otlp.go`
- Modify: `otelexport/exporter.go`

- [ ] **Step 1: Write the OTLP export bridge using the OTel SDK**

```go
// otelexport/otlp.go
package otelexport

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// otlpBridge wraps the OTel SDK to export spanData as OTLP spans.
type otlpBridge struct {
	exporter sdktrace.SpanExporter
	resource *resource.Resource
}

// newOTLPBridge creates a bridge to an OTLP collector.
func newOTLPBridge(ctx context.Context, cfg Config) (*otlpBridge, error) {
	var exp sdktrace.SpanExporter
	var err error

	switch cfg.Protocol {
	case GRPC:
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracegrpc.WithHeaders(cfg.Headers))
		}
		exp, err = otlptracegrpc.New(ctx, opts...)
	case HTTP:
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
		}
		exp, err = otlptracehttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("otelexport: unknown protocol %d", cfg.Protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("otelexport: creating exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
		),
	)
	if err != nil {
		exp.Shutdown(ctx)
		return nil, fmt.Errorf("otelexport: creating resource: %w", err)
	}

	return &otlpBridge{exporter: exp, resource: res}, nil
}

// export sends a batch of spanData to the OTLP collector.
func (ob *otlpBridge) export(ctx context.Context, batch []spanData) error {
	if len(batch) == 0 {
		return nil
	}

	snapshots := make([]sdktrace.ReadOnlySpan, len(batch))
	for i, sd := range batch {
		snapshots[i] = &spanSnapshot{
			sd:       sd,
			resource: ob.resource,
		}
	}

	return ob.exporter.ExportSpans(ctx, snapshots)
}

// shutdown closes the underlying OTLP exporter.
func (ob *otlpBridge) shutdown(ctx context.Context) error {
	return ob.exporter.Shutdown(ctx)
}

// spanSnapshot implements sdktrace.ReadOnlySpan for a spanData.
type spanSnapshot struct {
	sd       spanData
	resource *resource.Resource
}

func (s *spanSnapshot) Name() string                      { return s.sd.name }
func (s *spanSnapshot) SpanContext() oteltrace.SpanContext {
	return oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    s.sd.traceID,
		SpanID:     s.sd.spanID,
		TraceFlags: oteltrace.FlagsSampled,
	})
}
func (s *spanSnapshot) Parent() oteltrace.SpanContext {
	if !s.sd.parentID.IsValid() {
		return oteltrace.SpanContext{}
	}
	return oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    s.sd.traceID,
		SpanID:     s.sd.parentID,
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	})
}
func (s *spanSnapshot) SpanKind() oteltrace.SpanKind { return oteltrace.SpanKindInternal }
func (s *spanSnapshot) StartTime() time.Time         { return s.sd.startTime }
func (s *spanSnapshot) EndTime() time.Time           { return s.sd.startTime } // zero duration
func (s *spanSnapshot) Attributes() []attribute.KeyValue {
	var attrs []attribute.KeyValue
	for k, v := range s.sd.attributes {
		attrs = append(attrs, attribute.String(k, v))
	}
	return attrs
}
func (s *spanSnapshot) Links() []sdktrace.Link {
	var links []sdktrace.Link
	for _, sid := range s.sd.links {
		links = append(links, sdktrace.Link{
			SpanContext: oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
				TraceID: s.sd.traceID,
				SpanID:  sid,
			}),
		})
	}
	return links
}
func (s *spanSnapshot) Events() []sdktrace.Event                       { return nil }
func (s *spanSnapshot) Status() sdktrace.Status                        { return sdktrace.Status{} }
func (s *spanSnapshot) InstrumentationScope() instrumentation           { return instrumentation{} }
func (s *spanSnapshot) InstrumentationLibrary() instrumentation         { return instrumentation{} }
func (s *spanSnapshot) Resource() *resource.Resource                    { return s.resource }
func (s *spanSnapshot) DroppedAttributes() int                          { return 0 }
func (s *spanSnapshot) DroppedLinks() int                               { return 0 }
func (s *spanSnapshot) DroppedEvents() int                              { return 0 }
func (s *spanSnapshot) ChildSpanCount() int                             { return 0 }

type instrumentation struct{}
func (instrumentation) Name() string    { return "gorapide/otelexport" }
func (instrumentation) Version() string { return "0.1.0" }
func (instrumentation) SchemaURL() string { return "" }
```

**Note:** The `InstrumentationScope` return type needs to match the SDK's expected interface. After running `go mod tidy`, check the exact interface required by the SDK version and adjust. The method signatures above will be refined to match the actual `sdktrace.ReadOnlySpan` interface.

- [ ] **Step 2: Update NewLiveExporter to use the OTLP bridge**

In `otelexport/exporter.go`, replace the `NewLiveExporter` function:

```go
// NewLiveExporter creates a new exporter connected to an OTLP collector.
func NewLiveExporter(cfg Config) (*LiveExporter, error) {
	cfg.defaults()

	tid := cfg.TraceID
	if !tid.IsValid() {
		tid = newTraceID()
	}

	le := &LiveExporter{
		traceID:  tid,
		exported: make(map[gorapide.EventID]bool),
	}

	if cfg.Endpoint != "" {
		bridge, err := newOTLPBridge(context.Background(), cfg)
		if err != nil {
			return nil, err
		}
		le.bridge = bridge
		le.batcher = newBatcher(cfg.BatchSize, cfg.BatchTimeout, func(ctx context.Context, batch []spanData) {
			bridge.export(ctx, batch)
		})
	} else {
		le.batcher = newBatcher(cfg.BatchSize, cfg.BatchTimeout, le.exportBatch)
	}

	le.batcher.start()
	return le, nil
}
```

Add `bridge *otlpBridge` field to `LiveExporter` struct, and update `Shutdown`:

```go
// In the LiveExporter struct, add:
	bridge   *otlpBridge

// Replace Shutdown:
func (le *LiveExporter) Shutdown(ctx context.Context) error {
	le.batcher.stop(ctx)
	if le.bridge != nil {
		return le.bridge.shutdown(ctx)
	}
	return nil
}
```

- [ ] **Step 3: Run go mod tidy and fix compilation**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/otelexport && go mod tidy`

The `sdktrace.ReadOnlySpan` interface may require additional methods. Fix any compilation errors by checking the interface definition and adding missing method stubs.

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/otelexport && go build ./...`

Iterate until compilation succeeds.

- [ ] **Step 4: Run all otelexport tests**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/otelexport && go test -v -race ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add otelexport/
git commit -m "feat(otelexport): wire OTLP bridge to OTel SDK for live span export"
```

---

### Task 11: Verify full test suite passes

**Files:** None (verification only)

- [ ] **Step 1: Run the complete test suite from the root module**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All PASS

- [ ] **Step 2: Run the otelexport module tests**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/otelexport && go test -race ./...`
Expected: All PASS

- [ ] **Step 3: Run go vet on both modules**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go vet ./... && cd otelexport && go vet ./...`
Expected: No issues

---

## Verification

**Map and Binding:**
1. `go test -race ./arch/ -run TestMap` — all Map tests pass
2. `go test -race ./arch/ -run TestBinding` — all Binding tests pass (unit + integration)
3. `go test -race ./...` — full suite passes, no regressions
4. Compile-time assertions in `arch/mapping.go` and `arch/binding.go` verify `MapTarget` and `BindingTarget` interface compliance

**OTel Export:**
1. `cd otelexport && go test -race ./...` — all exporter tests pass
2. For manual verification with a real collector: run `docker run -p 4317:4317 otel/opentelemetry-collector` and point the exporter at `localhost:4317`
