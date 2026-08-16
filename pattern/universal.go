package pattern

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ShaneDolphin/gorapide"
)

var ErrInvalidUniversalQualification = errors.New("invalid Rapide universal qualification")

// MaxUniversalRangeCardinality is the deterministic compatibility-profile
// bound for the finite Integer-range form recovered from Stanford's Rapide
// primer. Larger and non-range type domains remain explicit unsupported model
// forms instead of acquiring host-memory-dependent behavior.
const MaxUniversalRangeCardinality uint64 = 256

// universalRangePattern models
//
//	(!i : Integer range first..last by relation) P(!i)
//
// as one substituted instance of P for every Integer in the ascending range,
// folded from left to right by relation. The universal placeholder is local to
// the qualification and is therefore removed from each resulting binding
// environment; all other existential bindings must remain compatible across
// the folded instances.
type universalRangePattern struct {
	pattern     Pattern
	placeholder Placeholder
	relation    IterationRelation
	first       int64
	last        int64
}

// ForAllIntegerRange constructs Stanford Rapide's finite Integer-range
// universal qualification. The range is inclusive and ascends from first to
// last. A descending (empty) range is rejected until the original language's
// empty-domain fold identity is recovered unambiguously.
func ForAllIntegerRange(
	placeholder *Placeholder,
	first, last int64,
	relation IterationRelation,
	inner Pattern,
) Pattern {
	if inner == nil {
		panic("pattern.ForAllIntegerRange: pattern is nil")
	}
	if placeholder == nil || placeholder.name == "" {
		panic("pattern.ForAllIntegerRange: placeholder is nil or unnamed")
	}
	if placeholder.typ != "" && !strings.EqualFold(placeholder.typ, "Integer") {
		panic("pattern.ForAllIntegerRange: placeholder type is not Integer")
	}
	if !validUniversalRange(first, last) {
		panic("pattern.ForAllIntegerRange: invalid or unsupported range")
	}
	if !validIterationRelation(relation) {
		panic("pattern.ForAllIntegerRange: invalid relation")
	}
	copy := *placeholder
	copy.typ = "Integer"
	return &universalRangePattern{
		pattern: inner, placeholder: copy, relation: relation, first: first, last: last,
	}
}

func validUniversalRange(first, last int64) bool {
	if last < first {
		return false
	}
	return uint64(last)-uint64(first) < MaxUniversalRangeCardinality
}

func validateUniversalRangePattern(universal *universalRangePattern) error {
	if universal == nil || universal.pattern == nil || universal.placeholder.name == "" ||
		!strings.EqualFold(universal.placeholder.typ, "Integer") ||
		!validIterationRelation(universal.relation) ||
		!validUniversalRange(universal.first, universal.last) {
		return ErrInvalidUniversalQualification
	}
	types := make(map[string]string)
	if err := collectBoundPlaceholderTypes(universal.pattern, types); err != nil {
		return err
	}
	boundType, exists := types[universal.placeholder.name]
	if !exists {
		return fmt.Errorf("%w: placeholder %q does not occur in the qualified pattern", ErrInvalidUniversalQualification, universal.placeholder.name)
	}
	if boundType != "" && !strings.EqualFold(boundType, "Integer") {
		return fmt.Errorf("%w: placeholder %q has type %q, want Integer", ErrInvalidUniversalQualification, universal.placeholder.name, boundType)
	}
	return nil
}

func (universal *universalRangePattern) Match(poset PosetReader) []gorapide.EventSet {
	matches, err := MatchWithBindings(universal, poset)
	if err != nil {
		return []gorapide.EventSet{}
	}
	result := make([]gorapide.EventSet, 0, len(matches))
	for _, match := range matches {
		result = append(result, match.Events)
	}
	return result
}

func (universal *universalRangePattern) String() string {
	return fmt.Sprintf("ForAll(!%s:Integer range %d..%d by %s, %s)",
		universal.placeholder.name, universal.first, universal.last,
		universal.relation, universal.pattern.String())
}

func matchUniversalRangeWithBindings(
	universal *universalRangePattern,
	poset PosetReader,
) ([]MatchResult, error) {
	if err := validateUniversalRangePattern(universal); err != nil {
		return nil, err
	}
	inner, err := matchWithBindings(universal.pattern, poset)
	if err != nil {
		return nil, err
	}
	inner, err = canonicalizeMatchResults(inner)
	if err != nil {
		return nil, err
	}

	var current []MatchResult
	cardinality := uint64(universal.last) - uint64(universal.first) + 1
	for offset := uint64(0); offset < cardinality; offset++ {
		value := universal.first + int64(offset)
		instances := make([]MatchResult, 0, len(inner))
		for _, candidate := range inner {
			bindings, compatible, err := substituteUniversalBinding(
				candidate.Bindings, universal.placeholder.name, value,
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
		current, err = combineIterationMatches(current, instances, universal.relation, poset)
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

// substituteUniversalBinding performs the semantic substitution for one
// domain object. A branch of P that does not mention the universal placeholder
// is unchanged by substitution and therefore remains a candidate for every
// domain value.
func substituteUniversalBinding(bindings Bindings, name string, value int64) (Bindings, bool, error) {
	result := make(Bindings, 0, len(bindings))
	found := false
	for _, binding := range bindings {
		if binding.Placeholder != name {
			result = append(result, binding)
			continue
		}
		found = true
		equal, err := gorapide.CanonicalValuesEqual(binding.Value, value)
		if err != nil {
			return nil, false, err
		}
		if !equal {
			return nil, false, nil
		}
	}
	if !found {
		return append(Bindings(nil), bindings...), true, nil
	}
	normalized, compatible, err := mergeBindings(nil, result)
	return normalized, compatible, err
}

func universalRangeContainsBinding(universal *universalRangePattern, binding Binding) (bool, error) {
	cardinality := uint64(universal.last) - uint64(universal.first) + 1
	for offset := uint64(0); offset < cardinality; offset++ {
		equal, err := gorapide.CanonicalValuesEqual(binding.Value, universal.first+int64(offset))
		if err != nil {
			return false, err
		}
		if equal {
			return true, nil
		}
	}
	return false, nil
}
