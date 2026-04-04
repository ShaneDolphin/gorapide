package dsync

import (
	"context"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// Transport abstracts the network layer for poset synchronization.
type Transport interface {
	Send(ctx context.Context, target gorapide.NodeID, snap *gorapide.Snapshot) error
	Receive() <-chan *gorapide.Snapshot
	Close() error
}
