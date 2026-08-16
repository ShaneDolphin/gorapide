package pattern

import (
	"errors"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func selectionEvent(t *testing.T, poset *gorapide.Poset, id string, causes ...gorapide.EventID) *gorapide.Event {
	t.Helper()
	event := &gorapide.Event{ID: gorapide.EventID(id), Name: id, Source: "selection"}
	var err error
	if len(causes) == 0 {
		err = poset.AddEvent(event)
	} else {
		err = poset.AddEventWithCause(event, causes...)
	}
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestEarlierMatchUsesCausalRemainder(t *testing.T) {
	poset := gorapide.NewPoset()
	shared := selectionEvent(t, poset, "shared")
	early := selectionEvent(t, poset, "early", shared.ID)
	late := selectionEvent(t, poset, "late", early.ID)

	isEarlier, err := IsEarlierMatch(
		gorapide.EventSet{shared, early}, gorapide.EventSet{shared, late}, poset,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !isEarlier {
		t.Fatal("shared-event removal did not expose the causal earlier relation")
	}
	reverse, err := IsEarlierMatch(
		gorapide.EventSet{shared, late}, gorapide.EventSet{shared, early}, poset,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reverse {
		t.Fatal("later match was classified as earlier")
	}
}

func TestEarlierMatchRemovesIndependentEvents(t *testing.T) {
	poset := gorapide.NewPoset()
	early := selectionEvent(t, poset, "early")
	late := selectionEvent(t, poset, "late", early.ID)
	leftNoise := selectionEvent(t, poset, "left-noise")
	rightNoise := selectionEvent(t, poset, "right-noise")

	isEarlier, err := IsEarlierMatch(
		gorapide.EventSet{leftNoise, early}, gorapide.EventSet{rightNoise, late}, poset,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !isEarlier {
		t.Fatal("independent events obscured the causal earlier relation")
	}
}

func TestEarlierMatchLeavesIndependentAndEqualMatchesUnordered(t *testing.T) {
	poset := gorapide.NewPoset()
	left := selectionEvent(t, poset, "left")
	right := selectionEvent(t, poset, "right")
	for _, test := range []struct {
		name  string
		first gorapide.EventSet
		other gorapide.EventSet
	}{
		{name: "independent", first: gorapide.EventSet{left}, other: gorapide.EventSet{right}},
		{name: "equal", first: gorapide.EventSet{left}, other: gorapide.EventSet{left}},
		{name: "empty", first: nil, other: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			earlier, err := IsEarlierMatch(test.first, test.other, poset)
			if err != nil {
				t.Fatal(err)
			}
			if earlier {
				t.Fatal("unordered matches acquired an earlier relation")
			}
		})
	}
}

func TestEarlierMatchRequiresEveryRemainingRightEventToHavePredecessor(t *testing.T) {
	poset := gorapide.NewPoset()
	unrelatedRight := selectionEvent(t, poset, "unrelated-right")
	early := selectionEvent(t, poset, "early", unrelatedRight.ID)
	late := selectionEvent(t, poset, "late", early.ID)

	earlier, err := IsEarlierMatch(
		gorapide.EventSet{early}, gorapide.EventSet{late, unrelatedRight}, poset,
	)
	if err != nil {
		t.Fatal(err)
	}
	if earlier {
		t.Fatal("match was earlier even though one dependent right event had no left predecessor")
	}
}

func TestEarlierMatchRejectsEventsOutsideProjection(t *testing.T) {
	poset := gorapide.NewPoset()
	visible := selectionEvent(t, poset, "visible")
	hidden := &gorapide.Event{ID: "hidden", Name: "hidden", Source: "selection"}
	projection, err := NewProjection(poset, gorapide.EventSet{visible})
	if err != nil {
		t.Fatal(err)
	}
	_, err = IsEarlierMatch(gorapide.EventSet{hidden}, gorapide.EventSet{visible}, projection)
	if !errors.Is(err, ErrInvalidMatchSelection) {
		t.Fatalf("expected ErrInvalidMatchSelection, got %v", err)
	}
}
