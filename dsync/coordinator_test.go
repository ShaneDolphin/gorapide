package dsync

import (
	"context"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

func TestCoordinatorTwoNodeSync(t *testing.T) {
	// Setup: two nodes, each with one event.
	poset1 := gorapide.NewPoset()
	poset2 := gorapide.NewPoset()

	evA := gorapide.NewEvent("A", "node1", nil)
	if err := poset1.AddEvent(evA); err != nil {
		t.Fatalf("AddEvent A: %v", err)
	}

	evB := gorapide.NewEvent("B", "node2", nil)
	if err := poset2.AddEvent(evB); err != nil {
		t.Fatalf("AddEvent B: %v", err)
	}

	net := NewMemNetwork()
	tr1 := net.Transport("node1")
	tr2 := net.Transport("node2")

	c1 := NewCoordinator("node1", poset1, tr1, WithInterval(50*time.Millisecond))
	c2 := NewCoordinator("node2", poset2, tr2, WithInterval(50*time.Millisecond))

	c1.AddPeer("node2")
	c2.AddPeer("node1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c1.Start(ctx)
	c2.Start(ctx)

	// Wait for convergence.
	time.Sleep(500 * time.Millisecond)

	c1.Stop()
	c2.Stop()
	c1.Wait()
	c2.Wait()

	if poset1.Len() != 2 {
		t.Errorf("poset1: expected 2 events, got %d", poset1.Len())
	}
	if poset2.Len() != 2 {
		t.Errorf("poset2: expected 2 events, got %d", poset2.Len())
	}
}

func TestCoordinatorThreeNodeConvergence(t *testing.T) {
	// Three nodes, each with one unique event. After sync, all have 3.
	posets := make([]*gorapide.Poset, 3)
	transports := make([]*MemTransport, 3)
	coords := make([]*Coordinator, 3)

	net := NewMemNetwork()
	nodeIDs := []gorapide.NodeID{"n1", "n2", "n3"}

	for i, nid := range nodeIDs {
		posets[i] = gorapide.NewPoset()
		transports[i] = net.Transport(nid)
	}

	// Each node gets one unique event.
	names := []string{"Alpha", "Beta", "Gamma"}
	for i, name := range names {
		ev := gorapide.NewEvent(name, string(nodeIDs[i]), nil)
		if err := posets[i].AddEvent(ev); err != nil {
			t.Fatalf("AddEvent %s: %v", name, err)
		}
	}

	// Create coordinators with all peers.
	for i, nid := range nodeIDs {
		coords[i] = NewCoordinator(nid, posets[i], transports[i], WithInterval(50*time.Millisecond))
		for j, pid := range nodeIDs {
			if i != j {
				coords[i].AddPeer(pid)
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, c := range coords {
		c.Start(ctx)
	}

	time.Sleep(500 * time.Millisecond)

	for _, c := range coords {
		c.Stop()
	}
	for _, c := range coords {
		c.Wait()
	}

	for i, p := range posets {
		if p.Len() != 3 {
			t.Errorf("poset[%d] (%s): expected 3 events, got %d", i, nodeIDs[i], p.Len())
		}
	}
}

func TestCoordinatorStopIdempotent(t *testing.T) {
	poset := gorapide.NewPoset()
	net := NewMemNetwork()
	tr := net.Transport("node1")

	c := NewCoordinator("node1", poset, tr, WithInterval(50*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c.Start(ctx)

	// Double stop should not panic.
	c.Stop()
	c.Stop()
	c.Wait()
}
