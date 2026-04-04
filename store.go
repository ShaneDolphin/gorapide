package gorapide

// EventStore defines the interface for storing and retrieving events.
type EventStore interface {
	Add(e *Event) error
	Get(id EventID) (*Event, bool)
	All() EventSet
	ByName(name string) EventSet
	Len() int
}

// CausalStore defines the interface for storing and querying causal edges.
type CausalStore interface {
	AddEdge(from, to EventID) error
	DirectPredecessors(id EventID) []EventID
	DirectSuccessors(id EventID) []EventID
	HasPath(from, to EventID) bool // transitive reachability
}

// PosetQuerier defines read-only causal query operations on a poset.
type PosetQuerier interface {
	IsCausallyBefore(a, b EventID) bool
	IsCausallyIndependent(a, b EventID) bool
	CausalAncestors(id EventID) EventSet
	CausalDescendants(id EventID) EventSet
	CausalChain(from, to EventID) (EventSet, error)
	Roots() EventSet
	Leaves() EventSet
	TopologicalSort() []*Event
}

// PosetReadWriter combines event storage, causal storage, and query
// capabilities into a single interface representing a full poset.
type PosetReadWriter interface {
	EventStore
	CausalStore
	PosetQuerier
	AddEventWithCause(e *Event, causes ...EventID) error
	Validate() []error
	Stats() PosetStats
	DOT() string
}

// Compile-time assertion that *Poset satisfies PosetReadWriter.
var _ PosetReadWriter = (*Poset)(nil)

// ---------------------------------------------------------------------------
// EventStore interface methods on *Poset
// ---------------------------------------------------------------------------

// Add implements EventStore. It delegates to AddEvent.
func (p *Poset) Add(e *Event) error {
	return p.AddEvent(e)
}

// Get implements EventStore. It delegates to Event.
func (p *Poset) Get(id EventID) (*Event, bool) {
	return p.Event(id)
}

// All implements EventStore. It delegates to Events.
func (p *Poset) All() EventSet {
	return p.Events()
}

// ByName implements EventStore. It delegates to EventsByName.
func (p *Poset) ByName(name string) EventSet {
	return p.EventsByName(name)
}

// ---------------------------------------------------------------------------
// CausalStore interface methods on *Poset
// ---------------------------------------------------------------------------

// AddEdge implements CausalStore. It delegates to AddCausal.
func (p *Poset) AddEdge(from, to EventID) error {
	return p.AddCausal(from, to)
}

// DirectPredecessors implements CausalStore. Returns the EventIDs of
// immediate causal predecessors.
func (p *Poset) DirectPredecessors(id EventID) []EventID {
	p.mu.RLock()
	defer p.mu.RUnlock()
	preds := make([]EventID, 0, len(p.reverseCausal[id]))
	for pred := range p.reverseCausal[id] {
		preds = append(preds, pred)
	}
	return preds
}

// DirectSuccessors implements CausalStore. Returns the EventIDs of
// immediate causal successors.
func (p *Poset) DirectSuccessors(id EventID) []EventID {
	p.mu.RLock()
	defer p.mu.RUnlock()
	succs := make([]EventID, 0, len(p.causalEdges[id]))
	for succ := range p.causalEdges[id] {
		succs = append(succs, succ)
	}
	return succs
}

// HasPath implements CausalStore. Reports whether there is a transitive
// causal path from 'from' to 'to'.
func (p *Poset) HasPath(from, to EventID) bool {
	return p.IsCausallyBefore(from, to)
}

// ---------------------------------------------------------------------------
// Future Rapide Constructs
// ---------------------------------------------------------------------------

// MapTarget is a placeholder interface for future Rapide Map support.
// In the Rapide language, Maps define relationships between architectures,
// translating events from one architecture's vocabulary into another's.
// A MapTarget transforms a source event into zero or more target events,
// enabling cross-architecture event translation and composition.
//
// This interface is reserved and will be implemented in a future version.
type MapTarget interface {
	MapEvent(source *Event) ([]*Event, error)
}

// BindingTarget is a placeholder interface for future Rapide Binding support.
// In the Rapide language, Bindings allow different component interfaces to be
// connected dynamically at runtime. A binding maps event names from one
// interface to another, enabling modular composition of architectures.
//
// This interface is reserved and will be implemented in a future version.
type BindingTarget interface {
	Bind(from, to string) error
	Unbind(from string) error
}
