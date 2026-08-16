package pattern

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ShaneDolphin/gorapide"
)

var ErrInvalidNamedIteration = errors.New("invalid Rapide named iteration")

// MaxNamedRangeIterationCardinality is the compatibility-profile bound for a
// finite named Integer-range iterator. It is model data, not a host resource
// heuristic, so every implementation evaluates the same admitted domains.
const MaxNamedRangeIterationCardinality uint64 = 256

// namedRangeIterationPattern models Stanford Rapide's
//
//	[I : first..last rel relation] P(I)
//
// form. The iterator identifier is lexical and does not escape as a match
// binding. Each ascending range value is substituted into one instance of P,
// and the instances are folded left-to-right by relation.
type namedRangeIterationPattern struct {
	pattern  Pattern
	iterator Placeholder
	relation IterationRelation
	first    int64
	last     int64
}

// IterateIntegerRange constructs a finite named Integer-range iteration. A
// descending range denotes an empty iterator, which matches only the empty
// computation as specified by the Pattern LRM's zero-length iterator rule.
func IterateIntegerRange(
	iterator *Placeholder,
	first, last int64,
	relation IterationRelation,
	inner Pattern,
) Pattern {
	if inner == nil {
		panic("pattern.IterateIntegerRange: pattern is nil")
	}
	if iterator == nil || iterator.name == "" {
		panic("pattern.IterateIntegerRange: iterator is nil or unnamed")
	}
	if iterator.typ != "" && !strings.EqualFold(iterator.typ, "Integer") {
		panic("pattern.IterateIntegerRange: iterator type is not Integer")
	}
	if _, valid := namedRangeCardinality(first, last); !valid {
		panic("pattern.IterateIntegerRange: range exceeds the deterministic bound")
	}
	if !validIterationRelation(relation) {
		panic("pattern.IterateIntegerRange: invalid relation")
	}
	copy := *iterator
	copy.typ = "Integer"
	return &namedRangeIterationPattern{
		pattern: inner, iterator: copy, relation: relation, first: first, last: last,
	}
}

func namedRangeCardinality(first, last int64) (uint64, bool) {
	if last < first {
		return 0, true
	}
	difference := uint64(last) - uint64(first)
	if difference >= MaxNamedRangeIterationCardinality {
		return 0, false
	}
	return difference + 1, true
}

func validateNamedRangeIteration(iteration *namedRangeIterationPattern) error {
	if iteration == nil || iteration.pattern == nil || iteration.iterator.name == "" ||
		!strings.EqualFold(iteration.iterator.typ, "Integer") ||
		!validIterationRelation(iteration.relation) {
		return ErrInvalidNamedIteration
	}
	if _, valid := namedRangeCardinality(iteration.first, iteration.last); !valid {
		return ErrInvalidNamedIteration
	}
	types := make(map[string]string)
	if err := collectBoundPlaceholderTypes(iteration.pattern, types); err != nil {
		return err
	}
	if boundType, exists := types[iteration.iterator.name]; exists &&
		boundType != "" && !strings.EqualFold(boundType, "Integer") {
		return fmt.Errorf("%w: iterator %q has type %q, want Integer", ErrInvalidNamedIteration, iteration.iterator.name, boundType)
	}
	return nil
}

func (iteration *namedRangeIterationPattern) Match(poset PosetReader) []gorapide.EventSet {
	matches, err := MatchWithBindings(iteration, poset)
	if err != nil {
		return []gorapide.EventSet{}
	}
	result := make([]gorapide.EventSet, 0, len(matches))
	for _, match := range matches {
		result = append(result, match.Events)
	}
	return result
}

func (iteration *namedRangeIterationPattern) String() string {
	return fmt.Sprintf("Iterate([%s:Integer range %d..%d rel %s], %s)",
		iteration.iterator.name, iteration.first, iteration.last,
		iteration.relation, iteration.pattern.String())
}

func matchNamedRangeIterationWithBindings(
	iteration *namedRangeIterationPattern,
	poset PosetReader,
) ([]MatchResult, error) {
	if err := validateNamedRangeIteration(iteration); err != nil {
		return nil, err
	}
	cardinality, _ := namedRangeCardinality(iteration.first, iteration.last)
	if cardinality == 0 {
		return []MatchResult{{Events: gorapide.EventSet{}, Bindings: Bindings{}}}, nil
	}
	inner, err := matchWithBindings(iteration.pattern, poset)
	if err != nil {
		return nil, err
	}
	inner, err = canonicalizeMatchResults(inner)
	if err != nil {
		return nil, err
	}

	var current []MatchResult
	for offset := uint64(0); offset < cardinality; offset++ {
		value := iteration.first + int64(offset)
		instances := make([]MatchResult, 0, len(inner))
		for _, candidate := range inner {
			bindings, compatible, err := substituteUniversalBinding(
				candidate.Bindings, iteration.iterator.name, value,
			)
			if err != nil {
				return nil, err
			}
			if compatible {
				instances = append(instances, MatchResult{Events: candidate.Events, Bindings: bindings})
			}
		}
		instances, err = canonicalizeMatchResults(instances)
		if err != nil {
			return nil, err
		}
		if offset == 0 {
			current = instances
			continue
		}
		current, err = combineIterationMatches(current, instances, iteration.relation, poset)
		if err != nil {
			return nil, err
		}
		current, err = canonicalizeMatchResults(current)
		if err != nil {
			return nil, err
		}
	}
	return canonicalizeMatchResults(current)
}

func namedRangeContainsBinding(iteration *namedRangeIterationPattern, binding Binding) (bool, error) {
	cardinality, valid := namedRangeCardinality(iteration.first, iteration.last)
	if !valid {
		return false, ErrInvalidNamedIteration
	}
	for offset := uint64(0); offset < cardinality; offset++ {
		equal, err := gorapide.CanonicalValuesEqual(binding.Value, iteration.first+int64(offset))
		if err != nil {
			return false, err
		}
		if equal {
			return true, nil
		}
	}
	return false, nil
}
