# gorapide

A Go implementation of Stanford Rapide 1.0 causal event-driven architecture semantics.

gorapide models software architectures as collections of concurrent components that communicate through partially ordered event sets (posets). Every event carries a causal history, enabling precise reasoning about happens-before relationships, constraint verification, architecture-level observability, and distributed synchronization.

## Installation

```bash
go get github.com/ShaneDolphin/gorapide
```

Requires Go 1.22 or later. The core module has **zero external dependencies**.

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

## Package Structure

```
gorapide/              Core: Event, Poset, Builder, VectorClock, JSON/DOT/Mermaid export
  arch/                Architecture runtime: components, connections, behaviors,
                         Map/Binding, Participant, SubArchitecture, hierarchical constraints
  pattern/             Event Pattern Language: match, sequence, join, independent, timing
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

An `Event` is an immutable tuple with a unique ID, name, parameters, source component, and a `ClockStamp` (Lamport timestamp + wall time + optional vector clock). Events live in a `Poset` that tracks causal edges.

```go
p := gorapide.NewPoset()

e1 := gorapide.NewEvent("ScanStart", "scanner", nil)
p.AddEvent(e1)

e2 := gorapide.NewEvent("VulnFound", "scanner", map[string]any{"cve": "CVE-2026-0001"})
p.AddEventWithCause(e2, e1.ID)

fmt.Println(p.IsCausallyBefore(e1.ID, e2.ID)) // true
fmt.Println(p.TopologicalSort())                // [ScanStart, VulnFound]
```

### Event Patterns

The `pattern` package implements the Rapide Event Pattern Language:

```go
import "github.com/ShaneDolphin/gorapide/pattern"

// Match by name with guards
p := pattern.MatchEvent("VulnFound").WhereParam("severity", "CRITICAL")

// Causal sequence
p = pattern.Seq(pattern.MatchEvent("ScanStart"), pattern.MatchEvent("VulnFound"))

// Independence (causally unrelated)
p = pattern.Independent(pattern.MatchEvent("ScanA"), pattern.MatchEvent("ScanB"))

// Join (shared common ancestor)
p = pattern.Join(pattern.MatchEvent("FindingA"), pattern.MatchEvent("FindingB"))

// Timing: events within a duration
p = pattern.Within(pattern.Seq(
    pattern.MatchEvent("Start"), pattern.MatchEvent("End"),
), 5*time.Second)
```

Also supports: `ImmSeq`, `Or`, `And`, `Union`, `ForEach`, `Guard`, `Not`, `During`, `After`, `Before`, and pattern macros (`causal_chain`, `fan_in`, `fan_out`).

### Constraints

Both pattern-based and predicate-based constraints with runtime checking:

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

// Wire connections (Basic, Pipe, or Agent semantics)
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

Maps define cross-architecture event translation rules. Bindings create dynamic runtime wiring.

```go
// Map: translate events between interface vocabularies
m := arch.NewMap("scan_to_agg").
    From(scannerIface).
    To(aggregatorIface).
    TranslateWith("VulnFound", "Finding", func(e *gorapide.Event) map[string]any {
        return map[string]any{"cve": e.ParamString("cve"), "mapped": true}
    }).
    Build()

// Dynamic binding with a Map
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

// Merge remote snapshots (idempotent, deduplicates, buffers pending edges)
result, _ := poset.MergeSnapshot(remoteSnap)
poset.DrainPendingEdges() // resolve edges whose endpoints arrived late

// Automatic sync via Coordinator
net := dsync.NewMemNetwork() // or implement dsync.Transport for gRPC/NATS/HTTP
c := dsync.NewCoordinator("node1", poset, net.Transport("node1"),
    dsync.WithInterval(5*time.Second))
c.AddPeer("node2")
c.Start(ctx)
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
# Core module (8 packages, 462+ tests)
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
