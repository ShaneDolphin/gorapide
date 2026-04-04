// Package pattern implements the Rapide Event Pattern Language.
//
// In Rapide, event patterns are expressions that describe partial orders
// of events. A pattern is matched against a Poset and returns all matching
// subsets, where each subset is an EventSet satisfying the pattern.
package pattern

import (
	"fmt"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// PosetReader is the minimal interface a pattern needs to query a poset.
// It combines event access with causal query capabilities.
type PosetReader interface {
	gorapide.PosetQuerier
	All() gorapide.EventSet
	ByName(name string) gorapide.EventSet
	Len() int
}

// Pattern describes a partial order of events that can be matched against a poset.
type Pattern interface {
	// Match evaluates this pattern against a poset and returns all matching
	// event subsets. Each returned EventSet is a set of events from the poset
	// that satisfies the pattern. Returns an empty slice if no matches.
	Match(p PosetReader) []gorapide.EventSet

	// String returns a human-readable representation of the pattern.
	String() string
}

// BasicPattern matches individual events by name and optional predicate guards.
type BasicPattern struct {
	name       string               // event name to match (empty = match any)
	predicates []func(*gorapide.Event) bool
	label      string               // human-readable label for String()
}

// Match returns matches events by name (or all events if name is empty),
// filtered by any attached predicates. Each matching event is returned
// as a singleton EventSet.
func (bp *BasicPattern) Match(p PosetReader) []gorapide.EventSet {
	var candidates gorapide.EventSet
	if bp.name == "" {
		candidates = p.All()
	} else {
		candidates = p.ByName(bp.name)
	}

	results := make([]gorapide.EventSet, 0)
	for _, e := range candidates {
		if bp.matches(e) {
			results = append(results, gorapide.EventSet{e})
		}
	}
	return results
}

func (bp *BasicPattern) matches(e *gorapide.Event) bool {
	for _, pred := range bp.predicates {
		if !pred(e) {
			return false
		}
	}
	return true
}

// String returns a human-readable representation of the pattern.
func (bp *BasicPattern) String() string {
	if bp.label != "" {
		return bp.label
	}
	if bp.name == "" {
		return "MatchAny()"
	}
	return fmt.Sprintf("Match(%q)", bp.name)
}

// Where adds a custom predicate guard to the pattern. Only events for which
// fn returns true will match. Multiple Where calls are ANDed together.
func (bp *BasicPattern) Where(fn func(*gorapide.Event) bool) *BasicPattern {
	bp.predicates = append(bp.predicates, fn)
	if bp.label == "" {
		bp.label = bp.baseLabel() + ".Where(<fn>)"
	} else {
		bp.label += ".Where(<fn>)"
	}
	return bp
}

// WhereParam adds a predicate that matches events where params[key] == value.
func (bp *BasicPattern) WhereParam(key string, value any) *BasicPattern {
	bp.predicates = append(bp.predicates, func(e *gorapide.Event) bool {
		v, ok := e.Param(key)
		return ok && v == value
	})
	if bp.label == "" {
		bp.label = fmt.Sprintf("%s.WhereParam(%q, %v)", bp.baseLabel(), key, value)
	} else {
		bp.label += fmt.Sprintf(".WhereParam(%q, %v)", key, value)
	}
	return bp
}

// WhereSource adds a predicate that matches events from a specific source component.
func (bp *BasicPattern) WhereSource(source string) *BasicPattern {
	bp.predicates = append(bp.predicates, func(e *gorapide.Event) bool {
		return e.Source == source
	})
	if bp.label == "" {
		bp.label = fmt.Sprintf("%s.WhereSource(%q)", bp.baseLabel(), source)
	} else {
		bp.label += fmt.Sprintf(".WhereSource(%q)", source)
	}
	return bp
}

func (bp *BasicPattern) baseLabel() string {
	if bp.name == "" {
		return "MatchAny()"
	}
	return fmt.Sprintf("Match(%q)", bp.name)
}

// Match creates a BasicPattern that matches events with the given name.
func MatchEvent(name string) *BasicPattern {
	return &BasicPattern{name: name}
}

// MatchAny creates a BasicPattern that matches any event.
func MatchAny() *BasicPattern {
	return &BasicPattern{}
}

// Placeholder represents a named variable for binding event values during
// pattern matching (analogous to ?X in Rapide). Placeholders will be used
// in complex pattern matching with variable binding in later extensions.
type Placeholder struct {
	name string // placeholder name
	typ  string // optional type constraint
}

// Var creates a new named Placeholder.
func Var(name string) *Placeholder {
	return &Placeholder{name: name}
}

// Name returns the placeholder's name.
func (ph *Placeholder) Name() string {
	return ph.name
}

// Type returns the placeholder's optional type constraint.
func (ph *Placeholder) Type() string {
	return ph.typ
}

// WithType sets a type constraint on the placeholder.
func (ph *Placeholder) WithType(typ string) *Placeholder {
	ph.typ = typ
	return ph
}

// String returns a human-readable representation of the placeholder.
func (ph *Placeholder) String() string {
	if ph.typ != "" {
		return fmt.Sprintf("?%s:%s", ph.name, ph.typ)
	}
	return "?" + ph.name
}


