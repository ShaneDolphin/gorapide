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

func stringEscapeSource(spelling string) []byte {
	return []byte(`
type Driver is interface action out Set(value : String); end interface Driver;
type Worker is interface
  Label : String;
  action in Set(value : String);
  action out Values(
    generator_value : String; object_value : String;
    state_value : String; default_value : String;
    newline_value : String; backslash_value : String;
    quote_value : String; arbitrary_value : String
  );
  action out Changed(value : String);
  provides Echo : function(input : String is ` + spelling + `) return String;
end interface Worker;
module Store(Label_Arg : String is ` + spelling + `) return Worker is
  Label : String is ` + spelling + `;
  last : var String := ` + spelling + `;
  Echo : function(input : String) return String is
  begin
    return input;
  end function Echo;
initial
  last := Echo();
  Values(Label_Arg, Label, $last, ` + spelling + `, "line\nend", "slash\\end", "quote\'end", "\9223372036854775807");
serial
  when Set(` + spelling + `) where $last = ` + spelling + ` do
    last := "\9223372036854775807";
    Changed($last);
  end when;
end module Store;
architecture System(Label : String is ` + spelling + `) is
  driver : Driver;
  worker : Worker is Store(Label);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func TestStringEscapesAndArbitraryCharacterSequencesReplayCanonically(t *testing.T) {
	implicit, err := Compile(stringEscapeSource(`"Alpha"`), "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(stringEscapeSource(`"\65lpha"`), "system", map[string]any{
		"LABEL": "Alpha",
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
		t.Fatalf("equivalent direct/code String forms changed model identity: %s != %s", implicitDigest, explicitDigest)
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

	values := first.Poset.ByName("Values")
	if len(values) != 1 {
		t.Fatalf("Values events=%#v, want one", values)
	}
	for _, name := range []string{"generator_value", "object_value", "state_value", "default_value"} {
		value, _ := values[0].Param(name)
		if value != "Alpha" {
			t.Fatalf("Values.%s=%#v, want Alpha", name, value)
		}
	}
	for name, want := range map[string]string{
		"newline_value": "line\nend", "backslash_value": `slash\end`, "quote_value": "quote'end",
	} {
		value, _ := values[0].Param(name)
		if value != want {
			t.Fatalf("Values.%s=%#v, want %q", name, value, want)
		}
	}
	arbitrary, _ := values[0].Param("arbitrary_value")
	sequence, ok := arbitrary.(gorapide.RapideString)
	if !ok || !reflect.DeepEqual(sequence.Codes(), []int64{1<<63 - 1}) {
		t.Fatalf("arbitrary String=%#v", arbitrary)
	}
	changed := first.Poset.ByName("Changed")
	if len(changed) != 1 {
		t.Fatalf("Changed events=%#v, want one literal-filter match", changed)
	}
	changedValue, _ := changed[0].Param("value")
	changedSequence, ok := changedValue.(gorapide.RapideString)
	if !ok || !reflect.DeepEqual(changedSequence.Codes(), []int64{1<<63 - 1}) {
		t.Fatalf("Changed.value=%#v", changedValue)
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
		t.Fatal("String spelling, argument casing, or GOMAXPROCS changed artifact bytes")
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
		t.Fatal("String-sequence replay changed canonical artifact bytes")
	}
}

func TestStringEscapeDiagnosticsAreExplicit(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "unknown escape", value: `"a\t"`, want: "unsupported escape"},
		{name: "double quote escape", value: `"a\"b"`, want: "unsupported escape"},
		{name: "overflow", value: `"\9223372036854775808"`, want: "signed 64-bit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("architecture System(S : String is " + test.value + ") is end architecture System;")
			_, err := Compile(source, "System")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("diagnostic=%v, want %q", err, test.want)
			}
		})
	}
}
