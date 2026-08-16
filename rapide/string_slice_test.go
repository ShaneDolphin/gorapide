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

func stringSliceSource() []byte {
	return []byte(`
type Driver is interface action out Set(value : String); end interface Driver;
type Worker is interface
  Result : String;
  action in Set(value : String);
  action out Observed(
    method_value : String; bracket_value : String; state_value : String;
    empty_value : String; arbitrary : String; length : Integer
  );
end interface Worker;
module Store(Seed_Arg : String is "Charlie") return Worker is
  Result : String is Seed_Arg[1..4];
  last : var String := "\9223372036854775807AB\55296";
initial
  Observed(Seed_Arg.Slice(1, 4), Seed_Arg[5..7], $last[2..3],
    Seed_Arg[2..1], $last[1..4], Seed_Arg[2..Seed_Arg.Length()].Length());
serial
  when (?S : String) Set(?S)
    where ?S[1..2] = "Ch" and ?S.Slice(3, 4) = "ar" and
      ?S[?S.Length() + 1..?S.Length()].Is_Null() do
    last := ?S[2..4];
    Observed(?S.Slice(1, 4), ?S[5..7], $last[1..3],
      ?S[2..1], "\9223372036854775807AB\55296"[1..4], ?S[2..?S.Length()].Length());
  end when;
end module Store;
architecture System(Seed : String is "Charlie") is
  driver : Driver;
  worker : Worker is Store(Seed);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func TestStringSliceSyntaxesExecuteAndReplayCanonically(t *testing.T) {
	source := stringSliceSource()
	bracket, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	methodSource := bytes.ReplaceAll(source, []byte("Seed_Arg[1..4]"), []byte("Seed_Arg.Slice(1, 4)"))
	method, err := Compile(methodSource, "system")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(source, "system", map[string]any{"SEED": "Charlie"})
	if err != nil {
		t.Fatal(err)
	}
	modelDigest, err := bracket.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*arch.Architecture{method, explicit} {
		digest, err := candidate.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if digest != modelDigest {
			t.Fatalf("String Slice syntax/default changed model identity: %s != %s", digest, modelDigest)
		}
	}
	journal := arch.NewExecutionJournal(modelDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"value": "Charlie"},
	})

	previous := runtime.GOMAXPROCS(1)
	first, err := bracket.ExecuteDeterministic(journal)
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
	seenState := map[string]bool{}
	wantArbitrary := []int64{1<<63 - 1, 65, 66, 55296}
	for _, event := range observed {
		methodValue, _ := event.Param("method_value")
		bracketValue, _ := event.Param("bracket_value")
		stateValue, _ := event.Param("state_value")
		emptyValue, _ := event.Param("empty_value")
		arbitrary, _ := event.Param("arbitrary")
		length, _ := event.Param("length")
		if methodValue != "Char" || bracketValue != "lie" || emptyValue != "" || length != int64(6) {
			t.Fatalf("Observed method=%#v bracket=%#v empty=%#v length=%#v", methodValue, bracketValue, emptyValue, length)
		}
		seenState[stateValue.(string)] = true
		codes, err := gorapide.CanonicalRapideStringCodes(arbitrary)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(codes, wantArbitrary) {
			t.Fatalf("Observed arbitrary codes=%v, want %v", codes, wantArbitrary)
		}
	}
	if !seenState["AB"] || !seenState["har"] {
		t.Fatalf("String Slice state values=%v, want AB and har", seenState)
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
		t.Fatal("String Slice changed under equivalent syntax or GOMAXPROCS")
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
		t.Fatal("String Slice replay changed canonical artifact bytes")
	}
}

func TestStringSliceFailuresAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "wrong receiver", expression: `1[1..1]`, want: "receiver has type Integer, want String"},
		{name: "wrong lower", expression: `"A"[True..1]`, want: "bound 1 has type Boolean, want Integer"},
		{name: "wrong upper", expression: `"A"[1..False]`, want: "bound 2 has type Boolean, want Integer"},
		{name: "method missing bounds", expression: `"A".Slice()`, want: "unsupported closed behavior expression call"},
		{name: "method one bound", expression: `"A".Slice(1)`, want: "unsupported closed behavior expression call"},
		{name: "method extra bound", expression: `"A".Slice(1, 1, 1)`, want: "unsupported closed behavior expression call"},
		{name: "static lower bound", expression: `"A"[0..1]`, want: "outside length 1"},
		{name: "static upper bound", expression: `"A"[1..2]`, want: "outside length 1"},
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

func TestStringSliceRuntimeBoundsFailureIsDeterministic(t *testing.T) {
	model, err := Compile(stringSliceSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 20, arch.InputEvent{
		Key: "short", Source: "driver", Action: "Set", Params: map[string]any{"value": "Ch"},
	})
	for _, processors := range []int{1, 8} {
		previous := runtime.GOMAXPROCS(processors)
		_, err := model.ExecuteDeterministic(journal)
		runtime.GOMAXPROCS(previous)
		if err == nil || !strings.Contains(err.Error(), "String slice 3..4 is outside length 2") {
			t.Fatalf("GOMAXPROCS=%d bounds error=%v", processors, err)
		}
	}
}
