package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
)

func TestSourceClockAttachesPublishedBaseInterface(t *testing.T) {
	source := []byte(`
type Clock_Alias is Clock;
type Schema is interface type Timer is Clock_Alias; end interface Schema;
architecture System() is schema : Schema; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	got := mustCompiledComponentRapideType(t, model, "schema")
	clock, err := gorapide.RapideClockType()
	if err != nil {
		t.Fatal(err)
	}
	want := mustRootRapideInterfaceType(t,
		gorapide.ExactProvidedRapideTypeName("Timer", clock),
	)
	gotBytes, err := got.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := want.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("source Clock type:\n%s\nwant:\n%s", gotBytes, wantBytes)
	}
}

func TestSourceClockIsCanonicalAcrossAliasesCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type Clock_Alias is Clock;
type Schema is interface type Timer is Clock_Alias; end interface Schema;
architecture System() is schema : Schema; end architecture System;
`),
		[]byte(`
type Clock_Alias is cLoCk;
type Schema is interface type Timer is cLoCk_aLiAs; end interface Schema;
architecture System() is schema : Schema; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 30; iteration++ {
		if iteration == 15 {
			runtime.GOMAXPROCS(8)
		}
		model, err := Compile(sources[iteration%2], "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if baseline == "" {
			baseline = digest
		} else if digest != baseline {
			t.Fatalf("Clock alias/case/GOMAXPROCS changed model: %s != %s", baseline, digest)
		}
	}
}

func TestSourceClockFamilyNamesAreReserved(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "alias",
			source: `type clock is Integer;
architecture System() is end architecture System;`,
			want: `type alias "clock" collides with predefined type "Clock"`,
		},
		{
			name: "interface",
			source: `type SYNCHRONOUS_CLOCK is interface end interface SYNCHRONOUS_CLOCK;
architecture System() is end architecture System;`,
			want: `interface type "SYNCHRONOUS_CLOCK" collides with predefined type "Synchronous_Clock"`,
		},
		{
			name: "Union",
			source: `type GST is union Value : Float; end union GST;
architecture System() is end architecture System;`,
			want: `Union type "GST" collides with predefined type "GST"`,
		},
		{
			name: "Enumeration",
			source: `type Accuracy is enum Exact end enum Accuracy;
architecture System() is end architecture System;`,
			want: `Enumeration type "Accuracy" collides with predefined type "Accuracy"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Compile([]byte(test.source), "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestSourceGSTAndAccuracyAttachPublishedTypes(t *testing.T) {
	source := []byte(`
type GST_Alias is GST;
type Accuracy_Alias is Accuracy;
type Schema is interface
  type Global_Time is GST_Alias;
  type Precision is Accuracy_Alias;
end interface Schema;
architecture System() is schema : Schema; end architecture System;
`)
	model, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	got := mustCompiledComponentRapideType(t, model, "schema")
	gst, err := gorapide.RapidePredefinedType("GST")
	if err != nil {
		t.Fatal(err)
	}
	accuracy, err := gorapide.RapideAccuracyType()
	if err != nil {
		t.Fatal(err)
	}
	want := mustRootRapideInterfaceType(t,
		gorapide.ExactProvidedRapideTypeName("Global_Time", gst),
		gorapide.ExactProvidedRapideTypeName("Precision", accuracy),
	)
	gotBytes, err := got.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := want.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("source GST/Accuracy types:\n%s\nwant:\n%s", gotBytes, wantBytes)
	}
}

func TestSourceGSTAndAccuracyAreCanonicalAcrossAliasesCaseAndGOMAXPROCS(t *testing.T) {
	sources := [][]byte{
		[]byte(`
type GST_Alias is GST;
type Accuracy_Alias is Accuracy;
type Schema is interface
  type Global_Time is GST_Alias;
  type Precision is Accuracy_Alias;
end interface Schema;
architecture System() is schema : Schema; end architecture System;
`),
		[]byte(`
type Schema is interface
  type Global_Time is gSt;
  type Precision is aCcUrAcY;
end interface Schema;
architecture System() is schema : Schema; end architecture System;
`),
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var baseline string
	for iteration := 0; iteration < 30; iteration++ {
		if iteration == 15 {
			runtime.GOMAXPROCS(8)
		}
		model, err := Compile(sources[iteration%2], "System")
		if err != nil {
			t.Fatal(err)
		}
		digest, err := model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if baseline == "" {
			baseline = digest
		} else if digest != baseline {
			t.Fatalf("GST/Accuracy alias/case/GOMAXPROCS changed model: %s != %s", baseline, digest)
		}
	}
}

func TestSourceDerivedClockTypesFailAtPublishedTicksNameConflict(t *testing.T) {
	for _, name := range []string{"Synchronous_Clock", "Regular_Clock", "Slaved_Clock"} {
		t.Run(name, func(t *testing.T) {
			source := []byte("type Alias is " + name + ";\n" +
				"type Schema is interface type Value is Alias; end interface Schema;\n" +
				"architecture System() is schema : Schema; end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(),
				"function Ticks collides with inherited type-name Ticks") {
				t.Fatalf("%s conflict gate = %v", name, err)
			}
		})
	}
}
