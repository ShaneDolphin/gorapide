// Package arch implements GoRapide architecture models and executable
// semantics.
//
// The deterministic trusted surface is Architecture.PrepareDeterministic and
// the immutable PreparedArchitecture model-byte, digest, execute, replay, and
// explore methods, plus EventPatternMap.PrepareDeterministic and the equivalent
// PreparedEventPatternMap methods. Architecture and EventPatternMap convenience
// methods prepare and delegate through the same boundary. These entry points
// admit only closed canonical declarations and use the journal-driven semantic
// kernel.
//
// Architecture.Start, Component.Start, inbox delivery, callback behavior,
// dynamic Binding, callback Map, and SubArchitecture are legacy asynchronous
// adapters. They use goroutines, channels, callbacks, or ambient event
// creation and are rejected by deterministic model validation. Their checked
// delivery/start operations and LegacyError/WaitError accessors make bounded
// queue and bridge failures visible, but do not make scheduling or arrival
// order semantic. Model builders and mutators are construction-only and must
// not be used concurrently with deterministic preparation. After preparation,
// source mutation is isolated from the deeply owned snapshot, which may be
// executed concurrently.
package arch
