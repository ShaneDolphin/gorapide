package pattern

import (
	"fmt"
	"sort"

	"github.com/ShaneDolphin/gorapide"
)

// AssociatedEvents returns the visible event occurrences denoted by every
// basic pattern appearing in expression. Rapide match constraints apply to
// exactly this subcomputation rather than to an arbitrary existential match.
func AssociatedEvents(expression Pattern, poset PosetReader) (gorapide.EventSet, error) {
	if poset == nil {
		return nil, fmt.Errorf("%w: poset is nil", ErrInvalidBinding)
	}
	matches, err := associatedBasicMatches(expression, poset)
	if err != nil {
		return nil, err
	}
	byID := make(map[gorapide.EventID]*gorapide.Event)
	for _, match := range matches {
		for _, event := range match.Events {
			if _, exists := byID[event.ID]; !exists {
				byID[event.ID] = event.Snapshot()
			}
		}
	}
	ids := make([]gorapide.EventID, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := make(gorapide.EventSet, 0, len(ids))
	for _, id := range ids {
		result = append(result, byID[id])
	}
	return result, nil
}

func associatedBasicMatches(expression Pattern, poset PosetReader) ([]MatchResult, error) {
	if expression == nil {
		return matchBasicWithBindings(&BasicPattern{}, poset)
	}
	switch expression := expression.(type) {
	case *BasicPattern:
		return matchBasicWithBindings(expression, poset)
	case *seqPattern:
		return mergeAssociatedBasicMatches(expression.subs, poset)
	case *immSeqPattern:
		return mergeAssociatedBasicMatches([]Pattern{expression.p1, expression.p2}, poset)
	case *independentPattern:
		return mergeAssociatedBasicMatches([]Pattern{expression.p1, expression.p2}, poset)
	case *disjointPattern:
		return mergeAssociatedBasicMatches([]Pattern{expression.p1, expression.p2}, poset)
	case *unionPattern:
		return mergeAssociatedBasicMatches([]Pattern{expression.p1, expression.p2}, poset)
	case *orPattern:
		return mergeAssociatedBasicMatches(expression.subs, poset)
	case *andPattern:
		return mergeAssociatedBasicMatches(expression.subs, poset)
	case *iterationPattern:
		return associatedBasicMatches(expression.pattern, poset)
	case *namedRangeIterationPattern:
		matches, err := associatedBasicMatches(expression.pattern, poset)
		if err != nil {
			return nil, err
		}
		result := make([]MatchResult, 0, len(matches))
		for _, match := range matches {
			bindingIndex := -1
			for index, binding := range match.Bindings {
				if binding.Placeholder != expression.iterator.name {
					continue
				}
				bindingIndex = index
				contained, err := namedRangeContainsBinding(expression, binding)
				if err != nil {
					return nil, err
				}
				if !contained {
					bindingIndex = -2
				}
				break
			}
			if bindingIndex == -2 {
				continue
			}
			bindings := append(Bindings(nil), match.Bindings...)
			if bindingIndex >= 0 {
				bindings = append(bindings[:bindingIndex], bindings[bindingIndex+1:]...)
			}
			result = append(result, MatchResult{Events: match.Events, Bindings: bindings})
		}
		return canonicalizeMatchResults(result)
	case *universalRangePattern:
		matches, err := associatedBasicMatches(expression.pattern, poset)
		if err != nil {
			return nil, err
		}
		result := make([]MatchResult, 0, len(matches))
		for _, match := range matches {
			bindingIndex := -1
			for index, binding := range match.Bindings {
				if binding.Placeholder == expression.placeholder.name {
					bindingIndex = index
					contained, err := universalRangeContainsBinding(expression, binding)
					if err != nil {
						return nil, err
					}
					if !contained {
						bindingIndex = -2
					}
					break
				}
			}
			if bindingIndex == -2 {
				continue
			}
			bindings := append(Bindings(nil), match.Bindings...)
			if bindingIndex >= 0 {
				bindings = append(bindings[:bindingIndex], bindings[bindingIndex+1:]...)
			}
			result = append(result, MatchResult{Events: match.Events, Bindings: bindings})
		}
		return canonicalizeMatchResults(result)
	case *rapideTimingPattern:
		return associatedBasicMatches(expression.pattern, poset)
	case *rapideTimingFilterPattern:
		return associatedBasicMatches(expression.pattern, poset)
	case *rapideTimeBeforePattern:
		return mergeAssociatedBasicMatches([]Pattern{expression.left, expression.right}, poset)
	case *wherePattern:
		return associatedBasicMatches(expression.pattern, poset)
	default:
		return nil, fmt.Errorf("%w: whole-computation matching does not yet support %T", ErrOpaquePattern, expression)
	}
}

func mergeAssociatedBasicMatches(expressions []Pattern, poset PosetReader) ([]MatchResult, error) {
	result := make([]MatchResult, 0)
	for _, expression := range expressions {
		matches, err := associatedBasicMatches(expression, poset)
		if err != nil {
			return nil, err
		}
		result = append(result, matches...)
	}
	return canonicalizeMatchResults(result)
}

// MatchWhole evaluates Stanford Rapide's match-constraint rule: all visible
// events associated with basic patterns in expression must together form an
// exact match. Unrelated visible events are outside the constrained
// subcomputation and do not affect the result.
func MatchWhole(expression Pattern, poset PosetReader) ([]MatchResult, error) {
	associated, err := AssociatedEvents(expression, poset)
	if err != nil {
		return nil, err
	}
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		return nil, err
	}
	wanted := eventSetKey(associated)
	result := make([]MatchResult, 0)
	for _, match := range matches {
		if eventSetKey(match.Events) == wanted {
			result = append(result, match)
		}
	}
	return result, nil
}

func basicPatterns(expression Pattern) ([]*BasicPattern, error) {
	if expression == nil {
		return []*BasicPattern{{}}, nil
	}
	switch expression := expression.(type) {
	case *BasicPattern:
		return []*BasicPattern{expression}, nil
	case *seqPattern:
		return mergeBasicPatterns(expression.subs)
	case *immSeqPattern:
		return mergeBasicPatterns([]Pattern{expression.p1, expression.p2})
	case *independentPattern:
		return mergeBasicPatterns([]Pattern{expression.p1, expression.p2})
	case *disjointPattern:
		return mergeBasicPatterns([]Pattern{expression.p1, expression.p2})
	case *unionPattern:
		return mergeBasicPatterns([]Pattern{expression.p1, expression.p2})
	case *orPattern:
		return mergeBasicPatterns(expression.subs)
	case *andPattern:
		return mergeBasicPatterns(expression.subs)
	case *iterationPattern:
		return basicPatterns(expression.pattern)
	case *namedRangeIterationPattern:
		return basicPatterns(expression.pattern)
	case *universalRangePattern:
		return basicPatterns(expression.pattern)
	case *rapideTimingPattern:
		return basicPatterns(expression.pattern)
	case *rapideTimingFilterPattern:
		return basicPatterns(expression.pattern)
	case *rapideTimeBeforePattern:
		return mergeBasicPatterns([]Pattern{expression.left, expression.right})
	case *wherePattern:
		return basicPatterns(expression.pattern)
	default:
		return nil, fmt.Errorf("%w: whole-computation matching does not yet support %T", ErrOpaquePattern, expression)
	}
}

func mergeBasicPatterns(expressions []Pattern) ([]*BasicPattern, error) {
	result := make([]*BasicPattern, 0)
	for _, expression := range expressions {
		basics, err := basicPatterns(expression)
		if err != nil {
			return nil, err
		}
		result = append(result, basics...)
	}
	return result, nil
}
