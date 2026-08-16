package dsync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

type scriptedTransport struct {
	receive chan *gorapide.Snapshot
	send    func(context.Context, gorapide.NodeID, *gorapide.Snapshot) error
}

func newScriptedTransport() *scriptedTransport {
	return &scriptedTransport{receive: make(chan *gorapide.Snapshot)}
}

func (transport *scriptedTransport) Send(ctx context.Context, target gorapide.NodeID, snap *gorapide.Snapshot) error {
	if transport.send != nil {
		return transport.send(ctx, target, snap)
	}
	return nil
}

func (transport *scriptedTransport) Receive() <-chan *gorapide.Snapshot {
	return transport.receive
}

func (transport *scriptedTransport) Close() error {
	return nil
}

func TestCoordinatorPushRetainsStableDeduplicatedPeerErrors(t *testing.T) {
	transport := newScriptedTransport()
	transport.send = func(_ context.Context, target gorapide.NodeID, _ *gorapide.Snapshot) error {
		return fmt.Errorf("send to %s failed", target)
	}
	coordinator := NewCoordinator("local", gorapide.NewPoset(), transport)
	coordinator.AddPeer("z-peer")
	coordinator.AddPeer("a-peer")

	err := coordinator.pushOnce(context.Background())
	if !errors.Is(err, ErrCoordinatorFailure) {
		t.Fatalf("pushOnce error = %v, want ErrCoordinatorFailure", err)
	}
	issues := coordinator.Issues()
	if len(issues) != 2 {
		t.Fatalf("issues = %d, want one per failing peer", len(issues))
	}
	if issues[0].Peer != "a-peer" || issues[1].Peer != "z-peer" {
		t.Fatalf("issue order = [%s %s], want [a-peer z-peer]", issues[0].Peer, issues[1].Peer)
	}
	first := coordinator.LegacyError().Error()
	if err := coordinator.pushOnce(context.Background()); !errors.Is(err, ErrCoordinatorFailure) {
		t.Fatalf("second pushOnce error = %v, want ErrCoordinatorFailure", err)
	}
	if len(coordinator.Issues()) != 2 {
		t.Fatalf("repeated periodic failures were not deduplicated: %#v", coordinator.Issues())
	}
	if second := coordinator.LegacyError().Error(); second != first {
		t.Fatalf("retained diagnostic changed:\nfirst:  %s\nsecond: %s", first, second)
	}
}

func TestCoordinatorMergeRetainsSnapshotValidationError(t *testing.T) {
	coordinator := NewCoordinator("local", gorapide.NewPoset(), newScriptedTransport())
	snapshot := &gorapide.Snapshot{
		NodeID: "remote",
		Events: []gorapide.EventExport{{
			ID: "invalid", Name: "Invalid", Source: "remote", Lamport: 1, WallTime: "invalid",
		}},
		HighWater: 1,
	}

	err := coordinator.mergeReceivedSnapshot(snapshot)
	if !errors.Is(err, ErrCoordinatorFailure) || !errors.Is(err, gorapide.ErrInvalidSnapshotMerge) {
		t.Fatalf("mergeReceivedSnapshot error = %v, want coordinator and snapshot validation errors", err)
	}
	issues := coordinator.Issues()
	if len(issues) != 1 || issues[0].Operation != "merge" || issues[0].SnapshotNode != "remote" {
		t.Fatalf("retained merge issue = %#v", issues)
	}
	if !strings.Contains(coordinator.LegacyError().Error(), `merge snapshot "remote"`) {
		t.Fatalf("retained error lacks snapshot source: %v", coordinator.LegacyError())
	}
}

func TestCoordinatorRetainsPendingEdgeDrainFailure(t *testing.T) {
	stamp := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	poset := gorapide.NewPoset()
	if err := poset.AddEvent(&gorapide.Event{ID: "A", Name: "A", Source: "local", Params: map[string]any{}, Clock: gorapide.ClockStamp{WallTime: stamp}}); err != nil {
		t.Fatalf("AddEvent A: %v", err)
	}
	if _, err := poset.MergeSnapshot(&gorapide.Snapshot{
		NodeID: "pending-source", CausalEdges: [][]string{{"A", "B"}}, HighWater: 1,
	}); err != nil {
		t.Fatalf("create pending edge: %v", err)
	}
	if err := poset.AddEvent(&gorapide.Event{ID: "B", Name: "B", Source: "local", Params: map[string]any{}, Clock: gorapide.ClockStamp{WallTime: stamp}}); err != nil {
		t.Fatalf("AddEvent B: %v", err)
	}
	if err := poset.AddCausal("B", "A"); err != nil {
		t.Fatalf("AddCausal B->A: %v", err)
	}

	coordinator := NewCoordinator("local", poset, newScriptedTransport())
	err := coordinator.mergeReceivedSnapshot(&gorapide.Snapshot{NodeID: "drain-source", HighWater: 2})
	if !errors.Is(err, ErrCoordinatorFailure) || !errors.Is(err, gorapide.ErrCyclicCausal) {
		t.Fatalf("mergeReceivedSnapshot error = %v, want retained cyclic drain failure", err)
	}
	issues := coordinator.Issues()
	if len(issues) != 1 || issues[0].Operation != "drain" {
		t.Fatalf("retained drain issue = %#v", issues)
	}
}

func TestCoordinatorStartWrapperRetainsInvalidConfiguration(t *testing.T) {
	coordinator := NewCoordinator("local", gorapide.NewPoset(), newScriptedTransport(), WithInterval(0))
	if err := coordinator.StartChecked(context.Background()); !errors.Is(err, ErrCoordinatorFailure) {
		t.Fatalf("StartChecked error = %v, want ErrCoordinatorFailure", err)
	}
	coordinator.Start(context.Background())
	coordinator.Wait()
	if err := coordinator.LegacyError(); !errors.Is(err, ErrCoordinatorFailure) {
		t.Fatalf("LegacyError = %v, want retained ErrCoordinatorFailure", err)
	}
}

func TestCoordinatorStopCancelsBlockedTransportSend(t *testing.T) {
	transport := newScriptedTransport()
	started := make(chan struct{})
	var once sync.Once
	transport.send = func(ctx context.Context, _ gorapide.NodeID, _ *gorapide.Snapshot) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return ctx.Err()
	}
	coordinator := NewCoordinator("local", gorapide.NewPoset(), transport, WithInterval(time.Millisecond))
	coordinator.AddPeer("peer")
	if err := coordinator.StartChecked(context.Background()); err != nil {
		t.Fatalf("StartChecked: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked transport send")
	}
	coordinator.Stop()
	done := make(chan struct{})
	go func() {
		coordinator.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel the blocked transport send")
	}
	if err := coordinator.LegacyError(); err != nil {
		t.Fatalf("normal stop recorded a scheduling-dependent cancellation failure: %v", err)
	}
}
