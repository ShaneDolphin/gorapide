package pattern

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/ShaneDolphin/gorapide"
)

var ErrInvalidBinding = errors.New("invalid deterministic pattern binding")

const CanonicalMatchSetFormat = "gorapide.canonical-pattern-matches.v1"

// Binding is one placeholder assignment produced by a pattern match.
type Binding struct {
	Placeholder string
	Value       any
}

// Bindings is a canonically ordered binding environment. Lookup returns a
// defensive value copy so match results can be safely retained for replay and
// auditing.
type Bindings []Binding

// Lookup returns the value assigned to a placeholder name.
func (bindings Bindings) Lookup(name string) (any, bool) {
	index := sort.Search(len(bindings), func(i int) bool { return bindings[i].Placeholder >= name })
	if index == len(bindings) || bindings[index].Placeholder != name {
		return nil, false
	}
	copy, err := canonicalBindingValue(bindings[index].Value)
	if err != nil {
		return nil, false
	}
	return copy, true
}

// MatchResult preserves both the matching Rapide subcomputation and the
// placeholder environment established while matching it.
type MatchResult struct {
	Events   gorapide.EventSet
	Bindings Bindings
}

// ModuleSourceResolver supplies the allocation value denoted by an active
// event observation's source. Architecture execution views implement this for
// dynamic qualified patterns such as ?A.Radio(...).
type ModuleSourceResolver interface {
	ModuleValueForSource(source, expectedType string) (gorapide.RapideModuleValue, bool)
}

// CanonicalBinding is the lossless artifact representation of one placeholder
// assignment.
type CanonicalBinding struct {
	Placeholder string                  `json:"placeholder"`
	Value       gorapide.CanonicalValue `json:"value"`
}

// CanonicalMatch contains event identities, rather than a total event order,
// plus the canonically ordered environment produced by the match.
type CanonicalMatch struct {
	Events   []string           `json:"events"`
	Bindings []CanonicalBinding `json:"bindings"`
}

// CanonicalMatchSet is the versioned representation of a semantic set of
// pattern matches.
type CanonicalMatchSet struct {
	Format  string           `json:"format"`
	Matches []CanonicalMatch `json:"matches"`
}

// MarshalCanonicalMatches serializes pattern matches independently of Go map,
// input-slice, event-discovery, or binding-declaration order.
func MarshalCanonicalMatches(matches []MatchResult) ([]byte, error) {
	canonical := CanonicalMatchSet{Format: CanonicalMatchSetFormat, Matches: make([]CanonicalMatch, 0, len(matches))}
	for matchIndex, match := range matches {
		encoded, err := CanonicalizeMatch(match)
		if err != nil {
			return nil, fmt.Errorf("match %d: %w", matchIndex, err)
		}
		canonical.Matches = append(canonical.Matches, encoded)
	}
	sort.Slice(canonical.Matches, func(i, j int) bool {
		left, _ := json.Marshal(canonical.Matches[i])
		right, _ := json.Marshal(canonical.Matches[j])
		return string(left) < string(right)
	})
	write := 0
	previous := ""
	for i, match := range canonical.Matches {
		encoded, err := json.Marshal(match)
		if err != nil {
			return nil, err
		}
		key := string(encoded)
		if i > 0 && key == previous {
			continue
		}
		canonical.Matches[write] = match
		write++
		previous = key
	}
	canonical.Matches = canonical.Matches[:write]
	return json.Marshal(canonical)
}

// CanonicalizeMatch returns the lossless audit representation of one match.
// Event discovery order and binding declaration order have no semantic effect.
func CanonicalizeMatch(match MatchResult) (CanonicalMatch, error) {
	eventIDs := make([]string, 0, len(match.Events))
	seenEvents := make(map[gorapide.EventID]bool, len(match.Events))
	for _, event := range match.Events {
		if event == nil || event.ID == "" {
			return CanonicalMatch{}, fmt.Errorf("%w: match contains a nil or unidentified event", ErrInvalidBinding)
		}
		if !seenEvents[event.ID] {
			seenEvents[event.ID] = true
			eventIDs = append(eventIDs, string(event.ID))
		}
	}
	sort.Strings(eventIDs)
	bindings, compatible, err := mergeBindings(nil, match.Bindings)
	if err != nil {
		return CanonicalMatch{}, err
	}
	if !compatible {
		return CanonicalMatch{}, fmt.Errorf("%w: match contains conflicting assignments", ErrInvalidBinding)
	}
	encodedBindings := make([]CanonicalBinding, 0, len(bindings))
	for _, binding := range bindings {
		parameters, err := gorapide.CanonicalizeParameters(map[string]any{"value": binding.Value})
		if err != nil {
			return CanonicalMatch{}, fmt.Errorf("placeholder %q: %w", binding.Placeholder, err)
		}
		encodedBindings = append(encodedBindings, CanonicalBinding{
			Placeholder: binding.Placeholder, Value: parameters[0].Value,
		})
	}
	return CanonicalMatch{Events: eventIDs, Bindings: encodedBindings}, nil
}

// SemanticDigestMatches returns the SHA-256 digest of canonical match bytes.
func SemanticDigestMatches(matches []MatchResult) (string, error) {
	encoded, err := MarshalCanonicalMatches(matches)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:]), nil
}

// MatchWithBindings evaluates the deterministic, binding-aware pattern
// subset. Unsupported or opaque constructs fail explicitly. Results, events,
// and binding names are returned in canonical order.
func MatchWithBindings(expression Pattern, poset PosetReader) ([]MatchResult, error) {
	if poset == nil {
		return nil, fmt.Errorf("%w: poset is nil", ErrInvalidBinding)
	}
	results, err := matchWithBindings(expression, poset)
	if err != nil {
		return nil, err
	}
	return canonicalizeMatchResults(results)
}

func canonicalizeMatchResults(results []MatchResult) ([]MatchResult, error) {
	for i := range results {
		if err := sortMatchEvents(results[i].Events); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidBinding, err)
		}
		sort.Slice(results[i].Bindings, func(a, b int) bool {
			return results[i].Bindings[a].Placeholder < results[i].Bindings[b].Placeholder
		})
	}
	var err error
	results, err = sortMatchResults(results)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBinding, err)
	}
	return results, nil
}

func matchWithBindings(expression Pattern, poset PosetReader) ([]MatchResult, error) {
	if expression == nil {
		return matchBasicWithBindings(&BasicPattern{}, poset)
	}
	switch expression := expression.(type) {
	case *BasicPattern:
		return matchBasicWithBindings(expression, poset)
	case *seqPattern:
		leftMatches, err := matchWithBindings(expression.subs[0], poset)
		if err != nil {
			return nil, err
		}
		rightMatches, err := matchWithBindings(expression.subs[1], poset)
		if err != nil {
			return nil, err
		}
		results := make([]MatchResult, 0)
		for _, left := range leftMatches {
			for _, right := range rightMatches {
				if !allCausallyBefore(poset, left.Events, right.Events) {
					continue
				}
				bindings, compatible, err := mergeBindings(left.Bindings, right.Bindings)
				if err != nil {
					return nil, err
				}
				if !compatible {
					continue
				}
				results = append(results, MatchResult{
					Events: mergeEventSets(left.Events, right.Events), Bindings: bindings,
				})
			}
		}
		return results, nil
	case *immSeqPattern:
		return matchBinaryWithBindings(expression.p1, expression.p2, poset, func(left, right gorapide.EventSet) bool {
			return allCausallyBefore(poset, left, right) && !hasIntervening(poset, left, right, poset.All())
		})
	case *independentPattern:
		return matchBinaryWithBindings(expression.p1, expression.p2, poset, func(left, right gorapide.EventSet) bool {
			return allIndependent(poset, left, right)
		})
	case *disjointPattern:
		return matchBinaryWithBindings(expression.p1, expression.p2, poset, func(left, right gorapide.EventSet) bool {
			return eventSetsDisjoint(left, right)
		})
	case *unionPattern:
		return matchBinaryWithBindings(expression.p1, expression.p2, poset, func(_, _ gorapide.EventSet) bool { return true })
	case *orPattern:
		results := make([]MatchResult, 0)
		for _, sub := range expression.subs {
			matches, err := matchWithBindings(sub, poset)
			if err != nil {
				return nil, err
			}
			results = append(results, matches...)
		}
		return results, nil
	case *andPattern:
		if len(expression.subs) == 0 {
			return []MatchResult{}, nil
		}
		current, err := matchWithBindings(expression.subs[0], poset)
		if err != nil {
			return nil, err
		}
		for _, sub := range expression.subs[1:] {
			next, err := matchWithBindings(sub, poset)
			if err != nil {
				return nil, err
			}
			combined := make([]MatchResult, 0)
			for _, left := range current {
				for _, right := range next {
					if eventSetKey(left.Events) != eventSetKey(right.Events) {
						continue
					}
					bindings, compatible, err := mergeBindings(left.Bindings, right.Bindings)
					if err != nil {
						return nil, err
					}
					if compatible {
						combined = append(combined, MatchResult{Events: left.Events, Bindings: bindings})
					}
				}
			}
			current = combined
		}
		return current, nil
	case *iterationPattern:
		return matchIterationWithBindings(expression, poset)
	case *namedRangeIterationPattern:
		return matchNamedRangeIterationWithBindings(expression, poset)
	case *universalRangePattern:
		return matchUniversalRangeWithBindings(expression, poset)
	case *rapideTimingPattern:
		return matchRapideTimingWithBindings(expression, poset)
	case *rapideTimingFilterPattern:
		return matchRapideTimingFilterWithBindings(expression, poset)
	case *rapideTimeBeforePattern:
		return matchRapideTimeBeforeWithBindings(expression, poset)
	case *wherePattern:
		matches, err := matchWithBindings(expression.pattern, poset)
		if err != nil {
			return nil, err
		}
		result := make([]MatchResult, 0, len(matches))
		for _, match := range matches {
			evaluated, err := evaluateCondition(expression.condition, match.Bindings)
			if err != nil {
				return nil, err
			}
			matched, ok := evaluated.value.(bool)
			if !ok {
				return nil, fmt.Errorf("%w: guard evaluated to %s, want Boolean", ErrInvalidCondition, evaluated.typeName)
			}
			if matched {
				result = append(result, match)
			}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%w: binding-aware matching does not yet support %T", ErrOpaquePattern, expression)
	}
}

func matchBinaryWithBindings(leftExpression, rightExpression Pattern, poset PosetReader, relation func(gorapide.EventSet, gorapide.EventSet) bool) ([]MatchResult, error) {
	leftMatches, err := matchWithBindings(leftExpression, poset)
	if err != nil {
		return nil, err
	}
	rightMatches, err := matchWithBindings(rightExpression, poset)
	if err != nil {
		return nil, err
	}
	results := make([]MatchResult, 0)
	for _, left := range leftMatches {
		for _, right := range rightMatches {
			if !relation(left.Events, right.Events) {
				continue
			}
			bindings, compatible, err := mergeBindings(left.Bindings, right.Bindings)
			if err != nil {
				return nil, err
			}
			if compatible {
				results = append(results, MatchResult{
					Events: mergeEventSets(left.Events, right.Events), Bindings: bindings,
				})
			}
		}
	}
	return results, nil
}

func matchBasicWithBindings(basic *BasicPattern, poset PosetReader) ([]MatchResult, error) {
	if len(basic.predicates) != 0 {
		return nil, fmt.Errorf("%w: basic pattern %q has Go predicate guards", ErrOpaquePattern, basic.name)
	}
	if _, err := DeterministicKey(basic); err != nil {
		return nil, err
	}
	var candidates gorapide.EventSet
	if basic.name == "" {
		candidates = append(gorapide.EventSet(nil), poset.All()...)
	} else {
		candidates = append(gorapide.EventSet(nil), poset.ByName(basic.name)...)
	}
	if err := sortMatchEvents(candidates); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBinding, err)
	}

	results := make([]MatchResult, 0, len(candidates))
	for _, event := range candidates {
		if event == nil || !basic.matches(event) {
			continue
		}
		bindings := make(Bindings, 0, len(basic.parameterBinds)+1)
		valid := true
		if basic.sourceBind != nil {
			resolver, ok := poset.(ModuleSourceResolver)
			if !ok {
				return nil, fmt.Errorf("%w: module-source placeholder ?%s has no source resolver", ErrInvalidBinding, basic.sourceBind.name)
			}
			module, ok := resolver.ModuleValueForSource(event.Source, basic.sourceBind.typ)
			if !ok || module.Identity() == "" {
				valid = false
			} else {
				bindings = append(bindings, Binding{Placeholder: basic.sourceBind.name, Value: module})
			}
		}
		for _, declaration := range basic.parameterBinds {
			value, ok := event.Param(declaration.key)
			if !ok {
				valid = false
				break
			}
			placeholder := declaration.placeholder
			if placeholder.typ != "" && !gorapide.CanonicalValueMatchesPredefinedType(value, placeholder.typ) {
				valid = false
				break
			}
			canonical, err := canonicalBindingValue(value)
			if err != nil {
				return nil, fmt.Errorf("%w: parameter %q: %v", ErrInvalidBinding, declaration.key, err)
			}
			merged, compatible, err := mergeBindings(bindings, Bindings{{Placeholder: placeholder.name, Value: canonical}})
			if err != nil {
				return nil, err
			}
			if !compatible {
				valid = false
				break
			}
			bindings = merged
		}
		if valid {
			results = append(results, MatchResult{Events: gorapide.EventSet{event}, Bindings: bindings})
		}
	}
	return results, nil
}

func mergeBindings(left, right Bindings) (Bindings, bool, error) {
	values := make(map[string]any, len(left)+len(right))
	for _, binding := range append(append(Bindings(nil), left...), right...) {
		if binding.Placeholder == "" {
			return nil, false, fmt.Errorf("%w: placeholder name is empty", ErrInvalidBinding)
		}
		canonical, err := canonicalBindingValue(binding.Value)
		if err != nil {
			return nil, false, fmt.Errorf("%w: placeholder %q: %v", ErrInvalidBinding, binding.Placeholder, err)
		}
		if previous, exists := values[binding.Placeholder]; exists {
			equal, err := gorapide.CanonicalValuesEqual(previous, canonical)
			if err != nil {
				return nil, false, err
			}
			if !equal {
				return nil, false, nil
			}
			continue
		}
		values[binding.Placeholder] = canonical
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make(Bindings, 0, len(names))
	for _, name := range names {
		result = append(result, Binding{Placeholder: name, Value: values[name]})
	}
	return result, true, nil
}

func canonicalBindingValue(value any) (any, error) {
	parameters, err := gorapide.CanonicalizeParams(map[string]any{"value": value})
	if err != nil {
		return nil, err
	}
	return parameters["value"], nil
}

func sortMatchEvents(events gorapide.EventSet) error {
	type keyedEvent struct {
		key   string
		event *gorapide.Event
	}
	keyed := make([]keyedEvent, len(events))
	for i, event := range events {
		key, err := matchEventKey(event)
		if err != nil {
			return err
		}
		keyed[i] = keyedEvent{key: key, event: event}
	}
	sort.Slice(keyed, func(i, j int) bool { return keyed[i].key < keyed[j].key })
	for i := range keyed {
		events[i] = keyed[i].event
	}
	return nil
}

func matchEventKey(event *gorapide.Event) (string, error) {
	if event == nil {
		return "", fmt.Errorf("event is nil")
	}
	parameters, err := gorapide.CanonicalizeParameters(event.Params)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return "", err
	}
	key := struct {
		ID     string          `json:"id"`
		Source string          `json:"source"`
		Name   string          `json:"name"`
		Params json.RawMessage `json:"params"`
	}{ID: string(event.ID), Source: event.Source, Name: event.Name, Params: encoded}
	result, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func matchResultKey(result MatchResult) (string, error) {
	eventIDs := make([]string, 0, len(result.Events))
	seenEvents := make(map[gorapide.EventID]bool, len(result.Events))
	for _, event := range result.Events {
		if event == nil || event.ID == "" {
			return "", fmt.Errorf("match contains a nil or unidentified event")
		}
		if !seenEvents[event.ID] {
			seenEvents[event.ID] = true
			eventIDs = append(eventIDs, string(event.ID))
		}
	}
	sort.Strings(eventIDs)
	bindings, compatible, err := mergeBindings(nil, result.Bindings)
	if err != nil {
		return "", err
	}
	if !compatible {
		return "", fmt.Errorf("match contains conflicting assignments")
	}
	canonicalBindings := make([]CanonicalBinding, 0, len(bindings))
	for _, binding := range bindings {
		parameters, err := gorapide.CanonicalizeParameters(map[string]any{"value": binding.Value})
		if err != nil {
			return "", err
		}
		canonicalBindings = append(canonicalBindings, CanonicalBinding{
			Placeholder: binding.Placeholder, Value: parameters[0].Value,
		})
	}
	encoded, err := json.Marshal(CanonicalMatch{Events: eventIDs, Bindings: canonicalBindings})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func sortMatchResults(results []MatchResult) ([]MatchResult, error) {
	type keyedResult struct {
		key    string
		result MatchResult
	}
	keyed := make([]keyedResult, len(results))
	for i, result := range results {
		key, err := matchResultKey(result)
		if err != nil {
			return nil, err
		}
		keyed[i] = keyedResult{key: key, result: result}
	}
	sort.Slice(keyed, func(i, j int) bool { return keyed[i].key < keyed[j].key })
	for i := range keyed {
		results[i] = keyed[i].result
	}
	write := 0
	previous := ""
	for i, item := range keyed {
		if i > 0 && item.key == previous {
			continue
		}
		results[write] = item.result
		write++
		previous = item.key
	}
	return results[:write], nil
}

func patternHasBindings(expression Pattern) bool {
	switch expression := expression.(type) {
	case *BasicPattern:
		return expression.sourceBind != nil || len(expression.parameterBinds) != 0
	case *seqPattern:
		return patternHasBindings(expression.subs[0]) || patternHasBindings(expression.subs[1])
	case *immSeqPattern:
		return patternHasBindings(expression.p1) || patternHasBindings(expression.p2)
	case *independentPattern:
		return patternHasBindings(expression.p1) || patternHasBindings(expression.p2)
	case *disjointPattern:
		return patternHasBindings(expression.p1) || patternHasBindings(expression.p2)
	case *unionPattern:
		return patternHasBindings(expression.p1) || patternHasBindings(expression.p2)
	case *orPattern:
		for _, sub := range expression.subs {
			if patternHasBindings(sub) {
				return true
			}
		}
		return false
	case *andPattern:
		for _, sub := range expression.subs {
			if patternHasBindings(sub) {
				return true
			}
		}
		return false
	case *iterationPattern:
		return patternHasBindings(expression.pattern)
	case *namedRangeIterationPattern:
		return patternHasBindings(expression.pattern)
	case *universalRangePattern:
		return patternHasBindings(expression.pattern)
	case *rapideTimingPattern:
		return true
	case *rapideTimingFilterPattern:
		return patternHasBindings(expression.pattern)
	case *rapideTimeBeforePattern:
		return patternHasBindings(expression.left) || patternHasBindings(expression.right)
	case *wherePattern:
		return true
	default:
		return false
	}
}

func projectedBindingMatches(expression Pattern, poset PosetReader) ([]gorapide.EventSet, bool) {
	if !patternHasBindings(expression) {
		return nil, false
	}
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		return []gorapide.EventSet{}, true
	}
	results := make([]gorapide.EventSet, 0, len(matches))
	for _, match := range matches {
		results = append(results, match.Events)
	}
	return results, true
}
