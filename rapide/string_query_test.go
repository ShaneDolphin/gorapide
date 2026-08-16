package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide/arch"
)

func stringQuerySource() []byte {
	return []byte(`
type Driver is interface action out Set(value : String); end interface Driver;
type Worker is interface
  Count : Integer;
  action in Set(value : String);
  action out Observed(length : Integer; null : Boolean; arbitrary_length : Integer);
end interface Worker;
module Store(Seed_Arg : String is "Alpha") return Worker is
  Count : Integer is Seed_Arg.Length();
  last : var String := "";
initial
  Observed(Count, $last.Is_Null(), "\9223372036854775807".Length());
serial
  when (?S : String) Set(?S)
    where not ?S.Is_Null() and ?S.Length() = 5 do
    last := ?S;
    Observed($last.Length(), $last.Is_Null(), "\9223372036854775807".Length());
  end when;
end module Store;
architecture System(Seed : String is "Alpha") is
  driver : Driver;
  worker : Worker is Store(Seed);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func TestStringQueriesExecuteAndReplayCanonically(t *testing.T) {
	source := stringQuerySource()
	implicit, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(source, "system", map[string]any{"SEED": "Alpha"})
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
		t.Fatalf("String query defaults/actuals changed model identity: %s != %s", implicitDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(implicitDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"value": "Alpha"},
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
	seenNull := map[bool]bool{}
	for _, event := range observed {
		length, _ := event.Param("length")
		null, _ := event.Param("null")
		arbitraryLength, _ := event.Param("arbitrary_length")
		if length != int64(5) || arbitraryLength != int64(1) {
			t.Fatalf("Observed length=%#v arbitrary=%#v", length, arbitraryLength)
		}
		seenNull[null.(bool)] = true
	}
	if !seenNull[true] || !seenNull[false] {
		t.Fatalf("String Is_Null results=%#v, want both", seenNull)
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
		t.Fatal("String queries changed under argument spelling or GOMAXPROCS")
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
		t.Fatal("String query replay changed canonical artifact bytes")
	}
}

func TestStringQueryDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "length arity", expression: `"A".Length(1)`, want: "unsupported closed behavior expression call"},
		{name: "wrong receiver", expression: `1.Length()`, want: "receiver has type Integer, want String"},
		{name: "global null", expression: `Is_Null()`, want: "unsupported closed behavior expression call"},
		{name: "unknown member", expression: `"A".Host_Length()`, want: "unsupported closed behavior expression call"},
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
