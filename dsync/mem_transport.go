package dsync

import (
	"context"
	"fmt"
	"sync"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// MemNetwork is an in-memory network connecting MemTransport instances.
// It is used for testing distributed sync without real networking.
type MemNetwork struct {
	mu         sync.Mutex
	transports map[gorapide.NodeID]*MemTransport
}

// NewMemNetwork creates a new in-memory network.
func NewMemNetwork() *MemNetwork {
	return &MemNetwork{
		transports: make(map[gorapide.NodeID]*MemTransport),
	}
}

// Transport creates or retrieves the MemTransport for the given node.
func (n *MemNetwork) Transport(nodeID gorapide.NodeID) *MemTransport {
	n.mu.Lock()
	defer n.mu.Unlock()

	if t, ok := n.transports[nodeID]; ok {
		return t
	}

	t := &MemTransport{
		nodeID:  nodeID,
		network: n,
		inbox:   make(chan *gorapide.Snapshot, 256),
	}
	n.transports[nodeID] = t
	return t
}

// lookup returns the transport for a given node, or nil if not found.
func (n *MemNetwork) lookup(nodeID gorapide.NodeID) *MemTransport {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.transports[nodeID]
}

// MemTransport implements Transport using in-memory channels.
type MemTransport struct {
	nodeID  gorapide.NodeID
	network *MemNetwork
	inbox   chan *gorapide.Snapshot
	closed  bool
	mu      sync.Mutex
}

// Send delivers a snapshot to the target node's inbox channel.
func (t *MemTransport) Send(ctx context.Context, target gorapide.NodeID, snap *gorapide.Snapshot) error {
	peer := t.network.lookup(target)
	if peer == nil {
		return fmt.Errorf("dsync: unknown peer %s", target)
	}

	peer.mu.Lock()
	if peer.closed {
		peer.mu.Unlock()
		return fmt.Errorf("dsync: peer %s transport is closed", target)
	}
	peer.mu.Unlock()

	select {
	case peer.inbox <- snap:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Receive returns the channel on which incoming snapshots are delivered.
func (t *MemTransport) Receive() <-chan *gorapide.Snapshot {
	return t.inbox
}

// Close closes the inbox channel. Subsequent sends to this transport will fail.
func (t *MemTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		close(t.inbox)
	}
	return nil
}
