package constraint

import (
	"testing"

	"github.com/ShaneDolphin/gorapide/pattern"
)

func TestCloneDeterministicConstraintOwnsPatterns(t *testing.T) {
	alphabet := pattern.MatchEvent("Visible").WhereSource("left")
	clausePattern := pattern.MatchEvent("Required").WhereParam("n", int64(1))
	source := &Constraint{
		Name: "policy", Desc: "closed", Severity: "error",
		Alphabet: []pattern.Pattern{alphabet},
		Clauses: []ConstraintClause{{
			Kind: MustMatch, Name: "required", Pattern: clausePattern, Message: "missing",
		}},
	}
	clone, err := CloneDeterministicConstraint(source)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := clone.DeterministicDigest()
	if err != nil {
		t.Fatal(err)
	}
	alphabet.WhereSource("right")
	clausePattern.WhereParam("n", int64(2))
	source.Name = "changed"
	after, err := clone.DeterministicDigest()
	if err != nil {
		t.Fatal(err)
	}
	if after != digest {
		t.Fatalf("clone digest changed after source mutation: before=%s after=%s", digest, after)
	}
}

func TestConstraintSetDeterministicCheckersAreCanonical(t *testing.T) {
	left := &Constraint{Name: "left", Severity: "error"}
	right := &Constraint{Name: "right", Severity: "error"}
	set := NewConstraintSet("set")
	set.Add(right)
	set.Add(left)
	checkers, err := set.DeterministicCheckers()
	if err != nil {
		t.Fatal(err)
	}
	if len(checkers) != 2 {
		t.Fatalf("checkers=%d, want 2", len(checkers))
	}
	for index := 1; index < len(checkers); index++ {
		before, _ := checkers[index-1].DeterministicDigest()
		after, _ := checkers[index].DeterministicDigest()
		if after < before {
			t.Fatalf("checkers are not in canonical digest order: %s then %s", before, after)
		}
	}
}
