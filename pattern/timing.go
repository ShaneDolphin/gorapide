package pattern

import (
	"fmt"
	"time"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// --- During Pattern ---

// duringPattern matches inner pattern p, keeping only events with
// WallTime within [start, end].
type duringPattern struct {
	p     Pattern
	start time.Time
	end   time.Time
}

// During matches pattern p, but only includes events whose WallTime
// is within [start, end] inclusive.
func During(p Pattern, start, end time.Time) Pattern {
	if p == nil {
		panic("pattern.During: requires a non-nil sub-pattern")
	}
	return &duringPattern{p: p, start: start, end: end}
}

func (dp *duringPattern) Match(pr PosetReader) []gorapide.EventSet {
	inner := dp.p.Match(pr)
	results := make([]gorapide.EventSet, 0)
	for _, es := range inner {
		filtered := filterByTime(es, func(t time.Time) bool {
			return !t.Before(dp.start) && !t.After(dp.end)
		})
		if len(filtered) > 0 {
			results = append(results, filtered)
		}
	}
	return results
}

func (dp *duringPattern) String() string {
	return fmt.Sprintf("During(%s, %s..%s)", dp.p.String(),
		dp.start.Format(time.RFC3339), dp.end.Format(time.RFC3339))
}

// --- Within Pattern ---

// withinPattern matches inner pattern p, keeping only match sets where
// the time span from earliest to latest event is <= duration.
type withinPattern struct {
	p        Pattern
	duration time.Duration
}

// Within matches pattern p, but only includes match sets where the
// time span from the earliest to the latest event WallTime is <= duration.
func Within(p Pattern, duration time.Duration) Pattern {
	if p == nil {
		panic("pattern.Within: requires a non-nil sub-pattern")
	}
	return &withinPattern{p: p, duration: duration}
}

func (wp *withinPattern) Match(pr PosetReader) []gorapide.EventSet {
	inner := wp.p.Match(pr)
	results := make([]gorapide.EventSet, 0)
	for _, es := range inner {
		if len(es) == 0 {
			continue
		}
		earliest, latest := timeSpan(es)
		if latest.Sub(earliest) <= wp.duration {
			results = append(results, es)
		}
	}
	return results
}

func (wp *withinPattern) String() string {
	return fmt.Sprintf("Within(%s, %s)", wp.p.String(), wp.duration)
}

// --- After Pattern ---

// afterPattern matches inner pattern p, keeping only events after
// the reference time.
type afterPattern struct {
	p   Pattern
	ref time.Time
}

// After matches pattern p, but only includes events whose WallTime
// is strictly after the reference time.
func After(p Pattern, reference time.Time) Pattern {
	if p == nil {
		panic("pattern.After: requires a non-nil sub-pattern")
	}
	return &afterPattern{p: p, ref: reference}
}

func (ap *afterPattern) Match(pr PosetReader) []gorapide.EventSet {
	inner := ap.p.Match(pr)
	results := make([]gorapide.EventSet, 0)
	for _, es := range inner {
		filtered := filterByTime(es, func(t time.Time) bool {
			return t.After(ap.ref)
		})
		if len(filtered) > 0 {
			results = append(results, filtered)
		}
	}
	return results
}

func (ap *afterPattern) String() string {
	return fmt.Sprintf("After(%s, %s)", ap.p.String(), ap.ref.Format(time.RFC3339))
}

// --- Before Pattern ---

// beforePattern matches inner pattern p, keeping only events before
// the reference time.
type beforePattern struct {
	p   Pattern
	ref time.Time
}

// Before matches pattern p, but only includes events whose WallTime
// is strictly before the reference time.
func Before(p Pattern, reference time.Time) Pattern {
	if p == nil {
		panic("pattern.Before: requires a non-nil sub-pattern")
	}
	return &beforePattern{p: p, ref: reference}
}

func (bp *beforePattern) Match(pr PosetReader) []gorapide.EventSet {
	inner := bp.p.Match(pr)
	results := make([]gorapide.EventSet, 0)
	for _, es := range inner {
		filtered := filterByTime(es, func(t time.Time) bool {
			return t.Before(bp.ref)
		})
		if len(filtered) > 0 {
			results = append(results, filtered)
		}
	}
	return results
}

func (bp *beforePattern) String() string {
	return fmt.Sprintf("Before(%s, %s)", bp.p.String(), bp.ref.Format(time.RFC3339))
}

// --- Helpers ---

// filterByTime returns the subset of events matching the time predicate.
func filterByTime(es gorapide.EventSet, pred func(time.Time) bool) gorapide.EventSet {
	var result gorapide.EventSet
	for _, e := range es {
		if pred(e.Clock.WallTime) {
			result = append(result, e)
		}
	}
	return result
}

// timeSpan returns the earliest and latest WallTime in the event set.
func timeSpan(es gorapide.EventSet) (earliest, latest time.Time) {
	earliest = es[0].Clock.WallTime
	latest = es[0].Clock.WallTime
	for _, e := range es[1:] {
		if e.Clock.WallTime.Before(earliest) {
			earliest = e.Clock.WallTime
		}
		if e.Clock.WallTime.After(latest) {
			latest = e.Clock.WallTime
		}
	}
	return
}
