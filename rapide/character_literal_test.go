package rapide

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func characterLiteralSource(spelling string) []byte {
	return []byte(`
type Driver is interface action out Set(value : Character); end interface Driver;
type Worker is interface
  Seed : Character;
  action in Set(value : Character);
  action out Values(
    generator_value : Character; object_value : Character;
    state_value : Character; default_value : Character;
    newline_value : Character; backslash_value : Character; quote_value : Character
  );
  action out Changed(value : Character; ordered : Boolean);
  provides Echo : function(input : Character is ` + spelling + `) return Character;
end interface Worker;
module Store(Seed_Arg : Character is ` + spelling + `) return Worker is
  Seed : Character is ` + spelling + `;
  last : var Character := ` + spelling + `;
  Echo : function(input : Character) return Character is
  begin
    return input;
  end function Echo;
initial
  last := Echo();
  Values(Seed_Arg, Seed, $last, ` + spelling + `, '\n', '\\', '\'');
serial
  when Set(` + spelling + `) where '\64' < $last do
    last := '\n';
    Changed($last, $last < ` + spelling + `);
  end when;
end module Store;
architecture System(Code : Character is ` + spelling + `) is
  driver : Driver;
  worker : Worker is Store(Code);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func TestCharacterLiteralsExecuteAndReplayCanonically(t *testing.T) {
	implicit, err := Compile(characterLiteralSource(`'A'`), "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(characterLiteralSource(`'\65'`), "system", map[string]any{
		"CODE": gorapide.RapideCharacterFromCode(65),
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
		t.Fatalf("equivalent Character forms changed model identity: %s != %s", implicitDigest, explicitDigest)
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

	values := first.Poset.ByName("Values")
	if len(values) != 1 {
		t.Fatalf("Values events=%#v, want one", values)
	}
	wantCodes := map[string]int64{
		"generator_value": 65, "object_value": 65, "state_value": 65, "default_value": 65,
		"newline_value": 10, "backslash_value": 92, "quote_value": 39,
	}
	for name, want := range wantCodes {
		value, ok := values[0].Param(name)
		character, typed := value.(gorapide.RapideCharacter)
		if !ok || !typed || character.Code() != want {
			t.Fatalf("Values.%s=%#v, want Character(%d)", name, value, want)
		}
	}
	changed := first.Poset.ByName("Changed")
	if len(changed) != 1 {
		t.Fatalf("Changed events=%#v, want one literal-filter match", changed)
	}
	changedValue, _ := changed[0].Param("value")
	ordered, _ := changed[0].Param("ordered")
	if changedValue.(gorapide.RapideCharacter).Code() != 10 || ordered != true {
		t.Fatalf("Changed value=%#v ordered=%#v", changedValue, ordered)
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
		t.Fatal("Character spelling, argument casing, or GOMAXPROCS changed artifact bytes")
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
		t.Fatal("Character replay changed canonical artifact bytes")
	}
}

func TestCharacterLiteralDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "empty", value: `''`, want: "direct character literal"},
		{name: "direct digit", value: `'1'`, want: "ASCII English letter"},
		{name: "multiple", value: `'Ab'`, want: "exactly one"},
		{name: "unknown escape", value: `'\t'`, want: "unsupported escape"},
		{name: "overflow", value: `'\9223372036854775808'`, want: "signed 64-bit"},
		{name: "unterminated", value: `'A`, want: "exactly one"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("architecture System(C : Character is " + test.value + ") is end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}
