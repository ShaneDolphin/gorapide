# gorapide

A Go implementation of Stanford Rapide 1.0 causal event-driven architecture semantics.

gorapide models software architectures as collections of concurrent components that communicate through partially ordered event sets (posets). Every event carries a causal history, enabling precise reasoning about happens-before relationships, constraint verification, and architecture-level observability.

## Installation

```bash
go get github.com/beautiful-majestic-dolphin/gorapide
```

Requires Go 1.22 or later.

## Quick Start

Build a causal event graph with the fluent builder:

```go
package main

import (
    "fmt"
    "github.com/beautiful-majestic-dolphin/gorapide"
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
gorapide/           Core types: Event, Poset, Builder, JSON serialization
  pattern/          Event Pattern Language (match, sequence, join, independent)
  constraint/       Pattern and predicate constraints with runtime checker
  arch/             Architecture runtime (components, connections, behaviors)
  export/           Standalone format helpers (Jaeger JSON, DOT labels, Mermaid nodes)
  examples/         Runnable examples
```

## API Overview

### Events and Posets

An `Event` is an immutable tuple with a unique ID, name, parameters, source component, and a `ClockStamp` (Lamport + wall time). Events live in a `Poset` that tracks causal edges.

```go
p := gorapide.NewPoset()

e1 := gorapide.NewEvent("ScanStart", "scanner", nil)
p.AddEvent(e1)

e2 := gorapide.NewEvent("VulnFound", "scanner", map[string]any{"cve": "CVE-2026-0001"})
p.AddEventWithCause(e2, e1.ID)

fmt.Println(p.IsCausallyBefore(e1.ID, e2.ID)) // true
```

### Event Patterns

The `pattern` package implements the Rapide Event Pattern Language:

```go
import "github.com/beautiful-majestic-dolphin/gorapide/pattern"

// Match by name
p := pattern.MatchEvent("VulnFound")

// Match with guards
p = pattern.MatchEvent("VulnFound").WhereParam("severity", "CRITICAL")

// Causal sequence: A happened before B
p = pattern.Seq(pattern.MatchEvent("ScanStart"), pattern.MatchEvent("VulnFound"))

// Independence: A and B are causally unrelated
p = pattern.Independent(pattern.MatchEvent("ScanA"), pattern.MatchEvent("ScanB"))

// Join: A and B share a common ancestor
p = pattern.Join(pattern.MatchEvent("FindingA"), pattern.MatchEvent("FindingB"))
```

### Constraints

The `constraint` package provides both pattern-based and predicate-based constraints:

```go
import "github.com/beautiful-majestic-dolphin/gorapide/constraint"

cs := constraint.NewConstraintSet("pipeline-checks")

// Predicate constraint: check event count range
cs.Add(constraint.EventCount("VulnFound", 1, 100))

// Predicate constraint: all components must emit events
cs.Add(constraint.AllComponentsEmit([]string{"scanner", "aggregator", "renderer"}))

// Predicate constraint: max causal depth
cs.Add(constraint.CausalDepthMax(10))

// Pattern-based constraint: VulnFound must always be followed by DocSection
cs.Add(constraint.NewConstraint("completeness").
    Must("vuln_produces_doc",
        pattern.Seq(pattern.MatchEvent("VulnFound"), pattern.MatchEvent("DocSection")),
        "every VulnFound must produce a DocSection").
    Build())

// Check against a poset
violations, report := cs.CheckAndReport(poset)
fmt.Println(report)
```

Runtime checking integrates with the architecture:

```go
// Check after architecture stops
pipeline.WithConstraints(cs, constraint.CheckAfter)

// Check every 5 events during execution
pipeline.WithConstraintsOpts(cs, constraint.CheckOnEvent, func(ch *constraint.Checker) {
    ch.SetBatchSize(5)
})
```

### Architecture Runtime

The `arch` package models running systems:

```go
import "github.com/beautiful-majestic-dolphin/gorapide/arch"

// Define component interfaces
scannerIface := arch.Interface("Scanner").
    OutAction("VulnFound", arch.P("cve", "string")).
    Build()

// Create components and architecture
pipeline := arch.NewArchitecture("pipeline")
scanner := arch.NewComponent("scanner", scannerIface, nil)
pipeline.AddComponent(scanner)

// Define behaviors
scanner.OnReceive(func(comp *arch.Component, e *gorapide.Event) {
    comp.Emit("VulnFound", map[string]any{"cve": "CVE-2026-0001"}, e.ID)
})

// Wire connections with causal links
conn := arch.Connect("scanner", "aggregator").
    On(pattern.MatchEvent("VulnFound")).
    Pipe().Send("ProcessFinding").
    Build()
pipeline.AddConnection(conn)

// Run
pipeline.Start(context.Background())
// ... inject events, wait for processing ...
pipeline.Stop()
pipeline.Wait()
```

### Export and Serialization

Posets support JSON round-tripping, DOT/Mermaid visualization, and OpenTelemetry span conversion:

```go
// JSON
data, _ := json.Marshal(poset)
json.Unmarshal(data, newPoset)

// DOT with options
dot := poset.DOTWithOptions(gorapide.DOTOptions{
    ColorBySource:   true,
    ClusterBySource: true,
    ShowTimestamps:  true,
})

// Mermaid for markdown embedding
mermaid := poset.Mermaid()

// OpenTelemetry-compatible trace spans
spans := poset.ToTraceSpans()
```

## Running the Example

```bash
go run ./examples/ato_scanner/
```

This runs a five-component ATO security scanning pipeline that demonstrates interface definitions, component behaviors, pipe connections, constraint checking, and export formats. It intentionally drops a LOW-severity finding to show constraint violation detection.

## Running Tests

```bash
go test -race ./...
go vet ./...
```

## Heritage

gorapide implements the semantics described in the Stanford Rapide 1.0 language reference manuals:

- **Poset semantics** - partially ordered event sets with causal preorder relation
- **Event patterns** - the Event Pattern Language for matching causal structures
- **Architecture composition** - components, connections (basic/pipe/agent), and behaviors
- **Constraints** - pattern-based and sequential/predicate-based constraint checking

The original Rapide language was developed at Stanford University by David Luckham's research group for architecture-level modeling and simulation of concurrent systems.

## License

MIT

## Future Roadmap

- Map and Binding constructs for cross-architecture event translation
- Architecture refinement and hierarchical composition
- Distributed poset synchronization
- OpenTelemetry collector integration for live trace export
- Visual architecture editor and simulation playback
