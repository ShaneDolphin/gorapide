package arch

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// ActionKind distinguishes incoming vs outgoing actions on an interface.
type ActionKind int

const (
	InAction  ActionKind = iota // Incoming action (received by component)
	OutAction                   // Outgoing action (emitted by component)
)

// ParamDecl declares a named, typed parameter for an action.
type ParamDecl struct {
	Name string
	Type string
}

// P is shorthand for creating a ParamDecl.
func P(name, typ string) ParamDecl {
	return ParamDecl{Name: name, Type: typ}
}

// ActionDecl declares an action on an interface.
type ActionDecl struct {
	Name   string
	Kind   ActionKind
	Params []ParamDecl
}

// ServiceDecl groups related actions under a named service.
type ServiceDecl struct {
	Name    string
	Actions []ActionDecl
}

// InterfaceDecl declares the interface (set of actions) for a component.
type InterfaceDecl struct {
	Name     string
	Actions  []ActionDecl
	Services []ServiceDecl
}

// String returns a human-readable representation of the interface.
func (d *InterfaceDecl) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Interface(%s)", d.Name)
	for _, a := range d.Actions {
		kind := "in"
		if a.Kind == OutAction {
			kind = "out"
		}
		fmt.Fprintf(&b, " %s:%s", kind, a.Name)
	}
	for _, s := range d.Services {
		fmt.Fprintf(&b, " service:%s[%d]", s.Name, len(s.Actions))
	}
	return b.String()
}

// --- InterfaceDecl Builder ---

// InterfaceDeclBuilder builds an InterfaceDecl using a fluent API.
type InterfaceDeclBuilder struct {
	name     string
	actions  []ActionDecl
	services []ServiceDecl
}

// Interface starts building a new InterfaceDecl with the given name.
func Interface(name string) *InterfaceDeclBuilder {
	return &InterfaceDeclBuilder{name: name}
}

// InAction adds an incoming action declaration.
func (b *InterfaceDeclBuilder) InAction(name string, params ...ParamDecl) *InterfaceDeclBuilder {
	b.actions = append(b.actions, ActionDecl{
		Name:   name,
		Kind:   InAction,
		Params: params,
	})
	return b
}

// OutAction adds an outgoing action declaration.
func (b *InterfaceDeclBuilder) OutAction(name string, params ...ParamDecl) *InterfaceDeclBuilder {
	b.actions = append(b.actions, ActionDecl{
		Name:   name,
		Kind:   OutAction,
		Params: params,
	})
	return b
}

// ServiceBuilder is used within Service() to declare actions on a service.
type ServiceBuilder struct {
	actions []ActionDecl
}

// InAction adds an incoming action to the service.
func (s *ServiceBuilder) InAction(name string, params ...ParamDecl) {
	s.actions = append(s.actions, ActionDecl{
		Name:   name,
		Kind:   InAction,
		Params: params,
	})
}

// OutAction adds an outgoing action to the service.
func (s *ServiceBuilder) OutAction(name string, params ...ParamDecl) {
	s.actions = append(s.actions, ActionDecl{
		Name:   name,
		Kind:   OutAction,
		Params: params,
	})
}

// Service adds a named service group to the interface.
func (b *InterfaceDeclBuilder) Service(name string, fn func(*ServiceBuilder)) *InterfaceDeclBuilder {
	sb := &ServiceBuilder{}
	fn(sb)
	b.services = append(b.services, ServiceDecl{
		Name:    name,
		Actions: sb.actions,
	})
	return b
}

// Build finalizes and returns the InterfaceDecl.
func (b *InterfaceDeclBuilder) Build() *InterfaceDecl {
	return &InterfaceDecl{
		Name:     b.name,
		Actions:  b.actions,
		Services: b.services,
	}
}

// --- Component ---

// ComponentOption configures a Component.
type ComponentOption func(*Component)

// WithBufferSize sets the inbox channel buffer size.
func WithBufferSize(n int) ComponentOption {
	return func(c *Component) {
		c.bufSize = n
	}
}

// BehaviorFunc is a callback invoked when a component receives an event.
type BehaviorFunc func(comp *Component, e *gorapide.Event)

// Component is a runtime instance of an interface declaration.
// It has an inbox channel for receiving events, a reference to a shared
// poset for emitting events, and registered behaviors.
type Component struct {
	ID        string
	Interface *InterfaceDecl

	poset    *gorapide.Poset
	inbox    chan *gorapide.Event
	bufSize  int
	behavior BehaviorFunc

	rules    []*BehaviorRule
	observed gorapide.EventSet
	onEmit   func(*gorapide.Event) // set by Architecture for router notification

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewComponent creates a new Component with the given ID, interface, poset,
// and optional configuration.
func NewComponent(id string, iface *InterfaceDecl, poset *gorapide.Poset, opts ...ComponentOption) *Component {
	c := &Component{
		ID:        id,
		Interface: iface,
		poset:     poset,
		bufSize:   16, // default buffer size
	}
	for _, opt := range opts {
		opt(c)
	}
	c.inbox = make(chan *gorapide.Event, c.bufSize)
	return c
}

// OnReceive registers a behavior function called for each received event.
func (c *Component) OnReceive(fn BehaviorFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.behavior = fn
}

// Send delivers an event to the component's inbox. It is non-blocking:
// returns true if the event was enqueued, false if the inbox is full.
func (c *Component) Send(e *gorapide.Event) bool {
	select {
	case c.inbox <- e:
		return true
	default:
		return false
	}
}

// Emit creates a new event sourced from this component, adds it to the
// poset with optional causal predecessors, and returns it.
func (c *Component) Emit(name string, params map[string]any, causes ...gorapide.EventID) (*gorapide.Event, error) {
	e := gorapide.NewEvent(name, c.ID, params)
	if err := c.poset.AddEventWithCause(e, causes...); err != nil {
		return nil, fmt.Errorf("arch.Component.Emit: %w", err)
	}
	if c.onEmit != nil {
		c.onEmit(e)
	}
	return e, nil
}

// Start begins the component's event processing loop in a goroutine.
// The loop runs until Stop is called or the context is cancelled.
func (c *Component) Start(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return
	}
	c.running = true
	c.done = make(chan struct{})

	var loopCtx context.Context
	loopCtx, c.cancel = context.WithCancel(ctx)

	go c.run(loopCtx)
}

func (c *Component) run(ctx context.Context) {
	defer close(c.done)
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-c.inbox:
			if !ok {
				return
			}
			c.mu.Lock()
			fn := c.behavior
			c.mu.Unlock()
			if fn != nil {
				fn(c, e)
			}
			c.observe(e)
		}
	}
}

// Stop signals the component to stop processing events.
func (c *Component) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return
	}
	c.running = false
	c.cancel()
}

// Wait blocks until the component's event loop has exited.
func (c *Component) Wait() {
	c.mu.Lock()
	d := c.done
	c.mu.Unlock()
	if d != nil {
		<-d
	}
}

// String returns a human-readable representation of the component.
func (c *Component) String() string {
	return fmt.Sprintf("Component(%s, %s)", c.ID, c.Interface.Name)
}
