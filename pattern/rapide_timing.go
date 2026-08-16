package pattern

import (
	"errors"
	"fmt"
	"math"

	"github.com/ShaneDolphin/gorapide"
)

var (
	ErrInvalidRapideTimingPattern = errors.New("invalid deterministic Rapide timing pattern")
	ErrTimingBindingRange         = errors.New("Rapide timing binding is outside the supported Integer range")
)

// rapideTimingPattern implements Stanford Rapide's predefined
// Timing(P, T, D, C) primitive. T is the earliest Start on C and D is the
// latest Finish on C minus T. This is unrelated to the legacy wall-time
// helpers in timing.go.
type rapideTimingPattern struct {
	pattern  Pattern
	start    *Placeholder
	duration *Placeholder
	clock    string
}

// Timing constructs the deterministic Rapide timing primitive
// Timing(P,T,D,C). Every event in a matching subcomputation must be related to
// clock. The start and duration placeholders are Integer outputs of the
// primitive and are unified with any bindings of the same names in P.
func Timing(p Pattern, start, duration *Placeholder, clock string) Pattern {
	if p == nil {
		panic("pattern.Timing: requires a non-nil sub-pattern")
	}
	if start == nil || duration == nil {
		panic("pattern.Timing: requires start and duration placeholders")
	}
	startCopy := *start
	durationCopy := *duration
	if startCopy.typ == "" {
		startCopy.typ = "Integer"
	}
	if durationCopy.typ == "" {
		durationCopy.typ = "Integer"
	}
	return &rapideTimingPattern{pattern: p, start: &startCopy, duration: &durationCopy, clock: clock}
}

func (tp *rapideTimingPattern) Match(poset PosetReader) []gorapide.EventSet {
	matches, err := MatchWithBindings(tp, poset)
	if err != nil {
		return []gorapide.EventSet{}
	}
	result := make([]gorapide.EventSet, 0, len(matches))
	for _, match := range matches {
		result = append(result, match.Events)
	}
	return result
}

func (tp *rapideTimingPattern) String() string {
	if tp == nil {
		return "Timing(<nil>)"
	}
	return fmt.Sprintf("Timing(%s, %s, %s, %q)", tp.pattern.String(), tp.start, tp.duration, tp.clock)
}

func validateRapideTimingPattern(tp *rapideTimingPattern) error {
	if tp == nil || tp.pattern == nil || tp.clock == "" || tp.start == nil || tp.duration == nil {
		return fmt.Errorf("%w: pattern, clock, start, and duration are required", ErrInvalidRapideTimingPattern)
	}
	placeholders := []struct {
		role        string
		placeholder *Placeholder
	}{{"start", tp.start}, {"duration", tp.duration}}
	for _, declaration := range placeholders {
		role, placeholder := declaration.role, declaration.placeholder
		if placeholder.name == "" {
			return fmt.Errorf("%w: %s placeholder name is empty", ErrInvalidRapideTimingPattern, role)
		}
		if placeholder.typ != "Integer" {
			return fmt.Errorf("%w: %s placeholder %q has type %q, want Integer", ErrInvalidRapideTimingPattern, role, placeholder.name, placeholder.typ)
		}
	}
	return nil
}

func matchRapideTimingWithBindings(tp *rapideTimingPattern, poset PosetReader) ([]MatchResult, error) {
	if err := validateRapideTimingPattern(tp); err != nil {
		return nil, err
	}
	inner, err := matchWithBindings(tp.pattern, poset)
	if err != nil {
		return nil, err
	}
	inner, err = canonicalizeMatchResults(inner)
	if err != nil {
		return nil, err
	}
	result := make([]MatchResult, 0, len(inner))
	for _, match := range inner {
		earliest, latest, related := timingExtent(match.Events, tp.clock)
		if !related {
			continue
		}
		duration := latest - earliest
		if earliest > math.MaxInt64 || duration > math.MaxInt64 {
			return nil, fmt.Errorf("%w: clock %q produced start %d and duration %d", ErrTimingBindingRange, tp.clock, earliest, duration)
		}
		timingBindings := Bindings{
			{Placeholder: tp.start.name, Value: int64(earliest)},
			{Placeholder: tp.duration.name, Value: int64(duration)},
		}
		bindings, compatible, err := mergeBindings(match.Bindings, timingBindings)
		if err != nil {
			return nil, err
		}
		if compatible {
			result = append(result, MatchResult{Events: match.Events, Bindings: bindings})
		}
	}
	return result, nil
}
