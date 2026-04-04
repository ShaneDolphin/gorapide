package studio

import (
	"sync"
	"time"
)

// ReplayState represents the current operational state of a ReplayMachine.
type ReplayState int

const (
	ReplayStopped ReplayState = iota
	ReplayPlaying
	ReplayPaused
)

// ReplayMachine plays back a slice of RecordedEvents at a configurable
// speed, with support for pause and stop.
type ReplayMachine struct {
	events  []RecordedEvent
	onEvent func(*RecordedEvent)
	speed   float64
	index   int
	state   ReplayState
	stopCh  chan struct{}
	mu      sync.Mutex
}

// NewReplayMachine creates a ReplayMachine loaded with the given events.
// Speed defaults to 1.0 (real-time).
func NewReplayMachine(events []RecordedEvent) *ReplayMachine {
	cp := make([]RecordedEvent, len(events))
	copy(cp, events)
	return &ReplayMachine{
		events: cp,
		speed:  1.0,
		state:  ReplayStopped,
	}
}

// OnEvent registers a callback invoked for each event during playback.
// It must be called before Play.
func (rm *ReplayMachine) OnEvent(fn func(*RecordedEvent)) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.onEvent = fn
}

// SetSpeed sets the playback multiplier. 1.0 = real-time, 10.0 = 10x fast.
// Values <= 0 are ignored.
func (rm *ReplayMachine) SetSpeed(s float64) {
	if s <= 0 {
		return
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.speed = s
}

// Play starts (or resumes) playback from the current index.
// If already playing, Play is a no-op.
func (rm *ReplayMachine) Play() {
	rm.mu.Lock()
	if rm.state == ReplayPlaying {
		rm.mu.Unlock()
		return
	}
	rm.state = ReplayPlaying
	rm.stopCh = make(chan struct{})
	// Capture values needed by goroutine under the lock.
	startIndex := rm.index
	speed := rm.speed
	events := rm.events
	onEvent := rm.onEvent
	stopCh := rm.stopCh
	rm.mu.Unlock()

	go rm.playFrom(startIndex, speed, events, onEvent, stopCh)
}

// playFrom is the playback goroutine. It fires onEvent for each event,
// sleeping scaled inter-event delays between them. It exits when it
// reaches the end of the event list or the stopCh is closed.
func (rm *ReplayMachine) playFrom(
	startIndex int,
	speed float64,
	events []RecordedEvent,
	onEvent func(*RecordedEvent),
	stopCh chan struct{},
) {
	for i := startIndex; i < len(events); i++ {
		// Calculate delay before this event fires.
		if i > 0 {
			deltaMs := events[i].OffsetMs - events[i-1].OffsetMs
			if deltaMs < 0 {
				deltaMs = 0
			}
			scaledMs := float64(deltaMs) / speed
			delay := time.Duration(scaledMs * float64(time.Millisecond))
			if delay > 0 {
				select {
				case <-stopCh:
					// Pause/Stop: persist current index and return.
					rm.mu.Lock()
					rm.index = i
					rm.mu.Unlock()
					return
				case <-time.After(delay):
				}
			}
		}

		// Check again after sleeping — stop may have fired during zero-delay path.
		select {
		case <-stopCh:
			rm.mu.Lock()
			rm.index = i
			rm.mu.Unlock()
			return
		default:
		}

		// Fire callback.
		if onEvent != nil {
			ev := events[i]
			onEvent(&ev)
		}

		// Advance persisted index.
		rm.mu.Lock()
		rm.index = i + 1
		rm.mu.Unlock()
	}

	// Reached end of events — transition to stopped state.
	rm.mu.Lock()
	rm.state = ReplayStopped
	rm.mu.Unlock()
}

// Pause halts playback while preserving the current index so Play can resume.
// If not playing, Pause is a no-op.
func (rm *ReplayMachine) Pause() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.state != ReplayPlaying {
		return
	}
	rm.state = ReplayPaused
	close(rm.stopCh)
}

// Stop halts playback and resets the index to 0 so the next Play starts
// from the beginning. Safe to call multiple times.
func (rm *ReplayMachine) Stop() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.state == ReplayPlaying {
		close(rm.stopCh)
	}
	rm.state = ReplayStopped
	rm.index = 0
}

// Total returns the total number of events in the machine.
func (rm *ReplayMachine) Total() int {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return len(rm.events)
}

// Current returns the index of the next event to be played.
func (rm *ReplayMachine) Current() int {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.index
}

// State returns the current ReplayState.
func (rm *ReplayMachine) State() ReplayState {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.state
}
