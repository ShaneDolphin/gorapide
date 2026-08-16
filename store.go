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

// CausalPreorderStore extends the historical strict-causality store without
// changing that public interface for existing implementations.
type CausalPreorderStore interface {
	CausalStore
	AddEquivalent(left, right EventID) error
}

// CausalPreorderQuerier extends the historical poset query surface with the
// equivalence relation required by Rapide's causal preorder.
type CausalPreorderQuerier interface {
	PosetQuerier
	IsCausallyEquivalent(a, b EventID) bool
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

// CausalPreorderReadWriter is the complete read/write surface for Rapide
// computations that may contain nontrivial causal-equivalence classes.
type CausalPreorderReadWriter interface {
	PosetReadWriter
	CausalPreorderStore
	CausalPreorderQuerier
}

// Compile-time assertion that *Poset satisfies PosetReadWriter.
var _ PosetReadWriter = (*Poset)(nil)
var _ CausalPreorderReadWriter = (*Poset)(nil)

// Compile-time assertions that arch.Map satisfies MapTarget
// and arch.BindingManager satisfies BindingTarget are in the arch package
// (they cannot be here due to import cycle). See arch/mapping.go and arch/binding.go.

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

// AddEquivalent implements CausalStore. It delegates to
// AddCausalEquivalent.
func (p *Poset) AddEquivalent(left, right EventID) error {
	return p.AddCausalEquivalent(left, right)
}

// DirectPredecessors implements CausalStore. Returns the EventIDs of
// immediate causal predecessors.
func (p *Poset) DirectPredecessors(id EventID) []EventID {
	causes := p.DirectCauses(id)
	preds := make([]EventID, len(causes))
	for index, cause := range causes {
		preds[index] = cause.ID
	}
	return preds
}

// DirectSuccessors implements CausalStore. Returns the EventIDs of
// immediate causal successors.
func (p *Poset) DirectSuccessors(id EventID) []EventID {
	effects := p.DirectEffects(id)
	succs := make([]EventID, len(effects))
	for index, effect := range effects {
		succs[index] = effect.ID
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
