package dsync

import (
	"context"
	"fmt"
	"sync"

	"github.com/ShaneDolphin/gorapide"
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
		closing: make(chan struct{}),
	}
	t.sendCond = sync.NewCond(&t.mu)
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
	nodeID      gorapide.NodeID
	network     *MemNetwork
	inbox       chan *gorapide.Snapshot
	closing     chan struct{}
	closed      bool
	activeSends int
	sendCond    *sync.Cond
	mu          sync.Mutex
}

// Send delivers a snapshot to the target node's inbox channel.

func (t *MemTransport) Send(ctx context.Context, target gorapide.NodeID, snap *gorapide.Snapshot) error {
	if t == nil || t.network == nil {
		return fmt.Errorf("dsync: sender transport or network is nil")
	}
	if ctx == nil {
		return fmt.Errorf("dsync: send context is nil")
	}
	if snap == nil {
		return fmt.Errorf("dsync: snapshot is nil")
	}
	t.mu.Lock()
	senderClosed := t.closed
	t.mu.Unlock()
	if senderClosed {
		return fmt.Errorf("dsync: sender %s transport is closed", t.nodeID)
	}
	peer := t.network.lookup(target)
	if peer == nil {
		return fmt.Errorf("dsync: unknown peer %s", target)
	}
	owned, err := gorapide.CloneSnapshot(snap)
	if err != nil {
		return fmt.Errorf("dsync: clone snapshot for peer %s: %w", target, err)
	}

	peer.mu.Lock()
	if peer.closed {
		peer.mu.Unlock()
		return fmt.Errorf("dsync: peer %s transport is closed", target)
	}
	peer.activeSends++
	inbox := peer.inbox
	closing := peer.closing
	peer.mu.Unlock()
	defer func() {
		peer.mu.Lock()
		peer.activeSends--
		if peer.activeSends == 0 && peer.sendCond != nil {
			peer.sendCond.Broadcast()
		}
		peer.mu.Unlock()
	}()

	select {
	case inbox <- owned:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-closing:
		return fmt.Errorf("dsync: peer %s transport closed during send", target)
	}
}

// Receive returns the channel on which incoming snapshots are delivered.
func (t *MemTransport) Receive() <-chan *gorapide.Snapshot {
	return t.inbox
}

// Close closes the inbox channel. Subsequent sends to this transport will fail.
func (t *MemTransport) Close() error {
	if t == nil {
		return fmt.Errorf("dsync: transport is nil")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		close(t.closing)
		for t.activeSends != 0 {
			t.sendCond.Wait()
		}
		close(t.inbox)
	}
	return nil
}
