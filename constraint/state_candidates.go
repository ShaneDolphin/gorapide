package constraint

import (
	"fmt"
	"sort"

	"github.com/ShaneDolphin/gorapide/pattern"
)

// ClauseStateCandidate identifies every complete inner match whose
// state-dependent guard must be evaluated at one or more consistent cuts.
// Matches are already canonicalized by the pattern package.
type ClauseStateCandidate struct {
	Constraint string
	Clause     string
	Matches    []pattern.MatchResult
}

// StateWitnessCandidates returns the state-dependent clause matches over the
// exact filtered projection used by CheckDeterministicWithState.
func (c *Constraint) StateWitnessCandidates(poset pattern.PosetReader) ([]ClauseStateCandidate, error) {
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
	result := make([]ClauseStateCandidate, 0)
	for _, clause := range c.Clauses {
		requiresState, err := pattern.RequiresStateWitnesses(clause.Pattern)
		if err != nil {
			return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, err)
		}
		if !requiresState {
			continue
		}
		matches, err := pattern.StateWitnessCandidates(clause.Pattern, view)
		if err != nil {
			return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrConstraintEvaluation, c.Name, clause.Name, err)
		}
		result = append(result, ClauseStateCandidate{
			Constraint: c.Name,
			Clause:     clause.Name,
			Matches:    matches,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Clause < result[j].Clause })
	return result, nil
}

// StateWitnessCandidates returns all state-dependent clause matches in
// canonical constraint identity order.
func (set *ConstraintSet) StateWitnessCandidates(poset pattern.PosetReader) ([]ClauseStateCandidate, error) {
	if poset == nil {
		return nil, fmt.Errorf("%w: poset is nil", ErrInvalidConstraintSet)
	}
	members, err := set.deterministicMembers()
	if err != nil {
		return nil, err
	}
	result := make([]ClauseStateCandidate, 0)
	for _, member := range members {
		current, ok := member.checker.(*Constraint)
		if !ok {
			continue
		}
		candidates, err := current.StateWitnessCandidates(poset)
		if err != nil {
			return nil, err
		}
		result = append(result, candidates...)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Constraint != result[j].Constraint {
			return result[i].Constraint < result[j].Constraint
		}
		return result[i].Clause < result[j].Clause
	})
	return result, nil
}
