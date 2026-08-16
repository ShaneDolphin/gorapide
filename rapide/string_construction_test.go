package rapide

import (
	"bytes"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func stringConstructionSource() []byte {
	return []byte(`
type Driver is interface action out Set(value : String); end interface Driver;
type Worker is interface
  Result : String;
  action in Set(value : String);
  action out Observed(seed : String; wrapped : String; arbitrary : String; length : Integer);
end interface Worker;
module Store(Seed_Arg : String is "A".Append('B')) return Worker is
  Result : String is Seed_Arg.Prepend('\91').Append('\93');
  last : var String := "".Append(Code_To_Char(9223372036854775807));
initial
  Observed(Seed_Arg, Result, $last, $last.Length());
serial
  when (?S : String) Set(?S)
    where ?S.Append('B') = "AB" and
      ?S.Prepend(Code_To_Char(-1)).Append(Code_To_Char(9223372036854775807)).Length() = 3 do
    last := ?S.Prepend(Code_To_Char(-1)).Append(Code_To_Char(9223372036854775807));
    Observed(Seed_Arg, Result, $last, $last.Length());
  end when;
end module Store;
architecture System(Seed : String is "A".Append('B')) is
  driver : Driver;
  worker : Worker is Store(Seed);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func TestStringConstructionExecutesAndReplaysCanonically(t *testing.T) {
	source := stringConstructionSource()
	implicit, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(source, "system", map[string]any{"SEED": "AB"})
	if err != nil {
		t.Fatal(err)
	}
	implicitDigest, err := implicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	explicitDigest, err := explicit.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	if implicitDigest != explicitDigest {
		t.Fatalf("String construction defaults/actuals changed model identity: %s != %s", implicitDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(implicitDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"value": "A"},
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
	seen := map[int64][]int64{}
	for _, event := range observed {
		seed, _ := event.Param("seed")
		wrapped, _ := event.Param("wrapped")
		arbitrary, _ := event.Param("arbitrary")
		length, _ := event.Param("length")
		if seed != "AB" || wrapped != "[AB]" {
			t.Fatalf("Observed seed=%#v wrapped=%#v", seed, wrapped)
		}
		codes, err := gorapide.CanonicalRapideStringCodes(arbitrary)
		if err != nil {
			t.Fatal(err)
		}
		seen[length.(int64)] = codes
	}
	if want := []int64{1<<63 - 1}; !reflect.DeepEqual(seen[1], want) {
		t.Fatalf("initial arbitrary codes=%v, want %v", seen[1], want)
	}
	if want := []int64{-1, 65, 1<<63 - 1}; !reflect.DeepEqual(seen[3], want) {
		t.Fatalf("reaction arbitrary codes=%v, want %v", seen[3], want)
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
		t.Fatal("String construction changed under argument spelling or GOMAXPROCS")
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
		t.Fatal("String construction replay changed canonical artifact bytes")
	}
}

func TestStringConstructionDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "missing argument", expression: `"A".Append()`, want: "unsupported closed behavior expression call"},
		{name: "extra argument", expression: `"A".Append('B', 'C')`, want: "unsupported closed behavior expression call"},
		{name: "wrong receiver", expression: `1.Append('A')`, want: "receiver has type Integer, want String"},
		{name: "wrong argument", expression: `"A".Append(1)`, want: "argument has type Integer, want Character"},
		{name: "wrong prepend argument", expression: `"A".Prepend("B")`, want: "argument has type String, want Character"},
		{name: "global append", expression: `Append('A')`, want: "unsupported closed behavior expression call"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("architecture System(S : String is " + test.expression + ") is end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}
