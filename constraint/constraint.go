// Package constraint implements Rapide pattern constraints for verifying
// acceptable and unacceptable event patterns in a poset.
package constraint

import (
	"fmt"
	"strings"

	"github.com/beautiful-majestic-dolphin/gorapide"
	"github.com/beautiful-majestic-dolphin/gorapide/pattern"
)

// ConstraintKind distinguishes match vs never clauses.
type ConstraintKind int

const (
	MustMatch ConstraintKind = iota // This pattern MUST match in the poset
	MustNever                       // This pattern must NEVER match in the poset
)

func (k ConstraintKind) String() string {
	if k == MustMatch {
		return "MustMatch"
	}
	return "MustNever"
}

// ConstraintClause is a single check within a constraint.
type ConstraintClause struct {
	Kind    ConstraintKind
	Name    string
	Pattern pattern.Pattern
	Message string
}

// Constraint describes acceptable and unacceptable event patterns.
type Constraint struct {
	Name     string
	Desc     string
	Filter   pattern.Pattern // optional: scope to matching events
	Clauses  []ConstraintClause
	Severity string // "error", "warning", "info"
}

// ConstraintViolation describes a single constraint check failure.
type ConstraintViolation struct {
	Constraint    string
	Clause        string
	Kind          ConstraintKind
	Message       string
	MatchedEvents gorapide.EventSet
	Severity      string
}

// String returns a human-readable description of the violation.
func (v ConstraintViolation) String() string {
	return fmt.Sprintf("[%s] %s/%s (%s): %s",
		v.Severity, v.Constraint, v.Clause, v.Kind, v.Message)
}

// Check evaluates all clauses against the poset and returns violations.
func (c *Constraint) Check(poset *gorapide.Poset) []ConstraintViolation {
	var view pattern.PosetReader = poset

	if c.Filter != nil {
		matches := c.Filter.Match(poset)
		seen := make(map[gorapide.EventID]bool)
		var filtered gorapide.EventSet
		for _, es := range matches {
			for _, e := range es {
				if !seen[e.ID] {
					seen[e.ID] = true
					filtered = append(filtered, e)
				}
			}
		}
		view = &filteredView{events: filtered, poset: poset}
	}

	var violations []ConstraintViolation
	for _, clause := range c.Clauses {
		matches := clause.Pattern.Match(view)
		switch clause.Kind {
		case MustMatch:
			if len(matches) == 0 {
				violations = append(violations, ConstraintViolation{
					Constraint: c.Name,
					Clause:     clause.Name,
					Kind:       MustMatch,
					Message:    clause.Message,
					Severity:   c.Severity,
				})
			}
		case MustNever:
			for _, matched := range matches {
				violations = append(violations, ConstraintViolation{
					Constraint:    c.Name,
					Clause:        clause.Name,
					Kind:          MustNever,
					Message:       clause.Message,
					MatchedEvents: matched,
					Severity:      c.Severity,
				})
			}
		}
	}
	return violations
}

// String returns a human-readable representation of the constraint.
func (c *Constraint) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Constraint(%s", c.Name)
	if c.Desc != "" {
		fmt.Fprintf(&b, ": %s", c.Desc)
	}
	fmt.Fprintf(&b, ", %d clauses, severity=%s)", len(c.Clauses), c.Severity)
	return b.String()
}

// --- Builder ---

// ConstraintBuilder builds a Constraint using a fluent API.
type ConstraintBuilder struct {
	name     string
	desc     string
	severity string
	filter   pattern.Pattern
	clauses  []ConstraintClause
}

// NewConstraint starts building a new constraint with the given name.
func NewConstraint(name string) *ConstraintBuilder {
	return &ConstraintBuilder{
		name:     name,
		severity: "error", // default
	}
}

// Description sets the constraint description.
func (b *ConstraintBuilder) Description(desc string) *ConstraintBuilder {
	b.desc = desc
	return b
}

// Severity sets the constraint severity ("error", "warning", "info").
func (b *ConstraintBuilder) Severity(s string) *ConstraintBuilder {
	b.severity = s
	return b
}

// FilterBy sets an optional filter pattern that scopes which events
// are visible to the clause patterns.
func (b *ConstraintBuilder) FilterBy(p pattern.Pattern) *ConstraintBuilder {
	b.filter = p
	return b
}

// Must adds a match clause: the pattern MUST match in the poset.
func (b *ConstraintBuilder) Must(name string, p pattern.Pattern, msg string) *ConstraintBuilder {
	b.clauses = append(b.clauses, ConstraintClause{
		Kind:    MustMatch,
		Name:    name,
		Pattern: p,
		Message: msg,
	})
	return b
}

// MustNever adds a never clause: the pattern must NEVER match in the poset.
func (b *ConstraintBuilder) MustNever(name string, p pattern.Pattern, msg string) *ConstraintBuilder {
	b.clauses = append(b.clauses, ConstraintClause{
		Kind:    MustNever,
		Name:    name,
		Pattern: p,
		Message: msg,
	})
	return b
}

// Build finalizes and returns the Constraint.
func (b *ConstraintBuilder) Build() *Constraint {
	return &Constraint{
		Name:     b.name,
		Desc:     b.desc,
		Filter:   b.filter,
		Clauses:  b.clauses,
		Severity: b.severity,
	}
}

// --- filteredView implements pattern.PosetReader ---

// filteredView wraps a poset but scopes All/ByName/Len to only
// the filtered events. Causal queries delegate to the real poset.
type filteredView struct {
	events gorapide.EventSet
	poset  *gorapide.Poset
}

func (v *filteredView) All() gorapide.EventSet {
	return v.events
}

func (v *filteredView) ByName(name string) gorapide.EventSet {
	var result gorapide.EventSet
	for _, e := range v.events {
		if e.Name == name {
			result = append(result, e)
		}
	}
	return result
}

func (v *filteredView) Len() int {
	return len(v.events)
}

func (v *filteredView) IsCausallyBefore(a, b gorapide.EventID) bool {
	return v.poset.IsCausallyBefore(a, b)
}

func (v *filteredView) IsCausallyIndependent(a, b gorapide.EventID) bool {
	return v.poset.IsCausallyIndependent(a, b)
}

func (v *filteredView) CausalAncestors(id gorapide.EventID) gorapide.EventSet {
	return v.poset.CausalAncestors(id)
}

func (v *filteredView) CausalDescendants(id gorapide.EventID) gorapide.EventSet {
	return v.poset.CausalDescendants(id)
}

func (v *filteredView) CausalChain(from, to gorapide.EventID) (gorapide.EventSet, error) {
	return v.poset.CausalChain(from, to)
}

func (v *filteredView) Roots() gorapide.EventSet {
	return v.poset.Roots()
}

func (v *filteredView) Leaves() gorapide.EventSet {
	return v.poset.Leaves()
}

func (v *filteredView) TopologicalSort() []*gorapide.Event {
	return v.poset.TopologicalSort()
}
