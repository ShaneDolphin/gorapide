package constraint

import (
	"fmt"
	"strings"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// Checkable is the common interface for both pattern-based and predicate-based
// constraints. Both *Constraint and *PredicateConstraint satisfy it.
type Checkable interface {
	Check(poset *gorapide.Poset) []ConstraintViolation
}

// PredicateConstraint is a state-based constraint that evaluates a predicate
// function against the poset. This corresponds to Rapide's sequential
// constraints (Section 2 of the Constraint LRM).
type PredicateConstraint struct {
	Name      string
	Desc      string
	Severity  string
	Predicate func(*gorapide.Poset) []ConstraintViolation
}

// Check evaluates the predicate against the poset.
func (pc *PredicateConstraint) Check(poset *gorapide.Poset) []ConstraintViolation {
	if pc.Predicate == nil {
		return nil
	}
	return pc.Predicate(poset)
}

// String returns a human-readable representation.
func (pc *PredicateConstraint) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "PredicateConstraint(%s", pc.Name)
	if pc.Desc != "" {
		fmt.Fprintf(&b, ": %s", pc.Desc)
	}
	fmt.Fprintf(&b, ", severity=%s)", pc.Severity)
	return b.String()
}

// --- Convenience constructors ---

// EventCount returns a predicate constraint that checks the number of events
// with the given name is within [min, max].
func EventCount(eventName string, min, max int) *PredicateConstraint {
	return &PredicateConstraint{
		Name:     fmt.Sprintf("event_count_%s", eventName),
		Desc:     fmt.Sprintf("%s count must be in [%d, %d]", eventName, min, max),
		Severity: "error",
		Predicate: func(p *gorapide.Poset) []ConstraintViolation {
			count := len(p.EventsByName(eventName))
			if count < min || count > max {
				return []ConstraintViolation{{
					Constraint: fmt.Sprintf("event_count_%s", eventName),
					Clause:     "count_range",
					Kind:       MustMatch,
					Message:    fmt.Sprintf("expected %s count in [%d, %d], got %d", eventName, min, max, count),
					Severity:   "error",
				}}
			}
			return nil
		},
	}
}

// NoUnlinkedEvents returns a predicate constraint that verifies every pair
// of events in the poset is causally connected (no independent events exist
// when there are multiple events).
func NoUnlinkedEvents() *PredicateConstraint {
	return &PredicateConstraint{
		Name:     "no_unlinked_events",
		Desc:     "all events must be causally connected",
		Severity: "error",
		Predicate: func(p *gorapide.Poset) []ConstraintViolation {
			events := p.Events()
			if len(events) <= 1 {
				return nil
			}
			// Check that every event is reachable from or can reach at least one other event.
			// An event that is causally independent of ALL other events is unlinked.
			var unlinked gorapide.EventSet
			for _, e := range events {
				linked := false
				for _, other := range events {
					if e.ID == other.ID {
						continue
					}
					if !p.IsCausallyIndependent(e.ID, other.ID) {
						linked = true
						break
					}
				}
				if !linked {
					unlinked = append(unlinked, e)
				}
			}
			if len(unlinked) > 0 {
				return []ConstraintViolation{{
					Constraint:    "no_unlinked_events",
					Clause:        "all_linked",
					Kind:          MustNever,
					Message:       fmt.Sprintf("%d event(s) are causally independent of all others", len(unlinked)),
					MatchedEvents: unlinked,
					Severity:      "error",
				}}
			}
			return nil
		},
	}
}

// SingleRoot returns a predicate constraint that verifies the poset has
// exactly one root event (an event with no causal predecessors).
func SingleRoot() *PredicateConstraint {
	return &PredicateConstraint{
		Name:     "single_root",
		Desc:     "poset must have exactly one root event",
		Severity: "error",
		Predicate: func(p *gorapide.Poset) []ConstraintViolation {
			roots := p.Roots()
			if len(roots) != 1 {
				return []ConstraintViolation{{
					Constraint:    "single_root",
					Clause:        "root_count",
					Kind:          MustMatch,
					Message:       fmt.Sprintf("expected 1 root, got %d", len(roots)),
					MatchedEvents: roots,
					Severity:      "error",
				}}
			}
			return nil
		},
	}
}

// CompletesWithin returns a predicate constraint that verifies the longest
// causal chain in the poset does not exceed maxDepth events.
func CompletesWithin(maxDepth int) *PredicateConstraint {
	return &PredicateConstraint{
		Name:     "completes_within",
		Desc:     fmt.Sprintf("longest causal chain must not exceed %d events", maxDepth),
		Severity: "error",
		Predicate: func(p *gorapide.Poset) []ConstraintViolation {
			depth := longestChainLength(p)
			if depth > maxDepth {
				return []ConstraintViolation{{
					Constraint: "completes_within",
					Clause:     "max_depth",
					Kind:       MustMatch,
					Message:    fmt.Sprintf("longest causal chain is %d events, exceeds limit of %d", depth, maxDepth),
					Severity:   "error",
				}}
			}
			return nil
		},
	}
}

// longestChainLength computes the length of the longest causal chain
// using topological sort and dynamic programming.
func longestChainLength(p *gorapide.Poset) int {
	sorted := p.TopologicalSort()
	if len(sorted) == 0 {
		return 0
	}
	depth := make(map[gorapide.EventID]int, len(sorted))
	for _, e := range sorted {
		depth[e.ID] = 1
	}
	maxDepth := 1
	for _, e := range sorted {
		for _, desc := range p.DirectEffects(e.ID) {
			if d := depth[e.ID] + 1; d > depth[desc.ID] {
				depth[desc.ID] = d
			}
		}
		if depth[e.ID] > maxDepth {
			maxDepth = depth[e.ID]
		}
	}
	// Check descendants too since we update them after the node.
	for _, e := range sorted {
		if depth[e.ID] > maxDepth {
			maxDepth = depth[e.ID]
		}
	}
	return maxDepth
}

// AllComponentsEmit returns a predicate constraint that verifies every
// component in the given list has emitted at least one event.
func AllComponentsEmit(components []string) *PredicateConstraint {
	return &PredicateConstraint{
		Name:     "all_components_emit",
		Desc:     "all listed components must emit at least one event",
		Severity: "error",
		Predicate: func(p *gorapide.Poset) []ConstraintViolation {
			sources := make(map[string]bool)
			for _, e := range p.Events() {
				if e.Source != "" {
					sources[e.Source] = true
				}
			}
			var missing []string
			for _, comp := range components {
				if !sources[comp] {
					missing = append(missing, comp)
				}
			}
			if len(missing) > 0 {
				return []ConstraintViolation{{
					Constraint: "all_components_emit",
					Clause:     "component_activity",
					Kind:       MustMatch,
					Message:    fmt.Sprintf("components with no events: %s", strings.Join(missing, ", ")),
					Severity:   "error",
				}}
			}
			return nil
		},
	}
}

// CausalDepthMax returns a predicate constraint that verifies the longest
// causal chain does not exceed maxDepth events. This is an alias for
// CompletesWithin with a different name for clarity.
func CausalDepthMax(maxDepth int) *PredicateConstraint {
	c := CompletesWithin(maxDepth)
	c.Name = "causal_depth_max"
	c.Desc = fmt.Sprintf("causal depth must not exceed %d", maxDepth)
	return c
}

// --- ConstraintSet ---

// ConstraintSet aggregates multiple constraints (both pattern-based and
// predicate-based) and evaluates them together.
type ConstraintSet struct {
	Name    string
	checkers []Checkable
}

// NewConstraintSet creates a new ConstraintSet with the given name.
func NewConstraintSet(name string) *ConstraintSet {
	return &ConstraintSet{Name: name}
}

// Add registers a constraint checker. Both *Constraint and
// *PredicateConstraint satisfy the Checkable interface.
func (cs *ConstraintSet) Add(c Checkable) {
	cs.checkers = append(cs.checkers, c)
}

// Check evaluates all constraints against the poset and returns
// aggregated violations.
func (cs *ConstraintSet) Check(poset *gorapide.Poset) []ConstraintViolation {
	var all []ConstraintViolation
	for _, c := range cs.checkers {
		all = append(all, c.Check(poset)...)
	}
	return all
}

// CheckAndReport evaluates all constraints and returns both the violations
// and a human-readable report string.
func (cs *ConstraintSet) CheckAndReport(poset *gorapide.Poset) ([]ConstraintViolation, string) {
	violations := cs.Check(poset)
	var b strings.Builder
	fmt.Fprintf(&b, "ConstraintSet %q: %d violation(s)\n", cs.Name, len(violations))
	for _, v := range violations {
		fmt.Fprintf(&b, "  %s\n", v.String())
	}
	if len(violations) == 0 {
		fmt.Fprintf(&b, "  All constraints pass.\n")
	}
	return violations, b.String()
}
