package arch

import (
	"fmt"
	"sync"

	"github.com/ShaneDolphin/gorapide"
)

// Binding represents a dynamic runtime wiring between two components.
// It connects a source component to a target component, optionally
// translating events through a Map and using a specific ConnectionKind.
type Binding struct {
	ID       string
	FromComp string
	ToComp   string
	Map      *Map           // optional event translation
	Kind     ConnectionKind // BasicConnection, PipeConnection, AgentConnection
}

// BindingOption configures a Binding.
type BindingOption func(*Binding)

// WithBindingMap sets the event translation Map for a Binding.
func WithBindingMap(m *Map) BindingOption {
	return func(b *Binding) {
		b.Map = m
	}
}

// WithBindingKind sets the ConnectionKind for a Binding.
func WithBindingKind(k ConnectionKind) BindingOption {
	return func(b *Binding) {
		b.Kind = k
	}
}

// BindingManager is a thread-safe manager for dynamic runtime bindings.
// It implements gorapide.BindingTarget.
type BindingManager struct {
	bindings map[string]*Binding
	bySource map[string][]string // component ID -> binding IDs
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

// Bind creates a new binding from source to target with defaults
// (PipeConnection, no Map). Satisfies gorapide.BindingTarget.
func (bm *BindingManager) Bind(from, to string) error {
	_, err := bm.BindWith(from, to)
	return err
}

// Unbind removes ALL bindings where source == from.
// Satisfies gorapide.BindingTarget.
func (bm *BindingManager) Unbind(from string) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	ids, ok := bm.bySource[from]
	if !ok || len(ids) == 0 {
		return fmt.Errorf("arch.BindingManager.Unbind: no bindings from %q", from)
	}

	for _, id := range ids {
		delete(bm.bindings, id)
	}
	delete(bm.bySource, from)
	return nil
}

// BindWith creates a binding with options and returns the binding ID.
func (bm *BindingManager) BindWith(from, to string, opts ...BindingOption) (string, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	bm.nextID++
	id := fmt.Sprintf("bind-%d", bm.nextID)

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

// UnbindByID removes a specific binding by ID.
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
	// Clean up empty slice.
	if len(bm.bySource[b.FromComp]) == 0 {
		delete(bm.bySource, b.FromComp)
	}

	return nil
}

// BindingsFrom returns all active bindings originating from the given component.
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

// executeBinding processes an event through a binding, creating target events
// according to the binding's Map and Kind. Returns created events for cascade.
func (bm *BindingManager) executeBinding(b *Binding, triggerEvent *gorapide.Event, target *Component, poset *gorapide.Poset) []*gorapide.Event {
	if b.Map != nil {
		return bm.executeWithMap(b, triggerEvent, target, poset)
	}
	return bm.executeIdentity(b, triggerEvent, target, poset)
}

// executeWithMap translates events through the binding's Map.
func (bm *BindingManager) executeWithMap(b *Binding, triggerEvent *gorapide.Event, target *Component, poset *gorapide.Poset) []*gorapide.Event {
	mapped, err := b.Map.MapEvent(triggerEvent)
	if err != nil {
		return nil
	}

	var results []*gorapide.Event
	for _, me := range mapped {
		me.Source = target.ID

		switch b.Kind {
		case PipeConnection:
			_ = poset.AddEventWithCause(me, triggerEvent.ID)
		default:
			_ = poset.AddEvent(me)
		}

		target.Send(me)
		results = append(results, me)
	}
	return results
}

// executeIdentity handles identity translation (no Map) based on Kind.
func (bm *BindingManager) executeIdentity(b *Binding, triggerEvent *gorapide.Event, target *Component, poset *gorapide.Poset) []*gorapide.Event {
	switch b.Kind {
	case AgentConnection:
		// Forward original event.
		target.Send(triggerEvent)
		return nil

	case PipeConnection:
		params := copyParams(triggerEvent)
		e := gorapide.NewEvent(triggerEvent.Name, target.ID, params)
		_ = poset.AddEventWithCause(e, triggerEvent.ID)
		target.Send(e)
		return []*gorapide.Event{e}

	case BasicConnection:
		params := copyParams(triggerEvent)
		e := gorapide.NewEvent(triggerEvent.Name, target.ID, params)
		_ = poset.AddEvent(e)
		target.Send(e)
		return []*gorapide.Event{e}

	default:
		return nil
	}
}

// Compile-time assertion that *BindingManager satisfies gorapide.BindingTarget.
var _ gorapide.BindingTarget = (*BindingManager)(nil)
