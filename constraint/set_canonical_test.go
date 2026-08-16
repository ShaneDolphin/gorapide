package constraint

import (
	"bytes"
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func deterministicConstraintSet(reverse bool) *ConstraintSet {
	first := NewConstraint("must-have-start").Must("start", pattern.MatchEvent("Start"), "Start is required").Build()
	second := NewConstraint("no-error").MustNever("error", pattern.MatchEvent("Error"), "Error is forbidden").Build()
	constraints := []*Constraint{first, second}
	if reverse {
		constraints[0], constraints[1] = constraints[1], constraints[0]
	}
	set := NewConstraintSet("policy")
	for _, constraint := range constraints {
		set.Add(constraint)
	}
	return set
}

func TestConstraintSetCanonicalIdentityAndReportIgnoreMemberOrder(t *testing.T) {
	poset := gorapide.NewPoset()
	if err := poset.AddEvent(&gorapide.Event{ID: "start", Source: "component", Name: "Start"}); err != nil {
		t.Fatal(err)
	}
	forward := deterministicConstraintSet(false)
	reverse := deterministicConstraintSet(true)
	leftDigest, err := forward.DeterministicDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := reverse.DeterministicDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("constraint member order changed set identity: %s != %s", leftDigest, rightDigest)
	}
	left, err := forward.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	right, err := reverse.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	leftBytes, err := left.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := right.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, rightBytes) || !left.Passed {
		t.Fatalf("canonical set report differs or failed:\nleft=%s\nright=%s", leftBytes, rightBytes)
	}
	parsed, err := ParseCanonicalConstraintSetReport(leftBytes)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := parsed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leftBytes, roundTrip) {
		t.Fatal("constraint set report did not round-trip byte-identically")
	}
}

func TestConstraintSetRejectsOpaquePredicateChecker(t *testing.T) {
	set := NewConstraintSet("opaque")
	set.Add(CausalDepthMax(3))
	if _, err := set.DeterministicDigest(); !errors.Is(err, ErrInvalidConstraintSet) {
		t.Fatalf("expected ErrInvalidConstraintSet, got %v", err)
	}
}

func TestCanonicalConstraintSetReportReadsLegacyV1Members(t *testing.T) {
	set := deterministicConstraintSet(false)
	report, err := set.EvaluateCanonical(gorapide.Build().Event("Start").MustDone())
	if err != nil {
		t.Fatal(err)
	}
	report.Format = legacyCanonicalConstraintSetReportFormat
	for index := range report.Reports {
		report.Reports[index].Format = legacyCanonicalConstraintReportFormatV3
		report.Reports[index].StateEvaluations = nil
	}
	encoded, err := report.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCanonicalConstraintSetReport(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Format != legacyCanonicalConstraintSetReportFormat {
		t.Fatalf("legacy set report=%#v", parsed)
	}
}

func TestCanonicalConstraintSetReportReadsLegacyV2WithV4Members(t *testing.T) {
	set := deterministicConstraintSet(false)
	report, err := set.EvaluateCanonical(gorapide.Build().Event("Start").MustDone())
	if err != nil {
		t.Fatal(err)
	}
	report.Format = legacyCanonicalConstraintSetReportFormatV2
	for index := range report.Reports {
		report.Reports[index].Format = legacyCanonicalConstraintReportFormatV4
	}
	encoded, err := report.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCanonicalConstraintSetReport(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Format != legacyCanonicalConstraintSetReportFormatV2 {
		t.Fatalf("legacy v2 set report=%#v", parsed)
	}
}
