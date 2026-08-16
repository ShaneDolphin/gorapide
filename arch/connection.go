package arch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// ConnectionKind distinguishes the three Rapide connection semantics.
type ConnectionKind int

const (
	BasicConnection ConnectionKind = iota // Source and target are observations of one event
	PipeConnection                        // Outputs form a connection-local causal sequence
	AgentConnection                       // Outputs depend on triggers but not prior outputs
)

// ConnectionScope distinguishes architecture wiring, whose target is an
// in-action on another interface, from a connection declared inside a module,
// whose target is an out-action or private action invoked by that module. The
// distinction is semantic even when both endpoints name the same component.
type ConnectionScope int

const (
	ArchitectureConnectionScope ConnectionScope = iota
	ModuleConnectionScope
)

func (scope ConnectionScope) String() string {
	switch scope {
	case ArchitectureConnectionScope:
		return "architecture"
	case ModuleConnectionScope:
		return "module"
	default:
		return fmt.Sprintf("ConnectionScope(%d)", int(scope))
	}
}

var ErrDeliveryRejected = errors.New("target component rejected event delivery")

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
	// ID is a stable semantic identifier for this connection declaration.
	ID                   string
	Kind                 ConnectionKind
	Scope                ConnectionScope
	ArchitectureInstance string          // enclosing architecture instance; root when empty
	From                 string          // source component ID (or "*" for any)
	To                   string          // target component ID (or "*" for any)
	Trigger              pattern.Pattern // pattern that triggers this connection
	ActionName           string          // name of the action to send on target
	Parameters           []ConnectionParameter

	transform func(*gorapide.Event) map[string]any // optional param transform
	forward   bool                                 // legacy spelling for same-named target action

	mu             sync.Mutex
	previousOutput map[string]gorapide.EventID // target component ID -> prior pipe output
}

// Execute fires the connection, creating or observing events according to the
// connection kind.
func (conn *Connection) Execute(triggerEvent *gorapide.Event, source, target *Component) error {
	_, err := conn.execute(triggerEvent, source, target)
	return err
}

// execute is the internal version that also returns the generated event or
// target observation, used by the Architecture router for cascading.
func (conn *Connection) execute(triggerEvent *gorapide.Event, source, target *Component) (*gorapide.Event, error) {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	previous := conn.previousOutput[target.ID]
	event, nextPrevious, err := conn.apply(target.poset, triggerEvent, target.ID, previous)
	if err != nil {
		return nil, err
	}
	if conn.Kind == PipeConnection {
		if conn.previousOutput == nil {
			conn.previousOutput = make(map[string]gorapide.EventID)
		}
		conn.previousOutput[target.ID] = nextPrevious
	}
	if err := target.SendChecked(event); err != nil {
		return nil, fmt.Errorf("arch.Connection.Execute: %w", err)
	}
	return event, nil
}

// apply is the pure semantic connection transition shared by the legacy
// delivery API and the deterministic architecture kernel. It mutates only the
// supplied poset and receives connection-local pipe state explicitly.
func (conn *Connection) apply(poset *gorapide.Poset, triggerEvent *gorapide.Event, targetID string, previousOutput gorapide.EventID) (*gorapide.Event, gorapide.EventID, error) {
	bindings := pattern.Bindings(nil)
	if len(conn.Parameters) != 0 {
		matches, err := pattern.MatchWithBindings(conn.Trigger, newObservationView(gorapide.EventSet{triggerEvent}, poset))
		if err != nil || len(matches) == 0 {
			if err == nil {
				err = fmt.Errorf("trigger did not produce a binding match")
			}
			return nil, previousOutput, fmt.Errorf("arch.Connection.apply: connection %q: %w", conn.ID, err)
		}
		bindings = matches[0].Bindings
	}
	params, err := conn.resolveClosedParameters(triggerEvent, bindings)
	if err != nil {
		return nil, previousOutput, err
	}
	return conn.applyResolved(poset, triggerEvent, targetID, previousOutput, params)
}

func (conn *Connection) applyResolved(poset *gorapide.Poset, triggerEvent *gorapide.Event, targetID string, previousOutput gorapide.EventID, params map[string]any) (*gorapide.Event, gorapide.EventID, error) {
	return conn.applyResolvedTimed(poset, triggerEvent, targetID, previousOutput, params, nil)
}

func (conn *Connection) applyResolvedTimed(poset *gorapide.Poset, triggerEvent *gorapide.Event, targetID string, previousOutput gorapide.EventID, params map[string]any, timings []gorapide.EventTiming) (*gorapide.Event, gorapide.EventID, error) {
	switch conn.Kind {
	case BasicConnection:
		event, err := conn.applyBasic(poset, triggerEvent, targetID, params, timings)
		return event, previousOutput, err
	case PipeConnection:
		event, err := conn.applyPipe(poset, triggerEvent, targetID, previousOutput, params, timings)
		if err != nil {
			return nil, previousOutput, err
		}
		return event, event.ID, nil
	case AgentConnection:
		event, err := conn.applyAgent(poset, triggerEvent, targetID, params, timings)
		return event, previousOutput, err
	default:
		return nil, previousOutput, fmt.Errorf("arch.Connection.apply: unknown kind %d", conn.Kind)
	}
}

// applyResolvedMatch applies a nonempty compound trigger match. Basic
// connections retain one-occurrence identity and therefore remain restricted
// to single-event triggers; pipe and agent outputs depend on every occurrence
// participating in the match.
func (conn *Connection) applyResolvedMatch(
	poset *gorapide.Poset,
	match pattern.MatchResult,
	anchor *gorapide.Event,
	targetID string,
	previousOutput gorapide.EventID,
	params map[string]any,
	timings []gorapide.EventTiming,
) (*gorapide.Event, gorapide.EventID, error) {
	if len(match.Events) == 1 {
		return conn.applyResolvedTimed(poset, match.Events[0], targetID, previousOutput, params, timings)
	}
	if len(match.Events) == 0 {
		return nil, previousOutput, fmt.Errorf("arch.Connection.applyResolvedMatch: connection %q has an empty trigger match", conn.ID)
	}
	if conn.Kind == BasicConnection {
		return nil, previousOutput, fmt.Errorf("arch.Connection.applyResolvedMatch: basic connection %q cannot alias a compound match", conn.ID)
	}
	causes := make([]gorapide.EventID, 0, len(match.Events)+1)
	for _, event := range match.Events {
		causes = append(causes, event.ID)
	}
	if conn.Kind == PipeConnection && previousOutput != "" {
		causes = append(causes, previousOutput)
	}
	causes = canonicalEventIDs(causes)
	digest, err := pattern.SemanticDigestMatches([]pattern.MatchResult{match})
	if err != nil {
		return nil, previousOutput, err
	}
	event, err := conn.newOutputEventForMatch(anchor, targetID, params, causes, digest, timings)
	if err != nil {
		return nil, previousOutput, err
	}
	if err := poset.AddEventWithCause(event, causes...); err != nil {
		return nil, previousOutput, err
	}
	if conn.Kind == PipeConnection {
		return event, event.ID, nil
	}
	return event, previousOutput, nil
}

func (conn *Connection) applyBasic(poset *gorapide.Poset, triggerEvent *gorapide.Event, targetID string, params map[string]any, timings []gorapide.EventTiming) (*gorapide.Event, error) {
	observation := gorapide.EventObservation{
		Name:   conn.outputAction(triggerEvent),
		Source: targetID,
		Params: params,
	}
	view, err := poset.AddObservationWithTimings(triggerEvent.ID, observation, timings)
	if err != nil {
		return nil, fmt.Errorf("arch.Connection.applyBasic: %w", err)
	}
	return view, nil
}

func (conn *Connection) applyPipe(poset *gorapide.Poset, triggerEvent *gorapide.Event, targetID string, previousOutput gorapide.EventID, params map[string]any, timings []gorapide.EventTiming) (*gorapide.Event, error) {
	causes := []gorapide.EventID{triggerEvent.ID}
	if previousOutput != "" && previousOutput != triggerEvent.ID {
		causes = append(causes, previousOutput)
	}
	e, err := conn.newOutputEvent(triggerEvent, targetID, params, causes, timings)
	if err != nil {
		return nil, fmt.Errorf("arch.Connection.applyPipe: %w", err)
	}
	if err := poset.AddEventWithCause(e, causes...); err != nil {
		return nil, fmt.Errorf("arch.Connection.applyPipe: %w", err)
	}
	return e, nil
}

func (conn *Connection) applyAgent(poset *gorapide.Poset, triggerEvent *gorapide.Event, targetID string, params map[string]any, timings []gorapide.EventTiming) (*gorapide.Event, error) {
	causes := []gorapide.EventID{triggerEvent.ID}
	e, err := conn.newOutputEvent(triggerEvent, targetID, params, causes, timings)
	if err != nil {
		return nil, fmt.Errorf("arch.Connection.applyAgent: %w", err)
	}
	if err := poset.AddEventWithCause(e, causes...); err != nil {
		return nil, fmt.Errorf("arch.Connection.applyAgent: %w", err)
	}
	return e, nil
}

func (conn *Connection) outputAction(triggerEvent *gorapide.Event) string {
	if conn.ActionName != "" {
		return conn.ActionName
	}
	return triggerEvent.Name
}

func (conn *Connection) newOutputEvent(triggerEvent *gorapide.Event, targetID string, params map[string]any, causes []gorapide.EventID, timings []gorapide.EventTiming) (*gorapide.Event, error) {
	return gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile:    "stanford-rapide-1.0",
		Instance:   targetID,
		Action:     conn.outputAction(triggerEvent),
		Occurrence: conn.ID + "|trigger=" + string(triggerEvent.ID) + "|target=" + targetID,
		Causes:     causes,
		Timings:    timings,
	}, params)
}

func (conn *Connection) newOutputEventForMatch(
	anchor *gorapide.Event,
	targetID string,
	params map[string]any,
	causes []gorapide.EventID,
	matchDigest string,
	timings []gorapide.EventTiming,
) (*gorapide.Event, error) {
	action := conn.ActionName
	if action == "" && anchor != nil {
		action = anchor.Name
	}
	return gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: "stanford-rapide-1.0", Instance: targetID, Action: action,
		Occurrence: conn.ID + "|match=" + matchDigest + "|target=" + targetID,
		Causes:     causes, Timings: timings,
	}, params)
}

func (conn *Connection) resolveParams(triggerEvent *gorapide.Event) map[string]any {
	if conn.transform != nil {
		return conn.transform(triggerEvent)
	}
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

// ConnectionBuilder constructs a Connection using a fluent API.
type ConnectionBuilder struct {
	id                   string
	from                 string
	to                   string
	kind                 ConnectionKind
	scope                ConnectionScope
	architectureInstance string
	trigger              pattern.Pattern
	actionName           string
	parameters           []ConnectionParameter
	transform            func(*gorapide.Event) map[string]any
	forward              bool
}

// WithinModule marks a connection as belonging to one generated module. Such a
// connection must have that component as both endpoints and invokes one of its
// out-actions or private actions. Architecture connections remain the builder
// default.
func (b *ConnectionBuilder) WithinModule() *ConnectionBuilder {
	b.scope = ModuleConnectionScope
	return b
}

// WithinArchitecture records the statically enclosing architecture instance.
// The root architecture remains the default. This is required when flattened
// child declarations use their returned interface as the local boundary.
func (b *ConnectionBuilder) WithinArchitecture(instanceID string) *ConnectionBuilder {
	b.architectureInstance = instanceID
	return b
}

// Connect starts building a new connection from source to target component.
func Connect(from, to string) *ConnectionBuilder {
	return &ConnectionBuilder{
		from: from,
		to:   to,
		kind: BasicConnection,
	}
}

// IdentifiedBy assigns the stable declaration identity used by deterministic
// event provenance. It distinguishes otherwise identical declarations.
func (b *ConnectionBuilder) IdentifiedBy(id string) *ConnectionBuilder {
	b.id = id
	return b
}

// On sets the trigger pattern for this connection.
func (b *ConnectionBuilder) On(trigger pattern.Pattern) *ConnectionBuilder {
	b.trigger = trigger
	return b
}

// Pipe sets the connection kind to PipeConnection.
func (b *ConnectionBuilder) Pipe() *ConnectionBuilder {
	b.kind = PipeConnection
	return b
}

// Agent sets the connection kind to AgentConnection.
func (b *ConnectionBuilder) Agent() *ConnectionBuilder {
	b.kind = AgentConnection
	return b
}

// Send sets the action name to send on the target component.
func (b *ConnectionBuilder) Send(actionName string) *ConnectionBuilder {
	b.actionName = actionName
	b.parameters = nil
	b.transform = nil
	return b
}

// SendParameters sets a closed, trigger-binding-aware target action mapping.
func (b *ConnectionBuilder) SendParameters(actionName string, parameters ...ConnectionParameter) *ConnectionBuilder {
	b.actionName = actionName
	b.parameters = copyConnectionParameters(parameters)
	b.transform = nil
	return b
}

// SendWith sets the action name and a transform function for the params.
// Opaque transforms are outside the deterministic guarantee until Rapide's
// expression language is implemented.
func (b *ConnectionBuilder) SendWith(actionName string, transform func(*gorapide.Event) map[string]any) *ConnectionBuilder {
	b.actionName = actionName
	b.parameters = nil
	b.transform = transform
	return b
}

// Forward is retained for source compatibility. Agent connections still
// generate a target event; when no target action is named, the source action
// name is used.
func (b *ConnectionBuilder) Forward() *ConnectionBuilder {
	b.forward = true
	return b
}

// Build finalizes and returns the Connection.
func (b *ConnectionBuilder) Build() *Connection {
	id := b.id
	if id == "" {
		trigger, err := pattern.DeterministicKey(b.trigger)
		if err != nil {
			trigger = "invalid:" + err.Error()
		}
		placeholderTypes, err := pattern.BoundPlaceholderTypes(b.trigger)
		if err != nil {
			placeholderTypes = map[string]string{}
		}
		_, canonicalParameters, parameterErr := canonicalizeConnectionParameters("builder", b.parameters, placeholderTypes)
		parameterBytes, marshalErr := json.Marshal(canonicalParameters)
		parameterKey := string(parameterBytes)
		if parameterErr != nil {
			parameterKey = "invalid:" + parameterErr.Error()
		} else if marshalErr != nil {
			parameterKey = "invalid:" + marshalErr.Error()
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf(
			"gorapide:connection:v4\x00%s\x00%s\x00%s\x00%d\x00%d\x00%s\x00%s\x00%s",
			b.architectureInstance, b.from, b.to, b.kind, b.scope, trigger, b.actionName, parameterKey,
		)))
		id = "conn1-" + hex.EncodeToString(digest[:])
	}
	return &Connection{
		ID:                   id,
		Kind:                 b.kind,
		Scope:                b.scope,
		ArchitectureInstance: b.architectureInstance,
		From:                 b.from,
		To:                   b.to,
		Trigger:              b.trigger,
		ActionName:           b.actionName,
		Parameters:           copyConnectionParameters(b.parameters),
		transform:            b.transform,
		forward:              b.forward,
		previousOutput:       make(map[string]gorapide.EventID),
	}
}

func copyConnectionParameters(parameters []ConnectionParameter) []ConnectionParameter {
	result := make([]ConnectionParameter, len(parameters))
	for i, parameter := range parameters {
		result[i] = ConnectionParameter{Name: parameter.Name, Value: copyRuleValue(parameter.Value)}
	}
	return result
}

// copyExecutionConnection isolates mutable delivery state from both the
// canonical model and sibling executions. The trigger is owned by the sealed
// model and is immutable after preparation, so executions may share it safely.
func copyExecutionConnection(connection *Connection) *Connection {
	if connection == nil {
		return nil
	}
	return &Connection{
		ID: connection.ID, Kind: connection.Kind, Scope: connection.Scope,
		ArchitectureInstance: connection.ArchitectureInstance,
		From:                 connection.From, To: connection.To, Trigger: connection.Trigger,
		ActionName: connection.ActionName,
		Parameters: copyConnectionParameters(connection.Parameters),
		transform:  connection.transform, forward: connection.forward,
		previousOutput: make(map[string]gorapide.EventID),
	}
}

func copyExecutionConnections(connections []*Connection) []*Connection {
	result := make([]*Connection, len(connections))
	for index, connection := range connections {
		result[index] = copyExecutionConnection(connection)
	}
	return result
}
