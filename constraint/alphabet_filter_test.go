package constraint

import (
	"bytes"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestAlphabetFilterProjectsAllMatchingEventsAndPreservesCausality(t *testing.T) {
	poset := gorapide.Build().
		Event("Start").
		Event("Hidden").CausedBy("Start").
		Event("Done").CausedBy("Hidden").
		MustDone()
	constraint := NewConstraint("public-alphabet").
		FilterFrom(pattern.MatchEvent("Start"), pattern.MatchEvent("Done")).
		Must("ordered", pattern.ImmSeq(pattern.MatchEvent("Start"), pattern.MatchEvent("Done")), "filtered endpoints must be immediate").
		MustNever("hidden", pattern.MatchEvent("Hidden"), "Hidden must be absent from the filtered computation").
		Build()

	report, err := constraint.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || len(report.Violations) != 0 {
		t.Fatalf("alphabet-filter report=%#v", report)
	}
	fullDigest, err := poset.SemanticDigest()
	if err != nil {
		t.Fatal(err)
	}
	if report.PosetDigest != fullDigest || report.EvaluationPosetDigest == fullDigest {
		t.Fatalf("observed/evaluation digests=%s/%s full=%s", report.PosetDigest, report.EvaluationPosetDigest, fullDigest)
	}
	view, err := constraint.evaluationView(poset)
	if err != nil {
		t.Fatal(err)
	}
	if view.Len() != 2 || len(view.ByName("Hidden")) != 0 {
		t.Fatalf("filtered view len=%d hidden=%d", view.Len(), len(view.ByName("Hidden")))
	}
	start, done := view.ByName("Start"), view.ByName("Done")
	if len(start) != 1 || len(done) != 1 || !view.IsCausallyBefore(start[0].ID, done[0].ID) {
		t.Fatal("alphabet projection lost causality through the excluded event")
	}
}

func TestAlphabetFilterOrderAndDuplicatesAreNotSemantic(t *testing.T) {
	build := func(filters ...pattern.Pattern) *Constraint {
		return NewConstraint("alphabet-order").
			FilterFrom(filters...).
			MustNever("none", pattern.MatchEvent("Forbidden"), "forbidden").
			Build()
	}
	left := build(pattern.MatchEvent("A"), pattern.MatchEvent("B"), pattern.MatchEvent("A"))
	right := build(pattern.MatchEvent("B"), pattern.MatchEvent("A"))
	leftDigest, err := left.DeterministicDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.DeterministicDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("alphabet order/duplicates changed identity: %s != %s", leftDigest, rightDigest)
	}
	poset := gorapide.Build().Event("A").Event("B").Event("Other").MustDone()
	leftReport, err := left.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	rightReport, err := right.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	leftBytes, _ := leftReport.MarshalCanonical()
	rightBytes, _ := rightReport.MarshalCanonical()
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatalf("alphabet order/duplicates changed report:\nleft=%s\nright=%s", leftBytes, rightBytes)
	}
}

func TestAlphabetFilterRetainsOnlyMatchedQualifiedViews(t *testing.T) {
	poset := gorapide.NewPoset()
	event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: "test", Model: "alphabet", Instance: "worker", Action: "Private", Occurrence: "one",
	}, map[string]any{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	if _, err := poset.AddObservation(event.ID, gorapide.EventObservation{
		Name: "Public", Source: "worker", Params: map[string]any{"n": 1},
	}); err != nil {
		t.Fatal(err)
	}
	constraint := NewConstraint("qualified-view").
		FilterFrom(pattern.MatchEvent("Public").WhereSource("worker")).
		Must("public", pattern.MatchEvent("Public"), "public alias missing").
		MustNever("private", pattern.MatchEvent("Private"), "unselected private view leaked").
		Build()
	report, err := constraint.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("qualified-view alphabet report=%#v", report)
	}
}

func TestAlphabetFilterRejectsNonBasicAndMixedFilterModels(t *testing.T) {
	tests := []*Constraint{
		NewConstraint("compound").FilterFrom(pattern.Seq(pattern.MatchEvent("A"), pattern.MatchEvent("B"))).
			MustNever("none", pattern.MatchEvent("X"), "x").Build(),
		NewConstraint("mixed").FilterBy(pattern.MatchEvent("A")).FilterFrom(pattern.MatchEvent("B")).
			MustNever("none", pattern.MatchEvent("X"), "x").Build(),
	}
	for _, constraint := range tests {
		if _, err := constraint.DeterministicDigest(); err == nil {
			t.Fatalf("invalid alphabet filter model %q was accepted", constraint.Name)
		}
	}
}

func TestCanonicalConstraintReportReadsLegacyUnfilteredV1(t *testing.T) {
	constraint := NewConstraint("legacy-report").
		MustNever("none", pattern.MatchEvent("Forbidden"), "forbidden").Build()
	report, err := constraint.EvaluateCanonical(gorapide.Build().Event("Allowed").MustDone())
	if err != nil {
		t.Fatal(err)
	}
	report.Format = legacyCanonicalConstraintReportFormat
	report.EvaluationPosetDigest = ""
	encoded, err := report.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCanonicalConstraintReport(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Format != legacyCanonicalConstraintReportFormat || parsed.EvaluationPosetDigest != "" {
		t.Fatalf("legacy constraint report=%#v", parsed)
	}
}
