package arch

import (
	"fmt"
	"strings"

	"github.com/beautiful-majestic-dolphin/gorapide/constraint"
)

// HierarchicalViolationReport organizes constraint violations by hierarchy level.
type HierarchicalViolationReport struct {
	Level      string
	Violations []constraint.ConstraintViolation
	Children   []*HierarchicalViolationReport
}

// CheckHierarchy recursively checks constraints at all levels of the
// architecture hierarchy. Returns a report organized by level.
func CheckHierarchy(a *Architecture) *HierarchicalViolationReport {
	return checkLevel(a, a.Name)
}

func checkLevel(a *Architecture, prefix string) *HierarchicalViolationReport {
	report := &HierarchicalViolationReport{
		Level:      prefix,
		Violations: a.CheckConstraints(),
	}
	if report.Violations == nil {
		report.Violations = []constraint.ConstraintViolation{}
	}

	a.mu.RLock()
	subs := make([]*SubArchitecture, 0, len(a.subArchitectures))
	for _, sa := range a.subArchitectures {
		subs = append(subs, sa)
	}
	a.mu.RUnlock()

	for _, sa := range subs {
		childPrefix := fmt.Sprintf("%s/%s", prefix, sa.inner.Name)
		childReport := checkLevel(sa.inner, childPrefix)
		report.Children = append(report.Children, childReport)
	}

	return report
}

// String returns a formatted multi-level violation report.
func (r *HierarchicalViolationReport) String() string {
	var b strings.Builder
	r.writeLevel(&b, 0)
	return b.String()
}

func (r *HierarchicalViolationReport) writeLevel(b *strings.Builder, depth int) {
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s[%s] %d violations\n", indent, r.Level, len(r.Violations))
	for _, v := range r.Violations {
		fmt.Fprintf(b, "%s  - %s: %s (%s)\n", indent, v.Constraint, v.Message, v.Severity)
	}
	for _, child := range r.Children {
		child.writeLevel(b, depth+1)
	}
}

// TotalViolations returns the total number of violations across all levels.
func (r *HierarchicalViolationReport) TotalViolations() int {
	total := len(r.Violations)
	for _, child := range r.Children {
		total += child.TotalViolations()
	}
	return total
}
