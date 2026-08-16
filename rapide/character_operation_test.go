package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func characterOperationSource() []byte {
	return []byte(`
type Driver is interface action out Set(value : Character); end interface Driver;
type Worker is interface
  Marker : Character;
  action in Set(value : Character);
  action out Initial(
    generator_code : Integer; object_code : Integer;
    state_roundtrip : Character; default_character : Character;
    negative_character : Character
  );
  action out Converted(code : Integer; roundtrip : Character);
  provides Echo : function(
    value : Character is Code_To_Char(65);
    code : Integer is 'A'.Code()
  ) return Character;
end interface Worker;
module Store(Seed_Arg : Character is Code_To_Char(65)) return Worker is
  Marker : Character is Code_To_Char(65);
  last_code : var Integer := Seed_Arg.Code();
  last_character : var Character := Code_To_Char(0);
  Echo : function(value : Character; code : Integer) return Character is
  begin
    return Code_To_Char(code);
  end function Echo;
initial
  last_character := Echo();
  Initial(Seed_Arg.Code(), Marker.Code(), Code_To_Char($last_code), $last_character, Code_To_Char(-1));
serial
  when (?C : Character) Set(?C)
    where Code_To_Char(?C.Code()) = ?C do
    last_code := ?C.Code();
    Converted($last_code, Code_To_Char($last_code));
  end when;
end module Store;
architecture System(Seed : Character is Code_To_Char(65)) is
  driver : Driver;
  worker : Worker is Store(Seed);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func TestCharacterCodeOperationsExecuteAndReplayCanonically(t *testing.T) {
	source := characterOperationSource()
	implicit, err := Compile(source, "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(source, "system", map[string]any{
		"SEED": gorapide.RapideCharacterFromCode(65),
	})
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
		t.Fatalf("Character conversion defaults/actuals changed model identity: %s != %s", implicitDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(implicitDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set",
		Params: map[string]any{"value": gorapide.RapideCharacterFromCode(65)},
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

	initial := first.Poset.ByName("Initial")
	if len(initial) != 1 {
		t.Fatalf("Initial events=%#v", initial)
	}
	for _, name := range []string{"generator_code", "object_code"} {
		value, _ := initial[0].Param(name)
		if value != int64(65) {
			t.Fatalf("Initial.%s=%#v, want 65", name, value)
		}
	}
	for _, name := range []string{"state_roundtrip", "default_character"} {
		value, _ := initial[0].Param(name)
		if value.(gorapide.RapideCharacter).Code() != 65 {
			t.Fatalf("Initial.%s=%#v, want Character(65)", name, value)
		}
	}
	negative, _ := initial[0].Param("negative_character")
	if negative.(gorapide.RapideCharacter).Code() != -1 {
		t.Fatalf("Initial.negative_character=%#v", negative)
	}
	converted := first.Poset.ByName("Converted")
	if len(converted) != 1 {
		t.Fatalf("Converted events=%#v, want one guarded conversion", converted)
	}
	code, _ := converted[0].Param("code")
	roundTrip, _ := converted[0].Param("roundtrip")
	if code != int64(65) || roundTrip.(gorapide.RapideCharacter).Code() != 65 {
		t.Fatalf("Converted code=%#v roundtrip=%#v", code, roundTrip)
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
		t.Fatal("Character conversion changed under argument spelling or GOMAXPROCS")
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
		t.Fatal("Character conversion replay changed canonical artifact bytes")
	}
}

func TestCharacterCodeOperationDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name       string
		expression string
		want       string
	}{
		{name: "missing code argument", expression: `Code_To_Char()`, want: "unsupported closed behavior expression call"},
		{name: "extra code argument", expression: `Code_To_Char(1, 2)`, want: "unsupported closed behavior expression call"},
		{name: "wrong code type", expression: `Code_To_Char(1.0)`, want: "argument has type Float, want Integer"},
		{name: "member arity", expression: `'A'.Code(1)`, want: "unsupported closed behavior expression call"},
		{name: "wrong receiver", expression: `"A".Code()`, want: "receiver has type String, want Character"},
		{name: "unknown call", expression: `Host_Value(1)`, want: "unsupported closed behavior expression call"},
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
