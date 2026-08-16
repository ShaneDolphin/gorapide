package constraint

import (
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

func constraintStateValue(t *testing.T, value any) gorapide.CanonicalValue {
	t.Helper()
	encoded, err := gorapide.CanonicalizeParameters(map[string]any{"value": value})
	if err != nil {
		t.Fatal(err)
	}
	return encoded[0].Value
}

func TestConstraintStateWitnessesAuditEveryTrueCut(t *testing.T) {
	poset := gorapide.NewPoset()
	event, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: "test", Model: "state-witness", Instance: "worker", Action: "Check", Occurrence: "one",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	inner := pattern.MatchEvent("Check")
	matches, err := pattern.MatchWithBindings(inner, poset)
	if err != nil || len(matches) != 1 {
		t.Fatalf("matches=%#v error=%v", matches, err)
	}
	matchDigest, err := pattern.SemanticDigestMatches(matches)
	if err != nil {
		t.Fatal(err)
	}
	guarded := pattern.Where(inner, pattern.BinaryCondition(
		pattern.ConditionGreater,
		pattern.StateCondition("worker\x00version", "Integer"),
		pattern.LiteralCondition(int64(0)),
	))
	check := NewConstraint("monotonic").MustNever("source", guarded, "state guard matched").Build()
	digestOne := "sha256:" + strings.Repeat("1", 64)
	digestTwo := "sha256:" + strings.Repeat("2", 64)
	report, err := check.EvaluateCanonicalWithState(poset, []ClauseStateWitnesses{{
		Constraint: "monotonic", Clause: "source",
		Witnesses: []pattern.MatchStateWitness{
			{MatchDigest: matchDigest, WitnessDigest: digestOne, State: []pattern.StateWitnessValue{{Key: "worker\x00version", Value: constraintStateValue(t, int64(1))}}},
			{MatchDigest: matchDigest, WitnessDigest: digestTwo, State: []pattern.StateWitnessValue{{Key: "worker\x00version", Value: constraintStateValue(t, int64(2))}}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || len(report.Violations) != 1 || len(report.Violations[0].StateWitnesses) != 2 ||
		report.Violations[0].StateWitnesses[0] != digestOne || report.Violations[0].StateWitnesses[1] != digestTwo {
		t.Fatalf("state-guarded constraint report=%#v", report)
	}
	if len(report.StateEvaluations) != 2 || !report.StateEvaluations[0].GuardResult || !report.StateEvaluations[1].GuardResult {
		t.Fatalf("state guard evaluation ledger=%#v", report.StateEvaluations)
	}
	encoded, err := report.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCanonicalConstraintReport(encoded)
	if err != nil || len(parsed.Violations[0].StateWitnesses) != 2 || len(parsed.StateEvaluations) != 2 {
		t.Fatalf("state witness report round trip=%#v error=%v", parsed, err)
	}
	legacyV4 := report
	legacyV4.Format = legacyCanonicalConstraintReportFormatV4
	legacyBytes, err := legacyV4.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	legacyParsed, err := ParseCanonicalConstraintReport(legacyBytes)
	if err != nil || legacyParsed.Format != legacyCanonicalConstraintReportFormatV4 ||
		len(legacyParsed.StateEvaluations) != 2 {
		t.Fatalf("legacy v4 state report=%#v error=%v", legacyParsed, err)
	}
	contradictory := report
	duplicate := contradictory.StateEvaluations[0]
	duplicate.GuardResult = !duplicate.GuardResult
	contradictory.StateEvaluations = append(contradictory.StateEvaluations, duplicate)
	if _, err := contradictory.MarshalCanonical(); !errors.Is(err, ErrInvalidConstraintReport) {
		t.Fatalf("contradictory state evaluation error=%v", err)
	}
	unrelated := report
	unrelated.Violations = append([]CanonicalConstraintViolation(nil), report.Violations...)
	unrelated.Violations[0].StateWitnesses = []string{"sha256:" + strings.Repeat("f", 64)}
	if _, err := unrelated.MarshalCanonical(); !errors.Is(err, ErrInvalidConstraintReport) {
		t.Fatalf("unrelated state witness error=%v", err)
	}
}

func TestNegativeMatchStateViolationLinksOnlyTrueExactWitnesses(t *testing.T) {
	poset := gorapide.NewPoset()
	event := gorapide.NewEvent("Check", "worker", nil)
	if err := poset.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	inner := pattern.MatchEvent("Check")
	matches, err := pattern.MatchWithBindings(inner, poset)
	if err != nil || len(matches) != 1 {
		t.Fatalf("matches=%#v error=%v", matches, err)
	}
	matchDigest, err := pattern.SemanticDigestMatches(matches)
	if err != nil {
		t.Fatal(err)
	}
	guarded := pattern.Where(inner, pattern.BinaryCondition(
		pattern.ConditionGreater,
		pattern.StateCondition("worker\x00version", "Integer"),
		pattern.LiteralCondition(int64(0)),
	))
	check := NewConstraint("negative-state").
		MustNotMatch("source", guarded, "positive state must not make the whole computation match").
		Build()
	trueDigest := "sha256:" + strings.Repeat("3", 64)
	falseDigest := "sha256:" + strings.Repeat("4", 64)
	report, err := check.EvaluateCanonicalWithState(poset, []ClauseStateWitnesses{{
		Constraint: "negative-state", Clause: "source",
		Witnesses: []pattern.MatchStateWitness{
			{MatchDigest: matchDigest, WitnessDigest: trueDigest, State: []pattern.StateWitnessValue{{Key: "worker\x00version", Value: constraintStateValue(t, int64(1))}}},
			{MatchDigest: matchDigest, WitnessDigest: falseDigest, State: []pattern.StateWitnessValue{{Key: "worker\x00version", Value: constraintStateValue(t, int64(0))}}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || len(report.Violations) != 1 ||
		report.Violations[0].Kind != MustNotMatch.String() ||
		len(report.Violations[0].StateWitnesses) != 1 ||
		report.Violations[0].StateWitnesses[0] != trueDigest ||
		len(report.StateEvaluations) != 2 {
		t.Fatalf("negative state report=%#v", report)
	}
}

func TestStateWitnessCandidatesUseConstraintEvaluationProjection(t *testing.T) {
	poset := gorapide.NewPoset()
	visible := gorapide.NewEvent("Check", "worker", map[string]any{"id": int64(1)})
	hidden := gorapide.NewEvent("Check", "other", map[string]any{"id": int64(2)})
	if err := poset.AddEvent(visible); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddEvent(hidden); err != nil {
		t.Fatal(err)
	}
	guarded := pattern.Where(
		pattern.MatchEvent("Check").WhereSource("worker").BindParam("id", pattern.Var("id").WithType("Integer")),
		pattern.BinaryCondition(
			pattern.ConditionGreater,
			pattern.StateCondition("worker\x00version", "Integer"),
			pattern.LiteralCondition(int64(0)),
		),
	)
	current := NewConstraint("state-projection").
		FilterBy(pattern.MatchAny().WhereSource("worker")).
		MustNever("positive", guarded, "positive version").
		Build()
	set := NewConstraintSet("projection-set")
	set.Add(current)

	candidates, err := set.StateWitnessCandidates(poset)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Constraint != "state-projection" ||
		candidates[0].Clause != "positive" || len(candidates[0].Matches) != 1 {
		t.Fatalf("state candidates=%#v", candidates)
	}
	if got := candidates[0].Matches[0].Events; len(got) != 1 || got[0].ID != visible.ID {
		t.Fatalf("candidate events=%#v", got)
	}
}
