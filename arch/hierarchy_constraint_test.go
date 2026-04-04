package arch

import (
	"context"
	"testing"
	"time"

	"github.com/ShaneDolphin/gorapide/constraint"
)

func TestHierarchicalConstraintCheck(t *testing.T) {
	inner := NewArchitecture("inner")
	worker := NewComponent("worker", Interface("W").OutAction("Done").Build(), nil)
	inner.AddComponent(worker)

	innerCS := constraint.NewConstraintSet("inner-checks")
	innerCS.Add(constraint.EventCount("Done", 1, 10))
	inner.WithConstraints(innerCS, constraint.CheckAfter)

	subIface := Interface("Sub").OutAction("Result").Build()
	sa := WrapArchitecture("sub1", inner).
		WithInterface(subIface).
		Export("worker", "Done", "Result").
		Build()

	parent := NewArchitecture("parent")
	parent.AddSubArchitecture(sa)

	parentCS := constraint.NewConstraintSet("parent-checks")
	parentCS.Add(constraint.EventCount("Result", 1, 10))
	parent.WithConstraints(parentCS, constraint.CheckAfter)

	ctx := context.Background()
	parent.Start(ctx)

	worker.Emit("Done", nil)
	time.Sleep(200 * time.Millisecond)

	parent.Stop()
	parent.Wait()

	report := CheckHierarchy(parent)
	if report.Level != "parent" {
		t.Errorf("Level: want parent, got %s", report.Level)
	}
	if len(report.Violations) != 0 {
		t.Errorf("parent violations: want 0, got %d", len(report.Violations))
	}
	if len(report.Children) != 1 {
		t.Fatalf("children: want 1, got %d", len(report.Children))
	}
	if report.Children[0].Level != "parent/inner" {
		t.Errorf("child level: want parent/inner, got %s", report.Children[0].Level)
	}
}

func TestHierarchicalConstraintViolation(t *testing.T) {
	inner := NewArchitecture("inner")
	worker := NewComponent("worker", Interface("W").OutAction("Done").Build(), nil)
	inner.AddComponent(worker)

	innerCS := constraint.NewConstraintSet("inner-checks")
	innerCS.Add(constraint.EventCount("Done", 1, 10))
	inner.WithConstraints(innerCS, constraint.CheckAfter)

	sa := WrapArchitecture("sub1", inner).
		WithInterface(Interface("Sub").Build()).
		Build()

	parent := NewArchitecture("parent")
	parent.AddSubArchitecture(sa)

	ctx := context.Background()
	parent.Start(ctx)
	parent.Stop()
	parent.Wait()

	report := CheckHierarchy(parent)
	if len(report.Children) != 1 {
		t.Fatalf("children: want 1, got %d", len(report.Children))
	}
	if len(report.Children[0].Violations) == 0 {
		t.Error("inner constraint should have violations (no Done events)")
	}
}

func TestHierarchicalViolationReportTotalViolations(t *testing.T) {
	report := &HierarchicalViolationReport{
		Level:      "root",
		Violations: []constraint.ConstraintViolation{{Message: "v1"}},
		Children: []*HierarchicalViolationReport{
			{
				Level:      "child",
				Violations: []constraint.ConstraintViolation{{Message: "v2"}, {Message: "v3"}},
			},
		},
	}
	if report.TotalViolations() != 3 {
		t.Errorf("TotalViolations: want 3, got %d", report.TotalViolations())
	}
}

func TestHierarchicalViolationReportString(t *testing.T) {
	report := &HierarchicalViolationReport{
		Level:      "root",
		Violations: []constraint.ConstraintViolation{},
		Children: []*HierarchicalViolationReport{
			{
				Level:      "child",
				Violations: []constraint.ConstraintViolation{{Constraint: "c1", Message: "bad", Severity: "error"}},
			},
		},
	}
	s := report.String()
	if s == "" {
		t.Error("String should not be empty")
	}
}
