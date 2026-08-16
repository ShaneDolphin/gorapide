// Package gorapide provides the causal event, value, and causal-preorder
// substrate for GoRapide. Distinct causally equivalent event occurrences form
// the nodes of a quotient partial order without losing occurrence identity.
//
// The deterministic trusted surface consists of NewDeterministicEvent,
// canonical value/type operations, canonical poset parsing and marshaling, and
// read-only queries over deterministic event snapshots. Replayable artifacts
// must use the versioned canonical formats.
//
// NewEvent, NewEventID, ordinary JSON export, snapshots, and merge helpers
// belong to the legacy or integration surface. They retain historical
// compatibility, while checked snapshot merge rejects malformed or conflicting
// content with stable partial-result diagnostics instead of ambient repair.
// These facilities must not be used to establish a deterministic model
// identity or replay result.
package gorapide
