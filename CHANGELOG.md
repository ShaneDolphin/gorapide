# Changelog

## v0.2.0 — 2026-08-16

Deterministic Rapide engine (checkpoint RSD-0332). The legacy
asynchronous runtime remains available but is deprecated and excluded
from the deterministic guarantee.

### Added
- `rapide` package: parser, type checker, and compiler for a Stanford
  Rapide 1.0 subset, lowering to the existing `arch`/`pattern`/
  `constraint` types. Unsupported forms fail explicitly; nothing is
  silently approximated. Coverage: `docs/RAPIDE_FEATURE_MATRIX.md`.
- Sealed deterministic execution: `arch.Architecture.PrepareDeterministic`,
  `ExecuteDeterministic`, `ReplayDeterministic`, `ExploreDeterministic`,
  canonical model bytes and SHA-256 digests, versioned execution
  journals (v6) and artifacts (v70), engine v244. Walkthrough:
  `docs/DETERMINISTIC_EXECUTION.md`.
- Causal preorders: `AddCausalEquivalenceClass`, canonical poset
  formats v1–v4, equivalence-aware queries and merge.
- Rapide value semantics: triv, unbounded characters, exact-code
  strings, records, unions, sets, clocks, and exact-rational float
  arithmetic (single rounding; NaN/Inf rejected; -0 canonicalized).
- Deterministic constraint modes (`CheckDeterministic*`), `Disjoint`
  pattern combinator, pattern placeholder binding.
- Error surfacing on legacy paths: `StartChecked`, `WaitError`,
  `LegacyError`, structured dsync/merge errors.

### Changed — BREAKING for v0.1.0 callers
- **Basic connections** attach an observation (alias view) to the same
  event occurrence instead of minting an independent event per hop.
  Poset sizes and `EventsByName` counts change; an event is now visible
  under every observed name. This matches Rapide occurrence semantics.
- **Agent connections** mint a new event with a causal edge back to the
  trigger instead of forwarding the same event object. Code comparing
  event IDs across an agent connection must compare causes instead.
- **Poset queries return sorted defensive copies**, not live shared
  pointers. Cached `*Event` values no longer observe later Lamport
  updates (this also removes a data race). Re-query instead of caching.
- **Causality is a preorder**: ordering two causally-equivalent events
  returns `ErrSelfCausal`; new errors `ErrCauseMismatch`,
  `ErrCausalEquivalenceConflict`.
- **Fallible operations**: `MergeSnapshot`, `Poset.UnmarshalJSON`,
  `AddEvent`/`AddEventWithCause`, and dsync sends can now return errors
  where they previously could not. Check returned errors.
- **`Event.ParamInt`** coerces int8/16/32/64 (previously exact `int`
  only).
- `Event` and `PosetStats` gained exported fields; positional struct
  literals must become keyed literals.

### Deprecated
- `NewEvent`, `NewEventID` (random identity), callback behaviors
  (`OnReceive`/`OnEvent`), `Architecture.Start` async runtime,
  dynamic `Binding`, `SubArchitecture`, `dsync.Coordinator`. All still
  work; none are covered by the deterministic guarantee, and all are
  rejected by `PrepareDeterministic`.

### Evidence
- Semantic decision ledger (332 entries): `docs/SEMANTIC_DECISIONS.md`
- Conformance index (1,369 rows): `docs/CONFORMANCE_TESTS.md`
- Qualification: Windows/amd64 (Go 1.26.2, CGO off) per checkpoint
  RSD-0332, plus darwin/arm64 verification including the race detector
  for this release (see `docs/RELEASE_v0.2.0_CHECKPOINT.md`).

## v0.1.0 — 2026-07-12

Initial release: causal event posets, pattern language, constraints,
architecture runtime, distributed sync, studio, OTel export.
