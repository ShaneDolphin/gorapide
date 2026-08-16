package constraint

import (
	"fmt"

	"github.com/ShaneDolphin/gorapide/pattern"
)

// CloneDeterministicConstraint returns a deeply owned copy of one admitted
// pattern constraint. The returned constraint shares no mutable pattern node,
// clause slice, or alphabet slice with source.
//
// Callers must not mutate source concurrently with this function. After a
// successful return, later source mutation cannot change the clone's digest or
// evaluation behavior.
func CloneDeterministicConstraint(source *Constraint) (*Constraint, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: constraint is nil", ErrInvalidConstraintModel)
	}
	before, err := source.DeterministicDigest()
	if err != nil {
		return nil, err
	}
	result := &Constraint{
		Name: source.Name, Desc: source.Desc, Severity: source.Severity,
		Clauses: make([]ConstraintClause, len(source.Clauses)),
	}
	if source.Filter != nil {
		result.Filter, err = pattern.CloneDeterministic(source.Filter)
		if err != nil {
			return nil, fmt.Errorf("%w: constraint %q filter: %v", ErrInvalidConstraintModel, source.Name, err)
		}
	}
	result.Alphabet = make([]pattern.Pattern, len(source.Alphabet))
	for index, current := range source.Alphabet {
		result.Alphabet[index], err = pattern.CloneDeterministic(current)
		if err != nil {
			return nil, fmt.Errorf("%w: constraint %q alphabet %d: %v", ErrInvalidConstraintModel, source.Name, index, err)
		}
	}
	for index, clause := range source.Clauses {
		cloned, cloneErr := pattern.CloneDeterministic(clause.Pattern)
		if cloneErr != nil {
			return nil, fmt.Errorf("%w: constraint %q clause %q: %v", ErrInvalidConstraintModel, source.Name, clause.Name, cloneErr)
		}
		result.Clauses[index] = ConstraintClause{
			Kind: clause.Kind, Name: clause.Name, Pattern: cloned, Message: clause.Message,
		}
	}
	after, err := result.DeterministicDigest()
	if err != nil {
		return nil, err
	}
	if after != before {
		return nil, fmt.Errorf("%w: constraint %q clone digest changed", ErrInvalidConstraintModel, source.Name)
	}
	return result, nil
}

// DeterministicCheckers returns the set's admitted canonical checkers in
// canonical identity order. The returned slice is isolated, but its members
// are the declarations owned by the set; code preparing an immutable model
// must deeply clone each supported concrete checker before retaining it.
func (set *ConstraintSet) DeterministicCheckers() ([]CanonicalCheckable, error) {
	members, err := set.deterministicMembers()
	if err != nil {
		return nil, err
	}
	result := make([]CanonicalCheckable, len(members))
	for index, member := range members {
		result[index] = member.checker
	}
	return result, nil
}
