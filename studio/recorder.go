package studio

import (
	"sync"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

// RecordedEvent is a single captured event with timing metadata.
type RecordedEvent struct {
	Event    *gorapide.Event `json:"event"`
	OffsetMs int64           `json:"offset_ms"`
	SeqNum   int             `json:"seq_num"`
}

// Recorder captures events emitted through an architecture observer and
// annotates each with its wall-clock offset (in milliseconds) from the
// first observed event and a monotonically increasing sequence number.
type Recorder struct {
	events    []RecordedEvent
	startTime time.Time
	started   bool
	seqNum    int
	mu        sync.Mutex
}

// NewRecorder returns an empty, ready-to-use Recorder.
func NewRecorder() *Recorder { return &Recorder{} }

// Observer returns a callback compatible with arch.WithObserver.
// The first event received starts the clock; subsequent events are
// stamped with the elapsed milliseconds since that moment.
func (r *Recorder) Observer() func(*gorapide.Event) {
	return func(e *gorapide.Event) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if !r.started {
			r.startTime = time.Now()
			r.started = true
		}
		r.events = append(r.events, RecordedEvent{
			Event:    e,
			OffsetMs: time.Since(r.startTime).Milliseconds(),
			SeqNum:   r.seqNum,
		})
		r.seqNum++
	}
}

// Events returns a snapshot copy of all recorded events.
func (r *Recorder) Events() []RecordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]RecordedEvent, len(r.events))
	copy(result, r.events)
	return result
}

// Reset clears all recorded events and resets internal state so the
// Recorder can be reused for a new recording session.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
	r.seqNum = 0
	r.started = false
}
