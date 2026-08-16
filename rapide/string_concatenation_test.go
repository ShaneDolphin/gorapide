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

func stringConcatenationSource() []byte {
	return []byte(`
type Driver is interface action out Set(value : String); end interface Driver;
type Worker is interface
  Result : String;
  action in Set(value : String);
  action out Observed(
    base : String; surrounded : String; appended : String;
    prepended : String; arbitrary : String; length : Integer
  );
end interface Worker;
module Store(Seed_Arg : String is "A" & 'B') return Worker is
  Result : String is "[" & Seed_Arg & "]";
  last : var String := "";
initial
  last := '\9223372036854775807' & Seed_Arg & '\55296';
  Observed(Seed_Arg, Result, Seed_Arg & 'C', 'X' & Seed_Arg, $last, $last.Length());
serial
  when (?S : String) Set(?S)
    where ?S & 'B' = "AB" and 'X' & ?S = "XA" and (?S & "B").Length() = 2 do
    last := '\9223372036854775807' & ?S & '\55296';
    Observed(?S, "[" & ?S & "]", ?S & 'C', 'X' & ?S, $last, $last.Length());
  end when;
end module Store;
architecture System(Seed : String is "A" & 'B') is
  driver : Driver;
  worker : Worker is Store(Seed);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func TestStringConcatenationOverloadsExecuteAndReplayCanonically(t *testing.T) {
	source := stringConcatenationSource()
	implicit, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	methodSource := bytes.ReplaceAll(source, []byte(`"A" & 'B'`), []byte(`"A".Append('B')`))
	method, err := Compile(methodSource, "system")
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
	for _, candidate := range []struct {
		name  string
		model *arch.Architecture
	}{{name: "method", model: method}, {name: "explicit", model: explicit}} {
		digest, err := candidate.model.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if digest != implicitDigest {
			t.Fatalf("%s String spelling changed model identity: %s != %s", candidate.name, digest, implicitDigest)
		}
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
	second, err := method.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	observed := first.Poset.ByName("Observed")
	if len(observed) != 2 {
		t.Fatalf("Observed events=%#v, want initial and guarded reaction", observed)
	}
	seen := map[string][]int64{}
	for _, event := range observed {
		baseValue, _ := event.Param("base")
		base := baseValue.(string)
		surrounded, _ := event.Param("surrounded")
		appended, _ := event.Param("appended")
		prepended, _ := event.Param("prepended")
		arbitrary, _ := event.Param("arbitrary")
		length, _ := event.Param("length")
		if surrounded != "["+base+"]" || appended != base+"C" || prepended != "X"+base {
			t.Fatalf("Observed base=%q surrounded=%#v appended=%#v prepended=%#v", base, surrounded, appended, prepended)
		}
		codes, err := gorapide.CanonicalRapideStringCodes(arbitrary)
		if err != nil {
			t.Fatal(err)
		}
		if length != int64(len([]rune(base))+2) {
			t.Fatalf("Observed base=%q length=%#v", base, length)
		}
		seen[base] = codes
	}
	if want := []int64{1<<63 - 1, 65, 66, 55296}; !reflect.DeepEqual(seen["AB"], want) {
		t.Fatalf("initial arbitrary codes=%v, want %v", seen["AB"], want)
	}
	if want := []int64{1<<63 - 1, 65, 55296}; !reflect.DeepEqual(seen["A"], want) {
		t.Fatalf("reaction arbitrary codes=%v, want %v", seen["A"], want)
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
		t.Fatal("String concatenation changed under equivalent spelling or GOMAXPROCS")
	}
	artifactDigest, err := first.ArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := explicit.ReplayDeterministic(journal, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	replayedBytes, err := replayed.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, replayedBytes) {
		t.Fatal("String concatenation replay changed canonical artifact bytes")
	}
}

func TestStringConcatenationDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "integer left", expression: `1 & "A"`, want: `operator "&" is not defined for Integer and String`},
		{name: "integer right", expression: `"A" & 1`, want: `operator "&" is not defined for String and Integer`},
		{name: "two characters", expression: `'A' & 'B'`, want: `operator "&" is not defined for Character and Character`},
		{name: "boolean pair", expression: `True & False`, want: `operator "&" is not defined for Boolean and Boolean`},
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
