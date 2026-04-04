package arch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// TestArchitectureBind verifies that Bind registers a binding and Bindings()
// returns it.
func TestArchitectureBind(t *testing.T) {
	a := NewArchitecture("bind-test")

	prod := NewComponent("producer", Interface("P").OutAction("X").Build(), nil)
	cons := NewComponent("consumer", Interface("C").InAction("X").Build(), nil)
	a.AddComponent(prod)
	a.AddComponent(cons)

	if err := a.Bind("producer", "consumer"); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	bs := a.Bindings()
	if len(bs) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bs))
	}
	if bs[0].FromComp != "producer" || bs[0].ToComp != "consumer" {
		t.Errorf("binding endpoints: want producer->consumer, got %s->%s",
			bs[0].FromComp, bs[0].ToComp)
	}
}

// TestArchitectureBindUnknownComponent verifies that Bind returns an error
// when referencing unknown components.
func TestArchitectureBindUnknownComponent(t *testing.T) {
	a := NewArchitecture("bind-unknown")

	prod := NewComponent("producer", Interface("P").Build(), nil)
	a.AddComponent(prod)

	if err := a.Bind("producer", "ghost"); err == nil {
		t.Error("Bind with unknown target should return error")
	}
	if err := a.Bind("ghost", "producer"); err == nil {
		t.Error("Bind with unknown source should return error")
	}
}

// TestArchitectureUnbind verifies that Unbind removes bindings from a source.
func TestArchitectureUnbind(t *testing.T) {
	a := NewArchitecture("unbind-test")

	prod := NewComponent("producer", Interface("P").OutAction("X").Build(), nil)
	cons := NewComponent("consumer", Interface("C").InAction("X").Build(), nil)
	a.AddComponent(prod)
	a.AddComponent(cons)

	a.Bind("producer", "consumer")
	if len(a.Bindings()) != 1 {
		t.Fatal("expected 1 binding after Bind")
	}

	if err := a.Unbind("producer"); err != nil {
		t.Fatalf("Unbind: %v", err)
	}
	if len(a.Bindings()) != 0 {
		t.Errorf("expected 0 bindings after Unbind, got %d", len(a.Bindings()))
	}
}

// TestArchitectureBindWith verifies BindWith with a Map option.
func TestArchitectureBindWith(t *testing.T) {
	a := NewArchitecture("bindwith-test")

	prod := NewComponent("producer",
		Interface("P").OutAction("VulnFound").Build(), nil)
	cons := NewComponent("consumer",
		Interface("C").InAction("Finding").Build(), nil)
	a.AddComponent(prod)
	a.AddComponent(cons)

	m := NewMap("vuln-to-finding").
		Translate("VulnFound", "Finding").
		Build()

	id, err := a.BindWith("producer", "consumer", WithBindingMap(m))
	if err != nil {
		t.Fatalf("BindWith: %v", err)
	}
	if id == "" {
		t.Error("BindWith should return a non-empty ID")
	}

	bs := a.Bindings()
	if len(bs) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bs))
	}
	if bs[0].Map == nil {
		t.Error("binding should have a Map")
	}
}

// TestBindingRoutesEvents verifies that a dynamic binding routes events
// from producer to consumer in a live architecture.
func TestBindingRoutesEvents(t *testing.T) {
	a := NewArchitecture("binding-routes")

	prod := NewComponent("producer",
		Interface("P").OutAction("X").Build(), nil)
	cons := NewComponent("consumer",
		Interface("C").InAction("X").Build(), nil, WithBufferSize(8))
	a.AddComponent(prod)
	a.AddComponent(cons)

	a.Bind("producer", "consumer")

	done := make(chan struct{})
	cons.OnEvent("X", func(ctx BehaviorContext) {
		close(done)
	})

	a.Start(context.Background())

	prod.Emit("X", map[string]any{"val": 1})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for consumer to receive event via binding")
	}

	a.Stop()
	a.Wait()
}

// TestBindingWithMapTranslatesEvents verifies that a binding with a Map
// translates event names and parameters.
func TestBindingWithMapTranslatesEvents(t *testing.T) {
	a := NewArchitecture("binding-map")

	prod := NewComponent("producer",
		Interface("P").OutAction("VulnFound").Build(), nil)
	cons := NewComponent("consumer",
		Interface("C").InAction("Finding").Build(), nil, WithBufferSize(8))
	a.AddComponent(prod)
	a.AddComponent(cons)

	m := NewMap("vuln-to-finding").
		TranslateWith("VulnFound", "Finding", func(e *gorapide.Event) map[string]any {
			return map[string]any{
				"severity": e.ParamString("severity"),
				"source":   "scanner",
			}
		}).
		Build()

	a.BindWith("producer", "consumer", WithBindingMap(m))

	var received *gorapide.Event
	var mu sync.Mutex
	done := make(chan struct{})
	cons.OnEvent("Finding", func(ctx BehaviorContext) {
		mu.Lock()
		received = ctx.Matched[0]
		mu.Unlock()
		close(done)
	})

	a.Start(context.Background())

	prod.Emit("VulnFound", map[string]any{"severity": "HIGH", "cve": "CVE-2026-0001"})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for consumer to receive translated event")
	}

	a.Stop()
	a.Wait()

	mu.Lock()
	defer mu.Unlock()
	if received == nil {
		t.Fatal("consumer should have received a Finding event")
	}
	if received.Name != "Finding" {
		t.Errorf("event name: want Finding, got %s", received.Name)
	}
	if received.ParamString("severity") != "HIGH" {
		t.Errorf("severity: want HIGH, got %s", received.ParamString("severity"))
	}
	if received.ParamString("source") != "scanner" {
		t.Errorf("source: want scanner, got %s", received.ParamString("source"))
	}
}

// TestBindingCausalLink verifies that a PipeConnection binding creates a
// causal link (descendant) in the poset.
func TestBindingCausalLink(t *testing.T) {
	a := NewArchitecture("binding-causal")

	prod := NewComponent("producer",
		Interface("P").OutAction("X").Build(), nil)
	cons := NewComponent("consumer",
		Interface("C").InAction("X").Build(), nil, WithBufferSize(8))
	a.AddComponent(prod)
	a.AddComponent(cons)

	// Default binding kind is PipeConnection.
	a.Bind("producer", "consumer")

	done := make(chan struct{})
	cons.OnEvent("X", func(ctx BehaviorContext) {
		close(done)
	})

	a.Start(context.Background())

	original, _ := prod.Emit("X", map[string]any{"val": 42})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for consumer to receive event")
	}

	a.Stop()
	a.Wait()

	// Find the consumer's copy of the event.
	allX := a.Poset().ByName("X")
	var consEvent *gorapide.Event
	for _, e := range allX {
		if e.Source == "consumer" {
			consEvent = e
		}
	}
	if consEvent == nil {
		t.Fatal("consumer should have an X event in the poset")
	}
	if !a.Poset().IsCausallyBefore(original.ID, consEvent.ID) {
		t.Error("producer X should be causally before consumer X (pipe binding)")
	}
}

// TestBindingCoexistsWithStaticConnections verifies that both a static
// connection and a dynamic binding fire for the same producer event.
func TestBindingCoexistsWithStaticConnections(t *testing.T) {
	a := NewArchitecture("binding-coexist")

	prod := NewComponent("producer",
		Interface("P").OutAction("Data").Build(), nil)
	staticCons := NewComponent("static-cons",
		Interface("C").InAction("Data").Build(), nil, WithBufferSize(8))
	dynamicCons := NewComponent("dynamic-cons",
		Interface("C").InAction("Data").Build(), nil, WithBufferSize(8))
	a.AddComponent(prod)
	a.AddComponent(staticCons)
	a.AddComponent(dynamicCons)

	// Static connection: producer -> static-cons.
	a.AddConnection(Connect("producer", "static-cons").
		On(pattern.MatchEvent("Data")).Pipe().Send("Data").Build())

	// Dynamic binding: producer -> dynamic-cons.
	a.Bind("producer", "dynamic-cons")

	var mu sync.Mutex
	var staticReceived, dynamicReceived bool

	var wg sync.WaitGroup
	wg.Add(2)

	staticCons.OnEvent("Data", func(ctx BehaviorContext) {
		mu.Lock()
		staticReceived = true
		mu.Unlock()
		wg.Done()
	})
	dynamicCons.OnEvent("Data", func(ctx BehaviorContext) {
		mu.Lock()
		dynamicReceived = true
		mu.Unlock()
		wg.Done()
	})

	a.Start(context.Background())

	prod.Emit("Data", map[string]any{"val": 99})

	waitCh := make(chan struct{})
	go func() { wg.Wait(); close(waitCh) }()
	select {
	case <-waitCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for both consumers")
	}

	a.Stop()
	a.Wait()

	mu.Lock()
	defer mu.Unlock()
	if !staticReceived {
		t.Error("static consumer should have received the event via connection")
	}
	if !dynamicReceived {
		t.Error("dynamic consumer should have received the event via binding")
	}
}
