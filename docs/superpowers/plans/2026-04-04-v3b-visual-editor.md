# V3-B: Visual Architecture Editor and Simulation Playback

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a web-based visual tool for designing GoRapide architectures, running live simulations with event streaming, and replaying recorded sessions.

**Architecture:** A `studio/` package (pure Go, no deps) provides schema types, reconstruction from JSON to live Architecture, event recording, and replay state machine. A `cmd/rapide-studio/` binary (separate go.mod with `golang.org/x/net`) serves an HTTP/WS server. The frontend uses vendored Cytoscape.js + vanilla TypeScript compiled via esbuild (go:generate). Phased delivery: static editor → live simulation → recording/replay → polish.

**Tech Stack:** Go 1.22, golang.org/x/net/websocket, Cytoscape.js, TypeScript via esbuild

---

## File Structure

### studio/ package (pure Go, no external deps)

| File | Responsibility |
|------|---------------|
| `studio/schema.go` | ArchitectureSchema, ComponentSchema, ConnectionSchema, ConstraintSchema, Position, Validate() |
| `studio/schema_test.go` | JSON round-trip, validation tests |
| `studio/reconstruct.go` | Reconstruct(schema) → *arch.Architecture |
| `studio/reconstruct_test.go` | Reconstruction + event propagation tests |
| `studio/recorder.go` | RecordedEvent, Recorder, Observer() callback |
| `studio/recorder_test.go` | Recorder tests |
| `studio/replay.go` | ReplayMachine (play/pause/stop/seek/speed) |
| `studio/replay_test.go` | Replay state machine tests |

### cmd/rapide-studio/ (separate go.mod)

| File | Responsibility |
|------|---------------|
| `cmd/rapide-studio/go.mod` | Separate module requiring golang.org/x/net |
| `cmd/rapide-studio/main.go` | Entry point, flag parsing, server startup |
| `cmd/rapide-studio/server.go` | Route registration, middleware, session state |
| `cmd/rapide-studio/handlers.go` | REST CRUD + simulation + replay endpoints |
| `cmd/rapide-studio/ws.go` | WebSocket hub, client management, broadcasting |
| `cmd/rapide-studio/static.go` | go:embed + go:generate esbuild directive |
| `cmd/rapide-studio/static/index.html` | SPA shell |
| `cmd/rapide-studio/static/app.ts` | TypeScript source |
| `cmd/rapide-studio/static/style.css` | Stylesheet |
| `cmd/rapide-studio/static/lib/cytoscape.min.js` | Vendored Cytoscape.js |

---

## Phase 1: Studio Package + Static Editor

### Task 1: Architecture JSON schema

**Files:**
- Create: `studio/schema.go`
- Create: `studio/schema_test.go`

- [ ] **Step 1: Write failing tests**

```go
// studio/schema_test.go
package studio

import (
	"encoding/json"
	"testing"
)

func TestSchemaJSONRoundTrip(t *testing.T) {
	schema := &ArchitectureSchema{
		Name: "test-arch",
		Components: []ComponentSchema{
			{
				ID: "scanner",
				Interface: InterfaceSchema{
					Name: "Scanner",
					Actions: []ActionSchema{
						{Name: "VulnFound", Kind: "out"},
					},
				},
			},
			{
				ID: "aggregator",
				Interface: InterfaceSchema{
					Name: "Aggregator",
					Actions: []ActionSchema{
						{Name: "Finding", Kind: "in"},
					},
				},
			},
		},
		Connections: []ConnectionSchema{
			{
				From:       "scanner",
				To:         "aggregator",
				Kind:       "pipe",
				Trigger:    "VulnFound",
				ActionName: "Finding",
			},
		},
		Layout: map[string]Position{
			"scanner":    {X: 100, Y: 100},
			"aggregator": {X: 300, Y: 100},
		},
	}

	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var restored ArchitectureSchema
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if restored.Name != "test-arch" {
		t.Errorf("Name: want test-arch, got %s", restored.Name)
	}
	if len(restored.Components) != 2 {
		t.Errorf("Components: want 2, got %d", len(restored.Components))
	}
	if len(restored.Connections) != 1 {
		t.Errorf("Connections: want 1, got %d", len(restored.Connections))
	}
	if restored.Layout["scanner"].X != 100 {
		t.Errorf("Layout scanner X: want 100, got %f", restored.Layout["scanner"].X)
	}
}

func TestSchemaValidateSuccess(t *testing.T) {
	schema := &ArchitectureSchema{
		Name: "valid",
		Components: []ComponentSchema{
			{ID: "A", Interface: InterfaceSchema{Name: "I"}},
			{ID: "B", Interface: InterfaceSchema{Name: "I"}},
		},
		Connections: []ConnectionSchema{
			{From: "A", To: "B", Kind: "pipe", ActionName: "X"},
		},
	}
	if err := schema.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestSchemaValidateEmptyName(t *testing.T) {
	schema := &ArchitectureSchema{}
	if err := schema.Validate(); err == nil {
		t.Error("empty name should fail validation")
	}
}

func TestSchemaValidateDuplicateComponentID(t *testing.T) {
	schema := &ArchitectureSchema{
		Name: "test",
		Components: []ComponentSchema{
			{ID: "A", Interface: InterfaceSchema{Name: "I"}},
			{ID: "A", Interface: InterfaceSchema{Name: "I"}},
		},
	}
	if err := schema.Validate(); err == nil {
		t.Error("duplicate component ID should fail")
	}
}

func TestSchemaValidateBadConnectionRef(t *testing.T) {
	schema := &ArchitectureSchema{
		Name: "test",
		Components: []ComponentSchema{
			{ID: "A", Interface: InterfaceSchema{Name: "I"}},
		},
		Connections: []ConnectionSchema{
			{From: "A", To: "MISSING", Kind: "pipe", ActionName: "X"},
		},
	}
	if err := schema.Validate(); err == nil {
		t.Error("connection to missing component should fail")
	}
}

func TestSchemaValidateBadConnectionKind(t *testing.T) {
	schema := &ArchitectureSchema{
		Name: "test",
		Components: []ComponentSchema{
			{ID: "A", Interface: InterfaceSchema{Name: "I"}},
			{ID: "B", Interface: InterfaceSchema{Name: "I"}},
		},
		Connections: []ConnectionSchema{
			{From: "A", To: "B", Kind: "invalid", ActionName: "X"},
		},
	}
	if err := schema.Validate(); err == nil {
		t.Error("invalid connection kind should fail")
	}
}

func TestSchemaWithConstraints(t *testing.T) {
	schema := &ArchitectureSchema{
		Name: "test",
		Components: []ComponentSchema{
			{ID: "A", Interface: InterfaceSchema{Name: "I"}},
		},
		Constraints: []ConstraintSchema{
			{Name: "count", Kind: "event_count", Severity: "error", Args: map[string]any{"event": "X", "min": 1, "max": 10}},
		},
	}
	if err := schema.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./studio/ -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Write schema types**

```go
// studio/schema.go
package studio

import "fmt"

// ArchitectureSchema is the JSON-serializable definition of an architecture
// for the visual editor. It captures structure without Go runtime constructs.
type ArchitectureSchema struct {
	Name        string              `json:"name"`
	Components  []ComponentSchema   `json:"components"`
	Connections []ConnectionSchema  `json:"connections"`
	Constraints []ConstraintSchema  `json:"constraints,omitempty"`
	Layout      map[string]Position `json:"layout,omitempty"`
}

// ComponentSchema defines a component for the editor.
type ComponentSchema struct {
	ID        string          `json:"id"`
	Interface InterfaceSchema `json:"interface"`
}

// InterfaceSchema defines a component's interface.
type InterfaceSchema struct {
	Name     string          `json:"name"`
	Actions  []ActionSchema  `json:"actions,omitempty"`
	Services []ServiceSchema `json:"services,omitempty"`
}

// ActionSchema defines an action on an interface.
type ActionSchema struct {
	Name   string        `json:"name"`
	Kind   string        `json:"kind"` // "in" or "out"
	Params []ParamSchema `json:"params,omitempty"`
}

// ParamSchema defines a parameter on an action.
type ParamSchema struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ServiceSchema groups related actions.
type ServiceSchema struct {
	Name    string         `json:"name"`
	Actions []ActionSchema `json:"actions"`
}

// ConnectionSchema defines a connection between components.
type ConnectionSchema struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Kind       string `json:"kind"`        // "basic", "pipe", "agent"
	Trigger    string `json:"trigger,omitempty"`
	ActionName string `json:"action_name"`
}

// ConstraintSchema defines a constraint.
type ConstraintSchema struct {
	Name     string         `json:"name"`
	Kind     string         `json:"kind"`     // "event_count", "causal_depth_max", etc.
	Severity string         `json:"severity"` // "error", "warning", "info"
	Args     map[string]any `json:"args,omitempty"`
}

// Position represents a 2D canvas position.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

var validConnectionKinds = map[string]bool{
	"basic": true, "pipe": true, "agent": true,
}

// Validate checks the schema for structural errors.
func (s *ArchitectureSchema) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("studio: architecture name is required")
	}

	ids := make(map[string]bool)
	for _, c := range s.Components {
		if c.ID == "" {
			return fmt.Errorf("studio: component ID is required")
		}
		if ids[c.ID] {
			return fmt.Errorf("studio: duplicate component ID %q", c.ID)
		}
		ids[c.ID] = true
	}

	for _, conn := range s.Connections {
		if !validConnectionKinds[conn.Kind] {
			return fmt.Errorf("studio: invalid connection kind %q", conn.Kind)
		}
		if conn.From != "*" && !ids[conn.From] {
			return fmt.Errorf("studio: connection from unknown component %q", conn.From)
		}
		if conn.To != "*" && !ids[conn.To] {
			return fmt.Errorf("studio: connection to unknown component %q", conn.To)
		}
	}

	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./studio/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add studio/
git commit -m "feat(studio): add architecture JSON schema types with validation"
```

---

### Task 2: Reconstruct schema to live Architecture

**Files:**
- Create: `studio/reconstruct.go`
- Create: `studio/reconstruct_test.go`

- [ ] **Step 1: Write failing tests**

```go
// studio/reconstruct_test.go
package studio

import (
	"context"
	"testing"
	"time"
)

func TestReconstructBasic(t *testing.T) {
	schema := &ArchitectureSchema{
		Name: "test",
		Components: []ComponentSchema{
			{ID: "A", Interface: InterfaceSchema{Name: "IA", Actions: []ActionSchema{{Name: "X", Kind: "out"}}}},
			{ID: "B", Interface: InterfaceSchema{Name: "IB", Actions: []ActionSchema{{Name: "X", Kind: "in"}}}},
		},
		Connections: []ConnectionSchema{
			{From: "A", To: "B", Kind: "pipe", Trigger: "X", ActionName: "X"},
		},
	}

	a, err := Reconstruct(schema)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if a.Name != "test" {
		t.Errorf("Name: want test, got %s", a.Name)
	}

	comps := a.Components()
	if len(comps) != 2 {
		t.Errorf("Components: want 2, got %d", len(comps))
	}
}

func TestReconstructEventPropagation(t *testing.T) {
	schema := &ArchitectureSchema{
		Name: "test",
		Components: []ComponentSchema{
			{ID: "producer", Interface: InterfaceSchema{Name: "P", Actions: []ActionSchema{{Name: "Data", Kind: "out"}}}},
			{ID: "consumer", Interface: InterfaceSchema{Name: "C", Actions: []ActionSchema{{Name: "Data", Kind: "in"}}}},
		},
		Connections: []ConnectionSchema{
			{From: "producer", To: "consumer", Kind: "pipe", Trigger: "Data", ActionName: "Data"},
		},
	}

	a, err := Reconstruct(schema)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}

	ctx := context.Background()
	a.Start(ctx)

	a.Inject("Data", map[string]any{"val": 42})
	time.Sleep(100 * time.Millisecond)

	a.Stop()
	a.Wait()

	// Poset should have at least the injected event.
	if a.Poset().Len() < 1 {
		t.Error("poset should have events after injection")
	}
}

func TestReconstructConnectionKinds(t *testing.T) {
	for _, kind := range []string{"basic", "pipe", "agent"} {
		schema := &ArchitectureSchema{
			Name: "test",
			Components: []ComponentSchema{
				{ID: "A", Interface: InterfaceSchema{Name: "I"}},
				{ID: "B", Interface: InterfaceSchema{Name: "I"}},
			},
			Connections: []ConnectionSchema{
				{From: "A", To: "B", Kind: kind, ActionName: "X"},
			},
		}
		_, err := Reconstruct(schema)
		if err != nil {
			t.Errorf("Reconstruct with kind %q: %v", kind, err)
		}
	}
}

func TestReconstructInvalid(t *testing.T) {
	schema := &ArchitectureSchema{} // no name
	_, err := Reconstruct(schema)
	if err == nil {
		t.Error("Reconstruct should fail for invalid schema")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./studio/ -run "TestReconstruct" -v`
Expected: FAIL — `Reconstruct` undefined

- [ ] **Step 3: Write Reconstruct**

```go
// studio/reconstruct.go
package studio

import (
	"fmt"

	"github.com/ShaneDolphin/gorapide/arch"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// Reconstruct converts an ArchitectureSchema into a live arch.Architecture.
// Behaviors are not reconstructed (Phase 1 relies on connection-defined data flow).
func Reconstruct(schema *ArchitectureSchema) (*arch.Architecture, error) {
	if err := schema.Validate(); err != nil {
		return nil, fmt.Errorf("studio.Reconstruct: %w", err)
	}

	a := arch.NewArchitecture(schema.Name)

	// Build components.
	for _, cs := range schema.Components {
		iface := buildInterface(cs.Interface)
		comp := arch.NewComponent(cs.ID, iface, nil)
		if err := a.AddComponent(comp); err != nil {
			return nil, fmt.Errorf("studio.Reconstruct: adding component %q: %w", cs.ID, err)
		}
	}

	// Build connections.
	for i, cs := range schema.Connections {
		conn, err := buildConnection(cs)
		if err != nil {
			return nil, fmt.Errorf("studio.Reconstruct: connection %d: %w", i, err)
		}
		if err := a.AddConnection(conn); err != nil {
			return nil, fmt.Errorf("studio.Reconstruct: adding connection %d: %w", i, err)
		}
	}

	return a, nil
}

func buildInterface(is InterfaceSchema) *arch.InterfaceDecl {
	builder := arch.Interface(is.Name)
	for _, action := range is.Actions {
		params := make([]arch.ParamDecl, len(action.Params))
		for i, p := range action.Params {
			params[i] = arch.P(p.Name, p.Type)
		}
		switch action.Kind {
		case "in":
			builder.InAction(action.Name, params...)
		case "out":
			builder.OutAction(action.Name, params...)
		default:
			builder.OutAction(action.Name, params...)
		}
	}
	return builder.Build()
}

func buildConnection(cs ConnectionSchema) (*arch.Connection, error) {
	builder := arch.Connect(cs.From, cs.To)

	if cs.Trigger != "" {
		builder.On(pattern.MatchEvent(cs.Trigger))
	}

	switch cs.Kind {
	case "pipe":
		builder.Pipe()
	case "agent":
		builder.Agent()
	case "basic":
		// BasicConnection is default
	default:
		return nil, fmt.Errorf("unknown connection kind %q", cs.Kind)
	}

	builder.Send(cs.ActionName)
	return builder.Build(), nil
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./studio/ -run "TestReconstruct" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add studio/reconstruct.go studio/reconstruct_test.go
git commit -m "feat(studio): add Reconstruct to convert schema to live Architecture"
```

---

### Task 3: Event recorder

**Files:**
- Create: `studio/recorder.go`
- Create: `studio/recorder_test.go`

- [ ] **Step 1: Write failing tests**

```go
// studio/recorder_test.go
package studio

import (
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

func TestRecorderCapturesEvents(t *testing.T) {
	r := NewRecorder()
	obs := r.Observer()

	e1 := gorapide.NewEvent("A", "src", nil)
	e2 := gorapide.NewEvent("B", "src", nil)

	obs(e1)
	time.Sleep(10 * time.Millisecond)
	obs(e2)

	events := r.Events()
	if len(events) != 2 {
		t.Fatalf("Events: want 2, got %d", len(events))
	}
	if events[0].Event.Name != "A" {
		t.Errorf("first event: want A, got %s", events[0].Event.Name)
	}
	if events[1].Event.Name != "B" {
		t.Errorf("second event: want B, got %s", events[1].Event.Name)
	}
	if events[0].SeqNum != 0 || events[1].SeqNum != 1 {
		t.Errorf("SeqNum: want 0,1 got %d,%d", events[0].SeqNum, events[1].SeqNum)
	}
	if events[1].OffsetMs <= 0 {
		t.Error("second event should have positive offset from first")
	}
}

func TestRecorderReset(t *testing.T) {
	r := NewRecorder()
	obs := r.Observer()
	obs(gorapide.NewEvent("A", "src", nil))

	if len(r.Events()) != 1 {
		t.Fatal("should have 1 event")
	}

	r.Reset()
	if len(r.Events()) != 0 {
		t.Error("Reset should clear events")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./studio/ -run "TestRecorder" -v`
Expected: FAIL — `NewRecorder` undefined

- [ ] **Step 3: Write Recorder**

```go
// studio/recorder.go
package studio

import (
	"sync"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

// RecordedEvent captures an event with timing metadata for replay.
type RecordedEvent struct {
	Event    *gorapide.Event `json:"event"`
	OffsetMs int64           `json:"offset_ms"` // ms since recording start
	SeqNum   int             `json:"seq_num"`
}

// Recorder captures events from a running architecture for later replay.
type Recorder struct {
	events    []RecordedEvent
	startTime time.Time
	started   bool
	seqNum    int
	mu        sync.Mutex
}

// NewRecorder creates an empty Recorder.
func NewRecorder() *Recorder {
	return &Recorder{}
}

// Observer returns a callback function compatible with arch.WithObserver.
func (r *Recorder) Observer() func(*gorapide.Event) {
	return func(e *gorapide.Event) {
		r.mu.Lock()
		defer r.mu.Unlock()

		if !r.started {
			r.startTime = time.Now()
			r.started = true
		}

		offset := time.Since(r.startTime).Milliseconds()
		r.events = append(r.events, RecordedEvent{
			Event:    e,
			OffsetMs: offset,
			SeqNum:   r.seqNum,
		})
		r.seqNum++
	}
}

// Events returns a copy of all recorded events.
func (r *Recorder) Events() []RecordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]RecordedEvent, len(r.events))
	copy(result, r.events)
	return result
}

// Reset clears all recorded events.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
	r.seqNum = 0
	r.started = false
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./studio/ -run "TestRecorder" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add studio/recorder.go studio/recorder_test.go
git commit -m "feat(studio): add event Recorder with Observer callback"
```

---

### Task 4: Replay state machine

**Files:**
- Create: `studio/replay.go`
- Create: `studio/replay_test.go`

- [ ] **Step 1: Write failing tests**

```go
// studio/replay_test.go
package studio

import (
	"sync"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

func TestReplayMachinePlay(t *testing.T) {
	events := []RecordedEvent{
		{Event: gorapide.NewEvent("A", "src", nil), OffsetMs: 0, SeqNum: 0},
		{Event: gorapide.NewEvent("B", "src", nil), OffsetMs: 50, SeqNum: 1},
		{Event: gorapide.NewEvent("C", "src", nil), OffsetMs: 100, SeqNum: 2},
	}

	rm := NewReplayMachine(events)

	var mu sync.Mutex
	var played []string
	rm.OnEvent(func(re RecordedEvent) {
		mu.Lock()
		played = append(played, re.Event.Name)
		mu.Unlock()
	})

	rm.SetSpeed(10.0) // 10x speed for fast test
	rm.Play()
	time.Sleep(200 * time.Millisecond)
	rm.Stop()

	mu.Lock()
	defer mu.Unlock()
	if len(played) != 3 {
		t.Errorf("want 3 events played, got %d", len(played))
	}
}

func TestReplayMachinePause(t *testing.T) {
	events := []RecordedEvent{
		{Event: gorapide.NewEvent("A", "src", nil), OffsetMs: 0, SeqNum: 0},
		{Event: gorapide.NewEvent("B", "src", nil), OffsetMs: 500, SeqNum: 1}, // 500ms delay
	}

	rm := NewReplayMachine(events)
	rm.SetSpeed(1.0)

	var mu sync.Mutex
	var played []string
	rm.OnEvent(func(re RecordedEvent) {
		mu.Lock()
		played = append(played, re.Event.Name)
		mu.Unlock()
	})

	rm.Play()
	time.Sleep(100 * time.Millisecond) // A should have played
	rm.Pause()
	time.Sleep(600 * time.Millisecond) // Wait past B's offset

	mu.Lock()
	count := len(played)
	mu.Unlock()

	if count != 1 {
		t.Errorf("only A should have played during pause, got %d events", count)
	}
	rm.Stop()
}

func TestReplayMachineTotal(t *testing.T) {
	events := []RecordedEvent{
		{Event: gorapide.NewEvent("A", "src", nil), SeqNum: 0},
		{Event: gorapide.NewEvent("B", "src", nil), SeqNum: 1},
	}
	rm := NewReplayMachine(events)
	if rm.Total() != 2 {
		t.Errorf("Total: want 2, got %d", rm.Total())
	}
}

func TestReplayMachineStopIdempotent(t *testing.T) {
	rm := NewReplayMachine(nil)
	rm.Stop()
	rm.Stop() // should not panic
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./studio/ -run "TestReplayMachine" -v`
Expected: FAIL — `NewReplayMachine` undefined

- [ ] **Step 3: Write ReplayMachine**

```go
// studio/replay.go
package studio

import (
	"sync"
	"time"
)

// ReplayState represents the replay machine's state.
type ReplayState int

const (
	ReplayStopped ReplayState = iota
	ReplayPlaying
	ReplayPaused
)

// ReplayMachine steps through recorded events at configurable speed.
type ReplayMachine struct {
	events  []RecordedEvent
	index   int
	state   ReplayState
	speed   float64
	onEvent func(RecordedEvent)

	mu     sync.Mutex
	stopCh chan struct{}
}

// NewReplayMachine creates a replay machine for the given events.
func NewReplayMachine(events []RecordedEvent) *ReplayMachine {
	return &ReplayMachine{
		events: events,
		speed:  1.0,
	}
}

// OnEvent sets the callback invoked for each replayed event.
func (rm *ReplayMachine) OnEvent(fn func(RecordedEvent)) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.onEvent = fn
}

// SetSpeed sets the replay speed multiplier (1.0 = real-time).
func (rm *ReplayMachine) SetSpeed(s float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if s <= 0 {
		s = 1.0
	}
	rm.speed = s
}

// Play starts or resumes replay.
func (rm *ReplayMachine) Play() {
	rm.mu.Lock()
	if rm.state == ReplayPlaying {
		rm.mu.Unlock()
		return
	}
	rm.state = ReplayPlaying
	rm.stopCh = make(chan struct{})
	index := rm.index
	speed := rm.speed
	fn := rm.onEvent
	events := rm.events
	stopCh := rm.stopCh
	rm.mu.Unlock()

	go rm.runPlayback(events, index, speed, fn, stopCh)
}

// Pause pauses replay.
func (rm *ReplayMachine) Pause() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.state != ReplayPlaying {
		return
	}
	rm.state = ReplayPaused
	if rm.stopCh != nil {
		close(rm.stopCh)
		rm.stopCh = nil
	}
}

// Stop stops replay and resets position.
func (rm *ReplayMachine) Stop() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.state = ReplayStopped
	rm.index = 0
	if rm.stopCh != nil {
		close(rm.stopCh)
		rm.stopCh = nil
	}
}

// State returns the current replay state.
func (rm *ReplayMachine) State() ReplayState {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.state
}

// Current returns the current event index.
func (rm *ReplayMachine) Current() int {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.index
}

// Total returns the total number of events.
func (rm *ReplayMachine) Total() int {
	return len(rm.events)
}

func (rm *ReplayMachine) runPlayback(events []RecordedEvent, startIndex int, speed float64, fn func(RecordedEvent), stopCh chan struct{}) {
	for i := startIndex; i < len(events); i++ {
		// Calculate delay.
		if i > startIndex {
			deltaMs := events[i].OffsetMs - events[i-1].OffsetMs
			if deltaMs > 0 {
				delay := time.Duration(float64(deltaMs)/speed) * time.Millisecond
				select {
				case <-stopCh:
					rm.mu.Lock()
					rm.index = i
					rm.mu.Unlock()
					return
				case <-time.After(delay):
				}
			}
		}

		// Check if stopped.
		select {
		case <-stopCh:
			rm.mu.Lock()
			rm.index = i
			rm.mu.Unlock()
			return
		default:
		}

		if fn != nil {
			fn(events[i])
		}

		rm.mu.Lock()
		rm.index = i + 1
		rm.mu.Unlock()
	}

	// Replay finished naturally.
	rm.mu.Lock()
	rm.state = ReplayStopped
	rm.index = len(events)
	rm.mu.Unlock()
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./studio/ -run "TestReplayMachine" -v -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add studio/replay.go studio/replay_test.go
git commit -m "feat(studio): add ReplayMachine with play/pause/stop and speed control"
```

---

## Phase 2: HTTP Server + WebSocket + Frontend

### Task 5: cmd/rapide-studio module setup and HTTP server

**Files:**
- Create: `cmd/rapide-studio/go.mod`
- Create: `cmd/rapide-studio/main.go`
- Create: `cmd/rapide-studio/server.go`
- Create: `cmd/rapide-studio/handlers.go`

This task creates the HTTP server skeleton with REST endpoints for architecture CRUD and simulation control. The frontend (Task 7) and WebSocket (Task 6) come next.

- [ ] **Step 1: Create go.mod**

```bash
mkdir -p cmd/rapide-studio/static/lib
```

```
module github.com/ShaneDolphin/gorapide/cmd/rapide-studio

go 1.22

require (
    github.com/ShaneDolphin/gorapide v0.0.0
    golang.org/x/net v0.33.0
)

replace github.com/ShaneDolphin/gorapide => ../../
```

Run `cd cmd/rapide-studio && go mod tidy` to resolve versions.

- [ ] **Step 2: Write server.go**

```go
// cmd/rapide-studio/server.go
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/ShaneDolphin/gorapide/arch"
	"github.com/ShaneDolphin/gorapide/studio"
)

// Session holds the current server state.
type Session struct {
	mu           sync.RWMutex
	schemas      map[string]*studio.ArchitectureSchema // id -> schema
	nextID       int
	architecture *arch.Architecture // currently running sim
	recorder     *studio.Recorder
	simCancel    context.CancelFunc
	hub          *Hub
}

func newSession() *Session {
	return &Session{
		schemas: make(map[string]*studio.ArchitectureSchema),
		hub:     newHub(),
	}
}

func (s *Session) registerRoutes(mux *http.ServeMux) {
	// Architecture CRUD
	mux.HandleFunc("GET /api/architectures", s.listArchitectures)
	mux.HandleFunc("POST /api/architectures", s.createArchitecture)
	mux.HandleFunc("GET /api/architectures/{id}", s.getArchitecture)
	mux.HandleFunc("PUT /api/architectures/{id}", s.updateArchitecture)
	mux.HandleFunc("DELETE /api/architectures/{id}", s.deleteArchitecture)

	// Simulation
	mux.HandleFunc("POST /api/simulate/start/{id}", s.startSimulation)
	mux.HandleFunc("POST /api/simulate/stop", s.stopSimulation)
	mux.HandleFunc("POST /api/simulate/inject", s.injectEvent)

	// WebSocket
	mux.HandleFunc("GET /ws", s.handleWS)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
```

- [ ] **Step 3: Write handlers.go**

```go
// cmd/rapide-studio/handlers.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/studio"
)

func (s *Session) listArchitectures(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type entry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var list []entry
	for id, schema := range s.schemas {
		list = append(list, entry{ID: id, Name: schema.Name})
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Session) createArchitecture(w http.ResponseWriter, r *http.Request) {
	var schema studio.ArchitectureSchema
	if err := readJSON(r, &schema); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := schema.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("arch-%d", s.nextID)
	s.schemas[id] = &schema
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Session) getArchitecture(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.RLock()
	schema, ok := s.schemas[id]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

func (s *Session) updateArchitecture(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.RLock()
	_, ok := s.schemas[id]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var schema studio.ArchitectureSchema
	if err := readJSON(r, &schema); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := schema.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.schemas[id] = &schema
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Session) deleteArchitecture(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	delete(s.schemas, id)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Session) startSimulation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.RLock()
	schema, ok := s.schemas[id]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Stop any running simulation.
	s.stopCurrentSim()

	// Reconstruct and start.
	rec := studio.NewRecorder()
	a, err := studio.Reconstruct(schema)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Register observers: recorder + WS broadcast.
	a = a // re-assign to satisfy compiler
	// We need to add observers before Start. Unfortunately Reconstruct
	// doesn't expose this. Workaround: inject directly.
	// The observer broadcasts events to WebSocket clients.
	hub := s.hub
	observerFn := func(e *gorapide.Event) {
		rec.Observer()(e)
		data, _ := json.Marshal(e)
		msg, _ := json.Marshal(WSMessage{Type: "event", Data: data})
		hub.broadcast <- msg
	}

	// We need to use WithObserver, but Architecture is already created.
	// Add observer manually via the architecture's exported field.
	// Since we can't modify Architecture after creation through the public API,
	// we re-create with the observer.
	a2, err := studio.ReconstructWithObserver(schema, observerFn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	a2.Start(ctx)

	s.mu.Lock()
	s.architecture = a2
	s.recorder = rec
	s.simCancel = cancel
	s.mu.Unlock()

	msg, _ := json.Marshal(WSMessage{Type: "sim_started", Data: json.RawMessage(`{}`)})
	hub.broadcast <- msg

	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (s *Session) stopSimulation(w http.ResponseWriter, r *http.Request) {
	s.stopCurrentSim()
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Session) stopCurrentSim() {
	s.mu.Lock()
	a := s.architecture
	cancel := s.simCancel
	s.architecture = nil
	s.simCancel = nil
	s.mu.Unlock()

	if a != nil {
		a.Stop()
		a.Wait()
	}
	if cancel != nil {
		cancel()
	}

	msg, _ := json.Marshal(WSMessage{Type: "sim_stopped", Data: json.RawMessage(`{}`)})
	s.hub.broadcast <- msg
}

func (s *Session) injectEvent(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	a := s.architecture
	s.mu.RUnlock()
	if a == nil {
		http.Error(w, "no simulation running", http.StatusBadRequest)
		return
	}

	var req struct {
		Name   string         `json:"name"`
		Params map[string]any `json:"params"`
	}
	if err := readJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	a.Inject(req.Name, req.Params)
	writeJSON(w, http.StatusOK, map[string]string{"status": "injected"})
}
```

- [ ] **Step 4: Write main.go**

```go
// cmd/rapide-studio/main.go
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", ":8400", "listen address")
	flag.Parse()

	session := newSession()
	go session.hub.run()

	mux := http.NewServeMux()
	session.registerRoutes(mux)

	// Serve static files.
	mux.Handle("GET /", http.FileServer(http.Dir("static")))

	fmt.Printf("rapide-studio listening on %s\n", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
```

- [ ] **Step 5: Add ReconstructWithObserver to studio/reconstruct.go**

Append to `studio/reconstruct.go`:

```go
// ReconstructWithObserver is like Reconstruct but registers a global event observer.
func ReconstructWithObserver(schema *ArchitectureSchema, observer func(*gorapide.Event)) (*arch.Architecture, error) {
	if err := schema.Validate(); err != nil {
		return nil, fmt.Errorf("studio.Reconstruct: %w", err)
	}

	a := arch.NewArchitecture(schema.Name, arch.WithObserver(observer))

	for _, cs := range schema.Components {
		iface := buildInterface(cs.Interface)
		comp := arch.NewComponent(cs.ID, iface, nil)
		if err := a.AddComponent(comp); err != nil {
			return nil, fmt.Errorf("studio.Reconstruct: adding component %q: %w", cs.ID, err)
		}
	}

	for i, cs := range schema.Connections {
		conn, err := buildConnection(cs)
		if err != nil {
			return nil, fmt.Errorf("studio.Reconstruct: connection %d: %w", i, err)
		}
		if err := a.AddConnection(conn); err != nil {
			return nil, fmt.Errorf("studio.Reconstruct: adding connection %d: %w", i, err)
		}
	}

	return a, nil
}
```

Add import for gorapide in reconstruct.go:

```go
import (
	"fmt"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
	"github.com/ShaneDolphin/gorapide/pattern"
)
```

- [ ] **Step 6: Compile to verify**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/cmd/rapide-studio && go mod tidy && go build ./...`
Expected: Compiles successfully

- [ ] **Step 7: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add cmd/rapide-studio/ studio/reconstruct.go
git commit -m "feat(studio): add HTTP server with REST endpoints and simulation control"
```

---

### Task 6: WebSocket hub

**Files:**
- Create: `cmd/rapide-studio/ws.go`

- [ ] **Step 1: Write WebSocket hub and session handler**

```go
// cmd/rapide-studio/ws.go
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"

	"golang.org/x/net/websocket"
)

// WSMessage is the JSON envelope for all WebSocket messages.
type WSMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Hub manages WebSocket connections and broadcasts.
type Hub struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan []byte
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	mu         sync.RWMutex
}

func newHub() *Hub {
	return &Hub{
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
	}
}

func (h *Hub) run() {
	for {
		select {
		case conn := <-h.register:
			h.mu.Lock()
			h.clients[conn] = true
			h.mu.Unlock()
		case conn := <-h.unregister:
			h.mu.Lock()
			delete(h.clients, conn)
			h.mu.Unlock()
			conn.Close()
		case msg := <-h.broadcast:
			h.mu.RLock()
			for conn := range h.clients {
				if _, err := conn.Write(msg); err != nil {
					go func(c *websocket.Conn) {
						h.unregister <- c
					}(conn)
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (s *Session) handleWS(w http.ResponseWriter, r *http.Request) {
	wsHandler := websocket.Handler(func(conn *websocket.Conn) {
		s.hub.register <- conn
		defer func() { s.hub.unregister <- conn }()

		// Read messages from client.
		for {
			var msg WSMessage
			if err := websocket.JSON.Receive(conn, &msg); err != nil {
				if err != io.EOF {
					log.Printf("ws read error: %v", err)
				}
				return
			}
			s.handleWSMessage(msg)
		}
	})
	wsHandler.ServeHTTP(w, r)
}

func (s *Session) handleWSMessage(msg WSMessage) {
	switch msg.Type {
	case "inject":
		s.mu.RLock()
		a := s.architecture
		s.mu.RUnlock()
		if a == nil {
			return
		}
		var req struct {
			Name   string         `json:"name"`
			Params map[string]any `json:"params"`
		}
		json.Unmarshal(msg.Data, &req)
		a.Inject(req.Name, req.Params)
	}
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/cmd/rapide-studio && go mod tidy && go build ./...`
Expected: Compiles

- [ ] **Step 3: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add cmd/rapide-studio/ws.go
git commit -m "feat(studio): add WebSocket hub for live event streaming"
```

---

### Task 7: Frontend (static editor + simulation UI)

**Files:**
- Create: `cmd/rapide-studio/static/index.html`
- Create: `cmd/rapide-studio/static/style.css`
- Create: `cmd/rapide-studio/static/app.ts`
- Vendor: `cmd/rapide-studio/static/lib/cytoscape.min.js`

This is the largest single task. The subagent should create all four files with a complete working editor including:

1. **Cytoscape.js canvas** with component nodes and connection edges
2. **Toolbar** with Add Component, Add Connection, Save, Load, Simulate, Stop, Inject buttons
3. **Inspector panel** for editing selected node/edge properties
4. **Event feed panel** for live simulation events
5. **WebSocket connection** for real-time event streaming

- [ ] **Step 1: Download Cytoscape.js**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide/cmd/rapide-studio/static/lib
curl -L -o cytoscape.min.js https://cdnjs.cloudflare.com/ajax/libs/cytoscape/3.30.4/cytoscape.min.js
```

- [ ] **Step 2: Create index.html**

The HTML file should be a single-page app shell with:
- `<div id="cy">` for the Cytoscape canvas (takes 70% width)
- `<div id="inspector">` for the right panel (30% width)
- `<div id="toolbar">` for the top toolbar
- `<div id="events">` for the bottom event feed
- Script tags loading cytoscape.min.js then app.js

- [ ] **Step 3: Create style.css**

Dark theme layout with:
- Full viewport, no scroll
- Toolbar fixed at top (48px)
- Canvas on left (70%)
- Inspector panel on right (30%)
- Event feed at bottom (200px)
- Cytoscape.js container fills its area

- [ ] **Step 4: Create app.ts**

The TypeScript file should implement:
- Cytoscape graph initialization with layout
- `addComponent()` — prompts for ID, adds node
- `addConnection()` — prompts for source/target, adds edge
- `selectHandler()` — click node/edge shows properties in inspector
- `save()` / `load()` — POST/GET to REST API, convert between Cytoscape elements and ArchitectureSchema
- `startSim()` / `stopSim()` — POST to simulation endpoints
- `inject()` — POST to inject endpoint with form data
- WebSocket connection that receives events and appends to event feed
- Event feed: scrolling list showing event name, source, params, timestamp

- [ ] **Step 5: Create go:embed static.go**

```go
// cmd/rapide-studio/static.go
package main

// When the frontend is ready, uncomment this to embed static files:
// //go:generate esbuild static/app.ts --bundle --outfile=static/app.js
// //go:embed static/*
// var staticFS embed.FS
```

For now, serve files from disk via `http.FileServer(http.Dir("static"))` (already in main.go).

- [ ] **Step 6: Verify the app runs**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide/cmd/rapide-studio
go run . -addr :8400
# Open http://localhost:8400 in browser
```

- [ ] **Step 7: Commit**

```bash
cd /Users/shanemorris/Documents/Rapigo/gorapide
git add cmd/rapide-studio/static/
git commit -m "feat(studio): add visual editor frontend with Cytoscape.js canvas"
```

---

### Task 8: Final verification

- [ ] **Step 1: Run all Go tests**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test -race ./...`
Expected: All PASS

- [ ] **Step 2: Run studio tests**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go test ./studio/ -v`
Expected: All PASS

- [ ] **Step 3: Compile cmd/rapide-studio**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide/cmd/rapide-studio && go build -o rapide-studio .`
Expected: Binary produced

- [ ] **Step 4: Run go vet**

Run: `cd /Users/shanemorris/Documents/Rapigo/gorapide && go vet ./...`
Expected: No issues

---

## Verification

**Studio package:**
1. `go test ./studio/ -run TestSchema` — JSON round-trip + validation
2. `go test ./studio/ -run TestReconstruct` — schema to Architecture + event propagation
3. `go test ./studio/ -run TestRecorder` — event recording
4. `go test ./studio/ -run TestReplayMachine` — play/pause/stop/speed

**Server:**
1. `cd cmd/rapide-studio && go build` — compiles
2. Manual browser testing: create architecture, add components/connections, save, start simulation, inject events, observe event feed
