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
repeatedly as it grows. A second pass below removes a further redundant
copy in the same view-construction path and, for the common case of a
legacy event matched via its own primary role, removes the copy entirely.

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
- `eventView` no longer deep-copies the matched observation's `Params` map
  twice. Previously it copied `observation.Params` directly into the
  view's `Params`, then copied it again inside `copyObservations` for the
  same (aliased) entry in the event's `Observations` list. A new
  `copyObservationsSkipping` reuses the already-computed copy for that one
  entry (matched by map identity via `reflect.Value.Pointer`, never by
  content, and never for a nil map, to avoid aliasing two logically
  distinct empty maps) and still deep-copies every other entry normally.
- **Aliasing change:** when `EventsByName` matches a **legacy
  (non-deterministic)** event via its own **primary** Name/Source role —
  the role equal to the event's own `Name`/`Source`/`Params`, including the
  synthesized fallback for an event with no explicit `Observations` — it
  now returns the shared, frozen stored `*Event` pointer instead of
  building a cloned view. This aligns `EventsByName` with the aliasing
  norm `Event()` and `Events()` already use for legacy events (via
  `snapshotEvent`): results are race-free but frozen at query time, and
  **callers must not mutate a returned legacy-event primary-observation
  match**. Every other case is unchanged and still returns an isolated,
  defensively-copied view: a **secondary** observation role added via
  `AddObservation`/`AddObservationWithTimings`, and **any** match at all
  on a **deterministic** event (deterministic events stay deep-cloned
  everywhere, matching how `Event()`/`Events()` already treat them).
- Observable behavior — which `(event, observation)` pairs match, their
  values, and sort order — is unchanged in both passes.
  `TestEventsByNameContract` pins this: it was originally written against,
  and verified to pass unmodified on, v0.2.2; its defensive-copy assertion
  is now split so it still requires a fresh copy for secondary-role and
  deterministic-event matches, while separately requiring pointer-identity
  with the shared stored event for legacy primary-observation matches
  (verified to fail against the pre-aliasing code and pass after).

### Performance
`BenchmarkEventsByName` (5,000-event poset of deterministic events, 20
distinct names cycling, querying a name with 250 matches — exercises the
`eventView` double-copy dedupe only, since deterministic events never take
the aliasing shortcut) and `BenchmarkEventsByNameLegacyPrimary` (identical
shape, but every event is a legacy `NewEvent` occurrence matched via its
own primary role — exercises the aliasing change), Apple M3, no `-race`.
`ns/op` was noisy across runs in this environment; `B/op`/`allocs/op` were
exactly reproducible across every run and are the more reliable signal:

- `BenchmarkEventsByName`:
  - Before (v0.2.2, commit 95d623b): ~1.61ms–1.75ms/op, ~2.50MB/op,
    23,261 allocs/op.
  - After round 2 (commit 6fc9dc6): ~248µs–429µs/op, 248,528 B/op,
    2,011 allocs/op.
  - After round 3 (this fix, double-copy dedupe): ~315µs–493µs/op,
    164,528 B/op, 1,511 allocs/op — a further ~34% fewer bytes and ~25%
    fewer allocations than round 2, with identical query results.
- `BenchmarkEventsByNameLegacyPrimary` (new in round 3):
  - Before round 3 (round-2 code, still cloning every match): ~349µs–1.0ms/op,
    244,528 B/op, 1,761 allocs/op.
  - After round 3 (this fix, primary-observation aliasing): ~159µs–231µs/op,
    16,528 B/op, 261 allocs/op — about 93% fewer bytes and 85% fewer
    allocations for the common legacy-event case, since 250 of the 261
    remaining allocations are just the returned `EventSet` slice growth,
    not per-match clones.

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
