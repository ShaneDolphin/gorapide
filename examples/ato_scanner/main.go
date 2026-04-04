// Command ato_scanner demonstrates a security scanning pipeline modeled
// as a Rapide architecture using gorapide. Five components collaborate to
// produce an Authority-to-Operate (ATO) documentation package:
//
//   TrivyScanner     – emits container vulnerability findings
//   GripeScanner     – emits STIG compliance findings
//   FindingAggregator – deduplicates and enriches findings
//   DocumentRenderer  – produces document sections per finding
//   ATOAssembler      – assembles final ATO package
//
// The example intentionally drops one LOW-severity finding to demonstrate
// constraint violation detection.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/beautiful-majestic-dolphin/gorapide"
	"github.com/beautiful-majestic-dolphin/gorapide/arch"
	"github.com/beautiful-majestic-dolphin/gorapide/constraint"
	"github.com/beautiful-majestic-dolphin/gorapide/pattern"
)

func main() {
	fmt.Println("=== gorapide ATO Scanner Pipeline ===")
	fmt.Println()

	// ---------------------------------------------------------------
	// 1. Define component interfaces
	// ---------------------------------------------------------------

	scannerIface := arch.Interface("Scanner").
		OutAction("VulnFound",
			arch.P("cve", "string"),
			arch.P("severity", "string"),
			arch.P("package", "string"),
		).
		OutAction("ScanComplete").
		Build()

	aggregatorIface := arch.Interface("Aggregator").
		InAction("VulnFound").
		OutAction("EnrichedFinding",
			arch.P("cve", "string"),
			arch.P("severity", "string"),
			arch.P("risk_score", "string"),
		).
		Build()

	rendererIface := arch.Interface("Renderer").
		InAction("EnrichedFinding").
		OutAction("DocSection",
			arch.P("cve", "string"),
			arch.P("section_type", "string"),
		).
		Build()

	assemblerIface := arch.Interface("Assembler").
		InAction("DocSection").
		OutAction("ATOPackage").
		Build()

	// ---------------------------------------------------------------
	// 2. Create the architecture
	// ---------------------------------------------------------------

	pipeline := arch.NewArchitecture("ATO-Pipeline")

	trivyScanner := arch.NewComponent("trivy", scannerIface, nil)
	gripeScanner := arch.NewComponent("gripe", scannerIface, nil)
	aggregator := arch.NewComponent("aggregator", aggregatorIface, nil)
	renderer := arch.NewComponent("renderer", rendererIface, nil)
	assembler := arch.NewComponent("assembler", assemblerIface, nil)

	for _, c := range []*arch.Component{trivyScanner, gripeScanner, aggregator, renderer, assembler} {
		if err := pipeline.AddComponent(c); err != nil {
			fmt.Fprintf(os.Stderr, "AddComponent: %v\n", err)
			os.Exit(1)
		}
	}

	// ---------------------------------------------------------------
	// 3. Define component behaviors
	// ---------------------------------------------------------------

	// Aggregator: enriches findings with a risk score. Intentionally drops
	// LOW severity findings to demonstrate constraint violation.
	aggregator.OnReceive(func(comp *arch.Component, e *gorapide.Event) {
		if e.Name != "VulnFound" {
			return
		}
		severity := e.ParamString("severity")

		// INTENTIONAL BUG: drop LOW severity findings
		if severity == "LOW" {
			return
		}

		riskScore := "unknown"
		switch severity {
		case "CRITICAL":
			riskScore = "9.8"
		case "HIGH":
			riskScore = "7.5"
		case "MEDIUM":
			riskScore = "4.0"
		}

		comp.Emit("EnrichedFinding", map[string]any{
			"cve":        e.ParamString("cve"),
			"severity":   severity,
			"risk_score": riskScore,
			"source":     e.Source,
		}, e.ID)
	})

	// Renderer: converts enriched findings to document sections.
	renderer.OnReceive(func(comp *arch.Component, e *gorapide.Event) {
		if e.Name != "EnrichedFinding" {
			return
		}
		comp.Emit("DocSection", map[string]any{
			"cve":          e.ParamString("cve"),
			"section_type": "vulnerability_assessment",
			"severity":     e.ParamString("severity"),
			"risk_score":   e.ParamString("risk_score"),
		}, e.ID)
	})

	// Assembler: collects document sections into a final package.
	var sectionCount int
	assembler.OnReceive(func(comp *arch.Component, e *gorapide.Event) {
		if e.Name != "DocSection" {
			return
		}
		sectionCount++
	})

	// ---------------------------------------------------------------
	// 4. Wire connections (pipe = causal link)
	// ---------------------------------------------------------------

	// Scanners -> Aggregator
	trivyToAgg := arch.Connect("trivy", "aggregator").
		On(pattern.MatchEvent("VulnFound")).
		Pipe().
		Send("VulnFound").
		Build()

	gripeToAgg := arch.Connect("gripe", "aggregator").
		On(pattern.MatchEvent("VulnFound")).
		Pipe().
		Send("VulnFound").
		Build()

	// Aggregator -> Renderer
	aggToRenderer := arch.Connect("aggregator", "renderer").
		On(pattern.MatchEvent("EnrichedFinding")).
		Pipe().
		Send("EnrichedFinding").
		Build()

	// Renderer -> Assembler
	rendererToAssembler := arch.Connect("renderer", "assembler").
		On(pattern.MatchEvent("DocSection")).
		Pipe().
		Send("DocSection").
		Build()

	for _, conn := range []*arch.Connection{trivyToAgg, gripeToAgg, aggToRenderer, rendererToAssembler} {
		if err := pipeline.AddConnection(conn); err != nil {
			fmt.Fprintf(os.Stderr, "AddConnection: %v\n", err)
			os.Exit(1)
		}
	}

	// ---------------------------------------------------------------
	// 5. Configure constraints
	// ---------------------------------------------------------------

	cs := constraint.NewConstraintSet("ATO-Constraints")

	// All components must emit at least one event.
	cs.Add(constraint.AllComponentsEmit([]string{
		"trivy", "gripe", "aggregator", "renderer", "assembler",
	}))

	// Causal depth should not exceed 10.
	cs.Add(constraint.CausalDepthMax(10))

	// Completeness: every VulnFound must eventually produce a DocSection.
	cs.Add(&constraint.PredicateConstraint{
		Name:     "finding_completeness",
		Desc:     "every VulnFound must produce a DocSection descendant",
		Severity: "error",
		Predicate: func(p *gorapide.Poset) []constraint.ConstraintViolation {
			var violations []constraint.ConstraintViolation
			for _, vf := range p.EventsByName("VulnFound") {
				// Only check scanner-originated VulnFound events.
				if vf.Source != "trivy" && vf.Source != "gripe" {
					continue
				}
				descendants := p.CausalDescendants(vf.ID)
				hasDoc := false
				for _, d := range descendants {
					if d.Name == "DocSection" {
						hasDoc = true
						break
					}
				}
				if !hasDoc {
					violations = append(violations, constraint.ConstraintViolation{
						Constraint:    "finding_completeness",
						Clause:        "vuln_to_doc",
						Kind:          constraint.MustMatch,
						Message:       fmt.Sprintf("VulnFound %s (%s) has no DocSection descendant", vf.ParamString("cve"), vf.ID.Short()),
						MatchedEvents: gorapide.EventSet{vf},
						Severity:      "error",
					})
				}
			}
			return violations
		},
	})

	pipeline.WithConstraints(cs, constraint.CheckAfter)

	// ---------------------------------------------------------------
	// 6. Run the pipeline
	// ---------------------------------------------------------------

	ctx := context.Background()
	if err := pipeline.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Start: %v\n", err)
		os.Exit(1)
	}

	// Simulate scanner findings.
	trivyFindings := []struct{ cve, severity, pkg string }{
		{"CVE-2026-0001", "CRITICAL", "openssl"},
		{"CVE-2026-0002", "HIGH", "libcurl"},
		{"CVE-2026-0003", "LOW", "zlib"}, // will be dropped by aggregator
	}
	for _, f := range trivyFindings {
		trivyScanner.Emit("VulnFound", map[string]any{
			"cve":      f.cve,
			"severity": f.severity,
			"package":  f.pkg,
		})
	}
	trivyScanner.Emit("ScanComplete", nil)

	gripeFindings := []struct{ cve, severity, pkg string }{
		{"STIG-001", "HIGH", "sshd_config"},
		{"STIG-002", "MEDIUM", "audit_rules"},
	}
	for _, f := range gripeFindings {
		gripeScanner.Emit("VulnFound", map[string]any{
			"cve":      f.cve,
			"severity": f.severity,
			"package":  f.pkg,
		})
	}
	gripeScanner.Emit("ScanComplete", nil)

	// Give the pipeline time to process all events.
	time.Sleep(200 * time.Millisecond)

	pipeline.Stop()
	pipeline.Wait()

	// ---------------------------------------------------------------
	// 7. Analyze results
	// ---------------------------------------------------------------

	poset := pipeline.Poset()
	stats := poset.Stats()

	fmt.Println("--- Pipeline Statistics ---")
	fmt.Printf("  Events:     %d\n", stats.EventCount)
	fmt.Printf("  Edges:      %d\n", stats.EdgeCount)
	fmt.Printf("  Roots:      %d\n", stats.RootCount)
	fmt.Printf("  Leaves:     %d\n", stats.LeafCount)
	fmt.Printf("  Max depth:  %d\n", stats.MaxDepth)
	fmt.Printf("  Components: %d\n", stats.ComponentCount)
	fmt.Printf("  Sections:   %d\n", sectionCount)
	fmt.Println()

	// Print causal trace.
	fmt.Println("--- Causal Trace ---")
	sorted := poset.TopologicalSort()
	for _, e := range sorted {
		causes := poset.DirectCauses(e.ID)
		causeNames := make([]string, 0, len(causes))
		for _, c := range causes {
			causeNames = append(causeNames, c.Name)
		}
		sort.Strings(causeNames)
		if len(causeNames) == 0 {
			fmt.Printf("  %s @%s (root)\n", e.Name, e.Source)
		} else {
			fmt.Printf("  %s @%s <- %s\n", e.Name, e.Source, strings.Join(causeNames, ", "))
		}
	}
	fmt.Println()

	// Print constraint results.
	fmt.Println("--- Constraint Report ---")
	fmt.Print(pipeline.ConstraintReport())
	fmt.Println()

	violations := pipeline.CheckConstraints()
	if len(violations) > 0 {
		fmt.Printf("Found %d constraint violation(s):\n", len(violations))
		for _, v := range violations {
			fmt.Printf("  - %s\n", v.Message)
		}
	}
	fmt.Println()

	// ---------------------------------------------------------------
	// 8. Export
	// ---------------------------------------------------------------

	// DOT export with options.
	dot := poset.DOTWithOptions(gorapide.DOTOptions{
		ColorBySource:   true,
		ShowTimestamps:  true,
		ClusterBySource: true,
	})
	fmt.Println("--- DOT Export (first 10 lines) ---")
	lines := strings.Split(dot, "\n")
	for i, line := range lines {
		if i >= 10 {
			fmt.Printf("  ... (%d more lines)\n", len(lines)-10)
			break
		}
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()

	// Mermaid export.
	mermaid := poset.Mermaid()
	fmt.Println("--- Mermaid Export (first 5 lines) ---")
	mlines := strings.Split(mermaid, "\n")
	for i, line := range mlines {
		if i >= 5 {
			fmt.Printf("  ... (%d more lines)\n", len(mlines)-5)
			break
		}
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()

	// JSON export.
	jsonData, err := json.MarshalIndent(poset, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "JSON export: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("--- JSON Export (%d bytes) ---\n", len(jsonData))
	// Print just the metadata section.
	var export gorapide.PosetExport
	json.Unmarshal(jsonData, &export)
	fmt.Printf("  event_count: %s\n", export.Metadata["event_count"])
	fmt.Printf("  edge_count:  %s\n", export.Metadata["edge_count"])
	fmt.Println()

	// Trace spans.
	spans := poset.ToTraceSpans()
	fmt.Printf("--- Trace Spans: %d spans ---\n", len(spans))
	for _, s := range spans {
		parent := "(root)"
		if s.ParentID != "" {
			parent = s.ParentID[:8] + "..."
		}
		fmt.Printf("  %s  parent=%s  attrs=%v\n", s.Name, parent, s.Attributes)
	}
	fmt.Println()

	fmt.Println("=== Done ===")
}
