package dsync

import (
	"context"
	"sync"
	"time"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// CoordOption configures a Coordinator.
type CoordOption func(*Coordinator)

// WithInterval sets the push interval for the Coordinator.
func WithInterval(d time.Duration) CoordOption {
	return func(c *Coordinator) {
		c.interval = d
	}
}

// Coordinator manages periodic push/pull sync of a Poset via a Transport.
type Coordinator struct {
	nodeID    gorapide.NodeID
	poset     *gorapide.Poset
	transport Transport
	peers     []gorapide.NodeID
	interval  time.Duration

	mu       sync.Mutex
	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewCoordinator creates a new Coordinator for the given node.
// Default push interval is 5 seconds unless overridden with WithInterval.
func NewCoordinator(nodeID gorapide.NodeID, poset *gorapide.Poset, transport Transport, opts ...CoordOption) *Coordinator {
	c := &Coordinator{
		nodeID:    nodeID,
		poset:     poset,
		transport: transport,
		interval:  5 * time.Second,
		stopCh:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// AddPeer registers a peer node for snapshot synchronization.
func (c *Coordinator) AddPeer(id gorapide.NodeID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Avoid duplicates.
	for _, p := range c.peers {
		if p == id {
			return
		}
	}
	c.peers = append(c.peers, id)
}

// RemovePeer unregisters a peer node.
func (c *Coordinator) RemovePeer(id gorapide.NodeID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, p := range c.peers {
		if p == id {
			c.peers = append(c.peers[:i], c.peers[i+1:]...)
			return
		}
	}
}

// Start spawns the push and receive goroutines. The context controls the
// lifetime of the goroutines in addition to Stop().
func (c *Coordinator) Start(ctx context.Context) {
	c.wg.Add(2)
	go c.pushLoop(ctx)
	go c.receiveLoop(ctx)
}

// Stop signals the coordinator goroutines to shut down.
// It is safe to call multiple times.
func (c *Coordinator) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
}

// Wait blocks until both the push and receive goroutines have exited.
func (c *Coordinator) Wait() {
	c.wg.Wait()
}

// pushLoop periodically creates a snapshot and sends it to all peers.
func (c *Coordinator) pushLoop(ctx context.Context) {
	defer c.wg.Done()
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.pushOnce(ctx)
		}
	}
}

// pushOnce sends a snapshot to all current peers.
func (c *Coordinator) pushOnce(ctx context.Context) {
	snap := c.poset.CreateSnapshot(c.nodeID)

	c.mu.Lock()
	peers := make([]gorapide.NodeID, len(c.peers))
	copy(peers, c.peers)
	c.mu.Unlock()

	for _, peer := range peers {
		// Best effort: ignore send errors.
		_ = c.transport.Send(ctx, peer, snap)
	}
}

// receiveLoop reads incoming snapshots and merges them into the local poset.
func (c *Coordinator) receiveLoop(ctx context.Context) {
	defer c.wg.Done()
	ch := c.transport.Receive()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case snap, ok := <-ch:
			if !ok {
				return
			}
			_, _ = c.poset.MergeSnapshot(snap)
			_, _ = c.poset.DrainPendingEdges()
		}
	}
}
