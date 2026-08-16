package pattern

import (
	"fmt"

	"github.com/ShaneDolphin/gorapide"
)

// CloneDeterministic returns a deeply owned copy of a pattern admitted by the
// deterministic pattern algebra. The copy shares no mutable slice,
// placeholder, literal container, or child-pattern node with p.
//
// Callers must not mutate p concurrently with this function. After a
// successful return, later mutation of p cannot change the clone's key or
// matching behavior.
func CloneDeterministic(p Pattern) (Pattern, error) {
	before, err := DeterministicKey(p)
	if err != nil {
		return nil, err
	}
	result, err := cloneDeterministic(p)
	if err != nil {
		return nil, err
	}
	after, err := DeterministicKey(result)
	if err != nil {
		return nil, err
	}
	if before != after {
		return nil, fmt.Errorf("%w: clone changed deterministic identity", ErrOpaquePattern)
	}
	return result, nil
}

func cloneDeterministic(p Pattern) (Pattern, error) {
	switch expression := p.(type) {
	case nil:
		// A nil pattern is the historical spelling of Rapide MatchAny. It has
		// no mutable state to own and must remain nil because several legacy
		// compatibility paths distinguish that spelling while assigning it the
		// same deterministic key as an empty BasicPattern.
		return nil, nil
	case *BasicPattern:
		if expression == nil {
			return nil, fmt.Errorf("%w: basic pattern is nil", ErrOpaquePattern)
		}
		result := &BasicPattern{
			name:          expression.name,
			sourceFilters: append([]string(nil), expression.sourceFilters...),
			label:         expression.label,
		}
		if expression.sourceBind != nil {
			placeholder := *expression.sourceBind
			result.sourceBind = &placeholder
		}
		result.parameterFilters = make([]parameterFilter, len(expression.parameterFilters))
		for index, filter := range expression.parameterFilters {
			values, err := gorapide.CanonicalizeParams(map[string]any{"value": filter.value})
			if err != nil {
				return nil, fmt.Errorf("%w: parameter filter %q: %v", ErrOpaquePattern, filter.key, err)
			}
			result.parameterFilters[index] = parameterFilter{key: filter.key, value: values["value"]}
		}
		result.parameterBinds = make([]parameterBind, len(expression.parameterBinds))
		for index, binding := range expression.parameterBinds {
			result.parameterBinds[index].key = binding.key
			if binding.placeholder != nil {
				placeholder := *binding.placeholder
				result.parameterBinds[index].placeholder = &placeholder
			}
		}
		return result, nil
	case *seqPattern:
		subs, err := cloneDeterministicPatterns(expression.subs)
		if err != nil {
			return nil, err
		}
		return &seqPattern{subs: subs}, nil
	case *immSeqPattern:
		left, right, err := cloneDeterministicPair(expression.p1, expression.p2)
		if err != nil {
			return nil, err
		}
		return &immSeqPattern{p1: left, p2: right}, nil
	case *independentPattern:
		left, right, err := cloneDeterministicPair(expression.p1, expression.p2)
		if err != nil {
			return nil, err
		}
		return &independentPattern{p1: left, p2: right}, nil
	case *disjointPattern:
		left, right, err := cloneDeterministicPair(expression.p1, expression.p2)
		if err != nil {
			return nil, err
		}
		return &disjointPattern{p1: left, p2: right}, nil
	case *unionPattern:
		left, right, err := cloneDeterministicPair(expression.p1, expression.p2)
		if err != nil {
			return nil, err
		}
		return &unionPattern{p1: left, p2: right}, nil
	case *orPattern:
		subs, err := cloneDeterministicPatterns(expression.subs)
		if err != nil {
			return nil, err
		}
		return &orPattern{subs: subs}, nil
	case *andPattern:
		subs, err := cloneDeterministicPatterns(expression.subs)
		if err != nil {
			return nil, err
		}
		return &andPattern{subs: subs}, nil
	case *iterationPattern:
		inner, err := cloneDeterministic(expression.pattern)
		if err != nil {
			return nil, err
		}
		return &iterationPattern{
			pattern: inner, relation: expression.relation,
			min: expression.min, max: expression.max,
		}, nil
	case *namedRangeIterationPattern:
		inner, err := cloneDeterministic(expression.pattern)
		if err != nil {
			return nil, err
		}
		return &namedRangeIterationPattern{
			pattern: inner, iterator: expression.iterator, relation: expression.relation,
			first: expression.first, last: expression.last,
		}, nil
	case *universalRangePattern:
		inner, err := cloneDeterministic(expression.pattern)
		if err != nil {
			return nil, err
		}
		return &universalRangePattern{
			pattern: inner, placeholder: expression.placeholder, relation: expression.relation,
			first: expression.first, last: expression.last,
		}, nil
	case *rapideTimingPattern:
		inner, err := cloneDeterministic(expression.pattern)
		if err != nil {
			return nil, err
		}
		result := &rapideTimingPattern{pattern: inner, clock: expression.clock}
		if expression.start != nil {
			start := *expression.start
			result.start = &start
		}
		if expression.duration != nil {
			duration := *expression.duration
			result.duration = &duration
		}
		return result, nil
	case *rapideTimingFilterPattern:
		inner, err := cloneDeterministic(expression.pattern)
		if err != nil {
			return nil, err
		}
		return &rapideTimingFilterPattern{
			pattern: inner, kind: expression.kind, lower: expression.lower,
			upper: expression.upper, clock: expression.clock,
		}, nil
	case *rapideTimeBeforePattern:
		left, right, err := cloneDeterministicPair(expression.left, expression.right)
		if err != nil {
			return nil, err
		}
		return &rapideTimeBeforePattern{left: left, right: right, clock: expression.clock}, nil
	case *wherePattern:
		inner, err := cloneDeterministic(expression.pattern)
		if err != nil {
			return nil, err
		}
		condition, err := cloneDeterministicCondition(expression.condition)
		if err != nil {
			return nil, err
		}
		return &wherePattern{pattern: inner, condition: condition}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported deterministic pattern type %T", ErrOpaquePattern, p)
	}
}

func cloneDeterministicPatterns(patterns []Pattern) ([]Pattern, error) {
	result := make([]Pattern, len(patterns))
	for index, current := range patterns {
		clone, err := cloneDeterministic(current)
		if err != nil {
			return nil, err
		}
		result[index] = clone
	}
	return result, nil
}

func cloneDeterministicPair(left, right Pattern) (Pattern, Pattern, error) {
	leftClone, err := cloneDeterministic(left)
	if err != nil {
		return nil, nil, err
	}
	rightClone, err := cloneDeterministic(right)
	if err != nil {
		return nil, nil, err
	}
	return leftClone, rightClone, nil
}

func cloneDeterministicCondition(condition Condition) (Condition, error) {
	result := condition
	if condition.placeholder != nil {
		placeholder := *condition.placeholder
		result.placeholder = &placeholder
	}
	if condition.kind == conditionLiteral {
		literal, err := canonicalConditionValue(condition.literal)
		if err != nil {
			return Condition{}, err
		}
		result.literal = literal
	}
	result.operands = make([]Condition, len(condition.operands))
	for index, operand := range condition.operands {
		clone, err := cloneDeterministicCondition(operand)
		if err != nil {
			return Condition{}, err
		}
		result.operands[index] = clone
	}
	return result, nil
}
