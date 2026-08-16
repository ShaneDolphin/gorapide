package pattern

import (
	"errors"
	"fmt"
)

import "github.com/ShaneDolphin/gorapide"

var ErrInvalidIteration = errors.New("invalid Rapide pattern iteration")

// IterationRelation identifies the Rapide binary pattern combinator repeated
// by an iteration expression.
type IterationRelation string

const (
	RelationFollows          IterationRelation = "follows"
	RelationImmediateFollows IterationRelation = "immediate-follows"
	RelationIndependent      IterationRelation = "independent"
	RelationDisjoint         IterationRelation = "disjoint"
	RelationUnion            IterationRelation = "union"
	RelationAnd              IterationRelation = "and"
	RelationOr               IterationRelation = "or"
)

type iterationPattern struct {
	pattern  Pattern
	relation IterationRelation
	min      int
	max      int // -1 means the Rapide * or + unbounded cardinality
}

// IterateRange models `[min..max rel relation] pattern` with an anonymous
// iterator. Every repeated occurrence shares the enclosing placeholder scope.
func IterateRange(pattern Pattern, relation IterationRelation, min, max int) Pattern {
	if pattern == nil {
		panic("pattern.IterateRange: pattern is nil")
	}
	if min < 0 || max < min {
		panic("pattern.IterateRange: invalid cardinality range")
	}
	if !validIterationRelation(relation) {
		panic("pattern.IterateRange: invalid relation")
	}
	return &iterationPattern{pattern: pattern, relation: relation, min: min, max: max}
}

// IterateZeroOrMore models `[* rel relation] pattern`.
func IterateZeroOrMore(pattern Pattern, relation IterationRelation) Pattern {
	if pattern == nil {
		panic("pattern.IterateZeroOrMore: pattern is nil")
	}
	if !validIterationRelation(relation) {
		panic("pattern.IterateZeroOrMore: invalid relation")
	}
	return &iterationPattern{pattern: pattern, relation: relation, min: 0, max: -1}
}

// IterateOneOrMore models `[+ rel relation] pattern`.
func IterateOneOrMore(pattern Pattern, relation IterationRelation) Pattern {
	if pattern == nil {
		panic("pattern.IterateOneOrMore: pattern is nil")
	}
	if !validIterationRelation(relation) {
		panic("pattern.IterateOneOrMore: invalid relation")
	}
	return &iterationPattern{pattern: pattern, relation: relation, min: 1, max: -1}
}

func validIterationRelation(relation IterationRelation) bool {
	switch relation {
	case RelationFollows, RelationImmediateFollows, RelationIndependent,
		RelationDisjoint, RelationUnion, RelationAnd, RelationOr:
		return true
	default:
		return false
	}
}

func (iteration *iterationPattern) Match(poset PosetReader) []gorapide.EventSet {
	matches, err := MatchWithBindings(iteration, poset)
	if err != nil {
		return []gorapide.EventSet{}
	}
	results := make([]gorapide.EventSet, 0, len(matches))
	for _, match := range matches {
		results = append(results, match.Events)
	}
	return results
}

func (iteration *iterationPattern) String() string {
	cardinality := fmt.Sprintf("%d..%d", iteration.min, iteration.max)
	if iteration.max < 0 && iteration.min == 0 {
		cardinality = "*"
	} else if iteration.max < 0 && iteration.min == 1 {
		cardinality = "+"
	}
	return fmt.Sprintf("Iterate([%s rel %s], %s)", cardinality, iteration.relation, iteration.pattern.String())
}

func matchIterationWithBindings(iteration *iterationPattern, poset PosetReader) ([]MatchResult, error) {
	if iteration == nil || iteration.pattern == nil || iteration.min < 0 ||
		(iteration.max >= 0 && iteration.max < iteration.min) || !validIterationRelation(iteration.relation) {
		return nil, ErrInvalidIteration
	}
	inner, err := matchWithBindings(iteration.pattern, poset)
	if err != nil {
		return nil, err
	}
	inner, err = canonicalizeMatchResults(inner)
	if err != nil {
		return nil, err
	}

	results := make([]MatchResult, 0)
	if iteration.min == 0 {
		results = append(results, MatchResult{Events: gorapide.EventSet{}, Bindings: Bindings{}})
	}
	if len(inner) == 0 {
		return results, nil
	}

	ceiling := iteration.max
	if ceiling < 0 {
		ceiling = poset.Len()
		if ceiling < 1 {
			ceiling = 1
		}
	}
	current := append([]MatchResult(nil), inner...)
	for count := 1; count <= ceiling; count++ {
		if count >= iteration.min && (iteration.max < 0 || count <= iteration.max) {
			results = append(results, current...)
		}
		if count == ceiling || len(current) == 0 {
			break
		}
		next, err := combineIterationMatches(current, inner, iteration.relation, poset)
		if err != nil {
			return nil, err
		}
		next, err = canonicalizeMatchResults(next)
		if err != nil {
			return nil, err
		}
		if (iterationRelationIsIdempotent(iteration.relation) || matchResultsContainOnlyEmptyComputations(current)) && sameMatchResults(current, next) {
			if count < iteration.min && (iteration.max < 0 || iteration.min <= iteration.max) {
				results = append(results, next...)
			}
			break
		}
		current = next
	}
	return canonicalizeMatchResults(results)
}

func matchResultsContainOnlyEmptyComputations(results []MatchResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, result := range results {
		if len(result.Events) != 0 {
			return false
		}
	}
	return true
}

func combineIterationMatches(leftMatches, rightMatches []MatchResult, relation IterationRelation, poset PosetReader) ([]MatchResult, error) {
	if relation == RelationOr {
		return append(append([]MatchResult(nil), leftMatches...), rightMatches...), nil
	}
	results := make([]MatchResult, 0)
	for _, left := range leftMatches {
		for _, right := range rightMatches {
			if !iterationRelationHolds(relation, poset, left.Events, right.Events) {
				continue
			}
			bindings, compatible, err := mergeBindings(left.Bindings, right.Bindings)
			if err != nil {
				return nil, err
			}
			if !compatible {
				continue
			}
			events := mergeEventSets(left.Events, right.Events)
			if relation == RelationAnd {
				events = append(gorapide.EventSet(nil), left.Events...)
			}
			results = append(results, MatchResult{Events: events, Bindings: bindings})
		}
	}
	return results, nil
}

func iterationRelationHolds(relation IterationRelation, poset PosetReader, left, right gorapide.EventSet) bool {
	switch relation {
	case RelationFollows:
		return allCausallyBefore(poset, left, right)
	case RelationImmediateFollows:
		return allCausallyBefore(poset, left, right) && !hasIntervening(poset, left, right, poset.All())
	case RelationIndependent:
		return allIndependent(poset, left, right)
	case RelationDisjoint:
		return eventSetsDisjoint(left, right)
	case RelationUnion:
		return true
	case RelationAnd:
		return eventSetKey(left) == eventSetKey(right)
	case RelationOr:
		return true
	default:
		return false
	}
}

func iterationRelationIsIdempotent(relation IterationRelation) bool {
	return relation == RelationUnion || relation == RelationAnd || relation == RelationOr
}

func sameMatchResults(left, right []MatchResult) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		leftKey, leftErr := matchResultKey(left[i])
		rightKey, rightErr := matchResultKey(right[i])
		if leftErr != nil || rightErr != nil || leftKey != rightKey {
			return false
		}
	}
	return true
}
