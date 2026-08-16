package gorapide

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func canonicalTestEvent(t *testing.T, occurrence string) *Event {
	t.Helper()
	event, err := NewDeterministicEvent(EventProvenance{
		Profile:    "stanford-rapide-1.0",
		Model:      "canonical-tests",
		Instance:   "component",
		Action:     "Action",
		Occurrence: occurrence,
	}, map[string]any{"occurrence": occurrence})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestCanonicalPosetIgnoresInsertionOrder(t *testing.T) {
	first := canonicalTestEvent(t, "first")
	second := canonicalTestEvent(t, "second")

	left := NewPoset()
	if err := left.AddEvent(first); err != nil {
		t.Fatal(err)
	}
	if err := left.AddEvent(second); err != nil {
		t.Fatal(err)
	}

	firstAgain := canonicalTestEvent(t, "first")
	secondAgain := canonicalTestEvent(t, "second")
	right := NewPoset()
	if err := right.AddEvent(secondAgain); err != nil {
		t.Fatal(err)
	}
	if err := right.AddEvent(firstAgain); err != nil {
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
	if !bytes.Equal(leftBytes, rightBytes) {
		t.Fatalf("insertion order changed canonical bytes:\nleft=%s\nright=%s", leftBytes, rightBytes)
	}

	leftDigest, err := left.SemanticDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.SemanticDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("insertion order changed digest: %s != %s", leftDigest, rightDigest)
	}
}

func TestCanonicalPosetIgnoresObservationRegistrationOrder(t *testing.T) {
	build := func(reverse bool) *Poset {
		p := NewPoset()
		event := canonicalTestEvent(t, "observed")
		if err := p.AddEvent(event); err != nil {
			t.Fatal(err)
		}
		observations := []EventObservation{
			{Name: "Receive", Source: "beta", Params: map[string]any{"n": 1}},
			{Name: "Accept", Source: "alpha", Params: map[string]any{"n": 1}},
		}
		if reverse {
			observations[0], observations[1] = observations[1], observations[0]
		}
		for _, observation := range observations {
			if _, err := p.AddObservation(event.ID, observation); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}

	left, err := build(false).MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	right, err := build(true).MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatalf("observation order changed canonical bytes:\nleft=%s\nright=%s", left, right)
	}
}

func TestCanonicalPosetRejectsUnsupportedLegacyValue(t *testing.T) {
	p := NewPoset()
	event := NewEvent("Legacy", "component", map[string]any{
		"callback": func() {},
	})
	if err := p.AddEvent(event); err != nil {
		t.Fatal(err)
	}
	_, err := p.MarshalCanonical()
	if !errors.Is(err, ErrNonCanonicalValue) {
		t.Fatalf("expected ErrNonCanonicalValue, got %v", err)
	}
}

func TestCanonicalValuesPreserveNumericKinds(t *testing.T) {
	integer, err := CanonicalizeParameters(map[string]any{"n": int64(1)})
	if err != nil {
		t.Fatal(err)
	}
	unsigned, err := CanonicalizeParameters(map[string]any{"n": uint64(1)})
	if err != nil {
		t.Fatal(err)
	}
	floating, err := CanonicalizeParameters(map[string]any{"n": float64(1)})
	if err != nil {
		t.Fatal(err)
	}
	integerBytes, _ := json.Marshal(integer)
	unsignedBytes, _ := json.Marshal(unsigned)
	floatingBytes, _ := json.Marshal(floating)
	if bytes.Equal(integerBytes, unsignedBytes) || bytes.Equal(integerBytes, floatingBytes) || bytes.Equal(unsignedBytes, floatingBytes) {
		t.Fatalf("numeric kinds collapsed:\ninteger=%s\nunsigned=%s\nfloat=%s", integerBytes, unsignedBytes, floatingBytes)
	}
	if !bytes.Contains(integerBytes, []byte(`"kind":"integer"`)) ||
		!bytes.Contains(unsignedBytes, []byte(`"kind":"unsigned"`)) ||
		!bytes.Contains(floatingBytes, []byte(`"kind":"float64"`)) {
		t.Fatal("typed canonical values do not identify numeric kinds")
	}
}

func TestEncodeDecodeCanonicalValueRejectsNoncanonicalFields(t *testing.T) {
	encoded, err := EncodeCanonicalValue(int32(7))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCanonicalValue(encoded)
	if err != nil || decoded != int64(7) {
		t.Fatalf("decoded=%#v, err=%v", decoded, err)
	}
	encoded.Bool = true
	if _, err := DecodeCanonicalValue(encoded); err == nil {
		t.Fatal("noncanonical irrelevant Boolean field was accepted")
	}
	empty, err := EncodeCanonicalValue([]any{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCanonicalValue(empty); err != nil {
		t.Fatalf("canonical empty list failed round trip: %v", err)
	}
}

func TestCanonicalPosetRoundTripIsByteIdentical(t *testing.T) {
	p := NewPoset()
	root, err := NewDeterministicEvent(EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "round-trip", Instance: "a",
		Action: "Root", Occurrence: "1",
	}, map[string]any{
		"integer": int64(-2), "unsigned": uint64(2), "float": float64(1.5),
		"bytes": []byte{0, 1, 255}, "list": []any{true, "x"},
		"object": map[string]any{"b": nil, "a": "first"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AddEvent(root); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AddObservation(root.ID, EventObservation{
		Name: "VisibleRoot", Source: "b", Params: map[string]any{"n": uint64(3)},
	}); err != nil {
		t.Fatal(err)
	}
	child, err := NewDeterministicEvent(EventProvenance{
		Profile: "stanford-rapide-1.0", Model: "round-trip", Instance: "c",
		Action: "Child", Occurrence: "1", Causes: []EventID{root.ID},
	}, map[string]any{"ok": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AddEventWithCause(child, root.ID); err != nil {
		t.Fatal(err)
	}

	before, err := p.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ParseCanonicalPoset(before)
	if err != nil {
		t.Fatal(err)
	}
	after, err := restored.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("canonical round trip changed bytes:\nbefore=%s\nafter=%s", before, after)
	}
	if !restored.IsCausallyBefore(root.ID, child.ID) {
		t.Fatal("canonical round trip lost causal edge")
	}
	if len(restored.ByName("VisibleRoot")) != 1 {
		t.Fatal("canonical round trip lost event observation")
	}
}

func TestParseCanonicalPosetRejectsNoncanonicalAndFalseDepth(t *testing.T) {
	p := NewPoset()
	if err := p.AddEvent(canonicalTestEvent(t, "parse")); err != nil {
		t.Fatal(err)
	}
	canonical, err := p.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCanonicalPoset(append([]byte(" "), canonical...)); !errors.Is(err, ErrInvalidCanonicalPoset) {
		t.Fatalf("expected leading whitespace rejection, got %v", err)
	}
	falseDepth := strings.Replace(string(canonical), `"causal_depth":1`, `"causal_depth":2`, 1)
	if _, err := ParseCanonicalPoset([]byte(falseDepth)); !errors.Is(err, ErrInvalidCanonicalPoset) {
		t.Fatalf("expected inconsistent depth rejection, got %v", err)
	}
}
