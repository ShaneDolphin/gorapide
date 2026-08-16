package pattern

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ShaneDolphin/gorapide"
)

type rapideTimingFilterKind string

const (
	rapideAtFilter          rapideTimingFilterKind = "at"
	rapideBeforeFilter      rapideTimingFilterKind = "before"
	rapideAfterFilter       rapideTimingFilterKind = "after"
	rapideWithinFilter      rapideTimingFilterKind = "within"
	rapideWithinRangeFilter rapideTimingFilterKind = "within-range"
)

type rapideTimingFilterPattern struct {
	pattern Pattern
	kind    rapideTimingFilterKind
	lower   uint64
	upper   uint64
	clock   string
}

type rapideTimeBeforePattern struct {
	left  Pattern
	right Pattern
	clock string
}

// RapideAt matches P when its earliest start is tick and its duration is zero
// on clock. It is the duration-consistent form of predefined at(P,Time,C).
func RapideAt(p Pattern, tick uint64, clock string) Pattern {
	return newRapideTimingFilter(p, rapideAtFilter, tick, tick, clock)
}

// RapideBefore matches P when its latest finish on clock is <= tick.
func RapideBefore(p Pattern, tick uint64, clock string) Pattern {
	return newRapideTimingFilter(p, rapideBeforeFilter, 0, tick, clock)
}

// RapideAfter matches P when its earliest start on clock is >= tick.
func RapideAfter(p Pattern, tick uint64, clock string) Pattern {
	return newRapideTimingFilter(p, rapideAfterFilter, tick, 0, clock)
}

// RapideWithin matches P when its duration on clock is <= ticks.
func RapideWithin(p Pattern, ticks uint64, clock string) Pattern {
	return newRapideTimingFilter(p, rapideWithinFilter, 0, ticks, clock)
}

// RapideWithinRange matches P when its duration is in the inclusive range.
// A reversed range is retained as invalid closed model data and rejected by
// deterministic validation rather than panicking or silently swapping bounds.
func RapideWithinRange(p Pattern, minimum, maximum uint64, clock string) Pattern {
	return newRapideTimingFilter(p, rapideWithinRangeFilter, minimum, maximum, clock)
}

// RapideTimeBefore matches P1 and P2 when P1's latest finish is strictly less
// than P2's earliest start on clock. Temporal order does not add causal edges.
func RapideTimeBefore(left, right Pattern, clock string) Pattern {
	if left == nil || right == nil {
		panic("pattern.RapideTimeBefore: requires two non-nil sub-patterns")
	}
	return &rapideTimeBeforePattern{left: left, right: right, clock: clock}
}

func newRapideTimingFilter(p Pattern, kind rapideTimingFilterKind, lower, upper uint64, clock string) Pattern {
	if p == nil {
		panic("pattern Rapide timing macro: requires a non-nil sub-pattern")
	}
	return &rapideTimingFilterPattern{pattern: p, kind: kind, lower: lower, upper: upper, clock: clock}
}

func validateRapideTimingFilter(filter *rapideTimingFilterPattern) error {
	if filter == nil || filter.pattern == nil || filter.clock == "" {
		return fmt.Errorf("%w: timing macro pattern and clock are required", ErrInvalidRapideTimingPattern)
	}
	switch filter.kind {
	case rapideAtFilter, rapideBeforeFilter, rapideAfterFilter, rapideWithinFilter:
		return nil
	case rapideWithinRangeFilter:
		if filter.lower > filter.upper {
			return fmt.Errorf("%w: within range %d..%d is reversed", ErrInvalidRapideTimingPattern, filter.lower, filter.upper)
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown timing macro %q", ErrInvalidRapideTimingPattern, filter.kind)
	}
}

func validateRapideTimeBefore(expression *rapideTimeBeforePattern) error {
	if expression == nil || expression.left == nil || expression.right == nil || expression.clock == "" {
		return fmt.Errorf("%w: temporal-order patterns and clock are required", ErrInvalidRapideTimingPattern)
	}
	return nil
}

func (filter *rapideTimingFilterPattern) Match(poset PosetReader) []gorapide.EventSet {
	return projectedClosedMatches(filter, poset)
}

func (filter *rapideTimingFilterPattern) String() string {
	if filter == nil {
		return "RapideTimingMacro(<nil>)"
	}
	if filter.kind == rapideWithinRangeFilter {
		return fmt.Sprintf("RapideWithinRange(%s, %d, %d, %q)", filter.pattern.String(), filter.lower, filter.upper, filter.clock)
	}
	return fmt.Sprintf("Rapide%s(%s, %d, %q)", titleTimingFilter(filter.kind), filter.pattern.String(), filter.upperOrLower(), filter.clock)
}

func (expression *rapideTimeBeforePattern) Match(poset PosetReader) []gorapide.EventSet {
	return projectedClosedMatches(expression, poset)
}

func (expression *rapideTimeBeforePattern) String() string {
	if expression == nil {
		return "RapideTimeBefore(<nil>)"
	}
	return fmt.Sprintf("RapideTimeBefore(%s, %s, %q)", expression.left.String(), expression.right.String(), expression.clock)
}

func titleTimingFilter(kind rapideTimingFilterKind) string {
	switch kind {
	case rapideAtFilter:
		return "At"
	case rapideBeforeFilter:
		return "Before"
	case rapideAfterFilter:
		return "After"
	case rapideWithinFilter:
		return "Within"
	default:
		return "TimingMacro"
	}
}

func (filter *rapideTimingFilterPattern) upperOrLower() uint64 {
	if filter.kind == rapideAfterFilter || filter.kind == rapideAtFilter {
		return filter.lower
	}
	return filter.upper
}

func projectedClosedMatches(expression Pattern, poset PosetReader) []gorapide.EventSet {
	matches, err := MatchWithBindings(expression, poset)
	if err != nil {
		return []gorapide.EventSet{}
	}
	result := make([]gorapide.EventSet, 0, len(matches))
	for _, match := range matches {
		result = append(result, match.Events)
	}
	return result
}

func matchRapideTimingFilterWithBindings(filter *rapideTimingFilterPattern, poset PosetReader) ([]MatchResult, error) {
	if err := validateRapideTimingFilter(filter); err != nil {
		return nil, err
	}
	inner, err := matchWithBindings(filter.pattern, poset)
	if err != nil {
		return nil, err
	}
	inner, err = canonicalizeMatchResults(inner)
	if err != nil {
		return nil, err
	}
	result := make([]MatchResult, 0, len(inner))
	for _, match := range inner {
		earliest, latest, related := timingExtent(match.Events, filter.clock)
		if !related {
			continue
		}
		duration := latest - earliest
		accepted := false
		switch filter.kind {
		case rapideAtFilter:
			accepted = earliest == filter.lower && duration == 0
		case rapideBeforeFilter:
			accepted = latest <= filter.upper
		case rapideAfterFilter:
			accepted = earliest >= filter.lower
		case rapideWithinFilter:
			accepted = duration <= filter.upper
		case rapideWithinRangeFilter:
			accepted = filter.lower <= duration && duration <= filter.upper
		}
		if accepted {
			result = append(result, match)
		}
	}
	return result, nil
}

func matchRapideTimeBeforeWithBindings(expression *rapideTimeBeforePattern, poset PosetReader) ([]MatchResult, error) {
	if err := validateRapideTimeBefore(expression); err != nil {
		return nil, err
	}
	leftMatches, err := matchWithBindings(expression.left, poset)
	if err != nil {
		return nil, err
	}
	rightMatches, err := matchWithBindings(expression.right, poset)
	if err != nil {
		return nil, err
	}
	leftMatches, err = canonicalizeMatchResults(leftMatches)
	if err != nil {
		return nil, err
	}
	rightMatches, err = canonicalizeMatchResults(rightMatches)
	if err != nil {
		return nil, err
	}
	result := make([]MatchResult, 0)
	for _, left := range leftMatches {
		_, leftFinish, leftRelated := timingExtent(left.Events, expression.clock)
		if !leftRelated {
			continue
		}
		for _, right := range rightMatches {
			rightStart, _, rightRelated := timingExtent(right.Events, expression.clock)
			if !rightRelated || leftFinish >= rightStart {
				continue
			}
			bindings, compatible, err := mergeBindings(left.Bindings, right.Bindings)
			if err != nil {
				return nil, err
			}
			if compatible {
				result = append(result, MatchResult{Events: mergeEventSets(left.Events, right.Events), Bindings: bindings})
			}
		}
	}
	return result, nil
}

func timingExtent(events gorapide.EventSet, clock string) (earliest, latest uint64, related bool) {
	if len(events) == 0 || clock == "" {
		return 0, 0, false
	}
	for index, event := range events {
		timing, ok := event.Timing(clock)
		if !ok {
			return 0, 0, false
		}
		if index == 0 || timing.Start < earliest {
			earliest = timing.Start
		}
		if index == 0 || timing.Finish > latest {
			latest = timing.Finish
		}
	}
	return earliest, latest, true
}

func deterministicRapideTimingFilterKey(filter *rapideTimingFilterPattern) (string, error) {
	if err := validateRapideTimingFilter(filter); err != nil {
		return "", err
	}
	inner, err := DeterministicKey(filter.pattern)
	if err != nil {
		return "", err
	}
	descriptor := struct {
		Kind    string `json:"kind"`
		Macro   string `json:"macro"`
		Pattern string `json:"pattern"`
		Lower   string `json:"lower"`
		Upper   string `json:"upper"`
		Clock   string `json:"clock"`
	}{
		Kind: "timing-macro", Macro: string(filter.kind), Pattern: inner,
		Lower: strconv.FormatUint(filter.lower, 10), Upper: strconv.FormatUint(filter.upper, 10), Clock: filter.clock,
	}
	return marshalPatternDescriptor(descriptor)
}

func deterministicRapideTimeBeforeKey(expression *rapideTimeBeforePattern) (string, error) {
	if err := validateRapideTimeBefore(expression); err != nil {
		return "", err
	}
	left, err := DeterministicKey(expression.left)
	if err != nil {
		return "", err
	}
	right, err := DeterministicKey(expression.right)
	if err != nil {
		return "", err
	}
	descriptor := struct {
		Kind  string `json:"kind"`
		Left  string `json:"left"`
		Right string `json:"right"`
		Clock string `json:"clock"`
	}{Kind: "time-before", Left: left, Right: right, Clock: expression.clock}
	return marshalPatternDescriptor(descriptor)
}

func marshalPatternDescriptor(descriptor any) (string, error) {
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return "", fmt.Errorf("pattern.DeterministicKey: %w", err)
	}
	return string(encoded), nil
}
