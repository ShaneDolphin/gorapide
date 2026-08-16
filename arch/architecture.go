package arch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/constraint"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// ErrLegacyRuntimeFailure identifies an explicit failure in the deprecated
// goroutine/channel architecture adapter. It is never produced by the
// deterministic prepared kernel.
var ErrLegacyRuntimeFailure = errors.New("legacy asynchronous architecture runtime failed")

// Architecture composes components, connections, and constraints into
// a runnable Rapide system.
type Architecture struct {
	Name                          string
	generatorArguments            []ArchitectureGeneratorArgument
	returnInterface               *InterfaceDecl
	components                    map[string]*Component
	connections                   []*Connection
	functionConnections           []*FunctionConnection
	finiteIterators               map[string]*FiniteIteratorModule
	iteratorGenerators            map[string]*FiniteIteratorGenerator
	architectureInstances         map[string]ArchitectureInstanceDeclaration
	architectureConstraints       map[string]*constraint.ConstraintSet
	architectureInitials          map[string][]Statement
	architectureInitialExceptions map[string][]ExceptionDeclaration
	componentArchitectures        map[string]string
	bindings                      *BindingManager
	subArchitectures              map[string]*SubArchitecture
	poset                         *gorapide.Poset
	onEvent                       []func(*gorapide.Event)
	events                        chan *gorapide.Event // router notification channel

	constraintSet  *constraint.ConstraintSet
	constraintMode constraint.CheckMode
	checker        *constraint.Checker
	checkerOpts    func(*constraint.Checker)

	mu        sync.RWMutex
	running   bool
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{} // closed when router exits
	legacyErr error
}

// ArchitectureGeneratorArgument records one explicit actual object used to
// elaborate an architecture generator. Arguments are immutable model data:
// they are type-checked, canonically encoded, and included in deterministic
// architecture identity instead of being recovered from host state.
type ArchitectureGeneratorArgument struct {
	Name  string
	Type  string
	Value any
}

// ArchitectureArgument constructs one architecture-generator actual.
func ArchitectureArgument(name, typ string, value any) ArchitectureGeneratorArgument {
	return ArchitectureGeneratorArgument{Name: name, Type: typ, Value: value}
}

// ArchitectureInterfaceID is the stable occurrence identity of the enclosing
// architecture's returned interface. It is deliberately outside Rapide's
// identifier syntax so it cannot collide with a source-declared component.
// The architecture interface is a boundary endpoint, not a child component.
const ArchitectureInterfaceID = "$architecture"

// ArchOption configures an Architecture.
type ArchOption func(*Architecture)

// WithPoset uses an existing poset instead of creating a new one.
func WithPoset(p *gorapide.Poset) ArchOption {
	return func(a *Architecture) {
		a.poset = p
	}
}

// WithObserver adds a global event observer callback.
//
// Deprecated: observers run in the legacy asynchronous adapter and are
// rejected by deterministic model validation. Inspect the returned
// ExecutionResult instead.
func WithObserver(fn func(*gorapide.Event)) ArchOption {
	return func(a *Architecture) {
		a.onEvent = append(a.onEvent, fn)
	}
}

// NewArchitecture creates a new architecture with the given name.
func NewArchitecture(name string, opts ...ArchOption) *Architecture {
	a := &Architecture{
		Name:                          name,
		returnInterface:               &InterfaceDecl{Name: "Root"},
		components:                    make(map[string]*Component),
		finiteIterators:               make(map[string]*FiniteIteratorModule),
		iteratorGenerators:            make(map[string]*FiniteIteratorGenerator),
		architectureInstances:         make(map[string]ArchitectureInstanceDeclaration),
		architectureConstraints:       make(map[string]*constraint.ConstraintSet),
		architectureInitials:          make(map[string][]Statement),
		architectureInitialExceptions: make(map[string][]ExceptionDeclaration),
		componentArchitectures:        make(map[string]string),
		bindings:                      NewBindingManager(),
		subArchitectures:              make(map[string]*SubArchitecture),
		events:                        make(chan *gorapide.Event, 1024),
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.poset == nil {
		a.poset = gorapide.NewPoset()
	}
	return a
}

// SetGeneratorArguments replaces the explicit actual objects that created this
// architecture instance. Declaration order is retained because architecture
// generator formals are positional. Values are normalized immediately so a
// caller cannot mutate model identity through a retained slice or map.
func (a *Architecture) SetGeneratorArguments(arguments ...ArchitectureGeneratorArgument) error {
	if a == nil {
		return fmt.Errorf("arch: architecture is nil")
	}
	normalized := make([]ArchitectureGeneratorArgument, len(arguments))
	seen := make(map[string]bool, len(arguments))
	for index, argument := range arguments {
		if argument.Name == "" || argument.Type == "" {
			return fmt.Errorf("arch: architecture generator argument %d has an empty name or type", index)
		}
		key := strings.ToLower(argument.Name)
		if seen[key] {
			return fmt.Errorf("arch: duplicate architecture generator argument %q", argument.Name)
		}
		if !gorapide.IsSupportedPredefinedType(argument.Type) {
			return fmt.Errorf("%w: architecture generator argument %q has type %q",
				ErrUnsupportedRapideType, argument.Name, argument.Type)
		}
		value, err := gorapide.CanonicalizeParams(map[string]any{"value": argument.Value})
		if err != nil {
			return fmt.Errorf("arch: architecture generator argument %q: %w", argument.Name, err)
		}
		if !gorapide.CanonicalValueMatchesPredefinedType(value["value"], argument.Type) {
			return fmt.Errorf("%w: architecture generator argument %q does not match %s",
				ErrActionTypeMismatch, argument.Name, argument.Type)
		}
		seen[key] = true
		normalized[index] = ArchitectureGeneratorArgument{
			Name: argument.Name, Type: argument.Type, Value: value["value"],
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return fmt.Errorf("arch: architecture %q is running", a.Name)
	}
	a.generatorArguments = normalized
	return nil
}

// SetReturnInterface declares the interface returned by this architecture.
// Passing nil is rejected because even an architecture with an omitted return
// clause has Stanford's predefined empty Root interface.
func (a *Architecture) SetReturnInterface(iface *InterfaceDecl) error {
	if iface == nil {
		return fmt.Errorf("arch: architecture return interface is nil")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return fmt.Errorf("arch: architecture %q is running", a.Name)
	}
	a.returnInterface = iface
	return nil
}

// ReturnInterface returns the enclosing architecture's boundary interface.
// It is distinct from every entry returned by Components.
func (a *Architecture) ReturnInterface() *InterfaceDecl {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.returnInterface
}

// AddFiniteIteratorGenerator declares one closed zero-parameter Iterator(T)
// module generator as canonical architecture model content.
func (a *Architecture) AddFiniteIteratorGenerator(generator *FiniteIteratorGenerator) error {
	normalized, err := normalizeFiniteIteratorGenerator(generator)
	if err != nil {
		return err
	}
	name := normalized.name
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.iteratorGenerators[name]; exists {
		return fmt.Errorf("%w: duplicate generator %q", ErrInvalidFiniteIteratorGenerator, name)
	}
	a.iteratorGenerators[name] = normalized
	return nil
}

// AddFiniteIteratorModule declares one closed allocation-identified
// Iterator(T) implementation as canonical architecture model content.
func (a *Architecture) AddFiniteIteratorModule(module *FiniteIteratorModule) error {
	normalized, err := normalizeFiniteIteratorModule(module)
	if err != nil {
		return err
	}
	identity := normalized.module.Identity()
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.finiteIterators[identity]; exists {
		return fmt.Errorf("%w: duplicate module %q", ErrInvalidFiniteIteratorModule, identity)
	}
	a.finiteIterators[identity] = normalized
	return nil
}

// AddComponent registers a component in the architecture.
// The component's poset is wired to the architecture's shared poset.
func (a *Architecture) AddComponent(c *Component) error {
	return a.AddComponentInArchitecture(c, ArchitectureInterfaceID)
}

// AddComponentInArchitecture registers a deterministic component as owned by
// the root or by one declared nested architecture instance.
func (a *Architecture) AddComponentInArchitecture(c *Component, architectureID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c == nil {
		return fmt.Errorf("arch: component is nil")
	}
	if c.ID == ArchitectureInterfaceID {
		return fmt.Errorf("arch: component ID %q is reserved for the architecture interface", c.ID)
	}
	if _, exists := a.components[c.ID]; exists {
		return fmt.Errorf("arch: component %q already exists", c.ID)
	}
	if _, exists := a.architectureInstances[c.ID]; exists {
		return fmt.Errorf("arch: component %q conflicts with a deterministic architecture instance", c.ID)
	}
	if architectureID == "" {
		architectureID = ArchitectureInterfaceID
	}
	if architectureID != ArchitectureInterfaceID {
		if _, exists := a.architectureInstances[architectureID]; !exists {
			return fmt.Errorf("arch: component %q references undeclared architecture instance %q", c.ID, architectureID)
		}
	}
	c.poset = a.poset
	c.onEmit = a.notify
	a.components[c.ID] = c
	a.componentArchitectures[c.ID] = architectureID
	return nil
}

// AddConnection registers a connection rule.
// Validates that referenced component IDs exist (unless wildcards).
func (a *Architecture) AddConnection(conn *Connection) error {
	architectureID := ArchitectureInterfaceID
	if conn != nil && conn.ArchitectureInstance != "" {
		architectureID = conn.ArchitectureInstance
	}
	return a.AddConnectionInArchitecture(conn, architectureID)
}

// AddConnectionInArchitecture registers a connection in one static
// architecture scope. A child scope can address its own returned interface by
// the child instance ID; a parent scope sees that same ID as a component-like
// architecture value.
func (a *Architecture) AddConnectionInArchitecture(conn *Connection, architectureID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("arch: connection is nil")
	}
	if architectureID == "" {
		architectureID = ArchitectureInterfaceID
	}
	if conn.ArchitectureInstance != "" && conn.ArchitectureInstance != architectureID {
		return fmt.Errorf("arch: connection %q declares architecture %q, not %q",
			conn.ID, conn.ArchitectureInstance, architectureID)
	}
	if architectureID != ArchitectureInterfaceID {
		if _, exists := a.architectureInstances[architectureID]; !exists {
			return fmt.Errorf("arch: connection %q references undeclared architecture instance %q", conn.ID, architectureID)
		}
	}
	if conn.From != "*" {
		if !a.connectionEndpointVisibleLocked(architectureID, conn.From) {
			return fmt.Errorf("arch: source %q is not visible in architecture %q", conn.From, architectureID)
		}
	}
	if conn.To != "*" {
		if !a.connectionEndpointVisibleLocked(architectureID, conn.To) {
			return fmt.Errorf("arch: target %q is not visible in architecture %q", conn.To, architectureID)
		}
	}
	conn.ArchitectureInstance = architectureID
	a.connections = append(a.connections, conn)
	return nil
}

func (a *Architecture) connectionEndpointVisibleLocked(architectureID, endpointID string) bool {
	if endpointID == architectureID ||
		(architectureID == ArchitectureInterfaceID && endpointID == ArchitectureInterfaceID) {
		return true
	}
	if owner, exists := a.componentArchitectures[endpointID]; exists {
		return owner == architectureID
	}
	if declaration, exists := a.architectureInstances[endpointID]; exists {
		return declaration.Parent == architectureID
	}
	if architectureID == ArchitectureInterfaceID {
		_, exists := a.subArchitectures[endpointID]
		return exists
	}
	return false
}

// AddFunctionConnection registers one static synchronous function alias. At an
// architecture boundary, the returned interface has the polarity defined by
// the Architecture LRM: provides is a legal source and requires is a legal
// target.
func (a *Architecture) AddFunctionConnection(connection *FunctionConnection) error {
	architectureID := ArchitectureInterfaceID
	if connection != nil && connection.ArchitectureInstance != "" {
		architectureID = connection.ArchitectureInstance
	}
	return a.AddFunctionConnectionInArchitecture(connection, architectureID)
}

// AddFunctionConnectionInArchitecture registers a synchronous alias in one
// static architecture scope. Both endpoints must be constituents directly
// visible in that scope; either endpoint may be that scope's own boundary.
func (a *Architecture) AddFunctionConnectionInArchitecture(connection *FunctionConnection, architectureID string) error {
	if a == nil {
		return fmt.Errorf("arch: architecture is nil")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if connection == nil {
		return fmt.Errorf("arch: function connection is nil")
	}
	if architectureID == "" {
		architectureID = ArchitectureInterfaceID
	}
	if connection.ArchitectureInstance != "" && connection.ArchitectureInstance != architectureID {
		return fmt.Errorf("arch: function connection %q declares architecture %q, not %q",
			connection.ID, connection.ArchitectureInstance, architectureID)
	}
	if architectureID != ArchitectureInterfaceID {
		if _, exists := a.architectureInstances[architectureID]; !exists {
			return fmt.Errorf("arch: function connection %q references undeclared architecture instance %q", connection.ID, architectureID)
		}
	}
	if !a.connectionEndpointVisibleLocked(architectureID, connection.From) {
		return fmt.Errorf("arch: function source %q is not visible in architecture %q", connection.From, architectureID)
	}
	if !a.connectionEndpointVisibleLocked(architectureID, connection.To) {
		return fmt.Errorf("arch: function target %q is not visible in architecture %q", connection.To, architectureID)
	}
	snapshot := copyFunctionConnection(connection)
	snapshot.ArchitectureInstance = architectureID
	a.functionConnections = append(a.functionConnections, snapshot)
	return nil
}

// Component looks up a component by ID.
func (a *Architecture) Component(id string) (*Component, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.components[id]
	return c, ok
}

// Components returns all registered components, sorted by ID.
func (a *Architecture) Components() []*Component {
	a.mu.RLock()
	defer a.mu.RUnlock()
	comps := make([]*Component, 0, len(a.components))
	for _, c := range a.components {
		comps = append(comps, c)
	}
	sort.Slice(comps, func(i, j int) bool {
		return comps[i].ID < comps[j].ID
	})
	return comps
}

// Poset returns the architecture's shared legacy-runtime poset for inspection.
// Deterministic execution uses and returns a fresh poset through
// ExecutionResult and neither reads nor mutates this value.
func (a *Architecture) Poset() *gorapide.Poset {
	return a.poset
}

// Bind creates a dynamic binding from one component to another.
// Both components must already be registered in the architecture.
//
// Deprecated: dynamic bindings use the legacy asynchronous adapter and are
// rejected by deterministic model validation.
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

// Unbind removes all bindings originating from the given component.
//
// Deprecated: dynamic bindings are outside the deterministic trusted core.
func (a *Architecture) Unbind(from string) error {
	return a.bindings.Unbind(from)
}

// BindWith creates a binding with options and returns the binding ID.
// Both components must already be registered in the architecture.
//
// Deprecated: dynamic bindings and callback maps are outside the deterministic
// trusted core.
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

// UnbindByID removes a specific binding by ID.
//
// Deprecated: dynamic bindings are outside the deterministic trusted core.
func (a *Architecture) UnbindByID(id string) error {
	return a.bindings.UnbindByID(id)
}

// Bindings returns all active legacy-runtime bindings.
//
// Deprecated: dynamic bindings are outside the deterministic trusted core.
func (a *Architecture) Bindings() []*Binding {
	return a.bindings.ActiveBindings()
}

// AddSubArchitecture registers a sub-architecture in the parent architecture.
// The sub-architecture's ID must not conflict with any component ID.
//
// Deprecated: SubArchitecture is the goroutine/channel adapter and is rejected
// by deterministic model validation. Use deterministic architecture instance
// declarations for Rapide hierarchy.
func (a *Architecture) AddSubArchitecture(sa *SubArchitecture) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if sa == nil {
		return fmt.Errorf("arch: sub-architecture is nil")
	}
	if _, exists := a.components[sa.id]; exists {
		return fmt.Errorf("arch: ID %q already used by a component", sa.id)
	}
	if _, exists := a.architectureInstances[sa.id]; exists {
		return fmt.Errorf("arch: ID %q already used by a deterministic architecture instance", sa.id)
	}
	if _, exists := a.subArchitectures[sa.id]; exists {
		return fmt.Errorf("arch: sub-architecture %q already exists", sa.id)
	}
	sa.onEmit = a.notify
	sa.onError = a.recordLegacyError
	sa.parentPoset = a.poset
	a.subArchitectures[sa.id] = sa
	return nil
}

// SubArchitecture looks up a legacy goroutine/channel sub-architecture by ID.
//
// Deprecated: use deterministic architecture instance declarations for
// replayable Rapide hierarchy.
func (a *Architecture) SubArchitecture(id string) (*SubArchitecture, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	sa, ok := a.subArchitectures[id]
	return sa, ok
}

// WithConstraints configures constraint checking for the architecture.
func (a *Architecture) WithConstraints(cs *constraint.ConstraintSet, mode constraint.CheckMode) *Architecture {
	a.constraintSet = cs
	a.constraintMode = mode
	return a
}

// WithConstraintsOpts configures constraint checking with additional
// checker options (e.g., batch size, interval, callbacks).
//
// Deprecated: checker scheduling and callbacks are outside the deterministic
// trusted core. Attach a closed ConstraintSet with WithConstraints and consume
// the canonical report from ExecutionResult.
func (a *Architecture) WithConstraintsOpts(cs *constraint.ConstraintSet, mode constraint.CheckMode, opts func(*constraint.Checker)) *Architecture {
	a.constraintSet = cs
	a.constraintMode = mode
	a.checkerOpts = opts
	return a
}

// CheckConstraints manually runs all configured constraints against the
// current poset and returns violations. If a checker has been run, returns
// its accumulated violations; otherwise runs a one-shot check.
func (a *Architecture) CheckConstraints() []constraint.ConstraintViolation {
	a.mu.RLock()
	ch := a.checker
	cs := a.constraintSet
	a.mu.RUnlock()

	if ch != nil {
		return ch.Violations()
	}
	if cs != nil {
		return cs.Check(a.poset)
	}
	return nil
}

// ConstraintReport returns a formatted report from the checker, or runs
// a one-shot check if no checker has been configured.
func (a *Architecture) ConstraintReport() string {
	a.mu.RLock()
	ch := a.checker
	cs := a.constraintSet
	a.mu.RUnlock()

	if ch != nil {
		return ch.Report()
	}
	if cs != nil {
		_, report := cs.CheckAndReport(a.poset)
		return report
	}
	return "No constraints configured.\n"
}

// Start starts all components and the connection router.
//
// Deprecated: Start is the legacy goroutine/channel execution adapter. Use
// ExecuteDeterministic with an explicit ExecutionJournal for semantic work.
func (a *Architecture) Start(ctx context.Context) error {
	if a == nil {
		return fmt.Errorf("%w: architecture is nil", ErrLegacyRuntimeFailure)
	}
	if ctx == nil {
		return fmt.Errorf("%w: architecture %q start context is nil", ErrLegacyRuntimeFailure, a.Name)
	}
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil
	}
	a.running = true
	a.legacyErr = nil
	a.done = make(chan struct{})
	a.ctx, a.cancel = context.WithCancel(ctx)

	// Start constraint checker if configured.
	if a.constraintSet != nil {
		ch := constraint.NewChecker(a.constraintSet, a.constraintMode)
		if a.checkerOpts != nil {
			a.checkerOpts(ch)
		}
		a.checker = ch
		ch.Run(a.ctx, a.poset)
	}
	components := make([]*Component, 0, len(a.components))
	for _, component := range a.components {
		components = append(components, component)
	}
	subs := make([]*SubArchitecture, 0, len(a.subArchitectures))
	for _, sa := range a.subArchitectures {
		subs = append(subs, sa)
	}
	routerCtx := a.ctx
	a.mu.Unlock()

	for _, component := range components {
		if component == nil {
			err := fmt.Errorf("architecture %q has a nil legacy component", a.Name)
			a.recordLegacyError(err)
			_ = a.Stop()
			return a.LegacyError()
		}
	}
	for _, sa := range subs {
		if sa == nil {
			err := fmt.Errorf("architecture %q has a nil legacy sub-architecture", a.Name)
			a.recordLegacyError(err)
			_ = a.Stop()
			return a.LegacyError()
		}
	}
	sort.Slice(components, func(i, j int) bool { return components[i].ID < components[j].ID })
	sort.Slice(subs, func(i, j int) bool { return subs[i].id < subs[j].id })

	// Start router first so it's ready for events.
	go a.runRouter(routerCtx)

	// Start all components.
	for _, c := range components {
		c.Start(routerCtx)
	}
	// Start all sub-architectures.
	for _, sa := range subs {
		if err := sa.StartChecked(routerCtx); err != nil {
			a.recordLegacyError(fmt.Errorf("architecture %q sub-architecture %q start: %w", a.Name, sa.id, err))
			_ = a.Stop()
			return a.LegacyError()
		}
	}
	return nil
}

// Stop gracefully stops all components and the router.
//
// Deprecated: Stop controls only the legacy asynchronous adapter.
func (a *Architecture) Stop() error {
	if a == nil {
		return fmt.Errorf("%w: architecture is nil", ErrLegacyRuntimeFailure)
	}
	a.mu.Lock()
	if !a.running {
		err := a.legacyErr
		a.mu.Unlock()
		return err
	}
	a.running = false
	ch := a.checker
	components := make([]*Component, 0, len(a.components))
	for _, component := range a.components {
		components = append(components, component)
	}
	subs := make([]*SubArchitecture, 0, len(a.subArchitectures))
	for _, sa := range a.subArchitectures {
		subs = append(subs, sa)
	}
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	sort.Slice(components, func(i, j int) bool {
		if components[i] == nil {
			return components[j] != nil
		}
		if components[j] == nil {
			return false
		}
		return components[i].ID < components[j].ID
	})
	for _, component := range components {
		if component != nil {
			component.Stop()
		}
	}
	for _, sa := range subs {
		if sa == nil {
			a.recordLegacyError(fmt.Errorf("architecture %q has a nil legacy sub-architecture during stop", a.Name))
		}
	}
	sort.Slice(subs, func(i, j int) bool {
		if subs[i] == nil {
			return subs[j] != nil
		}
		if subs[j] == nil {
			return false
		}
		return subs[i].id < subs[j].id
	})

	// Stop all sub-architectures so their inner checkers shut down.
	for _, sa := range subs {
		if sa == nil {
			continue
		}
		if err := sa.StopChecked(); err != nil {
			a.recordLegacyError(fmt.Errorf("architecture %q sub-architecture %q stop: %w", a.Name, sa.id, err))
		}
	}

	if ch != nil {
		ch.Stop()
	}
	return a.LegacyError()
}

// Wait blocks until all components and the router have stopped.
//
// Deprecated: Wait controls only the legacy asynchronous adapter.
func (a *Architecture) Wait() {
	if a == nil {
		return
	}
	// Wait for router.
	a.mu.RLock()
	d := a.done
	ch := a.checker
	a.mu.RUnlock()
	if d != nil {
		<-d
	}
	// Wait for all components.
	a.mu.RLock()
	comps := make([]*Component, 0, len(a.components))
	for _, c := range a.components {
		comps = append(comps, c)
	}
	a.mu.RUnlock()
	sort.Slice(comps, func(i, j int) bool {
		if comps[i] == nil {
			return comps[j] != nil
		}
		if comps[j] == nil {
			return false
		}
		return comps[i].ID < comps[j].ID
	})
	for _, c := range comps {
		if c != nil {
			c.Wait()
		}
	}
	// Wait for all sub-architectures.
	a.mu.RLock()
	subs := make([]*SubArchitecture, 0, len(a.subArchitectures))
	for _, sa := range a.subArchitectures {
		subs = append(subs, sa)
	}
	a.mu.RUnlock()
	sort.Slice(subs, func(i, j int) bool {
		if subs[i] == nil {
			return subs[j] != nil
		}
		if subs[j] == nil {
			return false
		}
		return subs[i].id < subs[j].id
	})
	for _, sa := range subs {
		if sa != nil {
			sa.Wait()
		}
	}
	// Wait for checker.
	if ch != nil {
		ch.Wait()
	}
}

// WaitError waits for the deprecated asynchronous adapter and returns its
// first recorded routing or delivery failure. Deterministic callers must use a
// prepared architecture instead.
func (a *Architecture) WaitError() error {
	a.Wait()
	return a.LegacyError()
}

// LegacyError returns the first explicit failure recorded by the deprecated
// asynchronous adapter.
func (a *Architecture) LegacyError() error {
	if a == nil {
		return fmt.Errorf("%w: architecture is nil", ErrLegacyRuntimeFailure)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.legacyErr
}

func (a *Architecture) recordLegacyError(err error) {
	if a == nil || err == nil {
		return
	}
	a.mu.Lock()
	if a.legacyErr == nil {
		a.legacyErr = fmt.Errorf("%w: %w", ErrLegacyRuntimeFailure, err)
	}
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Inject creates an external event (no source component) and adds it
// to the architecture's poset, triggering connection rules.
//
// Deprecated: Inject creates a random, wall-clock event in the legacy runtime.
// Put external observations in an explicit ExecutionJournal instead.
func (a *Architecture) Inject(name string, params map[string]any) *gorapide.Event {
	e, err := a.InjectChecked(name, params)
	if err != nil {
		panic(fmt.Sprintf("arch.Architecture.Inject: %v", err))
	}
	return e
}

// InjectChecked is the explicit-error form of legacy external injection. It
// remains outside the trusted kernel but cannot silently lose router
// notification.
func (a *Architecture) InjectChecked(name string, params map[string]any) (*gorapide.Event, error) {
	if a == nil || a.poset == nil {
		return nil, fmt.Errorf("%w: architecture or poset is nil", ErrLegacyRuntimeFailure)
	}
	e := gorapide.NewEvent(name, "", params)
	if err := a.poset.AddEvent(e); err != nil {
		return nil, fmt.Errorf("%w: inject poset insertion: %w", ErrLegacyRuntimeFailure, err)
	}
	if err := a.notify(e); err != nil {
		return e, err
	}
	return e, nil
}

// notify sends an event to the router's notification channel.
func (a *Architecture) notify(e *gorapide.Event) error {
	if a == nil || a.events == nil || e == nil {
		return fmt.Errorf("%w: router notification has nil architecture, channel, or event", ErrDeliveryRejected)
	}
	select {
	case a.events <- e:
		return nil
	default:
		return fmt.Errorf("%w: architecture %q router queue is full for action %q from %q", ErrDeliveryRejected, a.Name, e.Name, e.Source)
	}
}

// runRouter is the connection routing goroutine. It reads events from
// the notification channel and evaluates connection rules.
func (a *Architecture) runRouter(ctx context.Context) {
	defer close(a.done)
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-a.events:
			if !ok {
				return
			}
			if ctx.Err() != nil {
				return
			}
			if err := a.processEventCascade(e); err != nil {
				a.recordLegacyError(err)
				return
			}
		}
	}
}

// processEventCascade processes an event and any events created by
// connection executions (cascading).
func (a *Architecture) processEventCascade(e *gorapide.Event) error {
	if a == nil || e == nil {
		return fmt.Errorf("legacy architecture cascade requires a non-nil architecture and event")
	}
	queue := []*gorapide.Event{e}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		a.mu.RLock()
		conns := make([]*Connection, len(a.connections))
		copy(conns, a.connections)
		a.mu.RUnlock()

		for _, conn := range conns {
			if conn == nil {
				return fmt.Errorf("architecture %q has a nil legacy connection while routing action %q", a.Name, current.Name)
			}
			targets := a.resolveTargets(conn, current)
			for _, target := range targets {
				source := a.resolveSource(conn, current)
				newEvent, err := conn.execute(current, source, target)
				if err != nil {
					return fmt.Errorf("architecture %q connection %q target %q action %q: %w", a.Name, conn.ID, target.ID, current.Name, err)
				}
				if newEvent != nil {
					queue = append(queue, newEvent)
				}
			}
		}

		// Evaluate dynamic bindings.
		if a.bindings == nil {
			return fmt.Errorf("architecture %q has no legacy binding manager", a.Name)
		}
		bindings := a.bindings.BindingsFrom(current.Source)
		for _, binding := range bindings {
			if binding == nil {
				return fmt.Errorf("architecture %q has a nil legacy binding for source %q", a.Name, current.Source)
			}
			a.mu.RLock()
			target, targetOK := a.components[binding.ToComp]
			a.mu.RUnlock()
			if !targetOK {
				return fmt.Errorf("architecture %q binding %q target component %q is missing", a.Name, binding.ID, binding.ToComp)
			}
			newEvents, err := a.bindings.executeBinding(binding, current, target, a.poset)
			if err != nil {
				return fmt.Errorf("architecture %q binding %q action %q: %w", a.Name, binding.ID, current.Name, err)
			}
			queue = append(queue, newEvents...)
		}

		// Evaluate sub-architecture import rules.
		a.mu.RLock()
		subs := make([]*SubArchitecture, 0, len(a.subArchitectures))
		for _, sa := range a.subArchitectures {
			subs = append(subs, sa)
		}
		a.mu.RUnlock()
		for _, sa := range subs {
			if sa == nil {
				return fmt.Errorf("architecture %q has a nil legacy sub-architecture", a.Name)
			}
		}
		sort.Slice(subs, func(i, j int) bool {
			return subs[i].id < subs[j].id
		})
		for _, sa := range subs {
			for _, rule := range sa.importRules {
				if current.Name == rule.OuterEvent {
					if err := sa.SendChecked(current); err != nil {
						return fmt.Errorf("architecture %q sub-architecture %q import action %q: %w", a.Name, sa.id, current.Name, err)
					}
					break
				}
			}
		}

		// Notify global observers.
		a.mu.RLock()
		observers := make([]func(*gorapide.Event), len(a.onEvent))
		copy(observers, a.onEvent)
		eventChecker := a.checker
		a.mu.RUnlock()
		for index, fn := range observers {
			if fn == nil {
				return fmt.Errorf("architecture %q observer %d is nil", a.Name, index)
			}
			fn(current)
		}
		// Notify checker for CheckOnEvent mode.
		if eventChecker != nil {
			eventChecker.NotifyEvent()
		}
	}
	return nil
}

// resolveTargets returns the target components for a connection given
// a trigger event, or nil if the connection doesn't match.
func (a *Architecture) resolveTargets(conn *Connection, e *gorapide.Event) []*Component {
	// Check source match.
	if conn.From != "*" && e.Source != conn.From {
		return nil
	}

	// Check trigger pattern.
	if conn.Trigger != nil {
		view := newObservationView(gorapide.EventSet{e}, a.poset)
		if len(conn.Trigger.Match(view)) == 0 {
			return nil
		}
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	if conn.To == "*" {
		var targets []*Component
		for _, c := range a.components {
			if c.ID != e.Source { // don't send back to source
				targets = append(targets, c)
			}
		}
		sort.Slice(targets, func(i, j int) bool {
			return targets[i].ID < targets[j].ID
		})
		return targets
	}

	if target, ok := a.components[conn.To]; ok {
		return []*Component{target}
	}
	return nil
}

// resolveSource returns the source component for a connection, or nil.
func (a *Architecture) resolveSource(conn *Connection, e *gorapide.Event) *Component {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if c, ok := a.components[e.Source]; ok {
		return c
	}
	return nil
}

// compile-time check that observationView satisfies pattern.PosetReader.
var _ pattern.PosetReader = (*observationView)(nil)
