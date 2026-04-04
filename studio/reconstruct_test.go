package studio

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// TestReconstructBasic verifies that a two-component, one-pipe-connection schema
// is converted into a live Architecture with the correct name and component count.
func TestReconstructBasic(t *testing.T) {
	schema := &ArchitectureSchema{
		Name: "basic-arch",
		Components: []ComponentSchema{
			{
				ID: "producer",
				Interface: InterfaceSchema{
					Name: "ProducerInterface",
					Actions: []ActionSchema{
						{Name: "DataOut", Kind: "out"},
					},
				},
			},
			{
				ID: "consumer",
				Interface: InterfaceSchema{
					Name: "ConsumerInterface",
					Actions: []ActionSchema{
						{Name: "DataIn", Kind: "in"},
					},
				},
			},
		},
		Connections: []ConnectionSchema{
			{
				From:       "producer",
				To:         "consumer",
				Kind:       "pipe",
				Trigger:    "DataOut",
				ActionName: "DataIn",
			},
		},
	}

	a, err := Reconstruct(schema)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}

	if a.Name != "basic-arch" {
		t.Errorf("Name: want %q, got %q", "basic-arch", a.Name)
	}

	comps := a.Components()
	if len(comps) != 2 {
		t.Errorf("component count: want 2, got %d", len(comps))
	}

	// Verify both component IDs are present.
	ids := make(map[string]bool, len(comps))
	for _, c := range comps {
		ids[c.ID] = true
	}
	for _, want := range []string{"producer", "consumer"} {
		if !ids[want] {
			t.Errorf("component %q not found in architecture", want)
		}
	}
}

// TestReconstructEventPropagation starts the architecture, injects an event, and
// verifies the event (and causally linked pipe events) appear in the poset.
func TestReconstructEventPropagation(t *testing.T) {
	schema := &ArchitectureSchema{
		Name: "propagation-arch",
		Components: []ComponentSchema{
			{
				ID: "src",
				Interface: InterfaceSchema{
					Name:    "SrcInterface",
					Actions: []ActionSchema{{Name: "Start", Kind: "out"}},
				},
			},
			{
				ID: "dst",
				Interface: InterfaceSchema{
					Name:    "DstInterface",
					Actions: []ActionSchema{{Name: "Begin", Kind: "in"}},
				},
			},
		},
		Connections: []ConnectionSchema{
			{
				From:       "*",
				To:         "dst",
				Kind:       "pipe",
				Trigger:    "Start",
				ActionName: "Begin",
			},
		},
	}

	a, err := Reconstruct(schema)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		a.Stop()
		a.Wait()
	}()

	// Give the router a moment to be ready then inject the trigger.
	trigger := a.Inject("Start", map[string]any{"v": 1})

	// Poll until the pipe creates a Begin event (or timeout).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		begins := a.Poset().ByName("Begin")
		if len(begins) > 0 {
			// Verify causal link.
			if !a.Poset().IsCausallyBefore(trigger.ID, begins[0].ID) {
				t.Error("Start event should be causally before Begin event (pipe)")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out: Begin event never appeared in poset")
}

// TestReconstructConnectionKinds verifies that basic, pipe, and agent connection
// kinds are all accepted and produce a valid Architecture.
func TestReconstructConnectionKinds(t *testing.T) {
	components := []ComponentSchema{
		{ID: "a", Interface: InterfaceSchema{Name: "IA", Actions: []ActionSchema{{Name: "E", Kind: "out"}}}},
		{ID: "b", Interface: InterfaceSchema{Name: "IB", Actions: []ActionSchema{{Name: "E", Kind: "in"}}}},
		{ID: "c", Interface: InterfaceSchema{Name: "IC", Actions: []ActionSchema{{Name: "E", Kind: "in"}}}},
		{ID: "d", Interface: InterfaceSchema{Name: "ID", Actions: []ActionSchema{{Name: "E", Kind: "in"}}}},
	}

	for _, tc := range []struct {
		kind string
		from string
		to   string
	}{
		{"basic", "a", "b"},
		{"pipe", "a", "c"},
		{"agent", "a", "d"},
	} {
		tc := tc
		t.Run(tc.kind, func(t *testing.T) {
			schema := &ArchitectureSchema{
				Name:       "kind-test-" + tc.kind,
				Components: components,
				Connections: []ConnectionSchema{
					{
						From:       tc.from,
						To:         tc.to,
						Kind:       tc.kind,
						Trigger:    "E",
						ActionName: "E",
					},
				},
			}
			a, err := Reconstruct(schema)
			if err != nil {
				t.Fatalf("Reconstruct (kind=%s): %v", tc.kind, err)
			}
			if a == nil {
				t.Fatalf("Reconstruct (kind=%s) returned nil Architecture", tc.kind)
			}
		})
	}
}

// TestReconstructInvalid verifies that an empty (invalid) schema returns an error.
func TestReconstructInvalid(t *testing.T) {
	schema := &ArchitectureSchema{} // Name is empty — fails Validate
	_, err := Reconstruct(schema)
	if err == nil {
		t.Error("expected error for empty schema, got nil")
	}
}

// TestReconstructWithObserver verifies that the observer callback receives events
// that pass through the architecture.
func TestReconstructWithObserver(t *testing.T) {
	schema := &ArchitectureSchema{
		Name: "observer-arch",
		Components: []ComponentSchema{
			{
				ID: "emitter",
				Interface: InterfaceSchema{
					Name:    "EmitterInterface",
					Actions: []ActionSchema{{Name: "Tick", Kind: "out"}},
				},
			},
		},
	}

	var mu sync.Mutex
	var received []*gorapide.Event

	a, err := ReconstructWithObserver(schema, func(e *gorapide.Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("ReconstructWithObserver: %v", err)
	}

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	a.Inject("Tick", nil)
	a.Inject("Tick", nil)

	// Allow time for observer callbacks to fire.
	time.Sleep(100 * time.Millisecond)

	a.Stop()
	a.Wait()

	mu.Lock()
	count := len(received)
	mu.Unlock()

	if count < 2 {
		t.Errorf("observer: want at least 2 events, got %d", count)
	}
}
