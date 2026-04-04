package arch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// --- Constructor and basics ---

func TestNewArchitecture(t *testing.T) {
	arch := NewArchitecture("test")
	if arch.Name != "test" {
		t.Errorf("Name: want test, got %s", arch.Name)
	}
	if arch.Poset() == nil {
		t.Error("Poset should not be nil")
	}
}

func TestNewArchitectureWithPoset(t *testing.T) {
	p := gorapide.NewPoset()
	arch := NewArchitecture("test", WithPoset(p))
	if arch.Poset() != p {
		t.Error("WithPoset should use the provided poset")
	}
}

func TestArchitecturePosetAccessor(t *testing.T) {
	arch := NewArchitecture("test")
	p := arch.Poset()
	if p == nil {
		t.Fatal("Poset() should return non-nil")
	}
	// Add event directly and verify via accessor.
	e := gorapide.NewEvent("X", "test", nil)
	p.AddEvent(e)
	if arch.Poset().Len() != 1 {
		t.Errorf("Poset should have 1 event, got %d", arch.Poset().Len())
	}
}

// --- AddComponent ---

func TestAddComponent(t *testing.T) {
	arch := NewArchitecture("test")
	comp := NewComponent("A", Interface("I").Build(), nil)
	err := arch.AddComponent(comp)
	if err != nil {
		t.Fatalf("AddComponent: %v", err)
	}

	got, ok := arch.Component("A")
	if !ok {
		t.Fatal("Component A should be found")
	}
	if got != comp {
		t.Error("Component should return the same pointer")
	}
}

func TestAddComponentSetsPoset(t *testing.T) {
	arch := NewArchitecture("test")
	comp := NewComponent("A", Interface("I").Build(), nil)
	arch.AddComponent(comp)

	// Component should now be able to emit (poset was set by AddComponent).
	e, err := comp.Emit("X", nil)
	if err != nil {
		t.Fatalf("Emit after AddComponent: %v", err)
	}
	if arch.Poset().Len() != 1 {
		t.Errorf("event should be in architecture's poset, got len=%d", arch.Poset().Len())
	}
	if e.Source != "A" {
		t.Errorf("Source: want A, got %s", e.Source)
	}
}

func TestAddComponentDuplicateID(t *testing.T) {
	arch := NewArchitecture("test")
	arch.AddComponent(NewComponent("A", Interface("I").Build(), nil))
	err := arch.AddComponent(NewComponent("A", Interface("I2").Build(), nil))
	if err == nil {
		t.Error("AddComponent with duplicate ID should return error")
	}
}

func TestComponentsReturnsAll(t *testing.T) {
	arch := NewArchitecture("test")
	arch.AddComponent(NewComponent("A", Interface("IA").Build(), nil))
	arch.AddComponent(NewComponent("B", Interface("IB").Build(), nil))

	comps := arch.Components()
	if len(comps) != 2 {
		t.Fatalf("expected 2 components, got %d", len(comps))
	}
}

// --- AddConnection ---

func TestAddConnectionValidatesEndpoints(t *testing.T) {
	arch := NewArchitecture("test")
	arch.AddComponent(NewComponent("A", Interface("I").Build(), nil))

	// Valid: A exists, B doesn't.
	conn := Connect("A", "B").On(pattern.MatchEvent("X")).Send("Y").Build()
	err := arch.AddConnection(conn)
	if err == nil {
		t.Error("AddConnection with unknown target should return error")
	}

	// Valid: wildcard target.
	connWild := Connect("A", "*").On(pattern.MatchEvent("X")).Send("Y").Build()
	err = arch.AddConnection(connWild)
	if err != nil {
		t.Errorf("AddConnection with wildcard target should succeed: %v", err)
	}
}

// --- Producer-Consumer with pipe connection ---

func TestProducerConsumerPipe(t *testing.T) {
	arch := NewArchitecture("pc")

	prod := NewComponent("producer",
		Interface("Producer").OutAction("Data").Build(), nil)
	cons := NewComponent("consumer",
		Interface("Consumer").InAction("Data").Build(), nil, WithBufferSize(8))

	arch.AddComponent(prod)
	arch.AddComponent(cons)
	arch.AddConnection(Connect("producer", "consumer").
		On(pattern.MatchEvent("Data")).Pipe().Send("Data").Build())

	done := make(chan struct{})
	cons.OnEvent("Data", func(ctx BehaviorContext) {
		close(done)
	})

	arch.Start(context.Background())

	data, _ := prod.Emit("Data", map[string]any{"val": 42})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for consumer to receive Data")
	}

	arch.Stop()
	arch.Wait()

	// Verify two Data events: producer's original + consumer's pipe copy.
	allData := arch.Poset().ByName("Data")
	if len(allData) != 2 {
		t.Fatalf("expected 2 Data events, got %d", len(allData))
	}
	var consEvent *gorapide.Event
	for _, e := range allData {
		if e.Source == "consumer" {
			consEvent = e
		}
	}
	if consEvent == nil {
		t.Fatal("consumer should have a Data event")
	}
	if !arch.Poset().IsCausallyBefore(data.ID, consEvent.ID) {
		t.Error("producer Data should be causally before consumer Data")
	}
}

// --- Fan-out: one producer, two consumers ---

func TestFanOut(t *testing.T) {
	arch := NewArchitecture("fanout")

	prod := NewComponent("prod",
		Interface("P").OutAction("Update").Build(), nil)
	c1 := NewComponent("c1",
		Interface("C").InAction("Update").Build(), nil, WithBufferSize(8))
	c2 := NewComponent("c2",
		Interface("C").InAction("Update").Build(), nil, WithBufferSize(8))

	arch.AddComponent(prod)
	arch.AddComponent(c1)
	arch.AddComponent(c2)

	arch.AddConnection(Connect("prod", "c1").
		On(pattern.MatchEvent("Update")).Pipe().Send("Update").Build())
	arch.AddConnection(Connect("prod", "c2").
		On(pattern.MatchEvent("Update")).Pipe().Send("Update").Build())

	var wg sync.WaitGroup
	wg.Add(2)
	c1.OnEvent("Update", func(ctx BehaviorContext) { wg.Done() })
	c2.OnEvent("Update", func(ctx BehaviorContext) { wg.Done() })

	arch.Start(context.Background())

	update, _ := prod.Emit("Update", map[string]any{"v": 1})

	waitCh := make(chan struct{})
	go func() { wg.Wait(); close(waitCh) }()
	select {
	case <-waitCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for both consumers")
	}

	arch.Stop()
	arch.Wait()

	// Both consumers should have Update events causally after producer's.
	for _, cid := range []string{"c1", "c2"} {
		found := false
		for _, e := range arch.Poset().ByName("Update") {
			if e.Source == cid {
				found = true
				if !arch.Poset().IsCausallyBefore(update.ID, e.ID) {
					t.Errorf("%s Update should be causally after prod Update", cid)
				}
			}
		}
		if !found {
			t.Errorf("consumer %s should have an Update event", cid)
		}
	}
}

// --- Fan-in: two producers, one aggregator ---

func TestFanIn(t *testing.T) {
	arch := NewArchitecture("fanin")

	p1 := NewComponent("p1",
		Interface("P").OutAction("Report").Build(), nil)
	p2 := NewComponent("p2",
		Interface("P").OutAction("Report").Build(), nil)
	agg := NewComponent("agg",
		Interface("A").InAction("Report").Build(), nil, WithBufferSize(8))

	arch.AddComponent(p1)
	arch.AddComponent(p2)
	arch.AddComponent(agg)

	arch.AddConnection(Connect("p1", "agg").
		On(pattern.MatchEvent("Report")).Pipe().Send("Report").Build())
	arch.AddConnection(Connect("p2", "agg").
		On(pattern.MatchEvent("Report")).Pipe().Send("Report").Build())

	var count int
	var mu sync.Mutex
	done := make(chan struct{})
	agg.OnEvent("Report", func(ctx BehaviorContext) {
		mu.Lock()
		count++
		if count == 2 {
			close(done)
		}
		mu.Unlock()
	})

	arch.Start(context.Background())

	r1, _ := p1.Emit("Report", map[string]any{"src": "p1"})
	r2, _ := p2.Emit("Report", map[string]any{"src": "p2"})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for aggregator")
	}

	arch.Stop()
	arch.Wait()

	// Aggregator should have 2 Report events.
	aggReports := arch.Poset().ByName("Report").Filter(func(e *gorapide.Event) bool {
		return e.Source == "agg"
	})
	if len(aggReports) != 2 {
		t.Fatalf("expected 2 agg Reports, got %d", len(aggReports))
	}

	// Both should be causally linked from respective producers.
	if !arch.Poset().IsCausallyBefore(r1.ID, aggReports[0].ID) &&
		!arch.Poset().IsCausallyBefore(r1.ID, aggReports[1].ID) {
		t.Error("p1 Report should be causally before one of agg's Reports")
	}
	if !arch.Poset().IsCausallyBefore(r2.ID, aggReports[0].ID) &&
		!arch.Poset().IsCausallyBefore(r2.ID, aggReports[1].ID) {
		t.Error("p2 Report should be causally before one of agg's Reports")
	}
}

// --- Full pipeline: Scanner -> Aggregator -> Renderer ---

func TestFullPipeline(t *testing.T) {
	arch := NewArchitecture("pipeline")

	scanner := NewComponent("scanner",
		Interface("Scanner").OutAction("VulnFound").Build(), nil)
	aggregator := NewComponent("aggregator",
		Interface("Aggregator").InAction("VulnFound").OutAction("Finding").Build(),
		nil, WithBufferSize(8))
	renderer := NewComponent("renderer",
		Interface("Renderer").InAction("Finding").OutAction("DocSection").Build(),
		nil, WithBufferSize(8))

	arch.AddComponent(scanner)
	arch.AddComponent(aggregator)
	arch.AddComponent(renderer)

	arch.AddConnection(Connect("scanner", "aggregator").
		On(pattern.MatchEvent("VulnFound")).Pipe().Send("VulnFound").Build())
	arch.AddConnection(Connect("aggregator", "renderer").
		On(pattern.MatchEvent("Finding")).Pipe().Send("Finding").Build())

	// Aggregator behavior: VulnFound → Finding.
	aggregator.OnEvent("VulnFound", func(ctx BehaviorContext) {
		ctx.Emit("Finding", map[string]any{
			"severity": ctx.ParamFrom("VulnFound", "severity"),
		})
	})

	// Renderer behavior: Finding → DocSection.
	var docEvent *gorapide.Event
	var docMu sync.Mutex
	done := make(chan struct{})
	renderer.OnEvent("Finding", func(ctx BehaviorContext) {
		docMu.Lock()
		docEvent = ctx.Emit("DocSection", map[string]any{
			"title": "Vulnerability Report",
		})
		docMu.Unlock()
		close(done)
	})

	arch.Start(context.Background())

	vulnEvent, _ := scanner.Emit("VulnFound", map[string]any{
		"severity": "HIGH",
		"cve":      "CVE-2026-0001",
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pipeline to complete")
	}

	arch.Stop()
	arch.Wait()

	// Verify events exist.
	findings := arch.Poset().ByName("Finding")
	if len(findings) < 1 {
		t.Fatal("expected at least 1 Finding event")
	}

	docMu.Lock()
	localDoc := docEvent
	docMu.Unlock()
	if localDoc == nil {
		t.Fatal("DocSection should have been emitted")
	}

	// Verify full causal chain: VulnFound -> ... -> DocSection.
	if !arch.Poset().IsCausallyBefore(vulnEvent.ID, localDoc.ID) {
		t.Error("VulnFound should be causally before DocSection (transitive)")
	}

	// Verify CausalChain returns intermediate events.
	chain, err := arch.Poset().CausalChain(vulnEvent.ID, localDoc.ID)
	if err != nil {
		t.Fatalf("CausalChain: %v", err)
	}
	if len(chain) < 3 {
		t.Errorf("CausalChain should have at least 3 events (VulnFound, Finding, DocSection), got %d", len(chain))
	}

	// Verify Finding severity was propagated.
	var aggFinding *gorapide.Event
	for _, e := range findings {
		if e.Source == "aggregator" {
			aggFinding = e
			break
		}
	}
	if aggFinding == nil {
		t.Fatal("aggregator should have emitted a Finding")
	}
	if aggFinding.ParamString("severity") != "HIGH" {
		t.Errorf("severity: want HIGH, got %s", aggFinding.ParamString("severity"))
	}
}

// --- Inject triggers connections ---

func TestInjectTriggersConnection(t *testing.T) {
	arch := NewArchitecture("inject")

	recv := NewComponent("recv",
		Interface("Receiver").InAction("Start").Build(), nil, WithBufferSize(8))
	arch.AddComponent(recv)

	arch.AddConnection(Connect("*", "recv").
		On(pattern.MatchEvent("Trigger")).Pipe().Send("Start").Build())

	done := make(chan struct{})
	recv.OnEvent("Start", func(ctx BehaviorContext) {
		close(done)
	})

	arch.Start(context.Background())

	trigger := arch.Inject("Trigger", map[string]any{"reason": "init"})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for injected event to trigger connection")
	}

	arch.Stop()
	arch.Wait()

	// Verify Start event exists with causal link from trigger.
	starts := arch.Poset().ByName("Start")
	if len(starts) != 1 {
		t.Fatalf("expected 1 Start event, got %d", len(starts))
	}
	if !arch.Poset().IsCausallyBefore(trigger.ID, starts[0].ID) {
		t.Error("Trigger should be causally before Start (pipe)")
	}
}

// --- Clean shutdown ---

func TestArchitectureCleanShutdown(t *testing.T) {
	arch := NewArchitecture("shutdown")

	for _, id := range []string{"A", "B", "C"} {
		comp := NewComponent(id, Interface("I").InAction("X").Build(), nil, WithBufferSize(8))
		arch.AddComponent(comp)
	}

	arch.Start(context.Background())

	// Give it a moment to be running.
	time.Sleep(10 * time.Millisecond)

	arch.Stop()

	waitDone := make(chan struct{})
	go func() {
		arch.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		// Clean shutdown.
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after Stop — possible goroutine leak")
	}
}

// --- WithObserver ---

func TestWithObserver(t *testing.T) {
	var observed []*gorapide.Event
	var mu sync.Mutex

	arch := NewArchitecture("obs", WithObserver(func(e *gorapide.Event) {
		mu.Lock()
		observed = append(observed, e)
		mu.Unlock()
	}))

	prod := NewComponent("P",
		Interface("P").OutAction("X").Build(), nil)
	arch.AddComponent(prod)

	arch.Start(context.Background())

	prod.Emit("X", map[string]any{"n": 1})
	prod.Emit("X", map[string]any{"n": 2})

	time.Sleep(100 * time.Millisecond)
	arch.Stop()
	arch.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 2 {
		t.Errorf("observer should have seen 2 events, got %d", len(observed))
	}
}

// --- Start idempotent ---

func TestArchitectureStartIdempotent(t *testing.T) {
	arch := NewArchitecture("idem")
	arch.AddComponent(NewComponent("A", Interface("I").Build(), nil))

	ctx := context.Background()
	arch.Start(ctx)
	arch.Start(ctx) // second call should not panic
	arch.Stop()
	arch.Wait()
}
