package gorapide

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestRapideEnumerationTypeIsExactUnionOfTriv(t *testing.T) {
	enumeration, err := NewRapideEnumerationType("Red", "Yellow", "Green")
	if err != nil {
		t.Fatal(err)
	}
	triv := mustRapidePredefinedType(t, "Triv")
	union, err := NewRapideUnionType(
		RapideUnionTag("Red", triv),
		RapideUnionTag("Yellow", triv),
		RapideUnionTag("Green", triv),
	)
	if err != nil {
		t.Fatal(err)
	}
	got := mustMarshalRapideType(t, enumeration)
	want := mustMarshalRapideType(t, union)
	if !bytes.Equal(got, want) {
		t.Fatalf("Enumeration reduction:\n%s\nwant Union-of-Triv:\n%s", got, want)
	}
	parsed, err := ParseRapideType(got)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip := mustMarshalRapideType(t, parsed); !bytes.Equal(roundTrip, got) {
		t.Fatalf("Enumeration reduction changed on round trip:\n%s\n%s", got, roundTrip)
	}
}

func TestRapideEnumerationSubtypeUsesLiteralSetInclusion(t *testing.T) {
	general, err := NewRapideEnumerationType("Red", "Yellow", "Green")
	if err != nil {
		t.Fatal(err)
	}
	special, err := NewRapideEnumerationType("Red", "Green")
	if err != nil {
		t.Fatal(err)
	}
	if subtype, err := IsRapideSubtype(special, general); err != nil || !subtype {
		t.Fatalf("Enumeration subset subtype = %v, %v", subtype, err)
	}
	if subtype, err := IsRapideSubtype(general, special); err != nil || subtype {
		t.Fatalf("reversed Enumeration subset subtype = %v, %v", subtype, err)
	}
}

func TestRapideEnumerationTypeIsCanonicalAcrossOrderCaseAndGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for iteration := 0; iteration < 30; iteration++ {
		if iteration == 15 {
			runtime.GOMAXPROCS(8)
		}
		literals := []string{"Red", "Yellow", "Green"}
		if iteration%2 != 0 {
			literals = []string{"GREEN", "red", "YELLOW"}
		}
		enumeration, err := NewRapideEnumerationType(literals...)
		if err != nil {
			t.Fatal(err)
		}
		encoded := mustMarshalRapideType(t, enumeration)
		if baseline == nil {
			baseline = encoded
		} else if !bytes.Equal(encoded, baseline) {
			t.Fatalf("Enumeration order/case/GOMAXPROCS changed descriptor:\n%s\n%s", baseline, encoded)
		}
	}
}

func TestRapideEnumerationTypeRejectsEmptyAndDuplicateLiterals(t *testing.T) {
	if _, err := NewRapideEnumerationType(); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), "requires at least one literal") {
		t.Fatalf("empty Enumeration error = %v", err)
	}
	if _, err := NewRapideEnumerationType("Red", "red"); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), `duplicate Union tag "red"`) {
		t.Fatalf("duplicate Enumeration literal error = %v", err)
	}
	if _, err := NewRapideUnionType(); !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), "requires at least one tag/member") {
		t.Fatalf("empty Union error = %v", err)
	}
}
