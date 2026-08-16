package arch

import (
	"fmt"
	"sort"
	"sync"

	"github.com/ShaneDolphin/gorapide"
)

// Binding represents a dynamic runtime wiring between two components.
// It connects a source component to a target component, optionally
// translating events through a Map and using a specific ConnectionKind.
//
// Deprecated: Binding is part of the legacy asynchronous adapter and is
// rejected by deterministic architecture validation.
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
//
// Deprecated: dynamic runtime binding is outside the deterministic trusted
// core. Express static supported wiring as canonical Rapide connections.
type BindingManager struct {
	bindings map[string]*Binding
	bySource map[string][]string // component ID -> binding IDs
	mu       sync.RWMutex
	nextID   int
}

// NewBindingManager creates an empty BindingManager.
//
// Deprecated: dynamic runtime binding is outside the deterministic trusted
// core.
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
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// executeBinding processes an event through a binding, creating target events
// according to the binding's Map and Kind. Returns created events for cascade
// or the exact mapping, insertion, kind, or inbox-delivery failure.
func (bm *BindingManager) executeBinding(b *Binding, triggerEvent *gorapide.Event, target *Component, poset *gorapide.Poset) ([]*gorapide.Event, error) {
	if b == nil || triggerEvent == nil || target == nil || poset == nil {
		return nil, fmt.Errorf("legacy binding execution requires binding, trigger, target, and poset")
	}
	if b.Map != nil {
		return bm.executeWithMap(b, triggerEvent, target, poset)
	}
	return bm.executeIdentity(b, triggerEvent, target, poset)
}

// executeWithMap translates events through the binding's Map.
func (bm *BindingManager) executeWithMap(b *Binding, triggerEvent *gorapide.Event, target *Component, poset *gorapide.Poset) ([]*gorapide.Event, error) {
	mapped, err := b.Map.MapEvent(triggerEvent)
	if err != nil {
		return nil, fmt.Errorf("binding %q map: %w", b.ID, err)
	}

	var results []*gorapide.Event
	for index, me := range mapped {
		if me == nil {
			return results, fmt.Errorf("binding %q map result %d is nil", b.ID, index)
		}
		me.Source = target.ID

		switch b.Kind {
		case PipeConnection:
			err = poset.AddEventWithCause(me, triggerEvent.ID)
		case BasicConnection, AgentConnection:
			err = poset.AddEvent(me)
		default:
			return results, fmt.Errorf("binding %q has unsupported connection kind %d", b.ID, b.Kind)
		}
		if err != nil {
			return results, fmt.Errorf("binding %q map result %d poset insertion: %w", b.ID, index, err)
		}
		if err := target.SendChecked(me); err != nil {
			return results, fmt.Errorf("binding %q map result %d delivery: %w", b.ID, index, err)
		}
		results = append(results, me)
	}
	return results, nil
}

// executeIdentity handles identity translation (no Map) based on Kind.
func (bm *BindingManager) executeIdentity(b *Binding, triggerEvent *gorapide.Event, target *Component, poset *gorapide.Poset) ([]*gorapide.Event, error) {
	switch b.Kind {
	case AgentConnection:
		// Forward original event.
		if err := target.SendChecked(triggerEvent); err != nil {
			return nil, fmt.Errorf("binding %q agent delivery: %w", b.ID, err)
		}
		return nil, nil

	case PipeConnection:
		params := copyParams(triggerEvent)
		e := gorapide.NewEvent(triggerEvent.Name, target.ID, params)
		if err := poset.AddEventWithCause(e, triggerEvent.ID); err != nil {
			return nil, fmt.Errorf("binding %q pipe poset insertion: %w", b.ID, err)
		}
		if err := target.SendChecked(e); err != nil {
			return nil, fmt.Errorf("binding %q pipe delivery: %w", b.ID, err)
		}
		return []*gorapide.Event{e}, nil

	case BasicConnection:
		params := copyParams(triggerEvent)
		e := gorapide.NewEvent(triggerEvent.Name, target.ID, params)
		if err := poset.AddEvent(e); err != nil {
			return nil, fmt.Errorf("binding %q basic poset insertion: %w", b.ID, err)
		}
		if err := target.SendChecked(e); err != nil {
			return nil, fmt.Errorf("binding %q basic delivery: %w", b.ID, err)
		}
		return []*gorapide.Event{e}, nil

	default:
		return nil, fmt.Errorf("binding %q has unsupported connection kind %d", b.ID, b.Kind)
	}
}

// Compile-time assertion that *BindingManager satisfies gorapide.BindingTarget.
var _ gorapide.BindingTarget = (*BindingManager)(nil)
