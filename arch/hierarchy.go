package arch

import (
	"context"
	"fmt"
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
//
// Deprecated: SubArchitecture is the goroutine/channel compatibility adapter
// and is rejected by deterministic model validation. Use deterministic nested
// architecture declarations in the single semantic kernel.
type SubArchitecture struct {
	id    string
	iface *InterfaceDecl
	inner *Architecture

	exportRules []ExportRule
	importRules []ImportRule

	// Set by parent architecture during AddSubArchitecture.
	onEmit      func(*gorapide.Event) error
	onError     func(error)
	parentPoset *gorapide.Poset

	inbox   chan *gorapide.Event
	bufSize int

	mu                      sync.Mutex
	running                 bool
	cancel                  context.CancelFunc
	done                    chan struct{}
	legacyErr               error
	exportObserverInstalled bool
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
	return sa.SendChecked(e) == nil
}

// SendChecked performs one non-blocking legacy import delivery and returns a
// typed error when the bridge cannot accept the event.
func (sa *SubArchitecture) SendChecked(e *gorapide.Event) error {
	if sa == nil {
		return fmt.Errorf("%w: sub-architecture is nil", ErrDeliveryRejected)
	}
	if e == nil {
		return fmt.Errorf("%w: sub-architecture %q event is nil", ErrDeliveryRejected, sa.id)
	}
	if sa.inbox == nil {
		return fmt.Errorf("%w: sub-architecture %q inbox is nil", ErrDeliveryRejected, sa.id)
	}
	select {
	case sa.inbox <- e:
		return nil
	default:
		return fmt.Errorf("%w: sub-architecture %q inbox is full", ErrDeliveryRejected, sa.id)
	}
}

// Start starts the inner architecture and the boundary bridge goroutine.
func (sa *SubArchitecture) Start(ctx context.Context) {
	if err := sa.StartChecked(ctx); err != nil {
		sa.recordLegacyError(err)
	}
}

// StartChecked starts the deprecated hierarchy adapter while returning
// construction and inner-start failures explicitly.
func (sa *SubArchitecture) StartChecked(ctx context.Context) error {
	if sa == nil {
		return fmt.Errorf("%w: sub-architecture is nil", ErrLegacyRuntimeFailure)
	}
	if ctx == nil {
		return fmt.Errorf("%w: sub-architecture %q start context is nil", ErrLegacyRuntimeFailure, sa.id)
	}
	if sa.inner == nil {
		return fmt.Errorf("%w: sub-architecture %q inner architecture is nil", ErrLegacyRuntimeFailure, sa.id)
	}
	if sa.inbox == nil {
		return fmt.Errorf("%w: sub-architecture %q inbox is nil", ErrLegacyRuntimeFailure, sa.id)
	}

	sa.mu.Lock()
	if sa.running {
		sa.mu.Unlock()
		return nil
	}
	sa.running = true
	sa.legacyErr = nil
	sa.done = make(chan struct{})

	bridgeCtx, cancel := context.WithCancel(ctx)
	sa.cancel = cancel
	installObserver := !sa.exportObserverInstalled
	sa.exportObserverInstalled = true
	sa.mu.Unlock()

	// Install export observer on inner architecture BEFORE starting it.
	if installObserver {
		sa.inner.mu.Lock()
		sa.inner.onEvent = append(sa.inner.onEvent, sa.handleExport)
		sa.inner.mu.Unlock()
	}

	// Start inner architecture.
	if err := sa.inner.Start(bridgeCtx); err != nil {
		cancel()
		sa.mu.Lock()
		sa.running = false
		close(sa.done)
		sa.mu.Unlock()
		return fmt.Errorf("%w: sub-architecture %q inner start: %w", ErrLegacyRuntimeFailure, sa.id, err)
	}

	// Start import bridge goroutine.
	go sa.runImportBridge(bridgeCtx)
	go sa.watchInner()
	return nil
}

// Stop stops the inner architecture and the bridge.
func (sa *SubArchitecture) Stop() {
	if err := sa.StopChecked(); err != nil {
		sa.recordLegacyError(err)
	}
}

// StopChecked stops the hierarchy adapter and returns any retained bridge or
// inner-architecture failure.
func (sa *SubArchitecture) StopChecked() error {
	if sa == nil {
		return fmt.Errorf("%w: sub-architecture is nil", ErrLegacyRuntimeFailure)
	}
	sa.mu.Lock()
	if !sa.running {
		err := sa.legacyErr
		sa.mu.Unlock()
		return err
	}
	sa.running = false
	inner := sa.inner
	cancel := sa.cancel
	sa.mu.Unlock()

	var stopErr error
	if inner == nil {
		stopErr = fmt.Errorf("%w: sub-architecture %q inner architecture is nil", ErrLegacyRuntimeFailure, sa.id)
	} else if err := inner.Stop(); err != nil {
		stopErr = fmt.Errorf("%w: sub-architecture %q inner stop: %w", ErrLegacyRuntimeFailure, sa.id, err)
	}
	if cancel != nil {
		cancel()
	}
	if stopErr != nil {
		sa.recordLegacyError(stopErr)
	}
	return sa.LegacyError()
}

// Wait blocks until the inner architecture and bridge have stopped.
func (sa *SubArchitecture) Wait() {
	if sa == nil {
		return
	}
	if sa.inner != nil {
		sa.inner.Wait()
	}
	sa.mu.Lock()
	d := sa.done
	sa.mu.Unlock()
	if d != nil {
		<-d
	}
}

// WaitError waits for the deprecated hierarchy adapter and returns its first
// retained bridge or inner-architecture failure.
func (sa *SubArchitecture) WaitError() error {
	sa.Wait()
	return sa.LegacyError()
}

// LegacyError returns the first failure retained by the deprecated hierarchy
// adapter.
func (sa *SubArchitecture) LegacyError() error {
	if sa == nil {
		return fmt.Errorf("%w: sub-architecture is nil", ErrLegacyRuntimeFailure)
	}
	sa.mu.Lock()
	defer sa.mu.Unlock()
	return sa.legacyErr
}

func (sa *SubArchitecture) recordLegacyError(err error) {
	if sa == nil || err == nil {
		return
	}
	sa.mu.Lock()
	if sa.legacyErr == nil {
		sa.legacyErr = fmt.Errorf("%w: sub-architecture %q: %w", ErrLegacyRuntimeFailure, sa.id, err)
	}
	cancel := sa.cancel
	onError := sa.onError
	retained := sa.legacyErr
	sa.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if onError != nil {
		onError(retained)
	}
}

func (sa *SubArchitecture) watchInner() {
	if err := sa.inner.WaitError(); err != nil {
		sa.recordLegacyError(fmt.Errorf("inner architecture: %w", err))
	}
}

// handleExport is registered as a WithObserver on the inner architecture.
// When an inner event matches an export rule, it creates a new event
// visible to the parent architecture.
func (sa *SubArchitecture) handleExport(e *gorapide.Event) {
	if err := sa.handleExportChecked(e); err != nil {
		sa.recordLegacyError(err)
	}
}

func (sa *SubArchitecture) handleExportChecked(e *gorapide.Event) error {
	if sa == nil || e == nil {
		return fmt.Errorf("legacy sub-architecture export requires a non-nil bridge and event")
	}
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
		if sa.parentPoset == nil {
			return fmt.Errorf("sub-architecture %q export %q has no parent poset", sa.id, rule.OuterEvent)
		}
		if err := sa.parentPoset.AddEvent(exported); err != nil {
			return fmt.Errorf("sub-architecture %q export %q parent insertion: %w", sa.id, rule.OuterEvent, err)
		}

		// Notify parent router.
		if sa.onEmit == nil {
			return fmt.Errorf("%w: sub-architecture %q export %q has no parent notifier", ErrDeliveryRejected, sa.id, rule.OuterEvent)
		}
		if err := sa.onEmit(exported); err != nil {
			return fmt.Errorf("sub-architecture %q export %q parent notification: %w", sa.id, rule.OuterEvent, err)
		}
	}
	return nil
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
			if ctx.Err() != nil {
				return
			}
			if err := sa.processImport(e); err != nil {
				sa.recordLegacyError(err)
				return
			}
		}
	}
}

// processImport applies import rules to a received event.
func (sa *SubArchitecture) processImport(e *gorapide.Event) error {
	if sa == nil || sa.inner == nil || e == nil {
		return fmt.Errorf("legacy sub-architecture import requires a non-nil bridge, inner architecture, and event")
	}
	matched := false
	for _, rule := range sa.importRules {
		if e.Name != rule.OuterEvent {
			continue
		}
		matched = true

		params := copyEventParams(e)
		if rule.Transform != nil {
			params = rule.Transform(e)
		}

		if rule.InnerTarget != "" {
			// Route directly to target component inside the inner architecture.
			target, ok := sa.inner.Component(rule.InnerTarget)
			if !ok {
				return fmt.Errorf("sub-architecture %q import %q target component %q is missing", sa.id, rule.OuterEvent, rule.InnerTarget)
			}
			inner := gorapide.NewEvent(rule.InnerEvent, rule.InnerTarget, params)
			if sa.inner.Poset() == nil {
				return fmt.Errorf("sub-architecture %q import %q inner poset is nil", sa.id, rule.OuterEvent)
			}
			if err := sa.inner.Poset().AddEvent(inner); err != nil {
				return fmt.Errorf("sub-architecture %q import %q inner insertion: %w", sa.id, rule.OuterEvent, err)
			}
			if err := target.SendChecked(inner); err != nil {
				return fmt.Errorf("sub-architecture %q import %q target %q delivery: %w", sa.id, rule.OuterEvent, rule.InnerTarget, err)
			}
			// Also notify inner router so connections/observers see it.
			if err := sa.inner.notify(inner); err != nil {
				return fmt.Errorf("sub-architecture %q import %q inner notification: %w", sa.id, rule.OuterEvent, err)
			}
		} else {
			// Broadcast into inner architecture.
			if _, err := sa.inner.InjectChecked(rule.InnerEvent, params); err != nil {
				return fmt.Errorf("sub-architecture %q import %q inner injection: %w", sa.id, rule.OuterEvent, err)
			}
		}
	}
	if !matched {
		return fmt.Errorf("%w: sub-architecture %q has no import rule for action %q", ErrDeliveryRejected, sa.id, e.Name)
	}
	return nil
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
//
// Deprecated: use deterministic nested architecture declarations for
// replayable Rapide hierarchy.
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
