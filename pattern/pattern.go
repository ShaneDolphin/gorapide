// Package pattern implements the Rapide Event Pattern Language.
//
// In Rapide, event patterns are expressions that describe partial orders
// of events. A pattern is matched against a Poset and returns all matching
// subsets, where each subset is an EventSet satisfying the pattern.
package pattern

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/ShaneDolphin/gorapide"
)

var ErrOpaquePattern = errors.New("pattern contains behavior outside the deterministic expression subset")

// PosetReader is the minimal interface a pattern needs to query a poset.
// It combines event access with causal query capabilities.
type PosetReader interface {
	gorapide.PosetQuerier
	All() gorapide.EventSet
	ByName(name string) gorapide.EventSet
	Len() int
}

// Pattern describes a partial order of events that can be matched against a poset.
type Pattern interface {
	// Match evaluates this pattern against a poset and returns all matching
	// event subsets. Each returned EventSet is a set of events from the poset
	// that satisfies the pattern. Returns an empty slice if no matches.
	Match(p PosetReader) []gorapide.EventSet

	// String returns a human-readable representation of the pattern.
	String() string
}

// BasicPattern matches individual events by name and optional predicate guards.
type BasicPattern struct {
	name             string // event name to match (empty = match any)
	parameterFilters []parameterFilter
	sourceFilters    []string
	sourceBind       *Placeholder
	parameterBinds   []parameterBind
	predicates       []func(*gorapide.Event) bool
	label            string // human-readable label for String()
}

type parameterFilter struct {
	key   string
	value any
}

type parameterBind struct {
	key         string
	placeholder *Placeholder
}

// Match returns matches events by name (or all events if name is empty),
// filtered by any attached predicates. Each matching event is returned
// as a singleton EventSet.
func (bp *BasicPattern) Match(p PosetReader) []gorapide.EventSet {
	var candidates gorapide.EventSet
	if bp.name == "" {
		candidates = p.All()
	} else {
		candidates = p.ByName(bp.name)
	}

	results := make([]gorapide.EventSet, 0)
	for _, e := range candidates {
		if bp.matches(e) {
			results = append(results, gorapide.EventSet{e})
		}
	}
	return results
}

func (bp *BasicPattern) matches(e *gorapide.Event) bool {
	for _, filter := range bp.parameterFilters {
		value, ok := e.Param(filter.key)
		if !ok {
			return false
		}
		equal, err := gorapide.CanonicalValuesEqual(value, filter.value)
		if err != nil || !equal {
			return false
		}
	}
	for _, source := range bp.sourceFilters {
		if e.Source != source {
			return false
		}
	}
	for _, pred := range bp.predicates {
		if !pred(e) {
			return false
		}
	}
	return true
}

// String returns a human-readable representation of the pattern.
func (bp *BasicPattern) String() string {
	if bp.label != "" {
		return bp.label
	}
	if bp.name == "" {
		return "MatchAny()"
	}
	return fmt.Sprintf("Match(%q)", bp.name)
}

// Where adds a custom predicate guard to the pattern. Only events for which
// fn returns true will match. Multiple Where calls are ANDed together.
func (bp *BasicPattern) Where(fn func(*gorapide.Event) bool) *BasicPattern {
	bp.predicates = append(bp.predicates, fn)
	if bp.label == "" {
		bp.label = bp.baseLabel() + ".Where(<fn>)"
	} else {
		bp.label += ".Where(<fn>)"
	}
	return bp
}

// WhereParam adds a predicate that matches events where params[key] == value.
func (bp *BasicPattern) WhereParam(key string, value any) *BasicPattern {
	bp.parameterFilters = append(bp.parameterFilters, parameterFilter{key: key, value: value})
	if bp.label == "" {
		bp.label = fmt.Sprintf("%s.WhereParam(%q, %v)", bp.baseLabel(), key, value)
	} else {
		bp.label += fmt.Sprintf(".WhereParam(%q, %v)", key, value)
	}
	return bp
}

// WhereSource adds a predicate that matches events from a specific source component.
func (bp *BasicPattern) WhereSource(source string) *BasicPattern {
	bp.sourceFilters = append(bp.sourceFilters, source)
	if bp.label == "" {
		bp.label = fmt.Sprintf("%s.WhereSource(%q)", bp.baseLabel(), source)
	} else {
		bp.label += fmt.Sprintf(".WhereSource(%q)", source)
	}
	return bp
}

// BindModuleSource binds the module that generated the active event
// observation. The PosetReader supplied to MatchWithBindings must implement
// ModuleSourceResolver; ordinary source strings are deliberately not treated
// as Rapide module values.
func (bp *BasicPattern) BindModuleSource(placeholder *Placeholder) *BasicPattern {
	if placeholder != nil {
		copy := *placeholder
		placeholder = &copy
	}
	bp.sourceBind = placeholder
	return bp
}

// BindParam binds an event parameter to a Rapide placeholder. Reusing the
// placeholder in a compound pattern requires every occurrence to have the same
// canonical value. Binding-aware evaluation is performed by MatchWithBindings.
func (bp *BasicPattern) BindParam(key string, placeholder *Placeholder) *BasicPattern {
	if placeholder != nil {
		copy := *placeholder
		placeholder = &copy
	}
	bp.parameterBinds = append(bp.parameterBinds, parameterBind{key: key, placeholder: placeholder})
	if bp.label == "" {
		bp.label = fmt.Sprintf("%s.BindParam(%q, %v)", bp.baseLabel(), key, placeholder)
	} else {
		bp.label += fmt.Sprintf(".BindParam(%q, %v)", key, placeholder)
	}
	return bp
}

func (bp *BasicPattern) baseLabel() string {
	if bp.name == "" {
		return "MatchAny()"
	}
	return fmt.Sprintf("Match(%q)", bp.name)
}

// Match creates a BasicPattern that matches events with the given name.
func MatchEvent(name string) *BasicPattern {
	return &BasicPattern{name: name}
}

// MatchAny creates a BasicPattern that matches any event.
func MatchAny() *BasicPattern {
	return &BasicPattern{}
}

// IsPlainBasicEventPattern reports whether expression names exactly one event
// and carries no source/parameter filters, bindings, or opaque predicates. It is
// used by semantic boundaries that deliberately admit only an unqualified
// action name rather than the wider single-event pattern language.
func IsPlainBasicEventPattern(expression Pattern) bool {
	basic, ok := expression.(*BasicPattern)
	return ok && basic != nil && basic.name != "" &&
		len(basic.parameterFilters) == 0 && len(basic.sourceFilters) == 0 &&
		basic.sourceBind == nil && len(basic.parameterBinds) == 0 &&
		len(basic.predicates) == 0
}

// IsUnqualifiedSingleEventPattern reports whether expression is a named basic
// event, optionally with deterministic parameter filters/bindings and a closed
// where condition, but with no fixed or bound source and no opaque predicate.
// It deliberately excludes every compound or qualified module-source form.
func IsUnqualifiedSingleEventPattern(expression Pattern) bool {
	switch expression := expression.(type) {
	case *BasicPattern:
		return expression != nil && expression.name != "" &&
			len(expression.sourceFilters) == 0 && expression.sourceBind == nil &&
			len(expression.predicates) == 0
	case *wherePattern:
		return expression != nil && IsUnqualifiedSingleEventPattern(expression.pattern)
	default:
		return false
	}
}

// IsModuleQualifiedSingleEventPattern reports whether expression is one named
// event whose source is bound by exactly one typed module placeholder. Closed
// parameter filters/bindings and a closed where condition are admitted; fixed
// sources, unqualified leaves, compound forms, and opaque predicates are not.
func IsModuleQualifiedSingleEventPattern(expression Pattern) bool {
	switch expression := expression.(type) {
	case *BasicPattern:
		return expression != nil && expression.name != "" &&
			len(expression.sourceFilters) == 0 && expression.sourceBind != nil &&
			expression.sourceBind.name != "" && expression.sourceBind.typ != "" &&
			len(expression.predicates) == 0
	case *wherePattern:
		if expression == nil || !IsModuleQualifiedSingleEventPattern(expression.pattern) {
			return false
		}
		_, err := DeterministicKey(expression)
		return err == nil
	default:
		return false
	}
}

// IsDeterministicSingleEventPattern reports whether expression denotes exactly
// one named event occurrence, with any supported fixed or bound source,
// deterministic parameter filters/bindings, and closed where conditions. It
// excludes compound, iterative, timing, universal, and opaque forms. Execution
// boundaries use this predicate when an event must be matched at its precise
// generation point rather than after a larger visible computation is built.
func IsDeterministicSingleEventPattern(expression Pattern) bool {
	switch expression := expression.(type) {
	case *BasicPattern:
		return expression != nil && expression.name != "" && len(expression.predicates) == 0
	case *wherePattern:
		if expression == nil || !IsDeterministicSingleEventPattern(expression.pattern) {
			return false
		}
		_, err := DeterministicKey(expression)
		return err == nil
	default:
		return false
	}
}

// IsUnqualifiedNonemptyEventPattern reports whether expression belongs to the
// deterministic source-pattern algebra, names only unqualified events, and
// cannot match an empty computation. It admits the compound relations and
// finite iterations produced by the Rapide source frontend, while excluding
// module-source qualifiers, timing/state-witness forms, universal
// qualifications, MatchAny, and opaque predicates.
func IsUnqualifiedNonemptyEventPattern(expression Pattern) bool {
	if !isUnqualifiedEventPattern(expression) {
		return false
	}
	if _, err := DeterministicKey(expression); err != nil {
		return false
	}
	requiresState, err := RequiresStateWitnesses(expression)
	if err != nil || requiresState {
		return false
	}
	empty, err := CanMatchEmpty(expression)
	return err == nil && !empty
}

// IsContextualNonemptyEventPattern reports whether expression belongs to the
// deterministic nonempty source-pattern algebra and contains at least one
// module-qualified leaf. Every other basic leaf must be unqualified: fixed
// source filters, MatchAny, opaque predicates, universal/timing/state-witness
// forms, and empty computations remain outside this dynamic route boundary.
// During execution, ScopeUnqualifiedEventSources gives those unqualified
// leaves their owning module while preserving module-qualified leaves.
func IsContextualNonemptyEventPattern(expression Pattern) bool {
	valid, qualified := isContextualEventPattern(expression)
	if !valid || !qualified {
		return false
	}
	if _, err := DeterministicKey(expression); err != nil {
		return false
	}
	requiresState, err := RequiresStateWitnesses(expression)
	if err != nil || requiresState {
		return false
	}
	empty, err := CanMatchEmpty(expression)
	return err == nil && !empty
}

func isContextualEventPattern(expression Pattern) (valid, qualified bool) {
	switch expression := expression.(type) {
	case *BasicPattern:
		if expression == nil || expression.name == "" || len(expression.sourceFilters) != 0 ||
			len(expression.predicates) != 0 {
			return false, false
		}
		return true, expression.sourceBind != nil
	case *seqPattern:
		return allContextualEventPatterns(expression.subs)
	case *immSeqPattern:
		return bothContextualEventPatterns(expression.p1, expression.p2)
	case *independentPattern:
		return bothContextualEventPatterns(expression.p1, expression.p2)
	case *disjointPattern:
		return bothContextualEventPatterns(expression.p1, expression.p2)
	case *unionPattern:
		return bothContextualEventPatterns(expression.p1, expression.p2)
	case *orPattern:
		return allContextualEventPatterns(expression.subs)
	case *andPattern:
		return allContextualEventPatterns(expression.subs)
	case *iterationPattern:
		if expression == nil {
			return false, false
		}
		return isContextualEventPattern(expression.pattern)
	case *namedRangeIterationPattern:
		if expression == nil {
			return false, false
		}
		return isContextualEventPattern(expression.pattern)
	case *wherePattern:
		if expression == nil {
			return false, false
		}
		return isContextualEventPattern(expression.pattern)
	default:
		return false, false
	}
}

func bothContextualEventPatterns(left, right Pattern) (valid, qualified bool) {
	return allContextualEventPatterns([]Pattern{left, right})
}

func allContextualEventPatterns(expressions []Pattern) (valid, qualified bool) {
	if len(expressions) == 0 {
		return false, false
	}
	for _, expression := range expressions {
		childValid, childQualified := isContextualEventPattern(expression)
		if !childValid {
			return false, false
		}
		qualified = qualified || childQualified
	}
	return true, qualified
}

func isUnqualifiedEventPattern(expression Pattern) bool {
	switch expression := expression.(type) {
	case *BasicPattern:
		return expression != nil && expression.name != "" &&
			len(expression.sourceFilters) == 0 && expression.sourceBind == nil &&
			len(expression.predicates) == 0
	case *seqPattern:
		return expression != nil && allUnqualifiedEventPatterns(expression.subs)
	case *immSeqPattern:
		return expression != nil && isUnqualifiedEventPattern(expression.p1) && isUnqualifiedEventPattern(expression.p2)
	case *independentPattern:
		return expression != nil && isUnqualifiedEventPattern(expression.p1) && isUnqualifiedEventPattern(expression.p2)
	case *disjointPattern:
		return expression != nil && isUnqualifiedEventPattern(expression.p1) && isUnqualifiedEventPattern(expression.p2)
	case *unionPattern:
		return expression != nil && isUnqualifiedEventPattern(expression.p1) && isUnqualifiedEventPattern(expression.p2)
	case *orPattern:
		return expression != nil && allUnqualifiedEventPatterns(expression.subs)
	case *andPattern:
		return expression != nil && allUnqualifiedEventPatterns(expression.subs)
	case *iterationPattern:
		return expression != nil && isUnqualifiedEventPattern(expression.pattern)
	case *namedRangeIterationPattern:
		return expression != nil && isUnqualifiedEventPattern(expression.pattern)
	case *wherePattern:
		return expression != nil && isUnqualifiedEventPattern(expression.pattern)
	default:
		return false
	}
}

func allUnqualifiedEventPatterns(expressions []Pattern) bool {
	if len(expressions) == 0 {
		return false
	}
	for _, expression := range expressions {
		if !isUnqualifiedEventPattern(expression) {
			return false
		}
	}
	return true
}

// DeterministicKey returns a stable semantic key for the currently supported
// deterministic pattern subset. Unsupported patterns and opaque Go predicates
// fail explicitly rather than being represented by an unstable String value.
func DeterministicKey(p Pattern) (string, error) {
	if p == nil {
		return `{"kind":"basic","name":""}`, nil
	}
	switch expression := p.(type) {
	case *BasicPattern:
		return deterministicBasicKey(expression)
	case *seqPattern:
		return deterministicCompositeKey("follows", expression.subs, false)
	case *immSeqPattern:
		return deterministicCompositeKey("immediate-follows", []Pattern{expression.p1, expression.p2}, false)
	case *independentPattern:
		return deterministicCompositeKey("independent", []Pattern{expression.p1, expression.p2}, true)
	case *disjointPattern:
		return deterministicCompositeKey("disjoint", []Pattern{expression.p1, expression.p2}, true)
	case *unionPattern:
		return deterministicCompositeKey("union", []Pattern{expression.p1, expression.p2}, true)
	case *orPattern:
		return deterministicCompositeKey("or", expression.subs, true)
	case *andPattern:
		return deterministicCompositeKey("and", expression.subs, true)
	case *iterationPattern:
		inner, err := DeterministicKey(expression.pattern)
		if err != nil {
			return "", err
		}
		descriptor := struct {
			Kind     string            `json:"kind"`
			Relation IterationRelation `json:"relation"`
			Minimum  int               `json:"minimum"`
			Maximum  int               `json:"maximum"`
			Pattern  string            `json:"pattern"`
		}{Kind: "iteration", Relation: expression.relation, Minimum: expression.min, Maximum: expression.max, Pattern: inner}
		encoded, err := json.Marshal(descriptor)
		if err != nil {
			return "", fmt.Errorf("pattern.DeterministicKey: %w", err)
		}
		return string(encoded), nil
	case *namedRangeIterationPattern:
		if err := validateNamedRangeIteration(expression); err != nil {
			return "", err
		}
		inner, err := DeterministicKey(expression.pattern)
		if err != nil {
			return "", err
		}
		descriptor := struct {
			Kind     string            `json:"kind"`
			Iterator string            `json:"iterator"`
			Type     string            `json:"type"`
			First    int64             `json:"first"`
			Last     int64             `json:"last"`
			Relation IterationRelation `json:"relation"`
			Pattern  string            `json:"pattern"`
		}{
			Kind: "named-integer-range-iteration", Iterator: expression.iterator.name,
			Type: "Integer", First: expression.first, Last: expression.last,
			Relation: expression.relation, Pattern: inner,
		}
		encoded, err := json.Marshal(descriptor)
		if err != nil {
			return "", fmt.Errorf("pattern.DeterministicKey: %w", err)
		}
		return string(encoded), nil
	case *universalRangePattern:
		if err := validateUniversalRangePattern(expression); err != nil {
			return "", err
		}
		inner, err := DeterministicKey(expression.pattern)
		if err != nil {
			return "", err
		}
		descriptor := struct {
			Kind        string            `json:"kind"`
			Placeholder string            `json:"placeholder"`
			Type        string            `json:"type"`
			First       int64             `json:"first"`
			Last        int64             `json:"last"`
			Relation    IterationRelation `json:"relation"`
			Pattern     string            `json:"pattern"`
		}{
			Kind: "universal-integer-range", Placeholder: expression.placeholder.name,
			Type: "Integer", First: expression.first, Last: expression.last,
			Relation: expression.relation, Pattern: inner,
		}
		encoded, err := json.Marshal(descriptor)
		if err != nil {
			return "", fmt.Errorf("pattern.DeterministicKey: %w", err)
		}
		return string(encoded), nil
	case *rapideTimingPattern:
		if err := validateRapideTimingPattern(expression); err != nil {
			return "", err
		}
		inner, err := DeterministicKey(expression.pattern)
		if err != nil {
			return "", err
		}
		descriptor := struct {
			Kind     string `json:"kind"`
			Pattern  string `json:"pattern"`
			Start    string `json:"start"`
			Duration string `json:"duration"`
			Clock    string `json:"clock"`
		}{Kind: "timing", Pattern: inner, Start: expression.start.name, Duration: expression.duration.name, Clock: expression.clock}
		encoded, err := json.Marshal(descriptor)
		if err != nil {
			return "", fmt.Errorf("pattern.DeterministicKey: %w", err)
		}
		return string(encoded), nil
	case *rapideTimingFilterPattern:
		return deterministicRapideTimingFilterKey(expression)
	case *rapideTimeBeforePattern:
		return deterministicRapideTimeBeforeKey(expression)
	case *wherePattern:
		inner, err := DeterministicKey(expression.pattern)
		if err != nil {
			return "", err
		}
		boundTypes := make(map[string]string)
		if err := collectBoundPlaceholderTypes(expression.pattern, boundTypes); err != nil {
			return "", err
		}
		conditionType, err := validateConditionType(expression.condition, boundTypes)
		if err != nil {
			return "", err
		}
		if conditionType != "" && conditionType != "Boolean" {
			return "", fmt.Errorf("%w: guard has type %s, want Boolean", ErrInvalidCondition, conditionType)
		}
		condition, err := deterministicConditionKey(expression.condition)
		if err != nil {
			return "", err
		}
		descriptor := struct {
			Kind      string `json:"kind"`
			Pattern   string `json:"pattern"`
			Condition string `json:"condition"`
		}{Kind: "where", Pattern: inner, Condition: condition}
		encoded, err := json.Marshal(descriptor)
		if err != nil {
			return "", fmt.Errorf("pattern.DeterministicKey: %w", err)
		}
		return string(encoded), nil
	default:
		return "", fmt.Errorf("%w: %T", ErrOpaquePattern, p)
	}
}

// DeterministicSingleEventKey validates the deterministic pattern subset that
// can be decided from one event observation. The initial architecture kernel
// uses this narrower contract until whole-poset connection firing is restored.
func DeterministicSingleEventKey(p Pattern) (string, error) {
	switch expression := p.(type) {
	case nil, *BasicPattern:
	case *wherePattern:
		if _, err := DeterministicSingleEventKey(expression.pattern); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("%w: single-event evaluation cannot decide %T", ErrOpaquePattern, p)
	}
	return DeterministicKey(p)
}

// CanMatchEmpty reports whether a deterministic pattern can produce a match
// containing no event occurrences. Connection activation currently requires a
// newly observed occurrence, so callers use this to reject unsupported
// zero-event triggers rather than silently ignoring them.
func CanMatchEmpty(p Pattern) (bool, error) {
	if p == nil {
		return false, nil
	}
	switch expression := p.(type) {
	case *BasicPattern:
		return false, nil
	case *seqPattern:
		return allPatternsCanMatchEmpty(expression.subs)
	case *immSeqPattern:
		return bothPatternsCanMatchEmpty(expression.p1, expression.p2)
	case *independentPattern:
		return bothPatternsCanMatchEmpty(expression.p1, expression.p2)
	case *disjointPattern:
		return bothPatternsCanMatchEmpty(expression.p1, expression.p2)
	case *unionPattern:
		return bothPatternsCanMatchEmpty(expression.p1, expression.p2)
	case *andPattern:
		return allPatternsCanMatchEmpty(expression.subs)
	case *orPattern:
		for _, operand := range expression.subs {
			empty, err := CanMatchEmpty(operand)
			if err != nil {
				return false, err
			}
			if empty {
				return true, nil
			}
		}
		return false, nil
	case *iterationPattern:
		if expression.min == 0 {
			return true, nil
		}
		return CanMatchEmpty(expression.pattern)
	case *namedRangeIterationPattern:
		cardinality, valid := namedRangeCardinality(expression.first, expression.last)
		if !valid {
			return false, ErrInvalidNamedIteration
		}
		if cardinality == 0 {
			return true, nil
		}
		return CanMatchEmpty(expression.pattern)
	case *universalRangePattern:
		if _, err := DeterministicKey(expression); err != nil {
			return false, err
		}
		return CanMatchEmpty(expression.pattern)
	case *rapideTimingPattern:
		if _, err := DeterministicKey(expression); err != nil {
			return false, err
		}
		return false, nil
	case *rapideTimingFilterPattern:
		if _, err := DeterministicKey(expression); err != nil {
			return false, err
		}
		return false, nil
	case *rapideTimeBeforePattern:
		if _, err := DeterministicKey(expression); err != nil {
			return false, err
		}
		return false, nil
	case *wherePattern:
		if _, err := DeterministicKey(expression); err != nil {
			return false, err
		}
		return CanMatchEmpty(expression.pattern)
	default:
		return false, fmt.Errorf("%w: %T", ErrOpaquePattern, p)
	}
}

func bothPatternsCanMatchEmpty(left, right Pattern) (bool, error) {
	return allPatternsCanMatchEmpty([]Pattern{left, right})
}

func allPatternsCanMatchEmpty(patterns []Pattern) (bool, error) {
	for _, operand := range patterns {
		empty, err := CanMatchEmpty(operand)
		if err != nil {
			return false, err
		}
		if !empty {
			return false, nil
		}
	}
	return true, nil
}

func deterministicBasicKey(basic *BasicPattern) (string, error) {
	if len(basic.predicates) != 0 {
		return "", fmt.Errorf("%w: basic pattern %q has Go predicate guards", ErrOpaquePattern, basic.name)
	}
	type canonicalFilter struct {
		Parameter string                  `json:"parameter"`
		Value     gorapide.CanonicalValue `json:"value"`
	}
	type canonicalBind struct {
		Parameter   string `json:"parameter"`
		Placeholder string `json:"placeholder"`
		Type        string `json:"type"`
	}
	descriptor := struct {
		Kind       string            `json:"kind"`
		Name       string            `json:"name"`
		Sources    []string          `json:"sources,omitempty"`
		SourceBind *canonicalBind    `json:"source_bind,omitempty"`
		Filters    []canonicalFilter `json:"filters,omitempty"`
		Binds      []canonicalBind   `json:"binds,omitempty"`
	}{Kind: "basic", Name: basic.name, Sources: append([]string(nil), basic.sourceFilters...)}
	sort.Strings(descriptor.Sources)
	for _, filter := range basic.parameterFilters {
		if filter.key == "" {
			return "", fmt.Errorf("%w: basic pattern has an empty parameter filter name", ErrOpaquePattern)
		}
		parameters, err := gorapide.CanonicalizeParameters(map[string]any{"value": filter.value})
		if err != nil {
			return "", fmt.Errorf("%w: parameter %q: %v", ErrOpaquePattern, filter.key, err)
		}
		descriptor.Filters = append(descriptor.Filters, canonicalFilter{Parameter: filter.key, Value: parameters[0].Value})
	}
	sort.Slice(descriptor.Filters, func(i, j int) bool {
		if descriptor.Filters[i].Parameter != descriptor.Filters[j].Parameter {
			return descriptor.Filters[i].Parameter < descriptor.Filters[j].Parameter
		}
		left, _ := json.Marshal(descriptor.Filters[i].Value)
		right, _ := json.Marshal(descriptor.Filters[j].Value)
		return string(left) < string(right)
	})
	if basic.sourceBind != nil {
		if basic.sourceBind.name == "" || basic.sourceBind.typ == "" ||
			gorapide.IsSupportedPredefinedType(basic.sourceBind.typ) {
			return "", fmt.Errorf("%w: basic pattern has an invalid module-source binding", ErrOpaquePattern)
		}
		descriptor.SourceBind = &canonicalBind{
			Placeholder: basic.sourceBind.name, Type: basic.sourceBind.typ,
		}
	}
	for _, binding := range basic.parameterBinds {
		if binding.key == "" || binding.placeholder == nil || binding.placeholder.name == "" {
			return "", fmt.Errorf("%w: basic pattern has an invalid parameter binding", ErrOpaquePattern)
		}
		if binding.placeholder.typ != "" && !gorapide.IsSupportedPredefinedType(binding.placeholder.typ) {
			return "", fmt.Errorf("%w: placeholder %q has unsupported type %q", ErrOpaquePattern, binding.placeholder.name, binding.placeholder.typ)
		}
		descriptor.Binds = append(descriptor.Binds, canonicalBind{
			Parameter: binding.key, Placeholder: binding.placeholder.name, Type: binding.placeholder.typ,
		})
	}
	sort.Slice(descriptor.Binds, func(i, j int) bool {
		if descriptor.Binds[i].Placeholder != descriptor.Binds[j].Placeholder {
			return descriptor.Binds[i].Placeholder < descriptor.Binds[j].Placeholder
		}
		if descriptor.Binds[i].Parameter != descriptor.Binds[j].Parameter {
			return descriptor.Binds[i].Parameter < descriptor.Binds[j].Parameter
		}
		return descriptor.Binds[i].Type < descriptor.Binds[j].Type
	})
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return "", fmt.Errorf("pattern.DeterministicKey: %w", err)
	}
	return string(encoded), nil
}

func deterministicCompositeKey(kind string, operands []Pattern, commutative bool) (string, error) {
	keys := make([]string, 0, len(operands))
	for _, operand := range operands {
		key, err := DeterministicKey(operand)
		if err != nil {
			return "", err
		}
		keys = append(keys, key)
	}
	if commutative {
		sort.Strings(keys)
	}
	descriptor := struct {
		Kind     string   `json:"kind"`
		Operands []string `json:"operands"`
	}{Kind: kind, Operands: keys}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return "", fmt.Errorf("pattern.DeterministicKey: %w", err)
	}
	return string(encoded), nil
}

// Placeholder represents a named variable for binding event values during
// pattern matching (analogous to ?X in Rapide). Placeholders will be used
// in complex pattern matching with variable binding in later extensions.
type Placeholder struct {
	name string // placeholder name
	typ  string // optional type constraint
}

// Var creates a new named Placeholder.
func Var(name string) *Placeholder {
	return &Placeholder{name: name}
}

// Name returns the placeholder's name.
func (ph *Placeholder) Name() string {
	return ph.name
}

// Type returns the placeholder's optional type constraint.
func (ph *Placeholder) Type() string {
	return ph.typ
}

// WithType sets a type constraint on the placeholder.
func (ph *Placeholder) WithType(typ string) *Placeholder {
	ph.typ = typ
	return ph
}

// String returns a human-readable representation of the placeholder.
func (ph *Placeholder) String() string {
	if ph.typ != "" {
		return fmt.Sprintf("?%s:%s", ph.name, ph.typ)
	}
	return "?" + ph.name
}
