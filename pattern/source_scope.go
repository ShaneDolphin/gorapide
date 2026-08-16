package pattern

import "fmt"

// ScopeUnqualifiedEventSources returns an execution-local copy of expression
// in which every unqualified basic event leaf is restricted to source. Fixed
// source leaves and module-qualified leaves retain their explicit semantics.
// The input expression is never mutated, so canonical model identities remain
// independent of runtime component allocation identities.
func ScopeUnqualifiedEventSources(expression Pattern, source string) (Pattern, error) {
	if source == "" {
		return nil, fmt.Errorf("%w: default event source is empty", ErrOpaquePattern)
	}
	return scopeUnqualifiedEventSources(expression, source)
}

func scopeUnqualifiedEventSources(expression Pattern, source string) (Pattern, error) {
	switch expression := expression.(type) {
	case *BasicPattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: nil basic pattern", ErrOpaquePattern)
		}
		if len(expression.predicates) != 0 {
			return nil, fmt.Errorf("%w: basic pattern %q has Go predicate guards", ErrOpaquePattern, expression.name)
		}
		copy := *expression
		copy.parameterFilters = append([]parameterFilter(nil), expression.parameterFilters...)
		copy.sourceFilters = append([]string(nil), expression.sourceFilters...)
		copy.parameterBinds = make([]parameterBind, len(expression.parameterBinds))
		for index, binding := range expression.parameterBinds {
			copy.parameterBinds[index] = binding
			if binding.placeholder != nil {
				placeholder := *binding.placeholder
				copy.parameterBinds[index].placeholder = &placeholder
			}
		}
		copy.predicates = nil
		if expression.sourceBind != nil {
			placeholder := *expression.sourceBind
			copy.sourceBind = &placeholder
		}
		if copy.sourceBind == nil && len(copy.sourceFilters) == 0 {
			copy.sourceFilters = []string{source}
		}
		return &copy, nil
	case *seqPattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: nil sequence pattern", ErrOpaquePattern)
		}
		subs, err := scopePatternList(expression.subs, source)
		if err != nil {
			return nil, err
		}
		return &seqPattern{subs: subs}, nil
	case *immSeqPattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: nil immediate-sequence pattern", ErrOpaquePattern)
		}
		left, right, err := scopePatternPair(expression.p1, expression.p2, source)
		if err != nil {
			return nil, err
		}
		return &immSeqPattern{p1: left, p2: right}, nil
	case *independentPattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: nil independence pattern", ErrOpaquePattern)
		}
		left, right, err := scopePatternPair(expression.p1, expression.p2, source)
		if err != nil {
			return nil, err
		}
		return &independentPattern{p1: left, p2: right}, nil
	case *disjointPattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: nil disjoint pattern", ErrOpaquePattern)
		}
		left, right, err := scopePatternPair(expression.p1, expression.p2, source)
		if err != nil {
			return nil, err
		}
		return &disjointPattern{p1: left, p2: right}, nil
	case *unionPattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: nil union pattern", ErrOpaquePattern)
		}
		left, right, err := scopePatternPair(expression.p1, expression.p2, source)
		if err != nil {
			return nil, err
		}
		return &unionPattern{p1: left, p2: right}, nil
	case *orPattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: nil disjunction pattern", ErrOpaquePattern)
		}
		subs, err := scopePatternList(expression.subs, source)
		if err != nil {
			return nil, err
		}
		return &orPattern{subs: subs}, nil
	case *andPattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: nil equivalence pattern", ErrOpaquePattern)
		}
		subs, err := scopePatternList(expression.subs, source)
		if err != nil {
			return nil, err
		}
		return &andPattern{subs: subs}, nil
	case *iterationPattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: nil iteration pattern", ErrOpaquePattern)
		}
		inner, err := scopeUnqualifiedEventSources(expression.pattern, source)
		if err != nil {
			return nil, err
		}
		return &iterationPattern{pattern: inner, relation: expression.relation, min: expression.min, max: expression.max}, nil
	case *namedRangeIterationPattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: nil named iteration pattern", ErrOpaquePattern)
		}
		inner, err := scopeUnqualifiedEventSources(expression.pattern, source)
		if err != nil {
			return nil, err
		}
		return &namedRangeIterationPattern{
			pattern: inner, iterator: expression.iterator, relation: expression.relation,
			first: expression.first, last: expression.last,
		}, nil
	case *universalRangePattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: nil universal pattern", ErrOpaquePattern)
		}
		inner, err := scopeUnqualifiedEventSources(expression.pattern, source)
		if err != nil {
			return nil, err
		}
		return &universalRangePattern{
			pattern: inner, placeholder: expression.placeholder, relation: expression.relation,
			first: expression.first, last: expression.last,
		}, nil
	case *wherePattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: nil where pattern", ErrOpaquePattern)
		}
		inner, err := scopeUnqualifiedEventSources(expression.pattern, source)
		if err != nil {
			return nil, err
		}
		return &wherePattern{pattern: inner, condition: copyCondition(expression.condition)}, nil
	default:
		return nil, fmt.Errorf("%w: source scoping does not support %T", ErrOpaquePattern, expression)
	}
}

func scopePatternList(expressions []Pattern, source string) ([]Pattern, error) {
	result := make([]Pattern, len(expressions))
	for index, expression := range expressions {
		copy, err := scopeUnqualifiedEventSources(expression, source)
		if err != nil {
			return nil, err
		}
		result[index] = copy
	}
	return result, nil
}

func scopePatternPair(left, right Pattern, source string) (Pattern, Pattern, error) {
	leftCopy, err := scopeUnqualifiedEventSources(left, source)
	if err != nil {
		return nil, nil, err
	}
	rightCopy, err := scopeUnqualifiedEventSources(right, source)
	if err != nil {
		return nil, nil, err
	}
	return leftCopy, rightCopy, nil
}
