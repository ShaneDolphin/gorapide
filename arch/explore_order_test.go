package arch

import "testing"

func TestChoiceScheduleLessOrdersFieldwise(t *testing.T) {
	a := []ChoiceDecision{{Point: "p1", Selection: "a"}}
	b := []ChoiceDecision{{Point: "p1", Selection: "b"}}
	prefix := []ChoiceDecision{{Point: "p1", Selection: "a"}}
	longer := []ChoiceDecision{{Point: "p1", Selection: "a"}, {Point: "p2", Selection: "a"}}

	if !choiceScheduleLess(a, b) || choiceScheduleLess(b, a) {
		t.Fatal("selection field must order a before b, asymmetrically")
	}
	if !choiceScheduleLess(prefix, longer) || choiceScheduleLess(longer, prefix) {
		t.Fatal("a prefix schedule must order before its extension")
	}
	if choiceScheduleLess(a, a) {
		t.Fatal("equal schedules must not compare less")
	}
}
