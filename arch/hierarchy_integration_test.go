package arch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/beautiful-majestic-dolphin/gorapide"
	"github.com/beautiful-majestic-dolphin/gorapide/pattern"
)

// TestHierarchyImportFlow: parent injects event, sub-architecture's inner
// component receives it via import rule.
func TestHierarchyImportFlow(t *testing.T) {
	// Inner architecture with a worker component.
	inner := NewArchitecture("inner")
	workerIface := Interface("Worker").InAction("Task").OutAction("Done").Build()
	worker := NewComponent("worker", workerIface, nil)
	inner.AddComponent(worker)

	var mu sync.Mutex
	var received []*gorapide.Event
	worker.OnEvent("Task", func(ctx BehaviorContext) {
		mu.Lock()
		received = append(received, ctx.Matched...)
		mu.Unlock()
	})

	// Wrap inner as sub-architecture.
	subIface := Interface("SubFace").InAction("Request").Build()
	sa := WrapArchitecture("sub1", inner).
		WithInterface(subIface).
		Import("Request", "worker", "Task").
		Build()

	// Parent architecture.
	parent := NewArchitecture("parent")
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	parent.Start(ctx)

	parent.Inject("Request", map[string]any{"job": "scan"})
	time.Sleep(200 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("inner worker should have received Task via import rule")
	}
}

// TestHierarchyExportFlow: inner component emits event, parent architecture
// sees it via export rule.
func TestHierarchyExportFlow(t *testing.T) {
	// Inner architecture with a worker that emits Done.
	inner := NewArchitecture("inner")
	workerIface := Interface("Worker").InAction("Task").OutAction("Done").Build()
	worker := NewComponent("worker", workerIface, nil)
	inner.AddComponent(worker)

	worker.OnEvent("Task", func(ctx BehaviorContext) {
		ctx.Emit("Done", map[string]any{"status": "ok"})
	})

	// Sub-architecture exports Done as Response.
	subIface := Interface("SubFace").InAction("Request").OutAction("Response").Build()
	sa := WrapArchitecture("sub1", inner).
		WithInterface(subIface).
		Import("Request", "worker", "Task").
		Export("worker", "Done", "Response").
		Build()

	// Parent with an observer to catch exported events.
	var mu sync.Mutex
	var observed []*gorapide.Event
	parent := NewArchitecture("parent", WithObserver(func(e *gorapide.Event) {
		mu.Lock()
		observed = append(observed, e)
		mu.Unlock()
	}))
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	parent.Start(ctx)

	parent.Inject("Request", map[string]any{"job": "scan"})
	time.Sleep(300 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Look for the exported Response event.
	found := false
	for _, e := range observed {
		if e.Name == "Response" && e.Source == "sub1" {
			found = true
			if e.ParamString("status") != "ok" {
				t.Errorf("Response status: want ok, got %s", e.ParamString("status"))
			}
		}
	}
	if !found {
		t.Error("parent should observe exported Response event from sub-architecture")
	}
}

// TestHierarchyExportWithTransform: export rule transforms params.
func TestHierarchyExportWithTransform(t *testing.T) {
	inner := NewArchitecture("inner")
	workerIface := Interface("Worker").OutAction("Raw").Build()
	worker := NewComponent("worker", workerIface, nil)
	inner.AddComponent(worker)

	subIface := Interface("SubFace").OutAction("Processed").Build()
	sa := WrapArchitecture("sub1", inner).
		WithInterface(subIface).
		ExportWith("worker", "Raw", "Processed", func(e *gorapide.Event) map[string]any {
			return map[string]any{"original": e.Name, "transformed": true}
		}).
		Build()

	var mu sync.Mutex
	var observed []*gorapide.Event
	parent := NewArchitecture("parent", WithObserver(func(e *gorapide.Event) {
		mu.Lock()
		observed = append(observed, e)
		mu.Unlock()
	}))
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	parent.Start(ctx)

	worker.Emit("Raw", map[string]any{"data": "test"})
	time.Sleep(200 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	mu.Lock()
	defer mu.Unlock()

	found := false
	for _, e := range observed {
		if e.Name == "Processed" {
			found = true
			v, ok := e.Param("transformed")
			if !ok || v != true {
				t.Error("transformed param should be true")
			}
		}
	}
	if !found {
		t.Error("parent should see Processed event with transformed params")
	}
}

// TestHierarchyExportTriggersParentConnection: exported event triggers
// a static connection in the parent architecture.
func TestHierarchyExportTriggersParentConnection(t *testing.T) {
	// Inner: worker emits Done.
	inner := NewArchitecture("inner")
	workerIface := Interface("Worker").InAction("Task").OutAction("Done").Build()
	worker := NewComponent("worker", workerIface, nil)
	inner.AddComponent(worker)
	worker.OnEvent("Task", func(ctx BehaviorContext) {
		ctx.Emit("Done", nil)
	})

	// Sub-arch exports Done as Result.
	subIface := Interface("Sub").InAction("Request").OutAction("Result").Build()
	sa := WrapArchitecture("sub1", inner).
		WithInterface(subIface).
		Import("Request", "worker", "Task").
		Export("worker", "Done", "Result").
		Build()

	// Parent: has a consumer that receives via static connection from sub1.
	consumerIface := Interface("Consumer").InAction("Outcome").Build()
	consumer := NewComponent("consumer", consumerIface, nil)

	var mu sync.Mutex
	var consumerGot []*gorapide.Event
	consumer.OnEvent("Outcome", func(ctx BehaviorContext) {
		mu.Lock()
		consumerGot = append(consumerGot, ctx.Matched...)
		mu.Unlock()
	})

	parent := NewArchitecture("parent")
	parent.AddSubArchitecture(sa)
	parent.AddComponent(consumer)
	parent.AddConnection(
		Connect("sub1", "consumer").
			On(pattern.MatchEvent("Result")).
			Pipe().
			Send("Outcome").
			Build(),
	)

	ctx := context.Background()
	parent.Start(ctx)

	parent.Inject("Request", nil)
	time.Sleep(300 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(consumerGot) == 0 {
		t.Fatal("consumer should receive Outcome via parent connection triggered by sub-arch export")
	}
}

// TestHierarchySeparatePosets: inner and parent have separate posets.
func TestHierarchySeparatePosets(t *testing.T) {
	inner := NewArchitecture("inner")
	workerIface := Interface("Worker").InAction("Task").OutAction("Done").Build()
	worker := NewComponent("worker", workerIface, nil)
	inner.AddComponent(worker)
	worker.OnEvent("Task", func(ctx BehaviorContext) {
		ctx.Emit("Done", nil)
	})

	subIface := Interface("Sub").InAction("Request").OutAction("Result").Build()
	sa := WrapArchitecture("sub1", inner).
		WithInterface(subIface).
		Import("Request", "worker", "Task").
		Export("worker", "Done", "Result").
		Build()

	parent := NewArchitecture("parent")
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	parent.Start(ctx)

	parent.Inject("Request", nil)
	time.Sleep(300 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	// Inner poset should have Task and Done events.
	innerEvents := inner.Poset().Events()
	innerNames := make(map[string]bool)
	for _, e := range innerEvents {
		innerNames[e.Name] = true
	}

	// Parent poset should have Request and Result, NOT Task or Done.
	parentEvents := parent.Poset().Events()
	parentNames := make(map[string]bool)
	for _, e := range parentEvents {
		parentNames[e.Name] = true
	}

	if !parentNames["Request"] {
		t.Error("parent poset should contain Request")
	}
	if !parentNames["Result"] {
		t.Error("parent poset should contain Result (exported)")
	}
	if parentNames["Task"] {
		t.Error("parent poset should NOT contain inner Task event")
	}
	if parentNames["Done"] {
		t.Error("parent poset should NOT contain inner Done event")
	}
}

// TestHierarchyWildcardExport: export rule with "*" matches any inner source.
func TestHierarchyWildcardExport(t *testing.T) {
	inner := NewArchitecture("inner")
	w1 := NewComponent("w1", Interface("W").OutAction("Ping").Build(), nil)
	w2 := NewComponent("w2", Interface("W").OutAction("Ping").Build(), nil)
	inner.AddComponent(w1)
	inner.AddComponent(w2)

	subIface := Interface("Sub").OutAction("Ping").Build()
	sa := WrapArchitecture("sub", inner).
		WithInterface(subIface).
		Export("*", "Ping", "Ping"). // wildcard source
		Build()

	var mu sync.Mutex
	var count int
	parent := NewArchitecture("parent", WithObserver(func(e *gorapide.Event) {
		if e.Name == "Ping" && e.Source == "sub" {
			mu.Lock()
			count++
			mu.Unlock()
		}
	}))
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	parent.Start(ctx)

	w1.Emit("Ping", nil)
	w2.Emit("Ping", nil)
	time.Sleep(200 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	mu.Lock()
	defer mu.Unlock()
	if count != 2 {
		t.Errorf("wildcard export should capture both Ping events, got %d", count)
	}
}
