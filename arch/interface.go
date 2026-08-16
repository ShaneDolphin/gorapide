package arch

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/constraint"
)

// ActionKind distinguishes incoming, outgoing, and module-private actions on
// an interface. Private actions are executable by a module of the interface
// type but are not visible to containing architectures.
type ActionKind int

const (
	InAction      ActionKind = iota // Incoming action (received by component)
	OutAction                       // Outgoing action (emitted by component)
	PrivateAction                   // Action visible only inside the component module
)

// FunctionKind distinguishes functions implemented by a component from
// functions that its implementation expects the containing architecture to
// supply. Rapide calls these the provides and requires parts of an interface.
type FunctionKind int

const (
	ProvidesFunction FunctionKind = iota
	RequiresFunction
)

// ParamDecl declares a named, typed action or function parameter. Default is
// nil for an action parameter or a required function actual; a non-nil value
// is the closed default denotation of a function formal object parameter.
type ParamDecl struct {
	Name           string
	Type           string
	Default        any
	structuralType *gorapide.RapideType
}

// P is shorthand for creating a ParamDecl.
func P(name, typ string) ParamDecl {
	return ParamDecl{Name: name, Type: typ}
}

// PStructural declares an action object parameter whose type is a closed
// structural Rapide interface. The display name participates in overload
// resolution while the immutable descriptor supplies exact value membership.
// Function execution over structural parameters remains a separate gate.
func PStructural(name, typeName string, typ gorapide.RapideType) ParamDecl {
	return ParamDecl{Name: name, Type: typeName, structuralType: &typ}
}

// StructuralRapideType returns the exact structural parameter type, when this
// is not a predefined-library parameter.
func (declaration ParamDecl) StructuralRapideType() (gorapide.RapideType, bool) {
	if declaration.structuralType == nil {
		return gorapide.RapideType{}, false
	}
	return *declaration.structuralType, true
}

// PDefault declares a function formal object parameter with a closed default
// denotation. Canonical model construction validates both the value and its
// exact supported predefined type. Action declarations reject defaults.
func PDefault(name, typ string, value any) ParamDecl {
	return ParamDecl{Name: name, Type: typ, Default: value}
}

// ActionDecl declares an action on an interface.
type ActionDecl struct {
	Name   string
	Kind   ActionKind
	Params []ParamDecl
}

// FunctionDecl declares a synchronous interface function. ReturnType is empty
// for a function with no returned object. Function execution and the related
// Call/Return events are not implied merely by declaring the signature.
type FunctionDecl struct {
	Name       string
	Kind       FunctionKind
	Params     []ParamDecl
	ReturnType string
}

// ServiceDecl groups related actions and functions under a named service.
type ServiceDecl struct {
	Name      string
	Actions   []ActionDecl
	Functions []FunctionDecl
}

// InterfaceDecl declares the externally visible actions and functions of a
// component.
type InterfaceDecl struct {
	Name           string
	Actions        []ActionDecl
	Functions      []FunctionDecl
	Services       []ServiceDecl
	structuralType *gorapide.RapideType
}

// StructuralRapideType returns the closed Stanford structural descriptor bound
// to this execution-facing interface, when one has been supplied. The value is
// immutable and returned by copy.
func (d *InterfaceDecl) StructuralRapideType() (gorapide.RapideType, bool) {
	if d == nil || d.structuralType == nil {
		return gorapide.RapideType{}, false
	}
	return *d.structuralType, true
}

// String returns a human-readable representation of the interface.
func (d *InterfaceDecl) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Interface(%s)", d.Name)
	for _, a := range d.Actions {
		kind := "private"
		switch a.Kind {
		case InAction:
			kind = "in"
		case OutAction:
			kind = "out"
		}
		fmt.Fprintf(&b, " %s:%s", kind, a.Name)
	}
	for _, f := range d.Functions {
		kind := "provides"
		if f.Kind == RequiresFunction {
			kind = "requires"
		}
		fmt.Fprintf(&b, " %s:%s", kind, f.Name)
	}
	for _, s := range d.Services {
		fmt.Fprintf(&b, " service:%s[%d actions,%d functions]", s.Name, len(s.Actions), len(s.Functions))
	}
	if d.structuralType != nil {
		b.WriteString(" structural:type")
	}
	return b.String()
}

// --- InterfaceDecl Builder ---

// InterfaceDeclBuilder builds an InterfaceDecl using a fluent API.
type InterfaceDeclBuilder struct {
	name           string
	actions        []ActionDecl
	functions      []FunctionDecl
	services       []ServiceDecl
	structuralType *gorapide.RapideType
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

// PrivateAction adds an action visible only to modules of this interface type.
func (b *InterfaceDeclBuilder) PrivateAction(name string, params ...ParamDecl) *InterfaceDeclBuilder {
	b.actions = append(b.actions, ActionDecl{
		Name:   name,
		Kind:   PrivateAction,
		Params: params,
	})
	return b
}

// ProvidesFunction adds a synchronous function implemented by the component.
// An empty returnType declares a function with no returned object.
func (b *InterfaceDeclBuilder) ProvidesFunction(name, returnType string, params ...ParamDecl) *InterfaceDeclBuilder {
	b.functions = append(b.functions, FunctionDecl{
		Name:       name,
		Kind:       ProvidesFunction,
		Params:     append([]ParamDecl(nil), params...),
		ReturnType: returnType,
	})
	return b
}

// RequiresFunction adds a synchronous function that the containing
// architecture must supply to the component implementation. An empty
// returnType declares a function with no returned object.
func (b *InterfaceDeclBuilder) RequiresFunction(name, returnType string, params ...ParamDecl) *InterfaceDeclBuilder {
	b.functions = append(b.functions, FunctionDecl{
		Name:       name,
		Kind:       RequiresFunction,
		Params:     append([]ParamDecl(nil), params...),
		ReturnType: returnType,
	})
	return b
}

// StructuralType binds the closed structural type descriptor used for source
// type-name/object constituents and subtype audit. Deterministic model
// validation rejects a zero or malformed descriptor.
func (b *InterfaceDeclBuilder) StructuralType(typ gorapide.RapideType) *InterfaceDeclBuilder {
	copy := typ
	b.structuralType = &copy
	return b
}

// ServiceBuilder is used within Service() to declare actions and functions on
// a service.
type ServiceBuilder struct {
	actions   []ActionDecl
	functions []FunctionDecl
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

// PrivateAction adds a module-private action to the service.
func (s *ServiceBuilder) PrivateAction(name string, params ...ParamDecl) {
	s.actions = append(s.actions, ActionDecl{
		Name:   name,
		Kind:   PrivateAction,
		Params: params,
	})
}

// ProvidesFunction adds a provided function to the service.
func (s *ServiceBuilder) ProvidesFunction(name, returnType string, params ...ParamDecl) {
	s.functions = append(s.functions, FunctionDecl{
		Name:       name,
		Kind:       ProvidesFunction,
		Params:     append([]ParamDecl(nil), params...),
		ReturnType: returnType,
	})
}

// RequiresFunction adds a required function to the service.
func (s *ServiceBuilder) RequiresFunction(name, returnType string, params ...ParamDecl) {
	s.functions = append(s.functions, FunctionDecl{
		Name:       name,
		Kind:       RequiresFunction,
		Params:     append([]ParamDecl(nil), params...),
		ReturnType: returnType,
	})
}

// Service adds a named service group to the interface.
func (b *InterfaceDeclBuilder) Service(name string, fn func(*ServiceBuilder)) *InterfaceDeclBuilder {
	sb := &ServiceBuilder{}
	fn(sb)
	b.services = append(b.services, ServiceDecl{
		Name:      name,
		Actions:   sb.actions,
		Functions: sb.functions,
	})
	return b
}

// Build finalizes and returns the InterfaceDecl.
func (b *InterfaceDeclBuilder) Build() *InterfaceDecl {
	return &InterfaceDecl{
		Name:           b.name,
		Actions:        b.actions,
		Functions:      b.functions,
		Services:       b.services,
		structuralType: b.structuralType,
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

	rules                    []*BehaviorRule
	transitions              []*DeclarativeRule
	processes                []*DeclarativeProcess
	processMode              ModuleProcessMode
	basicClocks              []BasicClockDeclaration
	stateDeclarations        []StateDeclaration
	functions                []*FunctionImplementation
	exceptions               []ExceptionDeclaration
	moduleHandler            *ExceptionHandler
	initializationParameters []ModuleInitializationParameter
	initialStatements        []Statement
	finalStatements          []Statement
	moduleConstraints        *constraint.ConstraintSet
	moduleMembership         *moduleMembershipDeclaration
	observed                 gorapide.EventSet
	consumed                 *RuleConsumption
	onEmit                   func(*gorapide.Event) error // set by Architecture for checked router notification

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// SetModuleConstraints attaches closed constraints to this generated module
// instance. They are evaluated over the events visible to this component,
// including its private actions, rather than over the containing architecture.
func (c *Component) SetModuleConstraints(set *constraint.ConstraintSet) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.moduleConstraints = set
}

// NewComponent creates a new Component with the given ID, interface, poset,
// and optional configuration.
func NewComponent(id string, iface *InterfaceDecl, poset *gorapide.Poset, opts ...ComponentOption) *Component {
	c := &Component{
		ID:        id,
		Interface: iface,
		poset:     poset,
		bufSize:   16, // default buffer size
		consumed:  NewRuleConsumption(),
	}
	for _, opt := range opts {
		opt(c)
	}
	c.inbox = make(chan *gorapide.Event, c.bufSize)
	return c
}

// OnReceive registers a behavior function called for each received event.
//
// Deprecated: receive callbacks are legacy host behavior and are rejected by
// deterministic model validation. Use closed declarative rules or processes.
func (c *Component) OnReceive(fn BehaviorFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.behavior = fn
}

// Send delivers an event to the component's inbox. It is non-blocking:
// returns true if the event was enqueued, false if the inbox is full.
//
// Deprecated: inbox delivery is part of the legacy asynchronous adapter. The
// deterministic engine uses a lossless semantic worklist and never calls Send.
func (c *Component) Send(e *gorapide.Event) bool {
	return c.SendChecked(e) == nil
}

// SendChecked performs one non-blocking legacy inbox delivery and returns a
// stable typed error instead of requiring callers to interpret or ignore a
// boolean rejection.
func (c *Component) SendChecked(e *gorapide.Event) error {
	if c == nil {
		return fmt.Errorf("%w: component is nil", ErrDeliveryRejected)
	}
	if e == nil {
		return fmt.Errorf("%w: component %q event is nil", ErrDeliveryRejected, c.ID)
	}
	select {
	case c.inbox <- e:
		return nil
	default:
		return fmt.Errorf("%w: component %q inbox is full", ErrDeliveryRejected, c.ID)
	}
}

// Emit creates a new event sourced from this component, adds it to the
// poset with optional causal predecessors, and returns it.
//
// Deprecated: Emit creates a random, wall-clock legacy event. Declare outputs
// in deterministic rules, processes, initial/final parts, or journal inputs.
func (c *Component) Emit(name string, params map[string]any, causes ...gorapide.EventID) (*gorapide.Event, error) {
	e := gorapide.NewEvent(name, c.ID, params)
	if err := c.poset.AddEventWithCause(e, causes...); err != nil {
		return nil, fmt.Errorf("arch.Component.Emit: %w", err)
	}
	if c.onEmit != nil {
		if err := c.onEmit(e); err != nil {
			return e, fmt.Errorf("arch.Component.Emit: %w", err)
		}
	}
	return e, nil
}

// Start begins the component's event processing loop in a goroutine.
// The loop runs until Stop is called or the context is cancelled.
//
// Deprecated: component goroutines are outside the deterministic trusted core.
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
			if ctx.Err() != nil {
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
//
// Deprecated: Stop controls only the legacy asynchronous adapter.
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
//
// Deprecated: Wait controls only the legacy asynchronous adapter.
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
