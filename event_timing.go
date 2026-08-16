package gorapide

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrInvalidEventTiming = errors.New("invalid Rapide event timing")
	ErrTimingNotRelated   = errors.New("event is not related to the named Rapide clock")
	ErrTimingCausality    = errors.New("Rapide event timing conflicts with causal order")
)

// EventTiming is one event's closed interval on a named Rapide local clock.
// Ticks are Natural values. Start and Finish model event duration and are not
// wall time, Lamport time, or a total order between independent clocks.
type EventTiming struct {
	Clock  string `json:"clock"`
	Start  uint64 `json:"start"`
	Finish uint64 `json:"finish"`
}

// CanonicalizeEventTimings validates, copies, and orders timing intervals by
// clock identity. One event may be related to several clocks, but at most once
// to each clock.
func CanonicalizeEventTimings(timings []EventTiming) ([]EventTiming, error) {
	result := append([]EventTiming(nil), timings...)
	for index, timing := range result {
		if timing.Clock == "" {
			return nil, fmt.Errorf("%w: interval %d has an empty clock identity", ErrInvalidEventTiming, index)
		}
		if timing.Finish < timing.Start {
			return nil, fmt.Errorf("%w: clock %q finishes at %d before it starts at %d", ErrInvalidEventTiming, timing.Clock, timing.Finish, timing.Start)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Clock < result[j].Clock })
	for index := 1; index < len(result); index++ {
		if result[index-1].Clock == result[index].Clock {
			return nil, fmt.Errorf("%w: duplicate clock identity %q", ErrInvalidEventTiming, result[index].Clock)
		}
	}
	return result, nil
}

// Timing returns a defensive copy of the event's interval on clock.
func (e *Event) Timing(clock string) (EventTiming, bool) {
	if e == nil || clock == "" {
		return EventTiming{}, false
	}
	index := sort.Search(len(e.Timings), func(i int) bool { return e.Timings[i].Clock >= clock })
	if index == len(e.Timings) || e.Timings[index].Clock != clock {
		return EventTiming{}, false
	}
	return e.Timings[index], true
}

// StartTime returns the event's start tick on clock.
func (e *Event) StartTime(clock string) (uint64, error) {
	timing, ok := e.Timing(clock)
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrTimingNotRelated, clock)
	}
	return timing.Start, nil
}

// FinishTime returns the event's finish tick on clock.
func (e *Event) FinishTime(clock string) (uint64, error) {
	timing, ok := e.Timing(clock)
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrTimingNotRelated, clock)
	}
	return timing.Finish, nil
}

// Length returns Finish-Start on clock.
func (e *Event) Length(clock string) (uint64, error) {
	timing, ok := e.Timing(clock)
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrTimingNotRelated, clock)
	}
	return timing.Finish - timing.Start, nil
}

func sharedTimingConflict(before, after *Event) error {
	if before == nil || after == nil {
		return nil
	}
	left, right := 0, 0
	for left < len(before.Timings) && right < len(after.Timings) {
		switch {
		case before.Timings[left].Clock < after.Timings[right].Clock:
			left++
		case before.Timings[left].Clock > after.Timings[right].Clock:
			right++
		default:
			if before.Timings[left].Finish > after.Timings[right].Start {
				return fmt.Errorf("%w: %s finishes at %s:%d after %s starts at %d",
					ErrTimingCausality, before.ID, before.Timings[left].Clock, before.Timings[left].Finish,
					after.ID, after.Timings[right].Start)
			}
			left++
			right++
		}
	}
	return nil
}
