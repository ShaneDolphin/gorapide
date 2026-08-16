package pattern

import (
	"fmt"
	"sort"
	"strings"
)

// HasModuleSourceBinding reports whether a deterministic pattern contains a
// qualified module-source placeholder such as ?A.Radio(...).
func HasModuleSourceBinding(expression Pattern) bool {
	switch expression := expression.(type) {
	case nil:
		return false
	case *BasicPattern:
		return expression.sourceBind != nil
	case *seqPattern:
		for _, sub := range expression.subs {
			if HasModuleSourceBinding(sub) {
				return true
			}
		}
	case *immSeqPattern:
		return HasModuleSourceBinding(expression.p1) || HasModuleSourceBinding(expression.p2)
	case *independentPattern:
		return HasModuleSourceBinding(expression.p1) || HasModuleSourceBinding(expression.p2)
	case *disjointPattern:
		return HasModuleSourceBinding(expression.p1) || HasModuleSourceBinding(expression.p2)
	case *unionPattern:
		return HasModuleSourceBinding(expression.p1) || HasModuleSourceBinding(expression.p2)
	case *orPattern:
		for _, sub := range expression.subs {
			if HasModuleSourceBinding(sub) {
				return true
			}
		}
	case *andPattern:
		for _, sub := range expression.subs {
			if HasModuleSourceBinding(sub) {
				return true
			}
		}
	case *iterationPattern:
		return HasModuleSourceBinding(expression.pattern)
	case *namedRangeIterationPattern:
		return HasModuleSourceBinding(expression.pattern)
	case *universalRangePattern:
		return HasModuleSourceBinding(expression.pattern)
	case *rapideTimingPattern:
		return HasModuleSourceBinding(expression.pattern)
	case *rapideTimingFilterPattern:
		return HasModuleSourceBinding(expression.pattern)
	case *rapideTimeBeforePattern:
		return HasModuleSourceBinding(expression.left) || HasModuleSourceBinding(expression.right)
	case *wherePattern:
		return HasModuleSourceBinding(expression.pattern)
	}
	return false
}

// BoundPlaceholders returns the canonical set of placeholder names declared
// by a deterministic pattern. It first validates the complete expression so
// opaque Go predicates and unsupported operators cannot enter a closed model.
func BoundPlaceholders(expression Pattern) ([]string, error) {
	if _, err := DeterministicKey(expression); err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	collectBoundPlaceholders(expression, names)
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func collectBoundPlaceholders(expression Pattern, names map[string]bool) {
	switch expression := expression.(type) {
	case nil:
		return
	case *BasicPattern:
		if expression.sourceBind != nil {
			names[expression.sourceBind.name] = true
		}
		for _, binding := range expression.parameterBinds {
			if binding.placeholder != nil {
				names[binding.placeholder.name] = true
			}
		}
	case *seqPattern:
		for _, sub := range expression.subs {
			collectBoundPlaceholders(sub, names)
		}
	case *immSeqPattern:
		collectBoundPlaceholders(expression.p1, names)
		collectBoundPlaceholders(expression.p2, names)
	case *independentPattern:
		collectBoundPlaceholders(expression.p1, names)
		collectBoundPlaceholders(expression.p2, names)
	case *disjointPattern:
		collectBoundPlaceholders(expression.p1, names)
		collectBoundPlaceholders(expression.p2, names)
	case *unionPattern:
		collectBoundPlaceholders(expression.p1, names)
		collectBoundPlaceholders(expression.p2, names)
	case *orPattern:
		for _, sub := range expression.subs {
			collectBoundPlaceholders(sub, names)
		}
	case *andPattern:
		for _, sub := range expression.subs {
			collectBoundPlaceholders(sub, names)
		}
	case *iterationPattern:
		collectBoundPlaceholders(expression.pattern, names)
	case *namedRangeIterationPattern:
		local := make(map[string]bool)
		collectBoundPlaceholders(expression.pattern, local)
		delete(local, expression.iterator.name)
		for name := range local {
			names[name] = true
		}
	case *universalRangePattern:
		local := make(map[string]bool)
		collectBoundPlaceholders(expression.pattern, local)
		delete(local, expression.placeholder.name)
		for name := range local {
			names[name] = true
		}
	case *rapideTimingPattern:
		collectBoundPlaceholders(expression.pattern, names)
		if expression.start != nil {
			names[expression.start.name] = true
		}
		if expression.duration != nil {
			names[expression.duration.name] = true
		}
	case *rapideTimingFilterPattern:
		collectBoundPlaceholders(expression.pattern, names)
	case *rapideTimeBeforePattern:
		collectBoundPlaceholders(expression.left, names)
		collectBoundPlaceholders(expression.right, names)
	case *wherePattern:
		collectBoundPlaceholders(expression.pattern, names)
	}
}

// BoundPlaceholderTypes returns the declared type of every placeholder in a
// deterministic pattern. An empty type means the placeholder is dynamically
// typed within the supported canonical value algebra. Conflicting nonempty
// declarations fail rather than acquiring traversal-order semantics.
func BoundPlaceholderTypes(expression Pattern) (map[string]string, error) {
	if _, err := DeterministicKey(expression); err != nil {
		return nil, err
	}
	result := make(map[string]string)
	if err := collectBoundPlaceholderTypes(expression, result); err != nil {
		return nil, err
	}
	return result, nil
}

func collectBoundPlaceholderTypes(expression Pattern, types map[string]string) error {
	switch expression := expression.(type) {
	case nil:
		return nil
	case *BasicPattern:
		if expression.sourceBind != nil {
			name, typeName := expression.sourceBind.name, expression.sourceBind.typ
			prior, exists := types[name]
			if exists && prior != "" && typeName != "" && prior != typeName {
				return fmt.Errorf("%w: placeholder %q has conflicting types %q and %q", ErrOpaquePattern, name, prior, typeName)
			}
			if !exists || prior == "" {
				types[name] = typeName
			}
		}
		for _, binding := range expression.parameterBinds {
			if binding.placeholder == nil {
				continue
			}
			name, typeName := binding.placeholder.name, binding.placeholder.typ
			prior, exists := types[name]
			if exists && prior != "" && typeName != "" && prior != typeName {
				return fmt.Errorf("%w: placeholder %q has conflicting types %q and %q", ErrOpaquePattern, name, prior, typeName)
			}
			if !exists || prior == "" {
				types[name] = typeName
			}
		}
	case *seqPattern:
		for _, sub := range expression.subs {
			if err := collectBoundPlaceholderTypes(sub, types); err != nil {
				return err
			}
		}
	case *immSeqPattern:
		if err := collectBoundPlaceholderTypes(expression.p1, types); err != nil {
			return err
		}
		return collectBoundPlaceholderTypes(expression.p2, types)
	case *independentPattern:
		if err := collectBoundPlaceholderTypes(expression.p1, types); err != nil {
			return err
		}
		return collectBoundPlaceholderTypes(expression.p2, types)
	case *disjointPattern:
		if err := collectBoundPlaceholderTypes(expression.p1, types); err != nil {
			return err
		}
		return collectBoundPlaceholderTypes(expression.p2, types)
	case *unionPattern:
		if err := collectBoundPlaceholderTypes(expression.p1, types); err != nil {
			return err
		}
		return collectBoundPlaceholderTypes(expression.p2, types)
	case *orPattern:
		for _, sub := range expression.subs {
			if err := collectBoundPlaceholderTypes(sub, types); err != nil {
				return err
			}
		}
	case *andPattern:
		for _, sub := range expression.subs {
			if err := collectBoundPlaceholderTypes(sub, types); err != nil {
				return err
			}
		}
	case *iterationPattern:
		return collectBoundPlaceholderTypes(expression.pattern, types)
	case *namedRangeIterationPattern:
		local := make(map[string]string)
		if err := collectBoundPlaceholderTypes(expression.pattern, local); err != nil {
			return err
		}
		if boundType, exists := local[expression.iterator.name]; exists &&
			boundType != "" && !strings.EqualFold(boundType, "Integer") {
			return fmt.Errorf("%w: named iterator %q has type %q, want Integer", ErrOpaquePattern, expression.iterator.name, boundType)
		}
		delete(local, expression.iterator.name)
		for name, typeName := range local {
			prior, exists := types[name]
			if exists && prior != "" && typeName != "" && prior != typeName {
				return fmt.Errorf("%w: placeholder %q has conflicting types %q and %q", ErrOpaquePattern, name, prior, typeName)
			}
			if !exists || prior == "" {
				types[name] = typeName
			}
		}
		return nil
	case *universalRangePattern:
		local := make(map[string]string)
		if err := collectBoundPlaceholderTypes(expression.pattern, local); err != nil {
			return err
		}
		boundType, exists := local[expression.placeholder.name]
		if !exists || boundType != "" && !strings.EqualFold(boundType, "Integer") {
			return fmt.Errorf("%w: universal placeholder %q is missing or is not Integer", ErrOpaquePattern, expression.placeholder.name)
		}
		delete(local, expression.placeholder.name)
		for name, typeName := range local {
			prior, exists := types[name]
			if exists && prior != "" && typeName != "" && prior != typeName {
				return fmt.Errorf("%w: placeholder %q has conflicting types %q and %q", ErrOpaquePattern, name, prior, typeName)
			}
			if !exists || prior == "" {
				types[name] = typeName
			}
		}
		return nil
	case *rapideTimingPattern:
		if err := collectBoundPlaceholderTypes(expression.pattern, types); err != nil {
			return err
		}
		for _, placeholder := range []*Placeholder{expression.start, expression.duration} {
			if placeholder == nil {
				continue
			}
			name, typeName := placeholder.name, placeholder.typ
			prior, exists := types[name]
			if exists && prior != "" && typeName != "" && prior != typeName {
				return fmt.Errorf("%w: placeholder %q has conflicting types %q and %q", ErrOpaquePattern, name, prior, typeName)
			}
			if !exists || prior == "" {
				types[name] = typeName
			}
		}
	case *rapideTimingFilterPattern:
		return collectBoundPlaceholderTypes(expression.pattern, types)
	case *rapideTimeBeforePattern:
		if err := collectBoundPlaceholderTypes(expression.left, types); err != nil {
			return err
		}
		return collectBoundPlaceholderTypes(expression.right, types)
	case *wherePattern:
		return collectBoundPlaceholderTypes(expression.pattern, types)
	}
	return nil
}
