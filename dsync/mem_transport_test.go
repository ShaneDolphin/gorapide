package dsync

import (
	"context"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

func TestMemTransportSendReceive(t *testing.T) {
	net := NewMemNetwork()
	t1 := net.Transport("node1")
	t2 := net.Transport("node2")

	snap := &gorapide.Snapshot{
		NodeID:    "node1",
		Events:    []gorapide.EventExport{{ID: "evt-1", Name: "ping"}},
		HighWater: 1,
	}

	ctx := context.Background()
	if err := t1.Send(ctx, "node2", snap); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	select {
	case got := <-t2.Receive():
		if got.NodeID != "node1" {
			t.Errorf("expected NodeID node1, got %s", got.NodeID)
		}
		if len(got.Events) != 1 || got.Events[0].ID != "evt-1" {
			t.Errorf("unexpected events: %+v", got.Events)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot")
	}
}

func TestMemTransportMultiplePeers(t *testing.T) {
	net := NewMemNetwork()
	t1 := net.Transport("node1")
	t2 := net.Transport("node2")
	t3 := net.Transport("node3")

	snap := &gorapide.Snapshot{
		NodeID:    "node1",
		Events:    []gorapide.EventExport{{ID: "evt-a", Name: "hello"}},
		HighWater: 1,
	}

	ctx := context.Background()
	if err := t1.Send(ctx, "node2", snap); err != nil {
		t.Fatalf("Send to node2 failed: %v", err)
	}
	if err := t1.Send(ctx, "node3", snap); err != nil {
		t.Fatalf("Send to node3 failed: %v", err)
	}

	for _, pair := range []struct {
		name string
		tr   *MemTransport
	}{
		{"node2", t2},
		{"node3", t3},
	} {
		select {
		case got := <-pair.tr.Receive():
			if got.NodeID != "node1" {
				t.Errorf("%s: expected NodeID node1, got %s", pair.name, got.NodeID)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: timed out waiting for snapshot", pair.name)
		}
	}
}

func TestMemTransportClose(t *testing.T) {
	net := NewMemNetwork()
	tr := net.Transport("node1")
	tr.Close()

	// After Close, the Receive channel should be closed.
	select {
	case _, ok := <-tr.Receive():
		if ok {
			t.Error("expected channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out: channel not closed")
	}
}

func TestMemTransportSendToUnknown(t *testing.T) {
	net := NewMemNetwork()
	t1 := net.Transport("node1")

	snap := &gorapide.Snapshot{NodeID: "node1"}
	ctx := context.Background()
	err := t1.Send(ctx, "nonexistent", snap)
	if err == nil {
		t.Fatal("expected error when sending to unknown peer")
	}
}
