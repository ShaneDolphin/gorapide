package pattern

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ShaneDolphin/gorapide"
)

// BasicEventBinding describes one placeholder established by a deterministic
// basic event pattern. It is intentionally declarative: callers can validate
// a closed pattern against an interface without inspecting package-private
// pattern nodes or relying on their display strings.
type BasicEventBinding struct {
	Parameter   string `json:"parameter"`
	Placeholder string `json:"placeholder"`
	Type        string `json:"type,omitempty"`
}

// BasicEventFilter is one closed literal equality test performed by a basic
// event pattern.
type BasicEventFilter struct {
	Parameter string                  `json:"parameter"`
	Value     gorapide.CanonicalValue `json:"value"`
}

// BasicEventReference is one basic event occurrence referenced anywhere in a
// deterministic event pattern. Parameters contains the union of parameter
// names read by literal filters and placeholder bindings.
type BasicEventReference struct {
	Action     string              `json:"action"`
	Sources    []string            `json:"sources,omitempty"`
	Parameters []string            `json:"parameters,omitempty"`
	Filters    []BasicEventFilter  `json:"filters,omitempty"`
	Bindings   []BasicEventBinding `json:"bindings,omitempty"`
}

// BasicEventReferences returns a canonical description of every basic event
// reference in a closed deterministic pattern. MatchAny is represented by an
// empty Action so language layers that require a statically named action can
// reject it explicitly.
func BasicEventReferences(expression Pattern) ([]BasicEventReference, error) {
	if _, err := DeterministicKey(expression); err != nil {
		return nil, err
	}
	result := make([]BasicEventReference, 0)
	if err := collectBasicEventReferences(expression, &result); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool {
		left, _ := json.Marshal(result[i])
		right, _ := json.Marshal(result[j])
		return string(left) < string(right)
	})
	return result, nil
}

func collectBasicEventReferences(expression Pattern, result *[]BasicEventReference) error {
	if expression == nil {
		*result = append(*result, BasicEventReference{})
		return nil
	}
	switch expression := expression.(type) {
	case *BasicPattern:
		parameters := make([]string, 0, len(expression.parameterFilters)+len(expression.parameterBinds))
		filters := make([]BasicEventFilter, 0, len(expression.parameterFilters))
		for _, filter := range expression.parameterFilters {
			parameters = append(parameters, filter.key)
			encoded, err := gorapide.CanonicalizeParameters(map[string]any{"value": filter.value})
			if err != nil {
				return fmt.Errorf("%w: basic filter %q: %v", ErrOpaquePattern, filter.key, err)
			}
			filters = append(filters, BasicEventFilter{Parameter: filter.key, Value: encoded[0].Value})
		}
		bindings := make([]BasicEventBinding, 0, len(expression.parameterBinds))
		for _, binding := range expression.parameterBinds {
			parameters = append(parameters, binding.key)
			if binding.placeholder == nil {
				return fmt.Errorf("%w: basic pattern has a nil placeholder", ErrOpaquePattern)
			}
			bindings = append(bindings, BasicEventBinding{
				Parameter: binding.key, Placeholder: binding.placeholder.name, Type: binding.placeholder.typ,
			})
		}
		sort.Strings(parameters)
		parameters = uniqueStrings(parameters)
		sort.Slice(filters, func(i, j int) bool {
			if filters[i].Parameter != filters[j].Parameter {
				return filters[i].Parameter < filters[j].Parameter
			}
			left, _ := json.Marshal(filters[i].Value)
			right, _ := json.Marshal(filters[j].Value)
			return string(left) < string(right)
		})
		sort.Slice(bindings, func(i, j int) bool {
			if bindings[i].Placeholder != bindings[j].Placeholder {
				return bindings[i].Placeholder < bindings[j].Placeholder
			}
			if bindings[i].Parameter != bindings[j].Parameter {
				return bindings[i].Parameter < bindings[j].Parameter
			}
			return bindings[i].Type < bindings[j].Type
		})
		sources := append([]string(nil), expression.sourceFilters...)
		sort.Strings(sources)
		*result = append(*result, BasicEventReference{
			Action: expression.name, Sources: uniqueStrings(sources), Parameters: parameters,
			Filters: filters, Bindings: bindings,
		})
		return nil
	case *seqPattern:
		return collectReferenceOperands(expression.subs, result)
	case *immSeqPattern:
		return collectReferenceOperands([]Pattern{expression.p1, expression.p2}, result)
	case *independentPattern:
		return collectReferenceOperands([]Pattern{expression.p1, expression.p2}, result)
	case *disjointPattern:
		return collectReferenceOperands([]Pattern{expression.p1, expression.p2}, result)
	case *unionPattern:
		return collectReferenceOperands([]Pattern{expression.p1, expression.p2}, result)
	case *orPattern:
		return collectReferenceOperands(expression.subs, result)
	case *andPattern:
		return collectReferenceOperands(expression.subs, result)
	case *iterationPattern:
		return collectBasicEventReferences(expression.pattern, result)
	case *namedRangeIterationPattern:
		return collectBasicEventReferences(expression.pattern, result)
	case *universalRangePattern:
		return collectBasicEventReferences(expression.pattern, result)
	case *rapideTimingPattern:
		return collectBasicEventReferences(expression.pattern, result)
	case *rapideTimingFilterPattern:
		return collectBasicEventReferences(expression.pattern, result)
	case *rapideTimeBeforePattern:
		return collectReferenceOperands([]Pattern{expression.left, expression.right}, result)
	case *wherePattern:
		return collectBasicEventReferences(expression.pattern, result)
	default:
		return fmt.Errorf("%w: %T", ErrOpaquePattern, expression)
	}
}

func collectReferenceOperands(operands []Pattern, result *[]BasicEventReference) error {
	for _, operand := range operands {
		if err := collectBasicEventReferences(operand, result); err != nil {
			return err
		}
	}
	return nil
}

func uniqueStrings(values []string) []string {
	write := 0
	for _, value := range values {
		if write > 0 && values[write-1] == value {
			continue
		}
		values[write] = value
		write++
	}
	return values[:write]
}
