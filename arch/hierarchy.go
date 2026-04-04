package arch

import (
	"context"
	"sync"

	"github.com/ShaneDolphin/gorapide"
)

// ExportRule defines how an inner event becomes visible to the parent architecture.
type ExportRule struct {
	InnerSource string                               // component ID inside sub-arch (or "*")
	InnerEvent  string                               // event name inside
	OuterEvent  string                               // event name visible to parent
	Transform   func(*gorapide.Event) map[string]any // optional param transform
}

// ImportRule defines how a parent event is forwarded into the sub-architecture.
type ImportRule struct {
	OuterEvent  string                               // event name from parent
	InnerTarget string                               // component ID inside (or "" for Inject)
	InnerEvent  string                               // event name inside
	Transform   func(*gorapide.Event) map[string]any // optional param transform
}

// SubArchitecture wraps an Architecture to participate as a component
// in a parent architecture. It bridges events across the hierarchy
// boundary using export and import rules.
type SubArchitecture struct {
	id    string
	iface *InterfaceDecl
	inner *Architecture

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

		if rule.InnerTarget != "" {
			// Route directly to target component inside the inner architecture.
			target, ok := sa.inner.Component(rule.InnerTarget)
			if !ok {
				continue
			}
			inner := gorapide.NewEvent(rule.InnerEvent, rule.InnerTarget, params)
			sa.inner.Poset().AddEvent(inner)
			target.Send(inner)
			// Also notify inner router so connections/observers see it.
			sa.inner.notify(inner)
		} else {
			// Broadcast into inner architecture.
			sa.inner.Inject(rule.InnerEvent, params)
		}
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
