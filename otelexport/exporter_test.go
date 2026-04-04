package otelexport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

func TestLiveExporterOnEvent(t *testing.T) {
	var mu sync.Mutex
	var exported []spanData

	poset := gorapide.NewPoset()
	exp := newTestExporter(poset, func(ctx context.Context, batch []spanData) {
		mu.Lock()
		exported = append(exported, batch...)
		mu.Unlock()
	})
	defer exp.Shutdown(context.Background())

	e := gorapide.NewEvent("ScanStart", "scanner", map[string]any{"target": "host1"})
	poset.AddEvent(e)
	exp.OnEvent(e)

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(exported) != 1 {
		t.Fatalf("want 1 exported span, got %d", len(exported))
	}
	if exported[0].name != "ScanStart" {
		t.Errorf("name: want ScanStart, got %s", exported[0].name)
	}
	if exported[0].source != "scanner" {
		t.Errorf("source: want scanner, got %s", exported[0].source)
	}
}

func TestLiveExporterDeduplicates(t *testing.T) {
	var mu sync.Mutex
	var exported []spanData

	poset := gorapide.NewPoset()
	exp := newTestExporter(poset, func(ctx context.Context, batch []spanData) {
		mu.Lock()
		exported = append(exported, batch...)
		mu.Unlock()
	})
	defer exp.Shutdown(context.Background())

	e := gorapide.NewEvent("X", "src", nil)
	poset.AddEvent(e)

	exp.OnEvent(e)
	exp.OnEvent(e) // duplicate
	exp.OnEvent(e) // duplicate

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(exported) != 1 {
		t.Errorf("want 1 deduplicated span, got %d", len(exported))
	}
}

func TestLiveExporterCausalParent(t *testing.T) {
	var mu sync.Mutex
	var exported []spanData

	poset := gorapide.NewPoset()
	exp := newTestExporter(poset, func(ctx context.Context, batch []spanData) {
		mu.Lock()
		exported = append(exported, batch...)
		mu.Unlock()
	})
	defer exp.Shutdown(context.Background())

	parent := gorapide.NewEvent("A", "src", nil)
	poset.AddEvent(parent)

	child := gorapide.NewEvent("B", "src", nil)
	poset.AddEventWithCause(child, parent.ID)

	exp.OnEvent(parent)
	exp.OnEvent(child)

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(exported) != 2 {
		t.Fatalf("want 2 spans, got %d", len(exported))
	}

	var childSpan spanData
	for _, s := range exported {
		if s.name == "B" {
			childSpan = s
			break
		}
	}

	parentSpanID := spanIDFromEventID(parent.ID)
	if childSpan.parentID != parentSpanID {
		t.Errorf("child parentID should reference parent span")
	}
}

func TestLiveExporterCount(t *testing.T) {
	poset := gorapide.NewPoset()
	exp := newTestExporter(poset, func(ctx context.Context, batch []spanData) {})
	defer exp.Shutdown(context.Background())

	for i := 0; i < 5; i++ {
		e := gorapide.NewEvent("X", "src", nil)
		poset.AddEvent(e)
		exp.OnEvent(e)
	}

	if exp.ExportedCount() != 5 {
		t.Errorf("ExportedCount: want 5, got %d", exp.ExportedCount())
	}
}

func TestLiveExporterSetPoset(t *testing.T) {
	exp := newTestExporter(nil, func(ctx context.Context, batch []spanData) {})
	defer exp.Shutdown(context.Background())

	poset := gorapide.NewPoset()
	exp.SetPoset(poset)

	e := gorapide.NewEvent("X", "src", nil)
	poset.AddEvent(e)
	exp.OnEvent(e)

	if exp.ExportedCount() != 1 {
		t.Errorf("ExportedCount: want 1, got %d", exp.ExportedCount())
	}
}
