# Changelog

## v0.2.3 — 2026-08-18

Performance fix: `EventsByName` (and the general observation-view path it
shares with `ObservationViews`/`AddObservationWithTimings`) was rewritten in
v0.2.0 to test observation names by first cloning and sorting every
observation of every scanned event, matching or not, and then, for each
match, building the returned view through a general-purpose clone helper
that computed a full `Params`/`Observations` copy it immediately discarded
and recomputed. Both costs scale with poset size and are paid on every call,
so this is a hot-path regression for any workload (pattern matching, rule
evaluation, constraint checking) that queries a poset by event name
repeatedly as it grows.

### Fixed
- `eventView` no longer routes through `cloneEvent`, which unconditionally
  deep-copies `Params` and `Observations` before `eventView` overwrote both
  fields with different values a line later. It now builds the view
  directly, copying `Timings`, `expectedCauses`, and `Clock.Vector` (the
  parts of `cloneEvent` that survive unmodified into the final view) once,
  and copying `observation.Params` into `Params` and the event's stored
  observations into `Observations` exactly once each. `cloneEvent` itself,
  and every other caller of it, is unchanged.
- `EventsByName` no longer calls `EventObservations()` (a full
  deep-copy-and-sort of every event's observations) just to test a name.
  A new unexported `(*Event).observationsNamed` scans the event's stored
  observations directly, with the same fallback to the synthesized
  `{Name, Source, Params}` self-observation for events with no explicit
  `Observations`, and does no copying or sorting — only entries that
  actually match a name reach `eventView`, which builds the caller-owned
  deep clone. `EventsByName`'s final result is still passed through the
  same `sortEventSet`, so output ordering is unchanged.
- Observable behavior is unchanged: same `(event, observation)` pairs
  matched, same values, same deterministic sort order, same
  defensive-copy guarantee on returned events. Pinned by
  `TestEventsByNameContract`, written against and verified to pass
  unmodified on v0.2.2 before the fix, and re-verified after.

### Performance
`BenchmarkEventsByName` (5,000-event poset, 20 distinct names cycling,
querying a name with 250 matches, Apple M3, no `-race`):
- Before (v0.2.2, commit 95d623b): ~1.61ms–1.75ms per call,
  ~2.50MB/op, 23,261 allocs/op.
- After (this fix): ~248µs–293µs per call, ~248.5KB/op, 2,011 allocs/op.
- Roughly 6x faster per call, and about 10x fewer bytes allocated and
  11.6x fewer allocations, with identical query results.

## v0.2.2 — 2026-08-18

Performance fix: v0.2.0's timing-closure validation on the two causal-insert
hot paths (`AddEventWithCause`, `addCausalLocked`) rode helpers that scanned
the entire event store per call, turning insertion of a long causal chain
into O(n^2) work even when no event in the poset carried any `EventTiming`.
Long-lived posets are the library's primary use case, so this was a hot-path
regression, not a corner case.

### Fixed
- `AddEventWithCause` now skips predecessor collection and the conflict
  check entirely when the incoming event carries no timing (a conflict
  requires a clock both events share; an untimed event can share none).
- `addCausalLocked` now tracks a poset-wide count of stored occurrences
  carrying a Rapide interval and skips the O(closure) timing-closure scan
  outright while it is zero. An edge between two untimed events can still
  create a transitive violation between a timed ancestor and a timed
  descendant, so this is a poset-wide count, not a per-edge local check.
  The count is kept exact through one storage chokepoint used by every
  path that writes an occurrence into the store, including
  `AddObservationWithTimings` and JSON state replacement
  (`Poset.UnmarshalJSON`, which previously bypassed it).
- Behavior for any poset containing at least one timing is unchanged:
  validation still runs the full closure scan exactly as before.

### Performance
`BenchmarkLinearChainInsertUntimed` (1,000-event untimed linear chain via
`AddEventWithCause`, Apple M3, no `-race`):
- Before (v0.2.1, commit 502be95): ~17.8s–20.6s per chain build.
- After (this fix): ~46ms–49ms per chain build.
- Roughly 380–440x faster; the shape now scales close to linearly instead
  of quadratically in chain length.

## v0.2.1 — 2026-08-16

Distribution-only release: the engineering documentation corpus is no
longer part of the repository or the module distribution. It is
maintained privately by the authors. No code changes.

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
- **Constraint checking is stricter and value-aware.** `Must`/`MustMatch`
  clauses now require the pattern to match the whole associated
  computation rather than existentially (a poset with two `X` events now
  violates `Must(MatchEvent("X"))`); constraints containing opaque Go
  predicates (`Where(func...)`) are rejected with an `evaluation`
  violation instead of silently evaluated; `ConstraintKind` values were
  renumbered (`MustNotMatch` inserted; `MustNever` moved) — never
  persist or compare the numeric values. `ConstraintViolation` also
  gained mid-struct fields (`Bindings`, `StateWitnesses`) — use keyed
  literals.
- **`arch.Connection` must be built via `Connect(...).Build()`.** The
  struct gained exported fields, an internal mutex (no longer copyable),
  and unexported state initialized by `Build()`; direct struct literals
  from v0.1.0 code are no longer supported.
- **`pattern.WhereParam` compares canonical values**, not Go `==`:
  integer widths now compare equal (e.g. `int64(1)` matches
  `WhereParam("n", 1)`), where v0.1.0 required exact type identity.

### Deprecated
- `NewEvent`, `NewEventID` (random identity), callback behaviors
  (`OnReceive`/`OnEvent`), `Architecture.Start` async runtime,
  dynamic `Binding`, `SubArchitecture`, `dsync.Coordinator`. All still
  work; none are covered by the deterministic guarantee, and all are
  rejected by `PrepareDeterministic`. Legacy callback behaviors now
  apply Rapide consumption semantics: a rule fires at most once per
  consumed event set, so overlapping matches sharing an event no longer
  both fire.

### Evidence
- Semantic decision ledger (332 entries): `docs/SEMANTIC_DECISIONS.md`
- Conformance index (1,369 rows): `docs/CONFORMANCE_TESTS.md`
- Qualification: Windows/amd64 (Go 1.26.2, CGO off) per checkpoint
  RSD-0332, plus darwin/arm64 verification including the race detector
  for this release (see `docs/RELEASE_v0.2.0_CHECKPOINT.md`).

## v0.1.0 — 2026-07-12

Initial release: causal event posets, pattern language, constraints,
architecture runtime, distributed sync, studio, OTel export.
