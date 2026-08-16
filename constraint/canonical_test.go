package constraint

import (
	"bytes"
	"errors"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func addConstraintCanonicalEvent(t *testing.T, poset *gorapide.Poset, action, occurrence string, params map[string]any, causes ...gorapide.EventID) *gorapide.Event {
	t.Helper()
	event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "constraint-canonical", Instance: "component",
		Action: action, Occurrence: occurrence, Causes: causes,
	}, params)
	if err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEventWithCause(event, causes...); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestConstraintDigestIgnoresClauseDeclarationOrder(t *testing.T) {
	first := NewConstraint("ordering").
		Description("canonical model").
		Must("required", pattern.MatchEvent("A"), "A required").
		MustNever("forbidden", pattern.MatchEvent("B"), "B forbidden").
		Build()
	second := NewConstraint("ordering").
		Description("canonical model").
		MustNever("forbidden", pattern.MatchEvent("B"), "B forbidden").
		Must("required", pattern.MatchEvent("A"), "A required").
		Build()
	firstDigest, err := first.DeterministicDigest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.DeterministicDigest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("clause order changed digest: %s != %s", firstDigest, secondDigest)
	}
}

func TestCanonicalConstraintReportIncludesBindingsAndCausalWitness(t *testing.T) {
	poset := gorapide.NewPoset()
	start := addConstraintCanonicalEvent(t, poset, "Start", "start", map[string]any{"subject": "alpha"})
	end := addConstraintCanonicalEvent(t, poset, "End", "end", map[string]any{"subject": "alpha"}, start.ID)
	subject := pattern.Var("S").WithType("String")
	constraint := NewConstraint("forbidden_sequence").
		MustNever("never_start_end", pattern.Seq(
			pattern.MatchEvent("Start").BindParam("subject", subject),
			pattern.MatchEvent("End").BindParam("subject", subject),
		), "sequence is forbidden").
		Build()

	report, err := constraint.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || len(report.Violations) != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	violation := report.Violations[0]
	if len(violation.Events) != 2 || len(violation.Bindings) != 1 || violation.Bindings[0].Placeholder != "S" {
		t.Fatalf("missing event or binding witness: %#v", violation)
	}
	if len(violation.Causality) != 1 || violation.Causality[0].Before != string(start.ID) || violation.Causality[0].After != string(end.ID) {
		t.Fatalf("missing causal witness: %#v", violation.Causality)
	}
	encoded, err := report.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCanonicalConstraintReport(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := parsed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatalf("report round trip changed bytes:\n%s\n%s", encoded, reencoded)
	}
}

func TestCanonicalNegativeMatchViolationRoundTripsWithDistinctKind(t *testing.T) {
	poset := gorapide.NewPoset()
	event := addConstraintCanonicalEvent(t, poset, "X", "one", map[string]any{"value": 7})
	value := pattern.Var("V").WithType("Integer")
	check := NewConstraint("negative").
		MustNotMatch("shape", pattern.MatchEvent("X").BindParam("value", value), "shape is forbidden as a whole").
		Build()
	report, err := check.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || len(report.Violations) != 1 {
		t.Fatalf("negative report=%#v", report)
	}
	violation := report.Violations[0]
	if violation.Kind != MustNotMatch.String() || len(violation.Events) != 1 ||
		violation.Events[0] != string(event.ID) || len(violation.Bindings) != 1 {
		t.Fatalf("negative canonical violation=%#v", violation)
	}
	encoded, err := report.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCanonicalConstraintReport(encoded)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := parsed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, roundTrip) {
		t.Fatal("negative match report did not round-trip byte-identically")
	}
	legacy := report
	legacy.Format = legacyCanonicalConstraintReportFormatV4
	if _, err := legacy.MarshalCanonical(); !errors.Is(err, ErrInvalidConstraintReport) {
		t.Fatalf("legacy report accepted a negative match violation: %v", err)
	}
}

func TestCanonicalConstraintReportRejectsNoncanonicalInput(t *testing.T) {
	poset := gorapide.NewPoset()
	addConstraintCanonicalEvent(t, poset, "Forbidden", "one", nil)
	constraint := NewConstraint("forbidden").
		MustNever("never", pattern.MatchEvent("Forbidden"), "forbidden").
		Build()
	report, err := constraint.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := report.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	withWhitespace := append(append([]byte(nil), encoded...), '\n')
	_, err = ParseCanonicalConstraintReport(withWhitespace)
	if !errors.Is(err, ErrInvalidConstraintReport) {
		t.Fatalf("expected ErrInvalidConstraintReport, got %v", err)
	}
	report.Passed = true
	if _, err := report.MarshalCanonical(); !errors.Is(err, ErrInvalidConstraintReport) {
		t.Fatalf("false passed flag was accepted: %v", err)
	}
}

func TestMatchFailureReportCarriesAssociatedEvents(t *testing.T) {
	poset := gorapide.NewPoset()
	first := addConstraintCanonicalEvent(t, poset, "X", "first", nil)
	second := addConstraintCanonicalEvent(t, poset, "X", "second", nil)
	constraint := NewConstraint("whole").Must("exact", pattern.MatchEvent("X"), "exact X computation required").Build()
	report, err := constraint.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Violations) != 1 || len(report.Violations[0].Events) != 2 {
		t.Fatalf("associated-event witness missing: %#v", report)
	}
	if report.Violations[0].Events[0] != string(first.ID) && report.Violations[0].Events[0] != string(second.ID) {
		t.Fatalf("unexpected witness IDs: %v", report.Violations[0].Events)
	}
}

func TestConstraintReportsIgnoreInsertionDeclarationOrderAndGOMAXPROCS(t *testing.T) {
	originalProcs := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(originalProcs)

	var expected []byte
	for run := 0; run < 100; run++ {
		runtime.GOMAXPROCS(1 + 7*(run%2))
		poset := gorapide.NewPoset()
		forbidden, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: "stanford-rapide-1.0", Model: "constraint-canonical", Instance: "component",
			Action: "Forbidden", Occurrence: "forbidden",
		}, map[string]any{"details": map[string]any{"priority": 1, "zone": "A"}})
		if err != nil {
			t.Fatal(err)
		}
		if run%2 == 1 {
			if err := poset.AddEvent(forbidden); err != nil {
				t.Fatal(err)
			}
		}
		start := addConstraintCanonicalEvent(t, poset, "Start", "start-stress", nil)
		addConstraintCanonicalEvent(t, poset, "End", "end-stress", nil, start.ID)
		if run%2 == 0 {
			if err := poset.AddEvent(forbidden); err != nil {
				t.Fatal(err)
			}
		}

		builder := NewConstraint("stable_report").Description("stable")
		if run%2 == 0 {
			builder.Must("ordered", pattern.Seq(pattern.MatchEvent("Start"), pattern.MatchEvent("End")), "ordered").
				MustNever("forbidden", pattern.MatchEvent("Forbidden"), "forbidden")
		} else {
			builder.MustNever("forbidden", pattern.MatchEvent("Forbidden"), "forbidden").
				Must("ordered", pattern.Seq(pattern.MatchEvent("Start"), pattern.MatchEvent("End")), "ordered")
		}
		report, err := builder.Build().EvaluateCanonical(poset)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := report.MarshalCanonical()
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			expected = encoded
		} else if !bytes.Equal(encoded, expected) {
			t.Fatalf("run %d changed canonical report", run)
		}
	}
}
