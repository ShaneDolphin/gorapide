package gorapide

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestRapideGSTPreservesAbstractFloatSubtype(t *testing.T) {
	gst := mustRapidePredefinedType(t, "GST")
	floatType := mustRapidePredefinedType(t, "Float")
	if !mustRapideSubtype(t, gst, floatType) {
		t.Fatal("GST does not subtype Float")
	}
	if mustRapideSubtype(t, floatType, gst) {
		t.Fatal("Float unexpectedly subtypes abstract GST")
	}
	if equal, err := RapideTypesEqual(gst, floatType); err != nil || equal {
		t.Fatalf("GST = Float: %v, %v", equal, err)
	}
	if IsSupportedPredefinedType("GST") || CanonicalValueMatchesPredefinedType(0.0, "GST") {
		t.Fatal("abstract GST unexpectedly acquired executable Float values")
	}
	encoded := mustMarshalRapideType(t, gst)
	parsed, err := ParseRapideType(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip := mustMarshalRapideType(t, parsed); !bytes.Equal(roundTrip, encoded) {
		t.Fatalf("GST descriptor changed on round trip:\n%s\n%s", encoded, roundTrip)
	}
}

func TestRapideAccuracyTypeErasesOnlyPublishedValueConstraint(t *testing.T) {
	got, err := RapideAccuracyType()
	if err != nil {
		t.Fatal(err)
	}
	kind, err := NewRapideEnumerationType("Interval", "Ratio")
	if err != nil {
		t.Fatal(err)
	}
	floatType := mustRapidePredefinedType(t, "Float")
	want := mustRapideInterfaceType(t,
		ProvidedRapideMember("Kind", kind),
		ProvidedRapideMember("Measure", floatType),
	)
	gotBytes := mustMarshalRapideType(t, got)
	wantBytes := mustMarshalRapideType(t, want)
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("Accuracy Record:\n%s\nwant:\n%s", gotBytes, wantBytes)
	}
}

func TestPublishedSynchronousClockConflictsWithTypeNameUniqueness(t *testing.T) {
	members, _ := mustPublishedSynchronousClockMembers(t)
	_, err := NewRapideInterfaceType(members...)
	if !errors.Is(err, ErrInvalidRapideType) ||
		!strings.Contains(err.Error(), `type-name constituent "ticks" collides`) {
		t.Fatalf("exact published Synchronous_Clock conflict = %v", err)
	}

	for _, constructor := range []struct {
		name string
		make func() (RapideType, error)
	}{
		{name: "Synchronous_Clock", make: RapideSynchronousClockType},
		{name: "Regular_Clock", make: RapideRegularClockType},
		{name: "Slaved_Clock", make: RapideSlavedClockType},
	} {
		_, err := constructor.make()
		if !errors.Is(err, ErrInvalidRapideType) ||
			!strings.Contains(err.Error(), "function Ticks collides with inherited type-name Ticks") {
			t.Fatalf("%s gate = %v", constructor.name, err)
		}
	}
}

func TestRapideClockSupportTypesAndDerivedGatesAreCanonicalAcrossGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline [][]byte
	var baselineErrors []string
	for iteration := 0; iteration < 30; iteration++ {
		if iteration == 15 {
			runtime.GOMAXPROCS(8)
		}
		accuracy, err := RapideAccuracyType()
		if err != nil {
			t.Fatal(err)
		}
		gst := mustRapidePredefinedType(t, "GST")
		encoded := [][]byte{
			mustMarshalRapideType(t, gst),
			mustMarshalRapideType(t, accuracy),
		}
		errors := make([]string, 0, 3)
		for _, constructor := range []func() (RapideType, error){
			RapideSynchronousClockType, RapideRegularClockType, RapideSlavedClockType,
		} {
			_, err := constructor()
			if err == nil {
				t.Fatal("derived Clock contradiction gate unexpectedly succeeded")
			}
			errors = append(errors, err.Error())
		}
		if baseline == nil {
			baseline = encoded
			baselineErrors = errors
			continue
		}
		for index := range encoded {
			if !bytes.Equal(encoded[index], baseline[index]) {
				t.Fatalf("GOMAXPROCS changed derived Clock descriptor %d", index)
			}
		}
		for index := range errors {
			if errors[index] != baselineErrors[index] {
				t.Fatalf("GOMAXPROCS changed derived Clock gate %d", index)
			}
		}
	}
}

func mustPublishedClockMembers(t *testing.T) ([]RapideInterfaceMember, RapideType) {
	t.Helper()
	natural := mustRapidePredefinedType(t, "Natural")
	ticks := mustRapideTypeNameReference(t, "Ticks")
	eventType, err := NewRapideEventType()
	if err != nil {
		t.Fatal(err)
	}
	now := mustRapideFunctionType(t, nil, ticks)
	eventQuery := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("E", eventType)}, ticks,
	)
	return []RapideInterfaceMember{
		BoundedProvidedRapideTypeName("Ticks", natural),
		ProvidedRapideMember("Now", now),
		ProvidedRapideMember("Start", eventQuery),
		ProvidedRapideMember("Finish", eventQuery),
		ProvidedRapideMember("Length", eventQuery),
	}, ticks
}

func mustPublishedSynchronousClockMembers(t *testing.T) ([]RapideInterfaceMember, RapideType) {
	t.Helper()
	members, ticks := mustPublishedClockMembers(t)
	gst := mustRapidePredefinedType(t, "GST")
	gstAtTick := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("T", ticks)}, gst,
	)
	distance := mustRapideFunctionType(t, []RapideFunctionParameter{
		RapideObjectParameter("T1", ticks),
		RapideObjectParameter("T2", ticks),
	}, gst)
	ticksAtGST := mustRapideFunctionType(t,
		[]RapideFunctionParameter{RapideObjectParameter("D", gst)}, ticks,
	)
	return append(members,
		ProvidedRapideMember("GST", gstAtTick),
		ProvidedRapideMember("Distance", distance),
		ProvidedRapideMember("Ticks", ticksAtGST),
	), ticks
}
