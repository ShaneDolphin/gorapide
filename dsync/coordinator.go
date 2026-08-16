package dsync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ShaneDolphin/gorapide"
)

// CoordOption configures a Coordinator.
type CoordOption func(*Coordinator)

// ErrCoordinatorFailure identifies one or more explicit failures in the
// deprecated best-effort synchronization adapter.
var ErrCoordinatorFailure = errors.New("distributed synchronization coordinator failed")

// CoordinatorIssue is one stable transport, merge, or pending-edge failure.
type CoordinatorIssue struct {
	Operation    string
	Peer         gorapide.NodeID
	SnapshotNode gorapide.NodeID
	Cause        error
}

func (issue CoordinatorIssue) Error() string {
	location := issue.Operation
	if issue.Peer != "" {
		location += fmt.Sprintf(" peer %q", issue.Peer)
	}
	if issue.SnapshotNode != "" {
		location += fmt.Sprintf(" snapshot %q", issue.SnapshotNode)
	}
	return fmt.Sprintf("%s: %v", location, issue.Cause)
}

func (issue CoordinatorIssue) Unwrap() error {
	return issue.Cause
}

// CoordinatorError is a stable, deduplicated set of adapter failures.
type CoordinatorError struct {
	Issues []CoordinatorIssue
}

func (e *CoordinatorError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return ErrCoordinatorFailure.Error()
	}
	parts := make([]string, len(e.Issues))
	for index, issue := range e.Issues {
		parts[index] = issue.Error()
	}
	return ErrCoordinatorFailure.Error() + ": " + strings.Join(parts, "; ")
}

func (e *CoordinatorError) Unwrap() []error {
	result := []error{ErrCoordinatorFailure}
	if e == nil {
		return result
	}
	for _, issue := range e.Issues {
		if issue.Cause != nil {
			result = append(result, issue.Cause)
		}
	}
	return result
}

func sortedCoordinatorIssues(issues []CoordinatorIssue) []CoordinatorIssue {
	result := append([]CoordinatorIssue(nil), issues...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Operation != result[j].Operation {
			return result[i].Operation < result[j].Operation
		}
		if result[i].Peer != result[j].Peer {
			return result[i].Peer < result[j].Peer
		}
		if result[i].SnapshotNode != result[j].SnapshotNode {
			return result[i].SnapshotNode < result[j].SnapshotNode
		}
		return result[i].Cause.Error() < result[j].Cause.Error()
	})
	return result
}

// WithInterval sets the push interval for the Coordinator.
func WithInterval(d time.Duration) CoordOption {
	return func(c *Coordinator) {
		c.interval = d
	}
}

// Coordinator manages periodic push/pull sync of a Poset via a Transport.
//
// Deprecated: Coordinator is a best-effort integration adapter, not a
// deterministic replication or replay protocol.
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
	cancel   context.CancelFunc
	started  bool
	stopped  bool
	issues   map[string]CoordinatorIssue
}

// NewCoordinator creates a new Coordinator for the given node.
// Default push interval is 5 seconds unless overridden with WithInterval.
//
// Deprecated: deterministic distributed operation requires explicit canonical
// input ordering and strict validation rather than periodic best-effort sync.
func NewCoordinator(nodeID gorapide.NodeID, poset *gorapide.Poset, transport Transport, opts ...CoordOption) *Coordinator {
	c := &Coordinator{
		nodeID:    nodeID,
		poset:     poset,
		transport: transport,
		interval:  5 * time.Second,
		stopCh:    make(chan struct{}),
		issues:    make(map[string]CoordinatorIssue),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// AddPeer registers a peer node for snapshot synchronization.
func (c *Coordinator) AddPeer(id gorapide.NodeID) {
	if err := c.AddPeerChecked(id); err != nil {
		c.recordIssues(CoordinatorIssue{Operation: "add-peer", Peer: id, Cause: err})
	}
}

// AddPeerChecked registers a peer or returns a stable configuration error.
func (c *Coordinator) AddPeerChecked(id gorapide.NodeID) error {
	if c == nil {
		return fmt.Errorf("%w: coordinator is nil", ErrCoordinatorFailure)
	}
	if id == "" {
		return fmt.Errorf("%w: peer ID is empty", ErrCoordinatorFailure)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Avoid duplicates.
	for _, p := range c.peers {
		if p == id {
			return nil
		}
	}
	c.peers = append(c.peers, id)
	sort.Slice(c.peers, func(i, j int) bool { return c.peers[i] < c.peers[j] })
	return nil
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
	if err := c.StartChecked(ctx); err != nil {
		c.recordIssues(CoordinatorIssue{Operation: "start", Cause: err})
	}
}

// StartChecked validates and starts the deprecated synchronization adapter.
func (c *Coordinator) StartChecked(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("%w: coordinator is nil", ErrCoordinatorFailure)
	}
	if ctx == nil {
		return fmt.Errorf("%w: start context is nil", ErrCoordinatorFailure)
	}
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	if c.stopped {
		c.mu.Unlock()
		return fmt.Errorf("%w: coordinator %q was already stopped", ErrCoordinatorFailure, c.nodeID)
	}
	if c.nodeID == "" {
		c.mu.Unlock()
		return fmt.Errorf("%w: coordinator node ID is empty", ErrCoordinatorFailure)
	}
	if c.poset == nil {
		c.mu.Unlock()
		return fmt.Errorf("%w: coordinator %q poset is nil", ErrCoordinatorFailure, c.nodeID)
	}
	if c.transport == nil {
		c.mu.Unlock()
		return fmt.Errorf("%w: coordinator %q transport is nil", ErrCoordinatorFailure, c.nodeID)
	}
	if c.interval <= 0 {
		c.mu.Unlock()
		return fmt.Errorf("%w: coordinator %q interval %s is not positive", ErrCoordinatorFailure, c.nodeID, c.interval)
	}
	transport := c.transport
	nodeID := c.nodeID
	c.mu.Unlock()

	receive := transport.Receive()
	if receive == nil {
		return fmt.Errorf("%w: coordinator %q receive channel is nil", ErrCoordinatorFailure, nodeID)
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		cancel()
		return nil
	}
	if c.stopped {
		c.mu.Unlock()
		cancel()
		return fmt.Errorf("%w: coordinator %q was already stopped", ErrCoordinatorFailure, c.nodeID)
	}
	c.cancel = cancel
	c.started = true
	c.wg.Add(2)
	c.mu.Unlock()
	go c.pushLoop(runCtx)
	go c.receiveLoop(runCtx, receive)
	return nil
}

// Stop signals the coordinator goroutines to shut down.
// It is safe to call multiple times.
func (c *Coordinator) Stop() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.stopped = true
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
}

// Wait blocks until both the push and receive goroutines have exited.
func (c *Coordinator) Wait() {
	if c == nil {
		return
	}
	c.wg.Wait()
}

// WaitError waits for the adapter and returns its stable retained issue set.
func (c *Coordinator) WaitError() error {
	c.Wait()
	return c.LegacyError()
}

// Issues returns a stable copy of every distinct failure retained by the
// best-effort adapter.
func (c *Coordinator) Issues() []CoordinatorIssue {
	if c == nil {
		return []CoordinatorIssue{{Operation: "coordinator", Cause: fmt.Errorf("coordinator is nil")}}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]CoordinatorIssue, 0, len(c.issues))
	for _, issue := range c.issues {
		result = append(result, issue)
	}
	return sortedCoordinatorIssues(result)
}

// LegacyError returns a stable aggregate of every distinct retained failure.
func (c *Coordinator) LegacyError() error {
	issues := c.Issues()
	if len(issues) == 0 {
		return nil
	}
	return &CoordinatorError{Issues: issues}
}

func (c *Coordinator) recordIssues(issues ...CoordinatorIssue) error {
	if len(issues) == 0 {
		return nil
	}
	if c != nil {
		c.mu.Lock()
		if c.issues == nil {
			c.issues = make(map[string]CoordinatorIssue)
		}
		for _, issue := range issues {
			if issue.Cause != nil {
				c.issues[issue.Error()] = issue
			}
		}
		c.mu.Unlock()
	}
	filtered := make([]CoordinatorIssue, 0, len(issues))
	for _, issue := range issues {
		if issue.Cause != nil {
			filtered = append(filtered, issue)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return &CoordinatorError{Issues: sortedCoordinatorIssues(filtered)}
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
			if ctx.Err() != nil {
				return
			}
			_ = c.pushOnce(ctx)
		}
	}
}

// pushOnce sends a snapshot to all current peers.
func (c *Coordinator) pushOnce(ctx context.Context) error {
	if c == nil || c.poset == nil || c.transport == nil {
		return c.recordIssues(CoordinatorIssue{Operation: "push", Cause: fmt.Errorf("coordinator, poset, or transport is nil")})
	}
	if ctx == nil {
		return c.recordIssues(CoordinatorIssue{Operation: "push", Cause: fmt.Errorf("context is nil")})
	}
	snap := c.poset.CreateSnapshot(c.nodeID)

	c.mu.Lock()
	peers := make([]gorapide.NodeID, len(c.peers))
	copy(peers, c.peers)
	c.mu.Unlock()
	sort.Slice(peers, func(i, j int) bool { return peers[i] < peers[j] })

	var issues []CoordinatorIssue
	for _, peer := range peers {
		if err := c.transport.Send(ctx, peer, snap); err != nil {
			if ctx.Err() != nil {
				return c.recordIssues(issues...)
			}
			issues = append(issues, CoordinatorIssue{Operation: "push", Peer: peer, SnapshotNode: c.nodeID, Cause: err})
		}
	}
	return c.recordIssues(issues...)
}

// receiveLoop reads incoming snapshots and merges them into the local poset.
func (c *Coordinator) receiveLoop(ctx context.Context, ch <-chan *gorapide.Snapshot) {
	defer c.wg.Done()

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
			if ctx.Err() != nil {
				return
			}
			_ = c.mergeReceivedSnapshot(snap)
		}
	}
}

func (c *Coordinator) mergeReceivedSnapshot(snap *gorapide.Snapshot) error {
	if c == nil || c.poset == nil {
		return c.recordIssues(CoordinatorIssue{Operation: "merge", Cause: fmt.Errorf("coordinator or poset is nil")})
	}
	snapshotNode := gorapide.NodeID("")
	if snap != nil {
		snapshotNode = snap.NodeID
	}
	var issues []CoordinatorIssue
	if _, err := c.poset.MergeSnapshot(snap); err != nil {
		issues = append(issues, CoordinatorIssue{Operation: "merge", SnapshotNode: snapshotNode, Cause: err})
	}
	if snap != nil {
		_, drainErrors := c.poset.DrainPendingEdges()
		sort.Slice(drainErrors, func(i, j int) bool { return drainErrors[i].Error() < drainErrors[j].Error() })
		for _, err := range drainErrors {
			issues = append(issues, CoordinatorIssue{Operation: "drain", SnapshotNode: snapshotNode, Cause: err})
		}
	}
	return c.recordIssues(issues...)
}
