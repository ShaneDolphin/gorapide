package pattern

import (
	"errors"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func stateWitnessTestValue(t *testing.T, value any) gorapide.CanonicalValue {
	t.Helper()
	encoded, err := gorapide.CanonicalizeParameters(map[string]any{"value": value})
	if err != nil {
		t.Fatal(err)
	}
	return encoded[0].Value
}

func stateWitnessTestDigest(digit string) string {
	return "sha256:" + strings.Repeat(digit, 64)
}

func TestMatchWithStateWitnessesRetainsEveryTrueCut(t *testing.T) {
	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "Check", "check", nil)
	inner := MatchEvent("Check")
	matches, err := MatchWithBindings(inner, poset)
	if err != nil || len(matches) != 1 {
		t.Fatalf("inner matches=%#v error=%v", matches, err)
	}
	matchDigest, err := SemanticDigestMatches(matches)
	if err != nil {
		t.Fatal(err)
	}
	expression := Where(inner, BinaryCondition(
		ConditionGreater,
		StateCondition("worker\x00version", "Integer"),
		LiteralCondition(int64(0)),
	))
	witnesses := []MatchStateWitness{
		{MatchDigest: matchDigest, WitnessDigest: stateWitnessTestDigest("0"), State: []StateWitnessValue{{Key: "worker\x00version", Value: stateWitnessTestValue(t, int64(0))}}},
		{MatchDigest: matchDigest, WitnessDigest: stateWitnessTestDigest("1"), State: []StateWitnessValue{{Key: "worker\x00version", Value: stateWitnessTestValue(t, int64(1))}}},
		{MatchDigest: matchDigest, WitnessDigest: stateWitnessTestDigest("2"), State: []StateWitnessValue{{Key: "worker\x00version", Value: stateWitnessTestValue(t, int64(2))}}},
	}
	guarded, evaluations, err := MatchWithStateWitnessAudit(expression, poset, witnesses)
	if err != nil {
		t.Fatal(err)
	}
	if len(guarded) != 1 || len(guarded[0].WitnessDigests) != 2 ||
		guarded[0].WitnessDigests[0] != stateWitnessTestDigest("1") || guarded[0].WitnessDigests[1] != stateWitnessTestDigest("2") {
		t.Fatalf("state-guarded matches=%#v", guarded)
	}
	if len(evaluations) != 3 || evaluations[0].Matched || !evaluations[1].Matched || !evaluations[2].Matched {
		t.Fatalf("state guard evaluation audit=%#v", evaluations)
	}
}

func TestStateConditionRequiresCompleteCanonicalWitnessData(t *testing.T) {
	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "Check", "check", nil)
	expression := Where(MatchEvent("Check"), StateCondition("worker\x00enabled", "Boolean"))
	if _, err := MatchWithBindings(expression, poset); !errors.Is(err, ErrInvalidCondition) {
		t.Fatalf("ordinary state guard error=%v", err)
	}
	if _, err := DeterministicKey(expression); err != nil {
		t.Fatalf("state condition was not canonical: %v", err)
	}
	if _, err := MatchWithStateWitnesses(expression, poset, nil); !errors.Is(err, ErrInvalidCondition) {
		t.Fatalf("missing witness error=%v", err)
	}
}

func TestStateWitnessRejectsMalformedDigestAndNoncanonicalValue(t *testing.T) {
	poset := gorapide.NewPoset()
	addBindingTestEvent(t, poset, "Check", "check", nil)
	inner := MatchEvent("Check")
	matches, err := MatchWithBindings(inner, poset)
	if err != nil {
		t.Fatal(err)
	}
	matchDigest, err := SemanticDigestMatches(matches)
	if err != nil {
		t.Fatal(err)
	}
	expression := Where(inner, StateCondition("worker\x00enabled", "Boolean"))
	malformed := MatchStateWitness{
		MatchDigest: matchDigest, WitnessDigest: "cut", State: []StateWitnessValue{{
			Key: "worker\x00enabled", Value: stateWitnessTestValue(t, true),
		}},
	}
	if _, err := MatchWithStateWitnesses(expression, poset, []MatchStateWitness{malformed}); !errors.Is(err, ErrInvalidCondition) {
		t.Fatalf("malformed digest error=%v", err)
	}
	malformed.WitnessDigest = stateWitnessTestDigest("a")
	malformed.State[0].Value.Text = "ignored"
	if _, err := MatchWithStateWitnesses(expression, poset, []MatchStateWitness{malformed}); !errors.Is(err, ErrInvalidCondition) {
		t.Fatalf("noncanonical value error=%v", err)
	}
}
