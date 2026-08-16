package rapide

import (
	"bytes"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func floatFloorSource() []byte {
	return []byte(`
type Driver is interface action out Set(value : Float); end interface Driver;
type Worker is interface
  Base : Float;
  action in Set(value : Float);
  action out Observed(
    input_floor : Integer; state_floor : Integer;
    tiny_floor : Integer; maximum_floor : Integer
  );
end interface Worker;
module Store(Seed_Arg : Float is 1.75) return Worker is
  Base : Float is Seed_Arg;
  last : var Float := -0.25;
initial
  Observed(Seed_Arg.Floor(), $last.Floor(),
    (-4.9406564584124654e-324).Floor(), 9223372036854774784.0.Floor());
serial
  when (?F : Float) Set(?F)
    where ?F.Floor() = -2 do
    last := ?F;
    Observed(?F.Floor(), $last.Floor(),
      (-4.9406564584124654e-324).Floor(), 9223372036854774784.0.Floor());
  end when;
end module Store;
architecture System(Seed : Float is 1.75) is
  driver : Driver;
  worker : Worker is Store(Seed);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func TestFloatFloorExecutesAndReplaysCanonically(t *testing.T) {
	source := floatFloorSource()
	implicit, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(source, "system", map[string]any{"SEED": 1.75})
	if err != nil {
		t.Fatal(err)
	}
	modelDigest, err := implicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if explicitDigest != modelDigest {
		t.Fatalf("Float.Floor default/actual changed model identity: %s != %s", explicitDigest, modelDigest)
	}
	journal := arch.NewExecutionJournal(modelDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"value": -1.25},
	})

	previous := runtime.GOMAXPROCS(1)
	first, err := implicit.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := explicit.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	observed := first.Poset.ByName("Observed")
	if len(observed) != 2 {
		t.Fatalf("Observed events=%#v, want initial and guarded reaction", observed)
	}
	seenInput := map[int64]bool{}
	seenState := map[int64]bool{}
	for _, event := range observed {
		inputFloor, _ := event.Param("input_floor")
		stateFloor, _ := event.Param("state_floor")
		tinyFloor, _ := event.Param("tiny_floor")
		maximumFloor, _ := event.Param("maximum_floor")
		seenInput[inputFloor.(int64)] = true
		seenState[stateFloor.(int64)] = true
		if tinyFloor != int64(-1) || maximumFloor != int64(9223372036854774784) {
			t.Fatalf("Observed tiny=%#v maximum=%#v", tinyFloor, maximumFloor)
		}
	}
	if !seenInput[1] || !seenInput[-2] || !seenState[-1] || !seenState[-2] {
		t.Fatalf("Float.Floor input/state results=%v/%v", seenInput, seenState)
	}

	firstBytes, err := first.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("Float.Floor changed under GOMAXPROCS or explicit defaults")
	}
	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := implicit.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("Float.Floor replay changed canonical artifact bytes")
	}
}

func TestFloatFloorDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "wrong receiver", expression: `1.Floor()`, want: "receiver has type Integer, want Float"},
		{name: "member argument", expression: `1.0.Floor(1)`, want: "unsupported closed behavior expression call"},
		{name: "global call", expression: `Floor()`, want: "unsupported closed behavior expression call"},
		{name: "positive overflow", expression: `9223372036854775808.0.Floor()`, want: "outside the signed 64-bit Integer range"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("architecture System(N : Integer is " + test.expression + ") is end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

func TestFloatFloorRuntimeOverflowIsDeterministic(t *testing.T) {
	model, err := Compile(floatFloorSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 20, arch.InputEvent{
		Key: "large", Source: "driver", Action: "Set", Params: map[string]any{"value": math.Ldexp(1, 63)},
	})
	for _, processors := range []int{1, 8} {
		previous := runtime.GOMAXPROCS(processors)
		_, err := model.ExecuteDeterministic(journal)
		runtime.GOMAXPROCS(previous)
		if err == nil || !strings.Contains(err.Error(), "outside the signed 64-bit Integer range") {
			t.Fatalf("GOMAXPROCS=%d Floor overflow error=%v", processors, err)
		}
	}
}
