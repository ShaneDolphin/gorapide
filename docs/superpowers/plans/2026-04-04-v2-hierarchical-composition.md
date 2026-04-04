# V2: Architecture Refinement & Hierarchical Composition

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable architectures to nest — a component slot in a parent architecture can itself be a sub-architecture containing its own components, with events flowing across hierarchy boundaries.

**Architecture:** Add a `Participant` interface satisfied by both `Component` and a new `SubArchitecture` wrapper. The `Architecture` struct gains a `subArchitectures` map alongside the existing `components` map (no refactoring — perfect backward compatibility). `SubArchitecture` bridges events via export rules (inner→parent) and import rules (parent→inner), each with optional parameter transformation. Separate posets per level preserve encapsulation.

**Tech Stack:** Go 1.22, zero new dependencies.

---

## File Structure

### New files

| File | Responsibility |
|------|---------------|
| `arch/participant.go` | `Participant` interface, `ParticipantID()`/`ParticipantInterface()` methods on `*Component` |
| `arch/participant_test.go` | Tests that `*Component` satisfies `Participant` |
| `arch/hierarchy.go` | `SubArchitecture`, `ExportRule`, `ImportRule`, boundary bridge goroutine, `SubArchBuilder` |
| `arch/hierarchy_test.go` | Unit tests for SubArchitecture construction, export/import rules |
| `arch/hierarchy_integration_test.go` | End-to-end tests: nested architecture event flow, multi-level, constraints |

### Modified files

| File | Change |
|------|--------|
| `arch/architecture.go` | Add `subArchitectures` map, `AddSubArchitecture()`, update `Start`/`Stop`/`Wait`/`processEventCascade` |

---

## Task 1: Participant interface

**Files:**
- Create: `arch/participant.go`
- Create: `arch/participant_test.go`

- [ ] **Step 1: Write failing test**

```go
// arch/participant_test.go
package arch

import (
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestComponentSatisfiesParticipant(t *testing.T) {
	var p Participant = NewComponent("test", Interface("I").Build(), gorapide.NewPoset())
	if p.ParticipantID() != "test" {
		t.Errorf("ParticipantID: want test, got %s", p.ParticipantID())
	}
	if p.ParticipantInterface().Name != "I" {
		t.Errorf("ParticipantInterface: want I, got %s", p.ParticipantInterface().Name)
	}
}

func TestParticipantSend(t *testing.T) {
	c := NewComponent("test", Interface("I").Build(), gorapide.NewPoset())
	var p Participant = c
	e := gorapide.NewEvent("X", "src", nil)
	ok := p.Send(e)
	if !ok {
		t.Error("Send should succeed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestComponentSatisfies|TestParticipantSend" -v`
Expected: FAIL — `Participant` undefined

- [ ] **Step 3: Write Participant interface and Component methods**

```go
// arch/participant.go
package arch

import (
	"context"

	"github.com/ShaneDolphin/gorapide"
)

// Participant is the common interface for anything that can participate
// in an architecture: plain components and nested sub-architectures.
type Participant interface {
	// ParticipantID returns the unique identifier within the parent architecture.
	ParticipantID() string

	// ParticipantInterface returns the declared interface (actions visible externally).
	ParticipantInterface() *InterfaceDecl

	// Send delivers an event to this participant's inbox.
	Send(e *gorapide.Event) bool

	// Start begins event processing.
	Start(ctx context.Context)

	// Stop signals the participant to shut down.
	Stop()

	// Wait blocks until the participant has fully stopped.
	Wait()
}

// ParticipantID returns the component's ID. Satisfies Participant.
func (c *Component) ParticipantID() string {
	return c.ID
}

// ParticipantInterface returns the component's interface declaration. Satisfies Participant.
func (c *Component) ParticipantInterface() *InterfaceDecl {
	return c.Interface
}

// Compile-time assertion that *Component satisfies Participant.
var _ Participant = (*Component)(nil)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestComponentSatisfies|TestParticipantSend" -v`
Expected: PASS

- [ ] **Step 5: Run full suite**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All PASS (105 arch tests + new ones)

- [ ] **Step 6: Commit**

```bash
git add arch/participant.go arch/participant_test.go
git commit -m "feat(arch): add Participant interface satisfied by Component"
```

---

## Task 2: SubArchitecture type and builder

**Files:**
- Create: `arch/hierarchy.go`
- Create: `arch/hierarchy_test.go`

- [ ] **Step 1: Write failing tests for SubArchitecture construction**

```go
// arch/hierarchy_test.go
package arch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

func TestSubArchitectureBuilder(t *testing.T) {
	inner := NewArchitecture("inner")
	iface := Interface("InnerFace").
		InAction("Request").
		OutAction("Response").
		Build()

	sa := WrapArchitecture("sub1", inner).
		WithInterface(iface).
		Export("worker", "Result", "Response").
		Import("Request", "worker", "Task").
		Build()

	if sa.ParticipantID() != "sub1" {
		t.Errorf("ID: want sub1, got %s", sa.ParticipantID())
	}
	if sa.ParticipantInterface().Name != "InnerFace" {
		t.Errorf("Interface: want InnerFace, got %s", sa.ParticipantInterface().Name)
	}
	if len(sa.exportRules) != 1 {
		t.Errorf("exportRules: want 1, got %d", len(sa.exportRules))
	}
	if len(sa.importRules) != 1 {
		t.Errorf("importRules: want 1, got %d", len(sa.importRules))
	}
}

func TestSubArchitectureSatisfiesParticipant(t *testing.T) {
	inner := NewArchitecture("inner")
	iface := Interface("I").Build()
	sa := WrapArchitecture("sub", inner).WithInterface(iface).Build()
	var p Participant = sa
	if p.ParticipantID() != "sub" {
		t.Errorf("ParticipantID: want sub, got %s", p.ParticipantID())
	}
}

func TestExportRuleWithTransform(t *testing.T) {
	inner := NewArchitecture("inner")
	iface := Interface("I").OutAction("Out").Build()

	sa := WrapArchitecture("sub", inner).
		WithInterface(iface).
		ExportWith("worker", "Result", "Out", func(e *gorapide.Event) map[string]any {
			return map[string]any{"mapped": true}
		}).
		Build()

	if sa.exportRules[0].Transform == nil {
		t.Error("ExportWith should set transform")
	}
}

func TestImportRuleWithTransform(t *testing.T) {
	inner := NewArchitecture("inner")
	iface := Interface("I").InAction("In").Build()

	sa := WrapArchitecture("sub", inner).
		WithInterface(iface).
		ImportWith("In", "worker", "Task", func(e *gorapide.Event) map[string]any {
			return map[string]any{"mapped": true}
		}).
		Build()

	if sa.importRules[0].Transform == nil {
		t.Error("ImportWith should set transform")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestSubArchitecture|TestExportRule|TestImportRule" -v`
Expected: FAIL — `WrapArchitecture` undefined

- [ ] **Step 3: Write SubArchitecture, rules, and builder**

```go
// arch/hierarchy.go
package arch

import (
	"context"
	"sync"

	"github.com/ShaneDolphin/gorapide"
)

// ExportRule defines how an inner event becomes visible to the parent architecture.
type ExportRule struct {
	InnerSource string                                // component ID inside sub-arch (or "*")
	InnerEvent  string                                // event name inside
	OuterEvent  string                                // event name visible to parent
	Transform   func(*gorapide.Event) map[string]any  // optional param transform
}

// ImportRule defines how a parent event is forwarded into the sub-architecture.
type ImportRule struct {
	OuterEvent  string                                // event name from parent
	InnerTarget string                                // component ID inside (or "" for Inject)
	InnerEvent  string                                // event name inside
	Transform   func(*gorapide.Event) map[string]any  // optional param transform
}

// SubArchitecture wraps an Architecture to participate as a component
// in a parent architecture. It bridges events across the hierarchy
// boundary using export and import rules.
type SubArchitecture struct {
	id          string
	iface       *InterfaceDecl
	inner       *Architecture

	exportRules []ExportRule
	importRules []ImportRule

	// Set by parent architecture during AddSubArchitecture.
	onEmit      func(*gorapide.Event)
	parentPoset *gorapide.Poset

	inbox   chan *gorapide.Event
	bufSize int

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// ParticipantID returns the sub-architecture's ID. Satisfies Participant.
func (sa *SubArchitecture) ParticipantID() string {
	return sa.id
}

// ParticipantInterface returns the sub-architecture's external interface. Satisfies Participant.
func (sa *SubArchitecture) ParticipantInterface() *InterfaceDecl {
	return sa.iface
}

// Send delivers an event to the sub-architecture's inbox for import processing.
func (sa *SubArchitecture) Send(e *gorapide.Event) bool {
	select {
	case sa.inbox <- e:
		return true
	default:
		return false
	}
}

// Start starts the inner architecture and the boundary bridge goroutine.
func (sa *SubArchitecture) Start(ctx context.Context) {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	if sa.running {
		return
	}
	sa.running = true
	sa.done = make(chan struct{})

	var bridgeCtx context.Context
	bridgeCtx, sa.cancel = context.WithCancel(ctx)

	// Install export observer on inner architecture BEFORE starting it.
	sa.inner.onEvent = append(sa.inner.onEvent, sa.handleExport)

	// Start inner architecture.
	sa.inner.Start(bridgeCtx)

	// Start import bridge goroutine.
	go sa.runImportBridge(bridgeCtx)
}

// Stop stops the inner architecture and the bridge.
func (sa *SubArchitecture) Stop() {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	if !sa.running {
		return
	}
	sa.running = false
	sa.inner.Stop()
	sa.cancel()
}

// Wait blocks until the inner architecture and bridge have stopped.
func (sa *SubArchitecture) Wait() {
	sa.inner.Wait()
	sa.mu.Lock()
	d := sa.done
	sa.mu.Unlock()
	if d != nil {
		<-d
	}
}

// handleExport is registered as a WithObserver on the inner architecture.
// When an inner event matches an export rule, it creates a new event
// visible to the parent architecture.
func (sa *SubArchitecture) handleExport(e *gorapide.Event) {
	for _, rule := range sa.exportRules {
		if rule.InnerSource != "*" && e.Source != rule.InnerSource {
			continue
		}
		if e.Name != rule.InnerEvent {
			continue
		}

		// Build params for the exported event.
		params := copyEventParams(e)
		if rule.Transform != nil {
			params = rule.Transform(e)
		}

		// Create event in parent poset.
		exported := gorapide.NewEvent(rule.OuterEvent, sa.id, params)
		if sa.parentPoset != nil {
			sa.parentPoset.AddEvent(exported)
		}

		// Notify parent router.
		if sa.onEmit != nil {
			sa.onEmit(exported)
		}
	}
}

// runImportBridge reads events from the inbox and routes them into
// the inner architecture according to import rules.
func (sa *SubArchitecture) runImportBridge(ctx context.Context) {
	defer close(sa.done)
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sa.inbox:
			if !ok {
				return
			}
			sa.processImport(e)
		}
	}
}

// processImport applies import rules to a received event.
func (sa *SubArchitecture) processImport(e *gorapide.Event) {
	for _, rule := range sa.importRules {
		if e.Name != rule.OuterEvent {
			continue
		}

		params := copyEventParams(e)
		if rule.Transform != nil {
			params = rule.Transform(e)
		}

		// Inject into inner architecture.
		sa.inner.Inject(rule.InnerEvent, params)
	}
}

// copyEventParams copies an event's params map.
func copyEventParams(e *gorapide.Event) map[string]any {
	params := make(map[string]any, len(e.Params))
	for k, v := range e.Params {
		params[k] = v
	}
	return params
}

// --- SubArchBuilder ---

// SubArchBuilder constructs a SubArchitecture using a fluent API.
type SubArchBuilder struct {
	id          string
	inner       *Architecture
	iface       *InterfaceDecl
	exportRules []ExportRule
	importRules []ImportRule
	bufSize     int
}

// WrapArchitecture starts building a SubArchitecture that wraps the given architecture.
func WrapArchitecture(id string, inner *Architecture) *SubArchBuilder {
	return &SubArchBuilder{
		id:      id,
		inner:   inner,
		bufSize: 16,
	}
}

// WithInterface sets the external interface visible to the parent.
func (b *SubArchBuilder) WithInterface(iface *InterfaceDecl) *SubArchBuilder {
	b.iface = iface
	return b
}

// Export adds an export rule: when innerSource emits innerEvent, export as outerEvent.
func (b *SubArchBuilder) Export(innerSource, innerEvent, outerEvent string) *SubArchBuilder {
	b.exportRules = append(b.exportRules, ExportRule{
		InnerSource: innerSource,
		InnerEvent:  innerEvent,
		OuterEvent:  outerEvent,
	})
	return b
}

// ExportWith adds an export rule with a parameter transform.
func (b *SubArchBuilder) ExportWith(innerSource, innerEvent, outerEvent string,
	transform func(*gorapide.Event) map[string]any) *SubArchBuilder {
	b.exportRules = append(b.exportRules, ExportRule{
		InnerSource: innerSource,
		InnerEvent:  innerEvent,
		OuterEvent:  outerEvent,
		Transform:   transform,
	})
	return b
}

// Import adds an import rule: when outerEvent arrives, inject as innerEvent to innerTarget.
func (b *SubArchBuilder) Import(outerEvent, innerTarget, innerEvent string) *SubArchBuilder {
	b.importRules = append(b.importRules, ImportRule{
		OuterEvent:  outerEvent,
		InnerTarget: innerTarget,
		InnerEvent:  innerEvent,
	})
	return b
}

// ImportWith adds an import rule with a parameter transform.
func (b *SubArchBuilder) ImportWith(outerEvent, innerTarget, innerEvent string,
	transform func(*gorapide.Event) map[string]any) *SubArchBuilder {
	b.importRules = append(b.importRules, ImportRule{
		OuterEvent:  outerEvent,
		InnerTarget: innerTarget,
		InnerEvent:  innerEvent,
		Transform:   transform,
	})
	return b
}

// WithBufferSize sets the inbox buffer size.
func (b *SubArchBuilder) WithBufferSize(n int) *SubArchBuilder {
	b.bufSize = n
	return b
}

// Build finalizes and returns the SubArchitecture.
func (b *SubArchBuilder) Build() *SubArchitecture {
	return &SubArchitecture{
		id:          b.id,
		iface:       b.iface,
		inner:       b.inner,
		exportRules: b.exportRules,
		importRules: b.importRules,
		inbox:       make(chan *gorapide.Event, b.bufSize),
		bufSize:     b.bufSize,
	}
}

// Compile-time assertion that *SubArchitecture satisfies Participant.
var _ Participant = (*SubArchitecture)(nil)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestSubArchitecture|TestExportRule|TestImportRule" -v`
Expected: PASS

- [ ] **Step 5: Run full suite**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add arch/hierarchy.go arch/hierarchy_test.go
git commit -m "feat(arch): add SubArchitecture type with export/import rules and builder"
```

---

## Task 3: Architecture integration for sub-architectures

**Files:**
- Modify: `arch/architecture.go`
- Append to: `arch/hierarchy_test.go`

- [ ] **Step 1: Write failing tests for AddSubArchitecture and lifecycle**

Append to `arch/hierarchy_test.go`:

```go
func TestArchitectureAddSubArchitecture(t *testing.T) {
	parent := NewArchitecture("parent")
	inner := NewArchitecture("inner")
	iface := Interface("I").Build()
	sa := WrapArchitecture("sub1", inner).WithInterface(iface).Build()

	err := parent.AddSubArchitecture(sa)
	if err != nil {
		t.Fatalf("AddSubArchitecture: %v", err)
	}
}

func TestArchitectureAddSubArchitectureDuplicate(t *testing.T) {
	parent := NewArchitecture("parent")
	inner := NewArchitecture("inner")
	iface := Interface("I").Build()
	sa := WrapArchitecture("sub1", inner).WithInterface(iface).Build()

	parent.AddSubArchitecture(sa)
	err := parent.AddSubArchitecture(sa)
	if err == nil {
		t.Error("duplicate sub-architecture ID should fail")
	}
}

func TestArchitectureAddSubArchitectureConflictsWithComponent(t *testing.T) {
	parent := NewArchitecture("parent")
	comp := NewComponent("sub1", Interface("I").Build(), nil)
	parent.AddComponent(comp)

	inner := NewArchitecture("inner")
	sa := WrapArchitecture("sub1", inner).WithInterface(Interface("I").Build()).Build()
	err := parent.AddSubArchitecture(sa)
	if err == nil {
		t.Error("sub-architecture ID conflicting with component should fail")
	}
}

func TestSubArchitectureLifecycle(t *testing.T) {
	parent := NewArchitecture("parent")
	inner := NewArchitecture("inner")
	iface := Interface("I").InAction("Ping").Build()

	workerIface := Interface("Worker").InAction("Task").OutAction("Result").Build()
	worker := NewComponent("worker", workerIface, nil)
	inner.AddComponent(worker)

	sa := WrapArchitecture("sub1", inner).
		WithInterface(iface).
		Import("Ping", "worker", "Task").
		Build()
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	err := parent.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Should not panic.
	parent.Stop()
	parent.Wait()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestArchitectureAddSub|TestSubArchitectureLifecycle" -v`
Expected: FAIL — `AddSubArchitecture` undefined

- [ ] **Step 3: Add sub-architecture support to Architecture**

In `arch/architecture.go`, make these changes:

Add `subArchitectures map[string]*SubArchitecture` field to the struct (after `bindings` on line 20):

```go
	subArchitectures map[string]*SubArchitecture
```

Initialize it in `NewArchitecture` (add to the struct literal):

```go
		subArchitectures: make(map[string]*SubArchitecture),
```

Add `AddSubArchitecture` method after the `Bindings()` method:

```go
// AddSubArchitecture registers a sub-architecture in the parent architecture.
// The sub-architecture's ID must not conflict with any component ID.
func (a *Architecture) AddSubArchitecture(sa *SubArchitecture) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.components[sa.id]; exists {
		return fmt.Errorf("arch: ID %q already used by a component", sa.id)
	}
	if _, exists := a.subArchitectures[sa.id]; exists {
		return fmt.Errorf("arch: sub-architecture %q already exists", sa.id)
	}
	sa.onEmit = a.notify
	sa.parentPoset = a.poset
	a.subArchitectures[sa.id] = sa
	return nil
}

// SubArchitecture looks up a sub-architecture by ID.
func (a *Architecture) SubArchitecture(id string) (*SubArchitecture, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	sa, ok := a.subArchitectures[id]
	return sa, ok
}
```

In the `Start` method, after starting all components (after line 257 `c.Start(a.ctx)`), add:

```go
	// Start all sub-architectures.
	for _, sa := range a.subArchitectures {
		sa.Start(a.ctx)
	}
```

In the `Wait` method, after waiting for all components (after the `c.Wait()` loop), add:

```go
	// Wait for all sub-architectures.
	a.mu.RLock()
	subs := make([]*SubArchitecture, 0, len(a.subArchitectures))
	for _, sa := range a.subArchitectures {
		subs = append(subs, sa)
	}
	a.mu.RUnlock()
	for _, sa := range subs {
		sa.Wait()
	}
```

In `processEventCascade`, after the dynamic bindings block and before the observer notification, add import rule evaluation:

```go
		// Evaluate sub-architecture import rules.
		a.mu.RLock()
		subs := make([]*SubArchitecture, 0, len(a.subArchitectures))
		for _, sa := range a.subArchitectures {
			subs = append(subs, sa)
		}
		a.mu.RUnlock()
		for _, sa := range subs {
			for _, rule := range sa.importRules {
				if current.Name == rule.OuterEvent {
					sa.Send(current)
					break // only need to send once per sub-arch
				}
			}
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestArchitectureAddSub|TestSubArchitectureLifecycle" -v`
Expected: PASS

- [ ] **Step 5: Run full suite**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add arch/architecture.go arch/hierarchy_test.go
git commit -m "feat(arch): integrate SubArchitecture into Architecture lifecycle and routing"
```

---

## Task 4: End-to-end integration tests

**Files:**
- Create: `arch/hierarchy_integration_test.go`

- [ ] **Step 1: Write integration tests for hierarchical event flow**

```go
// arch/hierarchy_integration_test.go
package arch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// TestHierarchyImportFlow: parent injects event, sub-architecture's inner
// component receives it via import rule.
func TestHierarchyImportFlow(t *testing.T) {
	// Inner architecture with a worker component.
	inner := NewArchitecture("inner")
	workerIface := Interface("Worker").InAction("Task").OutAction("Done").Build()
	worker := NewComponent("worker", workerIface, nil)
	inner.AddComponent(worker)

	var mu sync.Mutex
	var received []*gorapide.Event
	worker.OnEvent("Task", func(ctx BehaviorContext) {
		mu.Lock()
		received = append(received, ctx.Matched...)
		mu.Unlock()
	})

	// Wrap inner as sub-architecture.
	subIface := Interface("SubFace").InAction("Request").Build()
	sa := WrapArchitecture("sub1", inner).
		WithInterface(subIface).
		Import("Request", "worker", "Task").
		Build()

	// Parent architecture.
	parent := NewArchitecture("parent")
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	parent.Start(ctx)

	parent.Inject("Request", map[string]any{"job": "scan"})
	time.Sleep(200 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("inner worker should have received Task via import rule")
	}
}

// TestHierarchyExportFlow: inner component emits event, parent architecture
// sees it via export rule.
func TestHierarchyExportFlow(t *testing.T) {
	// Inner architecture with a worker that emits Done.
	inner := NewArchitecture("inner")
	workerIface := Interface("Worker").InAction("Task").OutAction("Done").Build()
	worker := NewComponent("worker", workerIface, nil)
	inner.AddComponent(worker)

	worker.OnEvent("Task", func(ctx BehaviorContext) {
		ctx.Emit("Done", map[string]any{"status": "ok"})
	})

	// Sub-architecture exports Done as Response.
	subIface := Interface("SubFace").InAction("Request").OutAction("Response").Build()
	sa := WrapArchitecture("sub1", inner).
		WithInterface(subIface).
		Import("Request", "worker", "Task").
		Export("worker", "Done", "Response").
		Build()

	// Parent with an observer to catch exported events.
	var mu sync.Mutex
	var observed []*gorapide.Event
	parent := NewArchitecture("parent", WithObserver(func(e *gorapide.Event) {
		mu.Lock()
		observed = append(observed, e)
		mu.Unlock()
	}))
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	parent.Start(ctx)

	parent.Inject("Request", map[string]any{"job": "scan"})
	time.Sleep(300 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Look for the exported Response event.
	found := false
	for _, e := range observed {
		if e.Name == "Response" && e.Source == "sub1" {
			found = true
			if e.ParamString("status") != "ok" {
				t.Errorf("Response status: want ok, got %s", e.ParamString("status"))
			}
		}
	}
	if !found {
		t.Error("parent should observe exported Response event from sub-architecture")
	}
}

// TestHierarchyExportWithTransform: export rule transforms params.
func TestHierarchyExportWithTransform(t *testing.T) {
	inner := NewArchitecture("inner")
	workerIface := Interface("Worker").OutAction("Raw").Build()
	worker := NewComponent("worker", workerIface, nil)
	inner.AddComponent(worker)

	subIface := Interface("SubFace").OutAction("Processed").Build()
	sa := WrapArchitecture("sub1", inner).
		WithInterface(subIface).
		ExportWith("worker", "Raw", "Processed", func(e *gorapide.Event) map[string]any {
			return map[string]any{"original": e.Name, "transformed": true}
		}).
		Build()

	var mu sync.Mutex
	var observed []*gorapide.Event
	parent := NewArchitecture("parent", WithObserver(func(e *gorapide.Event) {
		mu.Lock()
		observed = append(observed, e)
		mu.Unlock()
	}))
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	parent.Start(ctx)

	worker.Emit("Raw", map[string]any{"data": "test"})
	time.Sleep(200 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	mu.Lock()
	defer mu.Unlock()

	found := false
	for _, e := range observed {
		if e.Name == "Processed" {
			found = true
			v, ok := e.Param("transformed")
			if !ok || v != true {
				t.Error("transformed param should be true")
			}
		}
	}
	if !found {
		t.Error("parent should see Processed event with transformed params")
	}
}

// TestHierarchyExportTriggersParentConnection: exported event triggers
// a static connection in the parent architecture.
func TestHierarchyExportTriggersParentConnection(t *testing.T) {
	// Inner: worker emits Done.
	inner := NewArchitecture("inner")
	workerIface := Interface("Worker").InAction("Task").OutAction("Done").Build()
	worker := NewComponent("worker", workerIface, nil)
	inner.AddComponent(worker)
	worker.OnEvent("Task", func(ctx BehaviorContext) {
		ctx.Emit("Done", nil)
	})

	// Sub-arch exports Done as Result.
	subIface := Interface("Sub").InAction("Request").OutAction("Result").Build()
	sa := WrapArchitecture("sub1", inner).
		WithInterface(subIface).
		Import("Request", "worker", "Task").
		Export("worker", "Done", "Result").
		Build()

	// Parent: has a consumer that receives via static connection from sub1.
	consumerIface := Interface("Consumer").InAction("Outcome").Build()
	consumer := NewComponent("consumer", consumerIface, nil)

	var mu sync.Mutex
	var consumerGot []*gorapide.Event
	consumer.OnEvent("Outcome", func(ctx BehaviorContext) {
		mu.Lock()
		consumerGot = append(consumerGot, ctx.Matched...)
		mu.Unlock()
	})

	parent := NewArchitecture("parent")
	parent.AddSubArchitecture(sa)
	parent.AddComponent(consumer)
	parent.AddConnection(
		Connect("sub1", "consumer").
			On(pattern.MatchEvent("Result")).
			Pipe().
			Send("Outcome").
			Build(),
	)

	ctx := context.Background()
	parent.Start(ctx)

	parent.Inject("Request", nil)
	time.Sleep(300 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(consumerGot) == 0 {
		t.Fatal("consumer should receive Outcome via parent connection triggered by sub-arch export")
	}
}

// TestHierarchySeparatePosets: inner and parent have separate posets.
func TestHierarchySeparatePosets(t *testing.T) {
	inner := NewArchitecture("inner")
	workerIface := Interface("Worker").InAction("Task").OutAction("Done").Build()
	worker := NewComponent("worker", workerIface, nil)
	inner.AddComponent(worker)
	worker.OnEvent("Task", func(ctx BehaviorContext) {
		ctx.Emit("Done", nil)
	})

	subIface := Interface("Sub").InAction("Request").OutAction("Result").Build()
	sa := WrapArchitecture("sub1", inner).
		WithInterface(subIface).
		Import("Request", "worker", "Task").
		Export("worker", "Done", "Result").
		Build()

	parent := NewArchitecture("parent")
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	parent.Start(ctx)

	parent.Inject("Request", nil)
	time.Sleep(300 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	// Inner poset should have Task and Done events.
	innerEvents := inner.Poset().Events()
	innerNames := make(map[string]bool)
	for _, e := range innerEvents {
		innerNames[e.Name] = true
	}

	// Parent poset should have Request and Result, NOT Task or Done.
	parentEvents := parent.Poset().Events()
	parentNames := make(map[string]bool)
	for _, e := range parentEvents {
		parentNames[e.Name] = true
	}

	if !parentNames["Request"] {
		t.Error("parent poset should contain Request")
	}
	if !parentNames["Result"] {
		t.Error("parent poset should contain Result (exported)")
	}
	if parentNames["Task"] {
		t.Error("parent poset should NOT contain inner Task event")
	}
	if parentNames["Done"] {
		t.Error("parent poset should NOT contain inner Done event")
	}
}

// TestHierarchyWildcardExport: export rule with "*" matches any inner source.
func TestHierarchyWildcardExport(t *testing.T) {
	inner := NewArchitecture("inner")
	w1 := NewComponent("w1", Interface("W").OutAction("Ping").Build(), nil)
	w2 := NewComponent("w2", Interface("W").OutAction("Ping").Build(), nil)
	inner.AddComponent(w1)
	inner.AddComponent(w2)

	subIface := Interface("Sub").OutAction("Ping").Build()
	sa := WrapArchitecture("sub", inner).
		WithInterface(subIface).
		Export("*", "Ping", "Ping"). // wildcard source
		Build()

	var mu sync.Mutex
	var count int
	parent := NewArchitecture("parent", WithObserver(func(e *gorapide.Event) {
		if e.Name == "Ping" && e.Source == "sub" {
			mu.Lock()
			count++
			mu.Unlock()
		}
	}))
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	parent.Start(ctx)

	w1.Emit("Ping", nil)
	w2.Emit("Ping", nil)
	time.Sleep(200 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	mu.Lock()
	defer mu.Unlock()
	if count != 2 {
		t.Errorf("wildcard export should capture both Ping events, got %d", count)
	}
}
```

- [ ] **Step 2: Run tests**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestHierarchy" -v -race`
Expected: PASS

If any test fails, debug and fix the SubArchitecture implementation in `hierarchy.go` or the routing in `architecture.go`.

- [ ] **Step 3: Run full suite**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add arch/hierarchy_integration_test.go
git commit -m "test(arch): add hierarchical composition integration tests"
```

---

## Task 5: Hierarchical constraint composition

**Files:**
- Create: `arch/hierarchy_constraint.go`
- Create: `arch/hierarchy_constraint_test.go`

- [ ] **Step 1: Write failing tests**

```go
// arch/hierarchy_constraint_test.go
package arch

import (
	"context"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide/constraint"
)

func TestHierarchicalConstraintCheck(t *testing.T) {
	// Inner architecture with a constraint.
	inner := NewArchitecture("inner")
	worker := NewComponent("worker", Interface("W").OutAction("Done").Build(), nil)
	inner.AddComponent(worker)

	innerCS := constraint.NewConstraintSet("inner-checks")
	innerCS.Add(constraint.EventCount("Done", 1, 10))
	inner.WithConstraints(innerCS, constraint.CheckAfter)

	// Parent architecture with its own constraint.
	subIface := Interface("Sub").OutAction("Result").Build()
	sa := WrapArchitecture("sub1", inner).
		WithInterface(subIface).
		Export("worker", "Done", "Result").
		Build()

	parent := NewArchitecture("parent")
	parent.AddSubArchitecture(sa)

	parentCS := constraint.NewConstraintSet("parent-checks")
	parentCS.Add(constraint.EventCount("Result", 1, 10))
	parent.WithConstraints(parentCS, constraint.CheckAfter)

	// Run.
	ctx := context.Background()
	parent.Start(ctx)

	worker.Emit("Done", nil)
	time.Sleep(200 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	// Check both levels.
	report := CheckHierarchy(parent)
	if report.Level != "parent" {
		t.Errorf("Level: want parent, got %s", report.Level)
	}
	if len(report.Violations) != 0 {
		t.Errorf("parent violations: want 0, got %d", len(report.Violations))
	}
	if len(report.Children) != 1 {
		t.Fatalf("children: want 1, got %d", len(report.Children))
	}
	if report.Children[0].Level != "sub1/inner" {
		t.Errorf("child level: want sub1/inner, got %s", report.Children[0].Level)
	}
}

func TestHierarchicalConstraintViolation(t *testing.T) {
	inner := NewArchitecture("inner")
	worker := NewComponent("worker", Interface("W").OutAction("Done").Build(), nil)
	inner.AddComponent(worker)

	// Constraint requires at least 1 Done event — but we won't emit any.
	innerCS := constraint.NewConstraintSet("inner-checks")
	innerCS.Add(constraint.EventCount("Done", 1, 10))
	inner.WithConstraints(innerCS, constraint.CheckAfter)

	sa := WrapArchitecture("sub1", inner).
		WithInterface(Interface("Sub").Build()).
		Build()

	parent := NewArchitecture("parent")
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	parent.Start(ctx)
	parent.Stop()
	parent.Wait()

	report := CheckHierarchy(parent)
	if len(report.Children) != 1 {
		t.Fatalf("children: want 1, got %d", len(report.Children))
	}
	if len(report.Children[0].Violations) == 0 {
		t.Error("inner constraint should have violations (no Done events)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestHierarchicalConstraint" -v`
Expected: FAIL — `CheckHierarchy` undefined

- [ ] **Step 3: Write hierarchical constraint checking**

```go
// arch/hierarchy_constraint.go
package arch

import (
	"fmt"
	"strings"

	"github.com/ShaneDolphin/gorapide/constraint"
)

// HierarchicalViolationReport organizes constraint violations by hierarchy level.
type HierarchicalViolationReport struct {
	Level      string
	Violations []constraint.ConstraintViolation
	Children   []*HierarchicalViolationReport
}

// CheckHierarchy recursively checks constraints at all levels of the
// architecture hierarchy. Returns a report organized by level.
func CheckHierarchy(a *Architecture) *HierarchicalViolationReport {
	return checkLevel(a, a.Name)
}

func checkLevel(a *Architecture, prefix string) *HierarchicalViolationReport {
	report := &HierarchicalViolationReport{
		Level:      prefix,
		Violations: a.CheckConstraints(),
	}
	if report.Violations == nil {
		report.Violations = []constraint.ConstraintViolation{}
	}

	a.mu.RLock()
	subs := make([]*SubArchitecture, 0, len(a.subArchitectures))
	for _, sa := range a.subArchitectures {
		subs = append(subs, sa)
	}
	a.mu.RUnlock()

	for _, sa := range subs {
		childPrefix := fmt.Sprintf("%s/%s", prefix, sa.inner.Name)
		childReport := checkLevel(sa.inner, childPrefix)
		report.Children = append(report.Children, childReport)
	}

	return report
}

// String returns a formatted multi-level violation report.
func (r *HierarchicalViolationReport) String() string {
	var b strings.Builder
	r.writeLevel(&b, 0)
	return b.String()
}

func (r *HierarchicalViolationReport) writeLevel(b *strings.Builder, depth int) {
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s[%s] %d violations\n", indent, r.Level, len(r.Violations))
	for _, v := range r.Violations {
		fmt.Fprintf(b, "%s  - %s: %s (%s)\n", indent, v.Constraint, v.Message, v.Severity)
	}
	for _, child := range r.Children {
		child.writeLevel(b, depth+1)
	}
}

// TotalViolations returns the total number of violations across all levels.
func (r *HierarchicalViolationReport) TotalViolations() int {
	total := len(r.Violations)
	for _, child := range r.Children {
		total += child.TotalViolations()
	}
	return total
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -run "TestHierarchicalConstraint" -v`
Expected: PASS

- [ ] **Step 5: Run full suite**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add arch/hierarchy_constraint.go arch/hierarchy_constraint_test.go
git commit -m "feat(arch): add hierarchical constraint checking across architecture levels"
```

---

## Task 6: Final verification

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race -count=1 ./...`
Expected: All PASS

- [ ] **Step 2: Run go vet**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go vet ./...`
Expected: No issues

- [ ] **Step 3: Verify test counts**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./arch/ -v 2>&1 | grep -c "^--- PASS"`
Expected: Previous 105 + new participant/hierarchy tests

---

## Verification

1. `go test -race ./arch/ -run TestComponentSatisfies` — Component satisfies Participant
2. `go test -race ./arch/ -run TestSubArchitecture` — SubArchitecture construction
3. `go test -race ./arch/ -run TestHierarchyImport` — parent→child event flow
4. `go test -race ./arch/ -run TestHierarchyExport` — child→parent event flow
5. `go test -race ./arch/ -run TestHierarchySeparatePosets` — poset isolation
6. `go test -race ./arch/ -run TestHierarchicalConstraint` — multi-level constraint checking
7. `go test -race ./...` — no regressions across all packages
