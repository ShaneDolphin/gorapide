package studio

import (
	"sync"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

// makeEvents builds a slice of RecordedEvents with the supplied offsets.
func makeEvents(names []string, offsetsMs []int64) []RecordedEvent {
	events := make([]RecordedEvent, len(names))
	for i, name := range names {
		events[i] = RecordedEvent{
			Event:    gorapide.NewEvent(name, "test", nil),
			OffsetMs: offsetsMs[i],
			SeqNum:   i,
		}
	}
	return events
}

func TestReplayMachinePlay(t *testing.T) {
	// Three events at offsets 0 / 50 / 100 ms.
	// At 10x speed the gaps collapse to 5 ms each — well within a 2-second timeout.
	events := makeEvents(
		[]string{"a", "b", "c"},
		[]int64{0, 50, 100},
	)

	rm := NewReplayMachine(events)
	rm.SetSpeed(10.0)

	var mu sync.Mutex
	var received []string

	rm.OnEvent(func(re *RecordedEvent) {
		mu.Lock()
		received = append(received, re.Event.Name)
		mu.Unlock()
	})

	rm.Play()

	// Wait up to 2 seconds for all 3 events to be delivered.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	got := make([]string, len(received))
	copy(got, received)
	mu.Unlock()

	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d: %v", len(got), got)
	}
	expected := []string{"a", "b", "c"}
	for i, name := range expected {
		if got[i] != name {
			t.Errorf("event[%d]: expected %q, got %q", i, name, got[i])
		}
	}
}

func TestReplayMachinePause(t *testing.T) {
	// Two events: first at 0 ms, second at 500 ms.
	// At 1x speed the machine sleeps ~500 ms before firing the second event.
	// We pause after 100 ms — only the first event should have been delivered.
	events := makeEvents(
		[]string{"first", "second"},
		[]int64{0, 500},
	)

	rm := NewReplayMachine(events)
	rm.SetSpeed(1.0)

	var mu sync.Mutex
	var received []string

	rm.OnEvent(func(re *RecordedEvent) {
		mu.Lock()
		received = append(received, re.Event.Name)
		mu.Unlock()
	})

	rm.Play()
	time.Sleep(100 * time.Millisecond)
	rm.Pause()

	// Give a brief moment to ensure no extra callbacks fire after pause.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	got := make([]string, len(received))
	copy(got, received)
	mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("expected 1 event after pause, got %d: %v", len(got), got)
	}
	if got[0] != "first" {
		t.Errorf("expected %q, got %q", "first", got[0])
	}

	// Verify state is paused, not stopped.
	if rm.State() != ReplayPaused {
		t.Errorf("expected ReplayPaused state, got %v", rm.State())
	}
}

func TestReplayMachineTotal(t *testing.T) {
	events := makeEvents([]string{"x", "y", "z"}, []int64{0, 10, 20})
	rm := NewReplayMachine(events)

	if rm.Total() != 3 {
		t.Errorf("expected Total() == 3, got %d", rm.Total())
	}
}

func TestReplayMachineStopIdempotent(t *testing.T) {
	events := makeEvents([]string{"a"}, []int64{0})
	rm := NewReplayMachine(events)

	// Double-stop on a never-started machine must not panic.
	rm.Stop()
	rm.Stop()

	// Start and immediately stop twice — also must not panic.
	rm.Play()
	time.Sleep(5 * time.Millisecond)
	rm.Stop()
	rm.Stop()
}
