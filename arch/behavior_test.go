package arch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/beautiful-majestic-dolphin/gorapide"
	"github.com/beautiful-majestic-dolphin/gorapide/pattern"
)

// --- Simple OnEvent ---

func TestOnEventTriggersResponse(t *testing.T) {
	p := gorapide.NewPoset()
	ifaceA := Interface("Sender").OutAction("Request").Build()
	ifaceB := Interface("Responder").InAction("Request").OutAction("Response").Build()
	compA := NewComponent("A", ifaceA, p)
	compB := NewComponent("B", ifaceB, p, WithBufferSize(4))

	done := make(chan struct{})
	compB.OnEvent("Request", func(ctx BehaviorContext) {
		ctx.Emit("Response", map[string]any{"status": "ok"})
		close(done)
	})

	compB.Start(context.Background())

	req, _ := compA.Emit("Request", map[string]any{"data": "test"})
	compB.Send(req)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for behavior to fire")
	}

	compB.Stop()
	compB.Wait()

	// Verify Response exists in poset with causal link.
	resps := p.ByName("Response")
	if len(resps) != 1 {
		t.Fatalf("expected 1 Response, got %d", len(resps))
	}
	if !p.IsCausallyBefore(req.ID, resps[0].ID) {
		t.Error("Request should be causally before Response")
	}
	if resps[0].Source != "B" {
		t.Errorf("Source: want B, got %s", resps[0].Source)
	}
	if resps[0].ParamString("status") != "ok" {
		t.Errorf("status: want ok, got %s", resps[0].ParamString("status"))
	}
}

// --- OnPattern with Seq waits for both events ---

func TestOnPatternSeqWaitsForBoth(t *testing.T) {
	p := gorapide.NewPoset()
	iface := Interface("Worker").
		InAction("Init").InAction("DataReady").
		OutAction("Result").Build()
	comp := NewComponent("W", iface, p, WithBufferSize(4))

	done := make(chan struct{})
	comp.OnPattern("init_and_ready",
		pattern.Seq(pattern.MatchEvent("Init"), pattern.MatchEvent("DataReady")),
		func(ctx BehaviorContext) {
			ctx.Emit("Result", map[string]any{"computed": true})
			close(done)
		})

	comp.Start(context.Background())

	// Create events with causal relationship.
	init, _ := comp.Emit("Init", nil)
	ready, _ := comp.Emit("DataReady", nil, init.ID)

	// Send both to inbox.
	comp.Send(init)
	comp.Send(ready)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Seq pattern to fire")
	}

	comp.Stop()
	comp.Wait()

	results := p.ByName("Result")
	if len(results) != 1 {
		t.Fatalf("expected 1 Result, got %d", len(results))
	}
	// Result should be causally after both Init and DataReady.
	if !p.IsCausallyBefore(init.ID, results[0].ID) {
		t.Error("Init should be causally before Result")
	}
	if !p.IsCausallyBefore(ready.ID, results[0].ID) {
		t.Error("DataReady should be causally before Result")
	}
}

// --- OnPatternOnce fires exactly once ---

func TestOnPatternOnceFiresOnce(t *testing.T) {
	p := gorapide.NewPoset()
	iface := Interface("Counter").InAction("Tick").Build()
	comp := NewComponent("C", iface, p, WithBufferSize(10))

	var count int
	var mu sync.Mutex

	comp.OnPatternOnce("first_tick",
		pattern.MatchEvent("Tick"),
		func(ctx BehaviorContext) {
			mu.Lock()
			count++
			mu.Unlock()
		})

	comp.Start(context.Background())

	// Send 3 distinct Tick events.
	for i := 0; i < 3; i++ {
		tick, _ := comp.Emit("Tick", map[string]any{"i": i})
		comp.Send(tick)
	}

	time.Sleep(100 * time.Millisecond)
	comp.Stop()
	comp.Wait()

	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Errorf("OnPatternOnce should fire exactly once, fired %d times", count)
	}
}

// --- 3-component reactive chain ---

func TestThreeComponentReactiveChain(t *testing.T) {
	p := gorapide.NewPoset()
	ifaceA := Interface("Producer").OutAction("X").Build()
	ifaceB := Interface("Transformer").InAction("X").OutAction("Y").Build()
	ifaceC := Interface("Consumer").InAction("Y").OutAction("Z").Build()

	compA := NewComponent("A", ifaceA, p, WithBufferSize(4))
	compB := NewComponent("B", ifaceB, p, WithBufferSize(4))
	compC := NewComponent("C", ifaceC, p, WithBufferSize(4))

	doneC := make(chan struct{})

	compB.OnEvent("X", func(ctx BehaviorContext) {
		y := ctx.Emit("Y", map[string]any{"from": "B"})
		compC.Send(y)
	})

	compC.OnEvent("Y", func(ctx BehaviorContext) {
		ctx.Emit("Z", map[string]any{"from": "C"})
		close(doneC)
	})

	ctx := context.Background()
	compB.Start(ctx)
	compC.Start(ctx)

	x, _ := compA.Emit("X", map[string]any{"from": "A"})
	compB.Send(x)

	select {
	case <-doneC:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for chain to complete")
	}

	compB.Stop()
	compC.Stop()
	compB.Wait()
	compC.Wait()

	// Verify causal chain X -> Y -> Z.
	ys := p.ByName("Y")
	zs := p.ByName("Z")
	if len(ys) != 1 {
		t.Fatalf("expected 1 Y, got %d", len(ys))
	}
	if len(zs) != 1 {
		t.Fatalf("expected 1 Z, got %d", len(zs))
	}

	if !p.IsCausallyBefore(x.ID, ys[0].ID) {
		t.Error("X should be causally before Y")
	}
	if !p.IsCausallyBefore(ys[0].ID, zs[0].ID) {
		t.Error("Y should be causally before Z")
	}
	if !p.IsCausallyBefore(x.ID, zs[0].ID) {
		t.Error("X should be causally before Z (transitive)")
	}

	// Lamport ordering.
	if x.Clock.Lamport >= ys[0].Clock.Lamport {
		t.Errorf("Lamport: X(%d) should be < Y(%d)", x.Clock.Lamport, ys[0].Clock.Lamport)
	}
	if ys[0].Clock.Lamport >= zs[0].Clock.Lamport {
		t.Errorf("Lamport: Y(%d) should be < Z(%d)", ys[0].Clock.Lamport, zs[0].Clock.Lamport)
	}
}

// --- BehaviorContext.Emit links to ALL matched events ---

func TestBehaviorContextEmitLinksAllMatched(t *testing.T) {
	p := gorapide.NewPoset()
	iface := Interface("Joiner").
		InAction("A").InAction("B").
		OutAction("Joined").Build()
	comp := NewComponent("J", iface, p, WithBufferSize(4))

	done := make(chan struct{})
	comp.OnPattern("join_ab",
		pattern.Seq(pattern.MatchEvent("A"), pattern.MatchEvent("B")),
		func(ctx BehaviorContext) {
			ctx.Emit("Joined", nil)
			close(done)
		})

	comp.Start(context.Background())

	a, _ := comp.Emit("A", nil)
	b, _ := comp.Emit("B", nil, a.ID)
	comp.Send(a)
	comp.Send(b)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	comp.Stop()
	comp.Wait()

	joined := p.ByName("Joined")
	if len(joined) != 1 {
		t.Fatalf("expected 1 Joined, got %d", len(joined))
	}

	// Both A and B should be causal ancestors.
	if !p.IsCausallyBefore(a.ID, joined[0].ID) {
		t.Error("A should be causally before Joined")
	}
	if !p.IsCausallyBefore(b.ID, joined[0].ID) {
		t.Error("B should be causally before Joined")
	}
}

// --- BehaviorContext.EmitCausedBy with explicit causes ---

func TestBehaviorContextEmitCausedBy(t *testing.T) {
	p := gorapide.NewPoset()
	iface := Interface("Selective").
		InAction("X").InAction("Y").
		OutAction("Z").Build()
	comp := NewComponent("S", iface, p, WithBufferSize(4))

	done := make(chan struct{})
	comp.OnPattern("xy",
		pattern.Seq(pattern.MatchEvent("X"), pattern.MatchEvent("Y")),
		func(ctx BehaviorContext) {
			// Emit Z caused only by Y, not both.
			yID := ctx.Matched[1].ID
			ctx.EmitCausedBy("Z", nil, yID)
			close(done)
		})

	comp.Start(context.Background())

	x, _ := comp.Emit("X", nil)
	y, _ := comp.Emit("Y", nil, x.ID)
	comp.Send(x)
	comp.Send(y)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	comp.Stop()
	comp.Wait()

	zs := p.ByName("Z")
	if len(zs) != 1 {
		t.Fatalf("expected 1 Z, got %d", len(zs))
	}

	// Z should be caused by Y directly.
	if !p.IsCausallyBefore(y.ID, zs[0].ID) {
		t.Error("Y should be causally before Z")
	}
	// X -> Y -> Z (transitive) is fine, but X should NOT be a direct parent.
	preds := p.DirectPredecessors(zs[0].ID)
	for _, pred := range preds {
		if pred == x.ID {
			t.Error("X should NOT be a direct predecessor of Z")
		}
	}
}

// --- BehaviorContext.ParamFrom ---

func TestBehaviorContextParamFrom(t *testing.T) {
	p := gorapide.NewPoset()
	iface := Interface("Extractor").
		InAction("Data").
		OutAction("Extracted").Build()
	comp := NewComponent("E", iface, p, WithBufferSize(4))

	done := make(chan struct{})
	comp.OnEvent("Data", func(ctx BehaviorContext) {
		val := ctx.ParamFrom("Data", "key")
		ctx.Emit("Extracted", map[string]any{"found": val})
		close(done)
	})

	comp.Start(context.Background())

	data, _ := comp.Emit("Data", map[string]any{"key": "secret"})
	comp.Send(data)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	comp.Stop()
	comp.Wait()

	extracted := p.ByName("Extracted")
	if len(extracted) != 1 {
		t.Fatalf("expected 1 Extracted, got %d", len(extracted))
	}
	if extracted[0].ParamString("found") != "secret" {
		t.Errorf("found: want secret, got %s", extracted[0].ParamString("found"))
	}
}

func TestParamFromMissingReturnsNil(t *testing.T) {
	p := gorapide.NewPoset()
	iface := Interface("Test").InAction("Msg").Build()
	comp := NewComponent("T", iface, p, WithBufferSize(4))

	var result any
	done := make(chan struct{})
	comp.OnEvent("Msg", func(ctx BehaviorContext) {
		result = ctx.ParamFrom("Nonexistent", "key")
		close(done)
	})

	comp.Start(context.Background())

	msg, _ := comp.Emit("Msg", nil)
	comp.Send(msg)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	comp.Stop()
	comp.Wait()

	if result != nil {
		t.Errorf("ParamFrom for missing event should return nil, got %v", result)
	}
}

// --- No re-triggering for already-processed matches ---

func TestNoRetriggerAfterNewObservation(t *testing.T) {
	p := gorapide.NewPoset()
	iface := Interface("NR").
		InAction("A").InAction("B").InAction("C").Build()
	comp := NewComponent("NR", iface, p, WithBufferSize(10))

	var count int
	var mu sync.Mutex

	comp.OnPattern("seq_ab",
		pattern.Seq(pattern.MatchEvent("A"), pattern.MatchEvent("B")),
		func(ctx BehaviorContext) {
			mu.Lock()
			count++
			mu.Unlock()
		})

	comp.Start(context.Background())

	a, _ := comp.Emit("A", nil)
	b, _ := comp.Emit("B", nil, a.ID)
	c, _ := comp.Emit("C", nil, b.ID)

	comp.Send(a)
	comp.Send(b)
	comp.Send(c) // unrelated — should NOT re-trigger Seq(A,B)

	time.Sleep(100 * time.Millisecond)
	comp.Stop()
	comp.Wait()

	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Errorf("Seq(A,B) should fire exactly once, fired %d times", count)
	}
}

// --- OnEvent chaining ---

func TestOnEventReturnsComponentForChaining(t *testing.T) {
	p := gorapide.NewPoset()
	iface := Interface("Chain").InAction("A").InAction("B").Build()
	comp := NewComponent("C", iface, p)

	ret := comp.
		OnEvent("A", func(ctx BehaviorContext) {}).
		OnEvent("B", func(ctx BehaviorContext) {})

	if ret != comp {
		t.Error("OnEvent should return the same component for chaining")
	}
}

func TestOnPatternReturnsComponentForChaining(t *testing.T) {
	p := gorapide.NewPoset()
	iface := Interface("Chain").InAction("A").Build()
	comp := NewComponent("C", iface, p)

	ret := comp.OnPattern("r1", pattern.MatchEvent("A"), func(ctx BehaviorContext) {})
	if ret != comp {
		t.Error("OnPattern should return the same component for chaining")
	}
}

// --- BehaviorRule struct ---

func TestBehaviorRuleFields(t *testing.T) {
	rule := &BehaviorRule{
		Name:    "test_rule",
		Trigger: pattern.MatchEvent("Ping"),
		Action:  func(ctx BehaviorContext) {},
		Once:    true,
	}

	if rule.Name != "test_rule" {
		t.Errorf("Name: want test_rule, got %s", rule.Name)
	}
	if rule.Once != true {
		t.Error("Once should be true")
	}
}
