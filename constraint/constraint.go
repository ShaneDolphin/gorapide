// Package constraint implements Rapide pattern constraints for verifying
// acceptable and unacceptable event patterns in a poset.
package constraint

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

var ErrConstraintEvaluation = errors.New("constraint evaluation failed")

// ConstraintKind distinguishes positive exact-match, negative exact-match, and
// occurrence-prohibiting never clauses.
type ConstraintKind int

const (
	MustMatch    ConstraintKind = iota // The associated computation MUST match exactly.
	MustNotMatch                       // The associated computation MUST NOT match exactly.
	MustNever                          // No occurrence of this pattern may exist.
)

func (k ConstraintKind) String() string {
	switch k {
	case MustMatch:
		return "MustMatch"
	case MustNotMatch:
		return "MustNotMatch"
	case MustNever:
		return "MustNever"
	default:
		return fmt.Sprintf("ConstraintKind(%d)", k)
	}
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
	Filter   pattern.Pattern   // optional: scope to matching events
	Alphabet []pattern.Pattern // optional Stanford `from` alphabet filter
	Clauses  []ConstraintClause
	Severity string // "error", "warning", "info"
}

// CanonicalName returns the declared identity used by deterministic
// constraint-set ordering.
func (c *Constraint) CanonicalName() string {
	if c == nil {
		return ""
	}
	return c.Name
}

// ConstraintViolation describes a single constraint check failure.
type ConstraintViolation struct {
	Constraint     string
	Clause         string
	Kind           ConstraintKind
	Message        string
	MatchedEvents  gorapide.EventSet
	Bindings       pattern.Bindings
	Severity       string
	StateWitnesses []string
}

// ClauseStateWitnesses supplies canonical consistent-cut data for exactly one
// state-dependent clause. Constraint and Clause use declared identities.
type ClauseStateWitnesses struct {
	Constraint string
	Clause     string
	Witnesses  []pattern.MatchStateWitness
}

// ClauseStateEvaluation is one complete audit fact for a state-dependent
// guard evaluated at a canonical consistent-cut witness.
type ClauseStateEvaluation struct {
	Constraint    string
	Clause        string
	MatchDigest   string
	WitnessDigest string
	GuardResult   bool
}

// String returns a human-readable description of the violation.
func (v ConstraintViolation) String() string {
	return fmt.Sprintf("[%s] %s/%s (%s): %s",
		v.Severity, v.Constraint, v.Clause, v.Kind, v.Message)
}

// Check evaluates all clauses against the poset and returns violations. Legacy
// callers receive evaluation failures as an explicit synthetic violation; new
// deterministic callers should use CheckDeterministic to retain the error.
func (c *Constraint) Check(poset *gorapide.Poset) []ConstraintViolation {
	violations, err := c.CheckDeterministic(poset)
	if err == nil {
		return violations
	}
	name := ""
	severity := "error"
	if c != nil {
		name = c.Name
		if c.Severity != "" {
			severity = c.Severity
		}
	}
	return []ConstraintViolation{{
		Constraint: name, Clause: "evaluation", Kind: MustMatch,
		Message: err.Error(), Severity: severity,
	}}
}

// CheckDeterministic evaluates filters and clauses through the closed,
// binding-aware pattern subset. Positive and negative Rapide match clauses test
// an exact match of the associated visible subcomputation; never clauses reject
// every occurrence.
func (c *Constraint) CheckDeterministic(poset pattern.PosetReader) ([]ConstraintViolation, error) {
	return c.CheckDeterministicWithState(poset, nil)
}

// CheckDeterministicWithState evaluates closed state guards only from supplied
// canonical witness data. It never calls back into host behavior.
func (c *Constraint) CheckDeterministicWithState(
	poset pattern.PosetReader,
	stateWitnesses []ClauseStateWitnesses,
) ([]ConstraintViolation, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: constraint is nil", ErrConstraintEvaluation)
	}
	if poset == nil {
		return nil, fmt.Errorf("%w: poset is nil", ErrConstraintEvaluation)
	}
	if _, err := c.DeterministicDigest(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConstraintEvaluation, err)
	}
	view, err := c.evaluationView(poset)
	if err != nil {
		return nil, err
	}
	return c.checkDeterministicView(view, stateWitnesses)
}

func (c *Constraint) evaluationView(poset pattern.PosetReader) (pattern.PosetReader, error) {
	if c.Filter == nil && len(c.Alphabet) == 0 {
		return poset, nil
	}
	filters := c.Alphabet
	if c.Filter != nil {
		filters = []pattern.Pattern{c.Filter}
	}
	type viewKey struct {
		id     gorapide.EventID
		source string
		name   string
	}
	seen := make(map[viewKey]bool)
	filtered := make(gorapide.EventSet, 0)
	for _, filter := range filters {
		matches, err := pattern.MatchWithBindings(filter, poset)
		if err != nil {
			return nil, fmt.Errorf("%w: constraint %q filter: %v", ErrConstraintEvaluation, c.Name, err)
		}
		for _, match := range matches {
			for _, event := range match.Events {
				key := viewKey{id: event.ID, source: event.Source, name: event.Name}
				if seen[key] {
					continue
				}
				seen[key] = true
				filtered = append(filtered, event)
			}
		}
	}
	projection, err := pattern.NewProjection(poset, filtered)
	if err != nil {
		return nil, fmt.Errorf("%w: constraint %q projection: %v", ErrConstraintEvaluation, c.Name, err)
	}
	return projection, nil
}

func (c *Constraint) checkDeterministicView(
	view pattern.PosetReader,
	stateWitnesses []ClauseStateWitnesses,
) ([]ConstraintViolation, error) {
	witnesses := make(map[string][]pattern.MatchStateWitness)
	for _, entry := range stateWitnesses {
		if entry.Constraint != c.Name || entry.Clause == "" {
			return nil, fmt.Errorf("%w: state witness entry %s/%s does not belong to constraint %q", ErrConstraintEvaluation, entry.Constraint, entry.Clause, c.Name)
		}
		if _, duplicate := witnesses[entry.Clause]; duplicate {
			return nil, fmt.Errorf("%w: duplicate state witness entry for clause %q", ErrConstraintEvaluation, entry.Clause)
		}
		witnesses[entry.Clause] = append([]pattern.MatchStateWitness(nil), entry.Witnesses...)
	}
	var violations []ConstraintViolation
	for _, clause := range c.Clauses {
		requiresState, err := pattern.RequiresStateWitnesses(clause.Pattern)
		if err != nil {
			return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, err)
		}
		clauseWitnesses, supplied := witnesses[clause.Name]
		if requiresState != supplied {
			return nil, fmt.Errorf("%w: constraint %q clause %q state witness supply=%t, required=%t", ErrConstraintEvaluation, c.Name, clause.Name, supplied, requiresState)
		}
		delete(witnesses, clause.Name)
		switch clause.Kind {
		case MustMatch:
			var matches []pattern.MatchResult
			if requiresState {
				stateful, stateErr := pattern.MatchWithStateWitnesses(clause.Pattern, view, clauseWitnesses)
				if stateErr != nil {
					return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, stateErr)
				}
				associated, associatedErr := pattern.AssociatedEvents(clause.Pattern, view)
				if associatedErr != nil {
					return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, associatedErr)
				}
				for _, candidate := range stateful {
					if sameConstraintEventSet(candidate.Match.Events, associated) {
						matches = append(matches, candidate.Match)
					}
				}
			} else {
				matches, err = pattern.MatchWhole(clause.Pattern, view)
			}
			if err != nil {
				return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, err)
			}
			if len(matches) == 0 {
				associated, associatedErr := pattern.AssociatedEvents(clause.Pattern, view)
				if associatedErr != nil {
					return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, associatedErr)
				}
				violations = append(violations, ConstraintViolation{
					Constraint:    c.Name,
					Clause:        clause.Name,
					Kind:          MustMatch,
					Message:       clause.Message,
					MatchedEvents: associated,
					Severity:      c.Severity,
				})
			}
		case MustNotMatch:
			var stateWitnessesByMatch map[string][]string
			var matches []pattern.MatchResult
			if requiresState {
				stateful, stateErr := pattern.MatchWithStateWitnesses(clause.Pattern, view, clauseWitnesses)
				if stateErr != nil {
					return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, stateErr)
				}
				associated, associatedErr := pattern.AssociatedEvents(clause.Pattern, view)
				if associatedErr != nil {
					return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, associatedErr)
				}
				stateWitnessesByMatch = make(map[string][]string, len(stateful))
				for _, candidate := range stateful {
					if !sameConstraintEventSet(candidate.Match.Events, associated) {
						continue
					}
					matches = append(matches, candidate.Match)
					digest, digestErr := pattern.SemanticDigestMatches([]pattern.MatchResult{candidate.Match})
					if digestErr != nil {
						return nil, digestErr
					}
					stateWitnessesByMatch[digest] = append([]string(nil), candidate.WitnessDigests...)
				}
			} else {
				matches, err = pattern.MatchWhole(clause.Pattern, view)
			}
			if err != nil {
				return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, err)
			}
			for _, matched := range matches {
				var matchedWitnesses []string
				if requiresState {
					digest, digestErr := pattern.SemanticDigestMatches([]pattern.MatchResult{matched})
					if digestErr != nil {
						return nil, digestErr
					}
					matchedWitnesses = stateWitnessesByMatch[digest]
				}
				violations = append(violations, ConstraintViolation{
					Constraint:     c.Name,
					Clause:         clause.Name,
					Kind:           MustNotMatch,
					Message:        clause.Message,
					MatchedEvents:  matched.Events,
					Bindings:       matched.Bindings,
					Severity:       c.Severity,
					StateWitnesses: matchedWitnesses,
				})
			}
		case MustNever:
			var stateWitnessesByMatch map[string][]string
			var matches []pattern.MatchResult
			if requiresState {
				stateful, stateErr := pattern.MatchWithStateWitnesses(clause.Pattern, view, clauseWitnesses)
				if stateErr != nil {
					return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, stateErr)
				}
				stateWitnessesByMatch = make(map[string][]string, len(stateful))
				for _, candidate := range stateful {
					matches = append(matches, candidate.Match)
					digest, digestErr := pattern.SemanticDigestMatches([]pattern.MatchResult{candidate.Match})
					if digestErr != nil {
						return nil, digestErr
					}
					stateWitnessesByMatch[digest] = append([]string(nil), candidate.WitnessDigests...)
				}
			} else {
				matches, err = pattern.MatchWithBindings(clause.Pattern, view)
			}
			if err != nil {
				return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, err)
			}
			for _, matched := range matches {
				var matchedWitnesses []string
				if requiresState {
					digest, digestErr := pattern.SemanticDigestMatches([]pattern.MatchResult{matched})
					if digestErr != nil {
						return nil, digestErr
					}
					matchedWitnesses = stateWitnessesByMatch[digest]
				}
				violations = append(violations, ConstraintViolation{
					Constraint:     c.Name,
					Clause:         clause.Name,
					Kind:           MustNever,
					Message:        clause.Message,
					MatchedEvents:  matched.Events,
					Bindings:       matched.Bindings,
					Severity:       c.Severity,
					StateWitnesses: matchedWitnesses,
				})
			}
		default:
			return nil, fmt.Errorf("%w: constraint %q clause %q has kind %d", ErrConstraintEvaluation, c.Name, clause.Name, clause.Kind)
		}
	}
	if len(witnesses) != 0 {
		return nil, fmt.Errorf("%w: unused state witness clause entries remain", ErrConstraintEvaluation)
	}
	return violations, nil
}

func (c *Constraint) stateEvaluations(
	view pattern.PosetReader,
	stateWitnesses []ClauseStateWitnesses,
) ([]ClauseStateEvaluation, error) {
	witnesses := make(map[string][]pattern.MatchStateWitness, len(stateWitnesses))
	for _, entry := range stateWitnesses {
		if entry.Constraint != c.Name || entry.Clause == "" {
			return nil, fmt.Errorf("%w: state witness entry %s/%s does not belong to constraint %q", ErrConstraintEvaluation, entry.Constraint, entry.Clause, c.Name)
		}
		if _, duplicate := witnesses[entry.Clause]; duplicate {
			return nil, fmt.Errorf("%w: duplicate state witness entry for clause %q", ErrConstraintEvaluation, entry.Clause)
		}
		witnesses[entry.Clause] = append([]pattern.MatchStateWitness(nil), entry.Witnesses...)
	}
	result := make([]ClauseStateEvaluation, 0)
	for _, clause := range c.Clauses {
		requiresState, err := pattern.RequiresStateWitnesses(clause.Pattern)
		if err != nil {
			return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, err)
		}
		clauseWitnesses, supplied := witnesses[clause.Name]
		if requiresState != supplied {
			return nil, fmt.Errorf("%w: constraint %q clause %q state witness supply=%t, required=%t", ErrConstraintEvaluation, c.Name, clause.Name, supplied, requiresState)
		}
		delete(witnesses, clause.Name)
		if !requiresState {
			continue
		}
		_, evaluations, err := pattern.MatchWithStateWitnessAudit(clause.Pattern, view, clauseWitnesses)
		if err != nil {
			return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, err)
		}
		for _, evaluation := range evaluations {
			result = append(result, ClauseStateEvaluation{
				Constraint: c.Name, Clause: clause.Name,
				MatchDigest: evaluation.MatchDigest, WitnessDigest: evaluation.WitnessDigest,
				GuardResult: evaluation.Matched,
			})
		}
	}
	if len(witnesses) != 0 {
		return nil, fmt.Errorf("%w: unused state witness clause entries remain", ErrConstraintEvaluation)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Clause != result[j].Clause {
			return result[i].Clause < result[j].Clause
		}
		if result[i].MatchDigest != result[j].MatchDigest {
			return result[i].MatchDigest < result[j].MatchDigest
		}
		return result[i].WitnessDigest < result[j].WitnessDigest
	})
	return result, nil
}

func sameConstraintEventSet(left, right gorapide.EventSet) bool {
	ids := func(events gorapide.EventSet) []string {
		result := make([]string, 0, len(events))
		for _, event := range events {
			if event != nil {
				result = append(result, string(event.ID))
			}
		}
		sort.Strings(result)
		return uniqueConstraintStrings(result)
	}
	leftIDs, rightIDs := ids(left), ids(right)
	if len(leftIDs) != len(rightIDs) {
		return false
	}
	for index := range leftIDs {
		if leftIDs[index] != rightIDs[index] {
			return false
		}
	}
	return true
}

func uniqueConstraintStrings(values []string) []string {
	write := 0
	for _, value := range values {
		if value == "" || write > 0 && values[write-1] == value {
			continue
		}
		values[write] = value
		write++
	}
	return values[:write]
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
	alphabet []pattern.Pattern
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

// FilterFrom applies Stanford's alphabet-filter shorthand. The resulting
// computation contains every visible event view matching any listed basic
// pattern, with causality inherited transitively from the observed poset.
func (b *ConstraintBuilder) FilterFrom(patterns ...pattern.Pattern) *ConstraintBuilder {
	b.alphabet = append([]pattern.Pattern(nil), patterns...)
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

// MustNotMatch adds a negative match clause: the associated computation MUST
// NOT be an exact match of the pattern. This is distinct from MustNever, which
// rejects every contained occurrence.
func (b *ConstraintBuilder) MustNotMatch(name string, p pattern.Pattern, msg string) *ConstraintBuilder {
	b.clauses = append(b.clauses, ConstraintClause{
		Kind:    MustNotMatch,
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
		Alphabet: append([]pattern.Pattern(nil), b.alphabet...),
		Clauses:  b.clauses,
		Severity: b.severity,
	}
}
