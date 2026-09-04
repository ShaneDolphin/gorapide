package constraint

import (
	"fmt"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/pattern"
)

// digestCountingPoset is a PosetReader that counts how many times its
// semantic digest is requested. Projections built from it are separate
// objects and are not counted, so the counter measures only whole-poset
// re-encodings.
type digestCountingPoset struct {
	*gorapide.Poset
	digests int
}

func (p *digestCountingPoset) SemanticDigest() (string, error) {
	p.digests++
	return p.Poset.SemanticDigest()
}

func digestReusePoset(t *testing.T, claims int) *digestCountingPoset {
	t.Helper()
	p := gorapide.NewPoset()
	root, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "sys", Instance: "binder", Action: "submit", Occurrence: "1",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AddEvent(root); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < claims; i++ {
		claim, err := gorapide.NewDeterministicEvent(gorapide.EventProvenance{
			Profile: "stanford-rapide-1.0", Model: "sys", Instance: "claim", Action: "control_implementation",
			Occurrence: fmt.Sprint(i), Causes: []gorapide.EventID{root.ID},
		}, map[string]any{"control_id": fmt.Sprintf("AC-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if err := p.AddEventWithCause(claim, root.ID); err != nil {
			t.Fatal(err)
		}
	}
	return &digestCountingPoset{Poset: p}
}

func controlConstraint(id string) *Constraint {
	return NewConstraint("req/"+id).
		Must("impl", pattern.MatchEvent("control_implementation").WhereParam("control_id", id), "missing").
		Build()
}

// An unfiltered constraint evaluates against the poset itself, so its
// EvaluationPosetDigest is the PosetDigest by construction. v0.2.5 encoded the
// whole poset twice to discover that.
func TestConstraintEvaluateCanonicalDigestsUnfilteredPosetOnce(t *testing.T) {
	poset := digestReusePoset(t, 8)
	report, err := controlConstraint("AC-3").EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	if poset.digests != 1 {
		t.Fatalf("unfiltered constraint requested %d poset digests, want 1", poset.digests)
	}
	if report.PosetDigest != report.EvaluationPosetDigest {
		t.Fatalf("unfiltered constraint must report the poset digest as its evaluation digest")
	}
	expected, _ := poset.Poset.SemanticDigest()
	if report.PosetDigest != expected {
		t.Fatalf("PosetDigest = %s, want %s", report.PosetDigest, expected)
	}
}

// A filtered constraint still digests its projection separately: the
// projection is a different poset and its digest is a different value.
func TestConstraintEvaluateCanonicalFilteredKeepsProjectionDigest(t *testing.T) {
	poset := digestReusePoset(t, 8)
	filtered := NewConstraint("filtered").
		FilterBy(pattern.MatchEvent("control_implementation").WhereParam("control_id", "AC-3")).
		Must("one", pattern.MatchEvent("control_implementation").WhereParam("control_id", "AC-3"), "").
		Build()
	report, err := filtered.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	if poset.digests != 1 {
		t.Fatalf("filtered constraint requested %d whole-poset digests, want 1", poset.digests)
	}
	if report.PosetDigest == report.EvaluationPosetDigest {
		t.Fatalf("filtered constraint must digest its projection, not reuse the poset digest")
	}
}

// A set evaluation encodes the poset exactly once regardless of member count.
// v0.2.5 encoded it 1 + 2N times, which made evaluating a few hundred
// requirement constraints over a large poset take minutes.
func TestConstraintSetEvaluateCanonicalDigestsPosetOnce(t *testing.T) {
	poset := digestReusePoset(t, 16)
	set := NewConstraintSet("reqset")
	for i := 0; i < 12; i++ {
		set.Add(controlConstraint(fmt.Sprintf("AC-%d", i)))
	}
	report, err := set.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	if poset.digests != 1 {
		t.Fatalf("set of 12 requested %d poset digests, want 1", poset.digests)
	}
	if !report.Passed || len(report.Reports) != 12 {
		t.Fatalf("unexpected report: passed=%v members=%d", report.Passed, len(report.Reports))
	}
	for _, member := range report.Reports {
		if member.PosetDigest != report.PosetDigest {
			t.Fatalf("member PosetDigest %s differs from set PosetDigest %s", member.PosetDigest, report.PosetDigest)
		}
		if member.EvaluationPosetDigest != report.PosetDigest {
			t.Fatalf("unfiltered member EvaluationPosetDigest %s differs from set PosetDigest %s", member.EvaluationPosetDigest, report.PosetDigest)
		}
	}
}

// The reuse path must produce the same bytes as evaluating each member on its
// own, so a set report and its members' standalone reports stay interchangeable.
func TestConstraintSetDigestReuseMatchesStandaloneReports(t *testing.T) {
	poset := digestReusePoset(t, 16)
	set := NewConstraintSet("reqset")
	set.Add(controlConstraint("AC-1"))
	set.Add(controlConstraint("AC-99"))
	set.Add(NewConstraint("filtered").
		FilterBy(pattern.MatchEvent("control_implementation").WhereParam("control_id", "AC-2")).
		Must("one", pattern.MatchEvent("control_implementation").WhereParam("control_id", "AC-2"), "").
		Build())
	setReport, err := set.EvaluateCanonical(poset)
	if err != nil {
		t.Fatal(err)
	}
	members, err := set.DeterministicConstraints()
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != len(setReport.Reports) {
		t.Fatalf("members=%d reports=%d", len(members), len(setReport.Reports))
	}
	for index, member := range members {
		standalone, err := member.EvaluateCanonical(poset.Poset)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := standalone.MarshalCanonical()
		got, _ := setReport.Reports[index].MarshalCanonical()
		if string(want) != string(got) {
			t.Fatalf("member %q: set-evaluated report differs from standalone report\nset: %s\nstandalone: %s", member.Name, got, want)
		}
	}
}
