package pattern

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestProjectionPreservesVisibleCausalEquivalenceAndQuotientOrder(t *testing.T) {
	poset := gorapide.NewPoset()
	before := addBindingTestEvent(t, poset, "Before", "before", nil)
	left := addBindingTestEvent(t, poset, "Left", "left", nil)
	right := addBindingTestEvent(t, poset, "Right", "right", nil)
	after := addBindingTestEvent(t, poset, "After", "after", nil)
	if err := poset.AddCausalEquivalent(right.ID, left.ID); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddCausal(before.ID, right.ID); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddCausal(left.ID, after.ID); err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(poset, gorapide.EventSet{after, right, before, left})
	if err != nil {
		t.Fatal(err)
	}
	if !projection.IsCausallyEquivalent(left.ID, right.ID) ||
		projection.IsCausallyIndependent(left.ID, right.ID) ||
		!projection.IsCausallyBefore(before.ID, left.ID) ||
		!projection.IsCausallyBefore(right.ID, after.ID) {
		t.Fatal("projected quotient relation is incorrect")
	}
	encoded, err := projection.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	var canonical gorapide.CanonicalPoset
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		t.Fatal(err)
	}
	if canonical.Format != gorapide.CanonicalCausalPreorderFormat || len(canonical.CausalEquivalences) != 1 || len(canonical.Edges) != 2 {
		t.Fatalf("canonical projection=%#v", canonical)
	}
	restored, err := gorapide.ParseCanonicalPoset(encoded)
	if err != nil {
		t.Fatalf("%v\n%s", err, encoded)
	}
	roundTrip, _ := restored.MarshalCanonical()
	if !bytes.Equal(encoded, roundTrip) {
		t.Fatalf("projection round trip changed bytes:\n%s\n%s", encoded, roundTrip)
	}
}

func TestProjectionHidesEquivalentPeerWithoutLosingStrictCausality(t *testing.T) {
	poset := gorapide.NewPoset()
	before := addBindingTestEvent(t, poset, "Before", "before", nil)
	visible := addBindingTestEvent(t, poset, "Visible", "visible", nil)
	hidden := addBindingTestEvent(t, poset, "Hidden", "hidden", nil)
	if err := poset.AddCausalEquivalent(visible.ID, hidden.ID); err != nil {
		t.Fatal(err)
	}
	if err := poset.AddCausal(before.ID, hidden.ID); err != nil {
		t.Fatal(err)
	}
	projection, err := NewProjection(poset, gorapide.EventSet{visible, before})
	if err != nil {
		t.Fatal(err)
	}
	if projection.IsCausallyEquivalent(visible.ID, hidden.ID) || !projection.IsCausallyBefore(before.ID, visible.ID) {
		t.Fatal("hidden equivalent peer leaked or erased inherited causality")
	}
	encoded, err := projection.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	var canonical gorapide.CanonicalPoset
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		t.Fatal(err)
	}
	if canonical.Format != gorapide.CanonicalPosetFormat || len(canonical.CausalEquivalences) != 0 || len(canonical.Edges) != 1 {
		t.Fatalf("hidden-peer canonical projection=%#v", canonical)
	}
}
