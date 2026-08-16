package gorapide

import (
	"bytes"
	"runtime"
	"testing"
)

func TestRapideClockTypeMatchesPublishedBaseInterface(t *testing.T) {
	got, err := RapideClockType()
	if err != nil {
		t.Fatal(err)
	}
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
	want := mustRapideInterfaceType(t,
		BoundedProvidedRapideTypeName("Ticks", natural),
		ProvidedRapideMember("Now", now),
		ProvidedRapideMember("Start", eventQuery),
		ProvidedRapideMember("Finish", eventQuery),
		ProvidedRapideMember("Length", eventQuery),
	)
	gotBytes := mustMarshalRapideType(t, got)
	wantBytes := mustMarshalRapideType(t, want)
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("Clock interface:\n%s\nwant:\n%s", gotBytes, wantBytes)
	}
	parsed, err := ParseRapideType(gotBytes)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip := mustMarshalRapideType(t, parsed); !bytes.Equal(roundTrip, gotBytes) {
		t.Fatalf("Clock descriptor changed on round trip:\n%s\n%s", gotBytes, roundTrip)
	}
}

func TestRapideClockTypeTicksReferenceDoesNotEscapeInterface(t *testing.T) {
	clock, err := RapideClockType()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clock.MarshalCanonical(); err != nil {
		t.Fatal(err)
	}
	ticks := mustRapideTypeNameReference(t, "Ticks")
	if _, err := ticks.MarshalCanonical(); err == nil {
		t.Fatal("Clock-local Ticks reference escaped as a standalone type")
	}
}

func TestRapideClockTypeIsCanonicalAcrossGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline []byte
	for iteration := 0; iteration < 30; iteration++ {
		if iteration == 15 {
			runtime.GOMAXPROCS(8)
		}
		clock, err := RapideClockType()
		if err != nil {
			t.Fatal(err)
		}
		encoded := mustMarshalRapideType(t, clock)
		if baseline == nil {
			baseline = encoded
		} else if !bytes.Equal(encoded, baseline) {
			t.Fatalf("GOMAXPROCS changed Clock descriptor:\n%s\n%s", baseline, encoded)
		}
	}
}
