package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func stringIndexSource() []byte {
	return []byte(`
type Driver is interface action out Set(value : String); end interface Driver;
type Worker is interface
  Marker : Character;
  action in Set(value : String);
  action out Observed(
    special : Character; direct : Character; state_value : Character;
    arbitrary : Character; code : Integer
  );
end interface Worker;
module Store(Seed_Arg : String is "AB") return Worker is
  Marker : Character is Seed_Arg[2];
  last : var String := "\9223372036854775807";
initial
  Observed(Seed_Arg[1], Seed_Arg.[](2), $last[1], "\9223372036854775807"[1], Seed_Arg[2].Code());
serial
  when (?S : String) Set(?S)
    where ?S[2] = 'B' and ?S.[](1) = 'A' and ?S[2].Code() = 66 do
    last := ?S;
    Observed(?S[1], ?S.[](2), $last[2], "\9223372036854775807"[1], $last[2].Code());
  end when;
end module Store;
architecture System(Seed : String is "AB") is
  driver : Driver;
  worker : Worker is Store(Seed);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func TestStringIndexSyntaxesExecuteAndReplayCanonically(t *testing.T) {
	source := stringIndexSource()
	special, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	directSource := bytes.ReplaceAll(source, []byte("Seed_Arg[2]"), []byte("Seed_Arg.[](2)"))
	direct, err := Compile(directSource, "system")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(source, "system", map[string]any{"SEED": "AB"})
	if err != nil {
		t.Fatal(err)
	}
	modelDigest, err := special.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*arch.Architecture{direct, explicit} {
		digest, err := candidate.DeterministicModelDigest()
		if err != nil {
			t.Fatal(err)
		}
		if digest != modelDigest {
			t.Fatalf("String index spelling/default changed model identity: %s != %s", digest, modelDigest)
		}
	}
	journal := arch.NewExecutionJournal(modelDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set", Params: map[string]any{"value": "AB"},
	})

	previous := runtime.GOMAXPROCS(1)
	first, err := special.ExecuteDeterministic(journal)
	if err != nil {
		runtime.GOMAXPROCS(previous)
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(8)
	second, err := direct.ExecuteDeterministic(journal)
	runtime.GOMAXPROCS(previous)
	if err != nil {
		t.Fatal(err)
	}

	observed := first.Poset.ByName("Observed")
	if len(observed) != 2 {
		t.Fatalf("Observed events=%#v, want initial and guarded reaction", observed)
	}
	seenState := map[int64]bool{}
	for _, event := range observed {
		for _, expected := range []struct {
			name string
			code int64
		}{{name: "special", code: 65}, {name: "direct", code: 66}, {name: "arbitrary", code: 1<<63 - 1}} {
			value, _ := event.Param(expected.name)
			if value.(gorapide.RapideCharacter).Code() != expected.code {
				t.Fatalf("Observed.%s=%#v, want Character(%d)", expected.name, value, expected.code)
			}
		}
		stateValue, _ := event.Param("state_value")
		seenState[stateValue.(gorapide.RapideCharacter).Code()] = true
		code, _ := event.Param("code")
		if code != int64(66) {
			t.Fatalf("Observed.code=%#v, want 66", code)
		}
	}
	if !seenState[1<<63-1] || !seenState[66] {
		t.Fatalf("indexed state values=%v, want maximum code and 66", seenState)
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
		t.Fatal("String indexing changed under equivalent syntax or GOMAXPROCS")
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
		t.Fatal("String index replay changed canonical artifact bytes")
	}
}

func TestStringIndexFailuresAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "wrong receiver", expression: `1[1]`, want: "receiver has type Integer, want String"},
		{name: "wrong index", expression: `"A"[True]`, want: "index has type Boolean, want Integer"},
		{name: "direct missing index", expression: `"A".[]()`, want: "unsupported closed behavior expression call"},
		{name: "direct extra index", expression: `"A".[](1, 1)`, want: "unsupported closed behavior expression call"},
		{name: "static lower bound", expression: `"A"[0]`, want: "outside 1..1"},
		{name: "static upper bound", expression: `"A"[2]`, want: "outside 1..1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("architecture System(C : Character is " + test.expression + ") is end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}

func TestStringIndexRuntimeBoundsFailureIsDeterministic(t *testing.T) {
	model, err := Compile(stringIndexSource(), "System")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := model.DeterministicModelDigest()
	if err != nil {
		t.Fatal(err)
	}
	journal := arch.NewExecutionJournal(digest, 20, arch.InputEvent{
		Key: "short", Source: "driver", Action: "Set", Params: map[string]any{"value": "A"},
	})
	for _, processors := range []int{1, 8} {
		previous := runtime.GOMAXPROCS(processors)
		_, err := model.ExecuteDeterministic(journal)
		runtime.GOMAXPROCS(previous)
		if err == nil || !strings.Contains(err.Error(), "String index 2 is outside 1..1") {
			t.Fatalf("GOMAXPROCS=%d bounds error=%v", processors, err)
		}
	}
}
