package studio

import (
	"testing"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

func TestRecorderCapturesEvents(t *testing.T) {
	r := NewRecorder()
	obs := r.Observer()

	e1 := gorapide.NewEvent("click", "ui", nil)
	e2 := gorapide.NewEvent("submit", "ui", nil)

	obs(e1)
	obs(e2)

	events := r.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 recorded events, got %d", len(events))
	}

	if events[0].Event.Name != "click" {
		t.Errorf("expected first event name %q, got %q", "click", events[0].Event.Name)
	}
	if events[1].Event.Name != "submit" {
		t.Errorf("expected second event name %q, got %q", "submit", events[1].Event.Name)
	}

	if events[0].SeqNum != 0 {
		t.Errorf("expected SeqNum 0 for first event, got %d", events[0].SeqNum)
	}
	if events[1].SeqNum != 1 {
		t.Errorf("expected SeqNum 1 for second event, got %d", events[1].SeqNum)
	}

	// The first event is stamped at or very close to t=0; offset must be non-negative.
	if events[0].OffsetMs < 0 {
		t.Errorf("expected non-negative offset for first event, got %d", events[0].OffsetMs)
	}
	if events[1].OffsetMs < 0 {
		t.Errorf("expected non-negative offset for second event, got %d", events[1].OffsetMs)
	}
}

func TestRecorderReset(t *testing.T) {
	r := NewRecorder()
	obs := r.Observer()

	obs(gorapide.NewEvent("ping", "test", nil))

	if len(r.Events()) != 1 {
		t.Fatalf("expected 1 event before reset, got %d", len(r.Events()))
	}

	r.Reset()

	events := r.Events()
	if len(events) != 0 {
		t.Errorf("expected 0 events after reset, got %d", len(events))
	}
}
