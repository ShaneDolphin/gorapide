package constraint

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// --- NewChecker ---

func TestNewChecker(t *testing.T) {
	cs := NewConstraintSet("test")
	ch := NewChecker(cs, CheckAfter)
	if ch == nil {
		t.Fatal("NewChecker returned nil")
	}
}

// --- CheckAfter mode ---

func TestCheckAfterRunsOnStop(t *testing.T) {
	cs := NewConstraintSet("after_test")
	cs.Add(NewConstraint("c1").
		Must("has_x", pattern.MatchEvent("X"), "X required").
		Build())

	p := gorapide.NewPoset() // empty — will violate

	ch := NewChecker(cs, CheckAfter)
	ch.Run(context.Background(), p)
	ch.Stop()

	violations := ch.Violations()
	if len(violations) != 1 {
		t.Fatalf("CheckAfter: want 1 violation, got %d", len(violations))
	}
	if violations[0].Message != "X required" {
		t.Errorf("message: got %s", violations[0].Message)
	}
}

func TestCheckAfterNoViolations(t *testing.T) {
	cs := NewConstraintSet("after_pass")
	cs.Add(NewConstraint("c1").
		Must("has_x", pattern.MatchEvent("X"), "X required").
		Build())

	p := gorapide.Build().Event("X").MustDone()

	ch := NewChecker(cs, CheckAfter)
	ch.Run(context.Background(), p)
	ch.Stop()

	if len(ch.Violations()) != 0 {
		t.Errorf("no violations expected, got %d", len(ch.Violations()))
	}
}

// --- CheckPeriodic mode ---

func TestCheckPeriodicCatchesViolation(t *testing.T) {
	cs := NewConstraintSet("periodic_test")
	cs.Add(EventCount("Bad", 0, 0)) // "Bad" events must not exist

	p := gorapide.NewPoset()

	var mu sync.Mutex
	var violations []ConstraintViolation

	ch := NewChecker(cs, CheckPeriodic).
		SetInterval(20 * time.Millisecond).
		OnViolation(func(v ConstraintViolation) {
			mu.Lock()
			violations = append(violations, v)
			mu.Unlock()
		})

	ch.Run(context.Background(), p)

	// Initially no violations — no "Bad" events.
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	count := len(violations)
	mu.Unlock()
	if count != 0 {
		t.Fatalf("no Bad events yet: want 0 violations, got %d", count)
	}

	// Add a "Bad" event — next periodic check should catch it.
	bad := gorapide.NewEvent("Bad", "test", nil)
	p.AddEvent(bad)

	time.Sleep(50 * time.Millisecond)
	ch.Stop()

	mu.Lock()
	count = len(violations)
	mu.Unlock()
	if count == 0 {
		t.Error("periodic checker should have caught the Bad event")
	}
}

// --- CheckOnEvent mode ---

func TestCheckOnEventTriggersAfterBatch(t *testing.T) {
	cs := NewConstraintSet("on_event_test")
	cs.Add(EventCount("Bad", 0, 0))

	p := gorapide.NewPoset()

	var mu sync.Mutex
	var violations []ConstraintViolation

	ch := NewChecker(cs, CheckOnEvent).
		SetBatchSize(3).
		OnViolation(func(v ConstraintViolation) {
			mu.Lock()
			violations = append(violations, v)
			mu.Unlock()
		})

	ch.Run(context.Background(), p)

	// Add 2 events — below batch threshold.
	p.AddEvent(gorapide.NewEvent("Bad", "test", nil))
	ch.NotifyEvent()
	p.AddEvent(gorapide.NewEvent("OK", "test", nil))
	ch.NotifyEvent()

	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	count := len(violations)
	mu.Unlock()
	if count != 0 {
		t.Fatalf("below batch size: want 0 violations notified, got %d", count)
	}

	// Third event triggers batch check.
	p.AddEvent(gorapide.NewEvent("OK", "test", nil))
	ch.NotifyEvent()

	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	count = len(violations)
	mu.Unlock()
	if count == 0 {
		t.Error("batch threshold reached: should have violations")
	}

	ch.Stop()
}

// --- OnViolation callback ---

func TestOnViolationCallback(t *testing.T) {
	cs := NewConstraintSet("callback_test")
	cs.Add(NewConstraint("c1").
		Severity("error").
		Must("has_x", pattern.MatchEvent("X"), "X required").
		Build())

	p := gorapide.NewPoset()

	var received []ConstraintViolation
	var mu sync.Mutex

	ch := NewChecker(cs, CheckAfter).
		OnViolation(func(v ConstraintViolation) {
			mu.Lock()
			received = append(received, v)
			mu.Unlock()
		})

	ch.Run(context.Background(), p)
	ch.Stop()

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("callback: want 1, got %d", len(received))
	}
	if received[0].Message != "X required" {
		t.Errorf("message: got %s", received[0].Message)
	}
	if received[0].Severity != "error" {
		t.Errorf("severity: got %s", received[0].Severity)
	}
}

// --- Violations() accumulates across checks ---

func TestViolationsAccumulate(t *testing.T) {
	cs := NewConstraintSet("accum")
	cs.Add(EventCount("Bad", 0, 0))

	p := gorapide.NewPoset()
	p.AddEvent(gorapide.NewEvent("Bad", "test", nil))

	ch := NewChecker(cs, CheckPeriodic).
		SetInterval(15 * time.Millisecond)

	ch.Run(context.Background(), p)
	time.Sleep(50 * time.Millisecond)
	ch.Stop()

	violations := ch.Violations()
	if len(violations) < 2 {
		t.Errorf("periodic should accumulate violations across ticks, got %d", len(violations))
	}
}

// --- Report() ---

func TestCheckerReport(t *testing.T) {
	cs := NewConstraintSet("report_test")
	cs.Add(NewConstraint("c1").
		Must("has_x", pattern.MatchEvent("X"), "X required").
		Build())

	p := gorapide.NewPoset()

	ch := NewChecker(cs, CheckAfter)
	ch.Run(context.Background(), p)
	ch.Stop()

	report := ch.Report()
	if len(report) == 0 {
		t.Error("report should not be empty")
	}
	if !strings.Contains(report, "X required") {
		t.Errorf("report should contain violation message: got %s", report)
	}
	if !strings.Contains(report, "1 violation") {
		t.Errorf("report should mention violation count: got %s", report)
	}
}

func TestCheckerReportClean(t *testing.T) {
	cs := NewConstraintSet("clean")
	cs.Add(SingleRoot())

	p := gorapide.Build().Event("X").MustDone()

	ch := NewChecker(cs, CheckAfter)
	ch.Run(context.Background(), p)
	ch.Stop()

	report := ch.Report()
	if !strings.Contains(report, "0 violation") {
		t.Errorf("clean report should indicate no violations: got %s", report)
	}
}

// --- Clean shutdown ---

func TestCheckerCleanShutdown(t *testing.T) {
	cs := NewConstraintSet("shutdown")
	cs.Add(EventCount("X", 0, 100))

	p := gorapide.NewPoset()

	ch := NewChecker(cs, CheckPeriodic).
		SetInterval(10 * time.Millisecond)

	ch.Run(context.Background(), p)

	// Let it tick a few times.
	time.Sleep(50 * time.Millisecond)

	ch.Stop()

	// Stop should be idempotent.
	ch.Stop()

	// Verify no goroutine leak by waiting on done channel.
	done := make(chan struct{})
	go func() {
		ch.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("checker did not shut down cleanly")
	}
}

func TestCheckerStopBeforeRun(t *testing.T) {
	cs := NewConstraintSet("no_run")
	ch := NewChecker(cs, CheckAfter)
	ch.Stop() // should not panic
}

// --- SetInterval and SetBatchSize ---

func TestSetIntervalReturnsSelf(t *testing.T) {
	cs := NewConstraintSet("test")
	ch := NewChecker(cs, CheckPeriodic)
	got := ch.SetInterval(time.Second)
	if got != ch {
		t.Error("SetInterval should return the same Checker")
	}
}

func TestSetBatchSizeReturnsSelf(t *testing.T) {
	cs := NewConstraintSet("test")
	ch := NewChecker(cs, CheckOnEvent)
	got := ch.SetBatchSize(5)
	if got != ch {
		t.Error("SetBatchSize should return the same Checker")
	}
}

// --- CheckOnEvent with context cancellation ---

func TestCheckOnEventContextCancel(t *testing.T) {
	cs := NewConstraintSet("cancel")
	cs.Add(EventCount("X", 0, 100))

	p := gorapide.NewPoset()
	ctx, cancel := context.WithCancel(context.Background())

	ch := NewChecker(cs, CheckOnEvent).SetBatchSize(1)
	ch.Run(ctx, p)

	cancel()

	done := make(chan struct{})
	go func() {
		ch.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("checker should stop when context is cancelled")
	}
}
