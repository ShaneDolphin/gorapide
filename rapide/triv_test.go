package rapide

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/ShaneDolphin/gorapide"
	"github.com/ShaneDolphin/gorapide/arch"
)

func trivSource(unit string) []byte {
	return []byte(`
type Driver is interface action out Set(value : Triv); end interface Driver;
type Worker is interface
  Seed : Triv;
  action in Set(value : Triv);
  action out Values(generator_value : Triv; object_value : Triv; state_value : Triv;
    default_value : Triv; qualified_value : Triv; equal : Boolean);
  action out Changed(value : Triv; equal : Boolean);
  provides Echo : function(input : Triv is ` + unit + `) return Triv;
end interface Worker;
module Store(Seed_Arg : Triv is ` + unit + `) return Worker is
  Seed : Triv is ` + unit + `;
  last : var Triv := ` + unit + `;
  Echo : function(input : Triv) return Triv is
  begin
    return input;
  end function Echo;
initial
  last := Echo();
  Values(Seed_Arg, Seed, $last, ` + unit + `, Triv'(` + unit + `), ` + unit + ` = $last);
serial
  when (?T : Triv) Set(?T) where ?T = ` + unit + ` do
    last := ?T;
    Changed($last, $last = ` + unit + `);
  end when;
end module Store;
architecture System(Token : Triv is ` + unit + `) is
  driver : Driver;
  worker : Worker is Store(Token);
connect
  driver.Set => worker.Set;
end architecture System;
`)
}

func TestTrivUnitExecutesAndReplaysCanonically(t *testing.T) {
	implicit, err := Compile(trivSource("Unit"), "System")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := CompileWithArguments(trivSource("unit"), "system", map[string]any{
		"TOKEN": gorapide.RapideUnit(),
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
		t.Fatalf("Unit case or explicit actual changed model identity: %s != %s", implicitDigest, explicitDigest)
	}
	journal := arch.NewExecutionJournal(implicitDigest, 20, arch.InputEvent{
		Key: "set", Source: "driver", Action: "Set",
		Params: map[string]any{"value": gorapide.RapideUnit()},
	})
	journalBytes, err := journal.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	journal, err = arch.ParseExecutionJournal(journalBytes)
	if err != nil {
		t.Fatal(err)
	}

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
	for _, name := range []string{"generator_value", "object_value", "state_value", "default_value", "qualified_value"} {
		value, ok := values[0].Param(name)
		if unit, typed := value.(gorapide.RapideTriv); !ok || !typed || unit != gorapide.RapideUnit() {
			t.Fatalf("Values.%s=%#v, want Unit", name, value)
		}
	}
	if equal, ok := values[0].Param("equal"); !ok || equal != true {
		t.Fatalf("Values.equal=%#v, want true", equal)
	}
	changed := first.Poset.ByName("Changed")
	if len(changed) != 1 {
		t.Fatalf("Changed events=%#v, want one Triv-filter match", changed)
	}
	if value, _ := changed[0].Param("value"); value != gorapide.RapideUnit() {
		t.Fatalf("Changed.value=%#v, want Unit", value)
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
		t.Fatal("Unit spelling, argument casing, or GOMAXPROCS changed artifact bytes")
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
		t.Fatal("Unit replay changed canonical artifact bytes")
	}
}

func TestTrivRejectsNonUnitValues(t *testing.T) {
	for _, value := range []any{nil, false, "Unit", int64(0)} {
		_, err := CompileWithArguments(trivSource("Unit"), "System", map[string]any{"Token": value})
		if err == nil {
			t.Fatalf("Triv architecture actual %#v unexpectedly compiled", value)
		}
	}
}
