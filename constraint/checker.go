package constraint

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// CheckMode determines when the runtime checker evaluates constraints.
type CheckMode int

const (
	CheckAfter    CheckMode = iota // Check constraints after architecture stops
	CheckPeriodic                  // Check at regular intervals during execution
	CheckOnEvent                   // Check after every N events
)

// Checker is a runtime constraint checker that evaluates a ConstraintSet
// against a poset according to a CheckMode schedule.
type Checker struct {
	constraints *ConstraintSet
	mode        CheckMode
	interval    time.Duration
	eventBatch  int

	violations  []ConstraintViolation
	onViolation func(ConstraintViolation)
	mu          sync.Mutex

	poset      *gorapide.Poset
	eventCount atomic.Int64
	notify     chan struct{} // signals batch threshold reached

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// NewChecker creates a runtime constraint checker.
func NewChecker(cs *ConstraintSet, mode CheckMode) *Checker {
	return &Checker{
		constraints: cs,
		mode:        mode,
		interval:    time.Second,
		eventBatch:  10,
		notify:      make(chan struct{}, 1),
	}
}

// OnViolation sets a callback invoked for each violation detected.
func (ch *Checker) OnViolation(fn func(ConstraintViolation)) *Checker {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.onViolation = fn
	return ch
}

// SetInterval sets the check interval for CheckPeriodic mode.
func (ch *Checker) SetInterval(d time.Duration) *Checker {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.interval = d
	return ch
}

// SetBatchSize sets the event batch size for CheckOnEvent mode.
func (ch *Checker) SetBatchSize(n int) *Checker {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.eventBatch = n
	return ch
}

// Run starts the checker in the background. For CheckAfter mode, the
// actual check happens in Stop(). For CheckPeriodic and CheckOnEvent,
// a goroutine is started that evaluates constraints on schedule.
func (ch *Checker) Run(ctx context.Context, poset *gorapide.Poset) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.poset = poset
	ch.done = make(chan struct{})
	ch.ctx, ch.cancel = context.WithCancel(ctx)

	switch ch.mode {
	case CheckAfter:
		// No background goroutine — check runs in Stop().
		// Mark done as open so Wait() works.
	case CheckPeriodic:
		go ch.runPeriodic()
	case CheckOnEvent:
		go ch.runOnEvent()
	}
}

// Stop stops the checker. For CheckAfter mode, this triggers the final check.
func (ch *Checker) Stop() {
	ch.mu.Lock()
	if ch.cancel == nil {
		ch.mu.Unlock()
		return
	}

	if ch.mode == CheckAfter {
		// Run the check synchronously before signaling done.
		poset := ch.poset
		ch.mu.Unlock()
		ch.runCheck(poset)
		ch.mu.Lock()
		ch.cancel()
		if ch.done != nil {
			select {
			case <-ch.done:
			default:
				close(ch.done)
			}
		}
		ch.mu.Unlock()
		return
	}

	ch.cancel()
	ch.mu.Unlock()
}

// Wait blocks until the checker's background goroutine has exited.
func (ch *Checker) Wait() {
	ch.mu.Lock()
	d := ch.done
	ch.mu.Unlock()
	if d != nil {
		<-d
	}
}

// NotifyEvent is called by the architecture when an event is processed.
// For CheckOnEvent mode, it increments the event counter and signals
// when the batch threshold is reached.
func (ch *Checker) NotifyEvent() {
	count := ch.eventCount.Add(1)
	ch.mu.Lock()
	batch := ch.eventBatch
	ch.mu.Unlock()
	if count%int64(batch) == 0 {
		select {
		case ch.notify <- struct{}{}:
		default:
		}
	}
}

// Violations returns all accumulated violations.
func (ch *Checker) Violations() []ConstraintViolation {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	result := make([]ConstraintViolation, len(ch.violations))
	copy(result, ch.violations)
	return result
}

// Report returns a formatted report of all violations.
func (ch *Checker) Report() string {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "Constraint check: %d violation(s)\n", len(ch.violations))
	for _, v := range ch.violations {
		fmt.Fprintf(&b, "  %s\n", v.String())
	}
	if len(ch.violations) == 0 {
		fmt.Fprintf(&b, "  All constraints pass.\n")
	}
	return b.String()
}

// runCheck evaluates all constraints and records violations.
func (ch *Checker) runCheck(poset *gorapide.Poset) {
	violations := ch.constraints.Check(poset)

	ch.mu.Lock()
	ch.violations = append(ch.violations, violations...)
	fn := ch.onViolation
	ch.mu.Unlock()

	if fn != nil {
		for _, v := range violations {
			fn(v)
		}
	}
}

func (ch *Checker) runPeriodic() {
	defer close(ch.done)
	ch.mu.Lock()
	interval := ch.interval
	poset := ch.poset
	ch.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ch.ctx.Done():
			return
		case <-ticker.C:
			ch.runCheck(poset)
		}
	}
}

func (ch *Checker) runOnEvent() {
	defer close(ch.done)
	ch.mu.Lock()
	poset := ch.poset
	ch.mu.Unlock()

	for {
		select {
		case <-ch.ctx.Done():
			return
		case <-ch.notify:
			ch.runCheck(poset)
		}
	}
}
