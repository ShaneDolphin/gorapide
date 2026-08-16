# gorapide

A Go implementation of Stanford Rapide 1.0 causal event-driven architecture semantics.

gorapide models software architectures as collections of concurrent components that communicate through partially ordered event sets (posets). Every event carries a causal history, enabling precise reasoning about happens-before relationships, constraint verification, architecture-level observability, and distributed synchronization.

## Installation

```bash
go get github.com/ShaneDolphin/gorapide
```

Requires Go 1.22 or later. The core module has **zero external dependencies**.

## Compatibility and Determinism Status

GoRapide is undergoing a source-grounded recovery of Stanford Rapide 1.0. It is
not yet a complete Rapide 1.0 implementation. The supported compatibility profile,
known gaps, and executable evidence are tracked in:

- [`docs/RAPIDE_COMPATIBILITY_PROFILE.md`](docs/RAPIDE_COMPATIBILITY_PROFILE.md)
- [`docs/RAPIDE_FEATURE_MATRIX.md`](docs/RAPIDE_FEATURE_MATRIX.md)
- [`docs/CONFORMANCE_TESTS.md`](docs/CONFORMANCE_TESTS.md)

`arch.Architecture.PrepareDeterministic` seals the currently supported subset
into a deeply owned `PreparedArchitecture`. The snapshot retains exact
canonical model bytes and may be executed, replayed, or explored repeatedly and
concurrently without rereading caller-owned builders. The existing
`ExecuteDeterministic`, `ReplayDeterministic`, and `ExploreDeterministic`
convenience methods prepare and delegate through the same boundary. Each run
uses a fresh poset, canonical input journal, typed content-derived event IDs,
explicit limits, and a stable logical worklist. It does not start goroutines,
deliver through component channels, read the wall clock, or use random IDs.
The current guaranteed formats are architecture v126, deterministic engine
v244, execution artifact v70, journal v6, semantic-step policy v2,
event-pattern map model/artifact v4, deterministic map engine v5, and map
exploration artifact v1. Canonical strict posets remain v1/v2; computations
with nontrivial causal-equivalence classes use causal-preorder v3/v4.

`EventPatternMap.PrepareDeterministic` provides the equivalent immutable map
snapshot. Mutating architecture, component, interface, pattern, constraint, or
map construction objects after successful preparation cannot change the
prepared canonical bytes or later artifacts.

The older `NewEvent`, `Architecture.Start`, callback behavior, dynamic binding,
and Studio replay APIs remain compatibility/experimental paths. They are not
covered by the deterministic guarantee.

The public boundary is classified as follows:

| Surface | Status | Use |
| --- | --- | --- |
| `rapide.Parse` / `rapide.Compile` / `rapide.CompileMap`, prepared architecture/map snapshots, deterministic model digest, execute, replay, explore, and canonical artifacts | trusted semantic core | Replayable Rapide models and computations |
| Architecture/component builders and declarative mutators | construction only | Build a closed model before deterministic validation/execution |
| `NewEvent`, `Start`, inbox delivery, callback behavior/`Map`, dynamic `Binding`, goroutine `SubArchitecture`, live `Checker`, and `dsync.Coordinator` | legacy asynchronous or integration | Compatibility only; rejected or isolated from deterministic execution |
| Ordinary JSON, DOT/Mermaid, Studio, trace, and telemetry export | presentation/integration | Inspection and adapters; never semantic identity |

See [`docs/DETERMINISTIC_TRUST_BOUNDARY_AUDIT.md`](docs/DETERMINISTIC_TRUST_BOUNDARY_AUDIT.md)
for the reachability evidence and closure plan. C1.2 closes executable model
ownership with immutable prepared architecture and map snapshots; its
[design](docs/C1_2_SEALED_MODEL_DESIGN.md) and
[verification evidence](docs/C1_2_SEALED_MODEL_EVIDENCE.md) are recorded
separately. C1.3 closes the inventoried silent delivery and ignored-error paths
in legacy architecture, hierarchy, binding, snapshot merge, and distributed
synchronization; its [contract](docs/C1_3_LOSS_ERROR_CONTRACT.md) and
[verification evidence](docs/C1_3_LOSS_ERROR_EVIDENCE.md) are recorded
separately. Phase 1 is not complete until the remaining supported
race/cross-platform qualification matrix passes.

The current source-expression milestone includes canonical direct predefined-
scalar qualifications such as `Boolean'(E)` and safe
`Positive <: Natural <: Integer` widening. Qualification is a typed identity
node, never a host conversion. General type-expression qualification,
attributes, `self`, enumeration disambiguation, and visibility restriction are
still explicit compatibility gates.

Typed function-event attributes are also source-visible in patterns:
`F'Call` repeats every formal parameter, and typed `F'Return` repeats those
formals plus `Return`. They match the kernel's real synchronous call/return
occurrences in behaviors and constraints. Overloaded attribute selection,
function-attribute connection triggers, void `F'Return`'s required `Root`
value, and `P'Running`/`P'Completed`/`P'Terminated` remain explicit gates.

Optional sub-modules with their own `go.mod`:
- `otelexport/` — live OpenTelemetry trace export (requires `go.opentelemetry.io/otel`)
- `cmd/rapide-studio/` — visual architecture editor (requires `golang.org/x/net`)

## Quick Start

Build a causal event graph with the fluent builder:

```go
package main

import (
    "fmt"
    "github.com/ShaneDolphin/gorapide"
)

func main() {
    p := gorapide.Build().
        Source("scanner").
        Event("ScanStart").
        Event("VulnFound", "severity", "HIGH").CausedBy("ScanStart").
        Source("aggregator").
        Event("Finding").CausedBy("VulnFound").
        MustDone()

    fmt.Println(p)
    fmt.Println(p.DOT())
}
```

See [docs/DETERMINISTIC_EXECUTION.md](docs/DETERMINISTIC_EXECUTION.md) for the complete deterministic execution walkthrough.

## Package Structure

```
gorapide/              Core: Event, Poset, Builder, VectorClock, JSON/DOT/Mermaid export
  arch/                Architecture runtime: components, connections, behaviors,
                         Map/Binding, Participant, SubArchitecture, hierarchical constraints
  pattern/             Event Pattern Language: typed bindings, causal/logical patterns, timing
  rapide/              Rapide source lexer, parser, static checker, and deterministic lowering
  constraint/          Pattern and predicate constraints with runtime checker
  export/              Standalone format helpers (Jaeger JSON, DOT labels, Mermaid nodes)
  dsync/               Distributed poset synchronization: Transport, Coordinator
  studio/              Visual editor backend: schema, reconstruct, recorder, replay
  otelexport/          Live OpenTelemetry span export (separate go.mod)
  cmd/rapide-studio/   Visual architecture editor web application (separate go.mod)
  examples/            Runnable examples
```

## Core API

### Events and Posets

An `Event` contains an ID, qualified observations, parameters, source component,
and clock information. `NewDeterministicEvent` constructs deeply isolated events
with semantic IDs and no wall-clock value. `NewEvent` is the legacy random-ID,
wall-clock constructor. Events live in a `Poset` that tracks causal edges.

```go
p := gorapide.NewPoset()

e1 := gorapide.NewEvent("ScanStart", "scanner", nil)
p.AddEvent(e1)

e2 := gorapide.NewEvent("VulnFound", "scanner", map[string]any{"cve": "CVE-2026-0001"})
p.AddEventWithCause(e2, e1.ID)

fmt.Println(p.IsCausallyBefore(e1.ID, e2.ID)) // true
fmt.Println(p.TopologicalSort())                // [ScanStart, VulnFound]
```

Rapide local-clock time is explicit simulation data, not Go wall time. An event
may be related to several independent clocks by closed start/finish intervals:

```go
timed, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
    Profile: "stanford-rapide-1.0",
    Model: "example",
    Instance: "scanner",
    Action: "ScanComplete",
    Occurrence: "scan-42",
    Timings: []gorapide.EventTiming{
        {Clock: "mission", Start: 120, Finish: 125},
        {Clock: "scanner", Start: 7, Finish: 12},
    },
}, nil)
if err != nil {
    panic(err)
}
```

Events timed when constructed use `evt2-`; untimed identities remain
byte-compatible `evt1-`. A basic connection can discover a new observer-clock
relationship while preserving the same occurrence identity. A timed poset uses
canonical format `gorapide.canonical-poset.v2`, with ticks encoded as decimal
strings. Adding causality checks `Finish(E1) <= Start(E2)` on every clock shared
by causally ordered events, including through untimed intermediates.

Rapide interface objects are modules, so complex event parameters need
allocation identity rather than Go pointer identity or structural map equality.
`NewRapideModuleValue` derives a canonical `mod1-` identity from explicit model,
parent, generator/literal, occurrence, and causal coordinates:

```go
airplane, err := gorapide.NewRapideModuleValue(gorapide.ModuleAllocationProvenance{
    Profile: "stanford-rapide-1.0",
    Model: "air-traffic",
    Parent: "airspace",
    Generator: "Airplane",
    Occurrence: "flight-42",
})
if err != nil {
    panic(err)
}
```

The value survives event hashing, nested canonical values, placeholder
substitution, poset import/export, and replay. `SameRapideModule` implements the
tool-supplied allocation identity relation (`==`); it is intentionally distinct
from a module's potentially user-defined `=` function.

The next closed type-system layer represents structural interface, function,
event, and action types without inventing nominal source-type identity. For
example, an employee interface with both `Name` and `Salary` provided objects is
a subtype of a salary-only interface, and the predefined `Iterator(T)` interface
inherits that covariance through its `Item()` result:

```go
stringType, _ := gorapide.RapidePredefinedType("String")
floatType, _ := gorapide.RapidePredefinedType("Float")
salaryInfo, _ := gorapide.NewRapideInterfaceType(
    gorapide.ProvidedRapideMember("Salary", floatType),
)
employee, _ := gorapide.NewRapideInterfaceType(
    gorapide.ProvidedRapideMember("Name", stringType),
    gorapide.ProvidedRapideMember("Salary", floatType),
)
employeeIterator, _ := gorapide.RapideIteratorType(employee)
salaryIterator, _ := gorapide.RapideIteratorType(salaryInfo)
ok, _ := gorapide.IsRapideSubtype(employeeIterator, salaryIterator) // true

// Iterable(T) provides a zero-parameter module generator named Iterator.
// The generator is structurally distinct from an ordinary function object.
employeeIterable, _ := gorapide.RapideIterableType(employee)
salaryIterable, _ := gorapide.RapideIterableType(salaryInfo)
iterableOK, _ := gorapide.IsRapideSubtype(employeeIterable, salaryIterable) // true

// Ref(T) and Discrete(T) are invariant by their published structural members.
employeeRef, _ := gorapide.RapideReferenceType(employee)
salaryRef, _ := gorapide.RapideReferenceType(salaryInfo)
refNarrows, _ := gorapide.IsRapideSubtype(employeeRef, salaryRef) // false
employeeDiscrete, _ := gorapide.RapideDiscreteType(employee)
salaryDiscrete, _ := gorapide.RapideDiscreteType(salaryInfo)
discreteNarrows, _ := gorapide.IsRapideSubtype(employeeDiscrete, salaryDiscrete) // false

publishedEvent, _ := gorapide.NewRapideEventType(
    gorapide.RapideEventParam("Item", employee),
)
publisher, _ := gorapide.NewRapideInterfaceType(
    gorapide.OutputRapideAction("Published", publishedEvent),
)

// Stanford interface type-name denotation specifications:
//   type Employee <: SalaryInfo;
//   type Name_Type is String;
elementType, _ := gorapide.NewRapideTypeNameReference("Element")
schema, _ := gorapide.NewRapideInterfaceType(
		gorapide.UnboundedProvidedRapideTypeName("Element"),
		gorapide.ProvidedRapideMember("Item", elementType),
    gorapide.BoundedProvidedRapideTypeName("Employee", salaryInfo),
    gorapide.ExactProvidedRapideTypeName("Name_Type", stringType),
    gorapide.UnboundedPrivateRapideTypeName("Representation"),
)
```

`RapideTypesEqual` follows Stanford's mutual-subtyping definition. Canonical type
descriptors round-trip byte-identically through
`gorapide.rapide-type.v2`; descriptor digests remain distinct from semantic type
equality. The exact recursive `Ref(T)` descriptor retains `:=`, `$`, and
`Is_Nil`; the exact `Discrete(T)` descriptor retains `<`, `=`, and `Succ`.
These operator designators are canonical function-object names, never Go
callbacks. Type-name constituents represent the published unconstrained,
subtype-bounded, and exact denotation specifications; constrained specifications
narrow subtype-wise and type identifiers cannot overload or collide with other
constituents. The `.rpd` frontend parses predefined-typed object and type-name
declarations across provides/requires/private regions, recursively normalizes
them through interface derivation, resolves closed non-nominal aliases and
direct/mutual recursive named interfaces, and elaborates closed nested
`Iterator(T)`, `Iterable(T)`, `Discrete(T)`, and `Ref(T)` applications in
aliases, module type denotations, interface structural slots, and nondependent
constructor signatures. `Iterable(T)` contains the exact published
module-generator constituent, not a function-valued object. Applications expand
to the same structural graph regardless of source case or alias spelling.
Interfaces also accept module-generator name declarations such as
`Iterator : module () return Iterator(Integer);` and
`Build : module (Initial : Integer; type Order <: Discrete(Integer)) return Worker;`
in provides, requires, or private regions. Ordered closed object parameters and
unbounded/bounded type parameters form the conformance signature, participate
in `include`/`replace` normalization, and are encoded on the distinct
module-generator constituent. Parameter-dependent types/results, defaults, and
a concrete module that claims to implement such an obligation still fail
explicitly until symbolic substitution, argument binding, bodies, and
membership tables are restored.
Composite action/function values remain behind an explicit execution-value
gate; the obsolete draft `Range(T)` type, user-declared constructor application,
dependent substitution, general expression-position/nested Record literals, and remaining rich collection type expressions are not
silently approximated. The frontend also parses the
three provides/private type-constructor denotation forms. Constructor characteristic functions retain
ordered formal object parameters plus unbounded/bounded formal type parameters;
closed nondependent name/application bounds and exact bodies follow the published
rule 6 subtype obligations. Finite type-name references are symbolic structural
leaves valid only beneath an interface declaring the referenced constituent;
standalone or unknown references fail. The frontend attaches the exact
descriptor as canonical architecture-v126 model content. Alias chains used in
execution signatures keep the canonical predefined membership type (`Natural`
does not silently become `Integer`). It does not yet elaborate user-declared or
dependent constructor applications, parameter-dependent constructor bodies,
rich non-name/application structural expressions, recursive bounded-type
assumption proofs, or interface
general structural objects in execution value slots, prove constructor/private-
function module membership, or execute arbitrary source-defined `Iterator(T)`
objects, so the kernel is not a claim of complete type-language or general
iterator-object support.

The bounded procedural subset now executes Stanford's first `for` statement
over finite Integer ranges through the published iterator protocol:

```go
body := arch.ForEachIntegerRange(
    "I", arch.LiteralValue(1), arch.LiteralValue(3),
    arch.CallAction("emit", "Emit", arch.BindingParam("value", "I")),
)
```

The equivalent `.rpd` form is
`for I : Integer in 1..3 do Emit(I); end;`; omitting `: Integer` infers the same
canonical type. The two endpoints are evaluated once, one replay-stable
`Range(Integer)` iterator module is allocated, and execution generates
`More'Call`/`More'Return` plus `Item'Call`/`Item'Return` observations exactly in
the LRM order. The lexical item and cursor survive process pauses, `next`, and
`exit`. A reversed range performs only `More'Return(False)`; a nonempty range
is capped at 256 items before any body effect. Architecture-v45, engine-v55,
and artifact-v36 bind this semantic slice. General source structural iterator
objects, `Iterable(T)` member-generator selection, and non-Integer discrete
ranges remain explicit future work.

The identifier itself may also be omitted when the body does not need the item:
`for in 1..3 do Tick(); end;` and
`for : Integer in 1..3 do Tick(); end;` compile identically. Go callers pass an
empty identifier to any of the three `ForEach...` constructors. GoRapide stores
that as binder absence and never invents a hidden source name, so the body
cannot accidentally capture or reference it.

Rapide's separate initializer/test/next form is also executable:

```go
loop := arch.ForObjectExpressions(
    arch.ObjectAssignment("i", arch.LiteralValue(1)),
    arch.ObjectValue(arch.LessOrEqualValues(
        arch.ReadState("i"), arch.LiteralValue(3))),
    arch.ObjectAssignment("i", arch.AddValues(
        arch.ReadState("i"), arch.LiteralValue(1))),
    arch.CallAction("emit", "Emit", arch.StateParam("value", "i")),
)
```

The equivalent source is
`for i := 1 in ($i <= 3) next i := $i + 1 do Emit($i); end for;`.
Direct function-call controls are also supported, for example
`for Initialize() in More() next Advance() do ... end;`. The initializer runs
once; each iteration runs the Boolean test, body, and next expression in that
order. A body `next` still runs the next expression, while `exit` does not.
As specified for predefined `Ref(T)`, `:=` returns `Ref(T)`, not the assigned
`T`; therefore an assignment cannot masquerade as the Boolean test even when
its right-hand side is Boolean. `$reference` remains the value operation.
Call/return events, `Ref` operations, and suspended-process phase are all
audited and replayable. Architecture-v126 and engine-v208 bind this form;
artifact-v69 contains all resulting evidence. Nested/selected calls,
structural object expressions, assignment from a nested call, and suspending
control functions remain explicit gaps.

The Go API can also register a finite implementation of an actual allocated
`Iterator(T)` module. Unlike a range loop's evaluation-local iterator, repeated
uses of the same module allocation share one execution-local cursor:

```go
integerType, _ := gorapide.RapidePredefinedType("Integer")
iteratorValue, _ := gorapide.NewRapideModuleValue(
    gorapide.ModuleAllocationProvenance{
        Profile: arch.CompatibilityProfile,
        Model: "example-model",
        Parent: "worker",
        Generator: "FiniteIntegerIterator",
        Occurrence: "work-order",
    },
)
iterator, _ := arch.NewFiniteIteratorModule(
    iteratorValue, integerType, int64(3), int64(1), int64(2),
)

architecture := arch.NewArchitecture("iterator-example")
if err := architecture.AddFiniteIteratorModule(iterator); err != nil {
    panic(err)
}
body := arch.ForEachIterator(
    "I", iterator,
    arch.CallAction("emit", "Emit", arch.BindingParam("value", "I")),
)
```

The allocation identity, exact item type, and ordered canonical sequence are
architecture-v126 content. `More`/`Item` remains observable, an early `exit`
leaves the next unread item for a later loop using the same allocation, and a
fresh execute or replay resets the cursor to zero. Artifact v39 audits every
registered iterator's final cardinality, next cursor, and exhaustion state.
This is a bounded, predefined-item implementation kernel—not a new collection
type or a claim of arbitrary source `Iterator(T)`/`Iterable(T)` execution.

A zero-parameter iterator generator has different Rapide semantics: every call
allocates a fresh module and cursor. Its name, type, and ordered finite domain
are canonical model data:

```go
generator, _ := arch.NewFiniteIteratorGenerator(
    "FreshItems", integerType, int64(3), int64(1), int64(2),
)
if err := architecture.AddFiniteIteratorGenerator(generator); err != nil {
    panic(err)
}
freshBody := arch.ForEachGeneratedIterator(
    "I", generator,
    arch.CallAction("emit-fresh", "Emit", arch.BindingParam("value", "I")),
)
```

Each evaluation derives a new `mod1-` identity from its model, parent, causal
frontier, match, and lexical statement occurrence, then emits that module's
implicit `Start` event before its first `More` call. Two calls restart
independently; suspension resumes the already allocated module. Normal
exhaustion, `exit`, function return, and process exit lose the evaluation-local
name and emit exactly one implicit `Finish`; that finalization occurrence does
not falsely order the enclosing continuation. The predefined Integer range form
uses the same fresh-module lifecycle. This restores
the module-generator allocation boundary described by Stanford without yet
claiming source generator calls, parameters, arbitrary generator/final bodies,
general alias loss, or `Iterable(T).Iterator` member selection. Externally
constructed closed iterator fixtures retain their shared cursor but are excluded
from lifecycle claims because their allocation event is outside the execution.

The deterministic engine now executes closed component-local basic clocks,
object-valued `in` action clauses, and nested process `pause`/`delay` continuations
without Go timers:

```go
_ = worker.AddBasicClock("Mission") // worker.Mission : Clock is Make_Clock()

timedBody := arch.StatementBody(
    arch.CallActionIn("scheduled", "Completed", "Mission", 5,
        arch.LiteralParam("status", "ok")),
    arch.CallActionPause("working", "Completed", "Mission", 2,
        arch.LiteralParam("status", "working")),
    // Mission.Ticks range 3..5: one member is a replayable choice.
    arch.PauseForRange("Mission", 3, 5),
    arch.DelayFor("Mission", 1),
)
```

Without an explicit directive, semantic quiescence advances a selected clock
exactly to its nearest input, scheduled-action, or suspended-process deadline.
Independent enabled clocks create ordinary choice records, so replay and bounded
exploration cover their permitted order.
The engine chooses zero unconstrained idle ticks; final counters, every advance,
deferred plan, materialized event, process suspension, and selected timing-object
choice appear in artifact v67. Journal v6 can reproduce a finite nonzero idle
history explicitly:

```go
journal.ClockAdvances = []arch.ClockAdvanceDirective{
    {Clock: arch.ClockID("worker", "Mission"), To: 3},
    {Clock: arch.ClockID("worker", "Mission"), To: 5},
}
```

Each absolute target is consumed at semantic quiescence, must move forward, and
cannot pass that clock's nearest deadline. Directive order is semantic and ticks
are encoded as lossless decimal strings. Without directives the canonical
minimum-deadline policy is unchanged. `in 0` canonicalizes to an ordinary call.
A `pause` or `delay` action spans the closed named-clock interval and is
generated at its finish; the equivalent
timed statement generates no event. `delay` additionally makes occurrences
generated during that interval unavailable only to the owning process. The
current resumable boundary is a closed `uint64` duration in a declarative-process
body, including supported nested `if`, `case`, and indefinite-loop control. The
continuation stack preserves selected branches, case alternative order, loop
iteration identity, `exit`/`next`, statement bounds, and lexical audit paths
across every yield. Zero ticks completes in the same process turn, creates no
clock advance, and preserves a timed action's `[Now, Now]` interval. The finite
subtype forms `CallActionInRange`, `CallActionPauseRange`,
`CallActionDelayRange`, `PauseForRange`, and `DelayForRange` represent
`C.Ticks range First..Last`: every member (currently at most 256) is a stable
`timing-object` choice for normal execution, exact replay, and bounded
exploration. A singleton range canonicalizes to its fixed object. Empty or larger
ranges fail explicitly pending complete subtype and `Timing_Error` support. The
same fixed literal, closed-expression, and finite range syntax is accepted in
`.rpd` module `initial` and `when` parts wherever that timing clause may
execute. Closed expressions may supply a fixed value or either range bound,
use immutable Integer module objects and generator actuals, and become
canonical fixed ticks or finite domains during elaboration.
Rule and function continuations; interface-exported/general dependent Ticks
objects and runtime/open timing expressions; automatic
exploration of the unbounded idle-tick domain; related clock types; and `.rpd`
`Timing_Error` propagation remain explicit gaps. The
`Rapide*` timing patterns below query the resulting intervals.

### Event Patterns

The `pattern` package implements a partial event-pattern API. The deterministic
subset has declarative literal/source filters, typed placeholder unification,
canonical match artifacts, and the foundational causal/logical operators.
Closed `P where B` guards over literals, complete placeholder bindings, and
explicit consistent-cut state witnesses are canonical pattern nodes. Binding-
only guards are supported by every source constraint scope; module-generator
constraints also resolve their own declared predefined-scalar state and obtain
witnesses from the engine. General source nesting, general object iteration,
general/nested qualification beyond the finite Integer-range subset,
architecture/interface state ownership, and state guards in other pattern
consumers remain in recovery:

```go
import "github.com/ShaneDolphin/gorapide/pattern"

// Match by name with guards
p := pattern.MatchEvent("VulnFound").WhereParam("severity", "CRITICAL")

// Causal sequence
p = pattern.Seq(pattern.MatchEvent("ScanStart"), pattern.MatchEvent("VulnFound"))

// Typed cross-event equality: only pairs with the same subject bind ?S
subject := pattern.Var("S").WithType("String")
p = pattern.Seq(
    pattern.MatchEvent("Take_In").BindParam("subject", subject),
    pattern.MatchEvent("Deliver").BindParam("subject", subject),
)
matches, err := pattern.MatchWithBindings(p, poset)

// Keep only complete matches whose bound version decreases.
first := pattern.Var("First").WithType("Integer")
second := pattern.Var("Second").WithType("Integer")
p = pattern.Where(
    pattern.Seq(
        pattern.MatchEvent("Write").BindParam("version", first),
        pattern.MatchEvent("Write").BindParam("version", second),
    ),
    pattern.BinaryCondition(
        pattern.ConditionGreaterEqual,
        pattern.BindingCondition(first),
        pattern.BindingCondition(second),
    ),
)
if err != nil {
    panic(err)
}
canonicalMatches, _ := pattern.MarshalCanonicalMatches(matches)

// Stanford Timing(P,T,D,C): every event must be related to "mission";
// T receives earliest Start and D receives latest Finish minus T.
p = pattern.Timing(
    pattern.Seq(pattern.MatchEvent("Start"), pattern.MatchEvent("End")),
    pattern.Var("T"), pattern.Var("D"), "mission",
)
timedMatches, err := pattern.MatchWithBindings(p, poset)

// Duration-consistent forms of the predefined timing macros.
p = pattern.RapideAt(pattern.MatchEvent("Start"), 10, "mission")
p = pattern.RapideBefore(pattern.MatchEvent("End"), 25, "mission")
p = pattern.RapideAfter(pattern.MatchEvent("Start"), 10, "mission")
p = pattern.RapideWithin(pattern.MatchEvent("Operation"), 15, "mission")
p = pattern.RapideWithinRange(pattern.MatchEvent("Operation"), 5, 15, "mission")
p = pattern.RapideTimeBefore(pattern.MatchEvent("Start"), pattern.MatchEvent("End"), "mission")

// Independence (causally unrelated)
p = pattern.Independent(pattern.MatchEvent("ScanA"), pattern.MatchEvent("ScanB"))

// Rapide disjoint conjunction: both match without sharing an event occurrence
p = pattern.Disjoint(pattern.MatchEvent("Observed"), pattern.MatchEvent("Approved"))

// Rapide [* rel ->] iteration over every finite ordered subcomputation
p = pattern.IterateZeroOrMore(pattern.MatchEvent("WriteReturn"), pattern.RelationFollows)

// Rapide [I : 1..10 rel ->] IssueCheck(I): substitute each range value.
p = pattern.IterateIntegerRange(
    pattern.Var("I"), 1, 10, pattern.RelationFollows,
    pattern.MatchEvent("IssueCheck").
        BindParam("number", pattern.Var("I").WithType("Integer")),
)

// Rapide (!D : Integer range 1..10 by ->) WriteCall(!D)
p = pattern.ForAllIntegerRange(
    pattern.Var("D"), 1, 10, pattern.RelationFollows,
    pattern.MatchEvent("WriteCall").
        BindParam("value", pattern.Var("D").WithType("Integer")),
)

// Project visibility without losing causality through hidden intermediates
visible, err := pattern.NewProjection(poset, selectedEvents)

```

The legacy API also exposes `Join`, `ForEach`, callback `Guard`, `Not`, timing
helpers, and pattern macros. Constructs not represented by the documented
deterministic expression subset fail explicitly in `MatchWithBindings` and
deterministic architecture validation. In particular, the legacy timing helpers
inspect `Clock.WallTime`; they are not Rapide local-clock patterns and are outside
the deterministic compatibility profile. The separate `pattern.Timing`
primitive and `pattern.Rapide*` macros above use only explicit Rapide clock
intervals. The prefix distinguishes them from the older Go wall-time functions;
the eventual `.rpd` frontend will retain Stanford's source spellings.

### Constraints

Pattern-based and predicate-based constraints are available to the legacy
runtime checker:

```go
import "github.com/ShaneDolphin/gorapide/constraint"

cs := constraint.NewConstraintSet("pipeline-checks")
cs.Add(constraint.EventCount("VulnFound", 1, 100))
cs.Add(constraint.AllComponentsEmit([]string{"scanner", "aggregator"}))
cs.Add(constraint.CausalDepthMax(10))

// Pattern-based: VulnFound must produce a DocSection
cs.Add(constraint.NewConstraint("completeness").
    Must("vuln_produces_doc",
        pattern.Seq(pattern.MatchEvent("VulnFound"), pattern.MatchEvent("DocSection")),
        "every VulnFound must produce a DocSection").
    Build())

violations, report := cs.CheckAndReport(poset)
```

Runtime checking modes: `CheckAfter` (on stop), `CheckPeriodic` (interval), `CheckOnEvent` (every N events).

The guaranteed kernel accepts only closed pattern constraints. A named
`ConstraintSet` is included in canonical architecture identity, evaluated over
the final computation, and emitted as `ExecutionResult.Constraints` with exact
set/constraint/poset digests, pass/fail decisions, matched event IDs, bindings,
and causal witnesses. Predicate callbacks and checker callback options fail
deterministic-model validation explicitly.

### Architecture Runtime

The `arch` package models running systems with components, connections, and behaviors:

```go
import "github.com/ShaneDolphin/gorapide/arch"

pipeline := arch.NewArchitecture("security-pipeline")

// Define and add components
scannerIface := arch.Interface("Scanner").
    OutAction("VulnFound", arch.P("cve", "string"), arch.P("severity", "string")).
    Build()
scanner := arch.NewComponent("scanner", scannerIface, nil)
pipeline.AddComponent(scanner)

// Register behaviors
scanner.OnEvent("Trigger", func(ctx arch.BehaviorContext) {
    ctx.Emit("VulnFound", map[string]any{"cve": "CVE-2026-0001", "severity": "HIGH"})
})

// Wire a legacy asynchronous runtime connection. For deterministic execution,
// use ExecuteDeterministic and an explicit InputEvent journal.
conn := arch.Connect("scanner", "aggregator").
    On(pattern.MatchEvent("VulnFound")).
    Pipe().Send("ProcessFinding").
    Build()
pipeline.AddConnection(conn)

// Run
pipeline.Start(context.Background())
pipeline.Inject("Trigger", nil)
time.Sleep(100 * time.Millisecond)
pipeline.Stop()
pipeline.Wait()
```

### Map and Binding Constructs

Rapide maps interpret a domain computation as a separate range computation.
The deterministic path uses closed mapping rules and never treats domain events
as range-event causes:

```go
view, err := rapide.CompileMap(rapideSource, "ScanToPolicy", "scanner")
// The source contains:
// map ScanToPolicy() from Scanner to Policy is
// rule
//   (?CVE : String) VulnFound(?CVE) ||>
//     Finding(?CVE) -> (Audit(?CVE) || Notify(?CVE));
// end map ScanToPolicy;
```

The active actual-domain identity (`scanner`) is an explicit application input,
not part of the map generator declaration. `CompileMapWithOptions` additionally
selects one of the six published induced-dependency policies. The current source
slice binds an actual whose interface is exactly the declared named interface;
richer structural-subtype actuals remain gated.

```go
view := arch.NewEventPatternMap("scan_policy_view").
    FromObject("scanner", scannerIface).
    ToInterface(policyIface).
    WithInducedDependencyPolicy(arch.StrongInducedDependencyPolicy).
    AddRule(arch.MappingRule("finding").
        On(pattern.MatchEvent("VulnFound").
            BindParam("cve", pattern.Var("CVE").WithType("String"))).
        Emit("Finding", arch.BindingParam("cve", "CVE")).
        Build()).
    Build()

mapped, err := view.ExecuteDeterministic(domainPoset, arch.MapExecutionLimits{
    MaxFirings: 1000, MaxRangeEvents: 2000,
})
rangePoset := mapped.Range
artifact, _ := mapped.MarshalCanonical()
```

The result includes map `Start`, generator-local order, one explicitly selected
published Rapide induced-dependency relation (`none`, `strong`, `maxima`,
`dominance`, `overlook`, or `diff`), exact domain-match witnesses, optional
range-constraint decisions, and a replay-verifiable canonical artifact. An
omitted policy canonicalizes to `strong`; the policy is part of model and
artifact identity. The current closed slice supports a single named
object/interface domain and source generators composed with `->`, `|>`, `||`,
one bounded full-causal-preorder join (`~`), finite disjunction (`or`), and bounded
finite iteration including `rel or`. Multi-result execution uses stable
generator IDs, explicit-or-canonical choice, exact replay, and bounded
exploration. Stateful maps, function events, module-generator domains, richer
actual-domain subtypes, multiple domains, nested/immediate-bearing join,
source equivalent conjunction, conjunction/union, arbitrary multi-result or unbounded iteration,
`Map_New`, and composition remain explicitly unsupported.

The older callback map below is a legacy adapter for dynamic runtime bindings.
Its Go callbacks are not part of canonical model identity and are not covered by
the deterministic guarantee:

```go
// Legacy adapter: translate one event between interface vocabularies
m := arch.NewMap("scan_to_agg").
    From(scannerIface).
    To(aggregatorIface).
    TranslateWith("VulnFound", "Finding", func(e *gorapide.Event) map[string]any {
        return map[string]any{"cve": e.ParamString("cve"), "mapped": true}
    }).
    Build()

// Dynamic binding with a legacy callback Map
pipeline.BindWith("scanner", "aggregator", arch.WithBindingMap(m))

// Simple binding (identity translation, PipeConnection)
pipeline.Bind("scanner", "consumer")

// Remove bindings
pipeline.Unbind("scanner")
```

### Hierarchical Composition

Architectures can nest — a sub-architecture participates as a component in a parent architecture with events flowing across boundaries via export/import rules:

```go
// Inner architecture with its own components
inner := arch.NewArchitecture("inner-pipeline")
worker := arch.NewComponent("worker", workerIface, nil)
inner.AddComponent(worker)

// Wrap as sub-architecture with boundary rules
sub := arch.WrapArchitecture("processing-unit", inner).
    WithInterface(subIface).
    Import("Request", "worker", "Task").                    // parent -> inner
    Export("worker", "Done", "Result").                      // inner -> parent
    ExportWith("worker", "Raw", "Processed", transformFn).   // with param transform
    Build()

parent := arch.NewArchitecture("parent")
parent.AddSubArchitecture(sub)
parent.Start(ctx)

// Hierarchical constraint checking
report := arch.CheckHierarchy(parent)
fmt.Println(report.TotalViolations())
```

Each architecture level has its own poset — events crossing boundaries create new events in the destination poset, preserving encapsulation.

### Distributed Poset Synchronization

Multiple GoRapide instances can synchronize their posets across nodes. The poset is a grow-only CRDT with idempotent merge.

```go
import "github.com/ShaneDolphin/gorapide/dsync"

// Vector clocks for distributed causality
e := gorapide.NewEvent("Scan", "node1", nil)
e.Clock.Vector = gorapide.VectorClock{"node1": 1}

// Create snapshots for exchange
snap := poset.CreateSnapshot("node1")
incrementalSnap := poset.CreateIncrementalSnapshot("node1", lastHighWater)

// Merge remote snapshots. A partial result and stable validation error may
// both be returned; never discard the error.
result, err := poset.MergeSnapshot(remoteSnap)
if err != nil {
    return fmt.Errorf("merge snapshot (partial result %+v): %w", result, err)
}
_, drainErrs := poset.DrainPendingEdges()
if len(drainErrs) != 0 {
    return errors.Join(drainErrs...)
}

// Automatic sync via Coordinator
net := dsync.NewMemNetwork() // or implement dsync.Transport for gRPC/NATS/HTTP
c := dsync.NewCoordinator("node1", poset, net.Transport("node1"),
    dsync.WithInterval(5*time.Second))
if err := c.AddPeerChecked("node2"); err != nil {
    return err
}
if err := c.StartChecked(ctx); err != nil {
    return err
}
defer c.Stop()
```

### Live OpenTelemetry Export

Stream poset events as OTLP spans to a collector during execution (separate sub-module):

```go
import "github.com/ShaneDolphin/gorapide/otelexport"

exporter, _ := otelexport.NewLiveExporter(otelexport.Config{
    Endpoint:    "localhost:4317",
    Protocol:    otelexport.GRPC,
    ServiceName: "my-pipeline",
    Insecure:    true,
})
defer exporter.Shutdown(ctx)

pipeline := arch.NewArchitecture("pipeline",
    arch.WithObserver(exporter.OnEvent), // zero-config integration
)
```

Events become zero-duration OTLP spans. Causal parents map to parent span IDs. Additional causal parents become span links. Batching and backpressure are built in.

### Export and Visualization

```go
// JSON round-trip (includes vector clocks when present)
data, _ := json.Marshal(poset)
json.Unmarshal(data, newPoset)

// DOT (Graphviz)
dot := poset.DOTWithOptions(gorapide.DOTOptions{
    ColorBySource:   true,
    ClusterBySource: true,
    ShowTimestamps:  true,
    HighlightPath:   []gorapide.EventID{e1.ID, e2.ID},
})

// Mermaid for markdown
mermaid := poset.Mermaid()

// OpenTelemetry-compatible trace spans
spans := poset.ToTraceSpans()
```

## Visual Architecture Editor

Rapide Studio is a web-based visual tool for designing architectures, running live simulations, and watching events flow in real-time.

```bash
cd cmd/rapide-studio
go run . -addr :8400
# Open http://localhost:8400
```

Features:
- **Drag-and-drop canvas** — design architectures visually with Cytoscape.js
- **Component inspector** — view and edit component interfaces and connections
- **Live simulation** — start/stop simulations, inject events, watch real-time event flow
- **Event feed** — scrolling list of all events with names, sources, and parameters
- **WebSocket streaming** — events broadcast over WebSocket as they occur

The editor uses an `ArchitectureSchema` JSON format that can be reconstructed into a live `arch.Architecture` via `studio.Reconstruct()`.

## Running the Example

```bash
go run ./examples/ato_scanner/
```

Five-component ATO security scanning pipeline demonstrating interface definitions, component behaviors, pipe connections, constraint checking, and export formats.

## Running Tests

```bash
# Deterministic core module (1,889 tests across 9 packages)
go test -race ./...

# OTel export sub-module
cd otelexport && go test -race ./...

# Visual editor
cd cmd/rapide-studio && go build ./...
```

## Heritage

gorapide implements the semantics described in the Stanford Rapide 1.0 language reference manuals:

- **Poset semantics** — partially ordered event sets with causal preorder relation
- **Event patterns** — the Event Pattern Language for matching causal structures
- **Architecture composition** — components, connections (basic/pipe/agent), behaviors, hierarchical sub-architectures
- **Maps and bindings** — cross-architecture event translation and dynamic runtime wiring
- **Constraints** — pattern-based and predicate-based constraint checking with runtime modes
- **Distributed synchronization** — vector clocks, CRDT-based poset merge, transport abstraction

The original Rapide language was developed at Stanford University by David Luckham's research group for architecture-level modeling and simulation of concurrent systems.

## License

MIT
