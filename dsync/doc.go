// Package dsync contains best-effort distributed poset synchronization
// adapters.
//
// Coordinator timing and transport arrival order are integration behavior, not
// part of GoRapide's deterministic trusted core. Checked peer/start operations
// plus Issues, LegacyError, and WaitError expose retained transport, merge, and
// pending-edge failures; they do not make those arrival paths semantic. A
// snapshot received through this package must not be treated as replayable
// semantic input unless it has passed a strict canonical validation protocol.
package dsync
