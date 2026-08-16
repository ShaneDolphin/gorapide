// Package constraint implements Rapide constraints and their canonical audit
// artifacts.
//
// ConstraintSet deterministic validation, digesting, and synchronous
// EvaluateCanonical methods are part of the deterministic trusted surface.
// Checker is a legacy live-scheduling adapter: its periodic mode uses wall
// time, its event mode uses a coalescing channel, and its callbacks are not
// canonical model content. Deterministic architecture execution evaluates
// closed constraints synchronously instead.
package constraint
