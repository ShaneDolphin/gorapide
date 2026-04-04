package arch

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/beautiful-majestic-dolphin/gorapide"
	"github.com/beautiful-majestic-dolphin/gorapide/constraint"
	"github.com/beautiful-majestic-dolphin/gorapide/pattern"
)

// Architecture composes components, connections, and constraints into
// a runnable Rapide system.
type Architecture struct {
	Name        string
	components  map[string]*Component
	connections []*Connection
	poset       *gorapide.Poset
	onEvent     []func(*gorapide.Event)
	events      chan *gorapide.Event // router notification channel

	constraintSet  *constraint.ConstraintSet
	constraintMode constraint.CheckMode
	checker        *constraint.Checker
	checkerOpts    func(*constraint.Checker)

	mu      sync.RWMutex
	running bool
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{} // closed when router exits
}

// ArchOption configures an Architecture.
type ArchOption func(*Architecture)

// WithPoset uses an existing poset instead of creating a new one.
func WithPoset(p *gorapide.Poset) ArchOption {
	return func(a *Architecture) {
		a.poset = p
	}
}

// WithObserver adds a global event observer callback.
func WithObserver(fn func(*gorapide.Event)) ArchOption {
	return func(a *Architecture) {
		a.onEvent = append(a.onEvent, fn)
	}
}

// NewArchitecture creates a new architecture with the given name.
func NewArchitecture(name string, opts ...ArchOption) *Architecture {
	a := &Architecture{
		Name:       name,
		components: make(map[string]*Component),
		events:     make(chan *gorapide.Event, 1024),
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.poset == nil {
		a.poset = gorapide.NewPoset()
	}
	return a
}

// AddComponent registers a component in the architecture.
// The component's poset is wired to the architecture's shared poset.
func (a *Architecture) AddComponent(c *Component) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.components[c.ID]; exists {
		return fmt.Errorf("arch: component %q already exists", c.ID)
	}
	c.poset = a.poset
	c.onEmit = a.notify
	a.components[c.ID] = c
	return nil
}

// AddConnection registers a connection rule.
// Validates that referenced component IDs exist (unless wildcards).
func (a *Architecture) AddConnection(conn *Connection) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if conn.From != "*" {
		if _, ok := a.components[conn.From]; !ok {
			return fmt.Errorf("arch: source component %q not found", conn.From)
		}
	}
	if conn.To != "*" {
		if _, ok := a.components[conn.To]; !ok {
			return fmt.Errorf("arch: target component %q not found", conn.To)
		}
	}
	a.connections = append(a.connections, conn)
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

// Poset returns the architecture's shared poset for inspection.
func (a *Architecture) Poset() *gorapide.Poset {
	return a.poset
}

// WithConstraints configures constraint checking for the architecture.
func (a *Architecture) WithConstraints(cs *constraint.ConstraintSet, mode constraint.CheckMode) *Architecture {
	a.constraintSet = cs
	a.constraintMode = mode
	return a
}

// WithConstraintsOpts configures constraint checking with additional
// checker options (e.g., batch size, interval, callbacks).
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
func (a *Architecture) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return nil
	}
	a.running = true
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

	// Start router first so it's ready for events.
	go a.runRouter(a.ctx)

	// Start all components.
	for _, c := range a.components {
		c.Start(a.ctx)
	}
	return nil
}

// Stop gracefully stops all components and the router.
func (a *Architecture) Stop() error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return nil
	}
	a.running = false
	ch := a.checker
	a.cancel()
	a.mu.Unlock()

	if ch != nil {
		ch.Stop()
	}
	return nil
}

// Wait blocks until all components and the router have stopped.
func (a *Architecture) Wait() {
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
	for _, c := range comps {
		c.Wait()
	}
	// Wait for checker.
	if ch != nil {
		ch.Wait()
	}
}

// Inject creates an external event (no source component) and adds it
// to the architecture's poset, triggering connection rules.
func (a *Architecture) Inject(name string, params map[string]any) *gorapide.Event {
	e := gorapide.NewEvent(name, "", params)
	if err := a.poset.AddEvent(e); err != nil {
		panic(fmt.Sprintf("arch.Architecture.Inject: %v", err))
	}
	a.notify(e)
	return e
}

// notify sends an event to the router's notification channel.
func (a *Architecture) notify(e *gorapide.Event) {
	select {
	case a.events <- e:
	default:
		// Buffer full — drop to avoid deadlock. Shouldn't happen with 1024 buffer.
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
		case e := <-a.events:
			a.processEventCascade(e)
		}
	}
}

// processEventCascade processes an event and any events created by
// connection executions (cascading).
func (a *Architecture) processEventCascade(e *gorapide.Event) {
	queue := []*gorapide.Event{e}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

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

// resolveTargets returns the target components for a connection given
// a trigger event, or nil if the connection doesn't match.
func (a *Architecture) resolveTargets(conn *Connection, e *gorapide.Event) []*Component {
	// Check source match.
	if conn.From != "*" && e.Source != conn.From {
		return nil
	}

	// Check trigger pattern.
	if conn.Trigger != nil {
		view := &observationView{
			observed: gorapide.EventSet{e},
			poset:    a.poset,
		}
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
