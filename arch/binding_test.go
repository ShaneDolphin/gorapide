package arch

import (
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestBindingManagerBind(t *testing.T) {
	bm := NewBindingManager()

	if err := bm.Bind("compA", "compB"); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	bindings := bm.BindingsFrom("compA")
	if len(bindings) != 1 {
		t.Fatalf("BindingsFrom: want 1, got %d", len(bindings))
	}

	b := bindings[0]
	if b.FromComp != "compA" {
		t.Errorf("FromComp: want compA, got %s", b.FromComp)
	}
	if b.ToComp != "compB" {
		t.Errorf("ToComp: want compB, got %s", b.ToComp)
	}
	if b.ID == "" {
		t.Error("ID should not be empty")
	}
}

func TestBindingManagerBindWith(t *testing.T) {
	bm := NewBindingManager()

	m := NewMap("testMap").
		Translate("X", "Y").
		Build()

	id, err := bm.BindWith("compA", "compB",
		WithBindingMap(m),
		WithBindingKind(AgentConnection),
	)
	if err != nil {
		t.Fatalf("BindWith: %v", err)
	}
	if id == "" {
		t.Fatal("BindWith should return a non-empty ID")
	}

	bindings := bm.BindingsFrom("compA")
	if len(bindings) != 1 {
		t.Fatalf("BindingsFrom: want 1, got %d", len(bindings))
	}

	b := bindings[0]
	if b.Map != m {
		t.Error("Map should match the provided Map")
	}
	if b.Kind != AgentConnection {
		t.Errorf("Kind: want AgentConnection, got %v", b.Kind)
	}
	if b.ID != id {
		t.Errorf("ID: want %s, got %s", id, b.ID)
	}
}

func TestBindingManagerUnbind(t *testing.T) {
	bm := NewBindingManager()

	// Create bindings from two different sources.
	_ = bm.Bind("compA", "compB")
	_ = bm.Bind("compA", "compC")
	_ = bm.Bind("compX", "compY")

	// Unbind all from compA.
	if err := bm.Unbind("compA"); err != nil {
		t.Fatalf("Unbind: %v", err)
	}

	// compA should have no bindings.
	if bindings := bm.BindingsFrom("compA"); len(bindings) != 0 {
		t.Errorf("BindingsFrom compA: want 0, got %d", len(bindings))
	}

	// compX bindings should remain.
	if bindings := bm.BindingsFrom("compX"); len(bindings) != 1 {
		t.Errorf("BindingsFrom compX: want 1, got %d", len(bindings))
	}
}

func TestBindingManagerUnbindByID(t *testing.T) {
	bm := NewBindingManager()

	id1, _ := bm.BindWith("compA", "compB")
	id2, _ := bm.BindWith("compA", "compC")

	// Remove only the first binding.
	if err := bm.UnbindByID(id1); err != nil {
		t.Fatalf("UnbindByID: %v", err)
	}

	bindings := bm.BindingsFrom("compA")
	if len(bindings) != 1 {
		t.Fatalf("BindingsFrom: want 1, got %d", len(bindings))
	}
	if bindings[0].ID != id2 {
		t.Errorf("remaining binding ID: want %s, got %s", id2, bindings[0].ID)
	}
}

func TestBindingManagerUnbindByIDNotFound(t *testing.T) {
	bm := NewBindingManager()

	err := bm.UnbindByID("nonexistent")
	if err == nil {
		t.Fatal("UnbindByID should error for unknown ID")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

func TestBindingManagerActiveBindings(t *testing.T) {
	bm := NewBindingManager()

	_ = bm.Bind("a", "b")
	_ = bm.Bind("c", "d")
	_ = bm.Bind("e", "f")

	all := bm.ActiveBindings()
	if len(all) != 3 {
		t.Errorf("ActiveBindings: want 3, got %d", len(all))
	}
}

func TestBindingManagerDefaultKind(t *testing.T) {
	bm := NewBindingManager()

	_ = bm.Bind("compA", "compB")

	bindings := bm.BindingsFrom("compA")
	if len(bindings) != 1 {
		t.Fatalf("BindingsFrom: want 1, got %d", len(bindings))
	}
	if bindings[0].Kind != PipeConnection {
		t.Errorf("default Kind: want PipeConnection, got %v", bindings[0].Kind)
	}
}

// --- executeBinding Tests ---

func TestExecuteBindingWithMap(t *testing.T) {
	bm := NewBindingManager()
	poset := gorapide.NewPoset()
	iface := Interface("Target").InAction("Y").Build()
	target := NewComponent("target", iface, poset)

	m := NewMap("testMap").
		Translate("X", "Y").
		Build()

	b := &Binding{
		ID:       "bind-test",
		FromComp: "source",
		ToComp:   "target",
		Map:      m,
		Kind:     PipeConnection,
	}

	trigger := gorapide.NewEvent("X", "source", map[string]any{"key": "val"})
	_ = poset.AddEvent(trigger)

	results := bm.executeBinding(b, trigger, target, poset)
	if len(results) != 1 {
		t.Fatalf("executeBinding with Map: want 1 result, got %d", len(results))
	}
	if results[0].Name != "Y" {
		t.Errorf("result name: want Y, got %s", results[0].Name)
	}
	if results[0].Source != "target" {
		t.Errorf("result source: want target, got %s", results[0].Source)
	}
}

func TestExecuteBindingIdentityPipe(t *testing.T) {
	bm := NewBindingManager()
	poset := gorapide.NewPoset()
	iface := Interface("Target").InAction("X").Build()
	target := NewComponent("target", iface, poset)

	b := &Binding{
		ID:       "bind-pipe",
		FromComp: "source",
		ToComp:   "target",
		Kind:     PipeConnection,
	}

	trigger := gorapide.NewEvent("X", "source", map[string]any{"key": "val"})
	_ = poset.AddEvent(trigger)

	results := bm.executeBinding(b, trigger, target, poset)
	if len(results) != 1 {
		t.Fatalf("executeBinding pipe: want 1 result, got %d", len(results))
	}
	if results[0].Name != "X" {
		t.Errorf("result name: want X, got %s", results[0].Name)
	}
	if results[0].Source != "target" {
		t.Errorf("result source: want target, got %s", results[0].Source)
	}
	// Verify causal link exists.
	if !poset.HasPath(trigger.ID, results[0].ID) {
		t.Error("pipe binding should create causal link from trigger to result")
	}
}

func TestExecuteBindingIdentityBasic(t *testing.T) {
	bm := NewBindingManager()
	poset := gorapide.NewPoset()
	iface := Interface("Target").InAction("X").Build()
	target := NewComponent("target", iface, poset)

	b := &Binding{
		ID:       "bind-basic",
		FromComp: "source",
		ToComp:   "target",
		Kind:     BasicConnection,
	}

	trigger := gorapide.NewEvent("X", "source", map[string]any{"key": "val"})
	_ = poset.AddEvent(trigger)

	results := bm.executeBinding(b, trigger, target, poset)
	if len(results) != 1 {
		t.Fatalf("executeBinding basic: want 1 result, got %d", len(results))
	}
	// No causal link for basic.
	if poset.HasPath(trigger.ID, results[0].ID) {
		t.Error("basic binding should NOT create causal link")
	}
}

func TestExecuteBindingIdentityAgent(t *testing.T) {
	bm := NewBindingManager()
	poset := gorapide.NewPoset()
	iface := Interface("Target").InAction("X").Build()
	target := NewComponent("target", iface, poset)

	b := &Binding{
		ID:       "bind-agent",
		FromComp: "source",
		ToComp:   "target",
		Kind:     AgentConnection,
	}

	trigger := gorapide.NewEvent("X", "source", map[string]any{"key": "val"})
	_ = poset.AddEvent(trigger)

	results := bm.executeBinding(b, trigger, target, poset)
	if results != nil {
		t.Errorf("agent binding should return nil results, got %d", len(results))
	}

	// The original event should have been sent to target inbox.
	select {
	case e := <-target.inbox:
		if e.ID != trigger.ID {
			t.Errorf("agent should forward original event, got different ID")
		}
	default:
		t.Error("agent binding should have sent event to target")
	}
}
