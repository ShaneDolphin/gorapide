package arch

import (
	"fmt"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// ConnectionKind distinguishes the three Rapide connection semantics.
type ConnectionKind int

const (
	BasicConnection ConnectionKind = iota // Async multicast, no causal link
	PipeConnection                        // Sync pipeline, causal link from trigger to effect
	AgentConnection                       // Shared observation, same event identity
)

func (k ConnectionKind) String() string {
	switch k {
	case BasicConnection:
		return "Basic"
	case PipeConnection:
		return "Pipe"
	case AgentConnection:
		return "Agent"
	}
	return fmt.Sprintf("ConnectionKind(%d)", int(k))
}

// Connection defines how events flow from one component to another.
type Connection struct {
	Kind       ConnectionKind
	From       string          // source component ID (or "*" for any)
	To         string          // target component ID (or "*" for any)
	Trigger    pattern.Pattern // pattern that triggers this connection
	ActionName string          // name of the action to send on target

	transform func(*gorapide.Event) map[string]any // optional param transform
	forward   bool                                  // for agent: forward original event
}

// Execute fires the connection, creating or forwarding events according
// to the connection kind.
func (conn *Connection) Execute(triggerEvent *gorapide.Event, source, target *Component) error {
	_, err := conn.execute(triggerEvent, source, target)
	return err
}

// execute is the internal version that also returns the created event
// (if any), used by the Architecture router for cascading.
func (conn *Connection) execute(triggerEvent *gorapide.Event, source, target *Component) (*gorapide.Event, error) {
	switch conn.Kind {
	case BasicConnection:
		return conn.executeBasic(triggerEvent, target)
	case PipeConnection:
		return conn.executePipe(triggerEvent, target)
	case AgentConnection:
		target.Send(triggerEvent)
		return nil, nil
	default:
		return nil, fmt.Errorf("arch.Connection.Execute: unknown kind %d", conn.Kind)
	}
}

func (conn *Connection) executeBasic(triggerEvent *gorapide.Event, target *Component) (*gorapide.Event, error) {
	params := conn.resolveParams(triggerEvent)
	e := gorapide.NewEvent(conn.ActionName, target.ID, params)
	// No causal link — add event independently.
	if err := target.poset.AddEvent(e); err != nil {
		return nil, fmt.Errorf("arch.Connection.executeBasic: %w", err)
	}
	target.Send(e)
	return e, nil
}

func (conn *Connection) executePipe(triggerEvent *gorapide.Event, target *Component) (*gorapide.Event, error) {
	params := conn.resolveParams(triggerEvent)
	e := gorapide.NewEvent(conn.ActionName, target.ID, params)
	// Causal link: trigger -> new event.
	if err := target.poset.AddEventWithCause(e, triggerEvent.ID); err != nil {
		return nil, fmt.Errorf("arch.Connection.executePipe: %w", err)
	}
	target.Send(e)
	return e, nil
}

func (conn *Connection) resolveParams(triggerEvent *gorapide.Event) map[string]any {
	if conn.transform != nil {
		return conn.transform(triggerEvent)
	}
	// Default: copy trigger params.
	params := make(map[string]any, len(triggerEvent.Params))
	for k, v := range triggerEvent.Params {
		params[k] = v
	}
	return params
}

// String returns a human-readable representation of the connection.
func (conn *Connection) String() string {
	return fmt.Sprintf("Connection(%s -> %s, %s, %s)",
		conn.From, conn.To, conn.Kind, conn.ActionName)
}

// --- Connection Builder ---

// ConnectionBuilder constructs a Connection using a fluent API.
type ConnectionBuilder struct {
	from       string
	to         string
	kind       ConnectionKind
	trigger    pattern.Pattern
	actionName string
	transform  func(*gorapide.Event) map[string]any
	forward    bool
}

// Connect starts building a new connection from source to target component.
func Connect(from, to string) *ConnectionBuilder {
	return &ConnectionBuilder{
		from: from,
		to:   to,
		kind: BasicConnection,
	}
}

// On sets the trigger pattern for this connection.
func (b *ConnectionBuilder) On(trigger pattern.Pattern) *ConnectionBuilder {
	b.trigger = trigger
	return b
}

// Pipe sets the connection kind to PipeConnection (causal link).
func (b *ConnectionBuilder) Pipe() *ConnectionBuilder {
	b.kind = PipeConnection
	return b
}

// Agent sets the connection kind to AgentConnection (shared observation).
func (b *ConnectionBuilder) Agent() *ConnectionBuilder {
	b.kind = AgentConnection
	return b
}

// Send sets the action name to send on the target component.
func (b *ConnectionBuilder) Send(actionName string) *ConnectionBuilder {
	b.actionName = actionName
	return b
}

// SendWith sets the action name and a transform function for the params.
func (b *ConnectionBuilder) SendWith(actionName string, transform func(*gorapide.Event) map[string]any) *ConnectionBuilder {
	b.actionName = actionName
	b.transform = transform
	return b
}

// Forward marks this connection to forward the original event (for agent connections).
func (b *ConnectionBuilder) Forward() *ConnectionBuilder {
	b.forward = true
	return b
}

// Build finalizes and returns the Connection.
func (b *ConnectionBuilder) Build() *Connection {
	return &Connection{
		Kind:       b.kind,
		From:       b.from,
		To:         b.to,
		Trigger:    b.trigger,
		ActionName: b.actionName,
		transform:  b.transform,
		forward:    b.forward,
	}
}
